package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mrYush/hint/internal/rpc"
)

// picker is the session list: what `hint -r` asks on the line reader,
// drawn as a list the arrows move through.
type picker struct {
	infos   []rpc.Info
	cursor  int
	loading bool
	err     string
}

// openPicker shows the list and asks the server for its rows.
func (m model) openPicker() (model, tea.Cmd) {
	m.picker = &picker{loading: true}
	cl, ctx := m.cl, m.ctx
	return m, func() tea.Msg {
		infos, err := cl.ListSessions(ctx)
		return sessionsMsg{infos: infos, err: err}
	}
}

// pickerKey handles a key while the list is open. Enter attaches the
// row under the cursor; Esc leaves the list — back to the current
// session, or into a fresh one when the shell has none yet.
func (m model) pickerKey(k tea.KeyPressMsg) (model, tea.Cmd) {
	p := m.picker
	switch k.String() {
	case "ctrl+c", "ctrl+d":
		return m.quit()
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.infos)-1 {
			p.cursor++
		}
	case "enter":
		if p.loading || len(p.infos) == 0 {
			return m, nil
		}
		return m.attach(p.infos[p.cursor].ID)
	case "esc":
		if m.sessionID != "" {
			m.picker = nil
			return m, nil
		}
		return m.openNew()
	}
	return m, nil
}

// attach asks the server for an existing session.
func (m model) attach(id string) (model, tea.Cmd) {
	m.picker.loading = true
	cl, ctx := m.cl, m.ctx
	return m, func() tea.Msg {
		view, err := cl.AttachSession(ctx, id)
		return attachedMsg{view: view, err: err}
	}
}

// openNew starts a fresh recorded session, the fallback of a picker with
// nothing to pick.
func (m model) openNew() (model, tea.Cmd) {
	if m.picker != nil {
		m.picker.loading = true
	}
	cl, ctx := m.cl, m.ctx
	return m, func() tea.Msg {
		view, err := cl.OpenSession(ctx, rpc.OpenOptions{})
		return attachedMsg{view: view, err: err}
	}
}

// sessionsLoaded fills the list from the server's answer.
func (m model) sessionsLoaded(msg sessionsMsg) model {
	if m.picker == nil {
		return m
	}
	m.picker.loading = false
	if msg.err != nil {
		m.picker.err = msg.err.Error()
		return m
	}
	m.picker.infos = msg.infos
	m.picker.cursor = 0
	return m
}

// attached switches the shell to the session the server returned and
// shows its history.
func (m model) attached(msg attachedMsg) model {
	if msg.err != nil {
		if m.picker != nil {
			m.picker.loading = false
			m.picker.err = msg.err.Error()
		} else {
			m.status = msg.err.Error()
		}
		return m
	}
	m.picker = nil
	m.sessionID = msg.view.ID
	m.recorded = true
	m.entries = fromHistory(msg.view.Messages)
	for _, w := range msg.view.Warnings {
		m.entries = append(m.entries, entry{kind: entryNotice, text: w, done: true})
	}
	m.rerender()
	m.vp.GotoBottom()
	return m
}

// view draws the list in a w×h box.
func (p *picker) view(w, h int, sty styles) string {
	var b strings.Builder
	b.WriteString(sty.header.Render("sessions") + "\n\n")
	switch {
	case p.loading:
		b.WriteString(sty.hint.Render("loading…"))
	case p.err != "":
		b.WriteString(sty.toolErr.Render(p.err))
	case len(p.infos) == 0:
		b.WriteString(sty.hint.Render("no recorded sessions in this directory; esc starts a new one"))
	default:
		for i, info := range p.infos {
			cursor := "  "
			if i == p.cursor {
				cursor = sty.pickerCursor.Render("▸ ")
			}
			row := fmt.Sprintf("%s  %s  %3d msgs  %s", short(info.ID), info.Modified.Local().Format("2006-01-02 15:04"), info.Messages, info.FirstPrompt)
			if i != p.cursor {
				row = sty.pickerDim.Render(row)
			}
			b.WriteString(cursor + wrapLine(row, w-2) + "\n")
		}
	}
	b.WriteString("\n" + sty.hint.Render("enter resume · esc back · ↑/↓ move"))
	return lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, b.String())
}

// wrapLine keeps a row on one line.
func wrapLine(s string, w int) string {
	if w <= 0 {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}
