package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// WorkspaceRoot records an explicit local root, never its remote credentials.
type WorkspaceRoot struct {
	ID      string `json:"id"`
	Alias   string `json:"alias"`
	Path    string `json:"path"`
	Primary bool   `json:"primary"`
}

type workspaceRootWire struct {
	ID        string `json:"id"`
	Alias     string `json:"alias"`
	Path      string `json:"path,omitempty"`
	PathBytes []byte `json:"path_bytes,omitempty"`
	Primary   bool   `json:"primary"`
}

// MarshalJSON preserves filesystem paths that cannot be represented as UTF-8.
func (r WorkspaceRoot) MarshalJSON() ([]byte, error) {
	w := workspaceRootWire{ID: r.ID, Alias: r.Alias, Primary: r.Primary}
	if utf8.ValidString(r.Path) {
		w.Path = r.Path
	} else {
		w.PathBytes = []byte(r.Path)
	}
	return json.Marshal(w)
}

// UnmarshalJSON accepts exactly one textual or byte-encoded path.
func (r *WorkspaceRoot) UnmarshalJSON(data []byte) error {
	var w workspaceRootWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	if w.Path != "" && w.PathBytes != nil {
		return fmt.Errorf("session: ambiguous workspace path")
	}
	path := w.Path
	if w.PathBytes != nil {
		path = string(w.PathBytes)
	}
	*r = WorkspaceRoot{ID: w.ID, Alias: w.Alias, Path: path, Primary: w.Primary}
	return nil
}

// WorkspaceRef is the versioned membership snapshot after an idle-session edit.
type WorkspaceRef struct {
	Version      int             `json:"version"`
	AfterTurn    int             `json:"after_turn"`
	Revision     uint64          `json:"revision"`
	Roots        []WorkspaceRoot `json:"roots"`
	Reservations []WorkspaceRoot `json:"reservations,omitempty"`
}

func (w WorkspaceRef) validate() error {
	if (w.Version != 1 && w.Version != 2) || w.Revision == 0 || w.AfterTurn < 0 || len(w.Roots) == 0 || len(w.Roots) > 256 || len(w.Reservations) > 256 {
		return fmt.Errorf("session: invalid workspace snapshot")
	}
	ids := make(map[string]WorkspaceRoot, len(w.Roots))
	aliases := make(map[string]WorkspaceRoot, len(w.Roots))
	primary := 0
	for _, root := range w.Roots {
		_, duplicateID := ids[root.ID]
		_, duplicateAlias := aliases[root.Alias]
		if !validWorkspaceRoot(root) || duplicateID || duplicateAlias {
			return fmt.Errorf("session: invalid or duplicate workspace root")
		}
		ids[root.ID], aliases[root.Alias] = root, root
		if root.Primary {
			primary++
		}
	}
	if primary != 1 {
		return fmt.Errorf("session: workspace must have one primary root")
	}
	reserved := make(map[string]bool, len(w.Reservations))
	for _, root := range w.Reservations {
		if !validWorkspaceRoot(root) || reserved[root.Alias] {
			return fmt.Errorf("session: invalid or duplicate workspace reservation")
		}
		reserved[root.Alias] = true
		if previous, ok := aliases[root.Alias]; ok && previous != root {
			return fmt.Errorf("session: conflicting workspace reservation alias")
		}
		if previous, ok := ids[root.ID]; ok && (previous.Path != root.Path || previous.Primary != root.Primary) {
			return fmt.Errorf("session: conflicting workspace reservation identity")
		}
		if root.Primary && !ids[root.ID].Primary {
			return fmt.Errorf("session: reservation cannot introduce a primary root")
		}
		ids[root.ID], aliases[root.Alias] = root, root
	}
	return nil
}

func validWorkspaceRoot(root WorkspaceRoot) bool {
	return root.ID != "" && root.Path != "" && !strings.ContainsRune(root.Path, 0) && root.Alias != "" && utf8.ValidString(root.Alias) && strings.TrimSpace(root.Alias) == root.Alias && !strings.ContainsAny(root.Alias, "/\\\x00")
}

// Workspace returns the most recently recorded membership, or a zero value.
func (s *Session) Workspace() WorkspaceRef {
	out := s.workspace
	out.Roots = append([]WorkspaceRoot(nil), out.Roots...)
	out.Reservations = append([]WorkspaceRoot(nil), out.Reservations...)
	return out
}

// AppendWorkspace records membership before the core makes it active.
func (s *Session) AppendWorkspace(ref WorkspaceRef) error {
	if err := ref.validate(); err != nil {
		return err
	}
	ref.Roots = append([]WorkspaceRoot(nil), ref.Roots...)
	ref.Reservations = append([]WorkspaceRoot(nil), ref.Reservations...)
	if err := s.write(Record{Kind: KindWorkspace, At: s.now().UTC(), Workspace: &ref}); err != nil {
		return err
	}
	s.workspace = ref
	s.workspaceHistory = append(s.workspaceHistory, ref)
	return nil
}

// WorkspaceAtTurn returns authority that existed during the selected visible
// user turn; changes made after that turn cannot grant authority to its fork.
func (s *Session) WorkspaceAtTurn(turn int) WorkspaceRef {
	history := s.WorkspaceHistoryAtTurn(turn)
	if len(history) == 0 {
		return WorkspaceRef{}
	}
	return history[len(history)-1]
}

// WorkspaceHistoryAtTurn preserves changes through a visible turn for a fork.
func (s *Session) WorkspaceHistoryAtTurn(turn int) []WorkspaceRef {
	return WorkspaceHistoryForTurn(s.workspaceHistory, s.totalUserTurns, Turns(s.messages), turn)
}

// WorkspaceHistoryForTurn translates monotonic authority events into a fork's
// visible-turn coordinates, retaining the baseline before compacted history.
func WorkspaceHistoryForTurn(history []WorkspaceRef, total, visible, turn int) []WorkspaceRef {
	offset := total - visible
	absolute := offset + turn
	var out []WorkspaceRef
	for _, ref := range history {
		if ref.AfterTurn >= absolute {
			continue
		}
		ref.Roots = append([]WorkspaceRoot(nil), ref.Roots...)
		ref.Reservations = append([]WorkspaceRoot(nil), ref.Reservations...)
		ref.AfterTurn -= offset
		if ref.AfterTurn <= 0 {
			ref.AfterTurn = 0
			out = []WorkspaceRef{ref}
		} else {
			out = append(out, ref)
		}
	}
	return out
}

// UserTurns returns the monotonic user-turn count across compaction checkpoints.
func (s *Session) UserTurns() int { return s.totalUserTurns }

// WorkspaceHistory returns independent snapshots, including removed-root
// reservations needed to keep parent authority from widening on restart.
func (s *Session) WorkspaceHistory() []WorkspaceRef {
	out := make([]WorkspaceRef, len(s.workspaceHistory))
	for i, ref := range s.workspaceHistory {
		ref.Roots = append([]WorkspaceRoot(nil), ref.Roots...)
		ref.Reservations = append([]WorkspaceRoot(nil), ref.Reservations...)
		out[i] = ref
	}
	return out
}
