package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"fx-desktop/internal/app/chrome"
)

// Native calibration on this Mac: the close button is 14 DIP tall, and
// default/zero-offset TitleBar.Left are 69/60. Keep its default X=9 while
// centering it in our 52 DIP toolbar. Also cover every window constructor.
func TestWindowsCenterTrafficLightsInToolbar(t *testing.T) {
	p := windowTrafficLights()
	if p == nil || p.X != 9 || p.Y*2+14 != chrome.TitleBarHeight {
		t.Errorf("traffic lights = %+v, want X=9 and center at %d DIP", p, chrome.TitleBarHeight/2)
	}
	count := 0
	for _, file := range []string{"coordinator.go", "session.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			typ, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || typ.Sel.Name != "WindowOptions" {
				return true
			}
			count++
			found := false
			for _, e := range lit.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "TrafficLightPosition" {
					continue
				}
				call, ok := kv.Value.(*ast.CallExpr)
				if ok {
					fn, ok := call.Fun.(*ast.Ident)
					found = ok && fn.Name == "windowTrafficLights"
				}
			}
			if !found {
				t.Errorf("%s window constructor omits shared traffic light positioning", file)
			}
			return true
		})
	}
	if count != 5 {
		t.Fatalf("checked %d window constructors, want 5", count)
	}
}
