package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/shell"
	"github.com/mrYush/hint/pkg/agentapi"
)

// Layout, in lines: the header, the status line and the input box are
// fixed; the transcript takes the rest.
const (
	headerLines = 1
	statusLines = 1
	inputLines  = 3
)

const help = "enter sends · ctrl+j newline · pgup/pgdn scroll · /sessions · /help · ctrl+d quits"

// model is the whole shell: the connection, the transcript and the three
// widgets. It is a value, as Bubble Tea wants; the pointers inside
// (dialog, picker, renderer) are the state that outlives one Update.
type model struct {
	ctx           context.Context
	cl            *rpc.Client
	send          sender
	sessionID     string
	recorded      bool
	serverVersion string
	dark          bool
	sty           styles

	width, height int
	vp            viewport.Model
	ta            textarea.Model
	spin          spinner.Model
	md            *glamour.TermRenderer
	mdWidth       int

	// entries is the transcript; rendered caches each entry's drawing at
	// the current width.
	entries  []entry
	rendered []string

	// busy is a turn in flight; cancelTurn ends it.
	busy       bool
	cancelTurn context.CancelFunc

	dialog *dialog
	picker *picker
	// status is a one-line remark under the transcript until the next key.
	status   string
	quitting bool
}

func newModel(ctx context.Context, opts Options, dark bool, send sender) model {
	ta := textarea.New()
	ta.Placeholder = "ask hint"
	ta.Prompt = "› "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetVirtualCursor(false)
	ta.SetHeight(inputLines)
	// Enter sends; a newline inside the question is Ctrl-J, so the input
	// never swallows the key that every chat uses to submit.
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j"))
	ta.Focus()
	sty := newStyles(dark)
	// The input is set apart by the rule above it and its prompt, not by
	// a filled cursor line.
	tas := ta.Styles()
	tas.Focused.CursorLine = lipgloss.NewStyle()
	tas.Blurred.CursorLine = lipgloss.NewStyle()
	tas.Focused.Prompt = sty.you
	tas.Blurred.Prompt = sty.hint
	ta.SetStyles(tas)

	vp := viewport.New()
	// The transcript scrolls by page; the arrows belong to the input.
	for _, b := range []*key.Binding{&vp.KeyMap.Up, &vp.KeyMap.Down, &vp.KeyMap.Left, &vp.KeyMap.Right, &vp.KeyMap.HalfPageUp, &vp.KeyMap.HalfPageDown} {
		b.SetEnabled(false)
	}

	m := model{
		ctx:           ctx,
		cl:            opts.Client,
		send:          send,
		sessionID:     opts.SessionID,
		recorded:      opts.Recorded,
		serverVersion: opts.ServerVersion,
		dark:          dark,
		sty:           sty,
		vp:            vp,
		ta:            ta,
		spin:          spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(sty.status)),
		entries:       fromHistory(opts.History),
	}
	if opts.PickSession {
		m.picker = &picker{loading: true}
	}
	return m
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink}
	if m.picker != nil {
		cl, ctx := m.cl, m.ctx
		cmds = append(cmds, func() tea.Msg {
			infos, err := cl.ListSessions(ctx)
			return sessionsMsg{infos: infos, err: err}
		})
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.MouseWheelMsg:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case spinner.TickMsg:
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case cursor.BlinkMsg:
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd

	case textDeltaMsg:
		m.appendText(string(msg))
		return m, nil
	case toolStartMsg:
		m.add(entry{kind: entryTool, name: msg.Name, text: shell.SummarizeArgs(msg.Arguments), done: true})
		return m, nil
	case toolEndMsg:
		switch {
		case msg.IsError:
			m.add(entry{kind: entryTool, name: msg.Name, text: shell.FirstLine(agentapi.ToolResult(msg).Text()), failed: true, done: true})
		case msg.Name == "todo":
			// The plan is for the user as much as for the model.
			m.add(entry{kind: entryNotice, text: agentapi.ToolResult(msg).Text(), done: true})
		}
		return m, nil
	case noticeMsg:
		m.add(entry{kind: entryNotice, text: string(msg), done: true})
		return m, nil
	case permissionMsg:
		m.dialog = newDialog(msg.req, msg.reply, m.width, m.bodyHeight(), m.sty)
		return m, nil
	case turnDoneMsg:
		m.finishTurn(msg)
		return m, nil

	case sessionsMsg:
		return m.sessionsLoaded(msg), nil
	case attachedMsg:
		return m.attached(msg), nil

	case tea.PasteMsg:
		// Bracketed paste: the terminal hands pasted text over as one
		// message, not as key presses, so it is not a question until the
		// user presses enter. A list or a dialog has nothing to paste into.
		if m.picker != nil || m.dialog != nil {
			return m, nil
		}
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd
	}
	// Whatever else arrives is for the input: the clipboard read behind
	// its ctrl+v comes back as a message of its own, for instance.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

// key routes a key press: to the list or the dialog while one is open,
// otherwise to the shell's own bindings and then to the input.
func (m model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.status = ""
	if m.picker != nil {
		return m.pickerKey(k)
	}
	if m.dialog != nil {
		if m.dialog.key(k) {
			m.dialog = nil
		}
		return m, nil
	}
	switch k.String() {
	case "ctrl+c":
		if m.busy {
			m.cancelTurn()
			m.status = "stopping the answer…"
			return m, nil
		}
		return m.quit()
	case "ctrl+d":
		return m.quit()
	case "enter":
		return m.submit()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(k)
		return m, cmd
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(k)
	return m, cmd
}

// quit ends the shell; a running turn is cancelled on the way out.
func (m model) quit() (model, tea.Cmd) {
	if m.busy {
		m.cancelTurn()
	}
	m.quitting = true
	return m, tea.Quit
}

// submit takes the input as a command or a question.
func (m model) submit() (model, tea.Cmd) {
	text := strings.TrimSpace(m.ta.Value())
	if text == "" {
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
		m.ta.Reset()
		switch cmd := strings.Fields(text)[0]; cmd {
		case "/exit", "/quit":
			return m.quit()
		case "/help":
			m.add(entry{kind: entryNotice, text: help, done: true})
		case "/sessions":
			if m.busy {
				m.status = "wait for the answer before switching sessions"
				return m, nil
			}
			return m.openPicker()
		default:
			m.status = fmt.Sprintf("unknown command %s; /help lists them", cmd)
		}
		return m, nil
	}
	if m.busy {
		m.status = "an answer is being written; Ctrl-C stops it"
		return m, nil
	}
	if m.sessionID == "" {
		m.status = "no session yet"
		return m, nil
	}
	m.ta.Reset()
	m.add(entry{kind: entryUser, text: text, done: true})
	m.add(entry{kind: entryAssistant})
	m.busy = true
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelTurn = cancel
	go runTurn(ctx, m.cl, m.sessionID, text, m.send)
	return m, m.spin.Tick
}

// finishTurn closes the streaming entry and renders it as markdown; a
// turn that produced nothing leaves no empty block behind.
func (m *model) finishTurn(msg turnDoneMsg) {
	m.busy = false
	if m.cancelTurn != nil {
		m.cancelTurn()
		m.cancelTurn = nil
	}
	m.dialog = nil
	if n := len(m.entries); n > 0 && m.entries[n-1].kind == entryAssistant && !m.entries[n-1].done {
		if m.entries[n-1].text == "" {
			m.entries = m.entries[:n-1]
			m.rendered = m.rendered[:n-1]
		} else {
			m.entries[n-1].done = true
			m.rendered[n-1] = m.renderEntry(m.entries[n-1], m.vp.Width())
		}
	}
	switch {
	case errors.Is(msg.err, shell.ErrInterrupted):
		m.add(entry{kind: entryNotice, text: "interrupted", done: true})
	case msg.err != nil:
		m.add(entry{kind: entryNotice, text: msg.err.Error(), done: true})
	default:
		m.setContent()
	}
}

// appendText grows the streaming answer, or starts one when the last
// entry is not an open answer.
func (m *model) appendText(delta string) {
	n := len(m.entries)
	if n == 0 || m.entries[n-1].kind != entryAssistant || m.entries[n-1].done {
		m.add(entry{kind: entryAssistant, text: delta})
		return
	}
	m.entries[n-1].text += delta
	m.rendered[n-1] = m.renderEntry(m.entries[n-1], m.vp.Width())
	m.setContent()
}

// add appends an entry and draws it.
func (m *model) add(e entry) {
	m.entries = append(m.entries, e)
	m.rendered = append(m.rendered, m.renderEntry(e, m.vp.Width()))
	m.setContent()
}

// rerender redraws every entry, after a resize or a new history.
func (m *model) rerender() {
	m.rendered = make([]string, len(m.entries))
	for i, e := range m.entries {
		m.rendered[i] = m.renderEntry(e, m.vp.Width())
	}
	m.setContent()
}

// setContent hands the transcript to the viewport and keeps the view at
// the bottom while the user has not scrolled away from it.
func (m *model) setContent() {
	follow := m.vp.AtBottom()
	m.vp.SetContent(strings.Join(m.rendered, "\n\n"))
	if follow {
		m.vp.GotoBottom()
	}
}

func (m *model) resize(w, h int) {
	m.width, m.height = w, h
	m.ta.SetWidth(w)
	m.vp.SetWidth(w)
	m.vp.SetHeight(m.bodyHeight())
	m.md = nil
	m.rerender()
	if m.dialog != nil {
		m.dialog = newDialog(m.dialog.req, m.dialog.reply, w, m.bodyHeight(), m.sty)
	}
}

// bodyHeight is what the transcript, the dialog or the list may use.
func (m model) bodyHeight() int {
	return max(m.height-headerLines-statusLines-inputLines-1, 1)
}

func (m model) View() tea.View {
	if m.quitting || m.width == 0 {
		return tea.NewView("")
	}
	var body string
	switch {
	case m.picker != nil:
		body = m.picker.view(m.width, m.bodyHeight(), m.sty)
	case m.dialog != nil:
		body = m.dialog.view(m.width, m.bodyHeight(), m.sty)
	default:
		body = m.vp.View()
	}
	content := strings.Join([]string{m.header(), body, m.statusLine(), m.ta.View()}, "\n")
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	if m.picker == nil && m.dialog == nil {
		if c := m.ta.Cursor(); c != nil {
			c.Y += headerLines + m.bodyHeight() + statusLines
			v.Cursor = c
		}
	}
	return v
}

func (m model) header() string {
	session := "no session"
	if m.sessionID != "" {
		session = "session " + short(m.sessionID)
		if !m.recorded {
			session += " (not recorded)"
		}
	}
	meta := " · " + session
	if m.serverVersion != "" {
		meta += " · core " + m.serverVersion
	}
	return rule(m.sty.header.Render("hint")+m.sty.hint.Render(meta), m.width, m.sty)
}

// statusLine is the rule between the transcript and the input, carrying
// what the user should know right now.
func (m model) statusLine() string {
	var text string
	switch {
	case m.status != "":
		text = m.sty.status.Render(m.status)
	case m.busy:
		text = m.spin.View() + m.sty.status.Render(" thinking… ctrl+c stops")
	default:
		text = m.sty.hint.Render(help)
	}
	return rule(text, m.width, m.sty)
}
