package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/console"
)

// ErrNoChoice reports that the user picked nothing: an empty answer, EOF,
// or a request to quit.
var ErrNoChoice = errors.New("session: no session chosen")

// maxListed bounds how many sessions the picker shows; older ones stay
// reachable with `--session <id>`, and `hint sessions` lists them all.
const maxListed = 20

// Choose prints infos on out, most recent first, and reads the user's
// pick from lines: a number from the list, or a prefix of a session id.
// An empty answer or EOF is [ErrNoChoice]; ctx ending is ctx.Err().
//
// It reads through the shared [console.LineReader] rather than opening
// stdin itself, because the permission prompter will read the same stream
// later in the run and two readers on one descriptor would race.
func Choose(ctx context.Context, out io.Writer, lines *console.LineReader, infos []Info) (Info, error) {
	if len(infos) == 0 {
		return Info{}, ErrNoSessions
	}
	shown := infos
	if len(shown) > maxListed {
		shown = shown[:maxListed]
	}
	fmt.Fprintf(out, "hint: sessions in %s\n", infos[0].Cwd)
	Print(out, shown)
	if len(infos) > len(shown) {
		fmt.Fprintf(out, "     (%d older sessions not listed)\n", len(infos)-len(shown))
	}

	lines.DiscardPending()
	for {
		fmt.Fprintf(out, "Continue which session? [1-%d, id prefix, or Enter to cancel]: ", len(shown))
		answer, err := lines.ReadLine(ctx)
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(out)
				return Info{}, ctx.Err()
			}
			fmt.Fprintf(out, "\nhint: no answer (%v)\n", err)
			return Info{}, ErrNoChoice
		}
		answer = strings.TrimSpace(answer)
		if answer == "" || answer == "q" {
			return Info{}, ErrNoChoice
		}
		if n, err := strconv.Atoi(answer); err == nil {
			if n >= 1 && n <= len(shown) {
				return shown[n-1], nil
			}
			fmt.Fprintf(out, "hint: enter a number between 1 and %d\n", len(shown))
			continue
		}
		var matches []Info
		for _, info := range infos {
			if strings.HasPrefix(info.ID, answer) {
				matches = append(matches, info)
			}
		}
		switch len(matches) {
		case 1:
			return matches[0], nil
		case 0:
			fmt.Fprintf(out, "hint: no session id starts with %q\n", answer)
		default:
			fmt.Fprintf(out, "hint: %d session ids start with %q; type more of it\n", len(matches), answer)
		}
	}
}

// firstLineOf returns the first line of s cut at max bytes, with an
// ellipsis when anything was left out; an empty s reads "(no prompt)".
func firstLineOf(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no prompt)"
	}
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

// Age renders how long ago t was, for a one-line notice ("2h ago").
func Age(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// Print lists infos on out, one per line and numbered from 1: when the
// conversation last grew, the id, how many messages it holds and the
// first prompt. The picker prints its choices this way, and `hint
// sessions` prints the whole directory.
func Print(out io.Writer, infos []Info) {
	for i, info := range infos {
		fmt.Fprintf(out, "%3d. %s  %-16s  %3d msgs  %s\n",
			i+1, info.Modified.Local().Format("2006-01-02 15:04"), info.ID, info.Messages, firstLineOf(info.FirstPrompt, 60))
	}
}
