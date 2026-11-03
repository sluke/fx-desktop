package fxcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubFx writes a script that records its argv and prints body.
func stubFx(t *testing.T, body string) (path, argvLog string) {
	t.Helper()
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv.txt")
	path = filepath.Join(dir, "fx")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argvLog + "'\ncat <<'JSON'\n" + body + "\nJSON\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, argvLog
}

// Every inspection is `fx <args> --json`: the flag is the adapter's promise,
// not something a caller can forget.
func TestReadStatusAppendsJSONAndDecodes(t *testing.T) {
	path, argvLog := stubFx(t, `{"model":"test/model","auth":"missing","auth_help":"run fx login"}`)
	st, err := ReadStatus(context.Background(), path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if st.Model != "test/model" || st.Auth != "missing" || st.AuthHelp != "run fx login" {
		t.Fatalf("status = %+v", st)
	}
	argv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(argv))
	if len(lines) != 2 || lines[0] != "status" || lines[1] != "--json" {
		t.Fatalf("argv = %v", lines)
	}
}

// An unset path is a configuration error, not a process that failed: the
// caller must not read it as "fx says no".
func TestRunRefusesAnEmptyPath(t *testing.T) {
	err := Run(context.Background(), "", t.TempDir(), &struct{}{}, "status")
	if err == nil || !strings.Contains(err.Error(), "not set") {
		t.Fatalf("err = %v", err)
	}
}

// The query obeys its caller's deadline, so a hung fx cannot hold a window's
// background task open.
func TestQueryObeysTheCallerContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fx")
	// exec replaces the shell, so no grandchild keeps stdout open after the
	// query is cancelled.
	script := "#!/bin/sh\nexec /bin/sleep 30\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := ReadStatus(ctx, path, dir); err == nil {
		t.Fatal("a cancelled query must fail")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the query outlived its context by %v", elapsed)
	}
}

// A failed status read renders as "nothing known", never as "signed out".
func TestReadAuthIsEmptyOnFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-fx")
	if auth := ReadAuth(missing, t.TempDir()); auth.Auth != "" || auth.Help != "" || len(auth.Providers) != 0 {
		t.Fatalf("auth = %+v", auth)
	}
}

// A malformed answer is an error: decoding must not silently hand back a zero
// value that reads as "fx reports nothing".
func TestRunRejectsMalformedJSON(t *testing.T) {
	path, _ := stubFx(t, `not json`)
	var out Status
	if err := Run(context.Background(), path, t.TempDir(), &out, "status"); err == nil {
		t.Fatal("malformed output must be reported")
	}
}
