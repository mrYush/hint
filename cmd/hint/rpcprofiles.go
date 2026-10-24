package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/pkg/agentapi"
)

// The profile half of the engine (WP1.5): a session answers with the
// provider of a profile chosen for it, not with the one the process was
// started with. The configuration is re-read on every switch, so a key
// that setup wrote a moment ago is in force without a restart.

// defaultRef names the default profile with its own model: what a
// session answers with until it is switched.
func (e *rpcEngine) defaultRef() rpc.ProfileRef {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	ref := rpc.ProfileRef{Profile: e.cfg.DefaultProvider}
	if p, err := e.cfg.Default(); err == nil {
		ref.Model = p.Model
	}
	return ref
}

// refresh re-reads the configuration when the engine knows how to, and
// drops the cached providers when it did: the default profile's key may
// have changed under them. The lock is the caller's.
func (e *rpcEngine) refresh() error {
	if e.reload == nil {
		return nil
	}
	cfg, err := e.reload()
	if err != nil {
		return fmt.Errorf("reloading the configuration: %w", err)
	}
	e.cfg = cfg
	e.providers = map[rpc.ProfileRef]agentapi.ChatProvider{}
	e.setup = newSetupInfo(cfg, e.setup.cwd, e.setup.flags)
	return nil
}

// providerFor returns the provider for ref, building and caching it on
// first use, with the profile it stands for. reload says whether to
// re-read the configuration first: a switch the user asked for does, so
// that it sees what setup just wrote; restoring a session's recorded
// profile on attach does not.
func (e *rpcEngine) providerFor(ref rpc.ProfileRef, reload bool) (agentapi.ChatProvider, config.Profile, error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if reload {
		if err := e.refresh(); err != nil {
			return nil, config.Profile{}, err
		}
	}
	own, err := e.cfg.Profile(ref.Profile)
	if err != nil {
		return nil, config.Profile{}, fmt.Errorf("%w: %s", rpc.ErrNotFound, strings.TrimPrefix(err.Error(), "config: "))
	}
	// The cache is keyed by the profile and the model that differs from
	// its own: a switch to the default profile, with or without its model
	// spelled out, finds the provider the engine started with.
	if ref.Model == own.Model {
		ref.Model = ""
	}
	if chat := e.providers[ref]; chat != nil {
		p, err := e.cfg.Usable(ref.Profile, "selected")
		if err != nil {
			return nil, config.Profile{}, fmt.Errorf("%w: %s", rpc.ErrInvalid, strings.TrimPrefix(err.Error(), "config: "))
		}
		if ref.Model != "" {
			p.Model = ref.Model
		}
		return chat, p, nil
	}
	chat, p, err := provider.ForProfile(e.cfg, ref.Profile, ref.Model, e.notices, e.debugLog)
	if err != nil {
		return nil, config.Profile{}, fmt.Errorf("%w: %s", rpc.ErrInvalid, strings.TrimPrefix(err.Error(), "config: "))
	}
	e.providers[ref] = chat
	return chat, p, nil
}

// Profiles lists the configured profiles as they stand on disk now.
func (e *rpcEngine) Profiles(context.Context) ([]rpc.ProfileInfo, error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if err := e.refresh(); err != nil {
		return nil, err
	}
	out := make([]rpc.ProfileInfo, 0, len(e.cfg.Providers))
	for _, p := range e.cfg.Providers {
		info := rpc.ProfileInfo{
			Name:     p.Name,
			Kind:     string(p.Kind),
			Model:    p.Model,
			Default:  p.Name == e.cfg.DefaultProvider,
			Fallback: p.Name == e.cfg.FallbackProvider,
			Usable:   true,
		}
		if _, err := e.cfg.Usable(p.Name, "selected"); err != nil {
			info.Usable = false
			info.Problem = strings.TrimPrefix(err.Error(), "config: ")
		}
		out = append(out, info)
	}
	return out, nil
}

// Fork starts a session from opts.ID's history through the end of turn
// opts.Turn. A source that is open on this engine is read as it stands;
// one that is not is loaded from its file. The fork is recorded unless
// opts.Ephemeral, and answers with the source's profile.
func (e *rpcEngine) Fork(_ context.Context, opts rpc.ForkOptions) (rpc.Session, error) {
	messages, profile, err := e.source(opts.ID)
	if err != nil {
		return nil, err
	}
	if opts.Turn == 0 {
		opts.Turn = session.Turns(messages)
		if opts.Turn == 0 {
			return nil, fmt.Errorf("%w: session %s has no turns to fork", rpc.ErrInvalid, opts.ID)
		}
	}
	cut, err := session.UpToTurn(messages, opts.Turn)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", rpc.ErrInvalid, strings.TrimPrefix(err.Error(), "session: "))
	}
	parent := session.Parent{ID: opts.ID, Turn: opts.Turn}
	if !opts.Ephemeral {
		sess, err := e.store.Fork(e.cwd, parent, cut, profile)
		if err != nil {
			return nil, err
		}
		return e.adopt(sess, sess.ID()), nil
	}
	id, err := session.NewID()
	if err != nil {
		return nil, err
	}
	live := e.adopt(nil, id)
	live.rec.Seed(cut)
	live.parent = &rpc.Parent{ID: parent.ID, Turn: parent.Turn}
	if profile.Name != "" {
		if err := live.switchTo(rpc.ProfileRef{Profile: profile.Name, Model: profile.Model}, false); err != nil {
			live.warnings = append(live.warnings, fmt.Sprintf("the source's profile is not usable (%s); answering with the default profile instead", strings.TrimPrefix(err.Error(), "config: ")))
		}
	}
	return live, nil
}

// source returns the conversation and recorded profile of session id:
// from the live session when it is open here, else from its file. An id
// may be a unique prefix, as for session.attach.
func (e *rpcEngine) source(id string) ([]agentapi.Message, session.ProfileRef, error) {
	e.mu.Lock()
	live := e.live[id]
	e.mu.Unlock()
	if live != nil {
		ref := live.Asked()
		return live.rec.Messages(), session.ProfileRef{Name: ref.Profile, Model: ref.Model}, nil
	}
	sess, err := e.store.Find(e.cwd, id)
	if err != nil {
		if errors.Is(err, session.ErrNoSessions) || errors.Is(err, session.ErrUnknownSession) {
			return nil, session.ProfileRef{}, fmt.Errorf("%w: %s", rpc.ErrNotFound, strings.TrimPrefix(err.Error(), "session: "))
		}
		return nil, session.ProfileRef{}, err
	}
	defer func() { _ = sess.Close() }()
	return sess.Messages(), sess.Profile(), nil
}

// SetProfile switches the session to ref for the turns from now on and
// records the switch in its file. It re-reads the configuration first,
// which is how a key written by setup reaches a running core.
func (s *rpcSession) SetProfile(_ context.Context, ref rpc.ProfileRef) (rpc.ProfileRef, error) {
	if err := s.switchTo(ref, true); err != nil {
		return rpc.ProfileRef{}, err
	}
	if s.file != nil {
		// What was asked is recorded, not what it resolved to: a model
		// left to the profile follows the profile when its config changes.
		if err := s.file.AppendProfile(session.ProfileRef{Name: ref.Profile, Model: ref.Model}); err != nil {
			return rpc.ProfileRef{}, err
		}
	}
	return s.Profile(), nil
}

// switchTo rebuilds the session's agent over ref's provider. The gate,
// the recorder and the preamble stay: an "always" grant and the
// conversation survive a change of model.
func (s *rpcSession) switchTo(ref rpc.ProfileRef, reload bool) error {
	chat, p, err := s.eng.providerFor(ref, reload)
	if err != nil {
		return err
	}
	s.use(chat, ref, rpc.ProfileRef{Profile: ref.Profile, Model: p.Model}, limitsFor(p))
	s.tracef("session %s profile %s model %s window %d", s.id, ref.Profile, p.Model, p.ContextWindow)
	return nil
}

// use installs a new agent over chat, under limits. asked is the profile
// as requested (the model only when it overrides the profile's), effective
// the same with the model filled in.
func (s *rpcSession) use(chat agentapi.ChatProvider, asked, effective rpc.ProfileRef, limits agent.Limits) {
	ag := agent.New(chat,
		agent.WithTools(s.eng.registry.Tools()...),
		agent.WithAuthorizer(s.gate),
		agent.WithLimits(limits),
		agent.WithPreamble(s.pre),
	)
	s.agMu.Lock()
	s.ag, s.asked, s.profile = ag, asked, effective
	s.agMu.Unlock()
}

// Asked is the profile as the session was switched to it: what a fork
// records so that it follows the same profile.
func (s *rpcSession) Asked() rpc.ProfileRef {
	s.agMu.Lock()
	defer s.agMu.Unlock()
	return s.asked
}
