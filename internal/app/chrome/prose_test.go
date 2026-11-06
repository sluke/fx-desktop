package chrome

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The chrome explains itself with layout: a button shows what can be
// attached, a highlighted row shows what Enter would take, and a section
// label shows where a group of controls belongs. A sentence that teaches an
// interaction is a sign the layout failed. These fragments are the teaching
// copy that used to sit on screen permanently.
var bannedHintFragments = []string{
	"fx works in your project", "Enter sends", "Shift + Enter",
	"@ attaches a file", "drop or paste an image", "click the console",
	"↑↓", "esc dismiss", "↵ insert", "enter run",
	"opens fx login", "Select a file to read", "Sessions start once",
	"Runs fx ask", "Ask a question, explore an idea",
	"These settings depend", "Interactive permission prompts",
	"Load more to continue searching",
}

// explanatoryProse recognises a说明书-style sentence: long, multi-word, and
// punctuated as a sentence. Short labels, statuses, paths and the fx CLI's
// own error/help text are not prose the chrome chose to write.
func explanatoryProse(s string) bool {
	return len(s) >= 45 && strings.Count(s, " ") >= 6 && strings.HasSuffix(s, ".")
}

type auditView struct {
	name   string
	render func(c *ui.Context)
}

func auditToolsView(page string) auditView {
	return auditView{"tools/" + page, func(c *ui.Context) {
		h, sh := testHost()
		h.Conversation = true
		h.Actions.ManageWorkspace = noop
		sh.Tools = ManagementState{Open: true, Page: page}
		Frame(c, h)
	}}
}

func chromeAuditViews() []auditView {
	views := []auditView{
		{"conversation", func(c *ui.Context) {
			state := ConversationState{}
			Conversation(c, &state, ConversationHost{Ready: true, CanAttach: true, CanMention: true})
		}},
		{"conversation/busy-with-image", func(c *ui.Context) {
			state := ConversationState{Attachments: []Attachment{{ID: 1, Name: "shot.png"}}}
			Conversation(c, &state, ConversationHost{Ready: true, Busy: true, CanAttach: true, CanMention: true})
		}},
		{"mention-picker", func(c *ui.Context) {
			state := ConversationState{Draft: "@", MentionOpen: true}
			h := ConversationHost{Ready: true, CanMention: true, MentionFiles: []MentionFile{{Name: "main.go", Path: "main.go"}}}
			Conversation(c, &state, h)
		}},
		{"palette", func(c *ui.Context) {
			h, sh := testHost()
			sh.PaletteOpen = true
			Frame(c, h)
		}},
		{"quick-ask", func(c *ui.Context) {
			h, sh := testHost()
			h.Actions.Ask = func(string) {}
			sh.AskOpen = true
			Frame(c, h)
		}},
		{"auth-card", func(c *ui.Context) {
			AuthNoticeCard(c, AuthNotice{Help: "Run fx login."}, noop)
		}},
		{"advanced-settings", func(c *ui.Context) {
			state := ConversationState{AdvancedOpen: true}
			advancedSettings(c, &state, ConversationHost{Ready: true}, palette(c.Theme()))
		}},
		{"console", func(c *ui.Context) {
			h, sh := consoleTestHost(ConsoleData{Runs: []ConsoleRun{{ID: 1, Title: "fx doctor"}},
				Body: func(c *ui.Context) { ui.Text(c, "CONSOLE-BODY") }})
			consoleDrawer(c, h, sh, palette(c.Theme()))
		}},
		{"review-empty", func(c *ui.Context) {
			h, sh := reviewTestHost(ReviewData{Open: true, Workspace: WorkspaceRepo,
				Changes: []Change{{Path: "a.go", Kind: "M"}}})
			reviewDrawer(c, h, sh, palette(c.Theme()), reviewWide)
		}},
		{"history", func(c *ui.Context) {
			h, sh := testHost()
			h.Actions.OpenHistory = noop
			sh.History.Open = true
			Frame(c, h)
		}},
		{"welcome", func(c *ui.Context) {
			h, _ := testHost()
			h.Data = SidebarData{Welcome: true, OnInstall: func() {}}
			Frame(c, h)
		}},
		{"start-screen", func(c *ui.Context) {
			h, _ := testHost()
			h.CenterOnly = true
			Frame(c, h)
		}},
	}
	for _, page := range []string{"Account", "MCP", "Folders", "Details", "Sessions", "Launch", "Models", "Rules", "Skills", "Slack", "GitHub"} {
		views = append(views, auditToolsView(page))
	}
	return views
}

// TestChromeExplainsByLayoutNotProse is the budget: no view may fall back on
// a permanent teaching sentence or a keyboard legend to explain itself.
func TestChromeExplainsByLayoutNotProse(t *testing.T) {
	for _, view := range chromeAuditViews() {
		t.Run(view.name, func(t *testing.T) {
			ts := ui.NewTester(view.render, 1000, 700)
			ts.Frame()
			for _, text := range ts.Texts() {
				for _, fragment := range bannedHintFragments {
					if strings.Contains(text, fragment) {
						t.Errorf("teaching copy %q is on screen: %q", fragment, text)
					}
				}
				if explanatoryProse(text) {
					t.Errorf("explanatory prose is on screen: %q", text)
				}
			}
		})
	}
}

// Deleting the prose only works if the same information is visible in the
// controls themselves, and if those controls actually do what they show.
func TestComposerAffordancesOpenWhatTheyShow(t *testing.T) {
	var opened, picked int
	state := ConversationState{Draft: "look at this"}
	h := ConversationHost{
		Ready: true, CanAttach: true, CanMention: true,
		MentionFiles: []MentionFile{{Name: "main.go", Path: "main.go"}},
		OpenMentions: func() { opened++ },
		PickImage:    func() { picked++ },
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 1000, 620)
	ts.Frame()
	if err := ts.Click("Attach image"); err != nil {
		t.Fatal(err)
	}
	if picked != 1 {
		t.Fatalf("the image button opened the chooser %d times, want once", picked)
	}
	if err := ts.Click("Mention a file"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	if state.Draft != "look at this @" {
		t.Fatalf("draft = %q, want an @ under the caret", state.Draft)
	}
	if !state.MentionOpen || opened != 1 {
		t.Fatalf("the @ button did not open the picker once: open=%v asked=%d", state.MentionOpen, opened)
	}
	if !ts.HasText("main.go") {
		t.Fatalf("the picker shows no project files: %v", ts.Texts())
	}
	if _, ok := ts.Find("Insert selected file"); !ok {
		t.Fatal("the picker's highlighted row has no return mark")
	}
}

func TestChromeUsesVisibleAffordancesInsteadOfHints(t *testing.T) {
	state := ConversationState{}
	ts := ui.NewTester(func(c *ui.Context) {
		Conversation(c, &state, ConversationHost{Ready: true, CanAttach: true, CanMention: true,
			PickImage: func() {}})
	}, 1000, 620)
	ts.Frame()
	for _, label := range []string{"Attach image", "Mention a file"} {
		if _, ok := ts.Find(label); !ok {
			t.Errorf("composer affordance %q is missing", label)
		}
	}

	picker := ConversationState{Draft: "@", MentionOpen: true}
	pickerHost := ConversationHost{Ready: true, CanMention: true, MentionFiles: []MentionFile{{Name: "main.go", Path: "main.go"}}}
	ts = ui.NewTester(func(c *ui.Context) { Conversation(c, &picker, pickerHost) }, 1000, 620)
	ts.Frame()
	if _, ok := ts.Find("Insert selected file"); !ok {
		t.Error("the highlighted file has no return mark")
	}

	h, sh := testHost()
	sh.PaletteOpen = true
	sh.PaletteHighlight = 0
	ts = ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1000, 700)
	ts.Frame()
	if _, ok := ts.Find("Run selected command"); !ok {
		t.Error("the highlighted command has no return mark")
	}

	for _, page := range []string{"MCP", "Folders", "Launch", "Rules"} {
		h, sh := testHost()
		h.Conversation = true
		h.Actions.ManageWorkspace = noop
		sh.Tools = ManagementState{Open: true, Page: page}
		ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1000, 700)
		ts.Frame()
		if !ts.HasText("Applies to new sessions") {
			t.Errorf("tools/%s has no badge for a deferred setting", page)
		}
	}
}
