package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

func pipeClient(t *testing.T, onEvent func(Packet)) (*Client, *json.Decoder, *json.Encoder) {
	t.Helper()
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	c := &Client{input: clientWrite, done: make(chan struct{}), pending: make(map[string]chan Packet), cancel: func() { clientRead.Close(); serverRead.Close() }, onEvent: onEvent}
	go c.read(clientRead)
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
