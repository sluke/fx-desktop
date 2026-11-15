package app

import (
	"reflect"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
)

func TestAskArgsBuildTheOneShotRequest(t *testing.T) {
	got, err := askArgs(chrome.AskRequest{
		Prompt: "summarize", Model: "openai/gpt", Effort: "high", Access: "full-access",
		Fast: "on", Ultra: "off", System: "be brief", ProviderOrder: "a,b", Strict: true,
		Images: "one.png\r\n two.png\n", Session: "last", NoSave: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ask", "--full-access", "--model", "openai/gpt", "--effort", "high", "--fast", "--no-ultrafast",
		"--provider-order", "a,b", "--provider-strict", "--resume", "last",
		"--image", "one.png", "--image", "two.png", "--system", "be brief", "--json", "summarize",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ask args=%v, want %v", got, want)
	}
}

func TestAskArgsProtectDashPromptsAndRejectInvalidCombinations(t *testing.T) {
	got, err := askArgs(chrome.AskRequest{Prompt: "--help", Access: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ask", "--auto", "--json", "--", "--help"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dash prompt args=%v, want %v", got, want)
	}
	for _, req := range []chrome.AskRequest{
		{Prompt: "x", Access: "prompt"},
		{Prompt: "x", Access: "auto", Fast: "sometimes"},
		{Prompt: "x", Access: "auto", Ultra: "maybe"},
		{Prompt: "x", Access: "auto", Session: "last", NoSave: true},
		{Prompt: "x", Access: "auto", Model: "--help"},
		{Prompt: "x", Access: "auto", ProviderOrder: "--all"},
		{Prompt: "x", Access: "auto", Strict: true},
	} {
		if _, err := askArgs(req); err == nil {
			t.Fatalf("accepted invalid ask request: %+v", req)
		}
	}
}

func TestAskArgsPutGlobalContextAndDirectoryFlagsBeforeAsk(t *testing.T) {
	extra := t.TempDir()
	got, err := askArgs(chrome.AskRequest{
		Prompt: "hello", Access: "auto", ContextLimits: "project_instructions_total_bytes=1000",
		AddDirs: extra,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--context-limit", "project_instructions_total_bytes=1000",
		"--add-dir", extra, "ask", "--auto", "--json", "hello",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ask args=%v, want %v", got, want)
	}
	if _, err := askArgs(chrome.AskRequest{Prompt: "hello", AddDirs: extra, NoAdditionalDirs: true}); err == nil {
		t.Fatal("accepted add dirs together with no-additional-dirs")
	}
}

func TestAskConfigLastOverrideWinsAndCannotReplaceDedicatedFields(t *testing.T) {
	got, err := askArgs(chrome.AskRequest{
		Prompt: "hello", Access: "auto", Provider: "codex", Timeout: "30",
		Config: "theme=dark\ntheme=light\nfast_mode=true\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ask", "--auto", "--provider", "codex", "--timeout", "30", "--config", "theme=light", "--config", "fast_mode=true", "--json", "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ask args=%v, want %v", got, want)
	}
	for _, config := range []string{"provider=grok", "permission.edit=allow", "model=x"} {
		if _, err := askArgs(chrome.AskRequest{Prompt: "hello", Config: config}); err == nil {
			t.Fatalf("accepted config override %s", config)
		}
	}
	wait, err := askWait(chrome.AskRequest{Timeout: "700"})
	if err != nil || wait < 700*time.Second {
		t.Fatalf("wait=%v err=%v", wait, err)
	}
}
