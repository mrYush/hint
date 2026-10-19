package config

import "strings"

// maskMinLen is the shortest secret for which Mask keeps any characters.
// Below it the hint value of a prefix outweighs what it reveals.
const maskMinLen = 16

// Mask returns a form of a secret that is safe to log: enough of the ends to
// recognize which key it was ("sk-abc…xyz"), or "***" when the value is too
// short to reveal anything. An empty input stays empty so optional keys do
// not render as a fake secret. API keys are ASCII, so byte slicing is safe.
func Mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) < maskMinLen {
		return "***"
	}
	return s[:6] + "…" + s[len(s)-3:]
}

// redactMinLen is the shortest secret Redact replaces. A shorter one is
// not a credential any provider issues, and replacing a few characters
// everywhere they occur would garble ordinary text.
const redactMinLen = 8

// Redact replaces every occurrence of every secret of at least
// redactMinLen characters in text with "***". The debug logger passes
// all output through it with Config.Secrets() before writing, and every
// tool result goes through it before the model sees it (WP1.7).
func Redact(text string, secrets []string) string {
	for _, s := range secrets {
		if len(s) < redactMinLen {
			continue
		}
		text = strings.ReplaceAll(text, s, "***")
	}
	return text
}
