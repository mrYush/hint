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
)

// replHelp is what /help prints.
const replHelp = `Commands:
  /help         show this help
  /exit, /quit  leave (Ctrl-D does too)
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
			return nil
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
			return nil
		case err != nil:
			return fmt.Errorf("reading input: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			switch cmd := strings.Fields(line)[0]; cmd {
			case "/exit", "/quit":
				return nil
			case "/help":
				fmt.Fprintln(a.io.err, replHelp)
			default:
				fmt.Fprintf(a.io.err, "hint: unknown command %s; /help lists them\n", cmd)
			}
			continue
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
		if res.answer != "" {
			fmt.Fprintln(a.io.out)
		} else if errors.Is(err, errInterrupted) {
			// The terminal echoed ^C on the question's line.
			fmt.Fprintln(a.io.err)
		}
		if err != nil {
			fmt.Fprintln(a.io.err, "hint:", err)
		}
	}
}

// errPromptInterrupted reports Ctrl-C while the prompt was waiting.
var errPromptInterrupted = errors.New("interrupted at the prompt")

// readLine reads the next line, giving up with errInterrupted when a
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
