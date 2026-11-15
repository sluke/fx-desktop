package fxlive

// Migrated verbatim from the r6 review probe
// BenchmarkReviewR6DuplicateLiveKeys, so the L6 before/after comparison uses
// the same harness, shapes and sizes. Includes JSON Parse and key assignment;
// it is not a pure-function timing.

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkLiveDuplicateKeys(b *testing.B) {
	for _, n := range []int{100, 500, 1500} {
		for _, mode := range []string{"duplicate", "unique"} {
			b.Run(fmt.Sprintf("%s/%d", mode, n), func(b *testing.B) {
				var s strings.Builder
				s.WriteString(`{"sessions":[{"id":"root","agents":[`)
				for i := 0; i < n; i++ {
					if i > 0 {
						s.WriteByte(',')
					}
					if mode == "duplicate" {
						s.WriteString(`{"name":"a"}`)
					} else {
						fmt.Fprintf(&s, `{"name":"a%d"}`, i)
					}
				}
				s.WriteString(`]}]}`)
				raw := []byte(s.String())
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					r, err := Parse(raw)
					if err != nil || len(r.Agents) != 1 {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
