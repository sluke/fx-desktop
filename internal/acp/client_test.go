package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func pipeClient(t *testing.T, onEvent func(Packet)) (*Client, *json.Decoder, *json.Encoder) {
	t.Helper()
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	c := &Client{input: clientWrite, done: make(chan struct{}), exited: make(chan struct{}), pending: make(map[string]chan Packet), cancel: func() { clientRead.Close(); serverRead.Close() }, onEvent: onEvent}
	c.startWritePump()
	go func() {
		// Mirror the real pump: the protocol side is closed when reading ends.
		err := c.read(clientRead)
		if err == nil {
			err = ErrClosed
		}
		c.finish(err)
	}()
	t.Cleanup(func() { c.Close(); serverWrite.Close() })
	return c, json.NewDecoder(serverRead), json.NewEncoder(serverWrite)
}
func TestClientRoutesStreamingUpdatesAndPermissionWhilePromptPending(t *testing.T) {
	events := make(chan Packet, 3)
	c, decoder, encoder := pipeClient(t, func(p Packet) { events <- p })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var result map[string]string
		err := c.Call(ctx, "session/prompt", map[string]string{"sessionId": "s1"}, &result)
		if err == nil && result["stopReason"] != "end_turn" {
			err = errors.New("wrong result")
		}
		done <- err
	}()
	var request Packet
	if err := decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]string{"text": "你好"}})
	encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "permission-1", "method": "session/request_permission", "params": map[string]any{}})
	if event := <-events; event.Method != "session/update" {
		t.Fatalf("unexpected update: %+v", event)
	}
	permission := <-events
	go c.Respond(permission.ID, map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": "reject_once"}})
	var reply Packet
	if err := decoder.Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if string(reply.ID) != `"permission-1"` {
		t.Fatalf("permission id changed: %s", reply.ID)
	}
	encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]string{"stopReason": "end_turn"}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestClientTimeoutAndCloseReleasePendingRequests(t *testing.T) {
	c, decoder, _ := pipeClient(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Call(ctx, "slow", nil, nil) }()
	var request Packet
	decoder.Decode(&request)
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	go func() { done <- c.Call(context.Background(), "waiting", nil, nil) }()
	decoder.Decode(&request)
	c.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close left request blocked")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) != 0 {
		t.Fatal("pending requests leaked")
	}
}
// When the peer never reads stdin, Call must still return promptly on cancel
// instead of blocking in the pipe write forever.
func TestCallCancelsDuringBlockedWrite(t *testing.T) {
	c, _, _ := pipeClient(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Call(ctx, "blocked", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel during blocked write: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Call stayed blocked after cancel")
	}
}

func TestCallPrecanceledReturnsImmediately(t *testing.T) {
	c, _, _ := pipeClient(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.Call(ctx, "early", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("precancel: %v", err)
	}
}

type blockingWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}
func (w *blockingWriter) Close() error { return nil }

func TestQueuedCancelLeavesConnectionUsable(t *testing.T) {
	w := &blockingWriter{started: make(chan struct{}), release: make(chan struct{})}
	c := &Client{
		input:   w,
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
		pending: map[string]chan Packet{},
		cancel:  func() {},
	}
	c.startWritePump()
	t.Cleanup(c.Close)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	first := make(chan error, 1)
	go func() { first <- c.Call(ctx1, "hold", nil, nil) }()
	select {
	case <-w.started:
	case <-time.After(time.Second):
		t.Fatal("first write did not start")
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() { second <- c.Call(ctx2, "queued", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	cancel2()
	close(w.release)

	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued call did not return")
	}
	if err := c.Notify("still-open", map[string]string{"ok": "1"}); err != nil {
		t.Fatalf("connection died after a queued cancel: %v", err)
	}
	select {
	case <-c.Done():
		t.Fatal("cancel before write started closed the connection")
	default:
	}
	_ = first
}

func TestNotifyRespectsClosedConnection(t *testing.T) {
	c, _, _ := pipeClient(t, nil)
	c.Close()
	if err := c.Notify("n", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("notify on closed: %v", err)
	}
}

func TestClientReportsRPCErrorAndRejectsMalformedFrames(t *testing.T) {
	c, decoder, encoder := pipeClient(t, nil)
	done := make(chan error, 1)
	go func() { done <- c.Call(context.Background(), "nope", nil, nil) }()
	var req Packet
	decoder.Decode(&req)
	encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": Error{Code: -32603, Message: "Sign in required"}})
	if err := <-done; err == nil || err.Error() != "Sign in required" {
		t.Fatalf("lost error: %v", err)
	}
	encoder.Encode(map[string]string{"garbage": "invalid"})
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("invalid frame did not close connection")
	}
}
