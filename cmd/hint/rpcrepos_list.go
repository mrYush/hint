package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/text"
)

// Root listing cursors are user navigation hints, not authority tokens.
// Every page is derived from this session's current bounded membership snapshot.
type repoListCursor struct {
	Session  string `json:"session"`
	Revision uint64 `json:"revision"`
	After    string `json:"after"`
}

func (s *rpcSession) repoListing(cursor string) (rpc.RepoResult, error) {
	roots, revision := s.workspace.Snapshot()
	start := 0
	if cursor != "" {
		if len(cursor) > 512 {
			return rpc.RepoResult{}, fmt.Errorf("%w: invalid root cursor", rpc.ErrInvalid)
		}
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		var state repoListCursor
		if err != nil || json.Unmarshal(data, &state) != nil || state.Session != s.workspace.ID() || state.Revision != revision {
			return rpc.RepoResult{}, fmt.Errorf("%w: stale root cursor; list roots again", rpc.ErrInvalid)
		}
		found := false
		for i, root := range roots {
			if root.ID == state.After {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return rpc.RepoResult{}, fmt.Errorf("%w: stale root cursor", rpc.ErrInvalid)
		}
	}
	result := rpc.RepoResult{Revision: revision, RootCount: len(roots)}
	body := fmt.Sprintf("Workspace revision %d (%d roots)\n", revision, len(roots))
	result.Text = body
	for i := start; i < len(roots) && i < start+16; i++ {
		root := roots[i]
		item := rpc.RepoInfo{ID: root.ID, Alias: root.Alias, Path: root.Path, Primary: root.Primary}
		if len(root.Path) > 256 || !utf8.ValidString(root.Path) {
			item.Path = ""
			item.PathOmitted = true
		}
		candidate := result
		candidate.Roots = append(append([]rpc.RepoInfo(nil), result.Roots...), item)
		primary := ""
		if root.Primary {
			primary = " (primary)"
		}
		display := root.Path
		if item.PathOmitted {
			display = text.Cut(display, 256) + "… (path omitted; use root ID)"
		}
		line := fmt.Sprintf("%q%s [%s]\n  %q\n", root.Alias, primary, root.ID, display)
		if i+1 < len(roots) {
			data, err := json.Marshal(repoListCursor{Session: s.workspace.ID(), Revision: revision, After: root.ID})
			if err != nil {
				return rpc.RepoResult{}, err
			}
			candidate.NextCursor = base64.RawURLEncoding.EncodeToString(data)
		} else {
			candidate.NextCursor = ""
		}
		candidate.Text = body + line
		if candidate.NextCursor != "" {
			candidate.Text += fmt.Sprintf("Continue: /repos list %s\n", candidate.NextCursor)
		}
		encoded, err := json.Marshal(candidate)
		if err != nil {
			return rpc.RepoResult{}, err
		}
		if len(encoded) > 8192 {
			if len(result.Roots) == 0 {
				return rpc.RepoResult{}, fmt.Errorf("%w: root item exceeds its output budget", rpc.ErrInvalid)
			}
			break
		}
		result = candidate
		body += line
	}
	return result, nil
}
