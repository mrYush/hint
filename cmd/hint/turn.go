package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

// errInterrupted is the outcome of a turn cancelled by Ctrl-C.
var errInterrupted = errors.New("interrupted")

// cancelWait bounds the turn.cancel sent after the run's own context has
// ended: that context is gone, and the server may already be too.
const cancelWait = 5 * time.Second

// turnResult is what one turn produced, in the shape the one-shot
// outputs print.
type turnResult struct {
	// answer is every text fragment the model wrote this turn, joined:
	// what the text output streamed, what the JSON output reports.
	answer string
	finish agentapi.FinishReason
	usage  agentapi.Usage
	// err is the failure that ended the turn, when finish is FinishError.
	err *agentapi.Error
}

// turn runs one user turn through the core: it starts the turn, follows
// the event stream to the terminal, answers permission questions on it,
// and returns what came back. stream receives the answer text as it
// arrives; nil collects it silently for an output that prints once at the
// end.
//
// The returned error is the turn's outcome — nil for a normal answer,
// "interrupted" for a cancel, the classified failure otherwise — and is
// what a one-shot exits with and what the REPL prints before the next
// prompt.
func (a *app) turn(ctx context.Context, question string, stream io.Writer) (turnResult, error) {
	t, err := a.client.StartTurn(ctx, a.sessionID, question)
	if err != nil {
		return turnResult{}, err
	}
	// Ctrl-C cancels the turn on the server; the events are still read
	// to the end, so the turn closes the way the server recorded it.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			cancelTurn(t)
		case <-finished:
		}
	}()

	var res turnResult
	var answer strings.Builder
	// lastErr remembers the most recent EventError. Per the contract,
	// EventError does not necessarily end the turn — a non-terminal
	// compaction failure is reported this way too, and the turn carries on
	// — so it only becomes the turn's result if EventTurnEnd actually
	// closes with FinishError.
	var lastErr *agentapi.Error
	var runErr error
	for ev := range t.Events() {
		switch ev.Kind {
		case agentapi.EventTextDelta:
			answer.WriteString(ev.Text)
			if stream != nil {
				fmt.Fprint(stream, ev.Text)
			}
		case agentapi.EventPermission:
			a.answer(ctx, t, *ev.Permission)
		case agentapi.EventToolStart:
			// Tool activity goes to stderr so the answer on stdout stays
			// clean for pipes and scripts.
			fmt.Fprintf(a.io.err, "hint: %s %s\n", ev.Call.Name, summarizeArgs(ev.Call.Arguments))
		case agentapi.EventToolEnd:
			switch {
			case ev.Result.IsError:
				fmt.Fprintf(a.io.err, "hint: %s failed: %s\n", ev.Result.Name, firstLine(ev.Result.Text()))
			case ev.Result.Name == "todo":
				// The plan is for the user as much as for the model.
				fmt.Fprintln(a.io.err, ev.Result.Text())
			}
		case agentapi.EventCompaction:
			fmt.Fprintf(a.io.err, "hint: compacted %d messages into a summary\n", ev.Compaction.MessagesReplaced)
		case agentapi.EventUsage:
			res.usage = *ev.Usage
		case agentapi.EventError:
			lastErr = ev.Err
		case agentapi.EventTurnEnd:
			res.finish = ev.FinishReason
			switch ev.FinishReason {
			case agentapi.FinishStop, agentapi.FinishToolCalls, agentapi.FinishContentFilter:
				// A normal stop. FinishToolCalls never actually reaches
				// EventTurnEnd (a tool round always loops back into the
				// agent, never ends the turn directly).
			case agentapi.FinishCanceled:
				runErr = errInterrupted
			case agentapi.FinishLength:
				fmt.Fprintln(a.io.err, "hint: response was truncated by the provider's token limit")
			case agentapi.FinishError:
				res.err = lastErr
				runErr = lastErr
			}
		case agentapi.EventTurnStart, agentapi.EventThinkingDelta, agentapi.EventMessage:
			// Nothing to show: thinking is not rendered, and a message is
			// the sum of deltas already printed.
		}
	}
	if err := t.Err(); err != nil {
		// The stream ended without turn.done: the connection to the core
		// is gone, and so is whatever the turn still had to say.
		return res, fmt.Errorf("lost the connection to the core: %w", err)
	}
	res.answer = answer.String()
	return res, runErr
}

// answer puts a permission question to the terminal and sends the
// decision back. ctx ending at the prompt is not answered at all: the
// cancel watcher in turn ends the turn, and the server denies.
func (a *app) answer(ctx context.Context, t *rpc.Turn, req agentapi.PermissionRequest) {
	decision, err := a.prompter.Prompt(ctx, permission.Ask{Request: req})
	if err != nil {
		return
	}
	if err := t.Reply(ctx, req.CallID, decision); err != nil && ctx.Err() == nil {
		fmt.Fprintf(a.io.err, "hint: warning: the answer did not reach the core: %v\n", err)
	}
}

// cancelTurn asks the server to stop t. A turn that has already ended
// makes this an error nobody needs to hear about.
func cancelTurn(t *rpc.Turn) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelWait)
	defer cancel()
	_ = t.Cancel(ctx)
}

// oneShot answers one question and returns the turn's outcome as the
// process's, in the configured output.
func (a *app) oneShot(ctx context.Context, question string) error {
	if a.opts.output == outputJSON {
		return a.oneShotJSON(ctx, question)
	}
	res, err := a.turn(ctx, question, a.io.out)
	if res.answer != "" {
		fmt.Fprintln(a.io.out)
	}
	return err
}

// jsonAnswer is the one object `--output json` prints: enough for a
// script to take the answer, tell a failure from a success, and continue
// the conversation later with --session.
type jsonAnswer struct {
	Answer       string                `json:"answer"`
	FinishReason agentapi.FinishReason `json:"finish_reason"`
	SessionID    string                `json:"session_id,omitempty"`
	Usage        *agentapi.Usage       `json:"usage,omitempty"`
	Error        *agentapi.Error       `json:"error,omitempty"`
}

// oneShotJSON runs the turn silently and prints one object afterwards.
// A failure is part of the object as well as the exit status, so a
// script that parses stdout still learns what went wrong.
func (a *app) oneShotJSON(ctx context.Context, question string) error {
	res, err := a.turn(ctx, question, nil)
	out := jsonAnswer{Answer: res.answer, FinishReason: res.finish, Error: res.err}
	if a.recorded {
		out.SessionID = a.sessionID
	}
	if res.usage != (agentapi.Usage{}) {
		out.Usage = &res.usage
	}
	if err != nil && out.Error == nil {
		out.Error = asAgentError(err)
	}
	if out.FinishReason == "" {
		out.FinishReason = agentapi.FinishError
	}
	enc := json.NewEncoder(a.io.out)
	if encErr := enc.Encode(out); encErr != nil {
		return fmt.Errorf("writing the answer: %w", encErr)
	}
	return err
}

// asAgentError gives a plain error the wire shape, keeping a classified
// one as it is.
func asAgentError(err error) *agentapi.Error {
	var e *agentapi.Error
	if errors.As(err, &e) {
		return e
	}
	return agentapi.WrapError(agentapi.KindOf(err), err, "%v", err)
}

// summarizeArgs renders a tool call's arguments on one short line.
func summarizeArgs(raw []byte) string {
	const max = 120
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

// firstLine returns the first line of s, for a one-line stderr notice.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
