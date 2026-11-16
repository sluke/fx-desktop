package chrome

import (
	"fmt"
	"testing"
)

// BenchmarkMentionRankColdWarm keeps the r7 P7 repeated-ranking harness and
// adds the cached path. cold calls the pure algorithm on every frame (the
// pre-fix shape); warm calls the UI owner that reuses rows until the snapshot,
// query or project changes. The warm cost must not grow with project size.
func BenchmarkMentionRankColdWarm(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		files := make([]MentionFile, n)
		for i := range files {
			name := fmt.Sprintf("file-%d.go", i)
			path := "internal/" + name
			files[i] = MentionFile{Path: path, Name: name, Dir: "internal", LowerName: name, LowerPath: path}
		}
		b.Run(fmt.Sprintf("cold/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = rankMentionFiles(files, "file-99", mentionLimit)
			}
		})
		b.Run(fmt.Sprintf("warm/%d", n), func(b *testing.B) {
			host := ConversationHost{MentionFiles: files, MentionEpoch: 1, MentionRoot: "/proj"}
			state := &ConversationState{MentionQuery: "file-99"}
			_ = rankedMentions(state, host) // fill the cache before timing
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = rankedMentions(state, host)
			}
		})
	}
}
