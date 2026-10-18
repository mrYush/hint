package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mrYush/hint/internal/rpc"
)

// picker is the session list: what `hint -r` asks on the line reader,
// drawn as a tree the arrows move through — a fork sits under the session
// it was cut from.
type picker struct {
	rows    []pickerRow
	cursor  int
	loading bool
	err     string
}

// pickerRow is one line of the tree: the session and how deep it sits.
type pickerRow struct {
	info  rpc.Info
	depth int
}

// tree orders infos as a forest: the sessions with no parent in the list
// in the order given (most recent first), each followed by its forks,
// recursively. A fork whose parent is gone is a root that still names
// it.
func tree(infos []rpc.Info) []pickerRow {
	present := map[string]bool{}
	for _, info := range infos {
		present[info.ID] = true
	}
	children := map[string][]rpc.Info{}
	var roots []rpc.Info
	for _, info := range infos {
		if info.Parent != nil && present[info.Parent.ID] && info.Parent.ID != info.ID {
			children[info.Parent.ID] = append(children[info.Parent.ID], info)
			continue
		}
		roots = append(roots, info)
	}
	var rows []pickerRow
	var walk func(info rpc.Info, depth int)
	walk = func(info rpc.Info, depth int) {
		rows = append(rows, pickerRow{info: info, depth: depth})
		for _, child := range children[info.ID] {
			walk(child, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return rows
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
	switch keyName(k) {
	case "ctrl+c", "ctrl+d":
		return m.quit()
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.rows)-1 {
			p.cursor++
		}
	case "enter":
		if p.loading || len(p.rows) == 0 {
			return m, nil
		}
		return m.attach(p.rows[p.cursor].info.ID)
	case "f":
		// A branch off the whole session under the cursor: the original
		// stays as it is, the shell moves to the copy.
		if p.loading || len(p.rows) == 0 {
			return m, nil
		}
		return m.fork(p.rows[p.cursor].info.ID, 0)
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

// fork asks the core for a new session cut from id after turn (0 for the
// whole conversation) and attaches the shell to it.
func (m model) fork(id string, turn int) (model, tea.Cmd) {
	if m.picker != nil {
		m.picker.loading = true
	}
	cl, ctx := m.cl, m.ctx
	return m, func() tea.Msg {
		view, err := cl.ForkSession(ctx, rpc.ForkOptions{ID: id, Turn: turn})
		return attachedMsg{view: view, err: err, forked: true}
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
	m.picker.rows = tree(msg.infos)
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
	m.profile = rpc.ProfileRef{Profile: msg.view.Profile, Model: msg.view.Model}
	m.entries = fromHistory(msg.view.Messages)
	m.turns, m.sel = turnsOf(m.entries), noSelection
	if msg.forked && msg.view.Parent != nil {
		m.entries = append(m.entries, entry{kind: entryNotice, turn: m.turns,
			text: fmt.Sprintf("forked from session %s after turn %d; this is session %s", short(msg.view.Parent.ID), msg.view.Parent.Turn, short(msg.view.ID)), done: true})
	}
	for _, w := range msg.view.Warnings {
		m.entries = append(m.entries, entry{kind: entryNotice, turn: m.turns, text: w, done: true})
	}
	m.rerender()
	m.vp.GotoBottom()
	return m
}

// view draws the list in a w×h box.
func (p *picker) view(w, h int, sty styles) string {
	var b strings.Builder
	b.WriteString(rule(sty.header.Render("sessions"), w, sty) + "\n\n")
	switch {
	case p.loading:
		b.WriteString(sty.hint.Render("loading…"))
	case p.err != "":
		b.WriteString(sty.toolErr.Render(p.err))
	case len(p.rows) == 0:
		b.WriteString(sty.hint.Render("no recorded sessions in this directory; esc starts a new one"))
	default:
		for i, r := range p.rows {
			cursor := "  "
			if i == p.cursor {
				cursor = sty.pickerCursor.Render("▸ ")
			}
			info := r.info
			indent := strings.Repeat("  ", r.depth)
			if r.depth > 0 {
				indent = strings.Repeat("  ", r.depth-1) + "└ "
			}
			row := fmt.Sprintf("%s%s  %s  %3d msgs  %s", indent, short(info.ID), info.Modified.Local().Format("2006-01-02 15:04"), info.Messages, info.FirstPrompt)
			if info.Parent != nil {
				row += fmt.Sprintf("  ↳ %s@%d", short(info.Parent.ID), info.Parent.Turn)
			}
			if i != p.cursor {
				row = sty.pickerDim.Render(row)
			}
			b.WriteString(cursor + wrapLine(row, w-2) + "\n")
		}
	}
	b.WriteString("\n" + sty.hint.Render("enter resume · f fork · esc back · ↑/↓ move"))
	return lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, b.String())
}

// wrapLine keeps a row on one line.
func wrapLine(s string, w int) string {
	if w <= 0 {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}
