// Package repomap exposes bounded code navigation over the shared local index.
package repomap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	// MaxOutputBytes includes the complete serialized map envelope.
	MaxOutputBytes = 8192
	// MinOutputBytes is the smallest supported map output configuration.
	MinOutputBytes = 1024
)

// Request selects a root-scoped declaration query or continues a prior query.
type Request struct {
	Repo    string `json:"repo,omitempty"`
	Path    string `json:"path,omitempty"`
	EntryID string `json:"entry_id,omitempty"`
	Symbol  string `json:"symbol,omitempty"`
	Match   string `json:"match,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
}

func decodeRequest(raw json.RawMessage) (Request, error) {
	var r Request
	if len(raw) > 64<<10 {
		return r, errors.New("arguments exceed 64 KiB")
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return r, errors.New("arguments must be an object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, fmt.Errorf("decode arguments: %w", err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return r, errors.New("expected one argument object")
	}
	// Presence matters: an empty path next to a cursor is still a second
	// request shape, and must not silently change continuation semantics.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return r, fmt.Errorf("decode fields: %w", err)
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return r, fmt.Errorf("%s must not be null", name)
		}
	}
	if _, ok := fields["cursor"]; ok {
		if r.Cursor == "" || len(fields) != 1 {
			return r, errors.New("cursor must be nonempty and the only field")
		}
		return r, nil
	}
	if _, path := fields["path"]; path {
		if _, id := fields["entry_id"]; id {
			return r, errors.New("path and entry_id are mutually exclusive")
		}
	}
	if _, present := fields["entry_id"]; present && r.EntryID == "" {
		return r, errors.New("entry_id must be nonempty")
	}
	if r.Match != "" && r.Symbol == "" {
		return r, errors.New("match requires symbol")
	}
	if r.Match != "" && r.Match != "exact" && r.Match != "prefix" {
		return r, errors.New("match must be exact or prefix")
	}
	if _, ok := fields["limit"]; ok && (r.Limit < 1 || r.Limit > 50) {
		return r, errors.New("limit must be between 1 and 50")
	}
	if r.Limit == 0 {
		r.Limit = 20
	}
	if r.Match == "" {
		r.Match = "exact"
	}
	if r.Path == "" && r.EntryID == "" {
		r.Path = "."
	}
	return r, nil
}

// Response describes an observed generation, independently of live completeness.
type Response struct {
	RootID             string         `json:"root_id"`
	MembershipRevision uint64         `json:"membership_revision"`
	Generation         uint64         `json:"generation"`
	Scope              Scope          `json:"scope"`
	Order              string         `json:"order"`
	Status             Status         `json:"status"`
	Coverage           Coverage       `json:"coverage"`
	Files              []File         `json:"files"`
	Entries            []Declaration  `json:"entries"`
	Verification       *Verification  `json:"verification,omitempty"`
	Invalidations      map[string]int `json:"invalidations,omitempty"`
	Page               Page           `json:"page"`
}

// Scope carries a usable handle when its display path must be abbreviated.
type Scope struct {
	EntryID       string `json:"entry_id,omitempty"`
	PathDisplay   string `json:"path_display,omitempty"`
	PathTruncated bool   `json:"path_truncated,omitempty"`
}

// Status describes available index work rather than source freshness.
type Status struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// Coverage uses optional maintained counts, never an undiscovered denominator.
type Coverage struct {
	DiscoveryComplete bool             `json:"discovery_complete"`
	KnownFiles        *int64           `json:"known_files,omitempty"`
	IndexedFiles      *int64           `json:"indexed_files,omitempty"`
	PendingFiles      *int64           `json:"pending_files,omitempty"`
	OmittedFiles      *int64           `json:"omitted_files,omitempty"`
	Omissions         map[string]int64 `json:"omissions,omitempty"`
}

// File identifies the source observation used by its declarations.
type File struct {
	ID            string `json:"id"`
	PathDisplay   string `json:"path_display,omitempty"`
	PathTruncated bool   `json:"path_truncated,omitempty"`
	SourceVersion string `json:"source_version"`
	Freshness     string `json:"freshness"`
	ObservedAt    string `json:"observed_at,omitempty"`
	CheckedAt     string `json:"checked_at,omitempty"`
}

// Declaration locates an observed declaration without returning its body.
type Declaration struct {
	ID                 string `json:"id"`
	FileID             string `json:"file_id"`
	Kind               string `json:"kind,omitempty"`
	Name               string `json:"name,omitempty"`
	Signature          string `json:"signature,omitempty"`
	Enclosing          string `json:"enclosing,omitempty"`
	StartByte          int64  `json:"start_byte"`
	EndByte            int64  `json:"end_byte"`
	StartLine          int    `json:"start_line,omitempty"`
	EndLine            int    `json:"end_line,omitempty"`
	SignatureTruncated bool   `json:"signature_truncated,omitempty"`
	DisplayTruncated   bool   `json:"display_truncated,omitempty"`
	OmissionReason     string `json:"omission_reason,omitempty"`
}

// Verification reports optional hashing work; access checks are mandatory.
type Verification struct {
	FilesChecked    int   `json:"files_checked,omitempty"`
	BytesRead       int64 `json:"bytes_read,omitempty"`
	BudgetExhausted bool  `json:"budget_exhausted"`
}

// Page distinguishes a completed generation query from a bounded work slice.
type Page struct {
	Truncated     bool   `json:"truncated"`
	QueryComplete bool   `json:"query_complete"`
	StopReason    string `json:"stop_reason,omitempty"`
	NextCursor    string `json:"next_cursor,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
}

type queryError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Reason    string `json:"reason,omitempty"`
}

func errorPayload(code, message string, retryable bool, reason ...string) string {
	detail := ""
	if len(reason) > 0 {
		detail = reason[0]
	}
	data, _ := json.Marshal(struct {
		Error queryError `json:"error"`
	}{queryError{Code: code, Message: abbreviate(message, 256), Retryable: retryable, Reason: detail}})
	return string(data)
}

// abbreviate counts UTF-8 bytes and does not split an encoded rune.
func abbreviate(s string, budget int) string {
	s = string(bytes.ToValidUTF8([]byte(s), []byte("\uFFFD")))
	if len(s) <= budget {
		return s
	}
	if budget < 3 {
		return ""
	}
	n := budget - 3
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}
