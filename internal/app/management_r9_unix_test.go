//go:build unix

package app

// Round-9 regressions for Workspace tools. Synthetic data and fake fx
// scripts; nothing here reads real user state.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

func r9WaitFile(path string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func r9Window(t *testing.T, fx string) (*Coordinator, *ProjectWindow, *manualLoop) {
	t.Helper()
	c := &Coordinator{fxPath: fx}
	loop := &manualLoop{}
	s := &ProjectWindow{coord: c, project: t.TempDir(), shell: chrome.DefaultShell(), loop: loop.dispatch}
	t.Cleanup(func() {
		for _, feature := range []string{"management", "management-read", "fx", "skills"} {
			s.cancelFeature(feature)
		}
	})
	return c, s, loop
}

// A long mutating job (fx upgrade) is started, the sheet is closed and
// reopened, and a second job is asked for. Starting the second one used to
// cancel the first one's scope and SIGKILL the upgrade part-way. The sheet
// now stays busy, the second job is refused, and the first runs to its end
// and reports its result.
func TestRegressionR9_ManagementJobSurvivesReopen(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	fx := filepath.Join(dir, "fx")
	script := "#!/bin/sh\ncase \"$1\" in\nupgrade)\n  : > \"$0.started\"\n  /bin/sleep 1\n  : > \"$0.finished\"\n  echo upgraded\n  ;;\n*) : > \"$0.second\"; echo '{}' ;;\nesac\n"
	if err := os.WriteFile(fx, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	c, s, loop := r9Window(t, fx)
	c.openManagement(s)

	c.runManagementJob(s, "upgrade-channel", []string{"upgrade", "--channel", "stable"})
	if !r9WaitFile(fx+".started", 5*time.Second) {
		t.Fatal("the fake upgrade never started")
	}
	// The sheet's Close button: Open=false + Close().
	s.shell.Tools.Open = false
	c.sessionManagement(s).Close()
	// The user opens Workspace tools again and asks for something else.
	c.openManagement(s)
	if !s.shell.Tools.Busy {
		t.Fatal("reopening the sheet cleared Busy while a mutation was still running")
	}
	c.runManagementJob(s, "config-set", []string{"config", "set", "theme", "dark"})
	c.runManagement(s, "doctor")

	if !r9WaitFile(fx+".finished", 5*time.Second) {
		t.Fatalf("the running `fx upgrade` did not finish; output=%q", s.shell.Tools.Output)
	}
	loop.await(t)
	loop.drain()
	if _, err := os.Stat(fx + ".second"); err == nil {
		t.Error("a second fx command ran while the upgrade was still running")
	}
	if s.shell.Tools.Busy || s.toolsJob {
		t.Errorf("the sheet is still busy after the job ended: busy=%v job=%v", s.shell.Tools.Busy, s.toolsJob)
	}
	if !strings.Contains(s.shell.Tools.Output, "upgraded") {
		t.Errorf("the upgrade's result was not published: %q", s.shell.Tools.Output)
	}
	// With the job over, the sheet works again.
	c.runManagementJob(s, "config-set", []string{"config", "set", "theme", "dark"})
	if !r9WaitFile(fx+".second", 5*time.Second) {
		t.Fatal("a job asked for after the upgrade ended never ran")
	}
	loop.await(t)
	loop.drain()
}

// A job that fails after the sheet was closed must still be reported
// somewhere the user can see once they come back.
func TestRegressionR9_ManagementJobFailureIsKeptWhenSheetClosed(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	fx := filepath.Join(dir, "fx")
	script := "#!/bin/sh\ncase \"$1\" in\nmcp)\n  : > \"$0.started\"\n  /bin/sleep 0.3\n  echo 'mcp add failed: server unreachable' >&2\n  : > \"$0.exited\"\n  exit 3\n  ;;\n*) echo '{}' ;;\nesac\n"
	if err := os.WriteFile(fx, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	c, s, loop := r9Window(t, fx)
	c.openManagement(s)
	c.runManagementJob(s, "mcp-add", []string{"mcp", "add", "srv", "--", "cmd"})
	if !r9WaitFile(fx+".started", 5*time.Second) {
		t.Fatal("the fake job never started")
	}
	s.shell.Tools.Open = false
	c.sessionManagement(s).Close()
	if !r9WaitFile(fx+".exited", 5*time.Second) {
		t.Fatal("the fake job never exited")
	}
	loop.await(t)
	loop.drain()
	c.openManagement(s)
	loop.drain()
	if !strings.Contains(s.shell.Tools.Output, "server unreachable") {
		t.Fatalf("a failed mutation that ended while the sheet was closed left no trace: output=%q busy=%v",
			s.shell.Tools.Output, s.shell.Tools.Busy)
	}
}

// `fx mcp show NAME --json` carries every tool description. A server
// with many tools produces a valid document over 64 KiB; the native view must
// either read it or say briefly that it is too large — not fail the view and
// print the raw JSON prefix as the "problem".
func TestRegressionR9_MCPShowOver64KiB(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	fx := filepath.Join(dir, "fx")
	type tool struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	doc := map[string]any{"name": "big", "source": "user", "transport": "stdio", "status": "connected", "command": "big-mcp"}
	var tools []tool
	for i := 0; i < 60; i++ {
		tools = append(tools, tool{Name: fmt.Sprintf("tool_%02d", i), Description: strings.Repeat("describes one tool. ", 75)})
	}
	doc["tools"] = tools
	payload, _ := json.Marshal(doc)
	if err := os.WriteFile(fx+".json", payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx, []byte("#!/bin/sh\nexec /bin/cat \"$0.json\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c, s, loop := r9Window(t, fx)
	c.openManagement(s)
	c.readManagement(s, "mcp-show", []string{"mcp", "show", "big", "--json"})
	loop.await(t)
	loop.drain()
	state := &s.shell.Tools
	t.Logf("payload=%d bytes; MCPDetail nil=%v; len(MCPProblem)=%d; len(Output)=%d; problem head=%q; output tail=%q",
		len(payload), state.MCPDetail == nil, len(state.MCPProblem), len(state.Output),
		state.MCPProblem[:min(60, len(state.MCPProblem))], state.Output[max(0, len(state.Output)-90):])
	if state.MCPDetail == nil || len(state.MCPDetail.Tools) != 60 {
		t.Errorf("a valid %d-byte `mcp show` document was not shown", len(payload))
	}
	if len(state.MCPProblem) > 1024 {
		t.Errorf("the problem line is a %d-byte raw JSON prefix, not a message", len(state.MCPProblem))
	}
}

// Clear-all reads pages of 100 sessions; they used to go through the 64 KiB
// text budget.
// Each record carries a title and a preview taken from the prompt.
func TestRegressionR9_ClearAllPageOver64KiB(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	fx := filepath.Join(dir, "fx")
	type row struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		Preview       string `json:"preview"`
		WorkspaceRoot string `json:"workspace_root"`
		CreatedAtMS   int64  `json:"created_at_ms"`
		UpdatedAtMS   int64  `json:"updated_at_ms"`
		HistoryLen    int    `json:"history_len"`
	}
	var rows []row
	for i := 0; i < 100; i++ {
		rows = append(rows, row{
			ID: fmt.Sprintf("sess_%04d", i), Title: strings.Repeat("refactor the importer ", 4),
			Preview:       strings.Repeat("please look at the failing test and ", 16),
			WorkspaceRoot: "/synthetic/project", CreatedAtMS: 1, UpdatedAtMS: int64(1000 - i), HistoryLen: 3,
		})
	}
	payload, _ := json.Marshal(map[string]any{"sessions": rows, "has_more": false})
	if err := os.WriteFile(fx+".json", payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx, []byte("#!/bin/sh\nexec /bin/cat \"$0.json\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessions, err := readAllHistorySessions(ctx, fx, dir, false)
	t.Logf("payload=%d bytes (%d B/session); sessions=%d err=%v", len(payload), len(payload)/100, len(sessions), err)
	if err != nil || len(sessions) != 100 {
		t.Fatalf("clear-all could not list a 100-session page: sessions=%d err=%v", len(sessions), err)
	}
}

// The Tools > Doctor page reads doctor through the management runner, which
// r8's cmdlimit fix did not cover. fx exits 1 (allowed for doctor) while a
// helper still holds stdout: the stream never reached EOF, so the prefix must
// not be published as a complete report.
func TestRegressionR9_ManagementDoctorRefusesIncompleteExitOne(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	fx := filepath.Join(dir, "fx")
	script := "#!/bin/sh\nprintf '%s' '{\"checks\":[{\"name\":\"auth\",\"status\":\"fail\",\"detail\":\"prefix only\"}],\"ok_count\":0,\"warn_count\":0,\"fail_count\":1}'\n/bin/sleep 3 &\necho $! > \"$0.pid\"\nexit 1\n"
	if err := os.WriteFile(fx, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if data, err := os.ReadFile(fx + ".pid"); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	c, s, loop := r9Window(t, fx)
	c.openManagement(s)
	start := time.Now()
	c.readManagement(s, "doctor", nil)
	loop.await(t)
	loop.drain()
	state := &s.shell.Tools
	t.Logf("returned after %v; doctor rows=%d output=%q", time.Since(start).Round(time.Millisecond), len(state.Doctor), state.Output)
	if len(state.Doctor) != 0 {
		t.Fatalf("an exit-1 doctor stream that never reached EOF was published as a report: rows=%+v output=%q", state.Doctor, state.Output)
	}
	if state.Busy || state.Output == "" || state.Output == "Reading fx…" {
		t.Errorf("the refused report left no reason on the page: busy=%v output=%q", state.Busy, state.Output)
	}
	// The reason is a sentence, not the runtime's wording for it.
	if !strings.Contains(state.Output, "incomplete") || strings.Contains(state.Output, "WaitDelay") {
		t.Errorf("the reason shown = %q", state.Output)
	}
}

// The document budget is for decoding. A document that decodes as JSON but is
// not what the page expects used to be printed raw, and with the larger
// budget that would be megabytes in the sheet: what a page shows stays
// bounded, and the problem line is a sentence.
func TestRegressionR9_FailedReadDoesNotPublishTheWholeDocument(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	fx := filepath.Join(dir, "fx")
	// Valid JSON, wrong shape: "models" should be a list.
	payload, _ := json.Marshal(map[string]any{"models": strings.Repeat("not a list ", 30000)})
	if len(payload) < 4*maxToolsTextBytes || len(payload) > managementJSONBytes {
		t.Fatalf("fixture is %d bytes; it must exceed the text bound and fit the document budget", len(payload))
	}
	if err := os.WriteFile(fx+".json", payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx, []byte("#!/bin/sh\nexec /bin/cat \"$0.json\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c, s, loop := r9Window(t, fx)
	c.openManagement(s)
	c.readManagement(s, "models", nil)
	loop.await(t)
	loop.drain()
	state := &s.shell.Tools
	if len(state.Models) != 0 {
		t.Fatalf("a document of the wrong shape produced %d models", len(state.Models))
	}
	if limit := maxToolsTextBytes + 64; len(state.Output) > limit {
		t.Errorf("the sheet shows %d bytes of raw document, want at most %d", len(state.Output), limit)
	}
	if state.ModelsProblem != "fx returned unreadable model JSON" {
		t.Errorf("the problem line = %.120q (%d bytes)", state.ModelsProblem, len(state.ModelsProblem))
	}
	if state.Busy {
		t.Error("the sheet is still busy")
	}
}

func TestRegressionR9_ReadProblemAndIncompleteOutput(t *testing.T) {
	short := "fx: not signed in"
	if got := readProblem("  "+short+"\n", errors.New("exit status 1"), "fallback"); got != short {
		t.Errorf("fx's own short text = %q", got)
	}
	long := strings.Repeat("x", maxProblemTextBytes+1)
	if got := readProblem(long, errors.New("exit status 1"), "fallback"); got != "exit status 1" {
		t.Errorf("a long stdout was shown as the problem: %.40q", got)
	}
	if got := readProblem(long, nil, "fallback"); got != "fallback" {
		t.Errorf("no text and no error = %q", got)
	}
	if got := readProblem("", nil, "fallback"); got != "fallback" {
		t.Errorf("empty = %q", got)
	}

	var err error = incompleteOutput{}
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Error("incompleteOutput does not answer errors.Is(err, exec.ErrWaitDelay)")
	}
	joined := errors.Join(&exec.ExitError{}, incompleteOutput{})
	if !errors.Is(joined, exec.ErrWaitDelay) {
		t.Error("the joined run error lost the incomplete mark")
	}
	if _, ok := decodeDoctor(`{"checks":[{"name":"auth","status":"ok"}]}`, joined); ok {
		t.Error("a doctor document from an incomplete stream was accepted")
	}
}
