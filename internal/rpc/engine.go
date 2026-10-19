package rpc

import (
	"context"
	"errors"
	"time"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

// ErrNotFound is what an Engine returns for a session or a profile that
// does not exist; the server answers it with CodeNotFound. ErrInvalid is
// for a request that names things that exist but cannot be used that way
// — a profile without a key, a turn the conversation does not have — and
// is answered with CodeInvalidParams. Any other error is CodeInternal.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
)

// Parent says where a fork's history came from: the session and the user
// turn (counted from 1) it was cut after.
type Parent struct {
	ID   string `json:"id"`
	Turn int    `json:"turn"`
}

// ProfileRef names the provider profile a session answers with, and the
// model when one overrides the profile's own. It is the body of
// session.profile and part of every session view.
type ProfileRef struct {
	Profile string `json:"profile"`
	Model   string `json:"model,omitempty"`
}

// ProfileInfo is one row of profile.list.
type ProfileInfo struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Model string `json:"model,omitempty"`
	// Default and Fallback mark the configured roles.
	Default  bool `json:"default,omitempty"`
	Fallback bool `json:"fallback,omitempty"`
	// Usable says a session could switch to it; Problem says why not
	// (no key, no model) when it cannot. A picker shows both.
	Usable  bool   `json:"usable"`
	Problem string `json:"problem,omitempty"`
}

// ForkOptions are the choices of session.fork: the source session, the
// turn to cut after (0 or omitted: the whole conversation), and whether
// the fork is written to disk.
type ForkOptions struct {
	ID        string `json:"id"`
	Turn      int    `json:"turn,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
}

// CompleteRequest is the body of complete: the text typed so far and the
// session it belongs to, for context. ID is the client's: the result
// echoes it, so a late answer for text that is no longer in the input is
// told from the one being waited for.
type CompleteRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
	Text      string `json:"text"`
}

// CompleteResult is the answer to complete. Suggestion is the
// continuation, empty when the model had none. Disabled says the
// configuration has completion off, with Reason; a client then stops
// asking. Usage is the provider's accounting, so a client can show what
// completion costs.
type CompleteResult struct {
	ID         string          `json:"id"`
	Suggestion string          `json:"suggestion,omitempty"`
	Disabled   bool            `json:"disabled,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	Usage      *agentapi.Usage `json:"usage,omitempty"`
}

// CompletionSettings is the completion block of the configuration as it
// stands on disk, for a client that drives the triggers.
type CompletionSettings struct {
	// Mode is off, tab, idle or both.
	Mode    string `json:"mode"`
	Profile string `json:"profile,omitempty"`
	Model   string `json:"model,omitempty"`
	// IdleAfterMs is the pause that triggers a request in the idle mode;
	// WarnAfterMs how long a request may take before the client warns.
	IdleAfterMs int `json:"idle_after_ms"`
	WarnAfterMs int `json:"warn_after_ms"`
}

// Info is one row of session.list, most recently modified first.
type Info struct {
	ID string `json:"id"`
	// Cwd is the directory the session belongs to: the server's own, but
	// a picker shows it, so the row carries it.
	Cwd         string    `json:"cwd,omitempty"`
	Messages    int       `json:"messages"`
	Created     time.Time `json:"created"`
	Modified    time.Time `json:"modified"`
	FirstPrompt string    `json:"first_prompt,omitempty"`
	// Parent is set for a fork, so a list can draw the tree.
	Parent *Parent `json:"parent,omitempty"`
}

// OpenOptions are the choices of session.open. The zero value is the
// default: a recorded session.
type OpenOptions struct {
	// Ephemeral asks for a session that is not written to disk. It still
	// has an id for the life of the server, so turns can name it.
	Ephemeral bool `json:"ephemeral,omitempty"`
}

// Session is one conversation the server can run a turn on. The server
// guarantees RunTurn is not called twice at once on the same value.
type Session interface {
	ID() string
	Created() time.Time
	Messages() []agentapi.Message
	// Warnings are what loading the session had to repair or skip; a
	// client shows them once when it attaches.
	Warnings() []string
	// Parent is where a fork's history came from; false for a session
	// started from nothing.
	Parent() (Parent, bool)
	// Profile is the provider the session answers with now.
	Profile() ProfileRef
	// SetProfile switches the session to ref's profile (and model, when
	// given) for every turn from now on, and returns what is in effect:
	// the model filled in from the profile when ref left it empty. The
	// server never calls it while a turn is in flight. An unknown profile
	// is ErrNotFound, one that cannot be used ErrInvalid.
	SetProfile(ctx context.Context, ref ProfileRef) (ProfileRef, error)
	// RunTurn records text as the user message and runs the agent. ask
	// receives every permission question; a nil ask denies them. The
	// returned channel carries the turn's events and is closed when the
	// turn ends. RunTurn must return before it needs a receiver on that
	// channel: the server writes the turn id to the client first, and
	// only then starts reading. A non-nil error means the turn never
	// started.
	RunTurn(ctx context.Context, text string, ask permission.Prompter) (<-chan agentapi.Event, error)
	Close() error
}

// Engine opens the sessions a server serves. Implementations are safe for
// concurrent Open, Attach and List. Two Attach calls for one id may return
// two values; the server keeps the first and does not ask twice.
type Engine interface {
	List(ctx context.Context) ([]Info, error)
	Open(ctx context.Context, opts OpenOptions) (Session, error)
	Attach(ctx context.Context, id string) (Session, error)
	// Profiles lists the provider profiles a session may switch to, as
	// the configuration stands now.
	Profiles(ctx context.Context) ([]ProfileInfo, error)
	// Fork starts a session holding opts.ID's history through the end of
	// opts.Turn (the whole history for 0), with the source's profile. The
	// server checks that the source has no turn in flight before calling.
	// An unknown source is ErrNotFound, a turn it does not have — or an
	// empty conversation — ErrInvalid.
	Fork(ctx context.Context, opts ForkOptions) (Session, error)
	// Complete asks a model for a short continuation of req.Text. It
	// blocks for the provider's answer and honours ctx: the server runs
	// it beside the connection's other requests and cancels it on
	// complete.cancel. A disabled configuration is a result, not an
	// error.
	Complete(ctx context.Context, req CompleteRequest) (CompleteResult, error)
	// CompletionSettings reads the completion block as the configuration
	// stands now.
	CompletionSettings(ctx context.Context) (CompletionSettings, error)
	// Models lists the models the profile's endpoint serves, by name. An
	// unknown profile is ErrNotFound.
	Models(ctx context.Context, profile string) ([]string, error)
}
