package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/internal/debuglog"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
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

// run loads the configuration, assembles the stack from the process
// environment and runs the invocation — the one place that touches the
// home directory, the working directory and the environment.
func run(ctx context.Context, opts options, inv invocation, sio stdio) error {
	cfg, err := config.Load(opts.flags)
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(sio.err, "warning: %s\n", w)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = "" // sessions and logs then land under relative defaults; better than refusing to run
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}

	// The trace is a debugging aid, so failing to open it is a warning,
	// not a reason to refuse the run. Every line it gets is redacted
	// against every configured secret on its way to the file.
	var trace *debuglog.Log
	if cfg.Debug || opts.debug {
		trace, err = debuglog.Open(debuglog.DefaultDir(home, os.LookupEnv), time.Now(), cfg.Secrets())
		if err != nil {
			fmt.Fprintf(sio.err, "hint: warning: no debug log: %v\n", err)
		} else {
			defer func() { _ = trace.Close() }()
			fmt.Fprintf(sio.err, "hint: debug log: %s\n", trace.Path())
			trace.Printf("hint %s", strings.Join(os.Args[1:], " "))
			trace.Printf("cwd %s", cwd)
		}
	}
	var debugLog func(string)
	if trace != nil {
		debugLog = func(line string) { trace.Printf("%s", line) }
	}

	chat, err := provider.Chat(cfg, sio.err, debugLog)
	if err != nil {
		return err
	}
	global := ""
	if dir := config.GlobalDir(home, os.LookupEnv); dir != "" {
		global = filepath.Join(dir, project.InstructionNames[0])
	}
	a, err := assemble(ctx, assembly{
		chat:   chat,
		cfg:    cfg,
		opts:   opts,
		cwd:    cwd,
		store:  session.NewStore(session.DefaultDir(home, os.LookupEnv)),
		global: global,
		trace:  trace,
		io:     sio,
	})
	if err != nil {
		return err
	}
	defer a.close()

	if inv.interactive {
		return a.repl(ctx, interrupts())
	}
	return a.oneShot(ctx, inv.question)
}

// assembly is everything [assemble] needs, gathered from the process by
// [run] and from fakes and temp directories by the tests.
type assembly struct {
	chat  agentapi.ChatProvider
	cfg   *config.Config
	opts  options
	cwd   string
	store *session.Store
	// global is the user's own HINT.md, or "" for none.
	global string
	// trace is the --debug log, or nil.
	trace *debuglog.Log
	io    stdio
}

// app is an assembled run: the agent behind its permission gate, the
// conversation it continues, and the streams it talks through. One app
// serves one one-shot answer or one whole interactive session.
type app struct {
	io       stdio
	opts     options
	trace    *debuglog.Log
	lines    *console.LineReader
	sess     *session.Session
	rec      *session.Recorder
	preamble []agentapi.Message
	agent    *agent.Agent
	// warnedSave remembers that the "session not fully saved" notice was
	// printed, so a REPL with a broken disk says it once, not per turn.
	warnedSave bool
}

// assemble builds the stack for as: the working-directory root, the
// project context, the tools behind the permission gate, the session and
// the agent. Warnings that belong to the run (a cut HINT.md, git refusing
// to answer) are printed here once.
func assemble(ctx context.Context, as assembly) (*app, error) {
	root, err := tool.NewRoot(as.cwd)
	if err != nil {
		return nil, fmt.Errorf("working directory: %w", err)
	}

	// The project context is this run's view of the directory: the
	// instruction files, the overview and git's ignore rules, bounded by
	// the configured limits.
	var overview project.Overview = project.None{}
	if as.cfg.Overview.Depth > 0 {
		overview = project.Tree{Depth: as.cfg.Overview.Depth, MaxEntries: as.cfg.Overview.MaxEntries}
	}
	pc, err := project.Load(ctx, root.Dir(), projectOptions(as, overview)...)
	if err != nil {
		return nil, err
	}
	for _, w := range pc.Warnings {
		fmt.Fprintf(as.io.err, "hint: warning: %s\n", w)
	}
	registry, err := toolRegistry(root, pc.InstructionPaths)
	if err != nil {
		return nil, fmt.Errorf("registering tools: %w", err)
	}

	// One reader owns stdin for the whole run: the session picker, the
	// permission prompter and the REPL all ask through it, so none can
	// swallow another's answer.
	lines := console.NewLineReader(as.io.in)

	sess, err := openSession(ctx, as.store, root.Dir(), as.opts.session, as.opts.sessionID, lines, as.io.err)
	if err != nil {
		return nil, err
	}

	// The preamble is this run's, not the conversation's: it is rebuilt
	// every time from the project context and never stored, so the
	// Recorder is told how long it is.
	preamble := []agentapi.Message{agentapi.SystemMessage(systemPrompt(pc))}
	rec := session.NewRecorder(sess, len(preamble))

	warnMode(as.io.err, as.opts.mode, as.io.tty)
	gate := permission.New(as.opts.mode, permission.NewLinePrompter(lines, as.io.err))

	// A profile that states its context window sizes proactive
	// compaction; the fallback profile keeps the same limits, and a
	// smaller window there is caught by its own overflow error.
	limits := agent.DefaultLimits()
	if p, err := as.cfg.Default(); err == nil && p.ContextWindow > 0 {
		limits.MaxContextTokens = p.ContextWindow
	}
	ag := agent.New(as.chat, agent.WithTools(registry.Tools()...), agent.WithAuthorizer(gate), agent.WithLimits(limits))

	a := &app{io: as.io, opts: as.opts, trace: as.trace, lines: lines, sess: sess, rec: rec, preamble: preamble, agent: ag}
	if as.trace != nil {
		for _, p := range as.cfg.Providers {
			as.trace.Printf("profile %s", p)
		}
		as.trace.Printf("default %s fallback %q mode %s window %d", as.cfg.DefaultProvider, as.cfg.FallbackProvider, as.opts.mode, limits.MaxContextTokens)
		if sess != nil {
			as.trace.Printf("session %s (%d messages) at %s", sess.ID(), sess.Len(), sess.Path())
		}
		as.trace.Printf("system prompt:\n%s", preamble[0].Text())
	}
	return a, nil
}

// close releases what assemble opened.
func (a *app) close() {
	if a.sess != nil {
		if err := a.sess.Close(); err != nil {
			fmt.Fprintf(a.io.err, "hint: warning: %v\n", err)
		}
	}
}

// tracef writes to the debug log when there is one.
func (a *app) tracef(format string, args ...any) {
	if a.trace != nil {
		a.trace.Printf(format, args...)
	}
}

// openSession returns the session this run records into per smode, or nil
// for sessionOff. Continuing or resuming an existing session prints a
// one-line notice on stderr, and any load warnings after it; a directory
// with nothing to continue falls back to a new session rather than
// refusing to run — except for --session, which names one and is refused
// when it is not there.
func openSession(ctx context.Context, store *session.Store, cwd string, smode sessionMode, id string, lines *console.LineReader, stderr io.Writer) (*session.Session, error) {
	switch smode {
	case sessionOff:
		return nil, nil
	case sessionNew:
		return store.Create(cwd)
	case sessionContinue:
		sess, err := store.Latest(cwd)
		if errors.Is(err, session.ErrNoSessions) {
			fmt.Fprintln(stderr, "hint: no previous session in this directory; starting a new one")
			return store.Create(cwd)
		}
		if err != nil {
			return nil, err
		}
		announce(stderr, sess)
		return sess, nil
	case sessionResume:
		infos, err := store.List(cwd)
		if err != nil {
			return nil, err
		}
		if len(infos) == 0 {
			fmt.Fprintln(stderr, "hint: no previous session in this directory; starting a new one")
			return store.Create(cwd)
		}
		info, err := session.Choose(ctx, stderr, lines, infos)
		if err != nil {
			return nil, err
		}
		sess, err := store.Open(info.Path)
		if err != nil {
			return nil, err
		}
		announce(stderr, sess)
		return sess, nil
	case sessionByID:
		sess, err := store.Find(cwd, id)
		if errors.Is(err, session.ErrNoSessions) {
			return nil, fmt.Errorf("--session %s: this directory has no recorded sessions", id)
		}
		if err != nil {
			return nil, err
		}
		announce(stderr, sess)
		return sess, nil
	default:
		return nil, fmt.Errorf("unknown session mode %d", smode)
	}
}

// announce says which session a run continues.
func announce(stderr io.Writer, sess *session.Session) {
	fmt.Fprintf(stderr, "hint: continuing session %s (%d messages, started %s)\n",
		sess.ID(), sess.Len(), session.Age(sess.Created(), time.Now()))
	for _, w := range sess.Warnings() {
		fmt.Fprintf(stderr, "hint: warning: %s: %s\n", sess.Path(), w)
	}
}

// projectOptions turns the configuration into project.Load's knobs. The
// instruction budget is passed only when the user set one: otherwise
// project scales its default to the profile's context window (WP0.12),
// which it learns here too. Summaries are opt-in and cached under the
// user's cache directory; a cache that cannot be located just means a
// model call per run.
func projectOptions(as assembly, overview project.Overview) []project.Option {
	opts := []project.Option{project.WithOverview(overview), project.WithGlobalInstructions(as.global)}
	if as.cfg.Instructions.BudgetExplicit {
		opts = append(opts, project.WithInstructionBudget(as.cfg.Instructions.Budget))
	}
	if p, err := as.cfg.Default(); err == nil {
		opts = append(opts, project.WithContextWindow(p.ContextWindow))
	}
	if as.cfg.Instructions.Summarize {
		var s project.Summarizer = project.ChatSummarizer{Provider: as.chat}
		if dir, err := project.DefaultSummaryCacheDir(); err == nil {
			s = project.CachedSummarizer{Dir: dir, Inner: s}
		}
		opts = append(opts, project.WithSummarizer(s))
	}
	return opts
}

// toolRegistry builds the tool set the CLI offers the model: every
// built-in, write_file, edit_file and bash included, plus the
// instructions tool over the instruction files this run discovered. They
// are only safe to register because assemble() puts the agent behind a
// permission.Gate; main_test.go pins both halves of that.
func toolRegistry(root tool.Root, instructionPaths []string) (*tool.Registry, error) {
	return tool.NewRegistry(append(builtin.All(root), builtin.NewInstructions(instructionPaths))...)
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

// systemPrompt assembles the run's system message: the assistant's
// standing orders, then the project context — where it is, what it looks
// like, and what the project's own instruction files say. The instruction
// files come last so they read as the most specific guidance; they are
// the user's words to the agent, not tool output, which is why they
// belong in the system message at all.
func systemPrompt(pc *project.Context) string {
	var b strings.Builder
	b.WriteString("You are a helpful assistant aiding a developer with their project.\n\n" +
		"You have tools to explore the project: list_dir, read_file, glob and grep. " +
		"Use them to look at the actual code before answering instead of guessing, " +
		"and refer to files by their paths. Use todo to show a plan for multi-step work. " +
		"The instructions tool reads the project's instruction files in full, one section at a time, " +
		"and tells which file and line an instruction comes from. " +
		"You can change the project with edit_file (preferred for targeted changes) and write_file, " +
		"and run commands with bash; the user is shown each edit as a diff and each command " +
		"before it runs and may decline it. A declined action must not be retried unchanged.\n\n")

	fmt.Fprintf(&b, "Working directory: %s\n", pc.Dir)
	switch pc.GitRoot {
	case "":
		b.WriteString("Not inside a git repository.\n")
	case pc.Dir:
		b.WriteString("It is the root of a git repository.\n")
	default:
		fmt.Fprintf(&b, "Git repository root: %s\n", pc.GitRoot)
	}
	if pc.Overview != "" {
		fmt.Fprintf(&b, "\nContents of the working directory:\n%s\n", pc.Overview)
	}
	if instr := project.RenderInstructions(pc.Instructions); instr != "" {
		b.WriteString("\nThe project keeps instructions for assistants; follow them. " +
			"When files at several levels disagree, the one nearest the working directory wins. " +
			"A section that ends in [...] is shown as its heading and first sentence only; " +
			"call instructions with the file's path and that heading before acting on what it covers.\n")
		b.WriteString(instr)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
