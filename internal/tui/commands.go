package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"

	"github.com/mrYush/hint/internal/config"
)

// command is one slash command of the input line. The same table drives
// /help, the dispatch in submit and Tab completion, so a command that
// exists is a command that is listed and completed — and one that is not
// available right now is none of the three.
type command struct {
	name string
	help string
	// next says what may follow the arguments typed so far; nil means the
	// command takes no argument.
	next func(m model, prev []string) argSpec
	// enabled says whether the command makes sense in the current state;
	// nil means always.
	enabled func(m model) bool
}

// argSpec is what may come at one argument position: the candidates Tab
// offers, and whether an argument is expected there even when nothing
// can be offered (a free value such as a model name).
type argSpec struct {
	cands    []string
	expected bool
}

// fixed is the next of a command with one optional argument from a
// closed set.
func fixed(args ...string) func(model, []string) argSpec {
	return func(_ model, prev []string) argSpec {
		if len(prev) == 0 {
			return argSpec{cands: args}
		}
		return argSpec{}
	}
}

// commands is the table. It is a function so that a later source of
// commands (a profile list from the core, WP1.5) can depend on the
// session the shell is attached to.
func commands() []command {
	return []command{
		{name: "/copy", help: "copy the last answer to the clipboard (or: question, code, all)",
			next: fixed("answer", "question", "code", "all"), enabled: func(m model) bool { return len(m.entries) > 0 }},
		{name: "/mouse", help: "toggle mouse mode: wheel scrolls, a click copies the block under it (off: select with the terminal)"},
		{name: "/setup", help: "fill in a provider profile: kind, base URL, API key (masked), model, context window, header timeout",
			enabled: func(m model) bool { return m.setup != nil && m.setup.Save != nil && !m.busy }},
		{name: "/config", help: "show | keys | set [--project] <key> <value> | unset [--project] <key>: any config key, checked before it is written",
			next: configArgs, enabled: func(m model) bool { return m.setup != nil && m.setup.Set != nil }},
		{name: "/sessions", help: "list this directory's sessions and resume one", enabled: func(m model) bool { return !m.busy }},
		{name: "/help", help: "show this list"},
		{name: "/exit", help: "leave (ctrl+d does too); /quit is the same"},
	}
}

// configArgs completes /config: the subcommand, --project, a key from the
// schema, and for a typed key its allowed values.
func configArgs(m model, prev []string) argSpec {
	if len(prev) == 0 {
		return argSpec{cands: []string{"show", "keys", "set", "unset"}}
	}
	sub, rest := prev[0], prev[1:]
	if sub != "set" && sub != "unset" {
		return argSpec{}
	}
	if len(rest) > 0 && rest[0] == "--project" {
		rest = rest[1:]
	}
	switch len(rest) {
	case 0:
		cands := configKeys(m, sub == "unset")
		if len(prev) == 1 {
			cands = append([]string{"--project"}, cands...)
		}
		return argSpec{cands: cands, expected: true}
	case 1:
		if sub == "unset" {
			return argSpec{}
		}
		k, _, err := config.LookupKey(rest[0])
		if err != nil {
			return argSpec{expected: true}
		}
		switch k.Type {
		case config.TypeEnum:
			return argSpec{cands: k.Enum, expected: true}
		case config.TypeBool:
			return argSpec{cands: []string{"true", "false"}, expected: true}
		case config.TypeString, config.TypeInt, config.TypeDuration, config.TypeSecret:
			// A free value: nothing to offer, but one is expected.
		}
		return argSpec{expected: true}
	}
	return argSpec{}
}

// configKeys are the keys /config offers: the top-level ones, and the
// fields of the profile in use under its name. A key is not offered for
// set — typed into the input line it would sit there in plain sight;
// /setup takes it masked.
func configKeys(m model, withSecrets bool) []string {
	var out []string
	for _, k := range config.Keys() {
		field, isProfile := k.Profile()
		switch {
		case k.Type == config.TypeSecret && !withSecrets:
		case isProfile:
			if m.setup != nil && m.setup.Profile != "" {
				out = append(out, "providers."+m.setup.Profile+"."+field)
			}
		default:
			out = append(out, k.Path)
		}
	}
	return out
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
	b.WriteString("enter sends · newline: shift+enter (where the terminal reports it), alt+enter, ctrl+j, or \\ at the end of the line then enter · tab completes commands · pgup/pgdn scroll · ctrl+y copies the last answer · ctrl+d quits\n")
	for _, c := range commands() {
		fmt.Fprintf(&b, "%-10s %s\n", c.name, c.help)
	}
	return strings.TrimRight(b.String(), "\n")
}

// complete returns the whole-line replacements Tab may offer for input:
// command names while the first word is being typed, then what the
// command's next says about the argument under the cursor. Anything not
// starting with "/" is a question, and a question has no deterministic
// completion.
func (m model) complete(input string) []string {
	if !strings.HasPrefix(input, "/") || strings.Contains(input, "\n") {
		return nil
	}
	cmd, prev, partial, ok := m.parseLine(input)
	if !ok {
		var out []string
		for _, c := range m.available() {
			if strings.HasPrefix(c.name, partial) {
				out = append(out, c.name)
			}
		}
		return out
	}
	if cmd.next == nil {
		return nil
	}
	head := strings.Join(append([]string{cmd.name}, prev...), " ") + " "
	var out []string
	for _, c := range cmd.next(m, prev).cands {
		if strings.HasPrefix(c, partial) {
			out = append(out, head+c)
		}
	}
	return out
}

// parseLine splits a command line into the command, the finished
// arguments and the word under the cursor. ok is false while the command
// name itself is being typed (partial is then that name) or names no
// available command.
func (m model) parseLine(input string) (cmd command, prev []string, partial string, ok bool) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return command{}, nil, "", false
	}
	if !strings.HasSuffix(input, " ") {
		partial, fields = fields[len(fields)-1], fields[:len(fields)-1]
	}
	if len(fields) == 0 {
		return command{}, nil, partial, false
	}
	for _, c := range m.available() {
		if c.name == fields[0] {
			return c, fields[1:], partial, true
		}
	}
	return command{}, nil, partial, false
}

// wantsMore says whether a completed line expects another word, so that
// Tab can type the space before it. A key prefix ending in "." does not:
// its continuation is the same word.
func (m model) wantsMore(line string) bool {
	if strings.HasSuffix(line, ".") {
		return false
	}
	cmd, prev, _, ok := m.parseLine(line + " ")
	if !ok || cmd.next == nil {
		return false
	}
	spec := cmd.next(m, prev)
	return spec.expected || len(spec.cands) > 0
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
		// The space before the next word is typed when one is expected.
		m.ta.SetValue(cands[0])
		if m.wantsMore(cands[0]) {
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
