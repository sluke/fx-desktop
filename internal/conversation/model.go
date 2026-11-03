// Package conversation owns the session's business model: the transcript, the
// approval request, the configuration fx published, and the usage the session
// has accumulated. It knows nothing about MyGo, windows or palettes; the app
// maps its values into the UI's presentation types.
package conversation

import (
	"time"

	"fx-desktop/internal/workspace"
)

// Message is one row of the transcript. It carries agent protocol data, never
// terminal escape sequences or colors.
type Message struct {
	ID                        uint64
	Role, Text, Title, Status string
	Done                      bool
	// Started and Ended time a tool call. The transcript shows how long the
	// call took: a row that says only "Running" next to a word like
	// "Completed" tells the reader nothing.
	Started, Ended time.Time
	// Images names the pictures sent with this message, so the history
	// records what was sent and not just the words that came with it.
	Images []string
	// Diff is set on a tool message that changed files. A diff is rendered
	// structurally rather than being flattened back into Text.
	Diff *DiffInfo
	// Command is the shell command a tool call ran. fx's ACP result preview
	// stops inside its own envelope, before the command's output, so the
	// command is the only human content an expanded shell call can show.
	Command string
	// Shell is set on a shell call, with the flags fx reported about its
	// captured result.
	Shell *ShellResult
}

// ShellResult is what fx reported about a shell call's result beyond its row
// state. The ACP preview cuts fx's envelope at 200 bytes, so only these flags
// reach the client; they are the part that changes what the result means.
type ShellResult struct {
	Truncated     bool
	Incomplete    bool
	Indeterminate bool
	Signal        string
}

// DiffInfo is one file's change as a list of classified lines.
type DiffInfo struct {
	Path  string
	Lines []workspace.DiffLine
}

// PermissionOption is one choice an approval request offers. The JSON tags are
// the protocol's, because fx sends them and the client echoes the id back.
type PermissionOption struct {
	ID   string `json:"optionId"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Permission is the approval a running turn is waiting for. It must be chosen
// explicitly; an expired request is never answered.
type Permission struct {
	ID, Title, Detail string
	Options           []PermissionOption
}

// ConfigOption is one session-level setting fx published when the session
// opened. The shell renders whatever fx sends: "model" is not special-cased,
// so the day fx adds an option it appears on its own.
type ConfigOption struct {
	ID, Name, Category, Type, CurrentValue string
	Options                                []ConfigChoice
}

// ConfigChoice is one selectable value: Value is what the protocol takes,
// Name is what fx calls it.
type ConfigChoice struct{ Value, Name string }

// ModeOption is one ACP mode. fx reports the mode twice — once as a config
// option, once as this list — and this list is the richer one because it
// carries the description.
type ModeOption struct{ ID, Name, Description string }

// FindConfig returns the option with this id.
func FindConfig(options []ConfigOption, id string) (ConfigOption, bool) {
	for _, o := range options {
		if o.ID == id {
			return o, true
		}
	}
	return ConfigOption{}, false
}

// ModeConfig is the option fx uses for the ACP mode: the one whose category
// says so. Matching the category first keeps a renamed id working.
func ModeConfig(options []ConfigOption) (ConfigOption, bool) {
	for _, o := range options {
		if o.Category == "mode" {
			return o, true
		}
	}
	return FindConfig(options, "mode")
}

// Status is a session's state without its transcript: what the rail, the agent
// strip and the sidebar read every frame.
type Status struct {
	Ready, Busy, Stopping, ConfigChanging bool
	// Permission says a turn is waiting on an approval; the request itself
	// belongs to the transcript view.
	Permission                              bool
	Steering, Images, Files                 bool
	Queued                                  int
	ContextUsed, ContextTotal, MessageCount int
	InputTokens, OutputTokens               int
	Problem, Model, Mode, SessionID         string
	SessionTitle                            string
}
