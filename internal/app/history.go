package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/fxsessions"
)

type fxSessionPage struct {
	Sessions   []FxSession `json:"sessions"`
	HasMore    bool        `json:"has_more"`
	NextCursor string      `json:"next_cursor"`
}

func readSessionPage(ctx context.Context, executable, dir string, all bool, limit int, cursor string) (fxSessionPage, error) {
	var page fxSessionPage
	if limit < 1 || limit > 100 || strings.ContainsRune(cursor, 0) {
		return page, fmt.Errorf("Invalid history page request")
	}
	args := []string{"sessions", "--limit", fmt.Sprint(limit)}
	if all {
		args = append(args, "--all")
	}
	if cursor != "" {
		args = append(args, "--cursor", cursor)
	}
	args = append(args, "--json")
	text, err := managementRead(ctx, executable, dir, args...)
	if err != nil {
		return page, err
	}
	if err = json.Unmarshal([]byte(text), &page); err != nil {
		return page, fmt.Errorf("Invalid history response: %w", err)
	}
	if page.Sessions == nil {
		return page, fmt.Errorf("History response is missing sessions")
	}
	if page.HasMore && page.NextCursor == "" || page.NextCursor != "" && page.NextCursor == cursor {
		return page, fmt.Errorf("History returned an invalid pagination cursor")
	}
	sort.SliceStable(page.Sessions, func(i, j int) bool { return page.Sessions[i].UpdatedAtMS > page.Sessions[j].UpdatedAtMS })
	return page, nil
}

type historyRequest struct {
	All           bool
	Query, Cursor string
	Pending       []chrome.HistorySession
	Seen, Cursors map[string]bool
	Scanned       int
	Exhausted     bool
}
type historyResult struct {
	historyRequest
	Entries []chrome.HistorySession
	Problem string
}

// Search walks source pages in bounded batches, retaining surplus matches for
// the next 20-result batch. Errors retain the last successful source cursor.
func readHistoryBatch(ctx context.Context, executable, dir string, request historyRequest) historyResult {
	r := historyResult{historyRequest: request}
	r.Seen = maps.Clone(request.Seen)
	if r.Seen == nil {
		r.Seen = map[string]bool{}
	}
	r.Cursors = maps.Clone(request.Cursors)
	if r.Cursors == nil {
		r.Cursors = map[string]bool{}
	}
	queue := slices.Clone(request.Pending)
	r.Pending = nil
	budget := 1
	if request.Query != "" {
		budget = 10
	}
	for n := 0; n < budget && len(queue) < 20 && !r.Exhausted; n++ {
		page, err := readSessionPage(ctx, executable, dir, request.All, 20, r.Cursor)
		if err != nil {
			r.Problem = err.Error()
			break
		}
		if page.NextCursor != "" && r.Cursors[page.NextCursor] {
			r.Problem = "History cursor repeated; refresh or retry."
			break
		}
		r.Cursors[r.Cursor] = true
		r.Scanned += len(page.Sessions)
		for _, entry := range page.Sessions {
			if entry.ID == "" || r.Seen[entry.ID] {
				continue
			}
			r.Seen[entry.ID] = true
			title := entry.Title
			if title == "" {
				title = entry.ID
			}
			if !strings.Contains(strings.ToLower(title), strings.ToLower(request.Query)) {
				continue
			}
			queue = append(queue, chrome.HistorySession{ID: entry.ID, Title: entry.Title, Workspace: entry.WorkspaceRoot, UpdatedAtMS: entry.UpdatedAtMS})
		}
		r.Cursor = page.NextCursor
		r.Exhausted = page.NextCursor == ""
	}
	r.Entries = slices.Clone(queue[:min(20, len(queue))])
	r.Pending = slices.Clone(queue[min(20, len(queue)):])
	return r
}

// readAllHistorySessions walks every page in one scope. Clear-all must not be
// limited to the rows already loaded or to the current search. A repeated
// cursor is an error rather than a loop: the source may be slow, never
// infinite.
func readAllHistorySessions(ctx context.Context, executable, dir string, all bool) ([]FxSession, error) {
	var sessions []FxSession
	seenIDs := map[string]bool{}
	seenCursors := map[string]bool{}
	cursor := ""
	for {
		if seenCursors[cursor] {
			return nil, fmt.Errorf("History cursor repeated; refresh or retry.")
		}
		seenCursors[cursor] = true
		page, err := readSessionPage(ctx, executable, dir, all, 100, cursor)
		if err != nil {
			return nil, err
		}
		for _, session := range page.Sessions {
			if session.ID == "" || seenIDs[session.ID] {
				continue
			}
			seenIDs[session.ID] = true
			sessions = append(sessions, session)
		}
		if !page.HasMore {
			return sessions, nil
		}
		cursor = page.NextCursor
	}
}

func (c *Coordinator) openHistory(s *Session) {
	s.shell.Tools.Open = false
	state := &s.shell.History
	generation := state.Generation + 1
	*state = chrome.HistoryState{Open: true, Generation: generation}
	c.loadHistory(s, true)
}

func (c *Coordinator) sessionHistory(s *Session) chrome.HistoryHost {
	h := chrome.HistoryHost{
		Run:    func(action string) { mygo.App.WhenReady(func() { c.loadHistory(s, action != "more") }) },
		Resume: c.sessionManagement(s).Resume,
		Delete: func(id, title string) { c.deleteHistorySession(s, id, title) },
		Clear:  func(all bool) { c.clearHistory(s, all) },
		Live:   c.liveSessionIDs(),
	}
	if s.chat != nil {
		h.ActiveID = s.chat.status().SessionID
	}
	return h
}

// liveSessionIDs names conversations open in any app window. History may
// still show their rows, but deletion must wait until the owning window
// closes: fx is writing that directory. Call this on the app loop, where
// Session.chat is assigned.
func (c *Coordinator) liveSessionIDs() map[string]bool {
	ids := map[string]bool{}
	for _, s := range c.sessionsSnapshot() {
		if s.chat == nil {
			continue
		}
		if id := s.chat.status().SessionID; id != "" {
			ids[id] = true
		}
	}
	return ids
}

// deletableHistoryIDs drops conversations that are open in a window. It also
// drops an empty ID, which is not a row fx could have produced but must never
// become a path.
func deletableHistoryIDs(sessions []FxSession, live map[string]bool) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if session.ID == "" || live[session.ID] {
			continue
		}
		ids = append(ids, session.ID)
	}
	return ids
}

// deleteHistorySessions performs the disk work after a confirmation. It also
// forgets the conversations' saved usage; that record is keyed by session ID,
// so leaving it behind would let a later stats cap evict live entries while
// naming conversations that no longer exist.
func (c *Coordinator) deleteHistorySessions(ids []string) ([]string, error) {
	deleted, err := fxsessions.DeleteMany(ids)
	if len(deleted) > 0 {
		if statsErr := c.stats.Delete(deleted...); statsErr != nil {
			log.Println("session stats:", statsErr)
		}
	}
	return deleted, err
}

// historySessionName makes a session title safe for one line of a native
// dialog. Titles come from prompts, so they can contain newlines and can be
// arbitrarily long; an empty title falls back to the ID.
func historySessionName(title, id string) string {
	name := strings.Join(strings.Fields(title), " ")
	if name == "" {
		name = id
	}
	const maxLen = 120
	if runes := []rune(name); len(runes) > maxLen {
		name = string(runes[:maxLen-1]) + "…"
	}
	return name
}

func (c *Coordinator) deleteHistorySession(s *Session, id, title string) {
	mygo.App.WhenReady(func() {
		w := s.winSnapshot()
		if w == nil || w.IsDestroyed() {
			return
		}
		if !fxsessions.ValidID(id) {
			mygo.Dialog.Error("Delete saved session", "This saved session has an invalid ID.")
			return
		}
		// The row is disabled for live sessions; check again because another
		// window may have resumed this conversation since that frame.
		if c.liveSessionIDs()[id] {
			mygo.Dialog.Error("Delete saved session", "This conversation is open in a window. Close it before deleting it.")
			return
		}
		name := historySessionName(title, id)
		result, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:        w,
			Type:          mygo.MessageWarning,
			Title:         "Delete saved session",
			Message:       "Delete \"" + name + "\"?",
			Detail:        "This permanently removes the conversation, its tool output and stored files. It cannot be undone.",
			Buttons:       []string{"Cancel", "Delete"},
			DefaultButton: 0,
			CancelButton:  0,
		})
		if err != nil || result.Button != 1 {
			return
		}
		state := &s.shell.History
		if state.Deleting {
			return
		}
		state.Deleting = true
		s.invalidate()
		go func() {
			_, delErr := c.deleteHistorySessions([]string{id})
			mygo.App.WhenReady(func() {
				if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
					return
				}
				state := &s.shell.History
				state.Deleting = false
				if delErr != nil {
					mygo.Dialog.Error("Delete saved session", delErr.Error())
				}
				c.refreshSavedLists()
				c.loadHistory(s, true)
			})
		}()
	})
}

// clearHistory confirms and then deletes every saved conversation in the
// current History scope: this workspace, or all workspaces when the panel's
// toggle is on. The list is re-read from fx rather than taken from the UI, so
// unloaded pages and search-hidden sessions are included. Conversations open
// in a window are always kept.
func (c *Coordinator) clearHistory(s *Session, all bool) {
	mygo.App.WhenReady(func() {
		w := s.winSnapshot()
		if w == nil || w.IsDestroyed() {
			return
		}
		message := "Delete all saved sessions in this workspace?"
		if all {
			message = "Delete all saved sessions across every workspace?"
		}
		result, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:        w,
			Type:          mygo.MessageWarning,
			Title:         "Clear session history",
			Message:       message,
			Detail:        "Their transcripts, tool output and stored files will be permanently removed. Conversations open in a window are kept. This cannot be undone.",
			Buttons:       []string{"Cancel", "Delete all"},
			DefaultButton: 0,
			CancelButton:  0,
		})
		if err != nil || result.Button != 1 {
			return
		}
		state := &s.shell.History
		if state.Deleting {
			return
		}
		state.Deleting = true
		s.invalidate()
		executable, dir := c.fxPathSnapshot(), s.project
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), fxTimeout)
			defer cancel()
			sessions, listErr := readAllHistorySessions(ctx, executable, dir, all)
			mygo.App.WhenReady(func() {
				if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
					return
				}
				state := &s.shell.History
				if listErr != nil {
					state.Deleting = false
					mygo.Dialog.Error("Clear session history", listErr.Error())
					s.invalidate()
					return
				}
				ids := deletableHistoryIDs(sessions, c.liveSessionIDs())
				if len(ids) == 0 {
					state.Deleting = false
					s.invalidate()
					_, _ = mygo.Dialog.Message(mygo.MessageOptions{
						Parent:  w,
						Type:    mygo.MessageInfo,
						Title:   "Clear session history",
						Message: "No sessions were deleted",
						Detail:  "No closed saved sessions were found. Conversations open in a window are kept.",
					})
					return
				}
				go func() {
					_, delErr := c.deleteHistorySessions(ids)
					mygo.App.WhenReady(func() {
						if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
							return
						}
						state := &s.shell.History
						state.Deleting = false
						if delErr != nil {
							mygo.Dialog.Error("Clear session history", delErr.Error())
						}
						c.refreshSavedLists()
						c.loadHistory(s, true)
					})
				}()
			})
		}()
	})
}

func applyHistoryResult(state *chrome.HistoryState, generation uint64, result historyResult) bool {
	if !state.Open || state.Generation != generation {
		return false
	}
	state.Busy = false
	state.Loaded = true
	state.Entries = append(state.Entries, result.Entries...)
	sort.SliceStable(state.Entries, func(i, j int) bool { return state.Entries[i].UpdatedAtMS > state.Entries[j].UpdatedAtMS })
	state.Pending, state.Seen, state.Cursors = result.Pending, result.Seen, result.Cursors
	state.Cursor, state.Exhausted, state.Scanned, state.Problem = result.Cursor, result.Exhausted, result.Scanned, result.Problem
	return true
}

func (c *Coordinator) loadHistory(s *Session, reset bool) {
	if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
		return
	}
	state := &s.shell.History
	if !state.Open {
		return
	}
	if reset {
		open, all, query, generation := state.Open, state.All, strings.TrimSpace(state.DraftQuery), state.Generation
		*state = chrome.HistoryState{Open: open, All: all, DraftQuery: query, Query: query, Generation: generation}
	} else if state.Busy || state.Loaded && !state.HasMore() && state.Problem == "" {
		return
	}
	state.Generation++
	generation := state.Generation
	state.Busy = true
	state.Problem = ""
	req := historyRequest{All: state.All, Query: state.Query, Cursor: state.Cursor, Pending: slices.Clone(state.Pending), Seen: maps.Clone(state.Seen), Cursors: maps.Clone(state.Cursors), Scanned: state.Scanned, Exhausted: state.Exhausted}
	executable, dir := c.fxPathSnapshot(), s.project
	s.invalidate()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), fxTimeout)
		defer cancel()
		result := readHistoryBatch(ctx, executable, dir, req)
		mygo.App.WhenReady(func() {
			if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
				return
			}
			if !applyHistoryResult(&s.shell.History, generation, result) {
				return
			}
			s.invalidate()
		})
	}()
}
