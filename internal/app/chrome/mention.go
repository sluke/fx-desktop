package chrome

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/workspace"
)

// MentionFile is one file the @ picker can offer. Path is what a mention
// writes into the message — the project path, the way fx's own picker writes
// it; Name and Dir are what a row shows.
type MentionFile = workspace.File

// mentionLimit is how many rows the picker shows. fx's own picker shows up to
// ten when the terminal has room; this one keeps a shorter list, so opening it
// never buries the message being written.
const mentionLimit = 8

// A mention is @path, where path names a file in the project. The rule for
// reading one lives in the workspace package, once, because the picker and
// the sender must see the same message the same way: otherwise a name the
// picker wrote would arrive at fx as prose.

// MentionPaths lists the paths a message names with @path, in the order they
// appear and without repeats.
func MentionPaths(text string) []string { return workspace.MentionPaths(text) }

// mentionAt reads the @ mention the caret is in: the query typed after the @,
// and the rune range the mention occupies (start at the @, end at the caret).
// ok is false when the caret is not in one, so the picker appears only where a
// mention could be written, and replaces only what was written.
func mentionAt(text string, caret int) (query string, start, end int, ok bool) {
	runes := []rune(text)
	caret = min(max(caret, 0), len(runes))
	start = caret
	for start > 0 && !workspace.MentionBreak(runes[start-1]) {
		start--
	}
	// The caret must be past the @ — sitting on it is not yet a mention — and
	// the path must run to the caret: punctuation after a name ends the
	// mention rather than becoming part of what is being typed.
	if start >= caret || runes[start] != '@' {
		return "", 0, 0, false
	}
	for _, r := range runes[start+1 : caret] {
		if workspace.MentionEnd(r) {
			return "", 0, 0, false
		}
	}
	return string(runes[start+1 : caret]), start, caret, true
}

// mentionText is what a mention writes: the path, marked with @ and followed
// by a space, so the word ends and the message can go on.
func mentionText(path string) string { return "@" + path + " " }

// mentionInsertion replaces the mention at [start, end) with path, leaving the
// rest of the draft alone.
func mentionInsertion(text string, start, end int, path string) string {
	runes := []rune(text)
	start = min(max(start, 0), len(runes))
	end = min(max(end, start), len(runes))
	return string(runes[:start]) + mentionText(path) + string(runes[end:])
}

// rankMentionFiles orders the files that match a query the way fx's picker
// does: the name itself, then a name that starts with the query, then a path
// that does, then anything that contains it, then the letters in order. Files
// that match none of those are left out. Ties keep the order the app listed
// them in, which is a stable order of its own.
//
// It is one pass with one bounded bucket per rank, not a sort: a project can
// hold a hundred thousand files, and the picker asks again on every frame it
// is open. Each bucket holds at most limit files because earlier ranks fill
// the answer first; a file beyond that could never be shown (§4.10.3).
func rankMentionFiles(files []MentionFile, query string, limit int) []MentionFile {
	if limit <= 0 {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return files[:min(limit, len(files))]
	}
	letters := []rune(q)
	var buckets [6][]MentionFile
	for _, file := range files {
		name, path := file.LowerName, file.LowerPath
		if name == "" {
			name = strings.ToLower(file.Name)
		}
		if path == "" {
			path = strings.ToLower(file.Path)
		}
		// One index per string answers both "starts with" and "contains".
		nameAt, pathAt := strings.Index(name, q), strings.Index(path, q)
		rank := -1
		switch {
		case name == q:
			rank = 0
		case nameAt == 0:
			rank = 1
		case pathAt == 0:
			rank = 2
		case nameAt > 0:
			rank = 3
		case pathAt > 0:
			rank = 4
		case scattered(name, letters) || scattered(path, letters):
			rank = 5
		}
		if rank >= 0 && len(buckets[rank]) < limit {
			buckets[rank] = append(buckets[rank], file)
		}
	}
	out := make([]MentionFile, 0, limit)
	for _, bucket := range buckets {
		for _, file := range bucket {
			if len(out) == limit {
				return out
			}
			out = append(out, file)
		}
	}
	return out
}

// scattered reports whether the query's letters appear in order, which is how
// fx's picker keeps a half-remembered path reachable.
func scattered(text string, letters []rune) bool {
	i := 0
	for _, r := range text {
		if i < len(letters) && r == letters[i] {
			i++
		}
	}
	return i == len(letters)
}

// activeMention is the file the picker would take now.
func activeMention(state *ConversationState, h ConversationHost) (MentionFile, bool) {
	rows := rankMentionFiles(h.MentionFiles, state.MentionQuery, mentionLimit)
	if len(rows) == 0 {
		return MentionFile{}, false
	}
	return rows[min(max(state.MentionActive, 0), len(rows)-1)], true
}

// dismissMention closes the picker: it belongs to the message being written,
// so it goes when the mention under the caret does.
func dismissMention(state *ConversationState) {
	state.MentionOpen = false
	state.MentionActive = 0
}

// acceptMention writes the chosen file into the draft where the mention was.
// The caret follows on the next pass, after the field has taken the text: a
// caret can only be moved while the pass that builds the field runs, and the
// field keeps the caret it holds when it publishes a new value.
//
// The insertion is an edit of the draft like any other: it is announced
// through h.DraftEdited so a submission in flight keeps the field it did not
// take (§B5).
func acceptMention(state *ConversationState, h ConversationHost, file MentionFile) {
	caret := state.MentionStart + len([]rune(mentionText(file.Path)))
	state.Draft = mentionInsertion(state.Draft, state.MentionStart, state.MentionEnd, file.Path)
	if h.DraftEdited != nil {
		h.DraftEdited()
	}
	state.MentionCaretAt, state.MentionCaretPending = caret, true
	dismissMention(state)
	state.MentionStart, state.MentionEnd, state.MentionQuery = 0, 0, ""
	// A row click takes the focus with it: the field takes it back, so the
	// message goes on where it was left.
	state.Input.Focus()
}

// mentionKey gives the picker the keys that move through and take its rows
// while it is open. It reports whether the key was the picker's, so Enter
// inserts the file it offers instead of sending a message that names one
// halfway.
func mentionKey(state *ConversationState, h ConversationHost, k ui.TextKey) bool {
	if !state.MentionOpen || k.Mods != 0 {
		return false
	}
	switch k.Key {
	case ui.KeyUp:
		state.MentionActive = max(0, state.MentionActive-1)
		return true
	case ui.KeyDown:
		state.MentionActive++
		return true
	case ui.KeyEscape:
		// Turning a mention down holds until what is being written changes:
		// the next pass reads the field again, and would offer it back.
		state.MentionClosed = "@" + state.MentionQuery
		dismissMention(state)
		return true
	case ui.KeyEnter:
		file, ok := activeMention(state, h)
		if !ok {
			return false
		}
		acceptMention(state, h, file)
		// The editor still holds the draft as it was: hand it the completed
		// text, which is what the bound value now says too.
		k.SetText(state.Draft)
		return true
	}
	return false
}

// followMention keeps the picker on the mention the field's caret is in. The
// caret can only be read while its own pass builds the field, so this runs
// there, right after the field is built and its text published.
func followMention(state *ConversationState, h ConversationHost, field ui.Element) {
	query, start, end, ok := mentionAt(state.Draft, textCaret(field))
	if !ok || !h.CanMention {
		// Leaving the mention forgets a dismissal: it was about that token.
		state.MentionClosed = ""
		dismissMention(state)
		return
	}
	state.MentionQuery, state.MentionStart, state.MentionEnd = query, start, end
	if state.MentionClosed == "@"+query {
		// The mention the user turned down is not offered back by the next
		// pass reading the same field. It comes back when the words change.
		state.MentionOpen = false
		return
	}
	state.MentionClosed = ""
	if state.MentionOpen {
		return
	}
	state.MentionOpen, state.MentionActive = true, 0
	// The files are the project's, so the app is the one that reads them.
	// Asking only flags a read and schedules a frame of its own, which is what
	// makes it safe to ask here, in the pass that saw the picker open.
	if h.OpenMentions != nil {
		h.OpenMentions()
	}
}

// textCaret is where the caret sits in a text input, in runes. The second
// offset is the caret; the first is the other end of a selection.
func textCaret(field ui.Element) int {
	_, end := field.TextSelection()
	return end
}

// mentionPicker offers the project files that match the mention being written.
// It opens inside the composer, under the field it completes: the mention is
// read from that field's caret, and a caret is only readable during the pass
// that builds the field.
func mentionPicker(c *ui.Context, state *ConversationState, h ConversationHost, p Palette) {
	if !state.MentionOpen {
		return
	}
	rows := rankMentionFiles(h.MentionFiles, state.MentionQuery, mentionLimit)
	active := 0
	if len(rows) > 0 {
		active = min(max(state.MentionActive, 0), len(rows)-1)
	}
	state.MentionActive = active
	ui.Column(c).FillWidth().Radius(RadiusControl).Background(p.Surface).BorderWidth(1).BorderColor(p.Hairline).Clip().
		Children(func() {
			ui.Row(c).FillWidth().Padding(Space4, Space5).AlignItems(ui.Center).Gap(Space4).Children(func() {
				mono(ui.Text(c, "Files")).FontSize(TypeLabel).FontWeight(500).TextColor(p.InkStrong).SingleLine()
				ui.Box(c).Grow(1)
				switch {
				case h.MentionLoading:
					ui.Text(c, "Reading this project…").FontSize(TypeLabel).TextColor(p.InkMuted).SingleLine()
				case h.MentionProblem != "":
					ui.Text(c, h.MentionProblem).FontSize(TypeLabel).TextColor(p.Warn).SingleLine()
				case len(rows) > 0:
					// A count is data, not a keyboard legend: the marked
					// row below shows what Enter would take.
					mono(ui.Text(c, itoa(len(rows))+" files")).FontSize(TypeBadge).TextColor(p.InkFaint).SingleLine()
				}
			})
			ui.Scroll(c).FillWidth().MaxHeight(220).Children(func() {
				switch {
				case len(rows) > 0:
					for i, file := range rows {
						mentionRow(c, state, h, p, file, i == active)
					}
				case h.MentionLoading:
					ui.Text(c, "Reading this project…").FontSize(TypeInput).TextColor(p.InkMuted).Padding(Space6, Space5)
				case h.MentionProblem != "":
					ui.Text(c, h.MentionProblem).FontSize(TypeInput).TextColor(p.InkMuted).Padding(Space6, Space5)
				default:
					ui.Text(c, "No file matches").FontSize(TypeInput).TextColor(p.InkMuted).Padding(Space6, Space5)
				}
			})
		})
}

// mentionRow is one file. What the message will say is the path, so the row
// shows the name it is found by first and the folder it lives in second.
func mentionRow(c *ui.Context, state *ConversationState, h ConversationHost, p Palette, file MentionFile, active bool) {
	row := ui.Row(c).Key("mention:"+file.Path).FillWidth().Padding(Space2, Space5).AlignItems(ui.Center).Gap(Space4).
		OnClick(func() { acceptMention(state, h, file) })
	// Only the highlighted mention is lifted; this picker has no hover state.
	// It still shares the sidebar's one selection surface (T50).
	stateSurface(row, p, active, false)
	row.Children(func() {
		ui.Text(c, file.Name).FontSize(TypeInput).TextColor(p.Ink).SingleLine()
		if file.Dir != "" {
			ui.Text(c, file.Dir).FontSize(TypeLabel).TextColor(p.InkMuted).SingleLine()
		}
		ui.Box(c).Grow(1)
		if active {
			// The return mark sits on the row Enter would take, so the
			// highlighted row explains the key without a footer sentence.
			ui.Icon(c, IconReturn).Label("Insert selected file").FontSize(IconCompact).TextColor(p.Accent)
		}
	})
}
