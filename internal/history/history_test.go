package history

import (
	"context"
	"errors"
	"testing"
)

// readerFor serves pages from a map keyed by cursor. It records how many
// pages were read so the search budget can be checked.
type readerFor struct {
	pages map[string]Page
	reads int
	err   error
}

func (r *readerFor) fetch(ctx context.Context, all bool, limit int, cursor string) (Page, error) {
	r.reads++
	if r.err != nil {
		return Page{}, r.err
	}
	return r.pages[cursor], nil
}

func page(ids ...string) Page {
	records := make([]Record, 0, len(ids))
	for _, id := range ids {
		records = append(records, Record{ID: id, Title: id, UpdatedAtMS: 1})
	}
	return Page{Sessions: records}
}

// One batch of an empty search reads one page; a search keeps walking within
// its budget until it has a full batch or the source runs out.
func TestReadBatchWalksWithinItsBudget(t *testing.T) {
	first := page("one")
	first.NextCursor = "next"
	r := &readerFor{pages: map[string]Page{"": first}}
	res := ReadBatch(context.Background(), r.fetch, Request{})
	if r.reads != 1 || len(res.Entries) != 1 || res.Exhausted {
		t.Fatalf("unfiltered batch = %+v, reads = %d", res, r.reads)
	}

	// A search reads up to ten pages looking for matches.
	pages := map[string]Page{}
	cursor := ""
	for i := 0; i < 12; i++ {
		next := ""
		if i < 11 {
			next = "p" + string(rune('a'+i))
		}
		pages[cursor] = Page{Sessions: []Record{{ID: "miss-" + cursor, Title: "nothing", UpdatedAtMS: 1}}, NextCursor: next}
		cursor = next
	}
	r = &readerFor{pages: pages}
	res = ReadBatch(context.Background(), r.fetch, Request{Query: "needle"})
	if r.reads != 10 {
		t.Fatalf("a search read %d pages, want the budget of 10", r.reads)
	}
	if len(res.Entries) != 0 || res.Problem != "" {
		t.Fatalf("res = %+v", res)
	}
}

// A source page that repeats a cursor is refused rather than walked forever.
func TestReadBatchRejectsARepeatedCursor(t *testing.T) {
	r := &readerFor{pages: map[string]Page{
		"":     {Sessions: []Record{{ID: "a", Title: "match a"}}, NextCursor: "same"},
		"same": {Sessions: []Record{{ID: "b", Title: "match b"}}, NextCursor: "same"},
	}}
	res := ReadBatch(context.Background(), r.fetch, Request{Query: "match"})
	if res.Problem == "" {
		t.Fatal("the repeat was not reported")
	}
}

// A failed page keeps the cursor walk where it was, so a retry continues
// instead of starting over.
func TestReadBatchRetainsProgressOnFailure(t *testing.T) {
	r := &readerFor{err: errors.New("boom")}
	res := ReadBatch(context.Background(), r.fetch, Request{Cursor: "p2", Scanned: 40, Seen: map[string]bool{"x": true}})
	if res.Cursor != "p2" || res.Scanned != 40 || !res.Seen["x"] {
		t.Fatalf("progress was lost: %+v", res)
	}
	if res.Problem != "boom" {
		t.Fatalf("problem = %q", res.Problem)
	}
}

// A result that belongs to an older generation is dropped, not published under
// the newer conditions.
func TestPagingDropsStaleResults(t *testing.T) {
	p := Paging{Generation: 2, Query: "new", Busy: true}
	if p.Apply(1, Result{Entries: []Session{{ID: "old"}}}) {
		t.Fatal("a stale batch was applied")
	}
	if len(p.Entries) != 0 || !p.Busy {
		t.Fatalf("paging changed: %+v", p)
	}
	if !p.Apply(2, Result{Entries: []Session{{ID: "new"}}, Request: Request{Cursor: "next", Scanned: 20}}) {
		t.Fatal("a current batch was dropped")
	}
	if len(p.Entries) != 1 || p.Busy || p.Cursor != "next" || p.Scanned != 20 {
		t.Fatalf("paging = %+v", p)
	}
}

// Reset forgets the walk but keeps the applied conditions and generation.
func TestPagingReset(t *testing.T) {
	p := Paging{Generation: 3, All: true, Query: "q", Cursor: "c", Loaded: true, Busy: true, Entries: []Session{{ID: "a"}}}
	p.Reset("q2", true)
	if p.Generation != 3 || p.Cursor != "" || p.Loaded || p.Busy || len(p.Entries) != 0 || p.Query != "q2" || !p.All {
		t.Fatalf("reset = %+v", p)
	}
}

// ReadAll walks every page and refuses a repeated cursor.
func TestReadAll(t *testing.T) {
	r := &readerFor{pages: map[string]Page{
		"":   {Sessions: []Record{{ID: "a"}}, HasMore: true, NextCursor: "p2"},
		"p2": {Sessions: []Record{{ID: "b"}, {ID: "a"}}},
	}}
	records, err := ReadAll(context.Background(), r.fetch, true)
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
	loop := &readerFor{pages: map[string]Page{
		"":     {HasMore: true, NextCursor: "same"},
		"same": {HasMore: true, NextCursor: "same"},
	}}
	if _, err := ReadAll(context.Background(), loop.fetch, true); !errors.Is(err, ErrRepeatedCursor) {
		t.Fatalf("err = %v", err)
	}
}

// Deletion never names a live conversation, and never an empty id.
func TestDeletableAndSessionName(t *testing.T) {
	ids := Deletable([]Record{{ID: "one"}, {ID: "live"}, {ID: ""}, {ID: "two"}}, map[string]bool{"live": true})
	if len(ids) != 2 || ids[0] != "one" || ids[1] != "two" {
		t.Fatalf("deletable = %v", ids)
	}
	if got := SessionName("  two\nlines  ", "id"); got != "two lines" {
		t.Fatalf("SessionName = %q", got)
	}
	if got := SessionName("", "id"); got != "id" {
		t.Fatalf("empty title fell back to %q", got)
	}
}
