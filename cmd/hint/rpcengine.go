package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/mcp"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/internal/repomap"
	"github.com/mrYush/hint/internal/repomap/worker"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/internal/shell"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/internal/workspace"
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
	// denied are the paths the file tools refuse inside the working
	// directory: the configuration and session directories (WP1.7). The
	// project's own .hint/config.yaml is denied regardless.
	denied []string
	// consent decides whether a project MCP server may start. Nil keeps
	// every project server disabled, which is what a test without a
	// config directory gets.
	consent mcp.Consent
	// mcpDial replaces the real MCP transport in tests.
	mcpDial mcp.DialFunc
	// mapPath opts into the persistent optional catalog; tests use private paths.
	mapPath   string
	mapRunner repomap.Extractor
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
	// mcp owns the configured MCP servers; nil when none are configured.
	mcp *mcp.Manager
	// mcpMu guards the one-time lazy load of the MCP tools. mcpLoaded
	// says Tools ran, mcpErr keeps a failed registration from retrying
	// every turn, and mcpWarned is how many warnings were already shown.
	mcpMu     sync.Mutex
	mcpLoaded bool
	mcpErr    error
	mcpWarned int
	notices   io.Writer
	trace     *debuglog.Log
	debugLog  func(string)
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

	// attachMu serializes looking a session up and opening it, so two
	// connections attaching one session do not open its file twice: the
	// second open would collide with this process's own lock.
	attachMu    sync.Mutex
	mu          sync.Mutex
	live        map[string]*rpcSession
	workspace   *workspace.Registry
	mapIndex    *repomap.Manager
	primaryRoot tool.Root
	closeOnce   sync.Once
	indexModeMu sync.RWMutex
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
	if opts.indexMaxBytes != 0 && opts.indexMaxBytes < 512<<10 {
		return nil, nil, errors.New("--index-max-bytes must be at least 524288 bytes")
	}
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
	var mapPath string
	if cache, cacheErr := os.UserCacheDir(); cacheErr == nil {
		mapPath = filepath.Join(cache, "hint", "index", "code-v2.sqlite")
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
	store := session.NewStore(session.DefaultDir(home, os.LookupEnv))
	warnFileKeys(cfg, notices)
	consent := mcpConsent(home)
	eng, err := newEngine(ctx, assembly{
		mapPath: mapPath,
		chat:    chat,
		cfg:     cfg,
		opts:    opts,
		cwd:     cwd,
		store:   store,
		global:  global,
		trace:   trace,
		notices: notices,
		reload:  func() (*config.Config, error) { return config.Load(opts.flags) },
		denied:  deniedPaths(home, cwd, os.LookupEnv, store.Dir()),
		consent: consent,
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	baseCleanup := cleanup
	cleanup = func() {
		eng.closeMCP()
		baseCleanup()
	}
	return eng, cleanup, nil
}

// deniedPaths are hint's own files, which the file tools refuse: every
// config file the loader reads, under its current or legacy name and
// whether it exists yet, the global config directory, and the sessions.
func deniedPaths(home, cwd string, lookupEnv func(string) (string, bool), sessions string) []string {
	return append(config.Paths(home, cwd, lookupEnv), config.GlobalDir(home, lookupEnv), sessions)
}

// newEngine builds the stack for as: the working-directory root, the
// project context, the tools, the limits. Warnings that belong to the
// run (a cut HINT.md, git refusing to answer) are printed here once.
func newEngine(ctx context.Context, as assembly) (_ *rpcEngine, resultErr error) {
	root, err := tool.NewRoot(as.cwd)
	if err != nil {
		return nil, fmt.Errorf("working directory: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, root.Close())
		}
	}()
	// hint's own files are off limits to the file tools even when the
	// working directory holds them (a run from $HOME, or the project's
	// .hint/config.yaml): a key must not reach the model through
	// read_file. The engine reads them itself, not through the tools.
	denied := append([]string{filepath.Join(root.Dir(), ".hint", "config.yaml")}, as.denied...)
	if as.mapPath != "" {
		denied = append(denied, filepath.Dir(as.mapPath))
	}
	root = root.Deny(denied...)

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
	for _, w := range pc.Snapshot().Warnings {
		fmt.Fprintf(as.notices, "hint: warning: %s\n", config.Redact(w, as.cfg.Secrets()))
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
	e := &rpcEngine{
		primaryRoot: root,
		cwd:         root.Dir(),
		store:       as.store,
		chat:        as.chat,
		pc:          pc,
		mode:        as.opts.mode,
		limits:      limits,
		notices:     as.notices,
		trace:       as.trace,
		debugLog:    debugLog,
		setup:       newSetupInfo(as.cfg, as.cwd, as.opts.flags),
		cfg:         as.cfg,
		reload:      as.reload,
		providers:   map[rpc.ProfileRef]agentapi.ChatProvider{{Profile: as.cfg.DefaultProvider}: as.chat},
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if e.mapIndex != nil {
			resultErr = errors.Join(resultErr, e.mapIndex.Close())
		}
		if e.workspace != nil {
			resultErr = errors.Join(resultErr, e.workspace.Close())
		}
		if e.mcp != nil {
			resultErr = errors.Join(resultErr, e.mcp.Close())
		}
	}()
	e.workspace, err = workspace.New(root, workspace.WithRootPolicy(func(attached tool.Root) tool.Root {
		denied := append([]string{filepath.Join(attached.Dir(), ".hint", "config.yaml")}, as.denied...)
		if as.mapPath != "" {
			denied = append(denied, filepath.Dir(as.mapPath))
		}
		return attached.Deny(denied...)
	}))
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	if as.mapPath != "" {
		runner := as.mapRunner
		if runner == nil {
			if executable, executableErr := os.Executable(); executableErr == nil {
				runner = &worker.Process{Command: []string{executable, "internal-extract"}, Environ: e.environ, Timeout: 3 * time.Second}
			}
		}
		e.mapIndex, err = repomap.New(ctx, repomap.Options{Path: as.mapPath, Runner: runner,
			Environ: e.environ, Sampler: repomap.LocalResourceSampler(as.mapPath), Store: index.Options{MaxBytes: as.opts.indexMaxBytes}})
		if err != nil {
			fmt.Fprintf(as.notices, "hint: code index unavailable: %s\n", config.Redact(err.Error(), e.secrets()))
		}
	}
	e.chat = e.observeInference(e.chat)
	e.providers[rpc.ProfileRef{Profile: as.cfg.DefaultProvider}] = e.chat
	// MCP servers are created here but not started: the first turn calls
	// ensureMCP, and prompt operations connect explicitly, so a run that never
	// uses MCP pays no process startup cost. Config and consent problems that
	// are knowable without a connection are reported now.
	manager, err := mcp.NewManager(mcpServers(as.cfg), mcp.Options{
		Client:      mcp.ClientInfo{Name: "hint", Version: buildVersion()},
		ProjectRoot: root.Dir(),
		Environ:     e.environ(),
		Consent:     as.consent,
		Budget:      mcp.DefaultBudget(),
		Dial:        as.mcpDial,
	})
	if err != nil {
		return nil, fmt.Errorf("MCP servers: %w", err)
	}
	e.mcp = manager
	// The tools read the secrets and the environment through the engine,
	// so a configuration reloaded by a profile switch is what they see.
	registry, err := toolRegistry(root, pc, e.secrets, e.environ)
	if err != nil {
		return nil, fmt.Errorf("registering tools: %w", err)
	}
	e.registry = registry
	e.mcp = manager
	secrets := e.secrets()
	e.mcpWarned = len(manager.Warnings())
	for _, w := range manager.Warnings() {
		fmt.Fprintf(as.notices, "hint: warning: %s\n", config.Redact(w, secrets))
	}
	return e, nil
}

// closeMCP releases owned helpers, storage and roots once the engine has stopped.
func (e *rpcEngine) closeMCP() {
	e.closeOnce.Do(func() {
		var err error
		if e.mapIndex != nil {
			err = errors.Join(err, e.mapIndex.Close())
		}
		if e.workspace != nil {
			err = errors.Join(err, e.workspace.Close())
		}
		err = errors.Join(err, e.primaryRoot.Close())
		if e.mcp != nil {
			err = errors.Join(err, e.mcp.Close())
		}
		if err != nil && e.notices != nil {
			fmt.Fprintf(e.notices, "hint: cleanup: %s\n", config.Redact(err.Error(), e.secrets()))
		}
	})
}

// secrets is every configured API key, for the redaction of tool results.
func (e *rpcEngine) secrets() []string {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	return e.cfg.Secrets()
}

// environ is the process environment without the API keys: what bash and
// ripgrep are started with.
func (e *rpcEngine) environ() []string {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	return config.FilterEnviron(os.Environ(), e.cfg.SecretEnv(), e.cfg.Secrets())
}

// warnFileKeys says once, at start, which profiles keep a literal key in
// a config file while the OS secret store would take it — the way to
// move it is hint setup, which is never done behind the user's back.
func warnFileKeys(cfg *config.Config, notices io.Writer) {
	if cfg.SecretStore.Store == config.StoreFile {
		return
	}
	var names []string
	for _, p := range cfg.Providers {
		if p.KeyFrom == config.KeyFromFile {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 || !config.KeyringAvailable(config.OSKeyring{}) {
		return
	}
	fmt.Fprintf(notices, "hint: warning: profile %s keeps its API key in a config file; hint setup moves it into the OS secret store\n", strings.Join(names, ", "))
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
	if live := e.liveSession(id); live != nil {
		return live, nil
	}
	e.attachMu.Lock()
	defer e.attachMu.Unlock()
	if live := e.liveSession(id); live != nil {
		return live, nil
	}
	sess, err := e.store.Find(e.cwd, id)
	if err != nil {
		// A prefix of a session this engine already holds names that
		// session: Find tried to open its file and met this process's own
		// lock. A lock held by another process falls through to busy.
		if errors.Is(err, session.ErrLocked) {
			if live := e.livePrefix(id); live != nil {
				return live, nil
			}
		}
		return nil, sessionLookupError(err, id)
	}
	return e.adopt(sess, sess.ID()), nil
}

// liveSession returns the live session with exactly id.
func (e *rpcEngine) liveSession(id string) *rpcSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.live[id]
}

// livePrefix returns the live session id is a unique prefix of, or nil.
// A file lookup has already resolved the prefix to one file before this
// runs, so a second match would mean the two disagree; nil then lets the
// caller report the lookup's own result.
func (e *rpcEngine) livePrefix(id string) *rpcSession {
	if id == "" {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var match *rpcSession
	for full, live := range e.live {
		if !strings.HasPrefix(full, id) {
			continue
		}
		if match != nil {
			return nil
		}
		match = live
	}
	return match
}

// sessionLookupError maps the store's errors onto the engine's: a missing
// session is not found, and one another hint process records is busy.
func sessionLookupError(err error, id string) error {
	switch {
	case errors.Is(err, session.ErrLocked):
		return fmt.Errorf("%w: session %s is open in another hint process", rpc.ErrBusy, id)
	case errors.Is(err, session.ErrNoSessions), errors.Is(err, session.ErrUnknownSession):
		return fmt.Errorf("%w: %s", rpc.ErrNotFound, strings.TrimPrefix(err.Error(), "session: "))
	default:
		return err
	}
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
	pre := newPreamble(e.pc, e.registry, e.notices, trace, func(warning string) string {
		return config.Redact(warning, e.secrets())
	})
	initial := pre.initial()
	live := &rpcSession{
		workspace: e.workspace.NewSession(),
		id:        id,
		created:   time.Now(),
		eng:       e,
		file:      sess,
		rec:       session.NewRecorder(sess, len(initial)),
		pre:       pre,
		gate:      gate,
		slot:      slot,
		notices:   e.notices,
		trace:     e.trace,
	}
	live.restoreWorkspace()
	pre.lookup = live.lookupSessionTool
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
	workspace        *workspace.Session
	repoInstructions repoInstructions
	reposMu          sync.Mutex
	repoHistory      []session.WorkspaceRef
	repoTurns        int
	id               string
	created          time.Time
	eng              *rpcEngine
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
	// agChat and agLimits are what use built the agent from; applyMCP
	// rebuilds it with the MCP tools once they are loaded.
	agChat          agentapi.ChatProvider
	agLimits        agent.Limits
	mcpApplied      bool
	catalogMode     repomap.Mode
	catalogRevision uint64

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
	s.eng.indexModeMu.RLock()
	release, err := s.workspace.BeginTurn()
	if err != nil {
		s.eng.indexModeMu.RUnlock()
		return nil, fmt.Errorf("begin workspace turn: %w", err)
	}
	releaseWorkspace := release
	release = func() {
		releaseWorkspace()
		s.eng.indexModeMu.RUnlock()
	}
	s.slot.set(ask)
	// MCP servers start on the first turn, not while the engine is built.
	// A failure here is a turn failure, before anything is recorded.
	if err := s.eng.ensureMCP(ctx); err != nil {
		release()
		s.slot.set(nil)
		return nil, err
	}
	s.applyMCP(ctx)
	user := agentapi.UserMessage(text)
	if err := s.rec.Append(user); err != nil {
		release()
		s.slot.set(nil)
		return nil, err
	}
	s.repoTurns++
	history := append(append([]agentapi.Message{}, s.pre.initial()...), s.rec.Messages()...)
	s.agMu.Lock()
	ag := s.ag
	s.agMu.Unlock()
	s.tracef("turn: %q (%d messages)", text, len(history))
	out := ag.RunTurn(ctx, history)
	ch := make(chan agentapi.Event)
	go func() {
		// Closing the stream lets callers start another turn immediately.
		// Release all turn state first; deferred calls run in reverse order.
		defer close(ch)
		defer release()
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

// addMCPResources joins configured resources to the same instruction layout
// as the project's files. Only this engine-wiring file converts between the
// MCP and project representations.
func (e *rpcEngine) addMCPResources(ctx context.Context, resources []mcp.ResourceContent) {
	if len(resources) == 0 {
		return
	}
	sources := make([]project.InstructionSource, 0, len(resources))
	for _, resource := range resources {
		sources = append(sources, project.InstructionSource{Path: resource.InstructionPath(), Content: resource.Text})
	}
	warnings := e.pc.AddInstructionSources(ctx, sources)
	for _, w := range warnings {
		fmt.Fprintf(e.notices, "hint: warning: %s\n", config.Redact(w, e.secrets()))
	}
}

// toolRegistry builds the tool set the engine offers the model: every
// built-in, write_file, edit_file and bash included, plus the
// instructions tool over the instruction documents this run discovered and
// any MCP tools the caller passes. They are only safe to register because
// adopt() puts the agent behind a permission.Gate; main_test.go pins both
// halves of that.
func toolRegistry(root tool.Root, pc *project.Context, secrets func() []string, environ func() []string, extra ...agentapi.Tool) (*tool.Registry, error) {
	var opts []builtin.Option
	if environ != nil {
		opts = append(opts, builtin.WithEnviron(environ))
	}
	instructions := builtin.NewInstructions(nil)
	if pc != nil {
		instructions = builtin.NewProjectInstructions(pc)
	}
	tools := append(builtin.All(root, opts...), instructions)
	tools = append(tools, extra...)
	for i, t := range tools {
		// Every result passes the redaction, so a key that leaked some
		// other way never reaches the model, an event or the session.
		tools[i] = tool.Redacting(t, secrets)
	}
	return tool.NewRegistry(tools...)
}

// systemPrompt assembles the run's system message: the product identity and
// the assistant's standing orders, then the project context — where it is,
// what it looks like, and what the project's own instruction files say. The
// identity comes first so it is established before project instructions,
// which may extend the assistant's behaviour; the instruction files come
// last so they read as the most specific guidance. They are the user's
// words to the agent, not tool output, which is why they belong in the
// system message at all. instructions is the layout to show (pc.Instructions,
// or one with the rules a conversation has touched) and pending the rules
// not loaded yet.
func systemPrompt(pc *project.Context, instructions []project.Instruction, pending []project.Rule) string {
	var b strings.Builder
	// The identity is deliberately the first thing in the prompt: a fresh
	// session with no configuration must still know the product it is, and
	// the model is instructed to name the provider only as its engine.
	b.WriteString("You are hint, an open-source coding assistant developed by mrYush (https://github.com/mrYush/hint). " +
		"When asked who you are, give your product name, developer, and source URL. " +
		"The configured provider and model are the engine you run on, not your identity or your developer. " +
		"You help the user with their project.\n\n" +
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

// applyMCP rebuilds after lazy MCP loading or a between-turn capability change.
// The permission gate and recorder survive; a new agent drops the previous
// request's context-cost baseline when its tool catalog changes.
func (s *rpcSession) applyMCP(ctx context.Context) {
	s.agMu.Lock()
	defer s.agMu.Unlock()
	var mode repomap.Mode
	if s.eng.mapIndex != nil {
		mode = s.eng.mapIndex.Mode()
	}
	_, revision := s.workspace.Snapshot()
	if s.mcpApplied && s.catalogMode == mode && s.catalogRevision == revision {
		return
	}
	s.catalogMode = mode
	s.catalogRevision = revision
	s.mcpApplied = true
	s.pre.refresh(ctx)
	if s.agChat == nil {
		return
	}
	s.ag = agent.New(s.agChat,
		agent.WithTools(s.sessionTools()...),
		agent.WithAuthorizer(s.gate),
		agent.WithLimits(s.agLimits),
		agent.WithPreamble(s.workspacePreamble()),
	)
}
