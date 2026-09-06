package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

const (
	bashName = "bash"
	// defaultBashTimeout applies when the call names none.
	defaultBashTimeout = 60 * time.Second
	// maxBashTimeout caps what a call may ask for.
	maxBashTimeout = 10 * time.Minute
	// bashWaitDelay is how long, after the process exits or is killed, to
	// wait for its output pipes to close before giving up on a child that
	// inherited them.
	bashWaitDelay = 2 * time.Second
)

const bashDescription = `Run a shell command in the working directory and return its output.

Use it to build, test, run linters, inspect git, or anything else the other tools do not cover; prefer read_file, list_dir, glob and grep for reading and searching files. The command runs non-interactively with no stdin, in a fresh shell each call (state such as cd or exported variables does not persist). stdout and stderr are returned separately, followed by the exit code when it is not zero. The default timeout is 60 seconds; pass timeout (in seconds, up to 600) for longer builds. Output is truncated in the middle when very long.`

type bashArgs struct {
	Command string   `json:"command" jsonschema_description:"The shell command to run"`
	Timeout tool.Int `json:"timeout,omitempty" jsonschema_description:"Timeout in seconds (default 60, maximum 600)"`
}

var bashSchema = tool.MustSchema(bashArgs{})

type bash struct {
	root      tool.Root
	maxOutput int
}

func newBash(root tool.Root, o options) agentapi.Tool {
	// No decorator timeout: the tool enforces its own per-call one, which
	// can legitimately exceed the default limit for a long build.
	return tool.WithLimits(&bash{root: root, maxOutput: o.limits.MaxOutput},
		tool.Limits{Timeout: 0, MaxOutput: o.limits.MaxOutput})
}

func (*bash) Name() string                 { return bashName }
func (*bash) Description() string          { return bashDescription }
func (*bash) InputSchema() json.RawMessage { return bashSchema }
func (*bash) Class() agentapi.ActionClass  { return agentapi.ClassExecute }

// Describe implements tool.Describer. Detail is the command and nothing
// else: the permission layer derives its allow-list key from it, and a
// user confirming a command must see exactly what the shell will get.
func (*bash) Describe(_ context.Context, args json.RawMessage) (tool.Description, error) {
	var in bashArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.Description{}, err
	}
	cmd := strings.TrimSpace(in.Command)
	if cmd == "" {
		return tool.Description{}, errors.New("command is required")
	}
	summary := fmt.Sprintf("run %q", firstLineOf(cmd, 80))
	if in.Timeout > 0 {
		summary += fmt.Sprintf(" (timeout %ds)", int(in.Timeout))
	}
	return tool.Description{Summary: summary, Detail: cmd}, nil
}

// firstLineOf returns the first line of s cut at max bytes, with an
// ellipsis when anything was left out.
func firstLineOf(s string, max int) string {
	cut := false
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s, cut = s[:i], true
	}
	if len(s) > max {
		s, cut = s[:max], true
	}
	if cut {
		s += "..."
	}
	return s
}

// Run implements agentapi.Tool.
func (t *bash) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in bashArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.InvalidArgs(callID, bashName, err), nil
	}
	if strings.TrimSpace(in.Command) == "" {
		return tool.InvalidArgs(callID, bashName, errors.New("command is required")), nil
	}
	if in.Timeout < 0 {
		return tool.InvalidArgs(callID, bashName, errors.New("timeout must not be negative")), nil
	}
	timeout := time.Duration(in.Timeout) * time.Second
	if timeout == 0 {
		timeout = defaultBashTimeout
	}
	timeout = min(timeout, maxBashTimeout)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd, err := shellCommand(ctx, in.Command)
	if err != nil {
		return agentapi.ToolResult{}, fmt.Errorf("bash: %w", err)
	}
	cmd.Dir = t.root.Dir()
	cmd.Env = os.Environ()
	cmd.WaitDelay = bashWaitDelay
	cmd.Cancel = func() error { return killProcessTree(cmd) }

	// stdout gets the larger share: it is what the model asked for, and
	// the decorator's cap still bounds the two together.
	stdout := tool.NewBoundedBuffer(t.maxOutput * 2 / 3)
	stderr := tool.NewBoundedBuffer(t.maxOutput / 3)
	cmd.Stdout, cmd.Stderr = stdout, stderr

	runErr := cmd.Run()

	var b strings.Builder
	b.WriteString(stdout.String())
	if stderr.Len() > 0 {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("--- stderr ---\n")
		b.WriteString(stderr.String())
	}
	text := b.String()

	note := func(s string) string {
		if text == "" {
			return s
		}
		return strings.TrimRight(text, "\n") + "\n" + s
	}

	var exit *exec.ExitError
	switch {
	case runErr == nil:
		if text == "" {
			text = "(no output)"
		}
		return agentapi.TextResult(callID, bashName, text), nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return agentapi.ErrorResult(callID, bashName, note(fmt.Sprintf("[timed out after %s; process killed]", timeout))), nil
	case ctx.Err() != nil:
		return agentapi.ErrorResult(callID, bashName, note("[canceled]")), nil
	case errors.Is(runErr, exec.ErrWaitDelay):
		// The command itself exited; a child it started kept the output
		// pipes open past the grace period and was cut loose.
		if text == "" {
			text = "(no output)"
		}
		return agentapi.TextResult(callID, bashName, note("[a background child process was detached]")), nil
	case errors.As(runErr, &exit):
		return agentapi.ErrorResult(callID, bashName, note(fmt.Sprintf("[exit code %d]", exit.ExitCode()))), nil
	default:
		// The shell could not be started at all: that is the tool's
		// machinery, not the command, failing.
		return agentapi.ToolResult{}, fmt.Errorf("bash: %w", runErr)
	}
}
