package app

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png" // the fixture decodes what it is sent
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"fx-desktop/internal/acp"
	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/sessionstats"
	"fx-desktop/internal/testutil"
	"github.com/egoist/mygo/ui"
)

// This fixture exercises real stdio/process lifetime with no provider or network.
func TestChatACPProcess(t *testing.T) {
	if os.Getenv("FX_DESKTOP_ACP_TEST_HELPER") != "1" {
		return
	}
	out := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	var promptID json.RawMessage
	update := func(u any) {
		out.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fixture-session", "update": u}})
	}
	// The fixture keeps the session's configuration the way fx does, so a
	// set_config_option round trip is exercised end to end rather than
	// against a stub that always says yes.
	model, mode, provider := "test/model", "ask", "test"
	// workspace is the project session/new opened, and embedded is whether
	// this fx takes a file's content in the prompt. Both are read from what
	// the client sent, so a block the GUI shaped wrongly fails here.
	workspace := ""
	embedded := os.Getenv("FX_DESKTOP_ACP_TEST_NO_EMBEDDED_CONTEXT") != "1"
	configOptions := func() []map[string]any {
		return []map[string]any{
			{"id": "provider", "name": "Provider", "category": "model", "type": "select", "currentValue": provider,
				"options": []map[string]string{{"value": "test", "name": "Test"}, {"value": "other", "name": "Other"}}},
			{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": model,
				"options": []map[string]string{{"value": "test/model", "name": "test/model"}, {"value": "test/other", "name": "test/other"}}},
			{"id": "mode", "name": "Session Mode", "category": "mode", "type": "select", "currentValue": mode,
				"options": []map[string]string{{"value": "ask", "name": "Ask"}, {"value": "code", "name": "Code"}}},
		}
	}
	modes := func() map[string]any {
		return map[string]any{"currentModeId": mode, "availableModes": []map[string]string{
			{"id": "ask", "name": "Ask", "description": "Request permission before making changes"},
			{"id": "code", "name": "Code", "description": "Run routine work and review other actions"},
		}}
	}
	for scanner.Scan() {
		var p acp.Packet
		if json.Unmarshal(scanner.Bytes(), &p) != nil {
			os.Exit(2)
		}
		switch p.Method {
		case "initialize":
			var params struct {
				Capabilities struct {
					Session struct {
						Notices json.RawMessage `json:"notices"`
					} `json:"session"`
				} `json:"clientCapabilities"`
				Meta struct {
					FX struct {
						Terminal bool `json:"terminal"`
					} `json:"fx"`
				} `json:"_meta"`
			}
			json.Unmarshal(p.Params, &params)
			if len(params.Capabilities.Session.Notices) == 0 || params.Meta.FX.Terminal {
				out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "error": acp.Error{Code: -32602, Message: "GUI capabilities missing"}})
				continue
			}
			steering := os.Getenv("FX_DESKTOP_ACP_TEST_NO_STEER") != "1"
			out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{
				"promptCapabilities": map[string]bool{"image": true, "audio": false, "embeddedContext": embedded},
				"_meta":              map[string]any{"fx": map[string]bool{"steering": steering}},
			}}})
		case "session/new", "session/load":
			// fx only inherits ~/.fx/mcp.json when the client asks. Make the
			// opt-in a hard contract here: if the client stops sending it,
			// the servers the user configured for fx would silently vanish
			// from the GUI, and every chat test fails loudly instead.
			var opening struct {
				Cwd  string `json:"cwd"`
				Meta struct {
					FX struct {
						ProfileMcpServers bool `json:"profileMcpServers"`
					} `json:"fx"`
				} `json:"_meta"`
			}
			json.Unmarshal(p.Params, &opening)
			if opening.Cwd == "" || !opening.Meta.FX.ProfileMcpServers {
				out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "error": acp.Error{Code: -32602, Message: "client did not opt into profile MCP servers"}})
				continue
			}
			workspace = opening.Cwd
			update(map[string]string{"sessionUpdate": "notice", "title": "Fixture diagnostic notice"})
			if p.Method == "session/load" {
				update(map[string]any{"sessionUpdate": "user_message_chunk", "messageId": "old-user", "content": map[string]string{"type": "text", "text": "Earlier question"}})
				update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "old-agent", "content": map[string]string{"type": "text", "text": "Earlier answer"}})
			}
			out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "result": map[string]any{"sessionId": "fixture-session", "configOptions": configOptions(), "modes": modes()}})
		case "session/prompt":
			var params struct {
				Prompt []promptBlock `json:"prompt"`
				Meta   struct {
					FX struct {
						Steer bool `json:"steer"`
					} `json:"fx"`
				} `json:"_meta"`
			}
			json.Unmarshal(p.Params, &params)
			// Images and files are hard contracts, like the MCP opt-in above:
			// the shapes measured against fx 0.0.14 are
			// {"type":"image","mimeType":…,"data":<base64>} and
			// {"type":"resource","resource":{"uri":"file://…","text":…}}.
			// Anything else is rejected exactly as fx rejects it, so a wrong
			// field name fails the test instead of quietly sending a picture
			// nobody can see or a file nobody can read.
			if bad := imageBlockProblem(params.Prompt); bad != "" {
				out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "error": acp.Error{Code: -32602, Message: bad}})
				continue
			}
			if bad := resourceBlockProblem(params.Prompt, workspace, embedded); bad != "" {
				out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "error": acp.Error{Code: -32602, Message: bad}})
				continue
			}
			if params.Meta.FX.Steer {
				// A steer joins the turn that already owns the session.
				// Acknowledge it without emitting the final chunks or
				// resolving the prompt that is still in flight.
				out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "result": map[string]string{"stopReason": "steer"}})
				continue
			}
			promptID = p.ID
			update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "tool-1", "title": "Read main.go", "status": "pending"})
			out.Encode(map[string]any{"jsonrpc": "2.0", "id": "approval-1", "method": "session/request_permission", "params": map[string]any{"sessionId": "fixture-session", "toolCall": map[string]any{"title": "Read main.go", "rawInput": map[string]string{"path": "main.go"}}, "options": []map[string]string{{"optionId": "allow_once", "name": "Allow once", "kind": "allow_once"}, {"optionId": "reject_once", "name": "Reject", "kind": "reject_once"}}}})
		case "session/cancel":
			out.Encode(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]string{"stopReason": "cancelled"}})
		case "session/set_config_option":
			// fx answers with the refreshed option set; the model list
			// follows a provider switch only this way.
			var params struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			json.Unmarshal(p.Params, &params)
			switch params.ConfigID {
			case "provider":
				provider = params.Value
			case "model":
				model = params.Value
			case "mode":
				mode = params.Value
			}
			out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "result": map[string]any{"configOptions": configOptions(), "modes": modes()}})
		case "":
			if string(p.ID) == `"approval-1"` {
				var result struct {
					Outcome struct {
						OptionID string `json:"optionId"`
					} `json:"outcome"`
				}
				json.Unmarshal(p.Result, &result)
				status, text := "failed", "Permission denied"
				if result.Outcome.OptionID == "allow_once" {
					status, text = "completed", "package main"
				}
				update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "tool-1", "status": status, "content": []map[string]any{{"type": "content", "content": map[string]string{"type": "text", "text": text}}}})
				for _, text := range []string{"Hello ", "世界", "\n\n**Done.**"} {
					update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "agent-1", "content": map[string]string{"type": "text", "text": text}})
				}
				// fx reports the turn's context use immediately before the
				// prompt result, and the result carries the turn's tokens.
				// The fixture mirrors that order so accumulation and
				// persistence are exercised end to end.
				update(map[string]any{"sessionUpdate": "usage_update", "used": 4321, "size": 200000})
				out.Encode(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]any{
					"stopReason": "end_turn",
					"usage":      map[string]int{"inputTokens": 1234, "outputTokens": 56},
				}})
			}
		}
	}
	os.Exit(0)
}

// promptBlock is one block of a fixture prompt as the app writes it: the two
// shapes fx takes, and nothing else.
type promptBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
	Resource struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"resource"`
}

// imageBlockProblem validates the prompt's image blocks the way fx does,
// returning fx's own message when one is wrong. A block the GUI mislabels, or
// hangs on a field fx does not read, is caught here rather than in production.
func imageBlockProblem(blocks []promptBlock) string {
	for _, block := range blocks {
		if block.Type != "image" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(block.Data)
		if err != nil || len(raw) == 0 {
			return "Invalid image prompt block"
		}
		_, format, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return "Invalid image prompt block"
		}
		if "image/"+format != block.MimeType && !(format == "jpeg" && block.MimeType == "image/jpg") {
			return "unsupported image type"
		}
	}
	return ""
}

// resourceBlockProblem validates the prompt's file blocks the way fx reads
// them: a resource block whose uri is a file:// URL of an absolute path inside
// the workspace, and whose text is the file itself. fx registers a uri it can
// resolve as a context target and takes the text it is given, so a block the
// GUI shaped wrongly — or filled with something other than the file — would
// arrive as a mention that carries nothing, or the wrong thing.
func resourceBlockProblem(blocks []promptBlock, workspace string, embedded bool) string {
	for _, block := range blocks {
		if block.Type != "resource" {
			continue
		}
		if !embedded {
			return "this fx does not take resource blocks"
		}
		uri, err := url.Parse(block.Resource.URI)
		if err != nil || !strings.EqualFold(uri.Scheme, "file") || !strings.HasPrefix(uri.Path, "/") {
			return "Invalid prompt block"
		}
		if workspace != "" && !strings.HasPrefix(uri.Path, workspace+string(filepath.Separator)) {
			return "resource block names a file outside the workspace"
		}
		file, err := os.ReadFile(uri.Path)
		if err != nil {
			return "resource block names no file"
		}
		if block.Resource.Text != string(file) {
			return "resource block text is not the file"
		}
	}
	return ""
}

func chatFixture(t *testing.T, args ...string) *chatSession {
	t.Helper()
	return chatFixtureSetup(t, nil, args...)
}

// chatFixtureSetup is chatFixture with one hook between construction and
// connect, for tests that must attach state (such as a usage store) before
// the ACP session is opened.
func chatFixtureSetup(t *testing.T, setup func(*chatSession), args ...string) *chatSession {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_DESKTOP_ACP_TEST_HELPER", "1")
	t.Setenv("FX_DESKTOP_ACP_TEST_BINARY", executable)
	file := filepath.Join(t.TempDir(), "fake-fx")
	if err = os.WriteFile(file, []byte("#!/bin/sh\nexec \"$FX_DESKTOP_ACP_TEST_BINARY\" -test.run=^TestChatACPProcess$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c := newChatSession(file, t.TempDir(), args, nil)
	if setup != nil {
		setup(c)
	}
	t.Cleanup(c.Close)
	go c.connect()
	awaitChat(t, c, func(s chatSnapshot) bool { return s.Ready })
	return c
}
func awaitChat(t *testing.T, c *chatSession, check func(chatSnapshot) bool) chatSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s := c.snapshot()
		if check(s) {
			return s
		}
		if s.Problem != "" {
			t.Fatal(s.Problem)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("chat state did not settle: %+v", c.snapshot())
	return chatSnapshot{}
}
func TestChatStreamPermissionAndDuplicateSubmit(t *testing.T) {
	c := chatFixture(t)
	if notices := c.snapshot().Notices; len(notices) != 1 || notices[0] != "Fixture diagnostic notice" {
		t.Fatalf("notices: %v", notices)
	}
	if !c.submit("Inspect the project", nil, nil) {
		t.Fatal("not submitted")
	}
	// fx advertises steering, so a message sent while the turn runs joins
	// that turn instead of being rejected — and it must not end it.
	if !c.submit("also check the tests", nil, nil) {
		t.Fatal("a steer should be accepted while busy")
	}
	s := awaitChat(t, c, func(s chatSnapshot) bool { return s.Permission != nil })
	if !s.Busy {
		t.Fatal("steering must not end the running turn")
	}
	if len(s.Messages) != 3 || s.Messages[1].Text != "also check the tests" {
		t.Fatalf("the steer was not added to the transcript: %+v", s.Messages)
	}
	c.choosePermission(s.Permission.ID, "not-an-option")
	if c.snapshot().Permission == nil {
		t.Fatal("invalid approval dismissed the request")
	}
	c.choosePermission(s.Permission.ID, "allow_once")
	s = awaitChat(t, c, func(s chatSnapshot) bool { return !s.Busy })
	if len(s.Messages) != 4 || s.Messages[2].Status != "completed" || s.Messages[3].Text != "Hello 世界\n\n**Done.**" {
		t.Fatalf("stream was not assembled: %+v", s.Messages)
	}
	// The tool call is timed so the transcript can say what it cost.
	tool := s.Messages[2]
	if tool.Started.IsZero() || tool.Ended.IsZero() || tool.Ended.Before(tool.Started) {
		t.Fatalf("the tool call was not timed: %+v", tool)
	}
	if !s.Messages[3].Done || s.Permission != nil {
		t.Fatal("turn did not finish")
	}
	// Changing an option goes through one path for every option, mode
	// included, and the answer's refreshed set is what the GUI believes.
	c.setConfig("model", "test/other")
	s = awaitChat(t, c, func(s chatSnapshot) bool { return s.Model == "test/other" })
	if option, ok := findConfigOption(s.Configs, "model"); !ok || option.CurrentValue != "test/other" {
		t.Fatalf("model option not refreshed: %+v", s.Configs)
	}
	c.setConfig("mode", "code")
	s = awaitChat(t, c, func(s chatSnapshot) bool { return s.Mode == "code" })
	if len(s.Modes) != 2 || s.Modes[0].Description == "" {
		t.Fatalf("available modes must carry their description: %+v", s.Modes)
	}
}

// An image rides the prompt as the one inline block shape fx accepts, and the
// transcript records that it did. The fixture decodes the block the way fx
// does, so a wrong field name or a mislabelled type fails here, not in front
// of a model.
func TestChatSendsInlineImages(t *testing.T) {
	c := chatFixture(t)
	if !c.canTakeImages() {
		t.Fatal("fx advertises promptCapabilities.image")
	}
	snap := c.snapshot()
	if !snap.Images {
		t.Fatal("the composer cannot offer what the snapshot does not carry")
	}
	png := pngBytes(t, 4, 4)
	if !c.submit("does this look right?", []promptImage{{Name: "shot.png", MimeType: "image/png", Data: png}}, nil) {
		t.Fatal("not submitted")
	}
	s := awaitChat(t, c, func(s chatSnapshot) bool { return s.Permission != nil })
	if len(s.Messages) != 2 || s.Messages[0].Text != "does this look right?" {
		t.Fatalf("transcript: %+v", s.Messages)
	}
	if names := s.Messages[0].Images; len(names) != 1 || names[0] != "shot.png" {
		t.Fatalf("the transcript does not record the image: %v", names)
	}
	c.choosePermission(s.Permission.ID, "allow_once")
	s = awaitChat(t, c, func(s chatSnapshot) bool { return !s.Busy })
	if s.Problem != "" {
		t.Fatalf("fx refused the message: %s", s.Problem)
	}
}

// Steering takes text alone and a queued message carries only what was written
// with it, so a turn that is already running does not take an image — and says
// so rather than sending the words without it.
func TestChatHoldsImagesForAnIdleTurn(t *testing.T) {
	c := chatFixture(t)
	if !c.submit("first", nil, nil) {
		t.Fatal("not submitted")
	}
	awaitChat(t, c, func(s chatSnapshot) bool { return s.Permission != nil })

	png := pngBytes(t, 4, 4)
	if c.submit("with a picture", []promptImage{{MimeType: "image/png", Data: png}}, nil) {
		t.Fatal("a running turn must not take an image")
	}
	s := c.snapshot()
	if len(s.Messages) != 2 {
		t.Fatalf("a refused image still wrote to the transcript: %+v", s.Messages)
	}
	if s.Queued != 0 {
		t.Fatal("and must not queue one either")
	}
	c.choosePermission(s.Permission.ID, "allow_once")
	awaitChat(t, c, func(s chatSnapshot) bool { return !s.Busy })
}

// Without the steering capability the composer still accepts text: the app
// queues it and delivers it when the turn ends, so nothing is dropped and
// nothing is silently swallowed.
func TestChatQueuesWhenAgentCannotSteer(t *testing.T) {
	t.Setenv("FX_DESKTOP_ACP_TEST_NO_STEER", "1")
	c := chatFixture(t)
	if c.snapshot().Steering {
		t.Fatal("fixture advertised steering when told not to")
	}
	if !c.submit("Inspect the project", nil, nil) {
		t.Fatal("not submitted")
	}
	awaitChat(t, c, func(s chatSnapshot) bool { return s.Permission != nil })
	if !c.submit("run the tests too", nil, nil) {
		t.Fatal("a queued message must be accepted while busy")
	}
	s := c.snapshot()
	if s.Queued != 1 {
		t.Fatalf("the waiting message should be labelled as queued: %d", s.Queued)
	}
	// Ending the turn delivers the queued message as its own turn.
	c.choosePermission(s.Permission.ID, "allow_once")
	s = awaitChat(t, c, func(s chatSnapshot) bool { return s.Busy && s.Queued == 0 })
	n := 0
	for _, m := range s.Messages {
		if m.Text == "run the tests too" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the queued message must be delivered exactly once, saw %d in %+v", n, s.Messages)
	}
	c.stopTurn()
}

// A turn can run for minutes, so a user may type more than one message into
// it. The composer stays open and every message is already in the transcript,
// so each has to be delivered in turn: replacing the waiting message would
// leave bubbles on screen that fx never received.
func TestChatQueuesEveryMessageInOrder(t *testing.T) {
	t.Setenv("FX_DESKTOP_ACP_TEST_NO_STEER", "1")
	c := chatFixture(t)
	if !c.submit("Inspect the project", nil, nil) {
		t.Fatal("not submitted")
	}
	awaitChat(t, c, func(s chatSnapshot) bool { return s.Permission != nil })
	for _, text := range []string{"run the tests", "then the vet", "and the build"} {
		if !c.submit(text, nil, nil) {
			t.Fatalf("queued message %q refused", text)
		}
	}
	if s := c.snapshot(); s.Queued != 3 {
		t.Fatalf("queued messages = %d, want 3", s.Queued)
	}
	// Every turn ends by asking for the same approval; allow each one. Keep
	// answering until all four turns have produced their reply (the first
	// plus the three queued ones), so the test proves every message was sent.
	const reply = "Hello 世界\n\n**Done.**"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s := c.snapshot()
		if s.Permission != nil {
			c.choosePermission(s.Permission.ID, "allow_once")
		}
		replies := 0
		for _, m := range s.Messages {
			if m.Role == "assistant" {
				replies += strings.Count(m.Text, reply)
			}
		}
		if replies == 4 && !s.Busy {
			break
		}
		time.Sleep(time.Millisecond)
	}
	s := c.snapshot()
	replies, tools := 0, 0
	var users []string
	for _, m := range s.Messages {
		if m.Role == "assistant" {
			replies += strings.Count(m.Text, reply)
		}
		if m.Role == "tool" {
			tools++
		}
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if replies != 4 {
		t.Fatalf("queued messages were not all delivered: %d replies for 4 messages", replies)
	}
	if tools != 4 {
		t.Fatalf("a new turn reused the previous turn's tool row: %d tool rows for 4 turns", tools)
	}
	wantUsers := []string{"Inspect the project", "run the tests", "then the vet", "and the build"}
	if !slices.Equal(users, wantUsers) {
		t.Fatalf("queued messages were not kept in order:\n got %q\nwant %q", users, wantUsers)
	}
	if s.Busy || s.Queued != 0 {
		t.Fatalf("queued turns did not finish: busy=%v queued=%d", s.Busy, s.Queued)
	}
}

func TestChatCancelAndResume(t *testing.T) {
	c := chatFixture(t, "--resume", "fixture-session")
	s := c.snapshot()
	if len(s.Messages) != 2 || !s.Messages[1].Done {
		t.Fatalf("saved conversation not replayed: %+v", s)
	}
	c.submit("Run something", nil, nil)
	awaitChat(t, c, func(s chatSnapshot) bool { return s.Permission != nil })
	c.stopTurn()
	awaitChat(t, c, func(s chatSnapshot) bool { return !s.Busy && s.Permission == nil })
	c.Close()
	if c.submit("after close", nil, nil) {
		t.Fatal("closed session accepted prompt")
	}
}
func TestChatRejectsForeignUpdatesAndPreservesPartialOutput(t *testing.T) {
	c := newChatSession("", "", nil, nil)
	defer c.Close()
	c.sessionID = "s1"
	event := func(sid, kind, id, text string) {
		raw, _ := json.Marshal(map[string]any{"sessionId": sid, "update": map[string]any{"sessionUpdate": kind, "messageId": id, "content": map[string]string{"type": "text", "text": text}}})
		c.event(acp.Packet{Method: "session/update", Params: raw})
	}
	event("foreign", "agent_message_chunk", "x", "wrong")
	event("s1", "agent_thought_chunk", "hidden", "private thought")
	event("s1", "agent_message_chunk", "a", "你好")
	c.fail(fmt.Errorf("Connection lost"))
	s := c.snapshot()
	if len(s.Messages) != 1 || s.Messages[0].Text != "你好" || s.Problem != "Connection lost" {
		t.Fatalf("snapshot: %+v", s)
	}
	if text := truncateText(strings.Repeat("世", 100), 20); !strings.HasSuffix(text, "… Output truncated") {
		t.Fatal(text)
	}
}

// usage_update is fx reporting provider-confirmed context use. The strip
// must show it, and a later update that omits the window size must not erase
// the size fx already told us.
func TestChatTracksContextUsage(t *testing.T) {
	c := newChatSession("", "", nil, nil)
	defer c.Close()
	c.sessionID = "s1"
	usage := func(sid string, used, size int) {
		update := map[string]any{"sessionUpdate": "usage_update", "used": used}
		if size > 0 {
			update["size"] = size
		}
		raw, _ := json.Marshal(map[string]any{"sessionId": sid, "update": update})
		c.event(acp.Packet{Method: "session/update", Params: raw})
	}
	usage("foreign", 999, 999)
	if s := c.snapshot(); s.ContextUsed != 0 || s.ContextTotal != 0 {
		t.Fatalf("a foreign session's usage leaked in: %+v", s)
	}
	usage("s1", 12000, 200000)
	if s := c.snapshot(); s.ContextUsed != 12000 || s.ContextTotal != 200000 {
		t.Fatalf("usage not tracked: %+v", c.snapshot())
	}
	usage("s1", 13000, 0)
	if s := c.snapshot(); s.ContextUsed != 13000 || s.ContextTotal != 200000 {
		t.Fatalf("a size-less update must keep the known window: %+v", c.snapshot())
	}
}

// Every completed prompt reports that turn's tokens, and that is the only
// token count providers that `fx usage` cannot read still supply. The
// conversation's total is the sum of those turns, and it must survive a
// restart with the context window it last saw.
func TestChatAccumulatesAndPersistsTurnUsage(t *testing.T) {
	testutil.IsolateHome(t)
	store, err := sessionstats.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := chatFixtureSetup(t, func(c *chatSession) { c.stats = store })
	turn := func(text string) {
		if !c.submit(text, nil, nil) {
			t.Fatalf("submit %q refused", text)
		}
		s := awaitChat(t, c, func(s chatSnapshot) bool { return s.Permission != nil })
		c.choosePermission(s.Permission.ID, "allow_once")
		awaitChat(t, c, func(s chatSnapshot) bool { return !s.Busy })
	}

	turn("first")
	if s := c.snapshot(); s.InputTokens != 1234 || s.OutputTokens != 56 {
		t.Fatalf("first turn usage: in=%d out=%d", s.InputTokens, s.OutputTokens)
	}
	turn("second")
	if s := c.snapshot(); s.InputTokens != 2468 || s.OutputTokens != 112 {
		t.Fatalf("turns were not summed: in=%d out=%d", s.InputTokens, s.OutputTokens)
	}
	// The fixture reports the window immediately before each prompt
	// result, exactly as fx does.
	if s := c.snapshot(); s.ContextUsed != 4321 || s.ContextTotal != 200000 {
		t.Fatalf("context usage: %d/%d", s.ContextUsed, s.ContextTotal)
	}

	// The turn-end save runs after busy is cleared, so the record lands a
	// moment after the transcript settles; wait for it rather than assuming
	// the disk won the race.
	var st sessionstats.Stats
	deadline := time.Now().Add(5 * time.Second)
	for {
		saved, err := sessionstats.Load()
		if err != nil {
			t.Fatal(err)
		}
		st = saved.Get("fixture-session")
		if st.InputTokens == 2468 && st.OutputTokens == 112 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tokens were not persisted: %+v", st)
		}
		time.Sleep(time.Millisecond)
	}
	if st.ContextUsed != 4321 || st.ContextTotal != 200000 {
		t.Fatalf("context was not persisted: %+v", st)
	}
	if st.UpdatedMS == 0 {
		t.Fatalf("persisted record has no timestamp: %+v", st)
	}
}

// session/load replays no usage_update, so a resumed conversation takes its
// last numbers from the app's record, then adds the next turn to them.
func TestChatRestoresUsageOnResume(t *testing.T) {
	testutil.IsolateHome(t)
	store, err := sessionstats.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("fixture-session", sessionstats.Stats{
		ContextUsed: 777, ContextTotal: 1000, InputTokens: 111, OutputTokens: 22,
	}); err != nil {
		t.Fatal(err)
	}
	c := chatFixtureSetup(t, func(c *chatSession) { c.stats = store }, "--resume", "fixture-session")
	if s := c.snapshot(); s.ContextUsed != 777 || s.ContextTotal != 1000 {
		t.Fatalf("context was not restored: %d/%d", s.ContextUsed, s.ContextTotal)
	}
	if s := c.snapshot(); s.InputTokens != 111 || s.OutputTokens != 22 {
		t.Fatalf("tokens were not restored: in=%d out=%d", s.InputTokens, s.OutputTokens)
	}

	// The fixture emits no usage on load, so these values can only have come
	// from the record. One turn then accumulates on top of them.
	if !c.submit("continue", nil, nil) {
		t.Fatal("submit refused")
	}
	s := awaitChat(t, c, func(s chatSnapshot) bool { return s.Permission != nil })
	c.choosePermission(s.Permission.ID, "allow_once")
	s = awaitChat(t, c, func(s chatSnapshot) bool { return !s.Busy })
	if s.InputTokens != 111+1234 || s.OutputTokens != 22+56 {
		t.Fatalf("restored totals were not accumulated onto: in=%d out=%d", s.InputTokens, s.OutputTokens)
	}
}

// The same native view and callbacks used by the app complete an ACP turn.
func TestChatViewProtocolRoundTrip(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	session := &Session{coord: NewCoordinator(), project: chat.project, chat: chat, started: time.Now(), shell: chrome.DefaultShell()}
	ts := ui.NewTester(session.view, 1000, 800)
	if err := ts.Click("Message fx"); err != nil {
		t.Fatal(err)
	}
	ts.Type("Inspect main.go")
	ts.Key(0, ui.KeyEnter)
	awaitChat(t, chat, func(s chatSnapshot) bool { return s.Permission != nil })
	ts.Frame()
	if !ts.HasText("Approval required") {
		t.Fatal("protocol approval did not appear in the view")
	}
	if err := ts.Click("Reject"); err != nil {
		t.Fatal(err)
	}
	awaitChat(t, chat, func(s chatSnapshot) bool { return !s.Busy })
	ts.Frame()
	if !ts.HasText("failed") || !ts.HasText("Hello 世界") || !ts.HasText("Inspect main.go") {
		t.Fatalf("round trip not visible: %v", ts.Texts())
	}
	if err := ts.Click("Copy response"); err != nil {
		t.Fatal(err)
	}
	if ts.Clipboard() != "Hello 世界\n\n**Done.**" {
		t.Fatalf("copied reply: %q", ts.Clipboard())
	}
}

func TestChatAppliesLaunchProviderEffortAndSpeedThroughConfig(t *testing.T) {
	c := chatFixtureSetup(t, nil, "--provider", "other", "--effort", "high", "--fast", "--ultrafast")
	s := awaitChat(t, c, func(s chatSnapshot) bool {
		option, ok := findConfigOption(s.Configs, "provider")
		return ok && option.CurrentValue == "other" && s.Effort == "high" && s.Fast && s.Ultra
	})
	if s.AdvancedProblem != "" {
		t.Fatalf("launch settings were rejected: %s", s.AdvancedProblem)
	}
}

func TestChatSwitchKeepsProjectWindow(t *testing.T) {
	testutil.IsolateHome(t)
	previous := chatFixture(t)
	coord := NewCoordinator()
	coord.fxPath = previous.executable
	session := &Session{coord: coord, project: previous.project, chat: previous, shell: chrome.DefaultShell()}
	coord.addSession(session)
	session.replaceConversation([]string{"--resume", "fixture-session"})
	t.Cleanup(session.chat.Close)
	snapshot := awaitChat(t, session.chat, func(s chatSnapshot) bool { return s.Ready })
	if coord.sessionCount() != 1 || session.chat == previous || len(snapshot.Messages) != 2 {
		t.Fatal("switch must replay in the same project session")
	}
	previous.mu.Lock()
	closed := previous.closed
	previous.mu.Unlock()
	if !closed {
		t.Fatal("previous agent process was left active")
	}
}

// A reply that keeps arriving must cost its own chunks, not the whole reply
// again: appending with += is quadratic in the reply's length (§4.10.2).
func TestStreamingReplyAppendStaysLinear(t *testing.T) {
	c := &chatSession{messages: []chrome.ChatMessage{{Role: "assistant"}}}
	const chunks = 20000
	chunk := "abcdefgh"
	allocs := testing.AllocsPerRun(5, func() {
		c.messages[0].Text = ""
		c.streams = nil
		for i := 0; i < chunks; i++ {
			c.appendStreamText(0, chunk)
		}
	})
	if allocs > 200 {
		t.Fatalf("streaming %d chunks allocated %.0f times per run; want at most 200", chunks, allocs)
	}
	if got, want := c.messages[0].Text, strings.Repeat(chunk, chunks); got != want {
		t.Fatalf("the assembled reply is %d bytes; want %d", len(got), len(want))
	}
}

// A frame must not allocate a copy of a session-sized transcript. The view
// keeps its own slice and copies only changed rows; snapshot() remains the
// independent copy for callers that need one.
func TestChatViewSnapshotDoesNotCopyTranscript(t *testing.T) {
	messages := make([]chrome.ChatMessage, 5000)
	for i := range messages {
		messages[i] = chrome.ChatMessage{ID: uint64(i + 1), Role: "assistant", Text: "a reply", Done: true}
	}
	c := &chatSession{messages: messages, byID: map[string]int{}}
	c.snapshotForView()
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			c.snapshotForView()
		}
	})
	if got := result.AllocedBytesPerOp(); got > 64<<10 {
		t.Fatalf("a view frame allocated %d bytes for %d messages; the transcript is being copied", got, len(messages))
	}
}

// The rendered transcript is updated in place between frames, while the
// snapshot callers can keep stays an independent copy.
func TestChatViewTranscriptUpdatesIncrementally(t *testing.T) {
	c := &chatSession{
		byID:     map[string]int{},
		messages: []chrome.ChatMessage{{ID: 1, Role: "user", Text: "one"}},
	}
	first := c.snapshotForView()
	if len(first.Messages) != 1 || first.Messages[0].Text != "one" {
		t.Fatalf("initial view: %+v", first.Messages)
	}
	kept := c.snapshot()

	c.mu.Lock()
	c.appendStreamText(0, "!")
	c.appendLocked("assistant", "assistant:a", "")
	c.appendStreamText(1, "hello")
	c.sessionID = "s"
	c.mu.Unlock()
	view := c.snapshotForView()
	if len(view.Messages) != 2 || view.Messages[0].Text != "one!" || view.Messages[1].Text != "hello" {
		t.Fatalf("streaming view: %+v", view.Messages)
	}
	if kept.Messages[0].Text != "one" {
		t.Fatalf("snapshot() aliased the mutable transcript: %+v", kept.Messages)
	}

	c.event(acp.Packet{Method: "session/update", Params: json.RawMessage(`{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"tool-1","title":"Run tests","status":"pending"}}`)})
	view = c.snapshotForView()
	if len(view.Messages) != 3 || view.Messages[2].Title != "Run tests" || view.Messages[2].Role != "tool" {
		t.Fatalf("tool row did not reach the view: %+v", view.Messages)
	}

	c.fail(fmt.Errorf("boom"))
	view = c.snapshotForView()
	for _, m := range view.Messages {
		if !m.Done {
			t.Fatalf("a finished turn left a live message: %+v", view.Messages)
		}
	}
}

// The status read that fans out to the rail must describe the same session as
// the transcript snapshot, or the two surfaces would disagree.
func TestChatStatusMatchesSnapshot(t *testing.T) {
	c := &chatSession{
		ready: true, busy: true, stopping: true, configuring: true,
		steering: true, images: true, files: true,
		queued:      []queuedPrompt{{text: "waiting"}, {text: "and waiting"}},
		contextUsed: 1200, contextTotal: 8192,
		problem: "boom", model: "gpt", mode: "ask", sessionID: "session-1", title: "A title",
		permission: &chrome.ChatPermission{ID: "p1"},
		messages:   []chrome.ChatMessage{{ID: 1}, {ID: 2}},
	}
	st := c.status()
	s := c.snapshot()
	if st.Ready != s.Ready || st.Busy != s.Busy || st.Stopping != s.Stopping || st.ConfigChanging != s.ConfigChanging {
		t.Fatalf("status flags %+v do not match the snapshot", st)
	}
	if st.Steering != s.Steering || st.Images != s.Images || st.Files != s.Files || st.Queued != s.Queued {
		t.Fatalf("status capabilities %+v do not match the snapshot", st)
	}
	if st.ContextUsed != s.ContextUsed || st.ContextTotal != s.ContextTotal {
		t.Fatalf("status context %+v does not match the snapshot", st)
	}
	if st.Problem != s.Problem || st.Model != s.Model || st.Mode != s.Mode || st.SessionID != s.SessionID || st.SessionTitle != s.SessionTitle {
		t.Fatalf("status identity %+v does not match the snapshot", st)
	}
	if st.MessageCount != len(s.Messages) {
		t.Fatalf("status counts %d messages; snapshot has %d", st.MessageCount, len(s.Messages))
	}
	if !st.Permission || s.Permission == nil {
		t.Fatalf("status permission = %v; snapshot permission = %v", st.Permission, s.Permission)
	}
}
