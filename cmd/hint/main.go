package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/console"
	dirctx "github.com/mrYush/hint/internal/context"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

func main() {
	var flags config.Flags
	var ask, autoEdit, yolo bool
	var continueLast, resume, noSession bool

	rootCmd := &cobra.Command{
		Use:   "hint [question]",
		Short: "A utility for getting contextual hints using LLM",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A run-time failure is printed once, by main; cobra keeps
			// reporting flag errors itself, since those never reach here.
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			// Ctrl-C cancels the context, which the provider layer reports
			// as a cancel — never as a network failure that would trigger a
			// fake failover.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return run(ctx, flags, runMode(ask, autoEdit, yolo),
				sessionModeOf(continueLast, resume, noSession), strings.Join(args, " "))
		},
	}

	// Run modes are a policy of this run, not part of a provider profile,
	// so they are plain flags rather than config.Flags. --ask exists so
	// that a script can spell the default out; the three are exclusive
	// and cobra rejects two at once.
	rootCmd.Flags().BoolVar(&ask, "ask", false, "Confirm every file edit and shell command (the default)")
	rootCmd.Flags().BoolVar(&autoEdit, "auto-edit", false, "Apply file edits without asking; still confirm shell commands")
	rootCmd.Flags().BoolVar(&yolo, "yolo", false, "Run every edit and command without asking (dangerous)")
	rootCmd.MarkFlagsMutuallyExclusive("ask", "auto-edit", "yolo")

	// Session selection. A run records its conversation by default; -c
	// and -r pick an earlier one of this directory to continue instead.
	rootCmd.Flags().BoolVarP(&continueLast, "continue", "c", false, "Continue the most recent session of this directory")
	rootCmd.Flags().BoolVarP(&resume, "resume", "r", false, "Pick a session of this directory to continue")
	rootCmd.Flags().BoolVar(&noSession, "no-session", false, "Do not record this conversation")
	rootCmd.MarkFlagsMutuallyExclusive("continue", "resume", "no-session")

	// Configuration flags. Defaults stay empty on purpose: a non-empty flag
	// default would override values from config files (the pre-WP0.2
	// --model=gpt-4 bug). Built-in defaults live in internal/config.
	rootCmd.PersistentFlags().StringVar(&flags.Provider, "provider", "", "Provider profile to use for this run")
	rootCmd.PersistentFlags().StringVar(&flags.APIURL, "api-url", "", "Base URL of the provider API (overrides the selected profile)")
	rootCmd.PersistentFlags().StringVar(&flags.APIKey, "api-key", "", "API key (overrides the selected profile)")
	rootCmd.PersistentFlags().StringVar(&flags.Model, "model", "", "Model name (overrides the selected profile)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runMode maps the three exclusive flags onto a [permission.Mode]; none
// set is ask.
func runMode(ask, autoEdit, yolo bool) permission.Mode {
	switch {
	case yolo:
		return permission.ModeYolo
	case autoEdit:
		return permission.ModeAutoEdit
	case ask:
		return permission.ModeAsk
	default:
		return permission.ModeAsk
	}
}

// sessionMode says which session a run records into.
type sessionMode int

const (
	// sessionNew starts a fresh session. The default.
	sessionNew sessionMode = iota
	// sessionContinue appends to the directory's most recent session.
	sessionContinue
	// sessionResume lets the user pick one of the directory's sessions.
	sessionResume
	// sessionOff records nothing.
	sessionOff
)

// sessionModeOf maps the exclusive session flags onto a sessionMode.
func sessionModeOf(continueLast, resume, noSession bool) sessionMode {
	switch {
	case noSession:
		return sessionOff
	case resume:
		return sessionResume
	case continueLast:
		return sessionContinue
	default:
		return sessionNew
	}
}

// run loads the configuration, builds the provider stack, and streams one
// answer to stdout.
func run(ctx context.Context, flags config.Flags, mode permission.Mode, smode sessionMode, question string) error {
	cfg, err := config.Load(flags)
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	// The WP0.9 file logger does not exist yet; when HINT_DEBUG is set the
	// clients' one-line notes go to stderr, redacted against every
	// configured secret.
	var debugLog func(string)
	if cfg.Debug {
		secrets := cfg.Secrets()
		debugLog = func(line string) {
			fmt.Fprintln(os.Stderr, "hint debug:", config.Redact(line, secrets))
		}
	}

	chat, err := provider.Chat(cfg, os.Stderr, debugLog)
	if err != nil {
		return err
	}

	dc, err := dirctx.GetDirectoryContext()
	if err != nil {
		return fmt.Errorf("getting context: %w", err)
	}
	root, err := tool.NewRoot(dc.CurrentDir)
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}
	registry, err := toolRegistry(root)
	if err != nil {
		return fmt.Errorf("registering tools: %w", err)
	}

	// One reader owns stdin for the whole run: the session picker and the
	// permission prompter both ask through it, so neither can swallow the
	// other's answer.
	lines := console.NewLineReader(os.Stdin)

	home, err := os.UserHomeDir()
	if err != nil {
		home = "" // sessions then land under the relative default; better than refusing to run
	}
	store := session.NewStore(session.DefaultDir(home, os.LookupEnv))
	sess, err := openSession(ctx, store, root.Dir(), smode, lines, os.Stderr)
	if err != nil {
		return err
	}
	if sess != nil {
		defer func() {
			if err := sess.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "hint: warning: %v\n", err)
			}
		}()
	}

	// The preamble is this run's, not the conversation's: it is rebuilt
	// every time (WP0.8 will derive it from HINT.md) and never stored, so
	// the Recorder is told how long it is.
	preamble := []agentapi.Message{agentapi.SystemMessage(systemPrompt(dc))}
	user := agentapi.UserMessage(question)
	history := append([]agentapi.Message(nil), preamble...)
	if sess != nil {
		history = append(history, sess.Messages()...)
		// Fail before the first request if the session cannot be written
		// at all; a read-only disk is better learned about now than after
		// the answer.
		if err := sess.AppendMessage(user); err != nil {
			return err
		}
	}
	history = append(history, user)
	rec := session.NewRecorder(sess, len(preamble))

	warnMode(os.Stderr, mode, os.Stdin)
	gate := permission.New(mode, permission.NewLinePrompter(lines, os.Stderr))
	a := agent.New(chat, agent.WithTools(registry.Tools()...), agent.WithAuthorizer(gate))

	printedText := false
	// lastErr remembers the most recent EventError. Per the contract,
	// EventError does not necessarily end the turn — a non-terminal
	// compaction failure is reported this way too, and the turn carries on
	// — so it only becomes the command's result if EventTurnEnd actually
	// closes with FinishError.
	var lastErr *agentapi.Error
	var runErr error
	for ev := range a.RunTurn(ctx, history) {
		rec.Observe(ev)
		switch ev.Kind {
		case agentapi.EventTextDelta:
			fmt.Print(ev.Text)
			printedText = true
		case agentapi.EventPermission:
			// The prompt itself is drawn by the gate's ReaderPrompter,
			// which also reads the answer; rendering it here too would
			// print it twice. The event still travels the stream for a
			// Phase 1 client that answers over RPC.
		case agentapi.EventToolStart:
			// Tool activity goes to stderr so the answer on stdout stays
			// clean for pipes and scripts.
			fmt.Fprintf(os.Stderr, "hint: %s %s\n", ev.Call.Name, summarizeArgs(ev.Call.Arguments))
		case agentapi.EventToolEnd:
			switch {
			case ev.Result.IsError:
				fmt.Fprintf(os.Stderr, "hint: %s failed: %s\n", ev.Result.Name, firstLine(ev.Result.Text()))
			case ev.Result.Name == "todo":
				// The plan is for the user as much as for the model.
				fmt.Fprintln(os.Stderr, ev.Result.Text())
			}
		case agentapi.EventCompaction:
			fmt.Fprintf(os.Stderr, "hint: compacted %d messages into a summary\n", ev.Compaction.MessagesReplaced)
		case agentapi.EventError:
			lastErr = ev.Err
		case agentapi.EventTurnEnd:
			if printedText {
				fmt.Println()
			}
			switch ev.FinishReason {
			case agentapi.FinishStop, agentapi.FinishToolCalls, agentapi.FinishContentFilter:
				// Nothing beyond the newline above: a normal stop, and
				// FinishToolCalls never actually reaches EventTurnEnd (a
				// tool round always loops back into the agent, never ends
				// the turn directly).
			case agentapi.FinishCanceled:
				runErr = fmt.Errorf("interrupted")
			case agentapi.FinishLength:
				fmt.Fprintln(os.Stderr, "hint: response was truncated by the provider's token limit")
			case agentapi.FinishError:
				runErr = lastErr
			}
		}
	}
	if err := rec.Err(); err != nil {
		// Recording is best effort once the answer is streaming: say so,
		// but the exit status stays the turn's.
		fmt.Fprintf(os.Stderr, "hint: warning: the session was not fully saved: %v\n", err)
	}
	return runErr
}

// openSession returns the session this run records into per smode, or nil
// for sessionOff. Continuing or resuming an existing session prints a
// one-line notice on stderr, and any load warnings after it; a directory
// with nothing to continue falls back to a new session rather than
// refusing to run.
func openSession(ctx context.Context, store *session.Store, cwd string, smode sessionMode, lines *console.LineReader, stderr io.Writer) (*session.Session, error) {
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

// toolRegistry builds the tool set the CLI offers the model: every
// built-in, write_file, edit_file and bash included. They are only safe
// to register because run() puts the agent behind a permission.Gate;
// main_test.go pins both halves of that.
func toolRegistry(root tool.Root) (*tool.Registry, error) {
	return tool.NewRegistry(builtin.All(root)...)
}

// warnMode prints the one-time notices a run mode deserves: --yolo is
// loud because nothing will ask again, and a run that cannot ask at all —
// stdin is not a terminal — says so up front instead of surprising the
// user with a string of denials.
func warnMode(w io.Writer, mode permission.Mode, stdin *os.File) {
	if mode == permission.ModeYolo {
		fmt.Fprintln(w, "hint: WARNING: --yolo: file edits and shell commands will run WITHOUT confirmation")
		return
	}
	if info, err := stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
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

// systemPrompt is the pre-WP0.3 prompt plus a pointer at the tools; WP0.8
// replaces it with real project context.
func systemPrompt(dc *dirctx.DirectoryContext) string {
	return fmt.Sprintf(
		"You are a helpful assistant aiding a developer with their project. "+
			"Current directory: %s\n"+
			"Top-level entries: %s\n\n"+
			"You have tools to explore the project: list_dir, read_file, glob and grep. "+
			"Use them to look at the actual code before answering instead of guessing, "+
			"and refer to files by their paths. Use todo to show a plan for multi-step work. "+
			"You can change the project with edit_file (preferred for targeted changes) and write_file, "+
			"and run commands with bash; the user is shown each edit as a diff and each command "+
			"before it runs and may decline it. A declined action must not be retried unchanged.",
		dc.CurrentDir,
		strings.Join(dc.Files, ", "),
	)
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
