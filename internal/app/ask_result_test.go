package app

// Ask result-boundary tests. TestAskTruncationReachesUI was migrated from the
// r5 review probe TestReviewR5AskTruncationReachesUI.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fx-desktop/internal/app/chrome"
)

// A cut-off --json document is an incomplete result, not a smaller answer.
// The UI must say so instead of showing 2 MiB of JSON prefix as the answer.
func TestAskTruncationReachesUI(t *testing.T) {
	dir := t.TempDir()
	fx := filepath.Join(dir, "fx")
	payload, _ := json.Marshal(map[string]any{"output": strings.Repeat("x", (2<<20)+128), "exit_code": 0})
	if err := os.WriteFile(fx+".json", payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx, []byte("#!/bin/sh\nexec /bin/cat \"$0.json\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{fxPath: fx}
	loop := &manualLoop{}
	s := &ProjectWindow{coord: c, project: dir, shell: chrome.DefaultShell(), loop: loop.dispatch}
	c.runAsk(s, chrome.AskRequest{Prompt: "fixture"})
	loop.await(t)
	loop.drain()
	if s.shell.AskBusy {
		t.Fatal("ask still busy")
	}
	if s.shell.AskProblem == "" {
		t.Fatalf("truncated JSON reported no problem; output bytes=%d prefix=%q", len(s.shell.AskOutput), s.shell.AskOutput[:min(35, len(s.shell.AskOutput))])
	}
	if strings.Contains(s.shell.AskOutput, `"output"`) {
		t.Fatalf("raw JSON prefix was presented as an answer: %q", s.shell.AskOutput[:min(80, len(s.shell.AskOutput))])
	}
	if !strings.Contains(s.shell.AskProblem, "incomplete") && !strings.Contains(s.shell.AskProblem, "cut off") {
		t.Fatalf("problem does not say the result is incomplete: %q", s.shell.AskProblem)
	}
}

// One boundary owns every stream fact. These cases are the pure
// "command result -> Ask domain result" contract (A5).
func TestResolveAskOutcomeCoversStreamMetadata(t *testing.T) {
	clean := `{"output":"the answer","final_output":"the answer","exit_code":0}`
	tests := []struct {
		name       string
		streams    fxStreams
		wantOutput string
		wantParts  []string
		noOutput   bool
	}{
		{name: "normal json", streams: fxStreams{stdout: clean}, wantOutput: "the answer"},
		{
			name:    "stdout truncated",
			streams: fxStreams{stdout: `{"output":"xxxxxxxx`, stdoutTruncated: true},
			wantParts: []string{"cut off", "incomplete"}, noOutput: true,
		},
		{
			name:    "valid json then truncated extra output",
			streams: fxStreams{stdout: clean + "\ntrailing junk", stdoutTruncated: true},
			wantOutput: "the answer", wantParts: []string{"extra output", "incomplete"},
		},
		{
			name: "exit zero malformed json", streams: fxStreams{stdout: "not json at all"},
			wantParts: []string{"readable --json"}, noOutput: true,
		},
		{
			name: "exit zero type drift", streams: fxStreams{stdout: `{"output":5}`},
			wantParts: []string{"readable --json"}, noOutput: true,
		},
		{
			name: "stderr truncated on exit zero", streams: fxStreams{stdout: clean, stderr: "warn", stderrTruncated: true},
			wantOutput: "the answer", wantParts: []string{"diagnostics were truncated"},
		},
		{
			name: "nonzero exit", streams: fxStreams{stdout: clean, exitCode: 1, err: errors.New("fx ask: exit status 1")},
			wantOutput: "the answer", wantParts: []string{"exit status 1"},
		},
		{
			name: "cancelled without json", streams: fxStreams{canceled: true, err: errors.New("fx ask: canceled")},
			wantParts: []string{"cancelled"}, noOutput: true,
		},
		{
			name: "timed out without json", streams: fxStreams{timedOut: true, err: errors.New("fx ask: deadline")},
			wantParts: []string{"timed out"}, noOutput: true,
		},
		{
			name: "auth failure keeps partial answer",
			streams: fxStreams{stdout: `{"output":"half","auth_failure":{"source":"fx login","reason":"expired","http_status":401}}`},
			wantOutput: "half", wantParts: []string{"fx login", "expired", "HTTP 401"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome := resolveAskOutcome(tt.streams)
			output, _, problem := outcome.presentation()
			if tt.noOutput && output != "" {
				t.Fatalf("untrusted output became the answer: %q", output)
			}
			if output != tt.wantOutput {
				t.Fatalf("output=%q, want %q", output, tt.wantOutput)
			}
			for _, want := range tt.wantParts {
				if !strings.Contains(problem, want) {
					t.Fatalf("problem=%q missing %q", problem, want)
				}
			}
			if tt.name == "normal json" && problem != "" {
				t.Fatalf("clean result reported a problem: %q", problem)
			}
		})
	}
}

// A result that exactly fills the capture is complete, not truncated. The
// whole answer has to reach the UI, with no cap note.
func TestAskExactlyAtCaptureLimitReachesUI(t *testing.T) {
	dir := t.TempDir()
	fx := filepath.Join(dir, "fx")
	const limit = 2 * 1024 * 1024
	answer := strings.Repeat("a", limit-27) // 11-byte prefix + 16-byte suffix
	document := `{"output":"` + answer + `","exit_code":0}`
	if len(document) != limit {
		t.Fatalf("fixture is %d bytes, want %d", len(document), limit)
	}
	if err := os.WriteFile(fx+".json", []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx, []byte("#!/bin/sh\nexec /bin/cat \"$0.json\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{fxPath: fx}
	loop := &manualLoop{}
	s := &ProjectWindow{coord: c, project: dir, shell: chrome.DefaultShell(), loop: loop.dispatch}
	c.runAsk(s, chrome.AskRequest{Prompt: "fixture"})
	loop.await(t)
	loop.drain()
	if s.shell.AskBusy || s.shell.AskProblem != "" {
		t.Fatalf("exact-limit result was reported incomplete: problem=%q", s.shell.AskProblem)
	}
	if s.shell.AskOutput != answer {
		t.Fatalf("answer length=%d, want %d", len(s.shell.AskOutput), len(answer))
	}
}

// Exit 0 with malformed --json must be a protocol problem in the UI, never
// an answer made of the raw document.
func TestAskMalformedExitZeroReportsProtocolProblem(t *testing.T) {
	dir := t.TempDir()
	fx := filepath.Join(dir, "fx")
	if err := os.WriteFile(fx, []byte("#!/bin/sh\nprintf '%s' '{\"output\":\"half'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{fxPath: fx}
	loop := &manualLoop{}
	s := &ProjectWindow{coord: c, project: dir, shell: chrome.DefaultShell(), loop: loop.dispatch}
	c.runAsk(s, chrome.AskRequest{Prompt: "fixture"})
	loop.await(t)
	loop.drain()
	if s.shell.AskProblem == "" || strings.Contains(s.shell.AskOutput, "output") {
		t.Fatalf("malformed --json was shown as an answer: output=%q problem=%q", s.shell.AskOutput, s.shell.AskProblem)
	}
	if !strings.Contains(s.shell.AskProblem, "Raw output (bounded)") {
		t.Fatalf("bounded diagnostic missing: %q", s.shell.AskProblem)
	}
}
