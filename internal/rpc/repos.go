package rpc

import (
	"context"
	"crypto/rand"
	"time"
)

// RepoRequest describes a user workspace operation; roots are never added from model output.
type RepoRequest struct {
	SessionID   string `json:"session_id"`
	OperationID string `json:"operation_id"`
	Action      string `json:"action"`
	Repo        string `json:"repo,omitempty"`
	Path        string `json:"path,omitempty"`
	Alias       string `json:"alias,omitempty"`
	URL         string `json:"url,omitempty"`
	Cursor      string `json:"cursor,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

// RepoInfo is one explicitly attached filesystem root.
type RepoInfo struct {
	ID          string `json:"id"`
	Alias       string `json:"alias"`
	Path        string `json:"path"`
	PathOmitted bool   `json:"path_omitted,omitempty"`
	Primary     bool   `json:"primary"`
}

// RepoResult returns a text tree or membership snapshot.
type RepoResult struct {
	Text       string     `json:"text"`
	Roots      []RepoInfo `json:"roots,omitempty"`
	Revision   uint64     `json:"revision"`
	NextCursor string     `json:"next_cursor,omitempty"`
	RootCount  int        `json:"root_count,omitempty"`
}

// ReposEngine is optional, so older engines can reject workspace administration.
type ReposEngine interface {
	Repos(context.Context, RepoRequest) (RepoResult, error)
}

// Repos calls session.repos for a held conversation.
func (c *Client) Repos(ctx context.Context, req RepoRequest) (RepoResult, error) {
	req.OperationID = rand.Text()
	var result RepoResult
	err := c.call(ctx, "session.repos", req, &result)
	if ctx.Err() != nil {
		cancelCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		var ignored any
		_ = c.call(cancelCtx, "session.repos.cancel", struct {
			OperationID string `json:"operation_id"`
		}{req.OperationID}, &ignored)
	}
	return result, err
}

func (c *conn) sessionRepos(req request) {
	var p RepoRequest
	if err := unmarshalParams(req.Params, &p); err != nil || p.SessionID == "" || p.OperationID == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "session_id and valid workspace arguments are required"))
		return
	}
	if c.srv.lookup(p.SessionID) == nil {
		_ = c.writeError(req.ID, errCode(CodeNotFound, "attach the session before using /repos"))
		return
	}
	readOnly := p.Action == "list" || p.Action == "tree" || (p.Action == "index" && p.Mode == "")
	if !readOnly && c.srv.busy(p.SessionID) {
		_ = c.writeError(req.ID, errCode(CodeBusy, "workspace changes require an idle session"))
		return
	}
	engine, ok := c.srv.Engine.(ReposEngine)
	if !ok {
		_ = c.writeError(req.ID, errCode(CodeMethodNotFound, "workspace administration is unavailable"))
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.reposMu.Lock()
	if c.repos == nil {
		c.repos = make(map[string]context.CancelFunc)
	}
	if len(c.repos) >= 16 || c.repos[p.OperationID] != nil {
		c.reposMu.Unlock()
		cancel()
		_ = c.writeError(req.ID, errCode(CodeBusy, "too many workspace operations"))
		return
	}
	c.repos[p.OperationID] = cancel
	c.reposMu.Unlock()
	go func() {
		defer cancel()
		defer func() { c.reposMu.Lock(); delete(c.repos, p.OperationID); c.reposMu.Unlock() }()
		result, err := engine.Repos(ctx, p)
		if err != nil {
			_ = c.writeError(req.ID, engineError(err))
			return
		}
		_ = c.writeResult(req.ID, result)
	}()
}

func (c *conn) cancelRepos(req request) {
	var p struct {
		OperationID string `json:"operation_id"`
	}
	if err := unmarshalParams(req.Params, &p); err != nil || p.OperationID == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "operation_id is required"))
		return
	}
	c.reposMu.Lock()
	cancel := c.repos[p.OperationID]
	c.reposMu.Unlock()
	if cancel != nil {
		cancel()
	}
	_ = c.writeResult(req.ID, struct{}{})
}
