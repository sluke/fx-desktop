package fxcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fx-desktop/internal/cmdlimit"
)

// BenchmarkQueryOutputBudget is the r7 Q7 harness, upgraded for the budget:
// the 16 MiB sample is kept (as required) but is now expected to be refused
// instead of decoded. The 64 KiB and 1 MiB fixtures are unchanged from the
// pre-fix baseline, so their B/op and allocs/op remain comparable.
func BenchmarkQueryOutputBudget(b *testing.B) {
	for _, n := range []int{64 << 10, 1 << 20, 16 << 20} {
		b.Run(fmt.Sprintf("%dKiB", n>>10), func(b *testing.B) {
			dir := b.TempDir()
			exe := filepath.Join(dir, "fx")
			script := "#!/bin/sh\nexec /bin/cat \"$0.json\"\n"
			if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
				b.Fatal(err)
			}
			body := `{"model":"fixture","unused":"` + strings.Repeat("x", n) + `"}`
			if err := os.WriteFile(exe+".json", []byte(body), 0o600); err != nil {
				b.Fatal(err)
			}
			overBudget := n > maxQueryBytes
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ctx, cancel := WithTimeout(context.Background())
				var dst Status
				err := Run(ctx, exe, dir, &dst, "status")
				cancel()
				if overBudget {
					if !errors.Is(err, cmdlimit.ErrTooLarge) || dst.Model != "" {
						b.Fatalf("oversized query err=%v model=%q", err, dst.Model)
					}
					continue
				}
				if err != nil || dst.Model != "fixture" {
					b.Fatalf("within-budget query err=%v model=%q", err, dst.Model)
				}
			}
		})
	}
}
