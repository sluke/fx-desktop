package app

import (
	"encoding/json"
	"testing"

	"fx-desktop/internal/acp"
	"fx-desktop/internal/app/chrome"
)

// cutShellEnvelope is the first 200 bytes of fx's shell result envelope — the
// whole of what an ACP tool result carries, because fx caps the preview there.
// It ends inside the output handle; the command's text (output_delta) is not
// in it. This is the shape the transcript used to print as a JSON payload.
const cutShellEnvelope = `{"session_id":null,"state":"completed","backend":"captured","persistence":"process","output_truncated":false,"output_incomplete":false,"output_terminal_safe":true,"full_output_handle":"fx-command-repl`

// The envelope is bookkeeping, not a payload: it must not survive as text.
func TestShellEnvelopeIsReadAsFlagsNotText(t *testing.T) {
	facts, text, ok := shellResult(cutShellEnvelope)
	if !ok {
		t.Fatal("the shell result envelope was not recognised")
	}
	if text != "" {
		t.Fatalf("the envelope was kept as payload text: %q", text)
	}
	if facts.Truncated || facts.Incomplete || facts.Indeterminate || facts.Signal != "" {
		t.Fatalf("a clean result reported a problem: %+v", facts)
	}
}

// A complete envelope carries the flags that matter and the command's output;
// both have to come through, and the handle must not.
func TestShellEnvelopeKeepsWarningsAndOutput(t *testing.T) {
	envelope := `{"session_id":"s","state":"failed","backend":"captured","persistence":"process","output_truncated":true,"output_incomplete":true,"output_terminal_safe":true,"full_output_handle":"h","exit_code":2,"signal":"SIGKILL","termination_indeterminate":true,"error":null,"retry_guidance":null,"output_delta":"boom\n"}`
	facts, text, ok := shellResult(envelope)
	if !ok {
		t.Fatal("a complete shell envelope was not recognised")
	}
	if !facts.Truncated || !facts.Incomplete || !facts.Indeterminate || facts.Signal != "SIGKILL" {
		t.Fatalf("the result flags were lost: %+v", facts)
	}
	if text != "boom\n" {
		t.Fatalf("the command output became %q", text)
	}
}

// Only fx's shell envelope is treated as one. Ordinary tool text, including a
// JSON object of the tool's own, stays the tool's output.
func TestOrdinaryToolTextIsNotAShellEnvelope(t *testing.T) {
	for _, text := range []string{
		"package main\n",
		`{"command":"go test ./..."}`,
		`{"error":{"type":"tool_execution_failed","message":"boom"}}`,
		"",
	} {
		facts, got, ok := shellResult(text)
		if ok || got != text || facts != (chrome.ShellResult{}) {
			t.Fatalf("%q was read as a shell envelope: %+v %q", text, facts, got)
		}
	}
}

// The command is in the call's arguments, not in its result. It survives the
// result preview being cut, which is what lets an expanded call say what ran.
func TestShellCommandComesFromTheCallArguments(t *testing.T) {
	for raw, want := range map[string]string{
		`{"action":"run","command":"go test ./..."}`: "go test ./...",
		`{"action":"run","command":"  ls  \n"}`:      "ls",
		`{"path":"main.go"}`:                         "",
		``:                                           "",
	} {
		if got := toolCommand(json.RawMessage(raw)); got != want {
			t.Fatalf("toolCommand(%s) = %q, want %q", raw, got, want)
		}
	}
}

// The live path: a call, then its result. The command is kept from the first
// frame, the envelope from the second, and the envelope's text never reaches
// the transcript.
func TestShellCallKeepsCommandAndDropsEnvelope(t *testing.T) {
	c := &chatSession{byID: map[string]int{}}
	call, _ := json.Marshal(map[string]any{
		"sessionId": "s",
		"update": map[string]any{
			"sessionUpdate": "tool_call", "toolCallId": "call-1", "name": "shell",
			"title": "Running", "status": "pending",
			"rawInput": map[string]string{"action": "run", "command": "ls -la"},
		},
	})
	c.event(acp.Packet{Method: "session/update", Params: call})
	result, _ := json.Marshal(map[string]any{
		"sessionId": "s",
		"update": map[string]any{
			"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed",
			"content": []map[string]any{{
				"type":    "content",
				"content": map[string]string{"type": "text", "text": cutShellEnvelope},
			}},
		},
	})
	c.event(acp.Packet{Method: "session/update", Params: result})
	if len(c.messages) != 1 {
		t.Fatalf("the shell call made %d messages", len(c.messages))
	}
	m := c.messages[0]
	if m.Command != "ls -la" {
		t.Fatalf("the command became %q", m.Command)
	}
	if m.Shell == nil {
		t.Fatal("the shell call is not marked as one")
	}
	if m.Text != "" {
		t.Fatalf("the envelope leaked into the payload: %q", m.Text)
	}
}
