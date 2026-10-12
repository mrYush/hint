package main

import (
	"context"
	"io"
	"sync"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// rpcEngine is the server's view of the CLI stack: one working directory,
// one provider, and a session file per conversation. Each session has its
// own permission gate, so an "always" grant does not leak into another
// conversation.
type rpcEngine struct {
	cwd      string
	store    *session.Store
	chat     agentapi.ChatProvider
	registry *tool.Registry
	pc       *project.Context
	mode     permission.Mode
	limits   agent.Limits
	notices  io.Writer

	mu   sync.Mutex
	live map[string]*rpcSession
}

func (e *rpcEngine) List(context.Context) ([]rpc.Info, error) {
	infos, err := e.store.List(e.cwd)
	if err != nil {
		return nil, err
	}
	out := make([]rpc.Info, 0, len(infos))
	for _, info := range infos {
		out = append(out, rpc.Info{
			ID: info.ID, Messages: info.Messages, Created: info.Created, FirstPrompt: info.FirstPrompt,
		})
	}
	return out, nil
}

func (e *rpcEngine) Open(context.Context) (rpc.Session, error) {
	sess, err := e.store.Create(e.cwd)
	if err != nil {
		return nil, err
	}
	return e.adopt(sess), nil
}

func (e *rpcEngine) Attach(_ context.Context, id string) (rpc.Session, error) {
	e.mu.Lock()
	if live := e.live[id]; live != nil {
		e.mu.Unlock()
		return live, nil
	}
	e.mu.Unlock()
	sess, err := e.store.Find(e.cwd, id)
	if err != nil {
		return nil, err
	}
	return e.adopt(sess), nil
}

// adopt registers sess, or returns the session already registered under
// the same id and closes the duplicate file.
func (e *rpcEngine) adopt(sess *session.Session) *rpcSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.live == nil {
		e.live = map[string]*rpcSession{}
	}
	if live := e.live[sess.ID()]; live != nil {
		_ = sess.Close()
		return live
	}
	slot := &promptSlot{}
	gate := permission.New(e.mode, slot)
	pre := newPreamble(e.pc, e.registry, e.notices, nil)
	ag := agent.New(e.chat,
		agent.WithTools(e.registry.Tools()...),
		agent.WithAuthorizer(gate),
		agent.WithLimits(e.limits),
		agent.WithPreamble(pre),
	)
	live := &rpcSession{
		file: sess,
		rec:  session.NewRecorder(sess, len(pre.initial())),
		pre:  pre,
		ag:   ag,
		slot: slot,
	}
	e.live[sess.ID()] = live
	return live
}

// rpcSession is one open conversation. The server does not call RunTurn
// twice at once.
type rpcSession struct {
	file *session.Session
	rec  *session.Recorder
	pre  *preamble
	ag   *agent.Agent
	slot *promptSlot

	closeOnce sync.Once
	closeErr  error
}

func (s *rpcSession) ID() string                   { return s.file.ID() }
func (s *rpcSession) Messages() []agentapi.Message { return s.rec.Messages() }

func (s *rpcSession) RunTurn(ctx context.Context, text string, ask permission.Prompter) (<-chan agentapi.Event, error) {
	s.slot.set(ask)
	user := agentapi.UserMessage(text)
	if err := s.rec.Append(user); err != nil {
		s.slot.set(nil)
		return nil, err
	}
	history := append(append([]agentapi.Message{}, s.pre.initial()...), s.rec.Messages()...)
	out := s.ag.RunTurn(ctx, history)
	ch := make(chan agentapi.Event)
	go func() {
		defer close(ch)
		defer s.slot.set(nil)
		for ev := range out {
			s.rec.Observe(ev)
			ch <- ev
		}
	}()
	return ch, nil
}

func (s *rpcSession) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.file.Close() })
	return s.closeErr
}

// promptSlot is the gate's Prompter. RunTurn points it at the connection
// that owns the turn and clears it when the turn ends, so a question
// never blocks on a client that has already disconnected.
type promptSlot struct {
	mu sync.Mutex
	p  permission.Prompter
}

func (s *promptSlot) set(p permission.Prompter) {
	s.mu.Lock()
	s.p = p
	s.mu.Unlock()
}

func (s *promptSlot) Prompt(ctx context.Context, ask permission.Ask) (permission.Decision, error) {
	s.mu.Lock()
	p := s.p
	s.mu.Unlock()
	if p == nil {
		return permission.Deny, nil
	}
	return p.Prompt(ctx, ask)
}
