package app

// Stream-capture regression tests. The first two were migrated from the r5
// review probes in .tmp/review-20261115-r5/ (TestReviewR5ProgressSurvivesCaptureCap
// and BenchmarkReviewR5StreamCapture); they now live with the production code.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A full capture must not freeze progress: fx keeps writing status lines long
// after the diagnostic budget is spent, and the UI has to show the newest one.
func TestStreamCaptureProgressSurvivesCaptureCap(t *testing.T) {
	var got []string
	b := managementOutput{limit: 8, emit: func(s string) { got = append(got, s) }}
	_, _ = b.Write([]byte("old-old\n"))
	b.last = time.Time{}
	_, _ = b.Write([]byte("new status\n"))
	if len(got) == 0 || got[len(got)-1] != "new status" {
		t.Fatalf("progress froze after capture cap: %q", got)
	}
}

// StreamCapture is the r5 allocation/complexity guard. The bounded-copy mode
// is an append-only control, not an implemented optimisation: it shows what
// the same bytes cost without a full-history parse.
func BenchmarkStreamCapture(b *testing.B) {
	chunk := bytes.Repeat([]byte("x\n"), 2048)
	// Real CLI diagnostics are dominated by a few long lines, not by 2048
	// one-byte lines. Both shapes must stay linear.
	realShape := bytes.Repeat([]byte(`{"level":"info","message":"contacting provider","attempt":1}`+"\n"), 1024)
	sink := func(string) {}
	for _, size := range []int{64 << 10, 256 << 10, 1 << 20} {
		for _, mode := range []string{"capture", "capture-progress", "capture-progress-long-lines", "bounded-copy-control"} {
			b.Run(fmt.Sprintf("%s/%dKiB", mode, size>>10), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					block := chunk
					if mode == "capture-progress-long-lines" {
						block = realShape[:min(len(realShape), len(chunk))]
					}
					if mode == "bounded-copy-control" {
						var out bytes.Buffer
						for n := 0; n < size; n += len(chunk) {
							_, _ = out.Write(chunk)
						}
						continue
					}
					out := managementOutput{limit: size}
					if mode == "capture-progress" || mode == "capture-progress-long-lines" {
						out.emit = sink
					}
					for n := 0; n < size; n += len(block) {
						_, _ = out.Write(block)
					}
				}
			})
		}
	}
}

// A capture with no progress callback must do no line work at all: stdout is
// the payload, and its writes are pure bounded appends.
func TestStreamCaptureWithoutEmitterDoesNotParseLines(t *testing.T) {
	b := managementOutput{limit: 64}
	chunks := []string{"first\n", "second\n", "third\n"}
	for _, chunk := range chunks {
		if n, err := b.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if got := b.buffer.String(); got != strings.Join(chunks, "") {
		t.Fatalf("captured %q", got)
	}
	if b.pending != "" || len(b.current) != 0 {
		t.Fatalf("no emitter but line state was built: pending=%q current=%q", b.pending, b.current)
	}
}

// Cross-chunk lines, CR/LF, blank lines and a rune split across writes must
// all be handled from the new bytes alone.
func TestStreamCaptureLineBoundaries(t *testing.T) {
	var got []string
	b := managementOutput{limit: 1 << 20, emit: func(s string) { got = append(got, s) }}
	write := func(s string) {
		b.last = time.Time{}
		_, _ = b.Write([]byte(s))
	}
	b.last = time.Time{}
	_, _ = b.Write([]byte("first line\r\n"))
	b.last = time.Time{}
	_, _ = b.Write([]byte("second line"))
	b.last = time.Time{}
	_, _ = b.Write([]byte(" continued\n\n"))
	b.last = time.Time{}
	_, _ = b.Write([]byte("中文状态\n"))
	b.last = time.Time{}
	_, _ = b.Write([]byte("half \xe4"))
	b.last = time.Time{}
	_, _ = b.Write([]byte("\xb8\xad\xe6\x96\x87\n"))
	write("x" + strings.Repeat("y", 512) + "\n")
	want := []string{"first line", "second line continued", "中文状态", "half 中文", "x" + strings.Repeat("y", 512)}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("progress lines = %q, want %q", got, want)
	}
	for _, line := range got {
		if len(line) > maxProgressLineBytes {
			t.Fatalf("progress line of %d bytes exceeds the bound", len(line))
		}
	}
}

// A giant line without a newline is bounded, and the capture cap never stops
// the stream from draining new status.
func TestStreamCaptureGiantLineIsBounded(t *testing.T) {
	var got []string
	b := managementOutput{limit: 16, emit: func(s string) { got = append(got, s) }}
	chunk := strings.Repeat("z", 8<<10)
	for i := 0; i < 8; i++ {
		b.last = time.Time{}
		_, _ = b.Write([]byte(chunk))
	}
	if len(b.current) > maxProgressLineBytes {
		t.Fatalf("unbounded line buffer: %d bytes", len(b.current))
	}
	b.last = time.Time{}
	_, _ = b.Write([]byte("done\n"))
	if len(got) == 0 || !strings.HasSuffix(got[len(got)-1], "done") {
		t.Fatalf("status after a giant line was lost: %q", got)
	}
}

// A real fx-shaped run: stdout and stderr both write, stderr passes its
// 32 KiB capture cap, and the newest retry line must still reach the UI.
func TestManagementStreamsProgressSurvivesStderrCap(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fx")
	script := `#!/bin/sh
i=0
while [ $i -lt 1200 ]; do echo "filler filler filler filler filler filler filler filler" >&2; i=$((i+1)); done
echo "retry 19" >&2
printf '{}'
`
	if err := os.WriteFile(file, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	streams := managementStreamsProgress(context.Background(), file, dir, 64*1024, func(line string) {
		lines = append(lines, line)
	}, "ask", "--json", "hi")
	if streams.err != nil || streams.stdout != "{}" || !streams.stderrTruncated {
		t.Fatalf("streams=%+v", streams)
	}
	if len(lines) == 0 || lines[len(lines)-1] != "retry 19" {
		t.Fatalf("progress stopped at the capture cap: %q", lines)
	}
}

// The final status is published even when the process ends without a
// trailing newline, and cancellation keeps the outcome explicit.
func TestManagementStreamsFinalProgressAndCancellation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fx")
	if err := os.WriteFile(file, []byte("#!/bin/sh\nprintf 'last status (no newline)' >&2\nprintf '{}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	streams := managementStreamsProgress(context.Background(), file, dir, 64*1024, func(line string) {
		lines = append(lines, line)
	}, "ask", "--json", "hi")
	if streams.err != nil || len(lines) == 0 || lines[len(lines)-1] != "last status (no newline)" {
		t.Fatalf("final unterminated line was lost: lines=%q streams=%+v", lines, streams)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := managementStreams(ctx, file, dir, 64*1024, "ask", "--json", "hi")
	if !canceled.canceled {
		t.Fatalf("cancellation not recorded: %+v", canceled)
	}
}

// The latest pending status must still be published when a run ends, and a
// callback string is a bounded copy, not a window onto capture history.
func TestStreamCaptureFinalProgressAfterThrottle(t *testing.T) {
	var got []string
	b := managementOutput{limit: 32, emit: func(s string) { got = append(got, s) }}
	_, _ = b.Write([]byte("retry 1\n"))
	_, _ = b.Write([]byte("retry 2\n")) // throttled: pending only
	if len(got) != 1 || got[0] != "retry 1" {
		t.Fatalf("throttle published %q", got)
	}
	if b.pending != "retry 2" {
		t.Fatalf("latest pending = %q, want retry 2", b.pending)
	}
	if line := b.flushPending(); line != "retry 2" || got[len(got)-1] != "retry 2" {
		t.Fatalf("final flush = %q, emissions = %q", line, got)
	}
	if b.flushPending() != "" {
		t.Fatal("flush is not idempotent")
	}
}
