package chrome

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/history"
)

func historyEntries(n int, now time.Time, prefix string) []HistorySession {
	entries := make([]HistorySession, n)
	for i := range entries {
		entries[i] = HistorySession{ID: prefix + fmt.Sprint(i), Title: prefix + fmt.Sprint(i), UpdatedAtMS: now.Add(-time.Duration(i) * time.Hour).UnixMilli()}
	}
	return entries
}

// rowIDs flattens rows into "group:<name>" and "session:<id>" for comparison.
func rowIDs(entries []HistorySession, rows []historyRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.group != "" {
			out = append(out, "group:"+r.group)
		} else {
			out = append(out, "session:"+entries[r.index].ID)
		}
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A panel whose entries and local day have not changed lays its rows out
// once, however many frames it is drawn for (r10 §4).
func TestHistoryRowsAreBuiltOncePerDataAndDay(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	h, sh := testHost()
	h.Actions.OpenHistory = noop
	h.History = HistoryHost{Run: func(string) {}, Now: now}
	sh.History = HistoryState{Open: true}
	sh.History.Apply(0, history.Result{Entries: historyEntries(60, now, "a")})
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 560)
	for i := 0; i < 100; i++ {
		ts.Frame()
	}
	if got := sh.History.rowCache.builds; got != 1 {
		t.Fatalf("100 quiet frames built the rows %d times, want once", got)
	}
	mustHave(t, ts, "Today", "a0")

	// A later minute of the same day changes nothing the rows depend on.
	h.History.Now = now.Add(7 * time.Minute)
	ts.Frame()
	if got := sh.History.rowCache.builds; got != 1 {
		t.Fatalf("a later minute rebuilt the rows (%d builds)", got)
	}

	// Another page is a new publication.
	sh.History.Apply(0, history.Result{Entries: []HistorySession{{ID: "newest", Title: "newest", UpdatedAtMS: now.Add(time.Minute).UnixMilli()}}})
	ts.Frame()
	if got := sh.History.rowCache.builds; got != 2 {
		t.Fatalf("a new page left %d builds, want 2", got)
	}
	want := rowIDs(sh.History.Entries, historyRows(sh.History.Entries, h.History.Now))
	if got := rowIDs(sh.History.Entries, sh.History.rowCache.rows); !sameStrings(got, want) || got[1] != "session:newest" {
		t.Fatalf("rows after a page do not follow the new order: %v", got[:4])
	}

	// A refresh that returns the same number of rows is still a different
	// list: the removed ID is gone and the restored one is present.
	count := len(sh.History.Entries)
	sh.History.Reset("", false)
	replacement := historyEntries(count, now, "b")
	sh.History.Apply(0, history.Result{Entries: replacement})
	ts.Frame()
	if got := sh.History.rowCache.builds; got < 3 {
		t.Fatalf("a same-length replacement was served from the cache (%d builds)", got)
	}
	got := rowIDs(sh.History.Entries, sh.History.rowCache.rows)
	for _, id := range got {
		if id == "session:newest" || id == "session:a0" {
			t.Fatalf("a replaced session is still a row: %v", id)
		}
	}
	if got[1] != "session:b0" || len(got) != len(rowIDs(replacement, historyRows(replacement, h.History.Now))) {
		t.Fatalf("rows after replacement: %v", got[:4])
	}

	// Entries assigned directly, without a revision, are caught by identity.
	builds := sh.History.rowCache.builds
	sh.History.Entries = historyEntries(3, now, "c")
	ts.Frame()
	if sh.History.rowCache.builds != builds+1 || len(sh.History.rowCache.rows) != 4 {
		t.Fatalf("directly assigned entries were not laid out: builds=%d rows=%d", sh.History.rowCache.builds-builds, len(sh.History.rowCache.rows))
	}

	// Closing the panel releases the rows; reopening lays them out again.
	sh.History.Open = false
	ts.Frame()
	if sh.History.rowCache.rows != nil || sh.History.rowCache.valid {
		t.Fatal("a closed panel kept its rows")
	}
	sh.History.Open = true
	ts.Frame()
	if len(sh.History.rowCache.rows) != 4 {
		t.Fatal("a reopened panel did not lay its rows out")
	}
}

// Midnight and a time-zone change both move the headings, so both rebuild.
func TestHistoryRowsRegroupAcrossMidnightAndZones(t *testing.T) {
	zone := time.FixedZone("east", 8*3600)
	evening := time.Date(2026, 11, 18, 23, 58, 0, 0, zone)
	state := HistoryState{}
	state.Apply(0, history.Result{Entries: []HistorySession{
		{ID: "late", UpdatedAtMS: evening.Add(-time.Minute).UnixMilli()},
		{ID: "noon", UpdatedAtMS: evening.Add(-12 * time.Hour).UnixMilli()},
		{ID: "prior", UpdatedAtMS: evening.Add(-30 * time.Hour).UnixMilli()},
		{ID: "unknown"},
	}})
	got := rowIDs(state.Entries, state.rowsFor(evening))
	want := []string{"group:Today", "session:late", "session:noon", "group:Yesterday", "session:prior", "group:Unknown date", "session:unknown"}
	if !sameStrings(got, want) {
		t.Fatalf("evening rows = %v", got)
	}
	if state.rowsFor(evening.Add(time.Minute)); state.rowCache.builds != 1 {
		t.Fatalf("a minute before midnight rebuilt: %d", state.rowCache.builds)
	}

	after := evening.Add(5 * time.Minute)
	got = rowIDs(state.Entries, state.rowsFor(after))
	want = []string{"group:Yesterday", "session:late", "session:noon", "group:Previous 7 days", "session:prior", "group:Unknown date", "session:unknown"}
	if !sameStrings(got, want) || state.rowCache.builds != 2 {
		t.Fatalf("rows after midnight = %v (builds %d)", got, state.rowCache.builds)
	}

	// The same instant read in a zone where it is still the 18th (11:03):
	// "late" is 10:57 that day, "noon" and "prior" both fall on the 17th.
	west := after.In(time.FixedZone("west", -5*3600))
	got = rowIDs(state.Entries, state.rowsFor(west))
	want = []string{"group:Today", "session:late", "group:Yesterday", "session:noon", "session:prior", "group:Unknown date", "session:unknown"}
	if !sameStrings(got, want) || state.rowCache.builds != 3 {
		t.Fatalf("rows in the western zone = %v (builds %d)", got, state.rowCache.builds)
	}

	// The cached rows always equal a cold build.
	for _, at := range []time.Time{evening, after, west} {
		if a, b := rowIDs(state.Entries, state.rowsFor(at)), rowIDs(state.Entries, historyRows(state.Entries, at)); !sameStrings(a, b) {
			t.Fatalf("cache and cold build disagree at %v: %v vs %v", at, a, b)
		}
	}
}

// The warm path allocates nothing, at any size; the cold build is one slice.
func TestHistoryRowsWarmPathDoesNotAllocate(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for _, n := range []int{100, 10000} {
		state := HistoryState{}
		state.Apply(0, history.Result{Entries: historyEntries(n, now, "s")})
		state.rowsFor(now)
		if allocs := testing.AllocsPerRun(50, func() { state.rowsFor(now) }); allocs != 0 {
			t.Fatalf("%d entries: a cache hit allocated %.0f times", n, allocs)
		}
		entries := state.Entries
		if allocs := testing.AllocsPerRun(20, func() { historyRows(entries, now) }); allocs > 1 {
			t.Fatalf("%d entries: a cold build allocated %.0f times, want one slice", n, allocs)
		}
	}
}

// A session whose ID reads like a heading does not share the heading's key.
func TestHistoryRowKeysKeepHeadingsAndSessionsApart(t *testing.T) {
	entries := []HistorySession{{ID: "Today", UpdatedAtMS: 1}}
	if historyRowKey(entries, historyRow{group: "Today"}) == historyRowKey(entries, historyRow{index: 0}) {
		t.Fatal("a heading and a session share a list key")
	}
}

func benchmarkHistoryEntries(n int, now time.Time) []HistorySession {
	return historyEntries(n, now, "")
}

// BenchmarkHistoryRowsCold is the full layout: what every frame used to pay.
func BenchmarkHistoryRowsCold(b *testing.B) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			entries := benchmarkHistoryEntries(n, now)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if rows := historyRows(entries, now); len(rows) < n {
					b.Fatal(len(rows))
				}
			}
		})
	}
}

// BenchmarkHistoryRowsWarm is a frame of an unchanged panel: a cache hit.
func BenchmarkHistoryRowsWarm(b *testing.B) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			state := HistoryState{}
			state.Apply(0, history.Result{Entries: benchmarkHistoryEntries(n, now)})
			state.rowsFor(now)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if rows := state.rowsFor(now); len(rows) < n {
					b.Fatal(len(rows))
				}
			}
		})
	}
}
