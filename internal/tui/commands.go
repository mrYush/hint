package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
)

// command is one slash command of the input line. The same table drives
// /help, the dispatch in submit and Tab completion, so a command that
// exists is a command that is listed and completed — and one that is not
// available right now is none of the three.
type command struct {
	name string
	help string
	// args are the arguments the command takes, a closed set; none means
	// the command takes no argument. Completion offers exactly these.
	args []string
	// enabled says whether the command makes sense in the current state;
	// nil means always.
	enabled func(m model) bool
}

// commands is the table. It is a function so that a later source of
// commands (a profile list from the core, WP1.5) can depend on the
// session the shell is attached to.
func commands() []command {
	return []command{
		{name: "/copy", help: "copy the last answer to the clipboard (or: question, code, all)",
			args: []string{"answer", "question", "code", "all"}, enabled: func(m model) bool { return len(m.entries) > 0 }},
		{name: "/mouse", help: "toggle mouse mode: wheel scrolls, a click copies the block under it (off: select with the terminal)"},
		{name: "/setup", help: "fill in the provider configuration (profile, base URL, model, API key)", enabled: func(m model) bool { return m.setup != nil && m.setup.Save != nil && !m.busy }},
		{name: "/sessions", help: "list this directory's sessions and resume one", enabled: func(m model) bool { return !m.busy }},
		{name: "/help", help: "show this list"},
		{name: "/exit", help: "leave (ctrl+d does too); /quit is the same"},
	}
}

// available returns the commands that can be used right now.
func (m model) available() []command {
	var out []command
	for _, c := range commands() {
		if c.enabled == nil || c.enabled(m) {
			out = append(out, c)
		}
	}
	return out
}

// helpText is /help: the keys, then every command that exists.
func helpText() string {
	var b strings.Builder
	b.WriteString("enter sends · ctrl+j newline · tab completes commands · pgup/pgdn scroll · ctrl+y copies the last answer · ctrl+d quits\n")
	for _, c := range commands() {
		fmt.Fprintf(&b, "%-10s %s\n", c.name, c.help)
	}
	return strings.TrimRight(b.String(), "\n")
}

// complete returns the whole-line replacements Tab may offer for input:
// command names while the first word is being typed, that command's
// arguments after it. Anything not starting with "/" is a question, and a
// question has no deterministic completion.
func (m model) complete(input string) []string {
	if !strings.HasPrefix(input, "/") || strings.Contains(input, "\n") {
		return nil
	}
	name, rest, hasArg := strings.Cut(input, " ")
	var out []string
	if !hasArg {
		for _, c := range m.available() {
			if strings.HasPrefix(c.name, name) {
				out = append(out, c.name)
			}
		}
		return out
	}
	for _, c := range m.available() {
		if c.name != name {
			continue
		}
		for _, a := range c.args {
			if strings.HasPrefix(a, strings.TrimLeft(rest, " ")) {
				out = append(out, name+" "+a)
			}
		}
	}
	return out
}

// completion is a Tab in progress: the line the candidates were computed
// for, and where in them the user has cycled to.
type completion struct {
	cands []string
	// shown is the text the input holds because of the last Tab; a key
	// that changes it ends the cycle.
	shown string
	idx   int
}

// tab completes the input line. The first Tab extends the line to the
// longest common prefix of the candidates, or takes the only one, and
// lists the rest; further Tabs cycle through them.
func (m model) tab() model {
	cur := m.ta.Value()
	if m.comp != nil && cur == m.comp.shown {
		m.comp.idx = (m.comp.idx + 1) % len(m.comp.cands)
		m.comp.shown = m.comp.cands[m.comp.idx]
		m.ta.SetValue(m.comp.shown)
		m.status = strings.Join(m.comp.cands, "  ")
		return m
	}
	m.comp = nil
	cands := m.complete(cur)
	switch len(cands) {
	case 0:
		if strings.HasPrefix(cur, "/") {
			m.status = "no command matches " + cur
		}
	case 1:
		// A command that takes arguments gets the space that starts them;
		// a finished command or argument is left as it is.
		m.ta.SetValue(cands[0])
		if !strings.Contains(cands[0], " ") && len(commandArgs(cands[0])) > 0 {
			m.ta.SetValue(cands[0] + " ")
		}
	default:
		sort.Strings(cands)
		if p := commonPrefix(cands); len(p) > len(cur) {
			m.ta.SetValue(p)
		}
		m.comp = &completion{cands: cands, shown: m.ta.Value(), idx: -1}
		m.status = strings.Join(cands, "  ")
	}
	return m
}

// commandArgs is the argument set of the command a completed line names.
func commandArgs(line string) []string {
	name, _, _ := strings.Cut(line, " ")
	for _, c := range commands() {
		if c.name == name {
			return c.args
		}
	}
	return nil
}

func commonPrefix(ss []string) string {
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}

// writeClipboard puts text on the system clipboard. It is a variable so
// that tests do not touch the real one.
var writeClipboard = clipboard.WriteAll

// copyCmd copies text two ways, because neither works everywhere: the
// terminal's own clipboard sequence (OSC 52) reaches the local clipboard
// through ssh and tmux but is ignored by some terminals, and the system
// clipboard needs a tool (xclip, xsel, wl-copy) on Linux. Whichever works
// does; the other fails quietly.
func copyCmd(text string) tea.Cmd {
	return tea.Batch(tea.SetClipboard(text), func() tea.Msg {
		_ = writeClipboard(text)
		return nil
	})
}

// copyTarget picks the text a /copy argument names.
func (m model) copyTarget(what string) (string, bool) {
	switch what {
	case "", "answer":
		return m.lastOf(entryAssistant)
	case "question":
		return m.lastOf(entryUser)
	case "code":
		answer, ok := m.lastOf(entryAssistant)
		if !ok {
			return "", false
		}
		return lastFence(answer)
	case "all":
		var parts []string
		for _, e := range m.entries {
			switch e.kind {
			case entryUser:
				parts = append(parts, "you:\n"+e.text)
			case entryAssistant:
				parts = append(parts, "hint:\n"+e.text)
			case entryTool, entryNotice:
			}
		}
		return strings.Join(parts, "\n\n"), len(parts) > 0
	}
	return "", false
}

// lastOf returns the text of the newest finished entry of a kind.
func (m model) lastOf(kind entryKind) (string, bool) {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if e := m.entries[i]; e.kind == kind && e.text != "" {
			return e.text, true
		}
	}
	return "", false
}

// lastFence returns the body of the last fenced code block of markdown.
func lastFence(md string) (string, bool) {
	var body []string
	var last []string
	in := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if in {
				last = body
			}
			in, body = !in, nil
			continue
		}
		if in {
			body = append(body, line)
		}
	}
	if last == nil {
		return "", false
	}
	return strings.Join(last, "\n"), true
}

// entryAt maps a screen row to the transcript entry drawn there, or -1.
// Entries are joined by one blank line, as setContent does.
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

// copied is the status text after a copy.
func copied(what string, text string) string {
	return fmt.Sprintf("copied %s (%d characters)", what, len([]rune(text)))
}
