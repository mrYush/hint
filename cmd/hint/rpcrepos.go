package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/repomap"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/internal/workspace"
)

// Repos administers only the workspace belonging to an already open session.
func (e *rpcEngine) Repos(ctx context.Context, req rpc.RepoRequest) (rpc.RepoResult, error) {
	e.mu.Lock()
	s := e.live[req.SessionID]
	e.mu.Unlock()
	if s == nil {
		return rpc.RepoResult{}, rpc.ErrNotFound
	}
	if s.workspace == nil {
		return rpc.RepoResult{}, fmt.Errorf("%w: workspace unavailable", rpc.ErrInvalid)
	}
	if err := validateRepoRequest(req); err != nil {
		return rpc.RepoResult{}, err
	}
	if req.Action == "index" {
		if e.mapIndex == nil {
			return rpc.RepoResult{}, fmt.Errorf("%w: tree index unavailable", rpc.ErrInvalid)
		}
		if req.Mode != "" {
			if !e.indexModeMu.TryLock() {
				return rpc.RepoResult{}, rpc.ErrBusy
			}
			defer e.indexModeMu.Unlock()
			if err := e.mapIndex.SetMode(repomap.Mode(req.Mode)); err != nil {
				return rpc.RepoResult{}, fmt.Errorf("%w: %w", rpc.ErrInvalid, err)
			}
		}
		return rpc.RepoResult{Text: "Index mode applies to all sessions in this Hint process.\n" + e.mapIndex.StatusText()}, nil
	}
	if req.Action == "tree" {
		if e.mapIndex == nil {
			return rpc.RepoResult{}, fmt.Errorf("%w: tree index unavailable", rpc.ErrInvalid)
		}
		text, err := e.mapIndex.Tree(ctx, s.workspace, req.Repo, req.Path, req.Cursor)
		return rpc.RepoResult{Text: text}, err
	}
	if req.Action != "list" {
		if !s.reposMu.TryLock() {
			return rpc.RepoResult{}, rpc.ErrBusy
		}
		defer s.reposMu.Unlock()
		err := s.workspace.Update(func(draft *workspace.Session) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			switch req.Action {
			case "add":
				path := req.Path
				if !filepath.IsAbs(path) {
					path = filepath.Join(e.cwd, path)
				}
				_, err := draft.AttachLocal(path, req.Alias)
				return err
			case "remove":
				return draft.Detach(req.Repo)
			case "clone":
				path := req.Path
				if !filepath.IsAbs(path) {
					path = filepath.Join(e.cwd, path)
				}
				if err := cloneRepo(ctx, req.URL, path, e.environ()); err != nil {
					return err
				}
				_, err := draft.AttachLocal(path, req.Alias)
				if err != nil {
					return fmt.Errorf("clone completed at %q but root was not attached: %w", path, err)
				}
				return nil
			}
			return rpc.ErrInvalid
		}, func(roots []workspace.RootInfo, revision uint64) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return s.persistWorkspace(roots, revision)
		})
		if err != nil {
			if errors.Is(err, workspace.ErrBusy) {
				return rpc.RepoResult{}, fmt.Errorf("%w: workspace changes require an idle session", rpc.ErrBusy)
			}
			return rpc.RepoResult{}, fmt.Errorf("%w: %s", rpc.ErrInvalid, configSafeRepoError(e, err))
		}
		if e.mapIndex != nil {
			e.mapIndex.Track(s.workspace)
		}
	}
	return s.repoListing(req.Cursor)
}

func configSafeRepoError(e *rpcEngine, err error) string {
	// Clone errors never contain URLs or subprocess output; the standard
	// engine redactor still protects configured credentials in filesystem errors.
	return config.Redact(err.Error(), e.secrets())
}

func validateRepoRequest(req rpc.RepoRequest) error {
	if req.Action != "index" && req.Mode != "" {
		return fmt.Errorf("%w: index mode requires the index action", rpc.ErrInvalid)
	}
	valid := false
	switch req.Action {
	case "index":
		valid = req.Repo == "" && req.Path == "" && req.Alias == "" && req.URL == "" && req.Cursor == "" && (req.Mode == "" || req.Mode == "off" || req.Mode == "auto" || req.Mode == "on")
	case "list":
		valid = req.Repo == "" && req.Path == "" && req.Alias == "" && req.URL == ""
	case "add":
		valid = req.Path != "" && req.Repo == "" && req.URL == "" && req.Cursor == ""
	case "remove":
		valid = req.Repo != "" && req.Path == "" && req.Alias == "" && req.URL == "" && req.Cursor == ""
	case "clone":
		valid = req.Path != "" && req.URL != "" && req.Repo == "" && req.Cursor == ""
	case "tree":
		valid = req.Alias == "" && req.URL == "" && (req.Cursor == "" || (req.Path == "" && req.Repo == ""))
	}
	if !valid {
		return fmt.Errorf("%w: invalid /repos action or arguments", rpc.ErrInvalid)
	}
	return nil
}

func (s *rpcSession) persistWorkspace(roots []workspace.RootInfo, revision uint64) error {
	ref := session.WorkspaceRef{Version: 2, Revision: revision}
	if s.file != nil {
		ref.AfterTurn = s.file.UserTurns()
	} else {
		ref.AfterTurn = s.repoTurns
	}
	for _, root := range roots {
		ref.Roots = append(ref.Roots, session.WorkspaceRoot{ID: root.ID, Alias: root.Alias, Path: root.Path, Primary: root.Primary})
	}
	seen := make(map[string]bool)
	for _, root := range append(s.workspace.Reservations(), roots...) {
		if seen[root.Alias] {
			continue
		}
		seen[root.Alias] = true
		ref.Reservations = append(ref.Reservations, session.WorkspaceRoot{ID: root.ID, Alias: root.Alias, Path: root.Path, Primary: root.Primary})
	}
	if s.file != nil {
		return s.file.AppendWorkspace(ref)
	}
	s.repoHistory = append(s.repoHistory, ref)
	return nil
}

// restoreWorkspace runs before building this session's tool set.
func (s *rpcSession) restoreWorkspace() {
	if s.workspace == nil || s.file == nil {
		return
	}
	s.reserveWorkspaceHistory(s.file.WorkspaceHistory())
	s.restoreWorkspaceRef(s.file.Workspace())
}

func (s *rpcSession) restoreWorkspaceRef(ref session.WorkspaceRef) {
	if s.workspace == nil || ref.Version == 0 {
		return
	}
	for _, root := range ref.Roots {
		if root.Primary {
			continue
		}
		restored, restoreErr := workspace.RestoreInfo(workspace.RootInfo{ID: root.ID, Alias: root.Alias, Path: root.Path, Primary: root.Primary})
		if restoreErr != nil {
			s.workspace.FailClosed()
			s.warnings = append(s.warnings, "workspace disabled: saved root identity is invalid")
			return
		}
		info, err := s.workspace.AttachLocal(root.Path, root.Alias)
		if err != nil {
			s.warnings = append(s.warnings, fmt.Sprintf("workspace root %q is unavailable: %s", root.Alias, configSafeRepoError(s.eng, err)))
			continue
		}
		if info.ID != restored.ID {
			_ = s.workspace.Detach(info.ID)
			s.warnings = append(s.warnings, fmt.Sprintf("workspace root %q changed identity; attach it explicitly", root.Alias))
		}
	}
	s.workspace.AdvanceRevision(ref.Revision)
	if s.eng.mapIndex != nil {
		s.eng.mapIndex.Track(s.workspace)
	}
}

func (e *rpcEngine) sourceWorkspace(id string, turn int) ([]session.WorkspaceRef, error) {
	if live := e.liveSession(id); live != nil {
		if !live.reposMu.TryLock() {
			return nil, rpc.ErrBusy
		}
		defer live.reposMu.Unlock()
		if live.file != nil {
			return live.file.WorkspaceHistoryAtTurn(turn), nil
		}
		return session.WorkspaceHistoryForTurn(live.repoHistory, live.repoTurns, session.Turns(live.rec.Messages()), turn), nil
	}
	file, err := e.store.Peek(e.cwd, id)
	if err != nil {
		return nil, sessionLookupError(err, id)
	}
	defer func() { _ = file.Close() }()
	return file.WorkspaceHistoryAtTurn(turn), nil
}

func (s *rpcSession) reserveWorkspaceHistory(history []session.WorkspaceRef) {
	if s.workspace == nil {
		return
	}
	for _, ref := range history {
		for _, root := range append(append([]session.WorkspaceRoot(nil), ref.Reservations...), ref.Roots...) {
			if err := s.workspace.Reserve(workspace.RootInfo{ID: root.ID, Alias: root.Alias, Path: root.Path, Primary: root.Primary}); err != nil {
				s.workspace.FailClosed()
				s.warnings = append(s.warnings, fmt.Sprintf("workspace disabled: reservation %q could not be restored: %v", root.Alias, err))
				return
			}
		}
	}
}
