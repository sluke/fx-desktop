package read

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// the message is sent without the file and without a refusal.
func TestRegressionR9_ResolveMentionBeforeFullWidthComma(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	control, err := Resolve(ctx, project, "see @main.go, thanks")
	if err != nil || len(control) != 1 {
		t.Fatalf("ASCII control: %v, %v", control, err)
	}
	got, err := Resolve(ctx, project, "看一下 @main.go，谢谢")
	t.Logf("Resolve -> %d mentions, err=%v", len(got), err)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Path != "main.go" {
		t.Errorf("mentions = %+v, want main.go attached", got)
	}
}
