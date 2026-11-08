package rpc

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type reposEngine struct {
	*fakeEngine
	started chan struct{}
	stopped chan struct{}
}

func (e *reposEngine) Repos(ctx context.Context, req RepoRequest) (RepoResult, error) {
	if req.Action == "clone" {
		close(e.started)
		<-ctx.Done()
		close(e.stopped)
		return RepoResult{}, ctx.Err()
	}
	return RepoResult{Text: "roots", Revision: 7}, nil
}

func TestReposOptionalEngineAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := &reposEngine{fakeEngine: &fakeEngine{sessions: map[string]*fakeSession{}}, started: make(chan struct{}), stopped: make(chan struct{})}
	srv := &Server{Engine: engine}
	client, peer := net.Pipe()
	go func() { _ = srv.ServeConn(ctx, peer) }()
	cl, err := Dial(ctx, client, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close(); srv.Close() }()
	view, err := cl.OpenSession(ctx, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := cl.Repos(ctx, RepoRequest{SessionID: view.ID, Action: "list"})
	if err != nil || got.Revision != 7 {
		t.Fatalf("list: %+v %v", got, err)
	}
	callCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := cl.Repos(callCtx, RepoRequest{SessionID: view.ID, Action: "clone"}); done <- err }()
	<-engine.started
	// The read loop remains live while the clone operation waits.
	if _, err := cl.ListSessions(ctx); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("client cancellation: %v", err)
	}
	select {
	case <-engine.stopped:
	case <-time.After(time.Second):
		t.Fatal("server clone was not canceled")
	}
}

func TestReposUnknownAndUnsupported(t *testing.T) {
	cl, _ := dialFake(t, &Server{})
	ctx := context.Background()
	var rpcErr *Error
	if _, err := cl.Repos(ctx, RepoRequest{SessionID: "missing", Action: "list"}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeNotFound {
		t.Fatalf("unknown session: %v", err)
	}
	view, err := cl.OpenSession(ctx, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Repos(ctx, RepoRequest{SessionID: view.ID, Action: "list"}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("legacy engine: %v", err)
	}
}
