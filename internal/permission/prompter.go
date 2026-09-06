package permission

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

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

// ReaderPrompter asks on an io.Writer and reads one-line answers from an
// io.Reader — the CLI's stderr and stdin.
//
// Answers: y/yes, n/no, a/always; an empty line is no, so that Enter alone
// is the safe choice. Reaching EOF denies: an unattended run (stdin from
// /dev/null, a pipe that closed) must not approve anything by accident.
//
// The prompter owns its reader for the life of the process: a blocked
// Read cannot be interrupted, so lines are read on one goroutine that
// outlives any single prompt, and a prompt cancelled by ctx leaves that
// goroutine parked on the next line. Nothing else may read the same
// stream — WP0.9's REPL has to take its own input through this reader
// rather than open stdin beside it. A line that arrived while no prompt
// was waiting (typed after a Ctrl-C, say) is discarded before the next
// question is printed, so it cannot be taken as the answer to it.
type ReaderPrompter struct {
	out io.Writer

	once  sync.Once
	in    io.Reader
	lines chan lineResult
	// eof is the reader's terminal error once it has been observed, kept
	// so that a later prompt denies immediately instead of waiting on a
	// goroutine that has exited. Prompts are sequential (the agent loop
	// asks about one call at a time), so no lock guards it.
	eof error
}

type lineResult struct {
	text string
	err  error
}

// NewReaderPrompter returns a prompter over in and out.
func NewReaderPrompter(in io.Reader, out io.Writer) *ReaderPrompter {
	return &ReaderPrompter{in: in, out: out, lines: make(chan lineResult)}
}

// start launches the reader goroutine on first use.
func (p *ReaderPrompter) start() {
	p.once.Do(func() {
		go func() {
			r := bufio.NewReader(p.in)
			for {
				line, err := r.ReadString('\n')
				if line != "" || err == nil {
					p.lines <- lineResult{text: line}
				}
				if err != nil {
					p.lines <- lineResult{err: err}
					return
				}
			}
		}()
	})
}

// discardPending drops lines the reader goroutine has already collected
// while no prompt was waiting. It only sees a line the goroutine is
// currently offering; one still being typed is, by definition, meant for
// the prompt about to be printed.
func (p *ReaderPrompter) discardPending() {
	for p.eof == nil {
		select {
		case line := <-p.lines:
			p.eof = line.err
		default:
			return
		}
	}
}

// Prompt implements Prompter.
func (p *ReaderPrompter) Prompt(ctx context.Context, ask Ask) (Decision, error) {
	p.start()

	p.discardPending()

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
		line := lineResult{err: p.eof}
		if p.eof == nil {
			select {
			case <-ctx.Done():
				fmt.Fprintln(p.out)
				return Deny, ctx.Err()
			case line = <-p.lines:
			}
		}
		if line.err != nil {
			// EOF or a broken pipe: nobody is there to answer.
			p.eof = line.err
			fmt.Fprintf(p.out, "\nhint: no answer (%v); denied\n", line.err)
			return Deny, nil
		}
		switch strings.ToLower(strings.TrimSpace(line.text)) {
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
