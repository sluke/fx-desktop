package chrome

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

func skipWithoutTerminalLib(t *testing.T) {
	t.Helper()
	if testing.Short() && os.Getenv("MYGO_GHOSTTY_VT") == "" {
		t.Skip("needs libghostty-vt")
	}
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	if err := terminal.Load(); err != nil {
		t.Skipf("no native terminal library: %v", err)
	}
}

// The drawer's whole point is a live terminal inside the main frame, which no
// fixture can prove: it needs a real process, a real pseudo-terminal and a real
// native library. The test skips where the library is not there, so the rest of
// the suite stays self-contained.
func TestConsoleDrawerHostsALiveTerminal(t *testing.T) {
	skipWithoutTerminalLib(t)
	codes := make(chan int, 1)
	term, err := terminal.New(terminal.Options{
		Command: []string{"/bin/sh", "-c", "echo hello-from-console; exit 3"},
		OnExit:  func(c int) { codes <- c },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()

	h, sh := testHost()
	h.Console = ConsoleData{
		Runs: []ConsoleRun{{ID: 1, Title: "probe", Command: "probe"}},
		Body: func(c *ui.Context) { terminal.View(c, term).Fill() },
	}
	sh.ConsoleOpen = true
	sh.ConsoleRun = 1
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)

	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(term.Text(), "hello-from-console") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		ts.Frame()
	}
	text := term.Text()
	if !strings.Contains(text, "hello-from-console") || !strings.Contains(text, "Process exited with code 3") {
		t.Fatalf("the embedded terminal did not run: %q", text)
	}
	// The drawer's own size is the terminal's: the frame really laid it out.
	if cols, rows := term.Size(); cols < 40 || rows < 5 {
		t.Fatalf("the drawer sized the terminal %d×%d", cols, rows)
	}
	select {
	case c := <-codes:
		if c != 3 {
			t.Fatalf("exit code = %d, want 3", c)
		}
	default:
		t.Fatal("no exit callback")
	}

	// ... and the tab reports the code the process reported.
	h.Console.Runs[0].Exited = true
	h.Console.Runs[0].Exit = 3
	ts.Frame()
	if !ts.HasText("exited 3") {
		t.Fatalf("the tab did not show the exit code\n%v", ts.Texts())
	}
}

// The console must not take the keyboard just by being on screen — the
// composer keeps it — and a click must hand it over, which is what makes
// `fx login` answerable from inside the drawer.
func TestConsoleDrawerTypesOnlyAfterAClick(t *testing.T) {
	skipWithoutTerminalLib(t)
	term, err := terminal.New(terminal.Options{
		Command: []string{"/bin/sh"},
		Env:     []string{"PS1=$ ", "ENV="},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()

	h, sh := testHost()
	h.Console = ConsoleData{
		Runs: []ConsoleRun{{ID: 1, Title: "probe"}},
		Body: func(c *ui.Context) { terminal.View(c, term).Fill() },
	}
	sh.ConsoleOpen = true
	sh.ConsoleRun = 1
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()

	// No click: keystrokes are not the console's.
	ts.Type("echo stolen")
	ts.Key(0, ui.KeyEnter)
	time.Sleep(150 * time.Millisecond)
	ts.Frame()
	if strings.Contains(term.Text(), "stolen") {
		t.Fatalf("the console took the keyboard without a click: %q", term.Text())
	}

	// A click gives it the keyboard, and the program answers.
	if err := ts.Click("Terminal"); err != nil {
		t.Fatal(err)
	}
	ts.Type("echo typed-ok")
	ts.Key(0, ui.KeyEnter)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(term.Text(), "typed-ok") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		ts.Frame()
	}
	if text := term.Text(); !strings.Contains(text, "typed-ok") {
		t.Fatalf("a click did not give the console the keyboard: %q", text)
	}
}
