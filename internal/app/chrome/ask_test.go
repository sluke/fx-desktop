package chrome

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestQuickAskBuildsTheNativeRequest(t *testing.T) {
	h, sh := testHost()
	sh.AskOpen = true
	sh.AskPrompt = "summarize this"
	sh.AskModel = "provider/model"
	sh.AskEffort = "high"
	sh.AskAccess = "full-access"
	sh.AskFast = "on"
	sh.AskUltra = "off"
	sh.AskSystem = "be brief"
	sh.AskProviderOrder = "a,b"
	sh.AskImages = "one.png"
	sh.AskSession = "last"
	sh.AskAddDirs = "/tmp/extra"
	sh.AskContextLimits = "skill_file_bytes=off"
	sh.AskStrict = true
	sh.AskNoAdditionalDirs = true
	var got AskRequest
	h.Actions.AskAdvanced = func(req AskRequest) { got = req }
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 560)
	ts.Frame()
	if err := ts.Click("Run"); err != nil {
		t.Fatal(err)
	}
	if got.Prompt != "summarize this" || got.Model != "provider/model" || got.Effort != "high" ||
		got.Access != "full-access" || got.Fast != "on" || got.Ultra != "off" ||
		got.System != "be brief" || got.ProviderOrder != "a,b" || got.Images != "one.png" ||
		got.Session != "last" || got.AddDirs != "/tmp/extra" || got.ContextLimits != "skill_file_bytes=off" ||
		!got.Strict || !got.NoAdditionalDirs {
		t.Fatalf("quick ask request=%+v", got)
	}
	if !sh.AskBusy {
		t.Fatal("quick ask did not enter its busy state")
	}
	if sh.AskOutput != "" || sh.AskProblem != "" {
		t.Fatalf("quick ask retained stale result: output=%q problem=%q", sh.AskOutput, sh.AskProblem)
	}
}

func TestQuickAskRendersResultAndBusyState(t *testing.T) {
	h, sh := testHost()
	sh.AskOpen = true
	sh.AskPrompt = "hello"
	sh.AskOutput = "The answer"
	calls := 0
	h.Actions.AskAdvanced = func(AskRequest) { calls++ }
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 560)
	ts.Frame()
	mustHave(t, ts, "The answer")
	mustHave(t, ts, "Cancel")
	sh.AskBusy = true
	ts.Frame()
	mustHave(t, ts, "Running fx ask…")
	_ = ts.Click("Run")
	if calls != 0 {
		t.Fatal("busy Run dispatched a second request")
	}
}
