package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/fxsessions"
	"fx-desktop/internal/history"
)

type fxSessionPage struct {
	Sessions   []FxSession `json:"sessions"`
	HasMore    bool        `json:"has_more"`
	NextCursor string      `json:"next_cursor"`
}

// readSessionPage reads and validates one page from fx. Validation lives here
// because it is about the wire — a malformed answer is an adapter problem, not
// a search rule.
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
	text, err := managementReadJSON(ctx, executable, dir, args...)
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

// historyReader adapts a specific fx/workspace pair to history.Reader, so the
// rules never hold an executable path or a directory of their own.
func historyReader(executable, dir string) history.Reader {
	return func(ctx context.Context, all bool, limit int, cursor string) (history.Page, error) {
		page, err := readSessionPage(ctx, executable, dir, all, limit, cursor)
		if err != nil {
			return history.Page{}, err
		}
		records := make([]history.Record, 0, len(page.Sessions))
		for _, entry := range page.Sessions {
			records = append(records, history.Record{
				ID: entry.ID, Title: entry.Title, WorkspaceRoot: entry.WorkspaceRoot, UpdatedAtMS: entry.UpdatedAtMS, Turns: entry.HistoryLen,
			})
		}
		return history.Page{Sessions: records, HasMore: page.HasMore, NextCursor: page.NextCursor}, nil
	}
}

// readHistoryBatch is the app's name for one bounded search batch.
func readHistoryBatch(ctx context.Context, executable, dir string, request history.Request) history.Result {
	return history.ReadBatch(ctx, historyReader(executable, dir), request)
}

// readAllHistorySessions walks every page in one scope, for clear-all.
func readAllHistorySessions(ctx context.Context, executable, dir string, all bool) ([]FxSession, error) {
	records, err := history.ReadAll(ctx, historyReader(executable, dir), all)
	if err != nil {
		return nil, err
	}
	sessions := make([]FxSession, 0, len(records))
	for _, record := range records {
		sessions = append(sessions, FxSession{ID: record.ID, Title: record.Title, WorkspaceRoot: record.WorkspaceRoot, UpdatedAtMS: record.UpdatedAtMS})
	}
	return sessions, nil
}

// deletableHistoryIDs names the rows clear-all may delete.
func deletableHistoryIDs(sessions []FxSession, live map[string]bool) []string {
	records := make([]history.Record, 0, len(sessions))
	for _, session := range sessions {
		records = append(records, history.Record{ID: session.ID})
	}
	return history.Deletable(records, live)
}

// historySessionName makes a title safe for one line of a native dialog.
func historySessionName(title, id string) string { return history.SessionName(title, id) }

func (c *Coordinator) openHistory(s *ProjectWindow) {
	s.shell.Tools.Open = false
	state := &s.shell.History
	generation := state.Generation + 1
	// The panel's state is rebuilt on every open, but a deletion belongs to
	// the window, not to one showing of the panel: closing and reopening while
	// one runs must not re-enable the delete controls (r10 §2).
	*state = chrome.HistoryState{Open: true, Deleting: s.historyDelete.busy(), Paging: history.Paging{Generation: generation}}
	c.loadHistory(s, true)
}

// historyDeleteGate is the window's one History deletion — a single delete or
// a clear-all. It lives on the window rather than in chrome.HistoryState
// because the panel's state is replaced each time the panel opens, while the
// work it started keeps running. App loop only.
//
// active names the operation in flight; zero means none. A completion callback
// ends only the operation it belongs to, so a callback that outlived its own
// operation cannot unlock a newer one.
type historyDeleteGate struct {
	seq, active uint64
}

func (g *historyDeleteGate) busy() bool { return g.active != 0 }

// beginHistoryDelete admits one deletion for this window, or refuses while
// another is in flight. The returned operation is what must be handed back.
func (s *ProjectWindow) beginHistoryDelete() (op uint64, ok bool) {
	gate := &s.historyDelete
	if gate.busy() {
		return 0, false
	}
	gate.seq++
	gate.active = gate.seq
	s.shell.History.Deleting = true
	return gate.active, true
}

// endHistoryDelete finishes op. It reports false, and changes nothing, when op
// is not the operation in flight.
func (s *ProjectWindow) endHistoryDelete(op uint64) bool {
	gate := &s.historyDelete
	if op == 0 || gate.active != op {
		return false
	}
	gate.active = 0
	s.shell.History.Deleting = false
	return true
}

// historyDeleteSeams replaces the deletion's I/O and native dialogs. Every nil
// field means the real thing; a test supplies fakes it can hold at each stage.
type historyDeleteSeams struct {
	list   func(ctx context.Context, executable, dir string, all bool) ([]FxSession, error)
	remove func(ids []string) ([]string, error)
	alert  func(title, message string)
	inform func(s *ProjectWindow, title, message, detail string)
}

func (c *Coordinator) listHistorySessions(ctx context.Context, executable, dir string, all bool) ([]FxSession, error) {
	if c.historySeams.list != nil {
		return c.historySeams.list(ctx, executable, dir, all)
	}
	return readAllHistorySessions(ctx, executable, dir, all)
}

func (c *Coordinator) removeHistorySessions(ids []string) ([]string, error) {
	if c.historySeams.remove != nil {
		return c.historySeams.remove(ids)
	}
	return c.deleteHistorySessions(ids)
}

func (c *Coordinator) historyAlert(title, message string) {
	if c.historySeams.alert != nil {
		c.historySeams.alert(title, message)
		return
	}
	mygo.Dialog.Error(title, message)
}

func (c *Coordinator) historyInform(s *ProjectWindow, title, message, detail string) {
	if c.historySeams.inform != nil {
		c.historySeams.inform(s, title, message, detail)
		return
	}
	_, _ = mygo.Dialog.Message(mygo.MessageOptions{
		Parent: s.winSnapshot(), Type: mygo.MessageInfo, Title: title, Message: message, Detail: detail,
	})
}

const historyOpenElsewhere = "This conversation is open in a window. Close it before deleting it."

// startHistoryDelete runs one confirmed deletion. It runs on the app loop and
// reports whether the work started. The window's gate is taken first, so a
// refused second deletion never touches the registry; the registry claim then
// decides between this deletion and a window that resumed the conversation
// while the dialog was up (T9).
func (c *Coordinator) startHistoryDelete(s *ProjectWindow, id string) bool {
	op, ok := s.beginHistoryDelete()
	if !ok {
		return false
	}
	finish, claimed := c.sessionUse.beginDelete(id)
	if !claimed {
		s.endHistoryDelete(op)
		c.historyAlert("Delete saved session", historyOpenElsewhere)
		return false
	}
	s.invalidate()
	go func() {
		_, delErr := c.removeHistorySessions([]string{id})
		// Release on every path, and before the callback: a failed deletion
		// must not leave the ID unusable, and neither must a window that
		// closed while the files were being removed (T9).
		finish()
		s.dispatch(func() { c.finishHistoryDelete(s, op, "Delete saved session", delErr) })
	}()
	return true
}

// startHistoryClear runs one confirmed clear-all through the same gate. The
// list is re-read from fx rather than taken from the UI, so unloaded pages and
// search-hidden sessions are included.
func (c *Coordinator) startHistoryClear(s *ProjectWindow, all bool) bool {
	op, ok := s.beginHistoryDelete()
	if !ok {
		return false
	}
	s.invalidate()
	executable, dir := c.fxPathSnapshot(), s.project
	go func() {
		ctx, cancel := context.WithTimeout(s.lifetime(), fxTimeout)
		sessions, listErr := c.listHistorySessions(ctx, executable, dir, all)
		cancel()
		s.dispatch(func() {
			if s.lifetime().Err() != nil {
				// The window closed while the list was being read: nothing
				// was claimed, and nothing is deleted on its behalf.
				s.endHistoryDelete(op)
				return
			}
			if listErr != nil {
				s.endHistoryDelete(op)
				s.invalidate()
				c.historyAlert("Clear session history", listErr.Error())
				return
			}
			ids := deletableHistoryIDs(sessions, c.liveSessionIDs())
			reserved, releases := c.reserveDeletes(ids)
			if len(reserved) == 0 {
				s.endHistoryDelete(op)
				s.invalidate()
				c.historyInform(s, "Clear session history", "No sessions were deleted",
					"No closed saved sessions were found. Conversations open in a window are kept.")
				return
			}
			go func() {
				_, delErr := c.removeHistorySessions(reserved)
				// Every claimed ID is released, whatever happened on disk: a
				// partial failure must not leave the survivors undeletable
				// (T9).
				for _, release := range releases {
					release()
				}
				s.dispatch(func() { c.finishHistoryDelete(s, op, "Clear session history", delErr) })
			}()
		})
	}()
	return true
}

// finishHistoryDelete is the one way a deletion that reached the disk ends. It
// unlocks the window only for its own operation, then refreshes what the
// deletion may have changed.
func (c *Coordinator) finishHistoryDelete(s *ProjectWindow, op uint64, title string, err error) {
	if !s.endHistoryDelete(op) {
		return
	}
	if err != nil {
		c.historyAlert(title, err.Error())
	}
	c.refreshSavedLists()
	c.loadHistory(s, true)
	s.invalidate()
}

func (c *Coordinator) sessionHistory(s *ProjectWindow) chrome.HistoryHost {
	h := chrome.HistoryHost{
		Run:    func(action string) { mygo.App.WhenReady(func() { c.loadHistory(s, action != "more") }) },
		Resume: c.sessionManagement(s).Resume,
		Delete: func(id, title string) { c.deleteHistorySession(s, id, title) },
		Clear:  func(all bool) { c.clearHistory(s, all) },
		Live:   c.liveSessionIDs(),
		Rel:    func(ms int64) string { return relTime(ms, time.Now()) },
	}
	if s.chat != nil {
		h.ActiveID = s.chat.status().SessionID
	}
	return h
}

// liveSessionIDs names the conversations this app may still write to: the ones
// open in a window, the ones still starting, and the ones whose window is gone
// but whose fx process is not yet confirmed reaped. It is read from the app's
// session registry rather than from the window list, because a window
// disappears before its process does (T9, R3).
func (c *Coordinator) liveSessionIDs() map[string]bool {
	return c.sessionUse.snapshot()
}

// reserveDeletes claims the IDs a clear-all is about to remove. An ID a window
// opened after the list was read is skipped rather than deleted under it, and
// the claim itself refuses a restore for the whole deletion (T9).
func (c *Coordinator) reserveDeletes(ids []string) (reserved []string, releases []func()) {
	for _, id := range ids {
		finish, ok := c.sessionUse.beginDelete(id)
		if !ok {
			continue
		}
		reserved = append(reserved, id)
		releases = append(releases, finish)
	}
	return reserved, releases
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

func (c *Coordinator) deleteHistorySession(s *ProjectWindow, id, title string) {
	mygo.App.WhenReady(func() {
		w := s.winSnapshot()
		if w == nil || w.IsDestroyed() {
			return
		}
		if !fxsessions.ValidID(id) {
			mygo.Dialog.Error("Delete saved session", "This saved session has an invalid ID.")
			return
		}
		// The row was disabled for live sessions when the panel drew; ask the
		// registry again because another window may have opened it since.
		// This check is only the early answer — the claim below is the one
		// that decides.
		if c.sessionUse.inUse(id) {
			mygo.Dialog.Error("Delete saved session", historyOpenElsewhere)
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
		c.startHistoryDelete(s, id)
	})
}

// clearHistory confirms and then deletes every saved conversation in the
// current History scope: this workspace, or all workspaces when the panel's
// toggle is on. The list is re-read from fx rather than taken from the UI, so
// unloaded pages and search-hidden sessions are included. Conversations open
// in a window are always kept.
func (c *Coordinator) clearHistory(s *ProjectWindow, all bool) {
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
		c.startHistoryClear(s, all)
	})
}

// applyHistoryResult publishes a batch against the panel it belongs to.
func applyHistoryResult(state *chrome.HistoryState, generation uint64, result history.Result) bool {
	if !state.Open {
		return false
	}
	return state.Apply(generation, result)
}

func (c *Coordinator) loadHistory(s *ProjectWindow, reset bool) {
	if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
		return
	}
	state := &s.shell.History
	if !state.Open {
		return
	}
	if reset {
		open := state.Open
		state.Reset(strings.TrimSpace(state.DraftQuery), state.All)
		state.Open = open
	} else if state.Busy || state.Loaded && !state.HasMore() && state.Problem == "" {
		return
	}
	state.Paging.Generation++
	generation := state.Generation
	state.Busy = true
	state.Problem = ""
	request := state.Request()
	executable, dir := c.fxPathSnapshot(), s.project
	s.invalidate()
	// A newer search or refresh retires this one: the read stops instead of
	// finishing for a result the generation check would drop. The scope is
	// claimed before the worker starts, so the newer query wins the race.
	scope, stop := s.scope("history")
	go func() {
		defer stop()
		ctx, cancel := context.WithTimeout(scope, fxTimeout)
		defer cancel()
		result := readHistoryBatch(ctx, executable, dir, request)
		s.dispatch(func() {
			if !applyHistoryResult(&s.shell.History, generation, result) {
				return
			}
			s.invalidate()
		})
	}()
}
