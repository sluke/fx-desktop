package chrome

import (
	"fmt"
	"strings"
	"testing"
)

// The composer asks which mention the caret is in on every frame. The answer
// is reused while neither the draft nor the caret moves, and is asked again
// as soon as either does.
func TestRegressionR9_MentionAtCaretFollowsDraftAndCaret(t *testing.T) {
	state := &ConversationState{Draft: "see @main.go and @util"}
	check := func(caret int) {
		t.Helper()
		q, s, e, ok := mentionAtCaret(state, caret)
		wq, ws, we, wok := mentionAt(state.Draft, caret)
		if q != wq || s != ws || e != we || ok != wok {
			t.Fatalf("draft %q caret %d: cached = %q [%d,%d) %v, direct = %q [%d,%d) %v", state.Draft, caret, q, s, e, ok, wq, ws, we, wok)
		}
	}
	end := len([]rune(state.Draft))
	check(end)
	check(end) // unchanged frame
	check(8)   // caret moved into the first mention
	check(2)   // caret left every mention
	state.Draft = "see @main.go and @utils"
	check(end) // same caret, edited draft
	state.Draft = "see @main.go and @util" + "s"
	check(end + 1)
	// A different string with the same length and caret is still a new
	// question: the cache compares content, not length.
	state.Draft = "see @mainxgo and @utils"
	check(8)
	state.Draft = "see @mainygo and @utils"
	check(8)

	// An unchanged frame does not decode the draft again.
	state.Draft = strings.Repeat("some pasted log line without any mention\n", 2000) + "@fi"
	caret := len([]rune(state.Draft))
	check(caret)
	if allocs := testing.AllocsPerRun(50, func() { mentionAtCaret(state, caret) }); allocs != 0 {
		t.Errorf("an unchanged frame allocated %.0f times", allocs)
	}
}

func BenchmarkRegressionR9MentionAtCaret(b *testing.B) {
	for _, size := range []int{4 << 10, 64 << 10, 1 << 20} {
		draft := strings.Repeat("some pasted log line without any mention\n", size/41)
		caret := len([]rune(draft))
		b.Run(fmt.Sprintf("direct/draft=%dKiB", size>>10), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				mentionAt(draft, caret)
			}
		})
		b.Run(fmt.Sprintf("steady-frame/draft=%dKiB", size>>10), func(b *testing.B) {
			state := &ConversationState{Draft: draft}
			mentionAtCaret(state, caret)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mentionAtCaret(state, caret)
			}
		})
	}
}
