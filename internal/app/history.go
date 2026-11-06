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
	*state = chrome.HistoryState{Open: true, Paging: history.Paging{Generation: generation}}
	c.loadHistory(s, true)
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
		// The user confirmed; now take the claim. Between the dialog and here
		// another window may have resumed this conversation — the claim is
		// what makes "one of the two" true instead of "both, in sequence"
		// (T9).
		finish, claimed := c.sessionUse.beginDelete(id)
		if !claimed {
			mygo.Dialog.Error("Delete saved session", "This conversation is open in a window. Close it before deleting it.")
			return
		}
		state := &s.shell.History
		if state.Deleting {
			finish()
			return
		}
		state.Deleting = true
		s.invalidate()
		go func() {
			_, delErr := c.deleteHistorySessions([]string{id})
			// Release on every path: a failed deletion must not leave the
			// ID unusable (T9).
			finish()
			s.dispatch(func() {
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
		state := &s.shell.History
		if state.Deleting {
			return
		}
		state.Deleting = true
		s.invalidate()
		executable, dir := c.fxPathSnapshot(), s.project
		go func() {
			ctx, cancel := context.WithTimeout(s.lifetime(), fxTimeout)
			defer cancel()
			sessions, listErr := readAllHistorySessions(ctx, executable, dir, all)
			s.dispatch(func() {
				state := &s.shell.History
				if listErr != nil {
					state.Deleting = false
					mygo.Dialog.Error("Clear session history", listErr.Error())
					s.invalidate()
					return
				}
				ids := deletableHistoryIDs(sessions, c.liveSessionIDs())
				reserved, releases := c.reserveDeletes(ids)
				if len(reserved) == 0 {
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
					_, delErr := c.deleteHistorySessions(reserved)
					// Every claimed ID is released, whatever happened on
					// disk: a partial failure must not leave the survivors
					// undeletable (T9).
					for _, release := range releases {
						release()
					}
					s.dispatch(func() {
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
