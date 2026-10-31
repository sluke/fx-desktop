package app

import (
	"encoding/json"
	"strings"

	"fx-desktop/internal/app/chrome"
)

// fx reports a shell result as an envelope: session, capture mode, output
// handles, then the command's text. The ACP client receives the first 200
// bytes of it, which is all bookkeeping — the preview ends inside the handle
// value, long before output_delta. The transcript therefore never shows the
// envelope: it keeps the flags that change what the result means and shows
// the command instead. These helpers do that split where the protocol lives,
// so the chrome package only sees the command and the flags.

// toolCommand returns the command a shell call was asked to run. fx sends the
// call's arguments as rawInput; a command is what tells a shell call from the
// rest.
func toolCommand(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	return strings.TrimSpace(in.Command)
}

// shellResult separates fx's shell result envelope from the payload a reader
// can use. The envelope's text becomes the command's output when the envelope
// was complete enough to carry one ("" otherwise), and the flags are returned
// so the transcript can say what happened to the output it cannot show. ok is
// false when the text is not an envelope at all: then it belongs to the tool
// as ordinary output.
func shellResult(text string) (chrome.ShellResult, string, bool) {
	fields, ok := jsonHeadObject(text)
	if !ok || !isShellEnvelope(fields) {
		return chrome.ShellResult{}, text, false
	}
	r := chrome.ShellResult{
		Truncated:     jsonBool(fields["output_truncated"]),
		Incomplete:    jsonBool(fields["output_incomplete"]),
		Indeterminate: jsonBool(fields["termination_indeterminate"]),
		Signal:        jsonString(fields["signal"]),
	}
	return r, envelopeOutput(fields), true
}

// isShellEnvelope recognises fx's shell result by the fields it always
// carries. Two of them are enough that a JSON payload which merely has a
// state or a session id is not mistaken for one.
func isShellEnvelope(fields map[string]json.RawMessage) bool {
	marks := []string{
		"session_id", "backend", "persistence", "output_truncated",
		"output_incomplete", "output_terminal_safe", "full_output_handle",
		"retry_guidance",
	}
	seen := 0
	for _, key := range marks {
		if _, ok := fields[key]; ok {
			seen++
		}
	}
	return seen >= 2
}

// envelopeOutput is the command text an envelope carries, when it is complete
// enough to include one. A cut preview simply has none.
func envelopeOutput(fields map[string]json.RawMessage) string {
	for _, key := range []string{"output_delta", "output"} {
		if text := jsonString(fields[key]); strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

// jsonHeadObject reads a JSON object from the head of s and stops at the first
// field it cannot finish. fx's preview ends in the middle of a value, and the
// fields before that cut are still true; a decoder that failed the whole
// object would throw them away.
func jsonHeadObject(s string) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(s)))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			break
		}
		name, ok := key.(string)
		if !ok {
			break
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			break
		}
		fields[name] = value
	}
	return fields, len(fields) > 0
}

func jsonBool(raw json.RawMessage) bool {
	var b bool
	return len(raw) > 0 && json.Unmarshal(raw, &b) == nil && b
}

func jsonString(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}
