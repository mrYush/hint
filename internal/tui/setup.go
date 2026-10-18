package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/rpc"
)

// Setup is what the shell may do with the configuration of the core it
// runs. The fields are filled by the process that owns that core; with a
// core in another process only Describe is set, since its configuration
// lives on that machine.
type Setup struct {
	// Describe says which files are in use and where setup writes.
	Describe string
	// Profile is the profile in use, Values its fields as the form starts
	// with them, keyed by field name (kind, base_url, …).
	Profile string
	Values  map[string]string
	// Save writes the form: the profile's name and its non-empty fields.
	// It returns what the user should be told.
	Save func(profile string, values map[string]string) (string, error)
	// Set and Unset change one key of the global file, or with project of
	// the repository's .hint/config.yaml.
	Set   func(key, value string, project bool) (string, error)
	Unset func(key string, project bool) (string, error)
	// Show is the configuration the next start resolves, keys masked.
	Show func() (string, error)
}

// setupDoneMsg is the answer of Save, with the profile it wrote.
type setupDoneMsg struct {
	profile string
	msg     string
	err     error
}

// formField is one input of the setup form: the profile name, or a field
// of the profile as the config schema describes it.
type formField struct {
	label string
	// key is the schema entry; zero for the name.
	key config.Key
	in  textinput.Model
}

// setupForm is a provider profile as a column of inputs, built from the
// schema so that a field added to the config is a field of the form. The
// key is masked as it is typed.
type setupForm struct {
	fields []formField
	focus  int
	err    string
	// saving is set between Save being asked and its answer.
	saving bool
}

// labelWidth is the column of the field names.
const labelWidth = 16

func newSetupForm(s *Setup, width int) *setupForm {
	name := textinput.New()
	name.SetValue(s.Profile)
	f := &setupForm{fields: []formField{{label: "profile", in: name}}}
	for _, k := range config.ProfileKeys() {
		field, _ := k.Profile()
		in := textinput.New()
		in.SetValue(s.Values[field])
		in.Placeholder = placeholder(k)
		if k.Type == config.TypeSecret {
			in.EchoMode = textinput.EchoPassword
		}
		f.fields = append(f.fields, formField{label: strings.ReplaceAll(field, "_", " "), key: k, in: in})
	}
	for i := range f.fields {
		f.fields[i].in.Prompt = ""
		f.fields[i].in.SetVirtualCursor(true)
	}
	f.setWidth(width)
	f.fields[0].in.Focus()
	return f
}

// setWidth gives every input the room right of the labels.
func (f *setupForm) setWidth(w int) {
	for i := range f.fields {
		f.fields[i].in.SetWidth(max(w-labelWidth-1, 10))
	}
}

// placeholder is what an empty field shows: what leaving it empty means.
func placeholder(k config.Key) string {
	switch {
	case k.Type == config.TypeSecret:
		return "paste the key, or ${VARIABLE}; empty keeps the current one"
	case k.Required:
		return "required"
	case len(k.Enum) > 0:
		return strings.Join(k.Enum, " | ")
	case k.Default != "":
		return "default " + k.Default
	default:
		return "not set"
	}
}

// move changes the focused field.
func (f *setupForm) move(delta int) tea.Cmd {
	f.fields[f.focus].in.Blur()
	f.focus = (f.focus + delta + len(f.fields)) % len(f.fields)
	return f.fields[f.focus].in.Focus()
}

// values checks every non-empty field against its type and returns the
// profile name and the fields to write. A bad value moves the focus to
// its field.
func (f *setupForm) values() (string, map[string]string, error) {
	name := strings.TrimSpace(f.fields[0].in.Value())
	if name == "" {
		f.focus = 0
		return "", nil, fmt.Errorf("a profile needs a name")
	}
	out := map[string]string{}
	for i, fl := range f.fields[1:] {
		v := strings.TrimSpace(fl.in.Value())
		if v == "" {
			if fl.key.Required {
				f.fields[f.focus].in.Blur()
				f.focus = i + 1
				f.fields[f.focus].in.Focus()
				field, _ := fl.key.Profile()
				return "", nil, fmt.Errorf("a profile needs a %s", strings.ReplaceAll(field, "_", " "))
			}
			continue
		}
		nv, err := fl.key.Normalize(v)
		if err != nil {
			f.fields[f.focus].in.Blur()
			f.focus = i + 1
			f.fields[f.focus].in.Focus()
			return "", nil, err
		}
		field, _ := fl.key.Profile()
		out[field] = nv
	}
	return name, out, nil
}

// openSetup shows the form.
func (m model) openSetup() (model, tea.Cmd) {
	m.setupForm = newSetupForm(m.setup, m.width)
	return m, m.setupForm.fields[0].in.Focus()
}

// setupKey handles a key while the form is open: tab and the arrows move
// between fields, enter moves on and saves from the last field, ctrl+s
// saves from anywhere, esc leaves without writing.
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
	case "enter", "ctrl+s":
		if k.String() == "enter" && f.focus < len(f.fields)-1 {
			return m, f.move(1)
		}
		name, values, err := f.values()
		if err != nil {
			f.err = strings.TrimPrefix(err.Error(), "config: ")
			return m, nil
		}
		f.saving, f.err = true, ""
		save := m.setup.Save
		return m, func() tea.Msg {
			msg, err := save(name, values)
			return setupDoneMsg{profile: name, msg: msg, err: err}
		}
	}
	var cmd tea.Cmd
	f.fields[f.focus].in, cmd = f.fields[f.focus].in.Update(k)
	return m, cmd
}

// setupSaved closes the form on success and keeps it open, with the
// reason, on failure. A saved profile is put to use at once: the session
// switches to it, which makes the core re-read the file, so a key typed
// after a rejection is in force without a restart.
func (m model) setupSaved(msg setupDoneMsg) (model, tea.Cmd) {
	if m.setupForm == nil {
		return m, nil
	}
	if msg.err != nil {
		m.setupForm.saving = false
		m.setupForm.err = strings.TrimPrefix(msg.err.Error(), "config: ")
		return m, nil
	}
	m.setupForm = nil
	m.add(entry{kind: entryNotice, text: msg.msg, done: true})
	if m.sessionID == "" || m.busy {
		return m, m.loadProfiles()
	}
	var cmd tea.Cmd
	m, cmd = m.switchProfile(rpc.ProfileRef{Profile: msg.profile})
	return m, tea.Batch(cmd, m.loadProfiles())
}

// view draws the form in a w×h box: the inputs, and under them the help
// of the focused field, so that what a value means is on screen.
func (f *setupForm) view(describe string, w, h int, sty styles) string {
	var b strings.Builder
	b.WriteString(rule(sty.header.Render("provider profile"), w, sty) + "\n")
	b.WriteString(sty.hint.Render(wrap(describe, w)) + "\n\n")
	for i, fl := range f.fields {
		label := padRight(fl.label, labelWidth)
		if i == f.focus {
			label = sty.you.Render(label)
		} else {
			label = sty.hint.Render(label)
		}
		b.WriteString(label + fl.in.View() + "\n")
	}
	help := "the name the profile is saved under; default_provider points at it"
	if k := f.fields[f.focus].key; k.Path != "" {
		help = k.Help
	}
	b.WriteString("\n" + sty.hint.Render(wrap(help, w)) + "\n")
	switch {
	case f.saving:
		b.WriteString(sty.hint.Render("saving…"))
	case f.err != "":
		b.WriteString(sty.toolErr.Render(f.err))
	default:
		b.WriteString(sty.hint.Render(wrap("tab/↑↓ move · enter next, saves on the last field · ctrl+s saves · esc cancels · empty fields keep their default", w)))
	}
	return lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, b.String())
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
