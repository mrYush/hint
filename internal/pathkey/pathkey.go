// Package pathkey defines shared lexical identities for local workspace paths.
package pathkey

import (
	"crypto/sha256"
	"encoding/base64"
	"path/filepath"
	"strings"
)

// EntryID hashes the root and relative path after converting this platform's
// separators to slash. It does not clean, case-fold, validate or grant access.
// Unix backslashes, colons, spaces and non-UTF-8 bytes remain significant.
func EntryID(rootID, relative string) string {
	sum := sha256.Sum256([]byte(rootID + "\x00" + filepath.ToSlash(relative)))
	return "e_" + base64.RawURLEncoding.EncodeToString(sum[:])
}

// Valid reports whether a stored path uses canonical slash-separated local
// components. The root marker "." is reserved for frontier/scope callers.
// Validation is lexical; source access still requires workspace authority.
func Valid(relative string) bool {
	if relative == "" || strings.ContainsRune(relative, 0) || relative != filepath.ToSlash(relative) || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return false
	}
	for _, component := range strings.Split(relative, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}
