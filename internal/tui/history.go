package tui

import tea "charm.land/bubbletea/v2"

// maxInputHistory bounds the sent lines kept for recall this run.
const maxInputHistory = 100

// rememberInput adds a sent line to the input history, collapsing an
// immediate repeat and dropping the oldest line past the bound. Sending
// a line also ends a walk through the history: the next recall starts
// from the empty input rather than from a stray draft.
func (m *model) rememberInput(text string) {
	m.histAt, m.draft = noSelection, ""
	if text == "" {
		return
	}
	if n := len(m.hist); n > 0 && m.hist[n-1] == text {
		return
	}
	m.hist = append(m.hist, text)
	if len(m.hist) > maxInputHistory {
		m.hist = append([]string(nil), m.hist[len(m.hist)-maxInputHistory:]...)
	}
}

// historyKey takes shift+↑/↓ for the input history. The arrows alone
// scroll the transcript, so recall lives on their shifted form; with
// nothing sent yet the keys are still taken, so they cannot start a
// text selection in the input.
func (m *model) historyKey(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "shift+up":
		m.historyBack()
	case "shift+down":
		m.historyForward()
	default:
		return false
	}
	return true
}

// historyBack moves one line further back. The first step keeps the
// input as the draft to return to.
func (m *model) historyBack() {
	if len(m.hist) == 0 {
		return
	}
	if m.histAt == noSelection {
		m.draft = m.ta.Value()
		m.histAt = 0
	} else if m.histAt < len(m.hist)-1 {
		m.histAt++
	}
	m.showHistory()
}

// historyForward moves one line towards the newest, and past it back to
// the draft.
func (m *model) historyForward() {
	if m.histAt == noSelection {
		return
	}
	m.histAt--
	m.showHistory()
}

// showHistory puts the line the walk stands on into the input, or the
// draft once the walk has passed the newest line.
func (m *model) showHistory() {
	var text string
	if m.histAt == noSelection {
		text = m.draft
		m.draft = ""
	} else {
		text = m.hist[len(m.hist)-1-m.histAt]
	}
	m.ta.SetValue(text)
	m.ta.CursorEnd()
	m.cmpl.sugg = nil
}
