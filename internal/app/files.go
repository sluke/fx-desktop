package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
)

// maxFileEntries bounds one folder's row count. A workspace folder can hold
// tens of thousands of names (node_modules, a build cache); the browser keeps
// the first entries and says how many it left out, so painting the rail stays
// proportional to what is on screen rather than to what is on disk.
const maxFileEntries = 400

// fileEntry is one name read from a folder: the only fact the tree needs from
// the disk. Whether it is a folder is resolved when the listing is read, so a
// frame never stats a path.
type fileEntry struct {
	name string
	dir  bool
}

// fileBrowser is the Files tab's cache and expansion state. Every map is
// guarded by the owning window's mu: directory reads finish on their own
// goroutine, and the frame reads what they left behind.
type fileBrowser struct {
	entries   map[string][]fileEntry // absolute folder -> ordered entries
	read      map[string]bool        // the folder has been read once
	expanded  map[string]bool        // the user opened this folder
	problems  map[string]string      // why a read failed
	loading   map[string]bool        // a read is in flight
	truncated map[string]int         // entries the cap left out
	// rows is the flattened projection of all of the above. A frame draws the
	// same tree many times between two changes, so it is rebuilt lazily on the
	// first frame after a write instead of on every repaint. Both fields are
	// guarded by the owning window's workspace.mu.
	rows  []chrome.FileRow
	dirty bool
}

func newFileBrowser() *fileBrowser {
	return &fileBrowser{
		entries:   map[string][]fileEntry{},
		read:      map[string]bool{},
		expanded:  map[string]bool{},
		problems:  map[string]string{},
		loading:   map[string]bool{},
		truncated: map[string]int{},
		dirty:     true,
	}
}

// markDirty retires the cached projection. Every write to a listing, an
// expansion or a read flag goes through it; the caller holds workspace.mu.
func (fb *fileBrowser) markDirty() {
	fb.dirty = true
	fb.rows = nil
}

// projected returns the visible rows. It walks the tree only when one of its
// inputs changed; otherwise a frame copies one slice header. Caller holds
// workspace.mu, because the maps it reads belong to that lock.
func (fb *fileBrowser) projected(root string) []chrome.FileRow {
	if fb.dirty || fb.rows == nil {
		fb.rows = flattenFiles(root, fb, root, 0)
		fb.dirty = false
	}
	return fb.rows
}

// ensureFiles starts reading a folder unless it has already been read or a
// read is in flight. It is safe to call while a frame builds: the disk call
// runs on its own goroutine and repaints when it answers.
func (c *Coordinator) ensureFiles(s *ProjectWindow, dir string) {
	if s == nil || dir == "" {
		return
	}
	s.workspace.mu.Lock()
	if s.workspace.files == nil {
		s.workspace.files = newFileBrowser()
	}
	fb := s.workspace.files
	if fb.read[dir] || fb.loading[dir] {
		s.workspace.mu.Unlock()
		return
	}
	fb.loading[dir] = true
	fb.markDirty()
	s.workspace.mu.Unlock()

	s.invalidate()
	go func() {
		entries, total, err := readFileEntries(dir)
		s.workspace.mu.Lock()
		// A refresh replaces the browser while a read is in flight. The
		// answer belongs to the browser that asked for it; filing it into
		// its successor would resurrect rows the refresh discarded.
		if s.workspace.files != fb {
			s.workspace.mu.Unlock()
			return
		}
		delete(fb.loading, dir)
		fb.read[dir] = true
		if err != nil {
			fb.problems[dir] = fileReadProblem(err)
		} else {
			fb.entries[dir] = entries
			if extra := total - len(entries); extra > 0 {
				fb.truncated[dir] = extra
			}
		}
		fb.markDirty()
		s.workspace.mu.Unlock()
		s.invalidate()
	}()
}

// readFileEntries lists one folder: folders first, then files, each by name
// without case. A symlinked folder is shown as a folder, since expanding it
// reads through the link, which is what the click asked for.
func readFileEntries(dir string) ([]fileEntry, int, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	entries := make([]fileEntry, 0, len(items))
	for _, item := range items {
		isDir := item.IsDir()
		if !isDir && item.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(dir, item.Name())); err == nil {
				isDir = info.IsDir()
			}
		}
		entries = append(entries, fileEntry{name: item.Name(), dir: isDir})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].dir != entries[j].dir {
			return entries[i].dir
		}
		li, lj := strings.ToLower(entries[i].name), strings.ToLower(entries[j].name)
		if li != lj {
			return li < lj
		}
		return entries[i].name < entries[j].name
	})
	total := len(entries)
	if total > maxFileEntries {
		entries = entries[:maxFileEntries]
	}
	return entries, total, nil
}

// fileReadProblem is the row's answer for a folder the browser could not read.
// It names the cause it knows and stays honest otherwise.
func fileReadProblem(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "Permission denied"
	case errors.Is(err, fs.ErrNotExist):
		return "Folder no longer exists"
	default:
		return "Could not read this folder"
	}
}

// flattenFiles turns the cached listings into the visible rows: the expanded
// folders' children sit under their folder, indented by depth. Only expanded
// folders are walked, so a large workspace costs only what the user opened.
func flattenFiles(root string, fb *fileBrowser, dir string, depth int) []chrome.FileRow {
	rows := make([]chrome.FileRow, 0, 16)
	appendFileRows(&rows, root, fb, dir, depth)
	return rows
}

// appendFileRows appends into one shared slice. The old recursive version
// built a slice per folder and copied the children into its parent with
// append(rows, flattenFiles(...)...), which is O(rows × depth) in both time
// and allocations on a deep tree.
func appendFileRows(rows *[]chrome.FileRow, root string, fb *fileBrowser, dir string, depth int) {
	entries := fb.entries[dir]
	for _, e := range entries {
		abs := filepath.Join(dir, e.name)
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			rel = e.name
		}
		row := chrome.FileRow{
			Name:     e.name,
			Path:     filepath.ToSlash(rel),
			Abs:      abs,
			Dir:      e.dir,
			Depth:    depth,
			Expanded: e.dir && fb.expanded[abs],
		}
		if row.Expanded {
			switch {
			case fb.loading[abs]:
				row.Note = "Reading…"
			case fb.problems[abs] != "":
				row.Note = fb.problems[abs]
			}
		}
		*rows = append(*rows, row)
		if row.Expanded {
			appendFileRows(rows, root, fb, abs, depth+1)
		}
	}
	if extra := fb.truncated[dir]; extra > 0 {
		*rows = append(*rows, chrome.FileRow{
			Name:  fmt.Sprintf("… %d more entries", extra),
			Depth: depth,
			More:  true,
		})
	}
}

// sessionFiles projects the cache into the panel. The read pass must not walk
// the disk; it only starts the root's first read if nothing has asked yet.
func (c *Coordinator) sessionFiles(s *ProjectWindow) chrome.FileBrowserData {
	if s == nil || s.project == "" {
		return chrome.FileBrowserData{}
	}
	// The tab is the only thing that needs a listing; a window that never
	// opens it should neither read the disk for it nor flatten a tree no
	// frame draws.
	if s.shell.InspectorTab != chrome.InspectorTabFiles {
		return chrome.FileBrowserData{Root: s.project}
	}
	c.ensureFiles(s, s.project)
	d := chrome.FileBrowserData{Root: s.project}
	s.workspace.mu.Lock()
	defer s.workspace.mu.Unlock()
	fb := s.workspace.files
	if fb == nil {
		return d
	}
	if !fb.read[s.project] && fb.loading[s.project] {
		d.Loading = true
	}
	d.Problem = fb.problems[s.project]
	// A repaint that changed nothing reuses the projection. The first frame
	// after a listing or expansion change still walks the tree here, under
	// workspace.mu, but that is once per change instead of once per frame.
	d.Rows = fb.projected(s.project)
	return d
}

// selectInspectorTab moves the right rail to the tab that was chosen and
// starts the files read on the way in, so the first frame of the browser is
// not an empty folder.
func (c *Coordinator) selectInspectorTab(s *ProjectWindow, tab int) {
	if s == nil {
		return
	}
	if tab != chrome.InspectorTabFiles {
		tab = chrome.InspectorTabTelemetry
	}
	s.shell.InspectorTab = tab
	s.shell.InspectorOpen = true
	if tab == chrome.InspectorTabFiles {
		c.ensureFiles(s, s.project)
	}
	s.invalidate()
}

// toggleFileDir expands or collapses one folder. Expanding starts its read on
// the first visit; collapsing keeps the listing, so reopening it is instant.
func (c *Coordinator) toggleFileDir(s *ProjectWindow, path string) {
	if s == nil {
		return
	}
	abs, ok := workspacePath(s.project, path)
	if !ok {
		return
	}
	s.workspace.mu.Lock()
	if s.workspace.files == nil {
		s.workspace.files = newFileBrowser()
	}
	fb := s.workspace.files
	opened := !fb.expanded[abs]
	if opened {
		fb.expanded[abs] = true
	} else {
		delete(fb.expanded, abs)
	}
	needRead := opened && !fb.read[abs] && !fb.loading[abs]
	fb.markDirty()
	s.workspace.mu.Unlock()

	if needRead {
		c.ensureFiles(s, abs)
	}
	s.invalidate()
}

// openFileWithDefaultApp hands one row to the platform: a file opens in its
// default application, a folder opens in the file manager. It is deferred past
// the frame because launching an application can spin the event loop.
func (c *Coordinator) openFileWithDefaultApp(s *ProjectWindow, path string) {
	if s == nil {
		return
	}
	abs, ok := workspacePath(s.project, path)
	if !ok {
		return
	}
	mygo.App.WhenReady(func() {
		if err := mygo.Shell.OpenPath(abs); err != nil {
			mygo.Dialog.Error("Open item", "Could not open:\n"+abs)
		}
	})
}

// openFileInTextEditor hands a file to the platform's text editor rather than
// to the application that owns its type. Resolving the editor can run a
// desktop query on Linux, so it happens off the frame, and a failure comes
// back as a dialog instead of a menu item that silently did nothing.
func (c *Coordinator) openFileInTextEditor(s *ProjectWindow, path string) {
	if s == nil {
		return
	}
	abs, ok := workspacePath(s.project, path)
	if !ok {
		return
	}
	mygo.App.WhenReady(func() {
		go func() {
			err := launchTextEditor(abs)
			if err == nil {
				return
			}
			mygo.App.WhenReady(func() {
				mygo.Dialog.Error("Open with Text Editor", err.Error())
			})
		}()
	})
}

// refreshFiles discards the cached listings and re-reads the root and every
// folder the user has open. The expansion itself survives: refreshing should
// show a file added a moment ago, not close the tree the user built.
func (c *Coordinator) refreshFiles(s *ProjectWindow) {
	if s == nil || s.project == "" {
		return
	}
	s.workspace.mu.Lock()
	var expanded []string
	if s.workspace.files != nil {
		expanded = make([]string, 0, len(s.workspace.files.expanded))
		for dir := range s.workspace.files.expanded {
			expanded = append(expanded, dir)
		}
	}
	fb := newFileBrowser()
	for _, dir := range expanded {
		fb.expanded[dir] = true
	}
	s.workspace.files = fb
	s.workspace.mu.Unlock()

	c.ensureFiles(s, s.project)
	for _, dir := range expanded {
		c.ensureFiles(s, dir)
	}
	s.invalidate()
}
