package permission

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// maxDetail bounds how much of a request's Detail the prompt prints: a
// diff of a whole rewritten file is not something a user reads in a
// terminal, and the marker tool.Truncate leaves says what was cut. The
// request itself travels the event stream untruncated — how to show a
// long Detail is the client's decision, per the contract.
const maxDetail = 16_000

// Decision is a user's answer to a permission prompt.
type Decision int

const (
	// Deny refuses the call. The zero value, so a Prompter that returns
	// without deciding denies.
	Deny Decision = iota
	// Allow lets this one call run.
	Allow
	// AllowAlways lets the call run and remembers the grant for the rest
	// of the run — see Ask.Always for what exactly is remembered.
	AllowAlways
)

// Ask is one question put to a Prompter.
type Ask struct {
	// Request is what the agent wants to do, with the preview in Detail.
	Request agentapi.PermissionRequest
	// Always says what an AllowAlways answer would remember, for the
	// prompt to show, e.g. `commands starting with "go test"`. Empty when
	// the option is not offered.
	Always string
}

// Prompter puts a permission question to whoever can answer it. The CLI's
// implementation reads stdin; Phase 1's will relay the question over RPC.
//
// Prompt blocks until there is an answer. It returns ctx.Err() when the
// context ends first, so that Ctrl-C at a prompt cancels the turn the same
// way it does anywhere else. Any other error means the prompter itself
// broke; the gate treats that as a denial.
type Prompter interface {
	Prompt(ctx context.Context, ask Ask) (Decision, error)
}

// ReaderPrompter asks on an io.Writer and reads one-line answers through a
// [console.LineReader] — the CLI's stderr and stdin.
//
// Answers: y/yes, n/no, a/always; an empty line is no, so that Enter alone
// is the safe choice. Reaching EOF denies: an unattended run (stdin from
// /dev/null, a pipe that closed) must not approve anything by accident.
//
// The line reader is shared with every other prompt in the process (the
// session picker, WP0.9's REPL): nothing else may read the same stream
// beside it. A line that arrived while no prompt was waiting (typed after
// a Ctrl-C, say) is discarded before the next question is printed, so it
// cannot be taken as the answer to it.
type ReaderPrompter struct {
	out   io.Writer
	lines *console.LineReader
}

// NewReaderPrompter returns a prompter over in and out. It owns in for the
// life of the process; when another component needs the same stream, build
// one [console.LineReader] and use [NewLinePrompter] instead.
func NewReaderPrompter(in io.Reader, out io.Writer) *ReaderPrompter {
	return NewLinePrompter(console.NewLineReader(in), out)
}

// NewLinePrompter returns a prompter that reads answers from lines and
// writes questions to out.
func NewLinePrompter(lines *console.LineReader, out io.Writer) *ReaderPrompter {
	return &ReaderPrompter{out: out, lines: lines}
}

// Prompt implements Prompter.
func (p *ReaderPrompter) Prompt(ctx context.Context, ask Ask) (Decision, error) {
	p.lines.DiscardPending()

	req := ask.Request
	fmt.Fprintf(p.out, "\nhint: %s\n", req.Summary)
	if detail := strings.TrimRight(tool.Truncate(req.Detail, maxDetail), "\n"); detail != "" {
		fmt.Fprintln(p.out, detail)
	}
	options := "[y]es / [N]o"
	if ask.Always != "" {
		options += fmt.Sprintf(" / [a]lways (%s)", ask.Always)
	}

	for {
		fmt.Fprintf(p.out, "Allow? %s: ", options)
		text, err := p.lines.ReadLine(ctx)
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(p.out)
				return Deny, ctx.Err()
			}
			// EOF or a broken pipe: nobody is there to answer.
			fmt.Fprintf(p.out, "\nhint: no answer (%v); denied\n", err)
			return Deny, nil
		}
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "y", "yes":
			return Allow, nil
		case "n", "no", "":
			return Deny, nil
		case "a", "always":
			if ask.Always == "" {
				fmt.Fprintln(p.out, "hint: \"always\" is not available for this command; answer y or n")
				continue
			}
			return AllowAlways, nil
		default:
			fmt.Fprintln(p.out, "hint: please answer y, n or a")
		}
	}
}
