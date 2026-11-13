// Package debuglog writes the --debug trace: the requests and responses
// of a run, the tool calls it made and the failures it met, to one file
// per run under the state directory. Traces are pruned when older than
// seven days and unmodified for seven days.
// They contain full prompts, responses, tool calls, and project-file content.
// Every line passes through
// [config.Redact] on its way to the disk, so a configured API key cannot
// reach the file even when a caller forgets to mask it — the client's own
// masking of its key is the first layer, this writer the last.
//
// The trace is a plain timestamped text file rather than structured
// logging: it is read by a person chasing a bad answer, not parsed by a
// machine, and a request body is easier to read unescaped. Structured
// logs can come with the core-as-a-service, where a log line has more
// than one consumer.
package debuglog

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/config"
)

// DefaultDir returns where traces go for the given home directory:
// $XDG_STATE_HOME/hint/log when the variable is set, otherwise
// ~/.local/state/hint/log. lookupEnv is injected, as in internal/config
// and internal/session, so tests never touch the process environment.
func DefaultDir(home string, lookupEnv func(string) (string, bool)) string {
	if lookupEnv != nil {
		if xdg, ok := lookupEnv("XDG_STATE_HOME"); ok && xdg != "" {
			return filepath.Join(xdg, "hint", "log")
		}
	}
	return filepath.Join(home, ".local", "state", "hint", "log")
}

// Log is one run's trace file.
type Log struct {
	path string
	f    *os.File
	out  *log.Logger
}

// Open creates the trace file for a run started at now, named by the time
// and the process id so two runs never share one, and returns the Log
// writing into it. secrets are the values [config.Redact] scrubs from
// every line — [config.Config.Secrets] in the CLI. The directory is
// created with owner-only permissions and the file likewise: a trace
// holds the user's prompts and their project's files.
func Open(dir string, now time.Time, secrets []string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("debuglog: %w", err)
	}
	pruneErr := prune(dir, now)
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.log", now.Format("20060102-150405"), os.Getpid()))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("debuglog: %w", err)
	}
	l := &Log{
		path: path,
		f:    f,
		out:  log.New(redactor{w: f, secrets: secrets}, "", log.LstdFlags|log.Lmicroseconds),
	}
	if pruneErr != nil {
		l.Printf("warning: pruning old debug logs: %v", pruneErr)
	}
	return l, nil
}

// Path returns the trace file, for the notice that tells the user where
// to look.
func (l *Log) Path() string { return l.path }

// Printf writes one timestamped entry. A multi-line argument (a request
// body) stays multi-line; the next entry's timestamp marks where it ends.
func (l *Log) Printf(format string, args ...any) {
	l.out.Printf(format, args...)
}

// Close flushes and closes the file.
func (l *Log) Close() error {
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("debuglog: close %s: %w", l.path, err)
	}
	return nil
}

// redactor is an io.Writer that scrubs secrets from every write before
// forwarding it. log.Logger issues one Write per entry, so a secret can
// never straddle two writes and slip through in halves.
type redactor struct {
	w       io.Writer
	secrets []string
}

// Write implements io.Writer. It reports len(p) on success, as the
// contract requires, even though the scrubbed text may be shorter.
func (r redactor) Write(p []byte) (int, error) {
	if _, err := io.WriteString(r.w, config.Redact(string(p), r.secrets)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// retentionDays limits exposure of prompt, response and project-file content
// written to debug traces. Only Hint's timestamped .log files are pruned.
const retentionDays = 7

func prune(dir string, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var failures []error
	cutoff := now.AddDate(0, 0, -retentionDays)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		name := entry.Name()
		if len(name) < len("20060102-150405-1.log") {
			continue
		}
		stamp, err := time.ParseInLocation("20060102-150405", name[:15], now.Location())
		if err != nil || name[15] != '-' {
			continue
		}
		pid := strings.TrimSuffix(name[16:], ".log")
		if pid == "" {
			continue
		}
		valid := true
		for _, c := range pid {
			if c < '0' || c > '9' {
				valid = false
				break
			}
		}
		if !valid || !stamp.Before(cutoff) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		// A long-running process may still be writing an old trace.
		if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
