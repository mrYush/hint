package config

import "os"

// expandValue substitutes $VAR and ${VAR} references in s using lookup.
// An unset variable expands to the empty string; whether the resulting empty
// field is an error is decided by validation, not here. Shell features
// beyond that ($(cmd), ${VAR:-default}) are deliberately unsupported.
func expandValue(s string, lookup func(string) (string, bool)) string {
	return os.Expand(s, func(name string) string {
		v, _ := lookup(name)
		return v
	})
}
