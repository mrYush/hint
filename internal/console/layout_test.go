package console_test

import (
	"testing"

	"github.com/mrYush/hint/internal/console"
)

func TestAnswerIgnoresTheLayout(t *testing.T) {
	cases := map[string]string{
		"y": "y", " Y ": "y", "н": "y", "Н": "y", "нуы": "yes",
		"т": "n", "тщ": "no", "ф": "a", "фдцфны": "always",
		"і": "s", "ў": "o", "5": "5", "": "",
	}
	for in, want := range cases {
		if got := console.Answer(in); got != want {
			t.Errorf("Answer(%q) = %q, want %q", in, got, want)
		}
	}
	// Every Russian letter has a key; a case pair maps to a case pair.
	for _, r := range "йцукенгшщзхъфывапролджэячсмитьбюё" {
		if console.QWERTYRune(r) == r {
			t.Errorf("%c has no US key", r)
		}
	}
	if console.QWERTY("Привет, мир") == "Привет, мир" {
		t.Error("QWERTY did nothing")
	}
}
