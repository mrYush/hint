package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/shell"
	"github.com/mrYush/hint/pkg/agentapi"
)

// termSink shows a turn on the terminal: the answer streams to out (nil
// collects it silently for an output that prints once at the end), tool
// activity and notices go to stderr so the answer on stdout stays clean
// for pipes and scripts, and permission questions are asked through the
// shared line reader.
type termSink struct {
	out      io.Writer
	err      io.Writer
	prompter permission.Prompter
}

func (s termSink) Text(delta string) {
	if s.out != nil {
		fmt.Fprint(s.out, delta)
	}
}

func (s termSink) ToolStart(call agentapi.ToolCall) {
	fmt.Fprintf(s.err, "hint: %s %s\n", call.Name, shell.SummarizeArgs(call.Arguments))
}

func (s termSink) ToolEnd(result agentapi.ToolResult) {
	switch {
	case result.IsError:
		fmt.Fprintf(s.err, "hint: %s failed: %s\n", result.Name, shell.FirstLine(result.Text()))
	case result.Name == "todo":
		// The plan is for the user as much as for the model.
		fmt.Fprintln(s.err, result.Text())
	}
}

func (s termSink) Notice(line string) {
	fmt.Fprintln(s.err, "hint:", line)
}

func (s termSink) Permission(ctx context.Context, req agentapi.PermissionRequest) (permission.Decision, error) {
	return s.prompter.Prompt(ctx, permission.Ask{Request: req})
}

// turn runs one user turn through the core and shows it on the terminal;
// stream receives the answer text as it arrives, nil collects it
// silently. The error is the turn's outcome, see [shell.RunTurn].
func (a *app) turn(ctx context.Context, question string, stream io.Writer) (shell.Result, error) {
	sink := termSink{out: stream, err: a.io.err, prompter: a.prompter}
	return shell.RunTurn(ctx, a.client, a.sessionID, question, sink)
}

// oneShot answers one question and returns the turn's outcome as the
// process's, in the configured output.
func (a *app) oneShot(ctx context.Context, question string) error {
	if a.opts.output == outputJSON {
		return a.oneShotJSON(ctx, question)
	}
	res, err := a.turn(ctx, question, a.io.out)
	if res.Answer != "" {
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
	out := jsonAnswer{Answer: res.Answer, FinishReason: res.Finish, Error: res.Err}
	if a.recorded {
		out.SessionID = a.sessionID
	}
	if res.Usage != (agentapi.Usage{}) {
		out.Usage = &res.Usage
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
