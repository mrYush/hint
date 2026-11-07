package repomap

import (
	"encoding/json"
	"fmt"
)

// encode counts the bytes actually sent, including escaping and continuation.
func encode(r Response, budget int) ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("repo map: encode result: %w", err)
	}
	if len(data) > budget {
		return nil, fmt.Errorf("repo map: result requires %d bytes, budget %d", len(data), budget)
	}
	return data, nil
}

// appendEntry never consumes a candidate that cannot fit the current page.
// The caller advances continuation only after this function succeeds.
func appendEntry(r *Response, file File, decl Declaration, budget int) bool {
	oldFiles, oldEntries := len(r.Files), len(r.Entries)
	known := false
	for _, f := range r.Files {
		if f.ID == file.ID {
			known = true
			break
		}
	}
	if !known {
		path := abbreviate(file.PathDisplay, 256)
		file.PathTruncated = file.PathTruncated || path != file.PathDisplay
		file.PathDisplay = path
		r.Files = append(r.Files, file)
	}
	name, enclosing := abbreviate(decl.Name, 256), abbreviate(decl.Enclosing, 256)
	decl.DisplayTruncated = name != decl.Name || enclosing != decl.Enclosing
	decl.Name, decl.Enclosing = name, enclosing
	signature := abbreviate(decl.Signature, 2048)
	decl.SignatureTruncated = signature != decl.Signature
	decl.Signature = signature
	r.Entries = append(r.Entries, decl)
	if _, err := encode(*r, budget); err == nil {
		return true
	}
	// A nonempty page ends before this candidate; it is retried on the next
	// page rather than repeatedly degrading useful records to stubs.
	if oldEntries == 0 {
		stub := Declaration{ID: decl.ID, FileID: decl.FileID, StartByte: decl.StartByte,
			EndByte: decl.EndByte, OmissionReason: "output_budget"}
		r.Entries[oldEntries] = stub
		if !known {
			r.Files[oldFiles].PathDisplay = ""
			// An absent display is unambiguous; the stub states why it is omitted.
			r.Files[oldFiles].PathTruncated = false
		}
		if _, err := encode(*r, budget); err == nil {
			return true
		}
	}
	r.Files = r.Files[:oldFiles]
	r.Entries = r.Entries[:oldEntries]
	return false
}

// appendInvalidation reports one definitive per-file exclusion without
// confusing current validation work with retained-generation coverage.
func appendInvalidation(r *Response, reason string, budget int) bool {
	if r.Invalidations == nil {
		r.Invalidations = make(map[string]int)
	}
	previous := r.Invalidations[reason]
	r.Invalidations[reason]++
	if _, err := encode(*r, budget); err == nil {
		return true
	}
	if previous == 0 {
		delete(r.Invalidations, reason)
	} else {
		r.Invalidations[reason] = previous
	}
	return false
}
