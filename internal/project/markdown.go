package project

import (
	"regexp"
	"strings"
)

// Section is one heading of a Markdown instruction file together with the
// text under it, up to the next heading of any level. A file's sections
// are a flat list in document order; nesting is implied by Level.
type Section struct {
	// Level is 1 to 6 for a heading, 0 for the text before the first
	// heading (or the whole file when it has none).
	Level int
	// Title is the heading text without its leading #s.
	Title string
	// Body is the text below the heading, trailing blank lines trimmed.
	Body string
	// Line is the 1-based line of the heading, or of the first line for
	// a Level-0 section.
	Line int
}

// Heading renders the section's heading line, or "" for Level 0.
func (s Section) Heading() string {
	if s.Level == 0 {
		return ""
	}
	return strings.Repeat("#", s.Level) + " " + s.Title
}

// Text renders the section as it appears in the file: heading, then body.
func (s Section) Text() string {
	if s.Level == 0 {
		return s.Body
	}
	if s.Body == "" {
		return s.Heading()
	}
	return s.Heading() + "\n" + s.Body
}

// atxHeading matches a Markdown ATX heading: up to three spaces of indent,
// one to six #s, a space, the title, optional closing #s.
var atxHeading = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*#*[ \t]*$`)

// Sections splits a Markdown document at its ATX headings (# to ######).
// A heading inside a fenced code block (``` or ~~~) is text, not a
// heading. Setext headings (a line underlined with === or ---) are not
// recognised: instruction files are written for agents by hand, and the
// ones seen so far all use #. A document with no heading is one Level-0
// section; a document that opens with a heading has no Level-0 section.
func Sections(content string) []Section {
	lines := strings.Split(content, "\n")
	var out []Section
	cur := Section{Line: 1}
	var body []string
	flush := func() {
		cur.Body = strings.TrimRight(strings.Join(body, "\n"), "\n")
		if cur.Level > 0 || strings.TrimSpace(cur.Body) != "" {
			out = append(out, cur)
		}
		body = nil
	}

	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if fence == "" {
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fence = trimmed[:3]
			} else if m := atxHeading.FindStringSubmatch(line); m != nil {
				flush()
				cur = Section{Level: len(m[1]), Title: strings.TrimSpace(m[2]), Line: i + 1}
				continue
			}
		} else if strings.HasPrefix(trimmed, fence) {
			fence = ""
		}
		body = append(body, line)
	}
	flush()
	return out
}

// Span returns the text of sections[i] together with every section nested
// under it: everything up to the next heading of the same or a higher
// level. It is what "read the Testing section" means when Testing has
// subsections.
func Span(sections []Section, i int) string {
	var parts []string
	for j := i; j < len(sections); j++ {
		if j > i && sections[j].Level <= sections[i].Level {
			break
		}
		parts = append(parts, sections[j].Text())
	}
	return strings.Join(parts, "\n\n")
}

// maxFirstSentence caps the excerpt an outline shows for a section.
const maxFirstSentence = 200

// FirstSentence returns the first sentence of the first paragraph of body,
// whitespace collapsed, or "" when the paragraph is a code block or body
// is blank. Beyond maxFirstSentence bytes the sentence is cut at a word.
func FirstSentence(body string) string {
	var para []string
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			break
		}
		// A list item or a quote still opens with a sentence worth
		// showing; the marker itself is noise in an outline.
		para = append(para, strings.TrimLeft(t, "-*+> \t"))
	}
	s := strings.Join(strings.Fields(strings.Join(para, " ")), " ")
	if s == "" {
		return ""
	}
	if end := sentenceEnd(s); end > 0 {
		s = s[:end]
	}
	if len(s) > maxFirstSentence {
		cut := strings.LastIndex(s[:maxFirstSentence], " ")
		if cut <= 0 {
			cut = maxFirstSentence
		}
		s = s[:cut] + "…"
	}
	return s
}

// sentenceEnd returns the index just past the first sentence terminator
// that is followed by a space, or 0 when there is none.
func sentenceEnd(s string) int {
	for i := 0; i+1 < len(s); i++ {
		switch s[i] {
		case '.', '!', '?':
			if s[i+1] == ' ' {
				return i + 1
			}
		}
	}
	return 0
}
