package management

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A management action is a typed request: the form value either names
// something fx accepts or the action is refused with a reason. Nothing is
// pasted into a shell string.
func TestCommandValidatesItsForm(t *testing.T) {
	if _, _, err := Command("login", Form{Provider: "--no-sandbox"}); err == nil {
		t.Fatal("a flag-shaped provider must be refused")
	}
	if _, _, err := Command("login", Form{Provider: "openai"}); err == nil {
		t.Fatal("a provider fx does not support must be refused")
	}
	if args, _, err := Command("login", Form{Provider: "codex"}); err != nil || !slices.Equal(args, []string{"login", "codex"}) {
		t.Fatalf("login args = %v, %v", args, err)
	}
	if _, _, err := Command("mcp-show", Form{Name: "bad\nname"}); err == nil {
		t.Fatal("a name with a newline must be refused")
	}
	if _, _, err := Command("upgrade-channel", Form{Channel: "nightly"}); err == nil {
		t.Fatal("an unknown channel must be refused")
	}
	if _, _, err := Command("unknown-action", Form{}); err == nil {
		t.Fatal("an unknown action must be refused")
	}
}

// An additional directory has to exist and be absolute: fx would fail later,
// with a message far from the form that caused it.
func TestWorkspaceAddRequiresAnAbsoluteExistingDirectory(t *testing.T) {
	if _, _, err := Command("workspace-add", Form{Directory: "relative/dir"}); err == nil {
		t.Fatal("a relative directory must be refused")
	}
	dir := t.TempDir()
	args, confirm, err := Command("workspace-add", Form{Directory: dir})
	if err != nil || confirm || !slices.Equal(args, []string{"workspace", "add", dir}) {
		t.Fatalf("workspace add = %v, %v, %v", args, confirm, err)
	}
	if _, confirm, err := Command("workspace-remove", Form{Directory: dir}); err != nil || !confirm {
		t.Fatalf("removing an additional workspace must confirm: %v, %v", confirm, err)
	}
}

// The launch request puts fx global flags first: fx rejects them after the
// subcommand, and the ACP argv is built from the rest.
func TestLaunchPutsGlobalFlagsFirst(t *testing.T) {
	dir := t.TempDir()
	args, confirm, err := Command("launch", Form{
		ContextLimits:    "default=120000",
		AddDirs:          dir,
		ProviderOverride: "codex",
		Model:            "test/model",
		Effort:           "high",
		Fast:             "on",
		Ultra:            "off",
	})
	if err != nil || !confirm {
		t.Fatalf("launch = %v, %v", args, err)
	}
	want := []string{"--context-limit", "default=120000", "--add-dir", dir, "--provider", "codex", "--model", "test/model", "--effort", "high", "--fast", "--no-ultrafast"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if _, _, err := Command("launch", Form{NoAdditionalDirs: true, AddDirs: dir}); err == nil {
		t.Fatal("ignore-saved and additional directories are exclusive")
	}
}

// A context that starts with a dash must not become an fx flag.
func TestGitHubSeparatesADashContext(t *testing.T) {
	args, confirm, err := GitHub(GitHubForm{Kind: "pr", Context: "--help", Auto: true, Create: true})
	if err != nil || !confirm {
		t.Fatalf("github = %v, %v", args, err)
	}
	want := []string{"pr", "--auto", "--create", "--", "--help"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if _, _, err := GitHub(GitHubForm{Kind: "commit"}); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
}

func TestGlobalArgsValidatesContextLimits(t *testing.T) {
	if _, err := GlobalArgs("Default=10", "", ""); err == nil {
		t.Fatal("an uppercase limit name must be refused")
	}
	if _, err := GlobalArgs("default=ten", "", ""); err == nil {
		t.Fatal("a non-numeric limit must be refused")
	}
	if _, err := GlobalArgs("default=off", "", ""); err != nil {
		t.Fatalf("off is a valid value: %v", err)
	}
	project := t.TempDir()
	if _, err := GlobalArgs("", project+"\n"+project, project); err == nil {
		t.Fatal("the primary workspace cannot also be an additional directory")
	}
	missing := filepath.Join(project, "gone")
	if _, err := GlobalArgs("", missing, project); err == nil {
		t.Fatal("an additional directory must exist")
	}
	if _, err := GlobalArgs("", "relative", ""); err == nil {
		t.Fatal("an additional directory must be absolute")
	}
}

// A mutation that changes state waits for confirmation; a read does not.
func TestConfirmFlagFollowsTheAction(t *testing.T) {
	for _, action := range []string{"mcp-approve-all", "slack-install", "workspace-clear", "session-recover"} {
		if _, confirm, err := Command(action, Form{SessionID: "abc", Directory: os.TempDir()}); err != nil || !confirm {
			t.Errorf("%s: confirm = %v, err = %v", action, confirm, err)
		}
	}
	for _, action := range []string{"mcp-list", "models", "doctor", "credits", "workspace-list"} {
		if _, confirm, err := Command(action, Form{}); err != nil || confirm {
			t.Errorf("%s: confirm = %v, err = %v", action, confirm, err)
		}
	}
}

// A mutation that can take minutes gets a longer timeout than a read.
func TestJobTimeoutFollowsTheAction(t *testing.T) {
	if got := JobTimeout("upgrade-channel"); got != 10*time.Minute {
		t.Fatalf("upgrade timeout = %v", got)
	}
	if got := JobTimeout("mcp-list"); got != time.Minute {
		t.Fatalf("read timeout = %v", got)
	}
}

// A malformed MCP environment line is refused rather than passed on.
func TestMCPPairsRequireKeyValue(t *testing.T) {
	args, _, err := Command("mcp-add", Form{Name: "srv", Command: "npx", Environment: "TOKEN=abc\r\n\r\nSECOND=2"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--env TOKEN=abc") || !strings.Contains(joined, "--env SECOND=2") {
		t.Fatalf("args = %v", args)
	}
	if _, _, err := Command("mcp-add", Form{Name: "srv", Command: "npx", Environment: "no-equals"}); err == nil {
		t.Fatal("a line without K=V must be refused")
	}
}
