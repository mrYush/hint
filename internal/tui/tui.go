// Package tui is the full-screen shell of the core: a Bubble Tea program
// that is, exactly like the REPL in cmd/hint, a client of the RPC
// protocol and nothing else. It never sees the agent loop; what a turn
// means is decided in internal/shell, what it looks like is decided
// here.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

// Options is what the shell needs from the process that starts it.
type Options struct {
	Client *rpc.Client
	// SessionID is the session the shell continues. Empty with
	// PickSession opens the session list first and attaches the pick.
	SessionID string
	// Recorded says whether SessionID is written to disk; the header
	// shows it and the exit note depends on it.
	Recorded bool
	// History is the conversation so far, shown before the first prompt.
	History []agentapi.Message
	// Profile is what the session answers with, for the header.
	Profile rpc.ProfileRef
	// PickSession starts in the session list (hint -r).
	PickSession bool
	// Setup, when set, lets the user fill in the provider configuration
	// from the shell; see [Setup].
	Setup *Setup
	// ServerVersion is shown in the header.
	ServerVersion string
	// Dark picks the markdown palette; nil asks the terminal.
	Dark *bool
	// Input and Output replace the terminal, for tests. Both nil means the
	// process's own terminal.
	Input  io.Reader
	Output io.Writer
}

// Outcome is what the shell leaves behind when it exits: the session it
// ended on, so the process can say how to continue it.
type Outcome struct {
	SessionID string
	Recorded  bool
}

// Run shows the shell until the user leaves. A turn still running when
// the user quits is cancelled on the server.
func Run(ctx context.Context, opts Options) (Outcome, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dark := true
	switch {
	case opts.Dark != nil:
		dark = *opts.Dark
	case opts.Input == nil && opts.Output == nil:
		dark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	}

	// The model sends itself messages from the goroutine that runs a turn,
	// through the program — which does not exist until the model does.
	// relay closes that loop.
	r := &relay{}
	m := newModel(ctx, opts, dark, r)
	popts := []tea.ProgramOption{tea.WithContext(ctx)}
	if opts.Input != nil {
		popts = append(popts, tea.WithInput(opts.Input))
	}
	if opts.Output != nil {
		popts = append(popts, tea.WithOutput(opts.Output))
	}
	p := tea.NewProgram(m, popts...)
	r.p = p

	final, err := p.Run()
	fm, ok := final.(model)
	if !ok {
		return Outcome{}, errors.Join(err, fmt.Errorf("tui: final model is %T", final))
	}
	return Outcome{SessionID: fm.sessionID, Recorded: fm.recorded}, err
}

// sender delivers a message to the running program from outside its
// update loop. *tea.Program is one; tests use a channel.
type sender interface {
	Send(tea.Msg)
}

// relay is a sender that learns its program after construction.
type relay struct {
	p *tea.Program
}

func (r *relay) Send(msg tea.Msg) { r.p.Send(msg) }
