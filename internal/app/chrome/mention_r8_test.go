package chrome

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	workspaceread "fx-desktop/internal/workspace/read"
)

func TestRegressionR8EmptyMentionCacheOwnsOnlyRows(t *testing.T) {
	h := ConversationHost{MentionFiles: make([]MentionFile, 100000), MentionEpoch: 1, MentionRoot: "fixture"}
	s := &ConversationState{}
	rows := rankedMentions(s, h)
	t.Logf("cached len=%d cap=%d", len(rows), cap(rows))
	if cap(rows) > mentionLimit {
		t.Fatalf("eight-row cache retains a %d-entry backing array", cap(rows))
	}
}

func TestMentionCompletionReplacesFullTokenAndResolves(t *testing.T) {
	root := t.TempDir()
	name := `新 "file".go`
	if err := os.WriteFile(filepath.Join(root, name), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ draft, prefix string }{
		{`read @"old file.go" and @other`, `read @"`},
		{`read @"old file.go" and @other`, `read @"old`},
		{`read @"old file.go" and @other`, `read @"old file.go`},
		{`read @"old file.go" and @other`, `read @"old file.go"`},
		{`read @old.go and @other`, `read @ol`},
		{`read @"old\" file.go" and @other`, `read @"old\"`},
	} {
		_, start, end, ok := mentionAt(tc.draft, len([]rune(tc.prefix)))
		if !ok {
			t.Fatalf("no mention in %q", tc.prefix)
		}
		got := mentionInsertion(tc.draft, start, end, name)
		want := "read " + mentionText(name) + " and @other"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		mentions, err := workspaceread.Resolve(context.Background(), root, got)
		if err != nil || len(mentions) != 1 || mentions[0].Path != name || mentions[0].Text != "content" {
			t.Fatalf("resolved %q: %+v, %v", got, mentions, err)
		}
	}
}

func TestMentionCacheOwnsRowsAndTracksSnapshot(t *testing.T) {
	files := []MentionFile{{Path: "one", Name: "one"}}
	h := ConversationHost{MentionFiles: files, MentionEpoch: 1, MentionRoot: "root"}
	s := &ConversationState{}
	rows := rankedMentions(s, h)
	files[0] = MentionFile{Path: "two", Name: "two"}
	if rows[0].Path != "one" {
		t.Fatal("cached rows share the source array")
	}
	for _, mode := range []string{"loading", "error", "ready"} {
		h.MentionLoading = mode == "loading"
		h.MentionProblem = ""
		if mode == "error" {
			h.MentionProblem = "failed"
		}
		if got := rankedMentions(s, h); got[0].Path != "one" {
			t.Fatalf("%s changed cached snapshot: %v", mode, got)
		}
	}
	h.MentionEpoch++
	if got := rankedMentions(s, h); got[0].Path != "two" {
		t.Fatalf("new snapshot not applied: %v", got)
	}
	h.MentionRoot = "other"
	h.MentionFiles = []MentionFile{{Path: "three", Name: "three"}}
	if got := rankedMentions(s, h); got[0].Path != "three" {
		t.Fatalf("new root not applied: %v", got)
	}
}

func TestRegressionR8DismissReleasesCachedRows(t *testing.T) {
	h := ConversationHost{MentionFiles: []MentionFile{{Path: "file.go", Name: "file.go"}}, MentionEpoch: 1}
	s := &ConversationState{MentionOpen: true}
	_ = rankedMentions(s, h)
	dismissMention(s)
	if s.mentionRankRows != nil {
		t.Fatalf("dismiss retained %d rows, cap=%d", len(s.mentionRankRows), cap(s.mentionRankRows))
	}
}

func TestRegressionR8QuotedCompletionClosesOnce(t *testing.T) {
	draft := `read @"my file.go" now`
	// Caret immediately before the existing closing quote.
	query, start, end, ok := mentionAt(draft, len([]rune(`read @"my file.go`)))
	if !ok {
		t.Fatal("quoted mention not active")
	}
	got := mentionInsertion(draft, start, end, "my file.go")
	t.Logf("query=%q start=%d end=%d got=%q", query, start, end, got)
	want := `read @"my file.go"  now`
	if got != want {
		t.Fatalf("completion leaves old quote/suffix: got=%q want=%q", got, want)
	}
}

// Diagnostic only: cumulative allocation is not retained heap. Keep just the
// dismissed UI state live after dropping the host, then measure after GC.
func TestRegressionR8ClosedCacheRetainedHeap(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		s := func() *ConversationState {
			files := make([]MentionFile, n)
			for i := range files {
				files[i] = MentionFile{Path: "fixture.go", Name: "fixture.go"}
			}
			h := ConversationHost{MentionFiles: files, MentionEpoch: 1}
			state := &ConversationState{}
			_ = rankedMentions(state, h)
			dismissMention(state)
			return state
		}()
		runtime.GC()
		runtime.ReadMemStats(&after)
		t.Logf("entries=%d post-GC retained heap delta=%d bytes cached len=%d cap=%d", n, int64(after.HeapAlloc)-int64(before.HeapAlloc), len(s.mentionRankRows), cap(s.mentionRankRows))
		runtime.KeepAlive(s)
	}
}

func BenchmarkRegressionR8MentionTransientFrames(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		files := make([]MentionFile, n)
		for i := range files {
			name := fmt.Sprintf("file-%d.go", i)
			path := "internal/" + name
			files[i] = MentionFile{Path: path, Name: name, LowerName: name, LowerPath: path}
		}
		for _, mode := range []string{"ready", "loading", "error"} {
			b.Run(fmt.Sprintf("%s/%d", mode, n), func(b *testing.B) {
				h := ConversationHost{MentionFiles: files, MentionEpoch: 1, MentionRoot: "fixture", MentionLoading: mode == "loading"}
				if mode == "error" {
					h.MentionProblem = "fixture refresh failed"
				}
				s := &ConversationState{MentionQuery: "file-99"}
				_ = rankedMentions(s, h)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = rankedMentions(s, h)
				}
			})
		}
	}
}
