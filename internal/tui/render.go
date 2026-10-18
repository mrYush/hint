package tui

import (
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	gstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"

	"github.com/mrYush/hint/internal/shell"
	"github.com/mrYush/hint/pkg/agentapi"
)

// entryKind says what a transcript entry is, which decides how it is
// drawn.
type entryKind int

const (
	entryUser entryKind = iota
	entryAssistant
	entryTool
	entryNotice
)

// entry is one block of the transcript. An assistant entry that is not
// done is the answer still streaming in; it is drawn as plain text and
// rendered as markdown once, when the turn ends.
type entry struct {
	kind entryKind
	text string
	done bool
	// name is the tool of an entryTool; failed marks its error result.
	name   string
	failed bool
}

// styles are the colors of the shell, chosen once for a dark or a light
// terminal.
type styles struct {
	header, you, assistant, tool, toolErr, notice, status, hint, rule lipgloss.Style
	dialogBox, dialogTitle, dialogKey                                 lipgloss.Style
	diffAdd, diffDel, diffHunk, diffHead                              lipgloss.Style
	pickerCursor, pickerDim                                           lipgloss.Style
}

func newStyles(dark bool) styles {
	ld := lipgloss.LightDark(dark)
	accent := ld(lipgloss.Color("62"), lipgloss.Color("69"))
	dim := ld(lipgloss.Color("245"), lipgloss.Color("240"))
	return styles{
		header:       lipgloss.NewStyle().Foreground(accent).Bold(true),
		you:          lipgloss.NewStyle().Foreground(ld(lipgloss.Color("36"), lipgloss.Color("43"))).Bold(true),
		assistant:    lipgloss.NewStyle().Foreground(accent).Bold(true),
		rule:         lipgloss.NewStyle().Foreground(ld(lipgloss.Color("250"), lipgloss.Color("238"))),
		tool:         lipgloss.NewStyle().Foreground(dim),
		toolErr:      lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
		notice:       lipgloss.NewStyle().Foreground(dim).Italic(true),
		status:       lipgloss.NewStyle().Foreground(dim),
		hint:         lipgloss.NewStyle().Foreground(dim),
		dialogBox:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("214")).Padding(0, 1),
		dialogTitle:  lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true),
		dialogKey:    lipgloss.NewStyle().Bold(true),
		diffAdd:      lipgloss.NewStyle().Foreground(lipgloss.Color("40")),
		diffDel:      lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
		diffHunk:     lipgloss.NewStyle().Foreground(accent),
		diffHead:     lipgloss.NewStyle().Bold(true),
		pickerCursor: lipgloss.NewStyle().Foreground(accent).Bold(true),
		pickerDim:    lipgloss.NewStyle().Foreground(dim),
	}
}

// renderEntry draws one transcript entry at width w.
func (m *model) renderEntry(e entry, w int) string {
	switch e.kind {
	case entryUser:
		return m.sty.you.Render("you") + "\n" + wrap(e.text, w)
	case entryAssistant:
		label := m.sty.assistant.Render("hint") + "\n"
		if e.done && e.text != "" {
			if s, ok := m.markdown(e.text, w); ok {
				return label + s
			}
		}
		return label + wrap(e.text, w)
	case entryTool:
		if e.failed {
			return m.sty.toolErr.Render("✗ " + e.name + " failed: " + e.text)
		}
		return m.sty.tool.Render("⚙ " + e.name + " " + e.text)
	case entryNotice:
		return m.sty.notice.Render("hint: " + e.text)
	default:
		return e.text
	}
}

// wrap breaks text into lines of at most w cells.
func wrap(text string, w int) string {
	if w <= 0 {
		return text
	}
	return lipgloss.NewStyle().Width(w).Render(text)
}

// markdown renders a finished message. The renderer is built per width
// and kept until the window changes; a render failure falls back to the
// plain text rather than hiding the answer.
func (m *model) markdown(text string, w int) (string, bool) {
	if m.md == nil || m.mdWidth != w {
		r, err := glamour.NewTermRenderer(glamour.WithStyles(markdownStyle(m.dark)), glamour.WithWordWrap(w))
		if err != nil {
			return "", false
		}
		m.md, m.mdWidth = r, w
	}
	s, err := m.md.Render(text)
	if err != nil {
		return "", false
	}
	return strings.TrimRight(s, "\n"), true
}

// markdownStyle is glamour's palette for the terminal's background with
// the filled areas taken out: a heading and inline code are told apart by
// color and weight, like everything else on the screen, not by a block of
// background behind them.
func markdownStyle(dark bool) ansi.StyleConfig {
	style := gstyles.LightStyleConfig
	if dark {
		style = gstyles.DarkStyleConfig
	}
	style.H1.BackgroundColor = nil
	style.Code.BackgroundColor = nil
	return style
}

// rule draws a horizontal line of width w with text at its left, the
// separator between the zones of the screen.
func rule(text string, w int, sty styles) string {
	line := sty.rule.Render("── ")
	if text != "" {
		line += text + " "
	}
	if n := w - lipgloss.Width(line); n > 0 {
		line += sty.rule.Render(strings.Repeat("─", n))
	}
	return line
}

// colorDiff colors a unified diff by line prefix. It is the whole of the
// diff view: the core already produced the diff, the shell only has to
// make it readable.
func colorDiff(detail string, sty styles) string {
	lines := strings.Split(detail, "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			lines[i] = sty.diffHead.Render(line)
		case strings.HasPrefix(line, "@@"):
			lines[i] = sty.diffHunk.Render(line)
		case strings.HasPrefix(line, "+"):
			lines[i] = sty.diffAdd.Render(line)
		case strings.HasPrefix(line, "-"):
			lines[i] = sty.diffDel.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

// fromHistory turns a recorded conversation into transcript entries: the
// user's questions, the assistant's answers and the tools it called.
// Tool results and the system prompt are not shown; they were not shown
// when the conversation happened either.
func fromHistory(msgs []agentapi.Message) []entry {
	var entries []entry
	for _, msg := range msgs {
		switch msg.Role {
		case agentapi.RoleUser:
			if text := msg.Text(); text != "" {
				entries = append(entries, entry{kind: entryUser, text: text, done: true})
			}
		case agentapi.RoleAssistant:
			if text := msg.Text(); text != "" {
				entries = append(entries, entry{kind: entryAssistant, text: text, done: true})
			}
			for _, call := range msg.ToolCalls {
				entries = append(entries, entry{kind: entryTool, name: call.Name, text: shell.SummarizeArgs(call.Arguments), done: true})
			}
		case agentapi.RoleSystem, agentapi.RoleTool:
			// Not shown: the prompt is the core's, a tool result was never
			// shown in full when it happened either.
		}
	}
	return entries
}

// short is the session id as the header shows it.
func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
