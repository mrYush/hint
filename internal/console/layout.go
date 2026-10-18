package console

import (
	"strings"
	"unicode"
)

// qwerty maps a letter of a Cyrillic layout to the Latin letter on the
// same key of a US keyboard: what the user meant when they pressed "y"
// for yes and the layout typed "н". Russian ЙЦУКЕН, with the keys where
// Ukrainian (і ї є) and Belarusian (і ў) differ.
var qwerty = func() map[rune]rune {
	pairs := "йqцwуeкrеtнyгuшiщoзpх[ъ]фaыsвdаfпgрhоjлkдlж;э'яzчxсcмvиbтnьmб,ю.ё`" +
		"іsї]є'ўo"
	m := map[rune]rune{}
	rs := []rune(pairs)
	for i := 0; i+1 < len(rs); i += 2 {
		from, to := rs[i], rs[i+1]
		m[from] = to
		if up := unicode.ToUpper(from); up != from {
			m[up] = unicode.ToUpper(to)
		}
	}
	return m
}()

// QWERTYRune returns the US-layout character on the key that produced r
// in a Cyrillic layout, or r itself.
func QWERTYRune(r rune) rune {
	if q, ok := qwerty[r]; ok {
		return q
	}
	return r
}

// QWERTY rewrites s as if it had been typed with a US layout. It is for
// answers made of letters a program checks — "y", "n", "always" — never
// for text a person writes.
func QWERTY(s string) string {
	return strings.Map(QWERTYRune, s)
}

// Answer is a typed answer as a prompt compares it: trimmed, in the US
// layout, lower case. "Н" and "нуы" are "y" and "yes".
func Answer(s string) string {
	return strings.ToLower(QWERTY(strings.TrimSpace(s)))
}
