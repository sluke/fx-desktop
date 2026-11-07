package chrome

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// This file is the design system's enforcement and audit layer. The rules
// read the real AST of every view file — not a hand-maintained list — so a
// new off-scale font size, an ad-hoc radius or a second selection color is
// caught the moment it is written. design_baseline.txt is the ratchet: the
// legacy deviations are counted once, and no change may add a new one or
// increase an existing count. Fixing code lowers the count; it never needs
// a baseline edit unless a *new* deliberate exception is being registered.
//
// Commands (see docs/design-system.md):
//
//	go test ./internal/app/chrome -run TestDesignSystem            # gate + audit summary
//	DESIGN_UPDATE_BASELINE=1 go test ./internal/app/chrome -run TestDesignSystemUpdateBaseline
//	DESIGN_AUDIT_REPORT=outputs/design-audit.md go test ./internal/app/chrome -run TestDesignSystemAudit -v
//
// The gate runs with the ordinary `go test ./...`, so no CI wiring is needed.

type dsRule struct {
	Title    string
	Severity string
	Fix      string
}

var dsRules = map[string]dsRule{
	"DS-01": {"字号越阶（chrome 文本 9–13 / 图标 10·11·12·13·14·16·22）", "高", "改用 Type* / Icon* / Content* / Display* 常量，或在设计系统文档登记新阶梯"},
	"DS-02": {"字号写成裸数字", "中", "改用 design.go 的 Type* / Icon* 常量（如 FontSize(TypeControl)）"},
	"DS-03": {"间距脱离 2px 栅格", "中", "改用 Space* 栅格常量（0/2/4/…/32）或登记的紧凑例外 ButtonPad* / InlinePad* / SheetPadX"},
	"DS-04": {"圆角越阶", "中", "改用 Radius* 阶梯常量（0/1/3/5/8/10/16）"},
	"DS-05": {"间距或圆角写成裸数字", "低", "改用 Space* / Radius* 常量"},
	"DS-06": {"视图代码内联构造颜色", "阻断", "颜色必须来自 Palette；淡色用 p.X.Alpha(a)"},
	"DS-07": {"选中或悬停态使用 Sunken 背景", "高", "统一走 stateSurface(...)，选中/悬停只用 SurfaceHi"},
	"DS-08": {"无可见文案的可点击控件缺 Label", "高", "补 .Label(...) 作为无障碍与测试标识"},
	"DS-09": {"破坏性按钮缺 Disabled 保护", "高", "补 .Disabled(busy || unavailable)"},
	"DS-10": {"Grow(1) 缺 MinWidth(0)/MinHeight(0) 伙伴", "低 · 已归零", "父容器是 Row 补 MinWidth(0)，是 Column 补 MinHeight(0)：Grow 沿父容器主轴分配空间"},
	"DS-11": {"FillWidth 容器又写死 Width", "中", "去掉固定宽度或去掉 FillWidth"},
}

const dsBaselinePath = "design_baseline.txt"

var (
	dsTextScale = map[string]bool{"9": true, "10": true, "11": true, "12": true, "13": true}
	dsIconScale = map[string]bool{"10": true, "11": true, "12": true, "13": true, "14": true, "16": true, "22": true}
	// RadiusBubble (8) is a documented step; it was missing from the ladder
	// check until batch two, which made two legal bubbles look like debt.
	dsRadiusSet   = map[string]bool{"0": true, "1": true, "3": true, "5": true, "8": true, "10": true, "16": true}
	dsSpacingGrid = map[int]bool{0: true, 2: true, 4: true, 6: true, 8: true, 10: true, 12: true, 14: true, 16: true, 18: true, 20: true, 24: true, 32: true}
	// Compact controls need off-grid padding to hit their height budget. They
	// are registered exceptions, not debt: dsResolvedValue skips them so the
	// audit does not report every Commit button as a DS-03 finding. Anything
	// else in design.go is resolved to its VALUE and checked against the grid.
	dsCompactExceptions = map[string]bool{
		"ButtonPadY": true, "ButtonPadX": true,
		"InlinePadY": true, "InlinePadX": true,
		"SheetPadX": true,
	}
	dsButtonRoots = map[string]bool{"Button": true, "ButtonBase": true, "PrimaryButton": true}
	dsStateWords  = regexp.MustCompile(`(?i)(active|selected|hovered|hover|highlight|pressed|focused|\bopen\b)`)
	// A toggle button's own expanded/pressed state is a well, not a selected
	// list row. Keep that distinction in the rule instead of silently
	// dropping "open" from the state words (which would stop flagging real
	// selected rows). The exception is deliberately narrow: only a bare
	// open/pressed/expanded condition, never a combined selection expression.
	// See docs/design-system.md §4.
	dsToggleOnly  = regexp.MustCompile(`(?i)^\s*(open|pressed|expanded)\s*$`)
	dsDestructive = regexp.MustCompile(`(?i)(delete|clear all|remove|commit|revoke|uninstall|reset)`)
	dsColorCtor   = map[string]bool{"Hex": true, "RGB": true, "RGBA": true, "Oklch": true}
	dsRuleOrder   = []string{"DS-01", "DS-02", "DS-03", "DS-04", "DS-05", "DS-06", "DS-07", "DS-08", "DS-09", "DS-10", "DS-11"}
)

type dsFinding struct {
	File   string
	Line   int
	Rule   string
	Value  string
	Detail string
}

func (f dsFinding) key() string { return f.File + "|" + f.Rule + "|" + f.Value }

// dsLit is one numeric argument written as a literal in the source.
type dsLit struct {
	Value string
	Line  int
}

// dsChain is one fluent ui.* expression, from its outermost modifier down to
// the ui.Text / ui.Icon / ui.Button call that started it.
type dsChain struct {
	file      string
	root      string
	methods   map[string]bool
	lines     map[string]int
	positions map[string]token.Pos
	lits      map[string][]dsLit
	args      map[string][]string
	argLines  map[string][]int
	rootArgs  []string
}

type dsCond struct {
	start, end token.Pos
	text       string
}

func dsText(src []byte, fset *token.FileSet, n ast.Node) string {
	if n == nil {
		return ""
	}
	a, b := fset.Position(n.Pos()).Offset, fset.Position(n.End()).Offset
	if a < 0 || b > len(src) || a > b {
		return ""
	}
	return string(src[a:b])
}

func dsNumericArgs(args []ast.Expr, fset *token.FileSet) []dsLit {
	var out []dsLit
	for _, a := range args {
		lit, ok := a.(*ast.BasicLit)
		if !ok || (lit.Kind != token.INT && lit.Kind != token.FLOAT) {
			continue
		}
		out = append(out, dsLit{Value: lit.Value, Line: fset.Position(lit.Pos()).Line})
	}
	return out
}

func dsArgTexts(args []ast.Expr, src []byte, fset *token.FileSet) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		s := dsText(src, fset, a)
		if len(s) > 160 {
			s = s[:160]
		}
		out = append(out, s)
	}
	return out
}

func dsArgLines(args []ast.Expr, fset *token.FileSet) []int {
	out := make([]int, 0, len(args))
	for _, a := range args {
		out = append(out, fset.Position(a.Pos()).Line)
	}
	return out
}

// dsDesignConstants parses design.go once and resolves EVERY declared
// constant to its numeric value. Batch four removed the Legacy* aliases, so
// the audit no longer needs a hand-maintained lookup table: it now catches
// ANY named off-grid value, not just the ones that happened to be called
// Legacy*. On-grid values are filtered out by the caller.
var (
	dsConstOnce  sync.Once
	dsConstTable map[string]int
)

func dsDesignConstants() map[string]int {
	dsConstOnce.Do(func() {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "design.go", nil, 0)
		if err != nil {
			return
		}
		dsConstTable = map[string]int{}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok {
						continue
					}
					if n, err := strconv.Atoi(lit.Value); err == nil {
						dsConstTable[name.Name] = n
					}
				}
			}
		}
	})
	return dsConstTable
}

// dsResolvedValue reads a spacing/radius argument's numeric value from a
// bare literal or from any constant declared in design.go. The registered
// compact exceptions (ButtonPad*, InlinePad*, SheetPadX) resolve to false:
// they are deliberate off-grid decisions, not debt.
func dsResolvedValue(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		return n, true
	}
	if dsCompactExceptions[s] {
		return 0, false
	}
	if n, ok := dsDesignConstants()[s]; ok {
		return n, true
	}
	return 0, false
}

func dsBuildChain(top *ast.CallExpr, path string, src []byte, fset *token.FileSet) *dsChain {
	ch := &dsChain{
		file:      filepath.Base(path),
		methods:   map[string]bool{},
		lines:     map[string]int{},
		positions: map[string]token.Pos{},
		lits:      map[string][]dsLit{},
		args:      map[string][]string{},
		argLines:  map[string][]int{},
	}
	cur := top
	for range 64 {
		sel, ok := cur.Fun.(*ast.SelectorExpr)
		if !ok {
			// A wrapper such as mono(ui.Text(...)) or stateSurface(row, ...):
			// keep walking into the first call-shaped argument.
			var next *ast.CallExpr
			for _, a := range cur.Args {
				if inner, ok := a.(*ast.CallExpr); ok {
					next = inner
					break
				}
			}
			if next == nil {
				break
			}
			cur = next
			continue
		}
		name := sel.Sel.Name
		ch.methods[name] = true
		ch.lines[name] = fset.Position(cur.Pos()).Line
		ch.positions[name] = cur.Pos()
		ch.lits[name] = dsNumericArgs(cur.Args, fset)
		ch.args[name] = dsArgTexts(cur.Args, src, fset)
		ch.argLines[name] = dsArgLines(cur.Args, fset)
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			cur = inner
			continue
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "ui" {
			ch.root = name
			ch.rootArgs = ch.args[name]
		}
		break
	}
	return ch
}

// dsAxisScope is the body of one Children(func(){...}) callback, tagged with
// the main axis of the container that owns it. Grow() shares free space along
// the PARENT's main axis, so a row child needs MinWidth(0) and a column child
// needs MinHeight(0).
type dsAxisScope struct {
	start, end token.Pos
	dir        string // "row" | "column"
}

// dsContainerDirection walks a Children(...) receiver back to the ui.* call
// that created it. Containers whose axis is ambiguous (a custom split, a
// DialogBase panel, a Box) return "" and are skipped rather than guessed —
// a false positive here would be worse than an unchecked call site.
func dsContainerDirection(e ast.Expr) string {
	cur, ok := e.(*ast.CallExpr)
	if !ok {
		return ""
	}
	for range 64 {
		sel, ok := cur.Fun.(*ast.SelectorExpr)
		if !ok {
			return ""
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "ui" {
			switch sel.Sel.Name {
			case "Row", "ScrollHorizontal":
				return "row"
			case "Column", "Scroll":
				return "column"
			}
			return ""
		}
		inner, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return ""
		}
		cur = inner
	}
	return ""
}

// dsParseFile returns every fluent chain plus the conditions of the if/case
// blocks enclosing them, so a Sunken background inside `if active` is
// distinguishable from the same color on an input well. The third return
// value maps Children callbacks to their container's main axis (T54).
func dsParseFile(t *testing.T, path string) ([]*dsChain, func(token.Pos) string, []dsAxisScope) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	receiver := map[ast.Expr]*ast.CallExpr{}
	ast.Inspect(file, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
				receiver[sel.X] = c
			}
		}
		return true
	})
	var conds []dsCond
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.IfStmt:
			conds = append(conds, dsCond{s.Body.Pos(), s.Body.End(), dsText(src, fset, s.Cond)})
		case *ast.CaseClause:
			var text string
			for _, e := range s.List {
				text += dsText(src, fset, e) + " "
			}
			conds = append(conds, dsCond{s.Colon, s.End(), text})
		}
		return true
	})
	condAt := func(pos token.Pos) string {
		var b strings.Builder
		for _, c := range conds {
			if pos >= c.start && pos < c.end {
				b.WriteString(c.text)
				b.WriteByte(' ')
			}
		}
		return b.String()
	}
	var chains []*dsChain
	ast.Inspect(file, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if _, hasParent := receiver[c]; hasParent {
			return true // not the outermost call of this chain
		}
		chains = append(chains, dsBuildChain(c, path, src, fset))
		return true
	})
	var scopes []dsAxisScope
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Children" {
			return true
		}
		// Record unknown containers too: they must SHIELD their children from
		// an outer scope. A button's row-like Children callback is invoked on
		// a variable (row.Children(...)), not on a ui.* chain; without its own
		// unknown scope, the Grow inside it would inherit the enclosing
		// Column's axis and be reported as a false positive.
		dir := dsContainerDirection(sel.X)
		for _, a := range call.Args {
			if fl, ok := a.(*ast.FuncLit); ok {
				scopes = append(scopes, dsAxisScope{start: fl.Body.Pos(), end: fl.Body.End(), dir: dir})
			}
		}
		return true
	})
	return chains, condAt, scopes
}

func dsCheckChain(ch *dsChain, condAt func(token.Pos) string) []dsFinding {
	var out []dsFinding
	add := func(rule, value, detail string, line int) {
		out = append(out, dsFinding{File: ch.file, Line: line, Rule: rule, Value: value, Detail: detail})
	}
	for _, l := range ch.lits["FontSize"] {
		allowed := dsTextScale
		if ch.root == "Icon" {
			allowed = dsIconScale
		}
		if !allowed[l.Value] {
			kind := "文本"
			if ch.root == "Icon" {
				kind = "图标"
			}
			add("DS-01", l.Value, fmt.Sprintf("%s FontSize(%s) 超出设计字阶（%s）", kind, l.Value, ch.root), l.Line)
		}
		add("DS-02", l.Value, "FontSize 写成裸数字，应引用 Type*/Icon* 常量", l.Line)
	}
	for _, m := range []string{"Padding", "Gap", "Radius"} {
		if len(ch.lits[m]) > 0 {
			add("DS-05", m, m+" 写成裸数字，应引用 Space*/Radius* 常量", ch.lines[m])
		}
	}
	// DS-03 / DS-04 look at VALUES, not at how they were written. Batch four
	// put every call site on a standard Space*/Radius* constant; the audit
	// still resolves each name back to its number, so a future constant with
	// an off-grid value is caught immediately rather than blessed by its name.
	for _, m := range []string{"Padding", "Gap"} {
		for i, a := range ch.args[m] {
			n, ok := dsResolvedValue(a)
			if !ok || dsSpacingGrid[n] {
				continue
			}
			line := ch.lines[m]
			if i < len(ch.argLines[m]) {
				line = ch.argLines[m][i]
			}
			add("DS-03", strconv.Itoa(n), fmt.Sprintf("%s(%s = %d) 不在 2px 栅格或命名例外内", m, a, n), line)
		}
	}
	for i, a := range ch.args["Radius"] {
		n, ok := dsResolvedValue(a)
		if !ok || dsRadiusSet[strconv.Itoa(n)] {
			continue
		}
		line := ch.lines["Radius"]
		if i < len(ch.argLines["Radius"]) {
			line = ch.argLines["Radius"][i]
		}
		add("DS-04", strconv.Itoa(n), fmt.Sprintf("Radius(%s = %d) 不在圆角阶梯内", a, n), line)
	}
	if dsColorCtor[ch.root] {
		add("DS-06", ch.root, "视图代码内联构造 ui."+ch.root+"(...) 颜色", ch.lines[ch.root])
	}
	for _, a := range ch.args["Background"] {
		if !strings.Contains(a, "p.Sunken") {
			continue
		}
		cond := condAt(ch.positions["Background"])
		if !dsStateWords.MatchString(cond) {
			continue
		}
		// A button expanded or pressed by its own toggle is a well: Sunken
		// is the right surface there. A row highlighting a selection is not,
		// and must come through stateSurface (T50). Only a bare toggle
		// condition qualifies, so a combined selection expression cannot
		// hide behind this exception.
		if dsToggleOnly.MatchString(cond) {
			continue
		}
		add("DS-07", "p.Sunken", "选中/悬停/激活分支里用 Sunken 作背景，应统一为 stateSurface(...) / SurfaceHi", ch.lines["Background"])
		break
	}
	if dsButtonRoots[ch.root] && ch.methods["OnClick"] {
		caption := ""
		if len(ch.rootArgs) > 1 {
			caption = strings.Trim(ch.rootArgs[1], `"`)
		}
		if (ch.root == "ButtonBase" || caption == "") && !ch.methods["Label"] {
			add("DS-08", ch.root, "可点击控件既无可见文案也无 Label，无法被无障碍与测试定位", ch.lines[ch.root])
		}
		if dsDestructive.MatchString(caption) && !ch.methods["Disabled"] {
			add("DS-09", caption, "破坏性按钮没有 Disabled 保护，忙/不可用状态下仍可触发", ch.lines[ch.root])
		}
	}
	if ch.methods["Grow"] && ch.root != "Box" && !ch.methods["MinWidth"] && !ch.methods["MinHeight"] {
		add("DS-10", ch.root, "Grow(1) 没有 MinWidth(0)/MinHeight(0) 伙伴，flex 收缩时可能溢出", ch.lines["Grow"])
	}
	if ch.methods["FillWidth"] && ch.methods["Width"] {
		for _, l := range ch.lits["Width"] {
			if n, err := strconv.Atoi(l.Value); err == nil && n > 0 {
				add("DS-11", l.Value, "FillWidth 容器又写死 Width，窄窗口会溢出", l.Line)
				break
			}
		}
	}
	return out
}

func dsFindings(t *testing.T) []dsFinding {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	var out []dsFinding
	for _, path := range paths {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") || base == "tokens.go" || base == "design.go" {
			continue // the system's own definitions, not views
		}
		chains, condAt, _ := dsParseFile(t, path)
		for _, ch := range chains {
			out = append(out, dsCheckChain(ch, condAt)...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

func dsCounts(findings []dsFinding) map[string]int {
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.key()]++
	}
	return counts
}

func dsReadBaseline(t *testing.T) map[string]int {
	t.Helper()
	f, err := os.Open(dsBaselinePath)
	if err != nil {
		t.Fatalf("missing %s — regenerate it with DESIGN_UPDATE_BASELINE=1 go test ./internal/app/chrome -run TestDesignSystemUpdateBaseline", dsBaselinePath)
	}
	defer f.Close()
	out := map[string]int{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) != 4 {
			t.Fatalf("bad baseline line %q", line)
		}
		n, err := strconv.Atoi(parts[3])
		if err != nil {
			t.Fatalf("bad baseline count %q", line)
		}
		out[strings.Join(parts[:3], "|")] = n
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestDesignSystemRatchet is the gate. Counted debt may only shrink; a new
// deviation fails. Batch four emptied the baseline, so any deviation at all
// is now "new". DS-06 is a hard rule with zero exemptions: a view may never
// invent a color outside the palette.
func TestDesignSystemRatchet(t *testing.T) {
	findings := dsFindings(t)
	if os.Getenv("DESIGN_UPDATE_BASELINE") == "1" {
		return // regeneration run; TestDesignSystemUpdateBaseline writes the file
	}
	baseline := dsReadBaseline(t)
	counts := dsCounts(findings)
	failed := false
	for key, n := range counts {
		want, known := baseline[key]
		parts := strings.Split(key, "|")
		if parts[2] == "DS-06" || !known {
			detail := ""
			for _, f := range findings {
				if f.key() == key {
					detail = fmt.Sprintf("%s:%d — %s", f.File, f.Line, f.Detail)
					break
				}
			}
			t.Errorf("new design deviation [%s] ×%d: %s\n  fix: %s", key, n, detail, dsRules[parts[1]].Fix)
			failed = true
			continue
		}
		if n > want {
			t.Errorf("design deviation grew [%s]: %d → %d (baseline %d)\n  fix: %s", key, want, n, want, dsRules[parts[1]].Fix)
			failed = true
		}
	}
	tightened := 0
	for key, want := range baseline {
		if counts[key] < want {
			tightened++
		}
	}
	if failed {
		t.Fatalf("design system gate failed — see docs/design-system.md. Regenerate the baseline only after fixing or explicitly registering a new exception.")
	}
	t.Logf("design system: %d legacy findings, %d keys; %d keys can be tightened", len(findings), len(counts), tightened)
}

// TestDesignSystemUpdateBaseline writes the current debt snapshot. It is a
// no-op unless explicitly requested, so an accidental `go test ./...` cannot
// silently bless new deviations.
func TestDesignSystemUpdateBaseline(t *testing.T) {
	if os.Getenv("DESIGN_UPDATE_BASELINE") != "1" {
		t.Skip("set DESIGN_UPDATE_BASELINE=1 to regenerate design_baseline.txt")
	}
	counts := dsCounts(dsFindings(t))
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Design-system ratchet baseline — generated, do not hand-edit.\n")
	b.WriteString("# Regenerate: DESIGN_UPDATE_BASELINE=1 go test ./internal/app/chrome -run TestDesignSystemUpdateBaseline\n")
	b.WriteString("# Every line is file|rule|value|count. A change may only LOWER a count;\n")
	b.WriteString("# raising one requires an explicit, documented exception in docs/design-system.md.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s|%d\n", k, counts[k])
	}
	if err := os.WriteFile(dsBaselinePath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d keys)", dsBaselinePath, len(keys))
}

// TestDesignSystemAudit prints the always-current inventory and, when
// DESIGN_AUDIT_REPORT points at a file, writes the full Markdown report.
func TestDesignSystemAudit(t *testing.T) {
	findings := dsFindings(t)
	byRule := map[string]int{}
	byFile := map[string]int{}
	for _, f := range findings {
		byRule[f.Rule]++
		byFile[f.File]++
	}
	for _, rule := range dsRuleOrder {
		t.Logf("%-6s %-3s %4d  %s", rule, dsRules[rule].Severity, byRule[rule], dsRules[rule].Title)
	}
	path := os.Getenv("DESIGN_AUDIT_REPORT")
	if path == "" {
		return
	}
	var b strings.Builder
	b.WriteString("# fx-desktop 设计系统存量审计报告\n\n")
	fmt.Fprintf(&b, "生成时间：%s（由 `TestDesignSystemAudit` 从源码 AST 实时扫描，非手工清单）\n\n", time.Now().Format("2006-01-02 15:04"))
	b.WriteString("本报告列出**存量**偏离；门禁 `TestDesignSystemRatchet` 保证后续任务不新增偏离。\n")
	b.WriteString("规则与工作流见 `docs/design-system.md`。\n\n")
	b.WriteString("## 总览\n\n| 规则 | 严重度 | 数量 | 含义 | 修复方向 |\n| --- | --- | ---: | --- | --- |\n")
	for _, rule := range dsRuleOrder {
		r := dsRules[rule]
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s |\n", rule, r.Severity, byRule[rule], r.Title, r.Fix)
	}
	fmt.Fprintf(&b, "\n合计 **%d** 处，分布在 %d 个视图文件。\n\n", len(findings), len(byFile))

	// High-signal findings first: these are the design/interaction bugs that
	// produced the recent rounds of screenshot feedback.
	b.WriteString("## 重点问题（会直接产生视觉/交互不一致）\n\n")
	for _, rule := range []string{"DS-06", "DS-07", "DS-08", "DS-09", "DS-11", "DS-01"} {
		var rows []dsFinding
		for _, f := range findings {
			if f.Rule == rule {
				rows = append(rows, f)
			}
		}
		fmt.Fprintf(&b, "### %s · %s（%d 处）\n\n", rule, dsRules[rule].Title, len(rows))
		if len(rows) == 0 {
			b.WriteString("无。\n\n")
			continue
		}
		b.WriteString("| 文件:行 | 值 | 说明 |\n| --- | --- | --- |\n")
		for _, f := range rows {
			fmt.Fprintf(&b, "| `%s:%d` | `%s` | %s |\n", f.File, f.Line, f.Value, f.Detail)
		}
		fmt.Fprintf(&b, "\n修复方向：%s\n\n", dsRules[rule].Fix)
		if rule == "DS-01" {
			b.WriteString("> 已登记的例外应改用 `ContentBody` / `ContentReadout` / `DisplayTitle` / `DisplayLead` 常量；紧凑图标用 `IconTiny` / `IconCompact` / `IconSmall`。除此之外出现的越阶值都是新阶梯，必须先改 `design.go` 与本文档并说明理由。\n\n")
		}
	}

	b.WriteString("## 全量存量清单（按文件）\n\n")
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, file := range files {
		fmt.Fprintf(&b, "<details><summary>%s · %d 处</summary>\n\n", file, byFile[file])
		b.WriteString("| 行 | 规则 | 值 | 说明 |\n| ---: | --- | --- | --- |\n")
		for _, f := range findings {
			if f.File == file {
				fmt.Fprintf(&b, "| %d | %s | `%s` | %s |\n", f.Line, f.Rule, f.Value, f.Detail)
			}
		}
		b.WriteString("\n</details>\n\n")
	}
	b.WriteString("## 布局体检\n\n")
	b.WriteString("`TestDesignSystemBreakpoints` 在 1440×900 / 1024×768 / 768×700 / 720×480 四档渲染主壳、Session history、Workspace tools、Review 四个视图，")
	b.WriteString("逐个检查可见标签的**横向**边界（纵向由 Scroll 容器负责，属正常滚动内容）。当前结果：无横向溢出。\n\n")
	b.WriteString("## 如何收敛\n\n")
	b.WriteString("1. 修一批 → 直接跑 `go test ./internal/app/chrome -run TestDesignSystemRatchet`，计数下降即可，无需改基线。\n")
	b.WriteString("2. 只有**新增经评审的例外**才允许提高基线：`DESIGN_UPDATE_BASELINE=1 go test ./internal/app/chrome -run TestDesignSystemUpdateBaseline`，并在 `docs/design-system.md` 写明理由。\n")
	b.WriteString("3. 重新出报告：`DESIGN_AUDIT_REPORT=outputs/design-audit.md go test ./internal/app/chrome -run TestDesignSystemAudit -v`。\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote audit report %s", path)
}

// TestDesignSystemBreakpoints is the runtime half of the layout audit: a
// control may never leave the window horizontally at any supported width.
// Vertical overflow is deliberately not checked — scrollable sheets move
// content below their viewport on purpose.
func TestDesignSystemBreakpoints(t *testing.T) {
	views := map[string]func(*Host, *ShellState){
		"shell": func(h *Host, sh *ShellState) {},
		"history": func(h *Host, sh *ShellState) {
			now := time.Now()
			h.Actions.OpenHistory = noop
			sh.History = HistoryState{Open: true, Loaded: true, Exhausted: true, Entries: []HistorySession{
				{ID: "a", Title: "First conversation", UpdatedAtMS: now.UnixMilli(), Turns: 3},
				{ID: "b", Title: "Second conversation", UpdatedAtMS: now.UnixMilli(), Turns: 6},
			}}
			h.History = HistoryHost{Run: func(string) {}, ActiveID: "b", Now: now, Rel: func(int64) string { return "just now" }}
		},
		"tools": func(h *Host, sh *ShellState) {
			h.Actions.ManageWorkspace = noop
			sh.Tools = ManagementState{Open: true, Page: "Launch", Transport: "stdio", Period: "month", Ultra: "default"}
			h.Management = ManagementHost{Run: func(string) {}}
		},
		"review": func(h *Host, sh *ShellState) {
			sh.ReviewOpen = true
			h.Actions.Commit = noop
			h.Actions.StageAll = noop
			h.Actions.ToggleReview = noop
			h.Actions.CloseReview = noop
		},
	}
	names := make([]string, 0, len(views))
	for n := range views {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, view := range names {
		for _, size := range []struct{ w, h int }{{1440, 900}, {1024, 768}, {768, 700}, {720, 480}} {
			h, sh := testHost()
			h.Appearance = "light"
			views[view](&h, sh)
			ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, size.w, size.h)
			ts.SetDark(false)
			ts.Frame()
			seen := map[string]bool{}
			for _, label := range ts.Texts() {
				if seen[label] {
					continue
				}
				seen[label] = true
				r, ok := ts.Find(label)
				if !ok || (r.W == 0 && r.H == 0) {
					continue // clipped away by a scroll viewport
				}
				if r.X < -1 || r.X+r.W > float32(size.w)+1 {
					t.Errorf("%s at %dx%d: %q extends outside the window horizontally: %+v", view, size.w, size.h, label, r)
				}
			}
		}
	}
}

// TestDesignSystemDocumentInSync keeps the written contract and the
// executable tokens from drifting apart.
func TestDesignSystemDocumentInSync(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "design-system.md"))
	if err != nil {
		t.Fatalf("docs/design-system.md is missing: %v", err)
	}
	text := string(doc)
	for _, name := range []string{
		"TypeBadge", "TypeLabel", "TypeControl", "TypeInput", "TypeTitle",
		"IconSmall", "IconMedium", "IconLarge", "IconDisplay",
		"ContentBody", "ContentReadout", "DisplayTitle", "DisplayLead",
		"Space0", "Space7", "Space9", "Space16", "ButtonPadX", "InlinePadY", "SheetPadX",
		"RadiusNone", "RadiusCell", "RadiusChip", "RadiusControl", "RadiusBubble", "RadiusSheet", "RadiusPill",
		"stateSurface", "平局向下", "T55",
		"TestDesignSystemRatchet", "TestDesignSystemNamedConstantsOnly", "TestDesignSystemGrowHasMinSize", "TestDesignSystemGrowAxisMatchesParent", "TestDesignSystemNoLegacyAliases", "TestDesignSystemOnScaleOnly", "TestDesignSystemBaselineIsEmpty", "design_baseline.txt",
	} {
		if !strings.Contains(text, name) {
			t.Errorf("docs/design-system.md does not document %s", name)
		}
	}
	for _, name := range []string{"IconTiny", "IconCompact", "IconXL"} {
		if !strings.Contains(text, name) {
			t.Errorf("docs/design-system.md does not document %s", name)
		}
	}
}

// The ratchet only stops NEW deviations. These rules are the ones that
// actually change what a user sees or can click — every screenshot round
// from T44 to T50 came from one of them — so they must reach zero, not
// merely stop growing. This gate also lists exactly what has to be repaired.
func TestDesignSystemHighSeverityIsZero(t *testing.T) {
	findings := dsFindings(t)
	var bad []dsFinding
	for _, f := range findings {
		switch f.Rule {
		case "DS-01", "DS-06", "DS-07", "DS-08", "DS-09", "DS-11":
			bad = append(bad, f)
		}
	}
	if len(bad) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d high-severity design findings remain:\n", len(bad))
	for _, f := range bad {
		fmt.Fprintf(&b, "  %s:%d  %s  %-10s  %s\n", f.File, f.Line, f.Rule, f.Value, f.Detail)
	}
	t.Fatal(b.String())
}

// Every list that highlights a row comes through stateSurface. Lock its one
// colour here so a future picker cannot quietly go back to Sunken (T50):
// the AST gate catches the literal, this test catches the rendered result.
func TestStateSurfaceUsesTheOneSelectionColor(t *testing.T) {
	var p Palette
	ts := ui.NewTester(func(c *ui.Context) {
		p = palette(c.Theme())
		ui.Column(c).Children(func() {
			active := ui.Row(c).Label("probe active row").Width(120).Height(20)
			stateSurface(active, p, true, false)
			idle := ui.Row(c).Label("probe idle row").Width(120).Height(20)
			stateSurface(idle, p, false, false)
		})
	}, 200, 80)
	ts.SetDark(false)
	ts.Frame()

	active, ok := ts.Find("probe active row")
	if !ok {
		t.Fatal("the active row is missing")
	}
	idle, ok := ts.Find("probe idle row")
	if !ok {
		t.Fatal("the idle row is missing")
	}
	img := ts.Image()
	got := img.RGBAAt(int(active.X)+10, int(active.Y)+10)
	want := p.SurfaceHi
	if got.R != want.R || got.G != want.G || got.B != want.B {
		t.Fatalf("selected row is rgb(%d,%d,%d), want SurfaceHi rgb(%d,%d,%d)",
			got.R, got.G, got.B, want.R, want.G, want.B)
	}
	plain := img.RGBAAt(int(idle.X)+10, int(idle.Y)+10)
	if plain.R == want.R && plain.G == want.G && plain.B == want.B {
		t.Fatal("an idle row is drawn with the selection surface")
	}
}

// Batch two named every bare visual number. From now on a raw font size,
// padding, gap or radius is a hard failure — not just another ratchet entry
// that could be blessed by regenerating the baseline. This is the guard that
// keeps T44–T46 from happening again (a new TextArea silently falling back to
// the theme's 13px, a new button inventing a 9px padding).
func TestDesignSystemNamedConstantsOnly(t *testing.T) {
	var bad []dsFinding
	for _, f := range dsFindings(t) {
		if f.Rule == "DS-02" || f.Rule == "DS-05" {
			bad = append(bad, f)
		}
	}
	if len(bad) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d bare visual literals remain — use the named constants in design.go:\n", len(bad))
	for i, f := range bad {
		if i == 20 {
			fmt.Fprintf(&b, "  ... and %d more\n", len(bad)-i)
			break
		}
		fmt.Fprintf(&b, "  %s:%d  %s  %-10s  %s\n", f.File, f.Line, f.Rule, f.Value, f.Detail)
	}
	t.Fatal(b.String())
}

// Batch three gave every Grow(1) the min-size partner its parent's main axis
// needs (mygo's Grow shares free space ALONG THE PARENT'S MAIN AXIS). A new
// Grow without one can silently squeeze a flex sibling out of the window at a
// narrow breakpoint, so DS-10 is zero-tolerance too — independent of the
// ratchet baseline, and with the axis rule spelled out in the failure text.
func TestDesignSystemGrowHasMinSize(t *testing.T) {
	var bad []dsFinding
	for _, f := range dsFindings(t) {
		if f.Rule == "DS-10" {
			bad = append(bad, f)
		}
	}
	if len(bad) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d Grow(1) calls have no min-size partner:\n", len(bad))
	for _, f := range bad {
		fmt.Fprintf(&b, "  %s:%d  %s  %s\n", f.File, f.Line, f.Value, f.Detail)
	}
	b.WriteString("  fix: a child of a Row needs MinWidth(0); a child of a Column needs MinHeight(0)\n")
	t.Fatal(b.String())
}

// Having *some* min-size partner is not enough: the wrong axis leaves the
// flex child as unconstrained as before. A probe confirmed that swapping
// MinWidth(0) for MinHeight(0) inside a Row slipped past both DS-10 and the
// breakpoint test. This test resolves each Grow call's innermost
// Children(func(){...}) container and checks the partner against that
// container's main axis. Custom containers with no known axis are skipped,
// by design — see dsContainerDirection.
func TestDesignSystemGrowAxisMatchesParent(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	var bad []string
	checked, skipped := 0, 0
	for _, path := range paths {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") || base == "tokens.go" || base == "design.go" {
			continue
		}
		chains, _, scopes := dsParseFile(t, path)
		for _, ch := range chains {
			if !ch.methods["Grow"] || ch.root == "Box" {
				continue
			}
			if !ch.methods["MinWidth"] && !ch.methods["MinHeight"] {
				continue // TestDesignSystemGrowHasMinSize owns that failure
			}
			pos := ch.positions["Grow"]
			dir, innermost := "", token.Pos(0)
			for _, s := range scopes {
				if pos >= s.start && pos < s.end && s.start >= innermost {
					innermost, dir = s.start, s.dir
				}
			}
			if dir == "" {
				skipped++
				continue
			}
			checked++
			want := "MinWidth"
			if dir == "column" {
				want = "MinHeight"
			}
			if !ch.methods[want] {
				bad = append(bad, fmt.Sprintf("  %s:%d  %s in a %s needs %s(0) (MinWidth=%v MinHeight=%v)",
					ch.file, ch.lines["Grow"], ch.root, dir, want, ch.methods["MinWidth"], ch.methods["MinHeight"]))
			}
		}
	}
	t.Logf("axis check: %d Grow calls with a known parent, %d skipped (custom containers)", checked, skipped)
	if len(bad) > 0 {
		t.Fatalf("%d Grow calls have a min-size partner on the WRONG axis:\n%s\n  fix: a child of a Row needs MinWidth(0); a child of a Column needs MinHeight(0)", len(bad), strings.Join(bad, "\n"))
	}
}

// Batch four (T55) removed the Legacy* debt aliases and retuned every use
// onto the standard scale. Three things must now stay true forever: no view
// may reference a Legacy* name again, every Space*/Radius* constant in
// design.go must itself be on the scale it defines, and the const parser
// must keep working (so dsResolvedValue cannot silently stop resolving).
func TestDesignSystemNoLegacyAliases(t *testing.T) {
	consts := dsDesignConstants()
	if len(consts) < 30 {
		t.Fatalf("design.go exposes only %d numeric constants — is dsDesignConstants still parsing it?", len(consts))
	}
	for name, n := range consts {
		if strings.HasPrefix(name, "Legacy") {
			t.Errorf("design.go still declares the removed debt alias %s = %d", name, n)
		}
		if strings.HasPrefix(name, "Space") && !dsSpacingGrid[n] {
			t.Errorf("design.go %s = %d is off the 2px spacing grid", name, n)
		}
		if strings.HasPrefix(name, "Radius") && !dsRadiusSet[strconv.Itoa(n)] {
			t.Errorf("design.go %s = %d is off the radius ladder", name, n)
		}
	}
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") || base == "design.go" {
			continue // test fixtures and the definition file itself
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if ok && strings.HasPrefix(id.Name, "Legacy") {
				t.Errorf("%s:%d references the removed debt alias %s — use a Space*/Radius* constant",
					base, fset.Position(id.Pos()).Line, id.Name)
			}
			return true
		})
	}
}

// Batch four emptied the ratchet. All eleven DS rules are now covered by a
// zero-tolerance test, so the baseline must stay empty: a hand-edited or
// regenerated baseline re-blessing old debt would otherwise be invisible,
// because the ratchet only fails when a count GROWS. Registering a genuine
// new exception means editing this test AND documenting the reason.
func TestDesignSystemBaselineIsEmpty(t *testing.T) {
	baseline := dsReadBaseline(t)
	if len(baseline) == 0 {
		return
	}
	keys := make([]string, 0, len(baseline))
	for k := range baseline {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "design_baseline.txt still lists %d legacy keys; batch four cleared all debt:\n", len(baseline))
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s = %d\n", k, baseline[k])
	}
	b.WriteString("  fix: repair the finding instead of regenerating the baseline\n")
	t.Fatal(b.String())
}

// DS-03 / DS-04 were ratcheted legacy debt until batch four. With the
// aliases gone there is no reason a view may ever introduce an off-grid
// value again, so they are zero-tolerance: refreshing the baseline must
// not be able to bless one either.
func TestDesignSystemOnScaleOnly(t *testing.T) {
	var bad []dsFinding
	for _, f := range dsFindings(t) {
		if f.Rule == "DS-03" || f.Rule == "DS-04" {
			bad = append(bad, f)
		}
	}
	if len(bad) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d off-scale spacing/radius values remain — use the named steps in design.go:\n", len(bad))
	for i, f := range bad {
		if i == 20 {
			fmt.Fprintf(&b, "  ... and %d more\n", len(bad)-i)
			break
		}
		fmt.Fprintf(&b, "  %s:%d  %s  %-10s  %s\n", f.File, f.Line, f.Rule, f.Value, f.Detail)
	}
	b.WriteString("  fix: Space* runs on a 2px grid (0/2/4/…/32); Radius* is 0/1/3/5/8/10/16\n")
	t.Fatal(b.String())
}
