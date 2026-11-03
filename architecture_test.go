package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestCapabilityPackagesDoNotDependOnTheUI pins the dependency direction §3.1
// asks for: the capability packages own rules and data, so they must build
// without MyGo, the app, or the chrome package. A violation here is a design
// regression, not a style question.
func TestCapabilityPackagesDoNotDependOnTheUI(t *testing.T) {
	forbidden := []string{
		"github.com/egoist/mygo",
		"fx-desktop/internal/app",
	}
	capabilities := []string{
		"internal/conversation",
		"internal/history",
		"internal/workspace",
		"internal/management",
	}
	for _, dir := range capabilities {
		for _, imp := range importsOf(t, dir) {
			for _, bad := range forbidden {
				if imp == bad || strings.HasPrefix(imp, bad+"/") {
					t.Errorf("%s imports %s: capability packages must not depend on the UI", dir, imp)
				}
			}
		}
	}
}

// TestAdaptersDoNotDependOnTheUI is the same direction for the outer layer:
// an adapter owns a process, a file format or a store, and must be usable
// without the app or a window.
func TestAdaptersDoNotDependOnTheUI(t *testing.T) {
	adapters := []string{
		"internal/acp",
		"internal/fxcli",
		"internal/fxbin",
		"internal/fxsessions",
		"internal/sessionstats",
		"internal/prefs",
		"internal/projects",
		"internal/configdir",
	}
	for _, dir := range adapters {
		for _, imp := range importsOf(t, dir) {
			if imp == "github.com/egoist/mygo" || strings.HasPrefix(imp, "github.com/egoist/mygo/") {
				t.Errorf("%s imports %s: adapters must not depend on the UI toolkit", dir, imp)
			}
			if imp == "fx-desktop/internal/app" || strings.HasPrefix(imp, "fx-desktop/internal/app/") {
				t.Errorf("%s imports %s: adapters must not depend on the app", dir, imp)
			}
		}
	}
}

// TestPresenterMapsBusinessDataOnly: the presenter turns business snapshots
// into UI models, so it may import chrome and the capability packages but not
// the app, a window, or the toolkit's runtime.
func TestPresenterMapsBusinessDataOnly(t *testing.T) {
	for _, imp := range importsOf(t, "internal/app/presenter") {
		if imp == "fx-desktop/internal/app" || strings.HasPrefix(imp, "fx-desktop/internal/app/") && imp != "fx-desktop/internal/app/chrome" {
			t.Errorf("presenter imports %s: it must stay a pure mapping", imp)
		}
		if imp == "github.com/egoist/mygo" || strings.HasPrefix(imp, "github.com/egoist/mygo/") {
			t.Errorf("presenter imports %s: it must not need the toolkit's runtime", imp)
		}
		if imp == "os/exec" {
			t.Error("presenter runs processes: it is a mapping, not an adapter")
		}
	}
}

// TestChromeDoesNotRunProcessesOrTouchTheDisk pins the other half: chrome
// renders, so it may read the data model but must not reach a process runner
// or a file adapter of its own.
func TestChromeDoesNotRunProcessesOrTouchTheDisk(t *testing.T) {
	forbidden := map[string]string{
		"os/exec":                            "a process runner",
		"fx-desktop/internal/workspace/read": "a workspace file adapter",
	}
	for _, imp := range importsOf(t, "internal/app/chrome") {
		if reason, ok := forbidden[imp]; ok {
			t.Errorf("chrome imports %s (%s): rendering must not do I/O", imp, reason)
		}
	}
}

// TestConversationModelIsUI Free asserts the session model has no presentation
// dependency, so a transcript row cannot smuggle a color or a widget into the
// business layer.
func TestConversationModelIsFreeOfPresentation(t *testing.T) {
	for _, imp := range importsOf(t, "internal/conversation") {
		if strings.Contains(imp, "mygo") || strings.Contains(imp, "chrome") {
			t.Errorf("conversation imports %s", imp)
		}
	}
}

// importsOf parses every non-test Go file in dir and returns its imports.
func importsOf(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var imports []string
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("import path %s: %v", spec.Path.Value, err)
			}
			imports = append(imports, path)
		}
	}
	return imports
}

// TestNoCapabilityReachesTheUIThroughADependency closes the gap the direct
// import checks leave: a capability package that imports another local package
// which itself imports the app would still drag the UI into the business layer.
// It walks the local import graph transitively, not just the direct imports.
func TestNoCapabilityReachesTheUIThroughADependency(t *testing.T) {
	graph := localPackageGraph(t)
	forbidden := func(imp string) bool {
		return imp == "fx-desktop/internal/app" || strings.HasPrefix(imp, "fx-desktop/internal/app/") ||
			imp == "github.com/egoist/mygo" || strings.HasPrefix(imp, "github.com/egoist/mygo/")
	}
	for _, root := range []string{
		"fx-desktop/internal/conversation",
		"fx-desktop/internal/history",
		"fx-desktop/internal/workspace",
		"fx-desktop/internal/management",
	} {
		for dep := range transitiveImports(graph, root) {
			if forbidden(dep) {
				t.Errorf("%s reaches %s through its dependency graph", root, dep)
			}
		}
	}
	// Chrome renders; it must not reach a process runner or the app through a
	// package it happens to import.
	for dep := range transitiveImports(graph, "fx-desktop/internal/app/chrome") {
		switch {
		case dep == "os/exec":
			t.Errorf("chrome reaches os/exec through its dependency graph")
		case dep == "fx-desktop/internal/workspace/read":
			t.Errorf("chrome reaches the workspace file adapter through its dependency graph")
		case dep == "fx-desktop/internal/app":
			t.Errorf("chrome reaches the app through its dependency graph")
		}
	}
}

// localPackageGraph maps each local package to its direct imports, including
// the root package.
func localPackageGraph(t *testing.T) map[string][]string {
	t.Helper()
	graph := map[string][]string{}
	var walk func(dir, pkgPath string)
	walk = func(dir, pkgPath string) {
		imports := importsOf(t, dir)
		graph[pkgPath] = imports
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			child := filepath.Join(dir, entry.Name())
			if !hasGoFiles(child) {
				continue
			}
			walk(child, pkgPath+"/"+entry.Name())
		}
	}
	walk("internal", "fx-desktop/internal")
	// The root package (main) is not under internal/.
	graph["fx-desktop"] = importsOf(t, ".")
	return graph
}

func hasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			return true
		}
	}
	return false
}

// transitiveImports is the closure of a package's local imports, including the
// package itself.
func transitiveImports(graph map[string][]string, root string) map[string]bool {
	seen := map[string]bool{}
	var visit func(pkg string)
	visit = func(pkg string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		for _, dep := range graph[pkg] {
			if _, local := graph[dep]; local {
				visit(dep)
			}
		}
	}
	visit(root)
	delete(seen, root)
	return seen
}
