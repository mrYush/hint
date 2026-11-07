package repomap

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
	"github.com/mrYush/hint/pkg/agentapi"
)

type listArgs struct {
	Repo    string `json:"repo,omitempty"`
	Path    string `json:"path,omitempty"`
	EntryID string `json:"entry_id,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
	Status  bool   `json:"status,omitempty"`
}

type listTool struct {
	manager   *Manager
	space     *workspace.Session
	lowBudget bool
}

func (*listTool) Name() string { return "repo_list" }
func (t *listTool) Description() string {
	description := "List attached roots, a paginated text tree for repo/path or entry_id, or detailed index diagnostics with status=true alone. Continue using only cursor. The tree shows current observations and may still be building."
	if t.lowBudget {
		description += " repo_map is unavailable: output budget below 1024 bytes."
	}
	return description
}
func (*listTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (*listTool) InputSchema() json.RawMessage { return listSchema }

var listSchema = tool.MustSchema(listArgs{})

func (t *listTool) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in listArgs
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return tool.InvalidArgs(callID, t.Name(), err), nil
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return tool.InvalidArgs(callID, t.Name(), errors.New("expected one object")), nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return tool.InvalidArgs(callID, t.Name(), err), nil
	}
	if fields == nil {
		return tool.InvalidArgs(callID, t.Name(), errors.New("expected an object")), nil
	}
	if _, status := fields["status"]; status && (!in.Status || len(fields) != 1) {
		return tool.InvalidArgs(callID, t.Name(), errors.New("status must be true and the only field")), nil
	}
	if _, cursor := fields["cursor"]; cursor && (len(fields) != 1 || in.Cursor == "") {
		return agentapi.ErrorResult(callID, t.Name(), "cursor must be the only argument"), nil
	}
	if _, entry := fields["entry_id"]; entry {
		if _, path := fields["path"]; path || in.EntryID == "" {
			return tool.InvalidArgs(callID, t.Name(), errors.New("entry_id must be nonempty and cannot be combined with path")), nil
		}
		target, err := t.manager.ResolveEntry(ctx, t.space, in.Repo, in.EntryID)
		if err != nil {
			return agentapi.ErrorResult(callID, t.Name(), err.Error()), nil
		}
		in.Path = target.RelativePath
	}
	budget := MaxOutputBytes
	if n := tool.OutputBudget(ctx); n > 0 {
		budget = min(budget, n)
	}
	if in.Status {
		text := t.manager.StatusText()
		if t.lowBudget {
			text = "repo_map unavailable: output_budget_below_1024\n" + text
			if len(text) > budget {
				text = "repo_map unavailable: output_budget_below_1024"
			}
		}
		if len(text) > budget {
			return agentapi.ErrorResult(callID, t.Name(), "index diagnostics exceed the configured output budget"), nil
		}
		return agentapi.TextResult(callID, t.Name(), text), nil
	}
	var text string
	var err error
	roots := in.Repo == "" && in.Path == "" && in.Cursor == ""
	if in.Cursor != "" {
		state, decodeErr := t.manager.decodeTreeCursor(t.space, in.Cursor)
		if decodeErr != nil {
			return listFailure(callID, fmt.Errorf("stale_cursor: restart the listing: %w", decodeErr))
		}
		roots = state.Kind == "roots"
	}
	if roots {
		text, err = t.manager.listRoots(t.space, in.Cursor, budget)
	} else {
		text, err = t.manager.tree(ctx, t.space, in.Repo, in.Path, in.Cursor, budget)
	}
	if err != nil {
		if ctx.Err() != nil {
			return agentapi.ToolResult{}, ctx.Err()
		}
		return agentapi.ErrorResult(callID, t.Name(), err.Error()), nil
	}
	return agentapi.TextResult(callID, t.Name(), text), nil
}

func listFailure(callID string, err error) (agentapi.ToolResult, error) {
	return agentapi.ErrorResult(callID, "repo_list", err.Error()), nil
}

type treeCursor struct {
	Kind     string `json:"k"`
	Session  string `json:"s"`
	Revision uint64 `json:"v"`
	Root     string `json:"r,omitempty"`
	Scope    string `json:"p,omitempty"`
	After    string `json:"a,omitempty"`
}

func (m *Manager) encodeTreeCursor(state treeCursor) string {
	data, _ := json.Marshal(state)
	mac := hmac.New(sha256.New, m.secret[:])
	_, _ = mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(append(data, mac.Sum(nil)[:16]...))
}

func (m *Manager) decodeTreeCursor(space *workspace.Session, token string) (treeCursor, error) {
	var state treeCursor
	if len(token) > 1024 {
		return state, index.ErrStale
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(data) < 17 {
		return state, index.ErrStale
	}
	body := data[:len(data)-16]
	mac := hmac.New(sha256.New, m.secret[:])
	_, _ = mac.Write(body)
	if !hmac.Equal(mac.Sum(nil)[:16], data[len(data)-16:]) || json.Unmarshal(body, &state) != nil {
		return state, index.ErrStale
	}
	_, revision := space.Snapshot()
	if state.Session != space.ID() || state.Revision != revision {
		return state, index.ErrStale
	}
	return state, nil
}

func (m *Manager) listRoots(space *workspace.Session, cursor string, budget int) (output string, resultErr error) {
	if budget < MinOutputBytes {
		return "", errors.New("listing output requires at least 1024 bytes")
	}
	roots, revision := space.Snapshot()
	defer func() {
		_, currentRevision := space.Snapshot()
		if currentRevision != revision {
			output, resultErr = "", workspace.ErrChanged
		}
	}()
	state := treeCursor{Kind: "roots", Session: space.ID(), Revision: revision}
	if cursor != "" {
		var err error
		state, err = m.decodeTreeCursor(space, cursor)
		if err != nil || state.Kind != "roots" {
			return "", index.ErrStale
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Workspace revision %d\n", revision)
	started := state.After == ""
	for i, root := range roots {
		if !started {
			started = root.ID == state.After
			continue
		}
		status := m.status(root.ID)
		line := fmt.Sprintf("%s [%s] %s %s\n  %s\n", quoteTreeDisplay(root.Alias, 80), root.ID,
			status.State, status.Reason, quoteTreeDisplay(root.Path, 160))
		next := state
		next.After = root.ID
		reserve := 0
		if i+1 < len(roots) {
			reserve = len(m.treeFooter(next))
		}
		if out.Len()+len(line)+reserve > budget {
			out.WriteString(m.treeFooter(state))
			return out.String(), nil
		}
		out.WriteString(line)
		state.After = root.ID
	}
	return out.String(), nil
}

func (m *Manager) treeFooter(state treeCursor) string {
	return "next_cursor: " + m.encodeTreeCursor(state) + "\n"
}

// quoteTreeDisplay bounds escaped display bytes, including control characters.
// Exact paths remain addressable through the opaque entry or root handle.
func quoteTreeDisplay(value string, budget int) string {
	value = abbreviate(value, budget)
	for {
		quoted := strconv.Quote(value)
		if len(quoted) <= budget {
			return quoted
		}
		value = abbreviate(value, max(4, len(value)/2))
	}
}

// Tree renders one level of current catalog observations without materializing
// all children. A continuation preserves scope and membership, not a live snapshot.
func (m *Manager) Tree(ctx context.Context, space *workspace.Session, repo, path, cursor string) (string, error) {
	return m.tree(ctx, space, repo, path, cursor, MaxOutputBytes)
}

func (m *Manager) tree(ctx context.Context, space *workspace.Session, repo, path, cursor string, budget int) (string, error) {
	if budget < MinOutputBytes {
		return "", errors.New("tree output requires at least 1024 bytes")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ctx, release, err := m.acquireFocused(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	var lease workspace.Lease
	var after []byte
	state := treeCursor{Kind: "tree", Session: space.ID()}
	if cursor != "" {
		state, err = m.decodeTreeCursor(space, cursor)
		if err != nil || state.Kind != "tree" {
			return "", index.ErrStale
		}
		path = "."
		if state.Scope != "" {
			file, lookupErr := m.store.EntryLookup(ctx, state.Root, state.Scope)
			if lookupErr != nil {
				return "", index.ErrStale
			}
			path = string(file.Path)
		}
		repo = state.Root
		if state.After != "" {
			file, lookupErr := m.store.EntryLookup(ctx, state.Root, state.After)
			if lookupErr != nil {
				return "", index.ErrStale
			}
			after = file.Path
		}
	}
	lease, err = space.Resolve(repo, path)
	if err != nil {
		return "", err
	}
	info, err := lease.Stat()
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("tree scope must be a directory")
	}
	if err := m.excluded(ctx, lease, true); err != nil {
		return "", err
	}
	state.Root, state.Revision = lease.RootID, lease.MembershipRevision
	parent := []byte(filepath.ToSlash(lease.RelativePath))
	if lease.RelativePath == "." {
		parent = nil
	} else {
		state.Scope = workspace.EntryID(lease.RootID, string(parent))
		// Focused navigation must install its own scope handle before returning
		// a continuation, even if background discovery has not reached it.
		if _, err := m.store.EntryLookup(ctx, lease.RootID, state.Scope); errors.Is(err, index.ErrNotFound) {
			if err := lease.Publish(func() error {
				_, err := m.store.PutSource(ctx, index.File{RootID: lease.RootID, ID: state.Scope,
					Path: parent, Kind: index.KindDirectory, State: index.StatePending,
					ModTime: info.ModTime(), ObservedAt: time.Now()}, nil)
				return err
			}); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		}
	}
	m.Track(space)
	page, err := m.store.ListEntries(ctx, lease.RootID, parent, after, 32, 256<<10)
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(page.Files))
	for _, file := range page.Files {
		paths = append(paths, string(file.Path))
	}
	ignored, err := m.gitIgnored(ctx, lease, paths)
	if err != nil {
		return "", err
	}
	status := m.status(lease.RootID)
	var out strings.Builder
	fmt.Fprintf(&out, "%s [%s] — %s %s\nCurrent observations; discovery may be incomplete.\n",
		quoteTreeDisplay(lease.RelativePath, 80), lease.RootID, status.State, status.Reason)
	complete := page.Exhausted
	for i, file := range page.Files {
		if ctx.Err() != nil {
			complete = false
			break
		}
		if project.Basic.Ignored(string(file.Path), file.Kind != index.KindFile) || ignored[string(file.Path)] {
			state.After = file.ID
			continue
		}
		child, err := space.Resolve(lease.RootID, string(file.Path))
		boundary := errors.Is(err, workspace.ErrBoundary) && file.Kind == index.KindBoundary
		if err == nil {
			_, err = child.Stat()
		}
		if err != nil && !boundary {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, tool.ErrDenied) || errors.Is(err, workspace.ErrBoundary) {
				state.After = file.ID
				continue
			}
			return "", err
		}
		if file.Kind == index.KindBoundary && !boundary {
			state.After = file.ID
			continue
		}
		reason := file.Reason
		if boundary {
			// A detached root reserves its ownership boundary even if its
			// directory is offline or gone. Do not imply live existence.
			reason = "ownership reservation; availability unverified"
		}
		line := fmt.Sprintf("  %s [%s] %s %s\n", quoteTreeDisplay(filepath.Base(string(file.Path)), 96), file.ID, file.Kind, reason)
		next := state
		next.After = file.ID
		reserve := 0
		if !page.Exhausted || i+1 < len(page.Files) {
			reserve = len(m.treeFooter(next))
		}
		if out.Len()+len(line)+reserve > budget {
			complete = false
			break
		}
		out.WriteString(line)
		state.After = file.ID
	}
	if !complete {
		out.WriteString(m.treeFooter(state))
	}
	if err := space.Publish(lease, func() error { return nil }); err != nil {
		return "", err
	}
	if out.Len() > budget {
		return "", errors.New("tree envelope exceeds output budget")
	}
	return out.String(), nil
}
