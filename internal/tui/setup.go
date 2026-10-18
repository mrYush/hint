package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Setup is what the form needs from the process: the words describing
// the configuration in use, the values to start from, and the function
// that writes them. Save nil means there is nothing to write here — a
// core in another process keeps its configuration on its own machine.
type Setup struct {
	Describe string
	Profile  string
	BaseURL  string
	Model    string
	// Save writes the answers and returns what the user should be told.
	// key is a literal or a ${VAR} reference; empty keeps the one in the
	// file.
	Save func(profile, baseURL, model, key string) (string, error)
}

// setupDoneMsg is the answer of Save.
type setupDoneMsg struct {
	msg string
	err error
}

const (
	fieldProfile = iota
	fieldBaseURL
	fieldModel
	fieldKey
	fieldCount
)

// setupForm is the provider configuration as four inputs; the key is
// masked as it is typed.
type setupForm struct {
	in    [fieldCount]textinput.Model
	focus int
	err   string
	// saving is set between Save being asked and its answer.
	saving bool
}

var fieldLabels = [fieldCount]string{"profile", "base URL", "model", "API key"}

func newSetupForm(s *Setup) *setupForm {
	f := &setupForm{}
	values := [fieldCount]string{s.Profile, s.BaseURL, s.Model, ""}
	for i := range f.in {
		in := textinput.New()
		in.Prompt = ""
		in.SetValue(values[i])
		in.SetVirtualCursor(true)
		if i == fieldKey {
			in.EchoMode = textinput.EchoPassword
			in.Placeholder = "paste the key, or ${VARIABLE}; empty keeps the current one"
		}
		f.in[i] = in
	}
	f.in[0].Focus()
	return f
}

// move changes the focused field.
func (f *setupForm) move(delta int) tea.Cmd {
	f.in[f.focus].Blur()
	f.focus = (f.focus + delta + fieldCount) % fieldCount
	return f.in[f.focus].Focus()
}

// openSetup shows the form.
func (m model) openSetup() (model, tea.Cmd) {
	m.setupForm = newSetupForm(m.setup)
	return m, m.setupForm.in[0].Focus()
}

// setupKey handles a key while the form is open.
func (m model) setupKey(k tea.KeyPressMsg) (model, tea.Cmd) {
	f := m.setupForm
	if f.saving {
		return m, nil
	}
	switch k.String() {
	case "esc", "ctrl+c":
		m.setupForm = nil
		return m, nil
	case "tab", "down":
		return m, f.move(1)
	case "shift+tab", "up":
		return m, f.move(-1)
	case "enter":
		if f.focus < fieldCount-1 {
			return m, f.move(1)
		}
		f.saving, f.err = true, ""
		save := m.setup.Save
		profile, baseURL, model, key := f.in[0].Value(), f.in[1].Value(), f.in[2].Value(), f.in[3].Value()
		return m, func() tea.Msg {
			msg, err := save(strings.TrimSpace(profile), strings.TrimSpace(baseURL), strings.TrimSpace(model), strings.TrimSpace(key))
			return setupDoneMsg{msg: msg, err: err}
		}
	}
	var cmd tea.Cmd
	f.in[f.focus], cmd = f.in[f.focus].Update(k)
	return m, cmd
}

// setupSaved closes the form on success and keeps it open, with the
// reason, on failure.
func (m model) setupSaved(msg setupDoneMsg) model {
	if m.setupForm == nil {
		return m
	}
	if msg.err != nil {
		m.setupForm.saving = false
		m.setupForm.err = msg.err.Error()
		return m
	}
	m.setupForm = nil
	m.add(entry{kind: entryNotice, text: msg.msg, done: true})
	return m
}

// view draws the form in a w×h box.
func (f *setupForm) view(describe string, w, h int, sty styles) string {
	var b strings.Builder
	b.WriteString(rule(sty.header.Render("configuration"), w, sty) + "\n")
	b.WriteString(sty.hint.Render(wrap(describe, w)) + "\n\n")
	for i := range f.in {
		label := sty.hint.Render(padRight(fieldLabels[i], 9))
		if i == f.focus {
			label = sty.you.Render(padRight(fieldLabels[i], 9))
		}
		b.WriteString(label + f.in[i].View() + "\n")
	}
	switch {
	case f.saving:
		b.WriteString("\n" + sty.hint.Render("saving…"))
	case f.err != "":
		b.WriteString("\n" + sty.toolErr.Render(f.err))
	default:
		b.WriteString("\n" + sty.hint.Render("tab/enter next field · enter on the key saves · esc cancels"))
	}
	return lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, b.String())
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
