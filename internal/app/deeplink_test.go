package app

import (
	"path/filepath"
	"testing"
)

func TestProjectFromURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{
			name: "open with path",
			raw:  "fx-desktop://open?path=/tmp/foo",
			want: "/tmp/foo",
			ok:   true,
		},
		{
			name: "no host path query",
			raw:  "fx-desktop://?path=/var/proj",
			want: "/var/proj",
			ok:   true,
		},
		{
			name: "encoded path",
			raw:  "fx-desktop://open?path=%2FUsers%2Fme%2Fcode",
			want: "/Users/me/code",
			ok:   true,
		},
		{
			name: "wrong scheme",
			raw:  "https://fx.sh/try",
			ok:   false,
		},
		{
			name: "missing path",
			raw:  "fx-desktop://open",
			ok:   false,
		},
		{
			name: "unknown host",
			raw:  "fx-desktop://install?path=/tmp",
			ok:   false,
		},
		{
			name: "garbage",
			raw:  "not a url",
			ok:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ProjectFromURL(tc.raw)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v (got %q)", ok, tc.ok, got)
			}
			if !tc.ok {
				return
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestProjectFromArg(t *testing.T) {
	wd := "/work/wd"
	tests := []struct {
		name string
		arg  string
		wd   string
		want string
		ok   bool
	}{
		{name: "empty", arg: "", ok: false},
		{name: "absolute", arg: "/abs/proj", want: "/abs/proj", ok: true},
		{name: "relative", arg: "child", wd: wd, want: filepath.Join(wd, "child"), ok: true},
		{name: "relative dot", arg: ".", wd: wd, want: wd, ok: true},
		{name: "deep link", arg: "fx-desktop://open?path=/dl/proj", want: "/dl/proj", ok: true},
		{name: "deep link no host", arg: "fx-desktop://?path=/dl/other", want: "/dl/other", ok: true},
		{name: "dot clean", arg: "/abs/../proj", want: "/proj", ok: true},
		{name: "flag long", arg: "--model", ok: false},
		{name: "flag short", arg: "-c", ok: false},
		{name: "flag with value", arg: "--model=x", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ProjectFromArg(tc.arg, tc.wd)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
