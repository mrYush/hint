// Package text holds small string operations shared across hint packages.
package text

import "unicode/utf8"

// Cut returns the longest prefix of s of at most n bytes that ends on a
// UTF-8 rune boundary. n <= 0 returns ""; n >= len(s) returns s.
//
// It assumes s is valid UTF-8. When n falls inside a rune, Cut backs up to
// that rune's first byte; it does not repair invalid bytes that already
// appear earlier in s.
func Cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
