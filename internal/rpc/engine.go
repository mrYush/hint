package rpc

import (
	"context"
	"time"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

// Info is one row of session.list.
type Info struct {
	ID          string    `json:"id"`
	Messages    int       `json:"messages"`
	Created     time.Time `json:"created"`
	FirstPrompt string    `json:"first_prompt,omitempty"`
}

// Session is one conversation the server can run a turn on. The server
// guarantees RunTurn is not called twice at once on the same value.
type Session interface {
	ID() string
	Messages() []agentapi.Message
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
	Open(ctx context.Context) (Session, error)
	Attach(ctx context.Context, id string) (Session, error)
}
