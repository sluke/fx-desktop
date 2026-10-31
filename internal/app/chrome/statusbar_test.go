package chrome

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// middots counts how many times a text is drawn.
func middots(ts *ui.Tester, want string) int {
	n := 0
	for _, s := range ts.Texts() {
		if s == want {
			n++
		}
	}
	return n
}

// The status bar's groups sit at the two ends of the strip, and the middot
// that joins segments on the left has the whole gap on its right. The
// separator starts over at the right group: a middot belongs between two
// segments, and one with nothing on its left reads as a stray dot.
func TestStatusBarOnlySeparatesSegmentsItShows(t *testing.T) {
	h, _ := testHost() // left: the version, highlighted: the tree, right: the path
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	if got := middots(ts, "·"); got != 1 {
		t.Errorf("middots = %d, want 1 (version · tree): %v", got, ts.Texts())
	}
	// The one middot joins the version to the tree, in that order.
	version, _ := ts.Find("fx v0.0.13")
	tree, _ := ts.Find("● 1 modified")
	dot, _ := ts.Find("·")
	if dot.X < version.X+version.W-0.01 || dot.X+dot.W > tree.X+0.01 {
		t.Errorf("middot %v is not between the version %v and the tree %v", dot, version, tree)
	}
	// The right group starts without one.
	if path, _ := ts.Find("/p/proj"); path.X < tree.X {
		t.Errorf("path at %v should sit at the far end, after the tree at %v", path.X, tree.X)
	}
}

// Two segments on the right are still two segments: the separator joins them
// there, and the group's first one starts without one.
func TestStatusBarJoinsTheRightGroupsOwnSegments(t *testing.T) {
	h, _ := testHost()
	h.StatusRight = []string{"/p/proj", "2 recent"}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	if got := middots(ts, "·"); got != 2 {
		t.Errorf("middots = %d, want 2 (version · tree, path · recent): %v", got, ts.Texts())
	}
	path, _ := ts.Find("/p/proj")
	recent, _ := ts.Find("2 recent")
	if path.X >= recent.X {
		t.Errorf("path at %v should sit before %v", path.X, recent.X)
	}
}
