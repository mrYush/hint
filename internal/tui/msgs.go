package tui

import (
	"context"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/shell"
	"github.com/mrYush/hint/pkg/agentapi"
)

// The messages a turn produces, one per shell.Sink method, plus the end
// of the turn. They are delivered through the program's Send, so Update
// sees them in order and on its own goroutine.
type (
	textDeltaMsg string
	toolStartMsg agentapi.ToolCall
	toolEndMsg   agentapi.ToolResult
	noticeMsg    string
	// permissionMsg opens the dialog; the answer goes back on reply, which
	// has room for one decision so the dialog never blocks on it.
	permissionMsg struct {
		req   agentapi.PermissionRequest
		reply chan<- permission.Decision
	}
	turnDoneMsg struct {
		res shell.Result
		err error
	}
	// sessionsMsg is the answer to session.list for the picker.
	sessionsMsg struct {
		infos []rpc.Info
		err   error
	}
	// attachedMsg is the session the picker chose, or a fresh one when
	// there was nothing to choose from.
	attachedMsg struct {
		view rpc.SessionView
		err  error
	}
)

// programSink is the shell.Sink of the TUI: every call becomes a message
// to the program. Permission is the one that waits — for the user's key,
// or for the turn to be cancelled under it.
type programSink struct {
	send sender
}

func (s programSink) Text(delta string)                  { s.send.Send(textDeltaMsg(delta)) }
func (s programSink) ToolStart(call agentapi.ToolCall)   { s.send.Send(toolStartMsg(call)) }
func (s programSink) ToolEnd(result agentapi.ToolResult) { s.send.Send(toolEndMsg(result)) }
func (s programSink) Notice(line string)                 { s.send.Send(noticeMsg(line)) }

func (s programSink) Permission(ctx context.Context, req agentapi.PermissionRequest) (permission.Decision, error) {
	reply := make(chan permission.Decision, 1)
	s.send.Send(permissionMsg{req: req, reply: reply})
	select {
	case d := <-reply:
		return d, nil
	case <-ctx.Done():
		return permission.Deny, ctx.Err()
	}
}

// runTurn is the goroutine behind one question: it drives the turn to
// its end and reports the outcome to the program.
func runTurn(ctx context.Context, cl *rpc.Client, sessionID, text string, send sender) {
	res, err := shell.RunTurn(ctx, cl, sessionID, text, programSink{send: send})
	send.Send(turnDoneMsg{res: res, err: err})
}
