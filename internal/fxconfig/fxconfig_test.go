package fxconfig

import (
	"reflect"
	"testing"
)

func TestSetAcceptsPlainPreferences(t *testing.T) {
	got, err := SetArgs(" fast_mode ", "true")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"config", "set", "fast_mode", "true"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v", got)
	}
	got, err = SetArgs("auto_compact_percent", "40")
	if err != nil || !reflect.DeepEqual(got, []string{"config", "set", "auto_compact_percent", "40"}) {
		t.Fatalf("args=%v err=%v", got, err)
	}
	if _, err := UnsetArgs("status_line.context"); err != nil {
		t.Fatal(err)
	}
}

func TestSetRejectsReservedDeprecatedAndOutOfRange(t *testing.T) {
	for _, key := range []string{"permission", "permission.edit", "provider", "models.gateway", "model", "skill_match_fuzzy", "statusLine"} {
		if _, err := SetArgs(key, "true"); err == nil {
			t.Fatalf("accepted %s", key)
		}
	}
	for _, value := range []string{"9", "81", "no"} {
		if _, err := SetArgs("auto_compact_percent", value); err == nil {
			t.Fatalf("accepted auto_compact_percent=%s", value)
		}
	}
	if _, err := SetArgs("fast_mode", "yes"); err == nil {
		t.Fatal("accepted a non-boolean")
	}
}
