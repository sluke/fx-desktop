package chrome

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/history"
)

func TestSidebarHistoryLimitsAndCollapsesRecentSessions(t *testing.T) {
	h, sh := testHost()
	h.Data.Saved = nil
	for i := 0; i < 30; i++ {
		h.Data.Saved = append(h.Data.Saved, SavedSession{ID: fmt.Sprint(i), Title: fmt.Sprintf("History item %02d", i)})
	}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	for i := 0; i < 5; i++ {
		mustHave(t, ts, fmt.Sprintf("History item %02d", i))
	}
	for i := 5; i < 30; i++ {
		mustNotHaveToken(t, ts, fmt.Sprintf("History item %02d", i))
	}
	if err := ts.Click("Toggle history"); err != nil {
		t.Fatal(err)
	}
	mustNotHaveToken(t, ts, "History item 00")
	if err := ts.Click("Toggle history"); err != nil {
		t.Fatal(err)
	}
	mustHave(t, ts, "History item 00")
	_ = sh
}

func TestHistoryBrowserSearchScopeResumeAndPagination(t *testing.T) {
	h, sh := testHost()
	h.Actions.OpenHistory = noop
	sh.History = HistoryState{Open: true, Paging: history.Paging{Loaded: true, Entries: []HistorySession{{ID: "one", Title: "First conversation", Workspace: "/other", UpdatedAtMS: time.Now().UnixMilli()}}}}
	var action, id, path string
	h.History = HistoryHost{Run: func(a string) { action = a }, Resume: func(i, p string) { id, path = i, p }, ActiveID: "one"}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 560)
	mustHave(t, ts, "Today", "Current", "First conversation")
	if err := ts.Click("Search history titles"); err != nil {
		t.Fatal(err)
	}
	ts.Type("needle")
	ts.Key(0, ui.KeyEnter)
	if action != "search" || sh.History.DraftQuery != "needle" {
		t.Fatal("search field did not submit")
	}
	action = ""
	_ = ts.Click("All workspaces")
	if !sh.History.All || action != "search" {
		t.Fatal("scope did not reload history")
	}
	action = ""
	_ = ts.Click("Load more history")
	if action != "more" {
		t.Fatal("load more did not request next page")
	}
	sh.History.Busy = true
	ts.Frame()
	action = ""
	_ = ts.Click("Load more history")
	if action != "" {
		t.Fatal("busy history dispatched duplicate page")
	}
	sh.History.Busy = false
	ts.Frame()
	_ = ts.Click("Resume history one")
	if id != "one" || path != "/other" {
		t.Fatalf("resume lost owning workspace: %q %q", id, path)
	}
	_ = ts.Click("Clear history search")
	if sh.History.DraftQuery != "" {
		t.Fatal("clear did not reset search text")
	}
}

func TestHistoryEmptyAndPartialSearchStatesFitMinimumWindow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		h, sh := testHost()
		h.Data.Saved = nil
		h.Actions.OpenHistory = func() { sh.History = HistoryState{Open: true, Paging: history.Paging{Loaded: true, Exhausted: true}} }
		h.History.Run = func(string) {}
		ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 480)
		ts.SetDark(dark)
		ts.Frame()
		if err := ts.Click("View all session history"); err != nil {
			t.Fatal(err)
		}
		mustHave(t, ts, "No saved sessions.", "End of history")
		for _, label := range []string{"Session history", "Refresh", "Close", "Search history titles"} {
			r, ok := ts.Find(label)
			if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 720 || r.Y+r.H > 480 {
				t.Fatalf("%s outside minimum history panel: %+v", label, r)
			}
		}
		sh.History.Query = "missing"
		sh.History.Exhausted = false
		ts.Frame()
		mustHave(t, ts, "No matches yet.")
		mustNotHaveToken(t, ts, "No matching titles.", "End of history")
		sh.History.Exhausted = true
		ts.Frame()
		mustHave(t, ts, "No matching titles.")
		sh.History.Problem = "Unable to read history"
		ts.Frame()
		mustHave(t, ts, "Unable to read history", "Could not load history.", "Retry")
	}
}

func TestHistoryRowsDeleteLiveAndClearAll(t *testing.T) {
	h, sh := testHost()
	h.Actions.OpenHistory = noop
	now := time.Now().UnixMilli()
	sh.History = HistoryState{Open: true, Loaded: true, Exhausted: true, Entries: []HistorySession{
		{ID: "one", Title: "First conversation", UpdatedAtMS: now},
		{ID: "live", Title: "Open conversation", UpdatedAtMS: now},
	}}
	var deletedID, deletedTitle string
	var resumedID string
	var cleared, clearAll bool
	h.History = HistoryHost{
		Run:      func(string) {},
		Resume:   func(id, _ string) { resumedID = id },
		Delete:   func(id, title string) { deletedID, deletedTitle = id, title },
		Clear:    func(all bool) { cleared, clearAll = true, all },
		ActiveID: "live",
		Live:     map[string]bool{"live": true},
	}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 560)
	mustHave(t, ts, "Current", "Open", "First conversation", "Open conversation")
	if err := ts.Click("Resume history one"); err != nil {
		t.Fatal(err)
	}
	if resumedID != "one" {
		t.Fatalf("resume dispatched %q", resumedID)
	}
	if err := ts.Click("Delete saved session one"); err != nil {
		t.Fatal(err)
	}
	if deletedID != "one" || deletedTitle != "First conversation" {
		t.Fatalf("delete dispatched %q %q", deletedID, deletedTitle)
	}
	deletedID = ""
	_ = ts.Click("Delete saved session live")
	if deletedID != "" {
		t.Fatal("delete was dispatched for an open session")
	}
	if err := ts.Click("Clear all session history"); err != nil {
		t.Fatal(err)
	}
	if !cleared || clearAll {
		t.Fatalf("clear all = %v, all = %v", cleared, clearAll)
	}
	cleared = false
	sh.History.All = true
	ts.Frame()
	_ = ts.Click("Clear all session history")
	if !cleared || !clearAll {
		t.Fatalf("workspace-wide clear lost the scope: %v %v", cleared, clearAll)
	}

	sh.History.Query = "First"
	ts.Frame()
	if _, ok := ts.Find("Clear all session history"); ok {
		t.Fatal("clear all was offered while search filtered the list")
	}
	sh.History.Query = ""
	sh.History.Deleting = true
	ts.Frame()
	mustHave(t, ts, "Deleting…")
	cleared = false
	deletedID = ""
	resumedID = ""
	_ = ts.Click("Clear all session history")
	_ = ts.Click("Delete saved session one")
	_ = ts.Click("Resume history one")
	if cleared || deletedID != "" || resumedID != "" {
		t.Fatal("history actions dispatched while a delete was running")
	}
}

func TestHistoryDeleteControlsFitMinimumWindow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		h, sh := testHost()
		h.Actions.OpenHistory = noop
		sh.History = HistoryState{Open: true, Loaded: true, Exhausted: true, Entries: []HistorySession{
			{ID: "one", Title: "First conversation", UpdatedAtMS: time.Now().UnixMilli()},
		}}
		h.History = HistoryHost{
			Run:      func(string) {},
			Resume:   func(string, string) {},
			Delete:   func(string, string) {},
			Clear:    func(bool) {},
			ActiveID: "one",
			Live:     map[string]bool{"one": true},
		}
		ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 480)
		ts.SetDark(dark)
		ts.Frame()
		for _, label := range []string{"Delete saved session one", "Clear all session history"} {
			r, ok := ts.Find(label)
			if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 720 || r.Y+r.H > 480 {
				t.Fatalf("%s outside minimum history panel (dark=%v): %+v", label, dark, r)
			}
		}
	}
}

func TestHistoryDateGroupsAndVirtualizedLongList(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 5, 0, 0, time.FixedZone("local", 8*3600))
	for _, tc := range []struct {
		at   time.Time
		want string
	}{{now, "Today"}, {now.Add(-10 * time.Minute), "Yesterday"}, {now.AddDate(0, 0, -4), "Previous 7 days"}, {now.AddDate(0, 0, -8), "Older"}} {
		if got := historyDate(tc.at.UnixMilli(), now); got != tc.want {
			t.Fatalf("date group %v: %s", tc.at, got)
		}
	}
	if historyDate(0, now) != "Unknown date" {
		t.Fatal("unknown date rendered as today")
	}
	h, sh := testHost()
	h.Actions.OpenHistory = noop
	sh.History = HistoryState{Open: true, Paging: history.Paging{Loaded: true, Exhausted: true}}
	for i := 0; i < 2000; i++ {
		sh.History.Entries = append(sh.History.Entries, HistorySession{ID: fmt.Sprint(i), Title: fmt.Sprintf("Conversation %04d", i), UpdatedAtMS: now.UnixMilli() - int64(i)*60000})
	}
	h.History.Now = now
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	count := 0
	for _, label := range ts.Texts() {
		if strings.HasPrefix(label, "Resume history ") {
			count++
		}
	}
	if count < 1 || count > 100 {
		t.Fatalf("history did not virtualize 2000 rows: built %d", count)
	}
	sh.History.List.ScrollToEnd()
	ts.Frame()
	ts.Frame()
	mustHave(t, ts, "Conversation 1999")
}

// The history drawer's rows used to be bare titles beside a delete button:
// no clock icon, no turn count, no relative time, so the drawer read as a
// different, flatter list than the sidebar's History section it expands.
// The rows now share that section's anatomy — icon, 12px title, 10px turns
// meta, a 10px relative-time line under the title.
func TestHistoryRowsMatchTheSidebarRowStyle(t *testing.T) {
	now := time.Now()
	h, sh := testHost()
	h.Appearance = "light"
	h.Actions.OpenHistory = noop
	sh.History = HistoryState{Open: true, Loaded: true, Exhausted: true, Entries: []HistorySession{
		{ID: "a", Title: "First conversation", UpdatedAtMS: now.Add(-2 * time.Minute).UnixMilli(), Turns: 3},
		{ID: "b", Title: "Second conversation", UpdatedAtMS: now.Add(-5 * time.Minute).UnixMilli(), Turns: 6},
	}}
	h.History = HistoryHost{
		Run:      func(string) {},
		ActiveID: "b",
		Now:      now,
		Rel:      func(int64) string { return "5m ago" },
	}
	ts := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() {
			Frame(c, h)
			// The reference row is the sidebar's own history row.
			p := palette(c.Theme())
			sidebarRow(c, p, IconClock, "ref title", "9 turns", false, nil)
		})
	}, 720, 560)
	ts.SetDark(false)
	ts.Frame()

	same := func(a, b ui.Rect, what string) {
		t.Helper()
		if diff := a.H - b.H; diff > 0.5 || diff < -0.5 {
			t.Fatalf("%s is %.2f high, want the sidebar row's %.2f", what, a.H, b.H)
		}
	}
	ref, ok := ts.Find("9 turns")
	if !ok {
		t.Fatal("the sidebar reference row is missing")
	}
	for _, turns := range []string{"3 turns", "6 turns"} {
		got, ok := ts.Find(turns)
		if !ok {
			t.Fatalf("turn meta %q is missing from the drawer rows", turns)
		}
		same(got, ref, turns)
	}
	when, ok := ts.Find("5m ago")
	if !ok {
		t.Fatal("the relative-time line is missing from the drawer rows")
	}
	same(when, ref, "relative time")
	title, ok := ts.Find("First conversation")
	if !ok {
		t.Fatal("the row title is missing")
	}
	refTitle, ok := ts.Find("ref title")
	if !ok {
		t.Fatal("the reference title is missing")
	}
	same(title, refTitle, "row title")
	// The clock icon leads the title: the title starts after the icon and
	// its gap, not at the row's left padding.
	rowPad := float32(6)
	if title.X-refTitle.X < rowPad {
		t.Fatalf("row title starts at x=%.1f, left of an icon gutter", title.X)
	}
}

// The sidebar marks its selected history row with a rounded SurfaceHi chip
// inset from the list edges. The drawer used to paint a flat, full-bleed
// Sunken band across the whole row instead, so the two lists read as
// different controls even though they show the same sessions.
func TestHistorySelectedRowMatchesTheSidebarSelection(t *testing.T) {
	now := time.Now()
	h, sh := testHost()
	h.Appearance = "light"
	h.Actions.OpenHistory = noop
	sh.History = HistoryState{Open: true, Loaded: true, Exhausted: true, Entries: []HistorySession{
		{ID: "a", Title: "First conversation", UpdatedAtMS: now.Add(-2 * time.Minute).UnixMilli(), Turns: 3},
		{ID: "b", Title: "Second conversation", UpdatedAtMS: now.Add(-5 * time.Minute).UnixMilli(), Turns: 6},
	}}
	h.History = HistoryHost{
		Run:      func(string) {},
		ActiveID: "b",
		Now:      now,
		Rel:      func(int64) string { return "5m ago" },
	}
	var p Palette
	ts := ui.NewTester(func(c *ui.Context) {
		Frame(c, h)
		p = palette(c.Theme())
	}, 720, 560)
	ts.SetDark(false)
	ts.Frame()

	list, ok := ts.Find("History results")
	if !ok {
		t.Fatal("the history list is missing")
	}
	active, ok := ts.Find("History row b")
	if !ok {
		t.Fatal("the selected row's surface is missing")
	}
	if gutter := active.X - list.X; gutter < 8 {
		t.Errorf("selection starts %.1fpx from the list edge, want the sidebar's 8px inset", gutter)
	}
	if gutter := list.X + list.W - (active.X + active.W); gutter < 8 {
		t.Errorf("selection ends %.1fpx from the list edge, want the sidebar's 8px inset", gutter)
	}
	if active.Y < 0 || active.Y+active.H > 560 {
		t.Fatalf("selection rect is off-frame: %+v", active)
	}

	img := ts.Image()
	// Two points inside the selection: the top edge clear of the row's text,
	// and the top-left corner. A square band fills both; the sidebar's
	// rounded chip leaves the corner showing the panel behind it.
	interior := img.RGBAAt(int(active.X)+12, int(active.Y)+2)
	corner := img.RGBAAt(int(active.X)+1, int(active.Y)+1)
	want := p.SurfaceHi
	if interior.R != want.R || interior.G != want.G || interior.B != want.B {
		t.Errorf("selection is rgb(%d,%d,%d), want the sidebar SurfaceHi rgb(%d,%d,%d)",
			interior.R, interior.G, interior.B, want.R, want.G, want.B)
	}
	if near(corner, interior, 2) {
		t.Error("selection has square corners; want the sidebar's rounded chip")
	}
}

func near(a, b color.RGBA, tol int) bool {
	return absInt(int(a.R)-int(b.R)) <= tol && absInt(int(a.G)-int(b.G)) <= tol && absInt(int(a.B)-int(b.B)) <= tol
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
