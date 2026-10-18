package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/pkg/agentapi"
)

// stdio is the process's three streams as one value, so that the run
// never reaches for os.Stdout itself and a test can hand it buffers.
type stdio struct {
	in       io.Reader
	out, err io.Writer
	// tty says whether in is a terminal: the REPL needs one, and the
	// permission notices say up front when there is none to answer.
	tty bool
}

// connectAuto is the value of a bare --connect: find the running server
// through the files it wrote next to its socket.
const connectAuto = "auto"

// run connects the CLI shell to a core and runs the invocation. The core
// is in this process unless --connect names a running `hint serve`;
// either way the shell only ever speaks the RPC protocol to it.
func run(ctx context.Context, opts options, inv invocation, sio stdio) error {
	var a *app
	var err error
	if opts.connect != "" {
		a, err = connect(ctx, opts, sio)
	} else {
		var eng *rpcEngine
		var cleanup func()
		eng, cleanup, err = newRPCEngine(ctx, opts, sio.err)
		if err != nil {
			return err
		}
		a, err = serveInProcess(ctx, eng, opts, sio)
		if err != nil {
			cleanup()
			return err
		}
		a.cleanup = cleanup
	}
	if err != nil {
		return err
	}
	defer a.close()

	if inv.interactive {
		if opts.tui {
			return a.tui(ctx)
		}
		return a.repl(ctx, interrupts())
	}
	return a.oneShot(ctx, inv.question)
}

// app is the CLI shell: a client of the core, the session it continues,
// and the streams it talks through. One app serves one one-shot answer
// or one whole interactive session.
type app struct {
	io     stdio
	opts   options
	lines  *console.LineReader
	client *rpc.Client
	// srv is the in-process core, nil when --connect reached a server in
	// another process. cleanup releases the debug log, when there is one.
	srv     *rpc.Server
	cleanup func()
	// sessionID names the conversation on the server; recorded says
	// whether it is written to disk, which is what --output json reports.
	sessionID string
	recorded  bool
	// history is the conversation so far, for a shell that redraws it.
	history []agentapi.Message
	// prompter answers permission events on the terminal. It is the only
	// permission UI: the core never asks on its own stdin.
	prompter permission.Prompter
}

// assemble builds the whole stack for as — the core and the shell over
// it, talking through sio — the way run does from the process and the
// tests do from fakes.
func assemble(ctx context.Context, as assembly, sio stdio) (*app, error) {
	eng, err := newEngine(ctx, as)
	if err != nil {
		return nil, err
	}
	return serveInProcess(ctx, eng, as.opts, sio)
}

// serveInProcess serves eng on one end of a pipe and connects the shell
// to the other. The bytes are the same ones an external client would
// send and receive; nothing in the shell can reach the agent any other
// way.
func serveInProcess(ctx context.Context, eng *rpcEngine, opts options, sio stdio) (*app, error) {
	srv := &rpc.Server{Engine: eng, Version: buildVersion()}
	shell, core := net.Pipe()
	go func() { _ = srv.ServeConn(ctx, core) }()
	cl, err := rpc.Dial(ctx, shell, "")
	if err != nil {
		srv.Close()
		return nil, err
	}
	a, err := newApp(ctx, cl, opts, sio)
	if err != nil {
		_ = cl.Close()
		srv.Close()
		return nil, err
	}
	a.srv = srv
	return a, nil
}

// connect reaches the `hint serve` --connect names: the one whose
// endpoint and token are in the runtime directory, or an explicit
// unix:/path or tcp:host:port with the token from the same directory.
func connect(ctx context.Context, opts options, sio stdio) (*app, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	dir := rpc.RuntimeDir(home, os.LookupEnv)
	var network, addr string
	if opts.connect == connectAuto {
		network, addr, err = rpc.ReadEndpoint(dir)
		if err != nil {
			return nil, fmt.Errorf("no running hint serve found (%w); start one, or pass --connect unix:/path", err)
		}
	} else if network, addr, err = parseListen(opts.connect, dir); err != nil {
		return nil, err
	}
	token, err := rpc.ReadToken(dir)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	nc, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("connecting to hint serve at %s %s: %w", network, addr, err)
	}
	cl, err := rpc.Dial(ctx, nc, token)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(sio.err, "hint: connected to hint serve at %s %s (server %s)\n", network, addr, cl.ServerVersion())
	a, err := newApp(ctx, cl, opts, sio)
	if err != nil {
		_ = cl.Close()
		return nil, err
	}
	return a, nil
}

// newApp opens the session the flags asked for and prints the one-time
// notices of the run.
func newApp(ctx context.Context, cl *rpc.Client, opts options, sio stdio) (*app, error) {
	// One reader owns stdin for the whole run: the session picker, the
	// permission prompter and the REPL all ask through it, so none can
	// swallow another's answer.
	lines := console.NewLineReader(sio.in)
	a := &app{
		io:       sio,
		opts:     opts,
		lines:    lines,
		client:   cl,
		prompter: permission.NewLinePrompter(lines, sio.err),
	}
	// The TUI has a session list of its own, so -r is not asked on the
	// line reader; the shell starts in that list instead.
	if !opts.tui || opts.session != sessionResume {
		view, recorded, err := openSession(ctx, cl, opts.session, opts.sessionID, lines, sio.err)
		if err != nil {
			return nil, err
		}
		a.sessionID, a.recorded, a.history = view.ID, recorded, view.Messages
	}
	warnMode(sio.err, opts.mode, sio.tty)
	return a, nil
}

// close releases what the run opened.
func (a *app) close() {
	_ = a.client.Close()
	if a.srv != nil {
		a.srv.Close()
	}
	if a.cleanup != nil {
		a.cleanup()
	}
}

// openSession opens or attaches the session this run talks to, per
// smode, and reports whether it is recorded. Continuing or resuming an
// existing session prints a one-line notice on stderr, and any load
// warnings after it; a directory with nothing to continue falls back to a
// new session rather than refusing to run — except for --session, which
// names one and is refused when it is not there.
func openSession(ctx context.Context, cl *rpc.Client, smode sessionMode, id string, lines *console.LineReader, stderr io.Writer) (rpc.SessionView, bool, error) {
	switch smode {
	case sessionOff:
		view, err := cl.OpenSession(ctx, rpc.OpenOptions{Ephemeral: true})
		return view, false, err
	case sessionNew:
		view, err := cl.OpenSession(ctx, rpc.OpenOptions{})
		return view, true, err
	case sessionContinue:
		infos, err := cl.ListSessions(ctx)
		if err != nil {
			return rpc.SessionView{}, false, err
		}
		if len(infos) == 0 {
			fmt.Fprintln(stderr, "hint: no previous session in this directory; starting a new one")
			view, err := cl.OpenSession(ctx, rpc.OpenOptions{})
			return view, true, err
		}
		return attach(ctx, cl, infos[0].ID, stderr)
	case sessionResume:
		infos, err := cl.ListSessions(ctx)
		if err != nil {
			return rpc.SessionView{}, false, err
		}
		if len(infos) == 0 {
			fmt.Fprintln(stderr, "hint: no previous session in this directory; starting a new one")
			view, err := cl.OpenSession(ctx, rpc.OpenOptions{})
			return view, true, err
		}
		choice, err := session.Choose(ctx, stderr, lines, sessionInfos(infos))
		if err != nil {
			return rpc.SessionView{}, false, err
		}
		return attach(ctx, cl, choice.ID, stderr)
	case sessionByID:
		view, recorded, err := attach(ctx, cl, id, stderr)
		var rpcErr *rpc.Error
		if errors.As(err, &rpcErr) && rpcErr.Code == rpc.CodeNotFound {
			if infos, lerr := cl.ListSessions(ctx); lerr == nil && len(infos) == 0 {
				return rpc.SessionView{}, false, fmt.Errorf("--session %s: this directory has no recorded sessions", id)
			}
		}
		return view, recorded, err
	default:
		return rpc.SessionView{}, false, fmt.Errorf("unknown session mode %d", smode)
	}
}

// attach continues the session id names and announces it.
func attach(ctx context.Context, cl *rpc.Client, id string, stderr io.Writer) (rpc.SessionView, bool, error) {
	view, err := cl.AttachSession(ctx, id)
	if err != nil {
		return rpc.SessionView{}, false, err
	}
	announce(stderr, view)
	return view, true, nil
}

// announce says which session a run continues.
func announce(stderr io.Writer, view rpc.SessionView) {
	fmt.Fprintf(stderr, "hint: continuing session %s (%d messages, started %s)\n",
		view.ID, len(view.Messages), session.Age(view.Created, time.Now()))
	for _, w := range view.Warnings {
		fmt.Fprintf(stderr, "hint: warning: session %s: %s\n", view.ID, w)
	}
}

// sessionInfos gives the picker the rows in the shape it prints.
func sessionInfos(infos []rpc.Info) []session.Info {
	out := make([]session.Info, 0, len(infos))
	for _, info := range infos {
		out = append(out, session.Info{
			ID:          info.ID,
			Cwd:         info.Cwd,
			Created:     info.Created,
			Modified:    info.Modified,
			Messages:    info.Messages,
			FirstPrompt: info.FirstPrompt,
		})
	}
	return out
}

// warnMode prints the one-time notices a run mode deserves: --yolo is
// loud because nothing will ask again, and a run that cannot ask at all —
// stdin is not a terminal — says so up front instead of surprising the
// user with a string of denials.
func warnMode(w io.Writer, mode permission.Mode, tty bool) {
	if mode == permission.ModeYolo {
		fmt.Fprintln(w, "hint: WARNING: --yolo: file edits and shell commands will run WITHOUT confirmation")
		return
	}
	if !tty {
		fmt.Fprintf(w, "hint: stdin is not a terminal; %s will be denied (use --auto-edit or --yolo for unattended runs)\n",
			needsAnswer(mode))
	}
}

// needsAnswer names what mode would have to ask about.
func needsAnswer(mode permission.Mode) string {
	if mode == permission.ModeAutoEdit {
		return "shell commands"
	}
	return "file edits and shell commands"
}
