package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/debuglog"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/internal/shell"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

// assembly is everything [newEngine] needs, gathered from the process by
// [newRPCEngine] and from fakes and temp directories by the tests.
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
	// notices is where the engine's own warnings go: the process's stderr
	// for every entry point today.
	notices io.Writer
	// reload re-reads the configuration from where cfg came from, so a
	// profile switch sees a key written by setup since the start. Nil
	// keeps cfg for the life of the engine, which is what a test with a
	// hand-made config wants.
	reload func() (*config.Config, error)
}

// rpcEngine is the core every entry point serves: one working directory,
// one provider, one tool set, and a session per conversation. The CLI
// reaches it through an in-process connection, `hint serve` over a
// socket, `hint --mode rpc` over the pipes. Each session has its own
// permission gate, so an "always" grant does not leak into another
// conversation.
type rpcEngine struct {
	cwd      string
	store    *session.Store
	chat     agentapi.ChatProvider
	registry *tool.Registry
	pc       *project.Context
	mode     permission.Mode
	limits   agent.Limits
	notices  io.Writer
	trace    *debuglog.Log
	debugLog func(string)
	// setup describes the configuration the engine was built from, for a
	// shell that has to say where a rejected key lives.
	setup *setupInfo

	// cfg is the configuration as last loaded; reload refreshes it (see
	// assembly.reload). providers caches one provider per profile and
	// model asked for since, seeded with chat for the default profile,
	// and is emptied whenever cfg is reloaded. cfgMu guards all three.
	cfgMu     sync.Mutex
	cfg       *config.Config
	reload    func() (*config.Config, error)
	providers map[rpc.ProfileRef]agentapi.ChatProvider

	// listModels stands in for provider.Models in tests.
	listModels func(context.Context, config.Profile) ([]provider.Model, error)

	mu   sync.Mutex
	live map[string]*rpcSession
}

// tracef writes to the debug log when there is one.
func (e *rpcEngine) tracef(format string, args ...any) {
	if e.trace != nil {
		e.trace.Printf(format, args...)
	}
}

// newRPCEngine assembles the engine from the process: configuration,
// home and working directory, the debug log. cleanup releases the log.
func newRPCEngine(ctx context.Context, opts options, notices io.Writer) (*rpcEngine, func(), error) {
	cfg, err := config.Load(opts.flags)
	if err != nil {
		return nil, nil, fmt.Errorf("loading configuration: %w", err)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(notices, "warning: %s\n", w)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "" // sessions and logs then land under relative defaults; better than refusing to run
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("working directory: %w", err)
	}

	// The trace is a debugging aid, so failing to open it is a warning,
	// not a reason to refuse the run. Every line it gets is redacted
	// against every configured secret on its way to the file.
	var trace *debuglog.Log
	cleanup := func() {}
	if cfg.Debug || opts.debug {
		trace, err = debuglog.Open(debuglog.DefaultDir(home, os.LookupEnv), time.Now(), cfg.Secrets())
		if err != nil {
			fmt.Fprintf(notices, "hint: warning: no debug log: %v\n", err)
		} else {
			cleanup = func() { _ = trace.Close() }
			fmt.Fprintf(notices, "hint: debug log: %s\n", trace.Path())
			trace.Printf("hint %s", strings.Join(os.Args[1:], " "))
			trace.Printf("cwd %s", cwd)
		}
	}
	var debugLog func(string)
	if trace != nil {
		debugLog = func(line string) { trace.Printf("%s", line) }
	}
	chat, err := provider.Chat(cfg, notices, debugLog)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	global := ""
	if dir := config.GlobalDir(home, os.LookupEnv); dir != "" {
		global = filepath.Join(dir, project.InstructionNames[0])
	}
	eng, err := newEngine(ctx, assembly{
		chat:    chat,
		cfg:     cfg,
		opts:    opts,
		cwd:     cwd,
		store:   session.NewStore(session.DefaultDir(home, os.LookupEnv)),
		global:  global,
		trace:   trace,
		notices: notices,
		reload:  func() (*config.Config, error) { return config.Load(opts.flags) },
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return eng, cleanup, nil
}

// newEngine builds the stack for as: the working-directory root, the
// project context, the tools, the limits. Warnings that belong to the
// run (a cut HINT.md, git refusing to answer) are printed here once.
func newEngine(ctx context.Context, as assembly) (*rpcEngine, error) {
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
		fmt.Fprintf(as.notices, "hint: warning: %s\n", w)
	}
	registry, err := toolRegistry(root, pc.InstructionPaths)
	if err != nil {
		return nil, fmt.Errorf("registering tools: %w", err)
	}

	// A profile that states its context window sizes proactive
	// compaction; the fallback profile keeps the same limits, and a
	// smaller window there is caught by its own overflow error.
	limits := agent.DefaultLimits()
	if p, err := as.cfg.Default(); err == nil {
		limits = limitsFor(p)
	}
	var debugLog func(string)
	if as.trace != nil {
		for _, p := range as.cfg.Providers {
			as.trace.Printf("profile %s", p)
		}
		as.trace.Printf("default %s fallback %q mode %s window %d", as.cfg.DefaultProvider, as.cfg.FallbackProvider, as.opts.mode, limits.MaxContextTokens)
		debugLog = func(line string) { as.trace.Printf("%s", line) }
	}
	return &rpcEngine{
		cwd:       root.Dir(),
		store:     as.store,
		chat:      as.chat,
		registry:  registry,
		pc:        pc,
		mode:      as.opts.mode,
		limits:    limits,
		notices:   as.notices,
		trace:     as.trace,
		debugLog:  debugLog,
		setup:     newSetupInfo(as.cfg, as.cwd, as.opts.flags),
		cfg:       as.cfg,
		reload:    as.reload,
		providers: map[rpc.ProfileRef]agentapi.ChatProvider{{Profile: as.cfg.DefaultProvider}: as.chat},
	}, nil
}

// limitsFor is the agent's limits under profile p: the built-in ones,
// with the context window the profile states when it states one. A
// profile that does not keeps the built-in window; its own overflow
// error catches a smaller one.
func limitsFor(p config.Profile) agent.Limits {
	limits := agent.DefaultLimits()
	if p.ContextWindow > 0 {
		limits.MaxContextTokens = p.ContextWindow
	}
	return limits
}

func (e *rpcEngine) List(context.Context) ([]rpc.Info, error) {
	infos, err := e.store.List(e.cwd)
	if err != nil {
		return nil, err
	}
	out := make([]rpc.Info, 0, len(infos))
	for _, info := range infos {
		row := rpc.Info{
			ID:          info.ID,
			Cwd:         info.Cwd,
			Messages:    info.Messages,
			Created:     info.Created,
			Modified:    info.Modified,
			FirstPrompt: info.FirstPrompt,
		}
		if info.Parent != nil {
			row.Parent = &rpc.Parent{ID: info.Parent.ID, Turn: info.Parent.Turn}
		}
		out = append(out, row)
	}
	return out, nil
}

func (e *rpcEngine) Open(_ context.Context, opts rpc.OpenOptions) (rpc.Session, error) {
	if opts.Ephemeral {
		// Nothing on disk: the conversation lives in the recorder for as
		// long as the server does. It still needs an id for turn.start.
		id, err := session.NewID()
		if err != nil {
			return nil, err
		}
		return e.adopt(nil, id), nil
	}
	sess, err := e.store.Create(e.cwd)
	if err != nil {
		return nil, err
	}
	return e.adopt(sess, sess.ID()), nil
}

func (e *rpcEngine) Attach(_ context.Context, id string) (rpc.Session, error) {
	e.mu.Lock()
	if live := e.live[id]; live != nil {
		e.mu.Unlock()
		return live, nil
	}
	e.mu.Unlock()
	sess, err := e.store.Find(e.cwd, id)
	if err != nil {
		return nil, err
	}
	return e.adopt(sess, sess.ID()), nil
}

// adopt registers sess under id, or returns the session already
// registered there and closes the duplicate file. A nil sess is an
// ephemeral session. The session answers with the profile its file last
// recorded; one the configuration no longer has, or cannot use, falls
// back to the default with a warning the client shows on attach.
func (e *rpcEngine) adopt(sess *session.Session, id string) *rpcSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.live == nil {
		e.live = map[string]*rpcSession{}
	}
	if live := e.live[id]; live != nil {
		if sess != nil {
			_ = sess.Close()
		}
		return live
	}
	live := e.newSession(sess, id)
	if sess != nil {
		if rec := sess.Profile(); rec.Name != "" {
			if err := live.switchTo(rpc.ProfileRef{Profile: rec.Name, Model: rec.Model}, false); err != nil {
				live.warnings = append(live.warnings, fmt.Sprintf("the session's profile is not usable (%s); answering with the default profile instead", strings.TrimPrefix(err.Error(), "config: ")))
			}
		}
	}
	e.live[id] = live
	return live
}

// newSession builds the stack of one conversation over the default
// provider: its own permission gate, so an "always" grant does not leak
// into another conversation, and its own recorder over sess (nil for an
// ephemeral session).
func (e *rpcEngine) newSession(sess *session.Session, id string) *rpcSession {
	// The preamble is this run's, not the conversation's: it is rebuilt
	// every time from the project context — and again before every
	// request once a split rule has been touched — and never stored, so
	// the Recorder is told how long it is.
	var trace func(string, ...any)
	if e.trace != nil {
		trace = e.trace.Printf
	}
	slot := &promptSlot{}
	gate := permission.New(e.mode, slot)
	pre := newPreamble(e.pc, e.registry, e.notices, trace)
	initial := pre.initial()
	live := &rpcSession{
		id:      id,
		created: time.Now(),
		eng:     e,
		file:    sess,
		rec:     session.NewRecorder(sess, len(initial)),
		pre:     pre,
		gate:    gate,
		slot:    slot,
		notices: e.notices,
		trace:   e.trace,
	}
	// The default profile as the configuration stands now; the engine's
	// own provider is the fallback should it have become unusable since.
	ref := e.defaultRef()
	if chat, p, err := e.providerFor(rpc.ProfileRef{Profile: ref.Profile}, false); err == nil {
		live.use(chat, rpc.ProfileRef{Profile: ref.Profile}, rpc.ProfileRef{Profile: ref.Profile, Model: p.Model}, limitsFor(p))
	} else {
		live.use(e.chat, rpc.ProfileRef{Profile: ref.Profile}, ref, e.limits)
	}
	if sess != nil {
		live.created = sess.Created()
	}
	if e.trace != nil {
		if sess != nil {
			e.trace.Printf("session %s (%d messages) at %s", sess.ID(), sess.Len(), sess.Path())
		} else {
			e.trace.Printf("session %s (not recorded)", id)
		}
		e.trace.Printf("system prompt:\n%s", initial[0].Text())
	}
	return live
}

// rpcSession is one open conversation. The server does not call RunTurn
// twice at once.
type rpcSession struct {
	id      string
	created time.Time
	eng     *rpcEngine
	// file is nil for an ephemeral session.
	file    *session.Session
	rec     *session.Recorder
	pre     *preamble
	gate    *permission.Gate
	slot    *promptSlot
	notices io.Writer
	trace   *debuglog.Log
	// parent is set on an ephemeral fork; a recorded one carries it in
	// its file.
	parent *rpc.Parent
	// warnings are the engine's own, shown with the file's on attach.
	warnings []string

	// agMu guards ag, asked and profile: SetProfile replaces them between
	// turns, RunTurn reads them when a turn starts. asked is the profile
	// as requested, profile the same with the model filled in.
	agMu    sync.Mutex
	ag      *agent.Agent
	asked   rpc.ProfileRef
	profile rpc.ProfileRef

	// warnedSave remembers that the "session not fully saved" notice was
	// printed, so a conversation on a broken disk says it once, not per
	// turn.
	warnedSave bool
	closeOnce  sync.Once
	closeErr   error
}

func (s *rpcSession) ID() string                   { return s.id }
func (s *rpcSession) Created() time.Time           { return s.created }
func (s *rpcSession) Messages() []agentapi.Message { return s.rec.Messages() }

func (s *rpcSession) Warnings() []string {
	var out []string
	if s.file != nil {
		out = s.file.Warnings()
	}
	return append(out, s.warnings...)
}

func (s *rpcSession) Parent() (rpc.Parent, bool) {
	if s.parent != nil {
		return *s.parent, true
	}
	if s.file != nil {
		if p, ok := s.file.Parent(); ok {
			return rpc.Parent{ID: p.ID, Turn: p.Turn}, true
		}
	}
	return rpc.Parent{}, false
}

func (s *rpcSession) Profile() rpc.ProfileRef {
	s.agMu.Lock()
	defer s.agMu.Unlock()
	return s.profile
}

// RunTurn records text as the user message and runs the agent. It fails
// before the first request if the session cannot be written at all; a
// read-only disk is better learned about now than after the answer.
func (s *rpcSession) RunTurn(ctx context.Context, text string, ask permission.Prompter) (<-chan agentapi.Event, error) {
	s.slot.set(ask)
	user := agentapi.UserMessage(text)
	if err := s.rec.Append(user); err != nil {
		s.slot.set(nil)
		return nil, err
	}
	history := append(append([]agentapi.Message{}, s.pre.initial()...), s.rec.Messages()...)
	s.agMu.Lock()
	ag := s.ag
	s.agMu.Unlock()
	s.tracef("turn: %q (%d messages)", text, len(history))
	out := ag.RunTurn(ctx, history)
	ch := make(chan agentapi.Event)
	go func() {
		defer close(ch)
		defer s.slot.set(nil)
		for ev := range out {
			s.rec.Observe(ev)
			s.traceEvent(ev)
			ch <- ev
		}
		if err := s.rec.Err(); err != nil && !s.warnedSave {
			// Recording is best effort once the answer is streaming: say
			// so where the engine's warnings go, but the turn's outcome
			// stays the turn's.
			s.warnedSave = true
			fmt.Fprintf(s.notices, "hint: warning: the session is not being saved: %v\n", err)
		}
	}()
	return ch, nil
}

func (s *rpcSession) Close() error {
	s.closeOnce.Do(func() {
		if s.file != nil {
			s.closeErr = s.file.Close()
		}
	})
	return s.closeErr
}

// tracef writes to the debug log when there is one.
func (s *rpcSession) tracef(format string, args ...any) {
	if s.trace != nil {
		s.trace.Printf(format, args...)
	}
}

// traceEvent writes an event to the debug log: the complete messages (the
// responses, in the sense the requirement means — the request bodies are
// logged by the provider client), the tool calls with their arguments,
// and everything that went wrong.
func (s *rpcSession) traceEvent(ev agentapi.Event) {
	if s.trace == nil {
		return
	}
	switch ev.Kind {
	case agentapi.EventMessage:
		body, err := json.Marshal(ev.Message)
		if err != nil {
			s.trace.Printf("message %s: (unmarshalable: %v)", ev.Message.Role, err)
			return
		}
		s.trace.Printf("message %s: %s", ev.Message.Role, body)
	case agentapi.EventToolStart:
		s.trace.Printf("tool %s start %s: %s", ev.Call.Name, ev.Call.ID, ev.Call.Arguments)
	case agentapi.EventToolEnd:
		s.trace.Printf("tool %s end %s: error=%v %d bytes: %s", ev.Result.Name, ev.Result.CallID, ev.Result.IsError, len(ev.Result.Text()), shell.FirstLine(ev.Result.Text()))
	case agentapi.EventPermission:
		s.trace.Printf("permission asked: %s", ev.Permission.Summary)
	case agentapi.EventCompaction:
		s.trace.Printf("compaction: %d messages replaced, ~%d -> ~%d tokens", ev.Compaction.MessagesReplaced, ev.Compaction.TokensBefore, ev.Compaction.TokensAfter)
	case agentapi.EventUsage:
		s.trace.Printf("usage: %+v", *ev.Usage)
	case agentapi.EventError:
		s.trace.Printf("error %s: %s", ev.Err.Kind, ev.Err.Message)
	case agentapi.EventTurnEnd:
		s.trace.Printf("turn end: %s", ev.FinishReason)
	case agentapi.EventTurnStart, agentapi.EventTextDelta, agentapi.EventThinkingDelta:
		// The deltas add up to the message logged above.
	}
}

// promptSlot is the gate's Prompter. RunTurn points it at the connection
// that owns the turn and clears it when the turn ends, so a question
// never blocks on a client that has already disconnected.
type promptSlot struct {
	mu sync.Mutex
	p  permission.Prompter
}

func (s *promptSlot) set(p permission.Prompter) {
	s.mu.Lock()
	s.p = p
	s.mu.Unlock()
}

func (s *promptSlot) Prompt(ctx context.Context, ask permission.Ask) (permission.Decision, error) {
	s.mu.Lock()
	p := s.p
	s.mu.Unlock()
	if p == nil {
		return permission.Deny, nil
	}
	return p.Prompt(ctx, ask)
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

// toolRegistry builds the tool set the engine offers the model: every
// built-in, write_file, edit_file and bash included, plus the
// instructions tool over the instruction files this run discovered. They
// are only safe to register because adopt() puts the agent behind a
// permission.Gate; main_test.go pins both halves of that.
func toolRegistry(root tool.Root, instructionPaths []string) (*tool.Registry, error) {
	return tool.NewRegistry(append(builtin.All(root), builtin.NewInstructions(instructionPaths))...)
}

// systemPrompt assembles the run's system message: the assistant's
// standing orders, then the project context — where it is, what it looks
// like, and what the project's own instruction files say. The instruction
// files come last so they read as the most specific guidance; they are
// the user's words to the agent, not tool output, which is why they
// belong in the system message at all. instructions is the layout to
// show (pc.Instructions, or one with the rules a conversation has
// touched) and pending the rules not loaded yet.
func systemPrompt(pc *project.Context, instructions []project.Instruction, pending []project.Rule) string {
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
	if instr := project.RenderInstructions(instructions); instr != "" {
		b.WriteString("\nThe project keeps instructions for assistants; follow them. " +
			"When files at several levels disagree, the one nearest the working directory wins. " +
			"A file with a paths attribute applies to files matching those patterns. " +
			"A section that ends in [...] is shown as its heading and first sentence only; " +
			"call instructions with the file's path and that heading before acting on what it covers.\n")
		b.WriteString(instr)
		b.WriteString("\n")
	}
	if index := project.RenderRuleIndex(pending); index != "" {
		b.WriteString("\n")
		b.WriteString(index)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
