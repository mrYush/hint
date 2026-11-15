package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"

	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/shell"
)

// replHelp is what /help prints.
const replHelp = `Commands:
  /help                    show this help
  /repos                   list roots; add/remove/tree/clone/next; index controls process-wide indexing
  /new                     start a fresh session; the current one stays
  /profile                 list the provider profiles; the one in use is marked
  /profile <name> [model]  answer with that profile (and model) from now on
  /prompts                 list MCP prompts
  /<server>:<prompt> [args] expand an MCP prompt; quote arguments containing spaces
  /exit, /quit             leave (Ctrl-D does too)
Anything else is a question for the assistant; each line is one question.
Ctrl-C stops an answer that is being written and returns to the prompt.`

// interrupts returns a channel that receives Ctrl-C for the life of the
// process. The REPL takes it as an argument so a test can press Ctrl-C by
// sending on a channel of its own.
func interrupts() <-chan os.Signal {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	return sigs
}

// repl is the interactive session: a plain line loop over the shared
// stdin reader, one question per line, until Ctrl-D, /exit or the
// context ends. Ctrl-C at the prompt says how to leave; Ctrl-C during an
// answer cancels that turn only — the conversation, the session file and
// the permission grants of the run all survive it.
func (a *app) repl(ctx context.Context, sigs <-chan os.Signal) error {
	fmt.Fprintln(a.io.err, "hint: interactive session; /help for commands, Ctrl-D or /exit to quit")
	for {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // a finished context ends the session normally, not as a failure
		}
		drain(sigs)
		fmt.Fprint(a.io.err, "\n> ")
		line, err := readLine(ctx, a.lines, sigs)
		switch {
		case errors.Is(err, errPromptInterrupted):
			fmt.Fprintln(a.io.err, "\nhint: press Ctrl-D or type /exit to quit")
			continue
		case errors.Is(err, io.EOF), ctx.Err() != nil:
			fmt.Fprintln(a.io.err)
			return nil //nolint:nilerr // Ctrl-D and a finished context are the normal ways out
		case err != nil:
			return fmt.Errorf("reading input: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			fields, splitErr := shell.SplitWords(line)
			if splitErr != nil {
				fmt.Fprintln(a.io.err, "hint: invalid command:", splitErr)
				continue
			}
			switch cmd := fields[0]; cmd {
			case "/exit", "/quit":
				return nil
			case "/help":
				fmt.Fprintln(a.io.err, replHelp)
				continue
			case "/new":
				if len(fields) > 1 {
					fmt.Fprintln(a.io.err, "hint: /new takes no argument")
					continue
				}
				a.newSession(ctx)
				continue
			case "/repos":
				a.reposCommand(ctx, sigs, fields[1:])
				continue
			case "/profile":
				a.profileCommand(ctx, fields[1:])
				continue
			case "/prompts":
				a.promptsCommand(ctx)
				continue
			default:
				if _, _, ok := rpc.ParsePromptCommand(cmd); !ok {
					fmt.Fprintf(a.io.err, "hint: unknown command %s; /help lists them\n", cmd)
					continue
				}
				expanded, err := a.expandPromptCancellable(ctx, sigs, cmd, fields[1:])
				if err != nil {
					if errors.Is(err, context.Canceled) && ctx.Err() == nil {
						fmt.Fprintln(a.io.err, "\nhint: prompt expansion stopped")
						continue
					}
					fmt.Fprintln(a.io.err, "hint:", err)
					continue
				}
				if strings.TrimSpace(expanded) == "" {
					fmt.Fprintln(a.io.err, "hint: the MCP prompt returned no text")
					continue
				}
				line = expanded
			}
		}

		turnCtx, cancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-sigs:
				cancel()
			case <-turnCtx.Done():
			}
		}()
		res, err := a.turn(turnCtx, line, a.io.out)
		cancel()
		if res.Answer != "" {
			fmt.Fprintln(a.io.out)
		} else if errors.Is(err, shell.ErrInterrupted) {
			// The terminal echoed ^C on the question's line.
			fmt.Fprintln(a.io.err)
		}
		if err != nil {
			fmt.Fprintln(a.io.err, "hint:", shell.Explain(err))
			if help := a.authHelp(err); help != "" {
				a.offerSetup(ctx, help)
			}
		}
	}
}

// newSession is /new: session.open on the default profile, ephemeral
// when this run records nothing. The session just left stays as it was.
func (a *app) newSession(ctx context.Context) {
	left := a.sessionID
	view, err := a.client.OpenSession(ctx, rpc.OpenOptions{Ephemeral: !a.recorded})
	if err != nil {
		fmt.Fprintln(a.io.err, "hint:", err)
		return
	}
	a.sessionID = view.ID
	a.history = nil
	a.profile = rpc.ProfileRef{Profile: view.Profile, Model: view.Model}
	fmt.Fprintf(a.io.err, "hint: left session %s; this is session %s\n", left, view.ID)
}

// profileCommand is /profile: alone it lists the profiles the core has,
// with a name (and a model) it switches the session.
func (a *app) profileCommand(ctx context.Context, args []string) {
	if len(args) == 0 {
		infos, err := a.client.Profiles(ctx)
		if err != nil {
			fmt.Fprintln(a.io.err, "hint:", err)
			return
		}
		for _, p := range infos {
			var marks []string
			if p.Name == a.profile.Profile {
				marks = append(marks, "in use")
			}
			if p.Default {
				marks = append(marks, "default")
			}
			if p.Fallback {
				marks = append(marks, "fallback")
			}
			line := fmt.Sprintf("  %-12s  %-7s  %s", p.Name, p.Kind, p.Model)
			if len(marks) > 0 {
				line += "  (" + strings.Join(marks, ", ") + ")"
			}
			if !p.Usable {
				line += "  ✗ " + p.Problem
			}
			fmt.Fprintln(a.io.err, line)
		}
		return
	}
	ref := rpc.ProfileRef{Profile: args[0]}
	if len(args) > 1 {
		ref.Model = args[1]
	}
	a.useProfile(ctx, ref)
}

// useProfile switches the session to ref and says what is in effect, or
// why nothing changed.
func (a *app) useProfile(ctx context.Context, ref rpc.ProfileRef) {
	got, err := a.client.SetProfile(ctx, a.sessionID, ref)
	if err != nil {
		fmt.Fprintln(a.io.err, "hint:", err.Error())
		return
	}
	a.profile = got
	fmt.Fprintf(a.io.err, "hint: answering with %s/%s from now on\n", got.Profile, got.Model)
}

// expandPromptCancellable expands a prompt until it returns or Ctrl-C
// arrives. The signal cancels that call and is drained, so it does not
// cancel the turn the expansion was going to start.
func (a *app) expandPromptCancellable(ctx context.Context, sigs <-chan os.Signal, command string, args []string) (string, error) {
	expandCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-sigs:
			cancel()
		case <-expandCtx.Done():
		}
	}()
	text, err := a.expandPrompt(expandCtx, command, args)
	cancel()
	<-stopped
	drain(sigs)
	return text, err
}

// errPromptInterrupted reports Ctrl-C while the prompt was waiting.
var errPromptInterrupted = errors.New("interrupted at the prompt")

// readLine reads the next line, giving up with errPromptInterrupted when a
// signal arrives first. The line reader is left parked on the next line,
// as its contract promises; the half-typed line itself is discarded by
// the terminal, which flushes its input on Ctrl-C.
func readLine(ctx context.Context, lines *console.LineReader, sigs <-chan os.Signal) (string, error) {
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var interrupted atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sigs:
			interrupted.Store(true)
			cancel()
		case <-done:
		}
	}()
	line, err := lines.ReadLine(readCtx)
	if err != nil && interrupted.Load() {
		return "", errPromptInterrupted
	}
	return line, err
}

// drain drops a Ctrl-C that arrived while nobody was listening — between
// the end of an answer and the next prompt — so it cannot cancel the
// question that follows.
func drain(sigs <-chan os.Signal) {
	for {
		select {
		case <-sigs:
		default:
			return
		}
	}
}

func (a *app) reposCommand(ctx context.Context, sigs <-chan os.Signal, args []string) {
	ctx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-sigs:
			cancel()
		case <-ctx.Done():
		}
	}()
	defer func() { cancel(); <-stopped; drain(sigs) }()
	req, err := shell.ParseRepos(a.sessionID, args)
	if err != nil {
		fmt.Fprintln(a.io.err, "hint:", err)
		return
	}
	result, err := a.client.Repos(ctx, req)
	if err != nil {
		fmt.Fprintln(a.io.err, "hint:", err)
		return
	}
	fmt.Fprintln(a.io.err, result.Text)
}
