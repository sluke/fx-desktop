package chrome

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// A tool call's payload is often JSON — the arguments of a running call, a
// search result, a command's report. Left verbatim it is one long line that
// wraps wherever the window ends, so the reader sees punctuation instead of
// structure. Here it is laid out as the design's data grid: the object's
// keys form a column, the values another, and braces, commas and indentation
// stay faint so the structure never competes with the data. The tones are
// the palette's own — colour is telemetry in this shell, not decoration.

// jsonSize matches the raw payload it replaces: a JSON block never changes
// scale inside the audit log.
const jsonSize = 11

// jsonLine is one rendered line of a payload.
//
//	key    the quoted key when the line opens one ("" otherwise)
//	value  a scalar, or a short array kept on one line
//	mark   a bracketing token ("{", "},", "]"), drawn faint
//	col    the rune column the value starts at, so nested keys align
type jsonLine struct {
	depth int
	key   string
	value string
	mark  string
	comma bool
	col   int
}

// formatJSON lays a JSON payload out and reports whether it is JSON at all.
// A payload that is still streaming formats too: an unbalanced object is
// what a running tool call looks like, and it must show what it has.
func formatJSON(text string) ([]jsonLine, bool) {
	s := strings.TrimSpace(text)
	if s == "" || (s[0] != '{' && s[0] != '[') || !jsonShaped(s) {
		return nil, false
	}
	p := jsonParser{s: s}
	p.run()
	p.flush()
	if len(p.lines) == 0 {
		return nil, false
	}
	if p.depth > 0 {
		// A call still running sends half an object. The faint ellipsis is
		// the same mark the payload uses when it stops at its line cap: it
		// says the data continues, instead of letting an unclosed brace
		// read as malformed output.
		p.lines = append(p.lines, jsonLine{depth: p.depth, mark: "…"})
	}
	return alignJSON(p.lines), true
}

// jsonShaped reports whether the text is JSON or a prefix of it. Valid JSON
// is the easy case; a running call sends the object before it is closed, so
// a quoted key followed by a colon is enough to read the rest as data.
func jsonShaped(s string) bool {
	if json.Valid([]byte(s)) {
		return true
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '"' {
			continue
		}
		_, next := scanJSONString(s, i)
		if j := skipJSONSpace(s, next); j < len(s) && s[j] == ':' {
			return true
		}
		i = next - 1
	}
	return false
}

type jsonParser struct {
	s     string
	i     int
	depth int
	lines []jsonLine
	cur   jsonLine
	open  bool
}

func (p *jsonParser) flush() {
	if p.open {
		p.lines = append(p.lines, p.cur)
	}
	p.cur = jsonLine{}
	p.open = false
}

func (p *jsonParser) run() {
	for p.i < len(p.s) {
		switch c := p.s[p.i]; {
		case c == '"':
			tok, next := scanJSONString(p.s, p.i)
			p.i = next
			if p.isKeyAhead() {
				// A key starts its own line; anything the line had
				// before it (a stray value) belongs above.
				p.flush()
				p.cur = jsonLine{depth: p.depth, key: tok}
			} else {
				if !p.open {
					p.cur = jsonLine{depth: p.depth}
				}
				p.cur.value = tok
			}
			p.open = true
		case c == '{' || c == '[':
			if inline, next, ok := inlineJSON(p.s, p.i); ok {
				if !p.open {
					p.cur = jsonLine{depth: p.depth}
				}
				p.cur.value = inline
				p.open = true
				p.i = next
				continue
			}
			if !p.open {
				p.cur = jsonLine{depth: p.depth}
			}
			p.cur.mark = string(c)
			p.open = true
			p.flush()
			p.depth++
			p.i++
		case c == '}' || c == ']':
			p.flush()
			if p.depth > 0 {
				p.depth--
			}
			line := jsonLine{depth: p.depth, mark: string(c)}
			p.i++
			if p.i < len(p.s) && p.s[p.i] == ',' {
				line.comma = true
				p.i++
			}
			p.lines = append(p.lines, line)
		case c == ':':
			p.i++
		case c == ',':
			if p.open {
				p.cur.comma = true
				p.flush()
			}
			p.i++
		case c == ' ' || c == '\n' || c == '\t' || c == '\r':
			p.i++
		default:
			j := p.i
			for j < len(p.s) && !jsonDelim(p.s[j]) {
				j++
			}
			if j == p.i {
				j++
			}
			if !p.open {
				p.cur = jsonLine{depth: p.depth}
			}
			p.cur.value = p.s[p.i:j]
			p.open = true
			p.i = j
		}
	}
}

func (p *jsonParser) isKeyAhead() bool {
	j := skipJSONSpace(p.s, p.i)
	return j < len(p.s) && p.s[j] == ':'
}

// scanJSONString returns the quoted token starting at i and the index just
// past it. An unterminated string — a payload still streaming — runs to the
// end: a running call must still show what it has.
func scanJSONString(s string, i int) (string, int) {
	j := i + 1
	for j < len(s) {
		switch s[j] {
		case '\\':
			j += 2
		case '"':
			return s[i : j+1], j + 1
		default:
			j++
		}
	}
	return s[i:], len(s)
}

// inlineJSON keeps an empty pair or a short list of scalars on one line.
// A list of strings that each get their own line pushes the rest of the
// object out of sight; `[1, 2, 3]` reads as one value.
func inlineJSON(s string, i int) (string, int, bool) {
	open := s[i]
	closer := byte('}')
	if open == '[' {
		closer = ']'
	}
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '"':
			_, next := scanJSONString(s, j)
			j = next - 1
		case '{', '[':
			return "", 0, false
		case closer:
			inner := strings.TrimSpace(s[i+1 : j])
			if inner == "" {
				return string([]byte{open, closer}), j + 1, true
			}
			if open != '[' {
				return "", 0, false
			}
			parts := strings.Split(inner, ",")
			for k, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" || strings.ContainsAny(part, "{}[]") {
					return "", 0, false
				}
				parts[k] = part
			}
			list := "[" + strings.Join(parts, ", ") + "]"
			if utf8.RuneCountInString(list) > 44 {
				return "", 0, false
			}
			return list, j + 1, true
		}
	}
	return "", 0, false
}

func jsonDelim(c byte) bool {
	switch c {
	case '{', '}', '[', ']', ',', ':', ' ', '\n', '\t', '\r':
		return true
	}
	return false
}

func skipJSONSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\n' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	return i
}

// alignJSON gives every keyed line at one depth the same value column, within
// a gutter that stays compact: one unusually long key must not push every
// value halfway across the block.
func alignJSON(lines []jsonLine) []jsonLine {
	const maxPad = 12
	start := map[int]int{}
	worst := map[int]int{}
	for _, l := range lines {
		if l.key == "" {
			continue
		}
		base := jsonIndent(l.depth) + utf8.RuneCountInString(l.key) + 2
		if d, ok := start[l.depth]; !ok || base < d {
			start[l.depth] = base
		}
		if base > worst[l.depth] {
			worst[l.depth] = base
		}
	}
	col := map[int]int{}
	for depth, w := range worst {
		if limit := start[depth] + maxPad; w > limit {
			w = limit
		}
		col[depth] = w
	}
	for i := range lines {
		if lines[i].key != "" {
			lines[i].col = col[lines[i].depth]
		}
	}
	return lines
}

func jsonIndent(depth int) int {
	if depth < 0 {
		depth = 0
	}
	return 2 * depth
}

// jsonText draws formatted lines as one selectable block, so the whole
// payload can still be copied the way the raw text could. Indentation and
// alignment ride on the glyph span that follows them: a text layout trims a
// run that holds nothing but spaces, and the JSON would lose its shape.
func jsonText(c *ui.Context, lines []jsonLine, p Palette) ui.Element {
	spans := make([]ui.Span, 0, len(lines)*5)
	pending := ""
	add := func(s string, col ui.Color, weight int) {
		if s == "" {
			return
		}
		spans = append(spans, ui.Span{Text: pending + s, Color: col, Weight: weight})
		pending = ""
	}
	space := func(n int) {
		if n > 0 {
			pending += strings.Repeat(" ", n)
		}
	}
	for i, l := range lines {
		if i > 0 {
			pending += "\n"
		}
		space(jsonIndent(l.depth))
		if l.key != "" {
			add(l.key, p.InkMuted, 500)
			pending += ": "
			// An opening bracket starts the block it names, so it stays
			// beside its key the way JSON is written; only values take
			// the column.
			if l.mark != "{" && l.mark != "[" {
				space(l.col - (jsonIndent(l.depth) + utf8.RuneCountInString(l.key) + 2))
			}
		}
		add(l.value, p.Ink, 0)
		add(l.mark, p.InkFaint, 0)
		if l.comma {
			add(",", p.InkFaint, 0)
		}
	}
	if len(spans) == 0 {
		spans = append(spans, ui.Span{Text: " "})
	}
	return ui.RichText(c, spans...).Font(MonoFont).FontSize(jsonSize).Selectable()
}

// capJSONLines keeps the head of a payload and reports what it left out, the
// same rule the verbatim payload follows.
func capJSONLines(lines []jsonLine, max int) ([]jsonLine, int) {
	if max <= 0 || len(lines) <= max {
		return lines, 0
	}
	return lines[:max], len(lines) - max
}

// payloadCache is one expanded tool call's laid-out payload. The transcript
// is rebuilt every frame and a payload can be a whole session's JSON, so the
// structure is computed once per text, not once per frame.
type payloadCache struct {
	text  string
	lines []jsonLine
	ok    bool
}

// payloadLines returns a message's payload as data when it is JSON. A
// message whose text has not changed reuses its layout; the emptiness check
// keeps a growing stream honest.
func payloadLines(state *ConversationState, m ChatMessage) ([]jsonLine, bool) {
	if cache, hit := state.Payloads[m.ID]; hit && cache.text == m.Text {
		return cache.lines, cache.ok
	}
	lines, ok := formatJSON(m.Text)
	if state.Payloads == nil {
		state.Payloads = map[uint64]payloadCache{}
	}
	state.Payloads[m.ID] = payloadCache{text: m.Text, lines: lines, ok: ok}
	return lines, ok
}
