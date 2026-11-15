package fxsessions

// L6b: the single address rule shared by the live parser and the command
// adapter. This is a local validation ceiling, not a claim about real fx.

import (
	"strings"
	"testing"
)

func TestValidRefBoundaries(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want bool
	}{
		{"abc", true},
		{"a/b/c/d", true},
		{"a/b/c/d/e", false},
		{"", false},
		{"a//b", false},
		{"../keep", false},
		{"a/#2", false},
		{"a/" + strings.Repeat("x", maxIDLength+1), false},
	} {
		if got := ValidRef(tc.ref); got != tc.want {
			t.Errorf("ValidRef(%q) = %v, want %v", tc.ref, got, tc.want)
		}
	}
	if MaxRefSegments != 4 {
		t.Fatalf("shared segment ceiling changed: %d", MaxRefSegments)
	}
}
