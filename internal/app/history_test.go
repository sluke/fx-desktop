package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/history"
	"fx-desktop/internal/sessionstats"
	"fx-desktop/internal/testutil"
)

func historyFixture(t *testing.T, pages map[string]fxSessionPage) (string, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fx")
	log := filepath.Join(dir, "calls")
	body := "#!/bin/sh\ncursor=''\nlimit=''\nall=0\nwhile [ $# -gt 0 ]; do\ncase \"$1\" in\n--cursor) cursor=\"$2\"; shift;;\n--limit) limit=\"$2\"; shift;;\n--all) all=1;;\nesac\nshift\ndone\nprintf '%s|%s|%s\\n' \"$limit\" \"$all\" \"$cursor\" >> '" + log + "'\ncase \"$cursor\" in\n"
	for cursor, page := range pages {
		data, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		body += "'" + cursor + "') printf '%s' '" + strings.ReplaceAll(string(data), "'", "'\"'\"'") + "';;\n"
	}
	body += "*) echo 'missing page' >&2;exit 2;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	return script, log
}

func historyPages() map[string]fxSessionPage {
	pages := map[string]fxSessionPage{}
	for p := 0; p < 3; p++ {
		page := fxSessionPage{Sessions: []FxSession{}}
		for i := p * 20; i < min((p+1)*20, 47); i++ {
			title := fmt.Sprintf("match %02d", i)
			if i < 10 {
				title = fmt.Sprintf("other %02d", i)
			}
			page.Sessions = append(page.Sessions, FxSession{ID: fmt.Sprint(i), Title: title, WorkspaceRoot: "/project", UpdatedAtMS: int64(1000 - i)})
		}
		if p < 2 {
			page.HasMore = true
			page.NextCursor = fmt.Sprintf("p%d", p+1)
		}
		cursor := ""
		if p > 0 {
			cursor = fmt.Sprintf("p%d", p)
		}
		pages[cursor] = page
	}
	return pages
}

func TestHistoryPagesUseCursorScopeAndTwentyResultBatches(t *testing.T) {
	fx, log := historyFixture(t, historyPages())
	dir := filepath.Dir(fx)
	req := history.Request{All: true}
	for i, want := range []int{20, 20, 7} {
		r := readHistoryBatch(context.Background(), fx, dir, req)
		if r.Problem != "" || len(r.Entries) != want || r.Scanned != min((i+1)*20, 47) || r.Exhausted != (i == 2) {
			t.Fatalf("page %d: %+v", i, r)
		}
		req = r.Request
	}
	calls, _ := os.ReadFile(log)
	if string(calls) != "20|1|\n20|1|p1\n20|1|p2\n" {
		t.Fatalf("pagination argv lost: %q", calls)
	}
}

func TestHistorySearchFindsLaterPagesAndRetainsSurplus(t *testing.T) {
	fx, log := historyFixture(t, historyPages())
	r := readHistoryBatch(context.Background(), fx, filepath.Dir(fx), history.Request{Query: "MATCH"})
	if r.Problem != "" || len(r.Entries) != 20 || len(r.Pending) != 10 || r.Cursor != "p2" || r.Scanned != 40 {
		t.Fatalf("search first batch: %+v", r)
	}
	next := readHistoryBatch(context.Background(), fx, filepath.Dir(fx), r.Request)
	if next.Problem != "" || len(next.Entries) != 17 || !next.Exhausted || len(next.Pending) != 0 || next.Scanned != 47 {
		t.Fatalf("search next batch: %+v", next)
	}
	ids := map[string]bool{}
	for _, e := range append(r.Entries, next.Entries...) {
		if ids[e.ID] {
			t.Fatal("duplicate search match")
		}
		ids[e.ID] = true
	}
	if len(ids) != 37 {
		t.Fatalf("lost search matches: %d", len(ids))
	}
	calls, _ := os.ReadFile(log)
	if strings.Count(string(calls), "\n") != 3 {
		t.Fatalf("surplus reread pages: %q", calls)
	}
}

func TestHistorySearchBudgetAndRetryPreserveProgress(t *testing.T) {
	pages := historyPages()
	delete(pages, "p1")
	fx, _ := historyFixture(t, pages)
	r := readHistoryBatch(context.Background(), fx, filepath.Dir(fx), history.Request{Query: "missing title"})
	if r.Problem == "" || r.Cursor != "p1" || r.Scanned != 20 || r.Exhausted {
		t.Fatalf("failed page lost retry cursor: %+v", r)
	}
	pages = map[string]fxSessionPage{}
	for i := 0; i < 12; i++ {
		cursor := ""
		if i > 0 {
			cursor = fmt.Sprint(i)
		}
		pages[cursor] = fxSessionPage{Sessions: []FxSession{{ID: fmt.Sprint(i), Title: "other"}}, HasMore: true, NextCursor: fmt.Sprint(i + 1)}
	}
	fx, _ = historyFixture(t, pages)
	r = readHistoryBatch(context.Background(), fx, filepath.Dir(fx), history.Request{Query: "needle"})
	if r.Problem != "" || r.Scanned != 10 || r.Cursor != "10" || r.Exhausted {
		t.Fatalf("search exhausted or exceeded scan budget: %+v", r)
	}
}

func TestHistoryRejectsMalformedCursorAndDeduplicates(t *testing.T) {
	for _, raw := range []string{"{broken", `{"sessions":null}`, `{"kind":"sessions"}`} {
		fx, _ := historyFixture(t, map[string]fxSessionPage{})
		if err := os.WriteFile(fx, []byte("#!/bin/sh\nprintf '%s' '"+raw+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := readSessionPage(context.Background(), fx, filepath.Dir(fx), false, 20, ""); err == nil {
			t.Fatalf("accepted malformed response: %s", raw)
		}
	}
	for _, page := range []fxSessionPage{
		{Sessions: []FxSession{}, HasMore: true},
		{Sessions: []FxSession{}, HasMore: true, NextCursor: "repeat"},
	} {
		fx, _ := historyFixture(t, map[string]fxSessionPage{"repeat": page})
		if _, err := readSessionPage(context.Background(), fx, filepath.Dir(fx), false, 20, "repeat"); err == nil {
			t.Fatal("accepted broken cursor")
		}
	}
	fx, _ := historyFixture(t, map[string]fxSessionPage{"": {Sessions: []FxSession{{ID: "same", Title: "a"}, {ID: "same", Title: "a"}, {ID: "new", Title: "b"}}}})
	r := readHistoryBatch(context.Background(), fx, filepath.Dir(fx), history.Request{})
	if len(r.Entries) != 2 || !r.Exhausted || r.Problem != "" {
		t.Fatalf("duplicates or absent terminal fields: %+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readSessionPage(ctx, fx, filepath.Dir(fx), false, 20, ""); err == nil {
		t.Fatal("cancelled query succeeded")
	}
	fx, _ = historyFixture(t, map[string]fxSessionPage{"p2": {Sessions: []FxSession{}, HasMore: true, NextCursor: "p1"}})
	r = readHistoryBatch(context.Background(), fx, filepath.Dir(fx), history.Request{Cursor: "p2", Cursors: map[string]bool{"p1": true}})
	if r.Problem == "" || r.Cursor != "p2" {
		t.Fatal("history followed a cursor cycle")
	}
}

func TestHistoryPublicationRejectsStaleAndClosedResults(t *testing.T) {
	state := chrome.HistoryState{Open: true, Paging: history.Paging{Busy: true, Generation: 2, Query: "new search"}}
	old := history.Result{Entries: []history.Session{{ID: "old"}}}
	if applyHistoryResult(&state, 1, old) || len(state.Entries) != 0 || !state.Busy {
		t.Fatal("stale response overwrote new search")
	}
	newer := history.Result{Entries: []history.Session{{ID: "new"}}, Request: history.Request{Cursor: "next", Scanned: 20}}
	if !applyHistoryResult(&state, 2, newer) || len(state.Entries) != 1 || state.Entries[0].ID != "new" || state.Busy || state.Cursor != "next" {
		t.Fatalf("current response not published: %+v", state)
	}
	state.Open = false
	if applyHistoryResult(&state, 2, old) || state.Entries[0].ID != "new" {
		t.Fatal("closed history accepted result")
	}
}

func TestRecentHistoryRequestsFiveAndSortsNewestFirst(t *testing.T) {
	page := historyPages()[""]
	for i, j := 0, len(page.Sessions)-1; i < j; i, j = i+1, j-1 {
		page.Sessions[i], page.Sessions[j] = page.Sessions[j], page.Sessions[i]
	}
	fx, log := historyFixture(t, map[string]fxSessionPage{"": page})
	rows, err := fxSessions(context.Background(), fx, filepath.Dir(fx))
	if err != nil || len(rows) != 5 || rows[0].ID != "0" || rows[4].ID != "4" {
		t.Fatalf("recent history not limited and sorted: %+v %v", rows, err)
	}
	calls, _ := os.ReadFile(log)
	if string(calls) != "5|0|\n" {
		t.Fatalf("recent query requested excess history: %q", calls)
	}
}

func TestReadAllHistorySessionsWalksEveryPage(t *testing.T) {
	fx, log := historyFixture(t, historyPages())
	sessions, err := readAllHistorySessions(context.Background(), fx, filepath.Dir(fx), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 47 {
		t.Fatalf("read %d sessions, want 47", len(sessions))
	}
	seen := map[string]bool{}
	for _, session := range sessions {
		if seen[session.ID] {
			t.Fatalf("duplicate session %q", session.ID)
		}
		seen[session.ID] = true
	}
	calls, _ := os.ReadFile(log)
	if string(calls) != "100|1|\n100|1|p1\n100|1|p2\n" {
		t.Fatalf("clear-all pagination argv lost: %q", calls)
	}
}

func TestReadAllHistorySessionsRejectsCursorCycle(t *testing.T) {
	fx, _ := historyFixture(t, map[string]fxSessionPage{
		"":   {Sessions: []FxSession{{ID: "one"}}, HasMore: true, NextCursor: "p1"},
		"p1": {Sessions: []FxSession{{ID: "two"}}, HasMore: true, NextCursor: "p2"},
		"p2": {Sessions: []FxSession{{ID: "three"}}, HasMore: true, NextCursor: "p1"},
	})
	if _, err := readAllHistorySessions(context.Background(), fx, filepath.Dir(fx), false); err == nil {
		t.Fatal("clear-all followed a cursor cycle")
	}
}

func TestDeletableHistoryIDsSkipsLiveAndEmpty(t *testing.T) {
	got := deletableHistoryIDs([]FxSession{{ID: "one"}, {ID: "live"}, {ID: ""}, {ID: "two"}}, map[string]bool{"live": true})
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("deletable ids = %v", got)
	}
}

// The set history must not delete comes from the app's session registry, not
// from the windows: a conversation keeps its claim after its window is gone,
// until its process is confirmed reaped (T9, R3).
func TestLiveSessionIDsCoversEveryWriter(t *testing.T) {
	c := &Coordinator{}
	open, err := c.sessionUse.claim("one")
	if err != nil {
		t.Fatal("a deletion claimed a free ID")
	}
	defer open.release()
	if _, err := c.sessionUse.claim("two"); err != nil {
		t.Fatal("a deletion claimed a free ID")
	}
	// A window with a conversation whose ID nobody claimed — the ID is not
	// protected just because it appears on screen, and it is protected with
	// no window in sight.
	c.sessions = []*ProjectWindow{{chat: &chatSession{sessionID: "three"}}, {chat: nil}}

	got := c.liveSessionIDs()
	if len(got) != 2 || !got["one"] || !got["two"] {
		t.Fatalf("live sessions = %v", got)
	}
	if got["three"] {
		t.Fatal("an unclaimed ID was reported as in use")
	}
}

// R3: a conversation that is closing is still a writer. Its window may already
// be gone and its replacement may already be on screen; only a confirmed
// reaping may release the ID.
func TestClosingWriterStaysProtectedUntilReaped(t *testing.T) {
	c := &Coordinator{}
	closing := newChatSession("", "", nil, nil)
	closing.registry = &c.sessionUse
	if err := closing.bookSession("still-writing"); err != nil {
		t.Fatal(err)
	}
	// The window closes: the chat is told to stop, and the window leaves the
	// list at once — before fx is gone.
	c.sessions = []*ProjectWindow{{chat: closing}}
	closing.Close() // no process was ever attempted, so this is a confirmed nothing
	c.removeSession(c.sessions[0])

	if c.sessionUse.inUse("still-writing") {
		t.Fatal("a confirmed teardown kept its claim")
	}

	// Now the real case: a teardown that has not confirmed the exit yet.
	c2 := &Coordinator{}
	live := newChatSession("", "", nil, nil)
	live.registry = &c2.sessionUse
	if err := live.bookSession("still-writing"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c2.sessionUse.beginDelete("still-writing"); ok {
		t.Fatal("a deletion claimed a session a live conversation holds")
	}
	live.finishClose(false, errNotConfirmed)
	if !c2.sessionUse.inUse("still-writing") {
		t.Fatal("an unconfirmed teardown released a writer that may still exist")
	}
	// The process exits afterwards; only now is the ID free.
	live.markReaped()
	if c2.sessionUse.inUse("still-writing") {
		t.Fatal("a confirmed reaping kept its claim")
	}
}

// errNotConfirmed is a teardown that could not establish the exit.
var errNotConfirmed = errors.New("fx did not exit")

// A deletion claims the ID for as long as it runs: a restore that arrives
// during it is refused, and the claim is released afterwards.
func TestDeleteClaimRefusesRestoreUntilFinished(t *testing.T) {
	c := &Coordinator{}
	finish, ok := c.sessionUse.beginDelete("s1")
	if !ok {
		t.Fatal("a free ID could not be claimed for deletion")
	}
	next := newChatSession("", "", nil, nil)
	next.registry = &c.sessionUse
	next.resume = "s1"
	if err := next.attachRegistry(&c.sessionUse); err == nil {
		t.Fatal("a restore was admitted for a session being deleted")
	}
	// The refused restore runs no process, so the window can report why and
	// the deletion still owns the ID.
	if got := c.sessionUse.snapshot(); !got["s1"] {
		t.Fatalf("the deletion lost its claim: %v", got)
	}
	finish()
	finish() // idempotent
	if c.sessionUse.inUse("s1") {
		t.Fatal("the claim outlived the deletion")
	}
	restore, err := c.sessionUse.claim("s1")
	if err != nil {
		t.Fatal("a finished deletion still refuses a restore")
	}
	restore.release()
}

// A partially failed clear-all releases every claim it took, so the survivors
// can be deleted again.
func TestPartialDeleteReleasesEveryClaim(t *testing.T) {
	c := &Coordinator{}
	ids := []string{"a", "b", "c"}
	reserved, releases := c.reserveDeletes(ids)
	if len(reserved) != 3 || len(releases) != 3 {
		t.Fatalf("reserved %v with %d releases", reserved, len(releases))
	}
	for _, release := range releases {
		release()
	}
	if got := c.sessionUse.snapshot(); len(got) != 0 {
		t.Fatalf("claims survived the deletion: %v", got)
	}
	for _, id := range ids {
		if _, ok := c.sessionUse.beginDelete(id); !ok {
			t.Fatalf("%s stayed undeletable after a failed clear-all", id)
		}
	}
}

// A clear-all that races a restore keeps the restored conversation: the ID is
// skipped, not deleted under the window that just opened it.
func TestClearAllSkipsASessionRestoredDuringTheDialog(t *testing.T) {
	c := &Coordinator{}
	// The snapshot the panel would compute before the confirmation.
	sessions := []FxSession{{ID: "kept"}, {ID: "gone"}}
	live := c.liveSessionIDs()
	if len(live) != 0 {
		t.Fatalf("nothing should be live yet: %v", live)
	}
	// Another window resumes "kept" while the confirmation is open.
	restore, err := c.sessionUse.claim("kept")
	if err != nil {
		t.Fatal("the restore was refused before any deletion started")
	}
	defer restore.release()
	ids := deletableHistoryIDs(sessions, c.liveSessionIDs())
	reserved, releases := c.reserveDeletes(ids)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	if len(reserved) != 1 || reserved[0] != "gone" {
		t.Fatalf("reserved %v, want only the closed conversation", reserved)
	}
}

func TestDeleteHistorySessionsRemovesDiskAndStats(t *testing.T) {
	home := testutil.IsolateHome(t)
	root := filepath.Join(home, ".fx", "sessions")
	for _, id := range []string{"drop", "keep"} {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := sessionstats.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("drop", sessionstats.Stats{InputTokens: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("keep", sessionstats.Stats{InputTokens: 7}); err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{stats: store}
	deleted, err := c.deleteHistorySessions([]string{"drop"})
	if err != nil || len(deleted) != 1 || deleted[0] != "drop" {
		t.Fatalf("deleted = %v, err = %v", deleted, err)
	}
	if _, err := os.Stat(filepath.Join(root, "drop")); !os.IsNotExist(err) {
		t.Fatalf("deleted session still on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "keep")); err != nil {
		t.Fatalf("unrelated session removed: %v", err)
	}
	if got := store.Get("drop"); !got.Empty() {
		t.Fatalf("deleted stats remain in memory: %+v", got)
	}
	if got := store.Get("keep"); got.InputTokens != 7 {
		t.Fatalf("unrelated stats removed: %+v", got)
	}
	again, err := sessionstats.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get("drop"); !got.Empty() {
		t.Fatalf("deleted stats survived a reload: %+v", got)
	}
}
