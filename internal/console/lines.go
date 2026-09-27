// Package console owns the process's interactive input. A [LineReader]
// wraps one stream — stdin, for the CLI — and is shared by everything that
// asks the user a question: the permission prompter (WP0.6), the session
// picker (WP0.7), and the REPL (WP0.9).
//
// There must be exactly one reader per stream. A blocked Read on stdin
// cannot be interrupted, so lines are read on a goroutine that outlives
// any single question; a second bufio.Reader on the same descriptor would
// race it for bytes and swallow the answer to somebody else's question.
package console

import (
	"bufio"
	"context"
	"io"
	"sync"
)

// LineReader reads lines from one stream on behalf of every prompt in the
// process.
//
// The reader goroutine starts lazily on the first call and runs until the
// stream ends. A question cancelled by ctx leaves it parked on the next
// line, so the reader stays usable afterwards. Once the stream has ended,
// [LineReader.ReadLine] keeps returning the terminal error immediately
// rather than waiting on a goroutine that has exited.
//
// Callers are sequential: the agent loop asks about one thing at a time,
// so no lock guards the terminal-error field.
type LineReader struct {
	once  sync.Once
	in    io.Reader
	lines chan line
	// eof is the stream's terminal error once observed.
	eof error
}

type line struct {
	text string
	err  error
}

// NewLineReader returns a LineReader over in.
func NewLineReader(in io.Reader) *LineReader {
	return &LineReader{in: in, lines: make(chan line)}
}

// start launches the reader goroutine on first use.
func (r *LineReader) start() {
	r.once.Do(func() {
		go func() {
			br := bufio.NewReader(r.in)
			for {
				text, err := br.ReadString('\n')
				if text != "" || err == nil {
					r.lines <- line{text: text}
				}
				if err != nil {
					r.lines <- line{err: err}
					return
				}
			}
		}()
	})
}

// DiscardPending drops lines the reader goroutine has already collected
// while nobody was waiting for one — typed after a Ctrl-C, say — so that
// they cannot be taken as the answer to the next question. It only sees a
// line the goroutine is currently offering; one still being typed is, by
// definition, meant for the question about to be asked.
func (r *LineReader) DiscardPending() {
	r.start()
	for r.eof == nil {
		select {
		case l := <-r.lines:
			r.eof = l.err
		default:
			return
		}
	}
}

// ReadLine blocks until the next line arrives and returns it with its
// trailing newline removed. It returns ctx.Err() when ctx ends first, and
// the stream's own terminal error — io.EOF for an exhausted stdin — once
// there is nothing left to read; that error is sticky.
func (r *LineReader) ReadLine(ctx context.Context) (string, error) {
	r.start()
	if r.eof != nil {
		return "", r.eof
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case l := <-r.lines:
		if l.err != nil {
			r.eof = l.err
			return "", l.err
		}
		return trimNewline(l.text), nil
	}
}

// trimNewline removes one trailing "\n" or "\r\n".
func trimNewline(s string) string {
	if n := len(s); n > 0 && s[n-1] == '\n' {
		s = s[:n-1]
		if n := len(s); n > 0 && s[n-1] == '\r' {
			s = s[:n-1]
		}
	}
	return s
}
