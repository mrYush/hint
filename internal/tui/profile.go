package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mrYush/hint/internal/rpc"
)

// profilePicker is the list of provider profiles: what /profile with no
// argument opens. Enter switches the session to the row under the cursor.
type profilePicker struct {
	infos   []rpc.ProfileInfo
	cursor  int
	loading bool
	err     string
}

// loadProfiles asks the core for its profiles; the answer feeds Tab
// completion and the picker.
func (m model) loadProfiles() tea.Cmd {
	cl, ctx := m.cl, m.ctx
	return func() tea.Msg {
		infos, err := cl.Profiles(ctx)
		return profilesMsg{infos: infos, err: err}
	}
}

// profilesLoaded keeps the list for completion and fills the picker when
// it is open, with the cursor on the profile in use.
func (m model) profilesLoaded(msg profilesMsg) model {
	if msg.err == nil {
		m.profiles = msg.infos
	}
	if m.ppicker == nil {
		return m
	}
	m.ppicker.loading = false
	if msg.err != nil {
		m.ppicker.err = msg.err.Error()
		return m
	}
	m.ppicker.infos = msg.infos
	for i, p := range msg.infos {
		if p.Name == m.profile.Profile {
			m.ppicker.cursor = i
		}
	}
	return m
}

// openProfiles shows the picker and refreshes the list behind it.
func (m model) openProfiles() (model, tea.Cmd) {
	m.ppicker = &profilePicker{loading: true}
	return m, m.loadProfiles()
}

// profileKey handles a key while the profile list is open.
func (m model) profileKey(k tea.KeyPressMsg) (model, tea.Cmd) {
	p := m.ppicker
	switch keyName(k) {
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
		m.ppicker = nil
		return m.switchProfile(rpc.ProfileRef{Profile: p.infos[p.cursor].Name})
	case "esc", "q":
		m.ppicker = nil
	}
	return m, nil
}

// switchProfile asks the core to answer this session with ref from now
// on. The answer is profileMsg; the header changes when it arrives.
func (m model) switchProfile(ref rpc.ProfileRef) (model, tea.Cmd) {
	if m.sessionID == "" {
		m.status = "no session to switch"
		return m, nil
	}
	cl, ctx, id := m.cl, m.ctx, m.sessionID
	return m, func() tea.Msg {
		got, err := cl.SetProfile(ctx, id, ref)
		return profileMsg{ref: got, err: err}
	}
}

// profileSwitched shows the outcome of a switch: the new profile in the
// header and a line in the transcript, or why it did not happen.
func (m model) profileSwitched(msg profileMsg) model {
	if msg.err != nil {
		m.status = strings.TrimPrefix(msg.err.Error(), "config: ")
		return m
	}
	m.profile = msg.ref
	m.add(entry{kind: entryNotice, text: "answering with " + describeProfile(msg.ref) + " from now on", done: true})
	return m
}

// describeProfile is a profile as the header and the notices name it:
// the profile, and the model when there is one.
func describeProfile(ref rpc.ProfileRef) string {
	if ref.Model == "" {
		return ref.Profile
	}
	return ref.Profile + "/" + ref.Model
}

// profileArgs completes /profile: a profile name first, then a model
// the profile serves, fetched by the first Tab on it.
func profileArgs(m model, prev []string) argSpec {
	switch len(prev) {
	case 0:
		names := make([]string, 0, len(m.profiles))
		for _, p := range m.profiles {
			names = append(names, p.Name)
		}
		return argSpec{cands: names}
	case 1:
		return argSpec{cands: m.modelsOf(prev[0])}
	}
	return argSpec{}
}

// view draws the profile list in a w×h box.
func (p *profilePicker) view(current rpc.ProfileRef, w, h int, sty styles) string {
	var b strings.Builder
	b.WriteString(rule(sty.header.Render("profiles"), w, sty) + "\n\n")
	switch {
	case p.loading:
		b.WriteString(sty.hint.Render("loading…"))
	case p.err != "":
		b.WriteString(sty.toolErr.Render(p.err))
	case len(p.infos) == 0:
		b.WriteString(sty.hint.Render("no profiles configured; /setup adds one"))
	default:
		for i, info := range p.infos {
			cursor := "  "
			if i == p.cursor {
				cursor = sty.pickerCursor.Render("▸ ")
			}
			var marks []string
			if info.Name == current.Profile {
				marks = append(marks, "in use")
			}
			if info.Default {
				marks = append(marks, "default")
			}
			if info.Fallback {
				marks = append(marks, "fallback")
			}
			row := fmt.Sprintf("%-12s  %-7s  %s", info.Name, info.Kind, info.Model)
			if len(marks) > 0 {
				row += "  (" + strings.Join(marks, ", ") + ")"
			}
			if !info.Usable {
				row += "  ✗ " + info.Problem
			}
			if i != p.cursor {
				row = sty.pickerDim.Render(row)
			}
			b.WriteString(cursor + wrapLine(row, w-2) + "\n")
		}
	}
	b.WriteString("\n" + sty.hint.Render("enter switch · esc back · ↑/↓ move"))
	return lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, b.String())
}
