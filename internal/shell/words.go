package shell

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SplitWords splits a command line on whitespace, with single quotes, double
// quotes and backslash escaping for whitespace, quotes and backslashes.
// A backslash before any other character is preserved, so Windows paths survive.
func SplitWords(line string) ([]string, error) {
	var out []string
	var word strings.Builder
	quote := rune(0)
	escaped, started := false, false
	flush := func() {
		if started {
			out = append(out, word.String())
			word.Reset()
			started = false
		}
	}
	for _, r := range line {
		if escaped {
			if !escapableWordRune(r) {
				word.WriteRune('\\')
			}
			word.WriteRune(r)
			escaped, started = false, true
			continue
		}
		if quote != 0 {
			switch {
			case r == quote:
				quote = 0
			case r == '\\' && quote == '"':
				escaped = true
			default:
				word.WriteRune(r)
			}
			started = true
			continue
		}
		switch {
		case r == '\\':
			escaped, started = true, true
		case r == '\'' || r == '"':
			quote, started = r, true
		case unicode.IsSpace(r):
			flush()
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if escaped {
		return nil, errors.New("unfinished escape")
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	flush()
	return out, nil
}

func escapableWordRune(r rune) bool {
	return unicode.IsSpace(r) || r == '\\' || r == '\'' || r == '"'
}

// QuoteWord escapes one argument for SplitWords.
func QuoteWord(word string) string {
	if word == "" {
		return "''"
	}
	var out strings.Builder
	out.Grow(len(word))
	for _, r := range word {
		if escapableWordRune(r) {
			out.WriteRune('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

// EndsInSpace reports whether the last rune is unquoted, unescaped whitespace.
func EndsInSpace(line string) bool {
	last, size := utf8.DecodeLastRuneInString(line)
	if last == utf8.RuneError && size == 0 || !unicode.IsSpace(last) {
		return false
	}
	lastPos := len(line) - size
	quote := rune(0)
	escaped := false
	for i, r := range line {
		if i == lastPos {
			return quote == 0 && !escaped
		}
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else if quote == '"' && r == '\\' {
				escaped = true
			}
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '\'', '"':
			quote = r
		}
	}
	return false
}
