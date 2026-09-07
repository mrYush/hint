// Command hint is the CLI of the agent: an interactive session by default,
// a one-shot answer with -p, and two small subcommands that look at the
// configuration (models) and the recorded conversations (sessions).
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/permission"
)

func main() {
	root := newRootCommand()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "hint:", err)
		os.Exit(1)
	}
}

// outputFormat is how a one-shot answer is printed.
type outputFormat string

const (
	// outputText streams the answer as it arrives. The default.
	outputText outputFormat = "text"
	// outputJSON prints one JSON object after the turn, for scripts.
	outputJSON outputFormat = "json"
)

// options are the choices of one invocation, as read from the flags.
type options struct {
	flags     config.Flags
	mode      permission.Mode
	session   sessionMode
	sessionID string
	debug     bool
	output    outputFormat
}

// newRootCommand builds the command tree. Flags are bound to locals and
// folded into an options value inside RunE, so that the code doing the
// work never sees cobra.
func newRootCommand() *cobra.Command {
	var flags config.Flags
	var ask, autoEdit, yolo bool
	var continueLast, resume, noSession bool
	var sessionID, prompt, output string
	var debug bool

	rootCmd := &cobra.Command{
		Use:   "hint [flags] [-p \"question\"]",
		Short: "A developer assistant that reads, edits and runs your project",
		Long: `hint is an agent for the directory you run it in. Without arguments it
starts an interactive session: type a question, read the answer, ask the
next one; the conversation is recorded and can be continued later with
-c. With -p it answers one question and exits, which is what a script
wants; --output json makes the answer machine-readable.

A question given as plain arguments (hint "question") still works as a
one-shot for compatibility and prints a note about -p.`,
		Example: `  hint                                   # interactive session
  hint -p "what does main.go do?"        # one answer, then exit
  hint -p "list the tests" --output json # for scripts
  hint -c                                # continue this directory's last session
  hint --session 3f2a                    # continue the session whose id starts with 3f2a
  hint sessions                          # list this directory's sessions
  hint models                            # list the models the profile serves`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A run-time failure is printed once, by main; cobra keeps
			// reporting flag errors itself, since those never reach here.
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			opts := options{
				flags:     flags,
				mode:      runMode(ask, autoEdit, yolo),
				session:   sessionModeOf(continueLast, resume, noSession, sessionID),
				sessionID: sessionID,
				debug:     debug,
			}
			switch outputFormat(output) {
			case outputText, outputJSON:
				opts.output = outputFormat(output)
			default:
				return fmt.Errorf("--output must be text or json, not %q", output)
			}
			inv, err := dispatch(prompt, args, opts.output, os.Stdin)
			if err != nil {
				return err
			}
			if inv.note != "" {
				fmt.Fprintln(os.Stderr, "hint:", inv.note)
			}
			// SIGTERM always ends the run. Ctrl-C ends a one-shot too,
			// which the provider layer reports as a cancel — never as a
			// network failure that would trigger a fake failover; the
			// REPL handles Ctrl-C itself, cancelling the turn and keeping
			// the session.
			signals := []os.Signal{syscall.SIGTERM}
			if !inv.interactive {
				signals = append(signals, os.Interrupt)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), signals...)
			defer stop()
			return run(ctx, opts, inv, stdio{in: os.Stdin, out: os.Stdout, err: os.Stderr, tty: isTerminal(os.Stdin)})
		},
	}

	rootCmd.Flags().StringVarP(&prompt, "prompt", "p", "", "Answer this one question and exit instead of starting a session")
	rootCmd.Flags().StringVar(&output, "output", string(outputText), "Output of a one-shot answer: text or json")

	// Run modes are a policy of this run, not part of a provider profile,
	// so they are plain flags rather than config.Flags. --ask exists so
	// that a script can spell the default out; the three are exclusive
	// and cobra rejects two at once.
	rootCmd.Flags().BoolVar(&ask, "ask", false, "Confirm every file edit and shell command (the default)")
	rootCmd.Flags().BoolVar(&autoEdit, "auto-edit", false, "Apply file edits without asking; still confirm shell commands")
	rootCmd.Flags().BoolVar(&yolo, "yolo", false, "Run every edit and command without asking (dangerous)")
	rootCmd.MarkFlagsMutuallyExclusive("ask", "auto-edit", "yolo")

	// Session selection. A run records its conversation by default; -c,
	// -r and --session pick an earlier one of this directory instead.
	rootCmd.Flags().BoolVarP(&continueLast, "continue", "c", false, "Continue the most recent session of this directory")
	rootCmd.Flags().BoolVarP(&resume, "resume", "r", false, "Pick a session of this directory to continue")
	rootCmd.Flags().StringVar(&sessionID, "session", "", "Continue the session of this directory with this id (a unique prefix will do)")
	rootCmd.Flags().BoolVar(&noSession, "no-session", false, "Do not record this conversation")
	rootCmd.MarkFlagsMutuallyExclusive("continue", "resume", "session", "no-session")

	// Configuration flags. Defaults stay empty on purpose: a non-empty flag
	// default would override values from config files (the pre-WP0.2
	// --model=gpt-4 bug). Built-in defaults live in internal/config.
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flags.Provider, "provider", "", "Provider profile to use for this run")
	pf.StringVar(&flags.APIURL, "api-url", "", "Base URL of the provider API (overrides the selected profile)")
	pf.StringVar(&flags.APIKey, "api-key", "", "API key (overrides the selected profile)")
	pf.StringVar(&flags.Model, "model", "", "Model name (overrides the selected profile)")
	pf.StringVar(&flags.ContextWindow, "context-window", "", "Context window of the model in tokens (overrides the selected profile)")
	pf.StringVar(&flags.InstructionBudget, "instruction-budget", "", "Bytes all HINT.md-style instruction files may take together (default 32768)")
	pf.StringVar(&flags.OverviewDepth, "overview-depth", "", "Directory levels listed in the system prompt; 0 for none (default 2)")
	pf.StringVar(&flags.OverviewEntries, "overview-entries", "", "Cap on the entries listed in the system prompt (default 100)")
	pf.BoolVar(&debug, "debug", false, "Write the run's requests, responses and tool calls (keys masked) to a log file")

	rootCmd.AddCommand(newSessionsCommand(), newModelsCommand(&flags))
	return rootCmd
}

// invocation is which of the run modes the flags and arguments asked for.
type invocation struct {
	// interactive is the REPL; otherwise question is answered once.
	interactive bool
	question    string
	// note is printed on stderr before the run: today, the migration
	// hint for a positional question.
	note string
}

// dispatch picks the run mode: -p is a one-shot, plain arguments are the
// compatibility one-shot with a note, and nothing at all is the
// interactive session — which needs a terminal to read from.
func dispatch(prompt string, args []string, output outputFormat, stdin *os.File) (invocation, error) {
	positional := strings.TrimSpace(strings.Join(args, " "))
	switch {
	case prompt != "" && positional != "":
		return invocation{}, errors.New("give the question either with -p or as arguments, not both")
	case prompt != "":
		return invocation{question: prompt}, nil
	case positional != "":
		return invocation{
			question: positional,
			note:     fmt.Sprintf("a question as arguments is deprecated; use: hint -p %q (bare hint starts an interactive session)", positional),
		}, nil
	}
	if output != outputText {
		return invocation{}, fmt.Errorf("--output %s needs a one-shot question: hint -p \"question\" --output %s", output, output)
	}
	if !isTerminal(stdin) {
		return invocation{}, errors.New("stdin is not a terminal, so there is nobody to ask; use hint -p \"question\" for a non-interactive run")
	}
	return invocation{interactive: true}, nil
}

// isTerminal reports whether f is a character device — a tty, but also
// /dev/null; a pipe or a regular file is not.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
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
	// sessionByID continues the session named by --session.
	sessionByID
	// sessionOff records nothing.
	sessionOff
)

// sessionModeOf maps the exclusive session flags onto a sessionMode.
func sessionModeOf(continueLast, resume, noSession bool, id string) sessionMode {
	switch {
	case noSession:
		return sessionOff
	case id != "":
		return sessionByID
	case resume:
		return sessionResume
	case continueLast:
		return sessionContinue
	default:
		return sessionNew
	}
}
