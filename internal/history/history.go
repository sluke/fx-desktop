// Package history owns the rules of saved-conversation history: how a page is
// requested, how search walks pages within a budget, how duplicates and
// repeated cursors are rejected, and which rows may be deleted. It performs no
// I/O of its own — the app passes in a Reader — and it knows nothing about
// windows or MyGo.
package history

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"
)

// Session is one saved conversation as the panel shows it.
type Session struct {
	ID, Title, Workspace string
	UpdatedAtMS          int64
}

// Record is one saved conversation as the store reported it.
type Record struct {
	ID            string
	Title         string
	WorkspaceRoot string
	UpdatedAtMS   int64
}

// Page is one page of saved conversations.
type Page struct {
	Sessions   []Record
	HasMore    bool
	NextCursor string
}

// Reader reads one page. The app supplies it, backed by `fx sessions --json`.
type Reader func(ctx context.Context, all bool, limit int, cursor string) (Page, error)

// Request is one search batch: the conditions, where the walk is, and what is
// already known. It is a value, not a pointer into UI state, so a worker can
// own it while the panel keeps rendering.
type Request struct {
	All           bool
	Query, Cursor string
	Pending       []Session
	Seen, Cursors map[string]bool
	Scanned       int
	Exhausted     bool
}

// Result is one search batch's outcome. Pending carries matches that did not
// fit this batch, so continuing the search does not re-walk the source.
type Result struct {
	Request
	Entries []Session
	Problem string
}

// pageSize is how many source rows one request asks for.
const pageSize = 20

// maxEntries is how many matches one batch returns.
const maxEntries = 20

// ReadBatch walks source pages in bounded batches, retaining surplus matches
// for the next batch. Errors retain the last successful source cursor, so a
// retry continues instead of starting over.
func ReadBatch(ctx context.Context, read Reader, request Request) Result {
	r := Result{Request: request}
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
	for n := 0; n < budget && len(queue) < maxEntries && !r.Exhausted; n++ {
		page, err := read(ctx, request.All, pageSize, r.Cursor)
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
			queue = append(queue, Session{ID: entry.ID, Title: entry.Title, Workspace: entry.WorkspaceRoot, UpdatedAtMS: entry.UpdatedAtMS})
		}
		r.Cursor = page.NextCursor
		r.Exhausted = page.NextCursor == ""
	}
	r.Entries = slices.Clone(queue[:min(maxEntries, len(queue))])
	r.Pending = slices.Clone(queue[min(maxEntries, len(queue)):])
	return r
}

// ReadAll walks every page in one scope. Clear-all must not be limited to the
// rows already loaded or to the current search. A repeated cursor is an error
// rather than a loop: the source may be slow, never infinite.
func ReadAll(ctx context.Context, read Reader, all bool) ([]Record, error) {
	var records []Record
	seenIDs := map[string]bool{}
	seenCursors := map[string]bool{}
	cursor := ""
	for {
		if seenCursors[cursor] {
			return nil, ErrRepeatedCursor
		}
		seenCursors[cursor] = true
		page, err := read(ctx, all, 100, cursor)
		if err != nil {
			return nil, err
		}
		for _, record := range page.Sessions {
			if record.ID == "" || seenIDs[record.ID] {
				continue
			}
			seenIDs[record.ID] = true
			records = append(records, record)
		}
		if !page.HasMore {
			return records, nil
		}
		cursor = page.NextCursor
	}
}

// Paging is the query side of the History panel: the applied conditions, the
// cursor walk, the dedupe sets, the generation the results belong to, and the
// matches published so far. It deliberately excludes the list widget's scroll
// and selection state, which belong to the view.
type Paging struct {
	All, Busy, Loaded, Exhausted bool
	Query, Cursor, Problem       string
	Entries, Pending             []Session
	Seen, Cursors                map[string]bool
	Scanned                      int
	Generation                   uint64
}

// HasMore reports whether another batch could produce rows.
func (p *Paging) HasMore() bool {
	return p.Loaded && (!p.Exhausted || len(p.Pending) > 0)
}

// Apply publishes a batch if it belongs to generation. It reports whether the
// result was current; a stale result is dropped rather than shown under newer
// conditions (§4.4).
func (p *Paging) Apply(generation uint64, result Result) bool {
	if p.Generation != generation {
		return false
	}
	p.Busy = false
	p.Loaded = true
	p.Entries = append(p.Entries, result.Entries...)
	sort.SliceStable(p.Entries, func(i, j int) bool { return p.Entries[i].UpdatedAtMS > p.Entries[j].UpdatedAtMS })
	p.Pending, p.Seen, p.Cursors = result.Pending, result.Seen, result.Cursors
	p.Cursor, p.Exhausted, p.Scanned, p.Problem = result.Cursor, result.Exhausted, result.Scanned, result.Problem
	return true
}

// Request snapshots the paging state for a worker. The worker never holds a
// pointer into the panel's state.
func (p *Paging) Request() Request {
	return Request{
		All: p.All, Query: p.Query, Cursor: p.Cursor,
		Pending: slices.Clone(p.Pending), Seen: maps.Clone(p.Seen), Cursors: maps.Clone(p.Cursors),
		Scanned: p.Scanned, Exhausted: p.Exhausted,
	}
}

// Reset forgets the walk but keeps the applied conditions and generation.
func (p *Paging) Reset(query string, all bool) {
	generation := p.Generation
	*p = Paging{All: all, Query: query, Generation: generation}
}

// Deletable drops conversations that are open in a window. It also drops an
// empty ID, which is not a row the store could have produced but must never
// become a path.
func Deletable(sessions []Record, live map[string]bool) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if session.ID == "" || live[session.ID] {
			continue
		}
		ids = append(ids, session.ID)
	}
	return ids
}

// SessionName makes a session title safe for one line of a native dialog.
// Titles come from prompts, so they can contain newlines and can be
// arbitrarily long; an empty title falls back to the ID.
func SessionName(title, id string) string {
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

// ErrRepeatedCursor is the store answering with a cursor the walk already
// used: the source may be slow, never infinite.
var ErrRepeatedCursor = errorString("History cursor repeated; refresh or retry.")

type errorString string

func (e errorString) Error() string { return string(e) }
