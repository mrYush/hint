package tui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mrYush/hint/internal/console"
)

// noSelection is the value of model.sel outside the message browser.
const noSelection = -1

// scrollKey moves the transcript for the keys that belong to it while the
// input has the focus, and reports whether it took the key. The arrows
// are shared: on the input's first line ↑ scrolls, on its last ↓ does, so
// a one-line question loses nothing — and a mouse wheel, which a terminal
// without mouse reporting sends as arrows, scrolls the transcript.
func (m *model) scrollKey(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "shift+up":
		m.vp.ScrollUp(1)
	case "shift+down":
		m.vp.ScrollDown(1)
	case "up":
		if m.ta.Line() != 0 {
			return false
		}
		m.vp.ScrollUp(1)
	case "down":
		if m.ta.Line() != m.ta.LineCount()-1 {
			return false
		}
		m.vp.ScrollDown(1)
	case "pgup":
		m.vp.ScrollUp(m.vp.Height())
	case "pgdown":
		m.vp.ScrollDown(m.vp.Height())
	case "ctrl+home":
		m.vp.GotoTop()
	case "ctrl+end":
		m.vp.GotoBottom()
	default:
		return false
	}
	return true
}

// startBrowse selects the newest block of the transcript; from there the
// arrows move between blocks instead of lines.
func (m *model) startBrowse() bool {
	if len(m.entries) == 0 {
		return false
	}
	m.selectEntry(len(m.entries) - 1)
	return true
}

// browseKey handles a key while a block is selected: move, copy, fork,
// or leave. Keys it does not know are ignored rather than typed, since
// the input is not focused.
func (m model) browseKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch keyName(k) {
	case "up", "k", "shift+tab":
		m.selectEntry(max(m.sel-1, 0))
	case "down", "j", "tab":
		m.selectEntry(min(m.sel+1, len(m.entries)-1))
	case "g", "home", "ctrl+home":
		m.selectEntry(0)
	case "G", "end", "ctrl+end":
		m.selectEntry(len(m.entries) - 1)
	case "pgup":
		m.vp.ScrollUp(m.vp.Height())
	case "pgdown":
		m.vp.ScrollDown(m.vp.Height())
	case "y", "enter":
		e := m.entries[m.sel]
		if e.text == "" {
			return m, nil
		}
		m.status = copied("block", e.text)
		return m, copyCmd(e.text)
	case "f":
		// A new session holding the turns up to and including this one;
		// the shell moves to it.
		turn := m.entries[m.sel].turn
		m.endBrowse()
		return m.fork(m.sessionID, turn)
	case "esc", "q", "ctrl+c", "i":
		m.endBrowse()
	case "ctrl+d":
		return m.quit()
	}
	return m, nil
}

// selectEntry moves the selection to i, redrawing the two blocks it
// touches and bringing i into view.
func (m *model) selectEntry(i int) {
	prev := m.sel
	m.sel = i
	if prev >= 0 && prev < len(m.entries) {
		m.rendered[prev] = m.draw(prev)
	}
	m.rendered[i] = m.draw(i)
	m.setContent()
	m.reveal(i)
}

// endBrowse gives the focus back to the input and the view to the end
// of the conversation.
func (m *model) endBrowse() {
	prev := m.sel
	m.sel = noSelection
	if prev >= 0 && prev < len(m.entries) {
		m.rendered[prev] = m.draw(prev)
		m.setContent()
	}
	m.vp.GotoBottom()
}

// reveal scrolls the least that shows entry i: its top when it is taller
// than the view, otherwise all of it.
func (m *model) reveal(i int) {
	top := m.entryTop(i)
	h := lipgloss.Height(m.rendered[i])
	switch {
	case top < m.vp.YOffset() || h > m.vp.Height():
		m.vp.SetYOffset(top)
	case top+h > m.vp.YOffset()+m.vp.Height():
		m.vp.SetYOffset(top + h - m.vp.Height())
	}
}

// entryTop is the first transcript line of entry i. Entries are joined by
// one blank line, as setContent joins them.
func (m model) entryTop(i int) int {
	top := 0
	for _, r := range m.rendered[:i] {
		top += lipgloss.Height(r) + 1
	}
	return top
}

// entryAt maps a screen row to the transcript entry drawn there, or -1.
func (m model) entryAt(row int) int {
	row -= headerLines
	if row < 0 || row >= m.vp.Height() {
		return -1
	}
	line := m.vp.YOffset() + row
	start := 0
	for i, r := range m.rendered {
		h := lipgloss.Height(r)
		if line >= start && line < start+h {
			return i
		}
		start += h + 1
	}
	return -1
}

// below is how many transcript lines are under the view.
func (m model) below() int {
	return max(m.vp.TotalLineCount()-m.vp.YOffset()-m.vp.Height(), 0)
}

// selectedGutter is the mark of the selected block: a bar down its left
// side, which takes two columns from the block's width.
const selectedGutter = 2

// draw renders entry i at the transcript's width, marked when selected.
func (m *model) draw(i int) string {
	w := m.vp.Width()
	if i != m.sel {
		return m.renderEntry(m.entries[i], w)
	}
	body := m.renderEntry(m.entries[i], max(w-selectedGutter, 1))
	return m.sty.selected.Render(body)
}

// keyName is the name of a pressed key for the one-letter commands of a
// dialog, the session list and the message browser, whatever the layout:
// "н" pressed on the key of "y" in a Cyrillic layout is "y". A terminal
// that speaks the kitty keyboard protocol says which US key it was
// (BaseCode), which covers every layout; without it a table of the
// Cyrillic layouts does. Named keys and chords are left as they are, and
// text typed into the input never goes through here.
func keyName(k tea.KeyPressMsg) string {
	s := k.String()
	r := []rune(s)
	if len(r) != 1 {
		return s
	}
	if q := console.QWERTYRune(r[0]); q != r[0] {
		return string(q)
	}
	if b := k.BaseCode; b > 0 && b < unicode.MaxASCII && r[0] > unicode.MaxASCII {
		if unicode.IsUpper(r[0]) {
			b = unicode.ToUpper(b)
		}
		return string(b)
	}
	return s
}
