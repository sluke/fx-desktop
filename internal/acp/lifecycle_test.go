package acp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// stubChild writes a shell script that stands in for fx. A real child process
// is what these tests need: a fake would not prove that reaping happens. The
// script ignores its arguments because the client always inserts the `acp`
// subcommand.
func stubChild(t *testing.T, body string) string {
	t.Helper()
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("/bin/sh is not available")
	}
	path := filepath.Join(t.TempDir(), "fx")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Done reports that the protocol is unusable; Exited reports that the child
// was reaped. A caller that is about to start a replacement needs the second,
// so it must not be closed before cmd.Wait returns.
func TestClientExitedFollowsProcessReap(t *testing.T) {
	child := stubChild(t, "exec /bin/cat > /dev/null")
	c, err := Start(context.Background(), child, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Shutdown(ctx, 3*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// Shutdown's contract is "confirmed reaped", so both signals are closed
	// by the time it returns.
	select {
	case <-c.Exited():
	default:
		t.Fatal("Shutdown returned before the process was reaped")
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Shutdown returned before the protocol side was closed")
	}
}

// A child that ignores stdin EOF is terminated, and its reaping is still
// confirmed: "killed" and "gone" are different facts, and the caller needs the
// second one.
func TestClientShutdownTerminatesAndStillReaps(t *testing.T) {
	child := stubChild(t, "trap '' TERM\nexec /bin/sleep 30")
	c, err := Start(context.Background(), child, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if err := c.Shutdown(ctx, 100*time.Millisecond); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Shutdown took %v, the grace period was not bounded", elapsed)
	}
	select {
	case <-c.Exited():
	default:
		t.Fatal("Shutdown returned before the process was reaped")
	}
}

// Close is idempotent and does not block the caller: the app loop calls it
// while frames are still being drawn.
func TestClientCloseIsIdempotentAndNonBlocking(t *testing.T) {
	child := stubChild(t, "exec /bin/cat > /dev/null")
	c, err := Start(context.Background(), child, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		c.Close()
		c.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked")
	}
	select {
	case <-c.Exited():
	case <-time.After(10 * time.Second):
		t.Fatal("the process was not reaped after Close")
	}
}

// A Shutdown that cannot confirm the exit says so instead of claiming success:
// the caller must not start a replacement conversation on a guess.
func TestClientShutdownReportsAnUnconfirmedExit(t *testing.T) {
	child := stubChild(t, "trap '' TERM\nexec /bin/sleep 30")
	c, err := Start(context.Background(), child, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Shutdown(ctx, time.Second); err == nil {
		t.Fatal("an expired deadline must be reported, not swallowed")
	}
	c.Close()
}
