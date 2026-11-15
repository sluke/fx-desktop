package app

// P6: the newest status must win at the end of a run, and a bounded line
// must stay valid UTF-8 when a rune is split across Write calls.
// TestProgressLatestWinsAtFinish and TestProgressLongSplitRune were migrated
// from the r6 review probes.

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestProgressLatestWinsAtFinish(t *testing.T) {
	var lines []string
	b := managementOutput{limit: 64, emit: func(s string) { lines = append(lines, s) }}
	_, _ = b.Write([]byte("ready\n"))
	_, _ = b.Write([]byte("retry\n"))
	_, _ = b.Write([]byte("ready\n"))
	b.flushPending()
	if len(lines) == 0 || lines[len(lines)-1] != "ready" {
		t.Fatalf("final published stale state: %q", lines)
	}
}

// The whole callback sequence matters, not just one cached field. The first
// write publishes immediately (zero last time); the rest are throttled.
func TestProgressCallbackSequence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes []string
		want   []string
	}{
		{"A-B-A", []string{"ready\n", "retry\n", "ready\n"}, []string{"ready"}},
		{"A-B-C", []string{"a\n", "b\n", "c\n"}, []string{"a", "c"}},
		{"repeat-A", []string{"a\n", "a\n", "a\n"}, []string{"a"}},
		{"blank-lines", []string{"a\n", "\n", "\n", "a\n"}, []string{"a"}},
		{"CR-progress", []string{"a\r", "b\r", "a\r"}, []string{"a"}},
		{"CRLF-progress", []string{"a\r\n", "b\r\n", "a\r\n"}, []string{"a"}},
		{"no-final-newline", []string{"a\n", "b"}, []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			b := managementOutput{limit: 64, emit: func(s string) { got = append(got, s) }}
			for _, chunk := range tc.writes {
				_, _ = b.Write([]byte(chunk))
			}
			b.flushPending()
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("callbacks = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProgressLongSplitRune(t *testing.T) {
	var lines []string
	b := managementOutput{limit: 8, emit: func(s string) { lines = append(lines, s) }}
	data := []byte(strings.Repeat("中", 2000) + "🙂\n")
	_, _ = b.Write(data[:6002])
	_, _ = b.Write(data[6002:])
	b.flushPending()
	if len(lines) == 0 {
		t.Fatal("no completed progress")
	}
	for _, s := range lines {
		if !utf8.ValidString(s) {
			t.Fatalf("valid stream produced invalid UTF-8 progress: len=%d head=%x", len(s), []byte(s[:min(8, len(s))]))
		}
		if len(s) > maxProgressLineBytes {
			t.Fatalf("progress line of %d bytes exceeds the budget", len(s))
		}
	}
	if !strings.HasSuffix(lines[len(lines)-1], "🙂") {
		t.Fatalf("newest tail was lost: %q", lines[len(lines)-1])
	}
}

// A 2-, 3- or 4-byte rune may be split at every byte boundary, and the line
// may be shorter than, equal to or longer than the line budget.
func TestProgressSplitRuneAtEveryBoundary(t *testing.T) {
	for ri, r := range []string{"é", "中", "🙂"} {
		runeBytes := []byte(r)
		for split := 1; split < len(runeBytes); split++ {
			for _, filler := range []int{0, maxProgressLineBytes - len(runeBytes), maxProgressLineBytes} {
				t.Run(fmt.Sprintf("rune%d-split%d-filler%d", ri, split, filler), func(t *testing.T) {
					var got []string
					b := managementOutput{limit: 64, emit: func(s string) { got = append(got, s) }}
					b.last = time.Now()
					data := []byte(strings.Repeat("q", filler) + r + "\n")
					cut := len(data) - 1 - len(runeBytes) + split
					_, _ = b.Write(data[:cut])
					_, _ = b.Write(data[cut:])
					b.flushPending()
					if len(got) == 0 {
						t.Fatal("no completed progress")
					}
					last := got[len(got)-1]
					if !utf8.ValidString(last) || len(last) > maxProgressLineBytes {
						t.Fatalf("invalid or unbounded progress: len=%d valid=%v", len(last), utf8.ValidString(last))
					}
					if !strings.HasSuffix(last, r) {
						t.Fatalf("newest rune lost: %q", last)
					}
				})
			}
		}
	}
}

// Invalid source bytes must not turn a valid stream into invalid output;
// the published line is always valid UTF-8 and bounded.
func TestProgressInvalidSourceBytesStayBounded(t *testing.T) {
	var got []string
	b := managementOutput{limit: 64, emit: func(s string) { got = append(got, s) }}
	_, _ = b.Write([]byte("ok\xffbad\n"))
	b.flushPending()
	if len(got) == 0 || !utf8.ValidString(got[len(got)-1]) {
		t.Fatalf("invalid source bytes produced invalid progress: %q", got)
	}
	if len(got[len(got)-1]) > maxProgressLineBytes {
		t.Fatalf("unbounded progress: %q", got[len(got)-1])
	}
}
