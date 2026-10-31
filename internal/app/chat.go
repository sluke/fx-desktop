package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fx-desktop/internal/acp"
	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/sessionstats"
)

// queuedPrompt is one message waiting for the running turn to end. The text
// and the files it named travel together: the file contents were read when
// the message was sent, and must not be looked up again under a different
// project state.
type queuedPrompt struct {
	text     string
	mentions []promptMention
}

// chatSession is independent of frame state. Workers publish snapshots; rendering
// never waits for a process, network, approval, or mutex held over I/O.
type chatSession struct {
	closedDone chan struct{}
	notices    []string
	mu         sync.Mutex
	client     *acp.Client
	cancel     context.CancelFunc
	ctx        context.Context
	project    string
	executable string
	// args are the flags fx acp itself accepts. globalArgs are the fx launch
	// flags that must precede the `acp` subcommand (context-limit, add-dir and
	// no-additional-dirs); app-only flags such as effort and fast are applied
	// through session/set_config_option after the session opens.
	args       []string
	globalArgs []string
	resume     string
	sessionID  string
	messages   []chrome.ChatMessage
	byID       map[string]int
	// viewMessages is the transcript the window renders. It is kept in sync
	// with messages incrementally: copying a session-sized transcript on every
	// frame is O(messages) allocation just to read one screen of it
	// (§4.10.6). snapshotForView owns this slice and may reuse its storage;
	// snapshot() still returns an independent copy for callers that keep it.
	viewMessages []chrome.ChatMessage
	viewDirty    map[int]struct{}
	// streams holds the builder a message is still arriving into. Appending
	// a chunk to a string copies everything already received, so a long
	// reply cost time quadratic in its length (§4.10.2). The builder grows
	// instead; Text is a view of the same bytes.
	streams                                    map[int]*strings.Builder
	next                                       uint64
	ready, busy, stopping, closed, configuring bool
	// steering mirrors fx's advertised agentCapabilities._meta.fx.steering.
	// Without it the app never pretends a running turn can be interrupted
	// with a message (§8 step 7).
	steering bool
	// images mirrors fx's advertised promptCapabilities.image. Without it
	// the composer offers no way to attach a picture, because fx would
	// refuse one (§8 step 8).
	images bool
	// files mirrors fx's advertised promptCapabilities.embeddedContext.
	// Without it a message that names a file with @ is sent as the text it
	// is: fx would take no content block, and a mention that carried nothing
	// would be worse than the words alone (§12.3.3).
	files bool
	// queued holds the messages typed while a turn was running on an fx that
	// cannot steer. They are delivered in order as the turn ends, so no
	// message that is already in the transcript is silently dropped, with the
	// files each named, which are read from the project when it is delivered.
	queued []queuedPrompt
	// contextUsed/contextTotal mirror fx's usage_update: provider-confirmed
	// context consumption and the window size, both 0 until fx knows.
	contextUsed, contextTotal int
	// inputTokens/outputTokens accumulate the usage reported by each
	// completed session/prompt result. That result is the only token count
	// every provider reports: `fx usage` reads AI Gateway alone. They are
	// the conversation's processed tokens, not the window's occupancy.
	inputTokens, outputTokens int
	// stats is the app's per-conversation usage record. It is attached by
	// the window that owns this session and stays nil in tests and in
	// command sessions, where nothing should be written.
	stats *sessionstats.Store
	// configs and modes are the session's live configuration, taken from
	// fx's answer to session/new and refreshed by every set_config_option.
	configs []chrome.ConfigOption
	modes   []chrome.ModeOption
	// effort, fast and ultra are accepted by fx through the same
	// session/set_config_option method even though session/new does not list
	// them. They are model-dependent; an unsupported value is reported on the
	// settings panel without ending the conversation.
	effort          string
	fast, ultra     bool
	initialEffort   string
	initialFast     *bool
	initialProvider string
	advancedProblem string
	// title is the conversation name fx generates. It arrives after the
	// first turn as session_info_update, so it is empty until then.
	title                string
	problem, model, mode string
	permission           *chrome.ChatPermission
	changed              func()
}

// configOptionWire is one entry of fx's configOptions. The shell re-renders
// whatever fx lists, so a new fx option needs no code change.
type configOptionWire struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	Type         string `json:"type"`
	CurrentValue string `json:"currentValue"`
	Options      []struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	} `json:"options"`
}

func toConfigOptions(entries []configOptionWire) []chrome.ConfigOption {
	out := make([]chrome.ConfigOption, 0, len(entries))
	for _, e := range entries {
		option := chrome.ConfigOption{ID: e.ID, Name: e.Name, Category: e.Category, Type: e.Type, CurrentValue: e.CurrentValue}
		for _, choice := range e.Options {
			option.Options = append(option.Options, chrome.ConfigChoice{Value: choice.Value, Name: choice.Name})
		}
		out = append(out, option)
	}
	return out
}

func findConfigOption(options []chrome.ConfigOption, id string) (chrome.ConfigOption, bool) {
	for _, o := range options {
		if o.ID == id {
			return o, true
		}
	}
	return chrome.ConfigOption{}, false
}

type chatSnapshot struct {
	Notices                               []string
	Messages                              []chrome.ChatMessage
	Ready, Busy, Stopping, ConfigChanging bool
	// Steering is fx's capability; Queued is how many messages are waiting
	// for the running turn to end.
	Steering bool
	Queued   int
	// Images is fx's promptCapabilities.image: whether the composer may
	// offer to attach pictures at all.
	Images bool
	// Files is fx's promptCapabilities.embeddedContext: whether a message may
	// name a file with @ and have fx read it.
	Files bool
	// ContextUsed/ContextTotal come from usage_update; InputTokens and
	// OutputTokens are the conversation's accumulated prompt usage.
	ContextUsed, ContextTotal int
	InputTokens, OutputTokens int
	// Configs, Modes and Mode are what fx says this session may be set to.
	Configs []chrome.ConfigOption
	Modes   []chrome.ModeOption
	Mode    string
	// SessionTitle is fx's generated conversation name, empty until the
	// first turn names it.
	SessionTitle              string
	Problem, Model, SessionID string
	// Effort/Fast/Ultra are the model-dependent settings fx accepts through
	// set_config_option. AdvancedProblem is a rejected value, not a dead
	// session.
	Effort, AdvancedProblem string
	Fast, Ultra             bool
	Permission              *chrome.ChatPermission
}

func newChatSession(executable, project string, args []string, changed func()) *chatSession {
	ctx, cancel := context.WithCancel(context.Background())
	c := &chatSession{closedDone: make(chan struct{}), ctx: ctx, cancel: cancel, executable: executable, project: project, byID: make(map[string]int), changed: changed, effort: "auto"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--sessions-v2":
			// fx 0.0.15 removed the experimental store flag. Drop it rather
			// than passing it to `fx acp`, where it is now unknown.
		case "--no-additional-dirs":
			// fx global flag, not an fx acp flag. Keeping it in a separate
			// slice lets acp.Start put it before the subcommand.
			c.globalArgs = append(c.globalArgs, args[i])
		case "--context-limit", "--add-dir":
			// Context limits and additional directories are global too. They
			// must precede `acp`; fx rejects them after the subcommand.
			if i+1 < len(args) {
				i++
				c.globalArgs = append(c.globalArgs, args[i-1], args[i])
			}
		case "--ultrafast":
			c.args = append(c.args, args[i])
			c.ultra = true
		case "--no-ultrafast":
			c.args = append(c.args, args[i])
			c.ultra = false
		case "--log-file":
			if i+1 < len(args) {
				i++
				c.args = append(c.args, "--log-file", args[i])
			}
		case "--model":
			if i+1 < len(args) {
				i++
				c.args = append(c.args, "--model", args[i])
				c.model = args[i]
			}
		case "--provider":
			// fx rejects --provider after `acp`; apply it through the
			// session's provider config once the session exists.
			if i+1 < len(args) {
				i++
				c.initialProvider = strings.TrimSpace(args[i])
			}
		case "--effort":
			if i+1 < len(args) {
				i++
				c.initialEffort = strings.TrimSpace(args[i])
				if c.initialEffort != "" {
					c.effort = c.initialEffort
				}
			}
		case "--fast":
			value := true
			c.initialFast = &value
			c.fast = true
		case "--no-fast":
			value := false
			c.initialFast = &value
			c.fast = false
		case "--resume":
			if i+1 < len(args) {
				i++
				c.resume = args[i]
			}
		case "-c", "--continue":
			c.resume = "last"
		}
	}
	return c
}

// canTakeImages reports fx's promptCapabilities.image. It is read while
// building a window's actions, outside the per-frame snapshot, so it takes the
// lock and answers the one question the palette asks.
func (c *chatSession) canTakeImages() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.images && c.ready
}

// snapshot returns the shell's view of the session.
// chatStatus is the chat's state without its transcript. The rail, the agent
// strip and the sidebar read it every frame; copying the message slice for
// them made a long conversation pay for itself several times a frame
// (§4.10.4).
type chatStatus struct {
	Ready, Busy, Stopping, ConfigChanging bool
	// Permission says a turn is waiting on an approval; the request itself
	// belongs to the transcript view.
	Permission                              bool
	Steering, Images, Files                 bool
	Queued                                  int
	ContextUsed, ContextTotal, MessageCount int
	InputTokens, OutputTokens               int
	Problem, Model, Mode, SessionID         string
	SessionTitle                            string
}

func (c *chatSession) statusLocked() chatStatus {
	return chatStatus{
		Ready:          c.ready,
		Busy:           c.busy,
		Stopping:       c.stopping,
		ConfigChanging: c.configuring,
		Permission:     c.permission != nil,
		Steering:       c.steering,
		Images:         c.images,
		Files:          c.files,
		Queued:         len(c.queued),
		ContextUsed:    c.contextUsed,
		ContextTotal:   c.contextTotal,
		InputTokens:    c.inputTokens,
		OutputTokens:   c.outputTokens,
		MessageCount:   len(c.messages),
		Problem:        c.problem,
		Model:          c.model,
		Mode:           c.mode,
		SessionID:      c.sessionID,
		SessionTitle:   c.title,
	}
}

// status is the per-frame read: no message slice is copied.
func (c *chatSession) status() chatStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

// restoreStats seeds this connection from the last usage the app recorded for
// id. fx replays no usage_update on session/load, so without this a resumed
// conversation would read as an empty one until its next turn ends. Live
// values always win: the guards keep a reconnect from replacing measurements
// with an older record.
func (c *chatSession) restoreStats(id string) {
	if c.stats == nil || id == "" {
		return
	}
	st := c.stats.Get(id)
	if st.Empty() {
		return
	}
	c.mu.Lock()
	if c.contextUsed == 0 && c.contextTotal == 0 {
		c.contextUsed, c.contextTotal = st.ContextUsed, st.ContextTotal
	}
	if c.inputTokens == 0 && c.outputTokens == 0 {
		c.inputTokens, c.outputTokens = st.InputTokens, st.OutputTokens
	}
	c.mu.Unlock()
}

// saveStats records the conversation's current counters. It must be called
// without c.mu held: the store writes a file, and the app loop must never
// wait for that.
func (c *chatSession) saveStats() {
	if c.stats == nil {
		return
	}
	c.mu.Lock()
	id := c.sessionID
	st := sessionstats.Stats{
		ContextUsed:  c.contextUsed,
		ContextTotal: c.contextTotal,
		InputTokens:  c.inputTokens,
		OutputTokens: c.outputTokens,
	}
	c.mu.Unlock()
	if id == "" || st.Empty() {
		return
	}
	_ = c.stats.Put(id, st)
}

func (c *chatSession) snapshot() chatSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked(append([]chrome.ChatMessage(nil), c.messages...))
}

// snapshotForView is the per-frame read. It returns a transcript owned by the
// session and updates only the messages that changed since the last frame,
// rather than allocating one copy of the whole conversation per frame. The
// caller must finish with it before the next call; the native UI renders the
// slice synchronously, and the only other caller is the test harness.
func (c *chatSession) snapshotForView() chatSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.syncViewLocked()
	return c.snapshotLocked(c.viewMessages)
}

func (c *chatSession) snapshotLocked(messages []chrome.ChatMessage) chatSnapshot {
	st := c.statusLocked()
	s := chatSnapshot{
		Ready: st.Ready, Busy: st.Busy, Stopping: st.Stopping, ConfigChanging: st.ConfigChanging,
		Problem: st.Problem, Model: st.Model, Mode: st.Mode, SessionID: st.SessionID,
		Steering: st.Steering, Images: st.Images, Files: st.Files, Queued: st.Queued,
		ContextUsed: st.ContextUsed, ContextTotal: st.ContextTotal,
		InputTokens: st.InputTokens, OutputTokens: st.OutputTokens,
		Configs: append([]chrome.ConfigOption(nil), c.configs...), Modes: append([]chrome.ModeOption(nil), c.modes...),
		SessionTitle: st.SessionTitle,
		Effort:       c.effort, Fast: c.fast, Ultra: c.ultra,
		AdvancedProblem: c.advancedProblem,
		Messages:        messages,
	}
	s.Notices = append([]string(nil), c.notices...)
	if c.permission != nil {
		p := *c.permission
		p.Options = append([]chrome.PermissionOption(nil), p.Options...)
		s.Permission = &p
	}
	return s
}

// syncViewLocked brings the rendered transcript up to date. Appending to the
// view is cheap, and only the dirty message values are copied; a changed
// message is one whose fields were written since the last frame.
func (c *chatSession) syncViewLocked() {
	if len(c.viewMessages) != len(c.messages) {
		if len(c.viewMessages) > len(c.messages) {
			c.viewMessages = c.viewMessages[:len(c.messages)]
		}
		c.viewMessages = append(c.viewMessages, c.messages[len(c.viewMessages):]...)
	}
	for index := range c.viewDirty {
		if index >= 0 && index < len(c.messages) {
			c.viewMessages[index] = c.messages[index]
		}
	}
	clear(c.viewDirty)
}

func (c *chatSession) markMessageDirtyLocked(index int) {
	if c.viewDirty == nil {
		c.viewDirty = make(map[int]struct{}, 4)
	}
	c.viewDirty[index] = struct{}{}
}

// finishMessagesLocked marks every live row done. It marks only the rows that
// changed, so ending a turn does not copy a long transcript into the view.
func (c *chatSession) finishMessagesLocked() {
	for i := range c.messages {
		if c.messages[i].Done {
			continue
		}
		c.messages[i].Done = true
		c.markMessageDirtyLocked(i)
	}
}
func (c *chatSession) invalidate() {
	if c.changed != nil {
		c.changed()
	}
}
func (c *chatSession) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.problem = err.Error()
	c.busy = false
	c.configuring = false
	c.stopping = false
	c.permission = nil
	c.finishMessagesLocked()
	c.mu.Unlock()
	c.invalidate()
}
func (c *chatSession) connect() {
	client, err := acp.StartWithGlobals(c.ctx, c.executable, c.project, c.globalArgs, c.args, c.event)
	if err != nil {
		c.fail(fmt.Errorf("Could not start fx: %w", err))
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		client.Close()
		return
	}
	c.client = client
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
	defer cancel()
	var init struct {
		ProtocolVersion int `json:"protocolVersion"`
		// fx advertises extras under agentCapabilities._meta.fx. Steering
		// is the only one the shell acts on: everything else it advertises
		// goes unclaimed rather than half-implemented.
		AgentCapabilities struct {
			PromptCapabilities struct {
				// Image gates the composer's attach affordances: a session
				// whose fx cannot take images must not offer to.
				Image bool `json:"image"`
				// EmbeddedContext gates @ mentions: it is fx saying it takes
				// a file's content in the prompt itself.
				EmbeddedContext bool `json:"embeddedContext"`
			} `json:"promptCapabilities"`
			Meta struct {
				FX struct {
					Steering bool `json:"steering"`
				} `json:"fx"`
			} `json:"_meta"`
		} `json:"agentCapabilities"`
	}
	if err = client.Call(ctx, "initialize", map[string]any{"protocolVersion": 1, "clientInfo": map[string]string{"name": "fx-desktop", "version": "0.1.0"}, "clientCapabilities": map[string]any{"session": map[string]any{"notices": map[string]any{}}}, "_meta": map[string]any{"fx": map[string]bool{"terminal": false}}}, &init); err != nil {
		client.Close()
		c.fail(err)
		return
	}
	if init.ProtocolVersion != 1 {
		client.Close()
		c.fail(errors.New("This fx ACP version is not supported"))
		return
	}
	resume := c.resume
	if resume == "last" {
		saved, err := fxSessions(ctx, c.executable, c.project)
		if err != nil {
			client.Close()
			c.fail(err)
			return
		}
		resume = ""
		var latest int64
		for _, s := range saved {
			if resume == "" || s.UpdatedAtMS > latest {
				resume = s.ID
				latest = s.UpdatedAtMS
			}
		}
	}
	// fx does not inherit the servers in ~/.fx/mcp.json unless the client
	// asks: the docs say profile servers "are not inherited unless the
	// client sets _meta.fx.profileMcpServers: true". A desktop client is a
	// full client, so it asks — otherwise the servers the user configured
	// for fx silently vanish here. Client entries still combine with the
	// project's approved .mcp.json servers; a client entry wins a same-name
	// project entry, and mcpServers:[] claims none, so nothing is shadowed.
	params := map[string]any{
		"cwd":        c.project,
		"mcpServers": []any{},
		"_meta":      map[string]any{"fx": map[string]bool{"profileMcpServers": true}},
	}
	method := "session/new"
	if resume != "" {
		method = "session/load"
		params["sessionId"] = resume
		c.mu.Lock()
		c.sessionID = resume
		c.mu.Unlock()
		// fx sends no usage_update when it loads a conversation, so the
		// last numbers we saw are the only ones available until this
		// session finishes another turn.
		c.restoreStats(resume)
	}
	var result struct {
		SessionID     string             `json:"sessionId"`
		ConfigOptions []configOptionWire `json:"configOptions"`
		Modes         struct {
			Current        string `json:"currentModeId"`
			AvailableModes []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"availableModes"`
		} `json:"modes"`
	}
	if err = client.Call(ctx, method, params, &result); err != nil {
		client.Close()
		c.fail(err)
		return
	}
	if result.SessionID == "" {
		result.SessionID = resume
	}
	if result.SessionID == "" {
		client.Close()
		c.fail(errors.New("fx did not return a conversation ID"))
		return
	}
	c.mu.Lock()
	c.sessionID = result.SessionID
	c.ready = true
	c.mode = result.Modes.Current
	c.steering = init.AgentCapabilities.Meta.FX.Steering
	c.images = init.AgentCapabilities.PromptCapabilities.Image
	c.files = init.AgentCapabilities.PromptCapabilities.EmbeddedContext
	c.configs = toConfigOptions(result.ConfigOptions)
	c.modes = make([]chrome.ModeOption, 0, len(result.Modes.AvailableModes))
	for _, m := range result.Modes.AvailableModes {
		c.modes = append(c.modes, chrome.ModeOption{ID: m.ID, Name: m.Name, Description: m.Description})
	}
	if option, ok := findConfigOption(c.configs, "model"); ok {
		c.model = option.CurrentValue
	}
	c.finishMessagesLocked()
	c.mu.Unlock()
	c.invalidate()
	c.applyInitialAdvanced(client)
	<-client.Done()
	c.mu.Lock()
	c.ready = false
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		c.fail(errors.New("The fx connection ended. Reopen this project to reconnect."))
	}
}

// applyInitialAdvanced sends the launch-time effort/fast choices. fx rejects
// these on the acp command line but accepts them through
// session/set_config_option; a model that does not support one reports an
// error on the settings panel rather than preventing the session from opening.
func (c *chatSession) applyInitialAdvanced(client *acp.Client) {
	c.mu.Lock()
	effort, fast, provider, sid := c.initialEffort, c.initialFast, c.initialProvider, c.sessionID
	c.mu.Unlock()
	if effort == "" && fast == nil && provider == "" {
		return
	}
	type request struct{ id, value string }
	var requests []request
	if provider != "" {
		requests = append(requests, request{"provider", provider})
	}
	if effort != "" && !strings.EqualFold(effort, "auto") {
		requests = append(requests, request{"effort", effort})
	}
	if fast != nil {
		value := "false"
		if *fast {
			value = "true"
		}
		requests = append(requests, request{"fast", value})
	}
	for _, request := range requests {
		ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
		var result struct {
			ConfigOptions []configOptionWire `json:"configOptions"`
		}
		err := client.Call(ctx, "session/set_config_option", map[string]string{"sessionId": sid, "configId": request.id, "value": request.value}, &result)
		cancel()
		c.mu.Lock()
		if err != nil {
			c.advancedProblem = err.Error()
			c.mu.Unlock()
			c.invalidate()
			continue
		}
		if len(result.ConfigOptions) > 0 {
			c.configs = toConfigOptions(result.ConfigOptions)
			if option, ok := findConfigOption(c.configs, "model"); ok {
				c.model = option.CurrentValue
			}
			if mode, ok := chrome.ModeConfig(c.configs); ok {
				c.mode = mode.CurrentValue
			}
		}
		c.applyAdvancedLocked(request.id, request.value)
		c.advancedProblem = ""
		c.mu.Unlock()
		c.invalidate()
	}
}

func (c *chatSession) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	client := c.client
	c.mu.Unlock()
	if client == nil {
		c.cancel()
		close(c.closedDone)
		return
	}
	go func() { client.Shutdown(); c.cancel(); close(c.closedDone) }()
}

func (c *chatSession) appendLocked(role, key, text string) int {
	c.next++
	c.messages = append(c.messages, chrome.ChatMessage{ID: c.next, Role: role, Text: text})
	index := len(c.messages) - 1
	if key != "" {
		c.byID[key] = index
	}
	return index
}

// appendStreamText adds one chunk to a message that is still arriving. The
// builder owns the growing buffer and Text is a view of it, so each chunk
// costs its own length rather than the whole reply. It must be called with
// c.mu held. The builder is never reset while its string is still shown:
// append-only writes leave the bytes a published string names unchanged.
func (c *chatSession) appendStreamText(index int, text string) {
	if text == "" || index < 0 || index >= len(c.messages) {
		return
	}
	b := c.streams[index]
	if b == nil {
		b = &strings.Builder{}
		b.WriteString(c.messages[index].Text)
		if c.streams == nil {
			c.streams = make(map[int]*strings.Builder)
		}
		c.streams[index] = b
	}
	b.WriteString(text)
	c.messages[index].Text = b.String()
	c.markMessageDirtyLocked(index)
}

func (c *chatSession) event(p acp.Packet) {
	if p.Method == "session/request_permission" {
		var params struct {
			SessionID string `json:"sessionId"`
			ToolCall  struct {
				Title    string          `json:"title"`
				RawInput json.RawMessage `json:"rawInput"`
			} `json:"toolCall"`
			Options []chrome.PermissionOption `json:"options"`
		}
		if json.Unmarshal(p.Params, &params) != nil {
			return
		}
		c.mu.Lock()
		if c.sessionID != "" && params.SessionID != c.sessionID {
			client := c.client
			c.mu.Unlock()
			if client != nil {
				go client.Reject(p.ID)
			}
			return
		}
		c.permission = &chrome.ChatPermission{ID: string(p.ID), Title: params.ToolCall.Title, Detail: truncateText(string(params.ToolCall.RawInput), 8000), Options: params.Options}
		c.mu.Unlock()
		c.invalidate()
		return
	}
	if p.Method != "session/update" {
		if len(p.ID) > 0 {
			c.mu.Lock()
			client := c.client
			c.mu.Unlock()
			if client != nil {
				go client.Reject(p.ID)
			}
		}
		return
	}
	var params struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			Kind      string          `json:"sessionUpdate"`
			MessageID string          `json:"messageId"`
			ToolID    string          `json:"toolCallId"`
			Name      string          `json:"name"`
			Title     string          `json:"title"`
			Status    string          `json:"status"`
			RawInput  json.RawMessage `json:"rawInput"`
			Content   json.RawMessage `json:"content"`
			// usage_update carries provider-confirmed context usage:
			// {"sessionUpdate":"usage_update","used":N,"size":N}.
			Used int `json:"used"`
			Size int `json:"size"`
			Meta struct {
				Fx struct {
					Tool struct {
						Internal bool `json:"internal"`
					} `json:"toolCall"`
				} `json:"fx"`
			} `json:"_meta"`
		} `json:"update"`
	}
	if json.Unmarshal(p.Params, &params) != nil {
		return
	}
	u := params.Update
	c.mu.Lock()
	if c.closed || (c.sessionID != "" && params.SessionID != c.sessionID) {
		c.mu.Unlock()
		return
	}
	switch u.Kind {
	case "agent_message_chunk", "user_message_chunk":
		var content struct{ Type, Text string }
		if json.Unmarshal(u.Content, &content) != nil || content.Type != "text" {
			c.mu.Unlock()
			return
		}
		role := "assistant"
		if u.Kind == "user_message_chunk" {
			role = "user"
		}
		key := role + ":" + u.MessageID
		index, ok := c.byID[key]
		if ok && role == "assistant" && index != len(c.messages)-1 {
			index = c.appendLocked(role, key, "")
		}
		if !ok {
			// fx may echo a submitted user message; bind the remote ID without duplicating it.
			if role == "user" && len(c.messages) > 0 && c.messages[len(c.messages)-1].Role == "user" && c.messages[len(c.messages)-1].Text == content.Text {
				c.byID[key] = len(c.messages) - 1
				c.mu.Unlock()
				return
			}
			index = c.appendLocked(role, key, "")
		}
		c.appendStreamText(index, content.Text)
	case "tool_call", "tool_call_update":
		if u.Meta.Fx.Tool.Internal {
			c.byID["hidden-tool:"+u.ToolID] = -1
			c.mu.Unlock()
			return
		}
		if _, hidden := c.byID["hidden-tool:"+u.ToolID]; hidden {
			c.mu.Unlock()
			return
		}
		key := "tool:" + u.ToolID
		index, ok := c.byID[key]
		if !ok {
			index = c.appendLocked("tool", key, "")
		}
		m := &c.messages[index]
		if u.Title != "" {
			m.Title = u.Title
		}
		if m.Title == "" {
			m.Title = "Tool activity"
		}
		if u.Status != "" {
			m.Status = u.Status
		}
		// A shell call's arguments are the command it ran, and fx's result
		// preview never gets far enough to show it. Keep the command when
		// the call is announced, so even a running row has something to
		// show behind its chevron.
		if cmd := toolCommand(u.RawInput); cmd != "" {
			m.Command = cmd
		}
		if u.Name == "shell" || m.Command != "" {
			if m.Shell == nil {
				m.Shell = &chrome.ShellResult{}
			}
		}
		if len(u.Content) > 0 {
			text, diff := toolContent(u.Content)
			if m.Shell != nil {
				if facts, shown, ok := shellResult(text); ok {
					// The envelope is fx's bookkeeping, not the payload:
					// its flags are kept, its text is dropped in favour of
					// the command (or the output it carried, if complete).
					m.Shell = &facts
					text = shown
				}
			}
			m.Text, m.Diff = text, diff
		}
		m.Done = m.Status == "completed" || m.Status == "failed"
		// Time the call so the transcript can say what it cost: a row that
		// reads "Running" next to a word like "Completed" tells the reader
		// nothing, but a duration and a state do.
		now := time.Now()
		if m.Started.IsZero() {
			m.Started = now
		}
		if m.Done && m.Ended.IsZero() {
			m.Ended = now
		}
		c.markMessageDirtyLocked(index)
	case "session_info_update":
		// fx names the conversation. It arrives after the first turn, which
		// is what tells the rail to show this name instead of guessing from
		// the directory.
		if title := strings.TrimSpace(u.Title); title != "" {
			c.title = title
		}
	case "usage_update":
		// fx only sends this once the context window is known; keep the
		// last good value if a later update is missing one.
		if u.Used > 0 {
			c.contextUsed = u.Used
		}
		if u.Size > 0 {
			c.contextTotal = u.Size
		}
	case "notice":
		c.notices = append(c.notices, truncateText(u.Title, 8000))
		if len(c.notices) > 8 {
			c.notices = c.notices[len(c.notices)-8:]
		}
	default:
		c.mu.Unlock()
		return
	}
	usage := u.Kind == "usage_update"
	c.mu.Unlock()
	if usage {
		// Persist outside c.mu: writing the record must not make the next
		// frame wait for the disk.
		c.saveStats()
	}
	c.invalidate()
}
func toolContent(raw json.RawMessage) (string, *chrome.DiffInfo) {
	var blocks []struct {
		Type                   string                      `json:"type"`
		Content                struct{ Type, Text string } `json:"content"`
		Path, OldText, NewText string
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return "", nil
	}
	var out strings.Builder
	var diff *chrome.DiffInfo
	for _, b := range blocks {
		switch {
		case b.Type == "content" && b.Content.Type == "text":
			out.WriteString(b.Content.Text)
			out.WriteByte('\n')
		case b.Type == "diff":
			// A single edit before this flattened to "path\nnewText",
			// which threw away the old text and left the user comparing
			// prose. Keep it structured so the timeline can colour it
			// (§4.2).
			if diff == nil {
				diff = &chrome.DiffInfo{}
			}
			if diff.Path == "" {
				diff.Path = b.Path
			}
			diff.Lines = append(diff.Lines, lineDiff(b.OldText, b.NewText)...)
		}
	}
	return truncateText(strings.TrimSpace(out.String()), 128*1024), diff
}
func truncateText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && (s[limit]&0xc0) == 0x80 {
		limit--
	}
	return s[:limit] + "\n… Output truncated"
}

// promptImage is one image on a prompt, already validated: the type is the
// one its bytes really are, and the size is inside fx's limit (chrome
// refuses anything else before it reaches here). The name rides along for the
// transcript; the wire carries only the type and the bytes.
type promptImage struct {
	Name     string
	MimeType string
	Data     []byte
}

// toPromptImages narrows the composer's attachments to what the wire carries.
// The preview and the id are for the chip, not for fx.
func toPromptImages(attachments []chrome.Attachment) []promptImage {
	if len(attachments) == 0 {
		return nil
	}
	images := make([]promptImage, 0, len(attachments))
	for _, a := range attachments {
		images = append(images, promptImage{Name: a.Name, MimeType: a.MimeType, Data: a.Data})
	}
	return images
}

// submit reserves the turn synchronously so double-clicks cannot send
// duplicates. While a turn is running the message is steered into it (when
// fx can) or queued (when it cannot) rather than rejected: the composer must
// not go dead for the length of a turn.
//
// Images are the exception, and say so: steering takes text alone and a queued
// message carries only what was written with it, so a message with images
// waits for an idle turn instead of arriving without them (§4.4.2). A mention
// is text that named a file, so it rides along either way, with the file read
// when the message is sent.
func (c *chatSession) submit(text string, images []promptImage, mentions []promptMention) bool {
	text = strings.TrimSpace(text)
	if text == "" && len(images) == 0 {
		return false
	}
	return c.send(text, images, mentions, true)
}

// send starts a turn, steers the running one, or queues the text until it
// ends. appending is false only when the text is already in the transcript
// (a queued message being delivered).
func (c *chatSession) send(text string, images []promptImage, mentions []promptMention, appending bool) bool {
	c.mu.Lock()
	if !c.ready || c.configuring || c.closed {
		c.mu.Unlock()
		return false
	}
	if c.busy {
		if len(images) > 0 {
			c.mu.Unlock()
			return false
		}
		steering := c.steering
		c.mu.Unlock()
		switch {
		case steering && appending:
			return c.steer(text, mentions)
		case appending:
			return c.queue(text, mentions)
		default:
			return false
		}
	}
	client, sid := c.beginTurnLocked(text, images, mentions, appending)
	c.mu.Unlock()
	c.invalidate()
	go c.runTurn(client, sid, text, images, mentions)
	return true
}

// beginTurnLocked reserves the next prompt while c.mu is held, so a turn end
// and a newly typed message cannot both believe the session is idle. It also
// forgets the reply and tool bindings of the last turn: a new prompt is a new
// reply even if fx reuses a message or tool-call ID across prompts.
func (c *chatSession) beginTurnLocked(text string, images []promptImage, mentions []promptMention, appending bool) (*acp.Client, string) {
	c.busy = true
	c.problem = ""
	for key := range c.byID {
		if strings.HasPrefix(key, "assistant:") || strings.HasPrefix(key, "tool:") || strings.HasPrefix(key, "hidden-tool:") {
			delete(c.byID, key)
		}
	}
	client, sid := c.client, c.sessionID
	if appending {
		index := c.appendLocked("user", "", text)
		c.messages[index].Done = true
		c.messages[index].Images = imageNames(images)
	}
	return client, sid
}

// runTurn owns one session/prompt call. When it ends, the next queued message
// is reserved under the same lock that clears busy: otherwise a message typed
// in the gap could start first, and the queued message would then be refused
// and lost even though it was already in the transcript.
func (c *chatSession) runTurn(client *acp.Client, sid, text string, images []promptImage, mentions []promptMention) {
	var result struct {
		StopReason string `json:"stopReason"`
		// Usage is this turn's provider-reported tokens. fx includes it on
		// every completed prompt; a provider that does not report token
		// counts leaves it out. It is the one token count that works on every
		// provider, unlike the AI-Gateway-only `fx usage` totals.
		Usage *struct {
			InputTokens  int `json:"inputTokens"`
			OutputTokens int `json:"outputTokens"`
		} `json:"usage"`
	}
	err := client.Call(c.ctx, "session/prompt", map[string]any{"sessionId": sid, "prompt": promptBlocks(text, images, mentions)}, &result)
	c.mu.Lock()
	c.busy = false
	c.stopping = false
	c.permission = nil
	c.finishMessagesLocked()
	if err != nil && !c.closed {
		c.problem = err.Error()
	}
	if result.Usage != nil {
		// Counts are summed, not replaced: the result describes one turn, and
		// the stat is for the whole conversation.
		if result.Usage.InputTokens > 0 {
			c.inputTokens += result.Usage.InputTokens
		}
		if result.Usage.OutputTokens > 0 {
			c.outputTokens += result.Usage.OutputTokens
		}
	}
	next, hasNext := c.nextQueuedLocked()
	c.mu.Unlock()
	if result.Usage != nil {
		c.saveStats()
	}
	c.invalidate()
	if hasNext {
		go c.runTurn(next.client, next.sid, next.prompt.text, nil, next.prompt.mentions)
	}
}

// activeTurn is a queued message that has just been reserved as a prompt.
type activeTurn struct {
	client *acp.Client
	sid    string
	prompt queuedPrompt
}

// nextQueuedLocked removes and reserves the oldest queued message. The caller
// holds c.mu, so the transcript order and the order fx receives match even if
// the UI is typing at the same moment.
func (c *chatSession) nextQueuedLocked() (activeTurn, bool) {
	if len(c.queued) == 0 || c.closed || !c.ready || c.client == nil {
		return activeTurn{}, false
	}
	prompt := c.queued[0]
	c.queued[0] = queuedPrompt{}
	c.queued = c.queued[1:]
	if len(c.queued) == 0 {
		c.queued = nil
	}
	client, sid := c.beginTurnLocked(prompt.text, nil, prompt.mentions, false)
	return activeTurn{client: client, sid: sid, prompt: prompt}, true
}

// promptBlocks builds the ACP prompt: the text, then one inline image block
// per attachment. The shape is measured, not guessed — fx 0.0.14 accepts
// {"type":"image","mimeType":…,"data":<base64>} and rejects anything else with
// -32602 "Invalid image prompt block" (§12.3.2).
func promptBlocks(text string, images []promptImage, mentions []promptMention) []map[string]any {
	blocks := make([]map[string]any, 0, len(images)+len(mentions)+1)
	if text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, image := range images {
		blocks = append(blocks, map[string]any{
			"type":     "image",
			"mimeType": image.MimeType,
			"data":     base64.StdEncoding.EncodeToString(image.Data),
		})
	}
	// A mention is a resource block whose text is the file, because that is
	// where fx reads a file from: the uri registers the file as a context
	// target, so the rules that apply to it come along too (§12.3.3).
	for _, mention := range mentions {
		blocks = append(blocks, map[string]any{
			"type": "resource",
			"resource": map[string]any{
				"uri":  fileURL(mention.Abs),
				"text": mention.Text,
			},
		})
	}
	return blocks
}

// fileURL is a file as fx takes it: a file:// URL of the absolute path. fx
// resolves it itself and refuses anything outside the workspace, so the path
// is the one the app already read the file from.
func fileURL(path string) string {
	if path == "" {
		return ""
	}
	return "file://" + filepath.ToSlash(path)
}

// imageNames labels a transcript entry with what was sent with it, so the
// history reads as the message the user wrote, not as an empty bubble.
func imageNames(images []promptImage) []string {
	if len(images) == 0 {
		return nil
	}
	names := make([]string, 0, len(images))
	for _, image := range images {
		name := image.Name
		if name == "" {
			name = image.MimeType
		}
		names = append(names, name)
	}
	return names
}

// steer delivers a message into a running turn. It is a separate call path
// on purpose: the turn is still owned by its own session/prompt, so this
// call must not clear busy or mark the transcript done (§8 step 7).
func (c *chatSession) steer(text string, mentions []promptMention) bool {
	c.mu.Lock()
	if !c.ready || !c.busy || !c.steering || c.closed || c.client == nil {
		c.mu.Unlock()
		return false
	}
	client, sid := c.client, c.sessionID
	index := c.appendLocked("user", "", text)
	c.messages[index].Done = true
	c.mu.Unlock()
	c.invalidate()
	go func() {
		params := map[string]any{
			"sessionId": sid,
			"prompt":    promptBlocks(text, nil, mentions),
			"_meta":     map[string]any{"fx": map[string]bool{"steer": true}},
		}
		var result json.RawMessage
		if err := client.Call(c.ctx, "session/prompt", params, &result); err != nil {
			// The turn itself is still running; report the failure without
			// ending it, so the user can decide to stop or retry.
			c.mu.Lock()
			if !c.closed {
				c.problem = err.Error()
			}
			c.mu.Unlock()
			c.invalidate()
		}
	}()
	return true
}

// queue holds a message typed while a turn is running and fx cannot steer.
// It is shown in the transcript immediately and delivered when the turn ends.
func (c *chatSession) queue(text string, mentions []promptMention) bool {
	c.mu.Lock()
	if !c.ready || !c.busy || c.closed {
		c.mu.Unlock()
		return false
	}
	c.queued = append(c.queued, queuedPrompt{text: text, mentions: mentions})
	index := c.appendLocked("user", "", text)
	c.messages[index].Done = true
	c.mu.Unlock()
	c.invalidate()
	return true
}
func (c *chatSession) stopTurn() {
	c.mu.Lock()
	if !c.busy || c.stopping || c.client == nil {
		c.mu.Unlock()
		return
	}
	c.stopping = true
	client, sid := c.client, c.sessionID
	c.mu.Unlock()
	c.invalidate()
	go func() {
		if err := client.Notify("session/cancel", map[string]string{"sessionId": sid}); err != nil {
			c.fail(err)
		}
	}()
}
func (c *chatSession) choosePermission(requestID, option string) {
	c.mu.Lock()
	p := c.permission
	if c.closed || c.stopping || p == nil || p.ID != requestID || c.client == nil {
		c.mu.Unlock()
		return
	}
	valid := false
	for _, o := range p.Options {
		if o.ID == option {
			valid = true
		}
	}
	if !valid {
		c.mu.Unlock()
		return
	}
	client := c.client
	c.permission = nil
	c.mu.Unlock()
	c.invalidate()
	go func() {
		if err := client.Respond(json.RawMessage(requestID), map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": option}}); err != nil {
			c.fail(err)
		}
	}()
}

// setConfig changes one session option by its fx config id. The mode and the
// model go through here too: fx answers with the refreshed option set, which
// is the only way the model list follows a provider switch, since no
// session/update carries config changes (§12.3).
func (c *chatSession) setConfig(configID, value string) {
	c.mu.Lock()
	if c.busy || c.configuring || c.closed || !c.ready || c.client == nil {
		c.mu.Unlock()
		return
	}
	c.configuring = true
	client, sid := c.client, c.sessionID
	c.mu.Unlock()
	c.invalidate()
	advanced := configID == "effort" || configID == "fast" || configID == "ultrafast"
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
		defer cancel()
		var result struct {
			ConfigOptions []configOptionWire `json:"configOptions"`
		}
		err := client.Call(ctx, "session/set_config_option", map[string]string{"sessionId": sid, "configId": configID, "value": value}, &result)
		if err != nil {
			if advanced {
				// An unsupported reasoning level is a setting problem, not a
				// dead connection. Keep the conversation usable and show the
				// rejection on the advanced settings panel.
				c.mu.Lock()
				c.advancedProblem = err.Error()
				c.configuring = false
				c.mu.Unlock()
				c.invalidate()
				return
			}
			c.fail(err)
			return
		}
		c.mu.Lock()
		if len(result.ConfigOptions) > 0 {
			c.configs = toConfigOptions(result.ConfigOptions)
		} else if !advanced {
			// fx accepted the change without echoing the list. Record it
			// locally rather than claiming nothing happened.
			c.applyConfig(configID, value)
		}
		if advanced {
			c.applyAdvancedLocked(configID, value)
			c.advancedProblem = ""
		}
		if mode, ok := chrome.ModeConfig(c.configs); ok {
			c.mode = mode.CurrentValue
		}
		if option, ok := findConfigOption(c.configs, "model"); ok {
			c.model = option.CurrentValue
		}
		c.configuring = false
		c.mu.Unlock()
		c.invalidate()
	}()
}

// setAdvanced applies the model-dependent settings as one configuration
// change. fx takes one id per call, so the calls are sequential; configuring
// stays true for the whole batch and a rejection is shown without closing the
// conversation.
func (c *chatSession) setAdvanced(effort string, fast, ultra bool) {
	c.mu.Lock()
	if c.busy || c.configuring || c.closed || !c.ready || c.client == nil {
		c.mu.Unlock()
		return
	}
	c.configuring = true
	client, sid := c.client, c.sessionID
	c.mu.Unlock()
	c.invalidate()
	go func() {
		type request struct{ id, value string }
		requests := []request{
			{"effort", effort},
			{"fast", fmt.Sprintf("%t", fast)},
			{"ultrafast", fmt.Sprintf("%t", ultra)},
		}
		var problem string
		for _, request := range requests {
			if request.id == "effort" && strings.TrimSpace(request.value) == "" {
				continue
			}
			ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
			var result struct {
				ConfigOptions []configOptionWire `json:"configOptions"`
			}
			err := client.Call(ctx, "session/set_config_option", map[string]string{"sessionId": sid, "configId": request.id, "value": request.value}, &result)
			cancel()
			c.mu.Lock()
			if err != nil {
				problem = err.Error()
				c.mu.Unlock()
				continue
			}
			if len(result.ConfigOptions) > 0 {
				c.configs = toConfigOptions(result.ConfigOptions)
				if option, ok := findConfigOption(c.configs, "model"); ok {
					c.model = option.CurrentValue
				}
				if mode, ok := chrome.ModeConfig(c.configs); ok {
					c.mode = mode.CurrentValue
				}
			}
			c.applyAdvancedLocked(request.id, request.value)
			c.mu.Unlock()
		}
		c.mu.Lock()
		c.advancedProblem = problem
		c.configuring = false
		c.mu.Unlock()
		c.invalidate()
	}()
}

// applyAdvancedLocked records a model-dependent setting the user changed.
// The caller holds c.mu.
func (c *chatSession) applyAdvancedLocked(configID, value string) {
	switch configID {
	case "effort":
		c.effort = value
	case "fast":
		c.fast = value == "true" || value == "on"
	case "ultrafast":
		c.ultra = value == "true" || value == "on"
	}
}

// applyConfig records a locally accepted value on the option it belongs to.
// The caller holds c.mu.
func (c *chatSession) applyConfig(configID, value string) {
	for i := range c.configs {
		if c.configs[i].ID == configID {
			c.configs[i].CurrentValue = value
		}
	}
}
