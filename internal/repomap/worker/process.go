// Package worker supervises a persistent declaration-extraction subprocess.
package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/mrYush/hint/internal/repomap/extract"
)

const (
	maxRequestBytes  = 12 << 20
	maxResponseBytes = (4 << 20) + 1024
	maxStderrBytes   = 4096
)

type request struct {
	Source extract.Source `json:"source"`
	Limits extract.Limits `json:"limits"`
}

type response struct {
	Result extract.Result `json:"result"`
	Error  string         `json:"error,omitempty"`
}

// Process lazily starts one helper and serializes requests to its bounded pipes.
// Command and Environ are trusted application configuration, never source content.
// Configure the fields before first use; the zero value has no executable.
type Process struct {
	Command []string
	Environ func() []string
	Timeout time.Duration

	once   sync.Once
	gate   chan struct{}
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reader *bufio.Reader
	stderr *boundedStderr
}

// Extract sends already-authorized bytes to the helper and returns its result.
// Timeout includes pipe I/O and extraction; on failure it kills and reaps the
// process before permitting another request or releasing the serialized slot.
func (p *Process) Extract(ctx context.Context, source extract.Source, limits extract.Limits) (extract.Result, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p.once.Do(func() { p.gate = make(chan struct{}, 1) })
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-jobCtx.Done():
		return extract.Result{}, fmt.Errorf("wait for extract worker: %w", jobCtx.Err())
	}
	if err := jobCtx.Err(); err != nil {
		return extract.Result{}, fmt.Errorf("extract worker: %w", err)
	}
	if len(source.Content) > 8<<20 || len(source.Path) > 4096 {
		return extract.Result{}, errors.New("extract worker: request exceeds input limit")
	}
	data, err := json.Marshal(request{Source: source, Limits: limits})
	if err != nil {
		return extract.Result{}, fmt.Errorf("encode extraction request: %w", err)
	}
	if len(data)+1 > maxRequestBytes {
		return extract.Result{}, errors.New("extract worker: encoded request exceeds limit")
	}
	if err := jobCtx.Err(); err != nil {
		return extract.Result{}, fmt.Errorf("extract worker: %w", err)
	}
	if err := p.start(); err != nil {
		return extract.Result{}, err
	}
	type outcome struct {
		value response
		err   error
	}
	done := make(chan outcome, 1)
	stdin, reader := p.stdin, p.reader
	go func() {
		if _, err := stdin.Write(append(data, '\n')); err != nil {
			done <- outcome{err: fmt.Errorf("write extraction request: %w", err)}
			return
		}
		line, err := readLine(reader, maxResponseBytes)
		if err != nil {
			done <- outcome{err: fmt.Errorf("read extraction response: %w", err)}
			return
		}
		var value response
		if err := json.Unmarshal(line, &value); err != nil {
			done <- outcome{err: fmt.Errorf("decode extraction response: %w", err)}
			return
		}
		done <- outcome{value: value}
	}()
	var result outcome
	select {
	case result = <-done:
	case <-jobCtx.Done():
		closeErr := p.stop()
		// Closing both parent pipe endpoints unblocks I/O even if a descendant
		// inherited a descriptor; always join the request goroutine before return.
		<-done
		return extract.Result{}, errors.Join(fmt.Errorf("extract worker: %w", jobCtx.Err()), closeErr)
	}
	if err := jobCtx.Err(); err != nil {
		return extract.Result{}, errors.Join(fmt.Errorf("extract worker: %w", err), p.stop())
	}
	if result.err != nil {
		return extract.Result{}, errors.Join(result.err, p.stop())
	}
	if result.value.Error != "" {
		return extract.Result{}, fmt.Errorf("extract worker: %s", result.value.Error)
	}
	if err := validateResult(result.value.Result, source, limits); err != nil {
		return extract.Result{}, errors.Join(err, p.stop())
	}
	return result.value.Result, nil
}

func (p *Process) start() error {
	if p.cmd != nil {
		return nil
	}
	if len(p.Command) == 0 || p.Command[0] == "" {
		return errors.New("extract worker: missing command")
	}
	cmd := exec.Command(p.Command[0], p.Command[1:]...)
	if p.Environ != nil {
		cmd.Env = p.Environ()
	} else {
		cmd.Env = os.Environ()
	}
	// Keep discarded grammar runtimes from accumulating until the default GC
	// trigger. These are isolated-helper collection settings, not an RSS ceiling.
	cmd.Env = append(cmd.Env, "GOMEMLIMIT=64MiB", "GOGC=50")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create extraction input pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Join(fmt.Errorf("create extraction output pipe: %w", err), stdin.Close())
	}
	stderr := &boundedStderr{}
	cmd.Stderr = stderr
	// WaitDelay also bounds os/exec's stderr copier if a descendant inherits it.
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Start(); err != nil {
		return errors.Join(fmt.Errorf("start extraction helper: %w", err), stdin.Close(), stdout.Close())
	}
	p.cmd, p.stdin, p.stdout, p.stderr = cmd, stdin, stdout, stderr
	p.reader = bufio.NewReaderSize(stdout, 32<<10)
	return nil
}

// Close terminates and reaps the helper, releasing its cached runtime.
// A later Extract starts a fresh helper, so resource-pressure suspension is reversible.
func (p *Process) Close() error {
	p.once.Do(func() { p.gate = make(chan struct{}, 1) })
	p.gate <- struct{}{}
	defer func() { <-p.gate }()
	return p.stop()
}

func (p *Process) stop() error {
	if p.cmd == nil {
		return nil
	}
	cmd, stdin, stdout := p.cmd, p.stdin, p.stdout
	p.cmd, p.stdin, p.stdout, p.reader, p.stderr = nil, nil, nil, nil, nil
	// Pipe endpoints are closed even when the process has already exited.
	inErr := stdin.Close()
	outErr := stdout.Close()
	killErr := cmd.Process.Kill()
	waitErr := cmd.Wait()
	if errors.Is(inErr, os.ErrClosed) {
		inErr = nil
	}
	if errors.Is(outErr, os.ErrClosed) {
		outErr = nil
	}
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	// A killed/already-exited helper is an expected lifecycle event.
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) || errors.Is(waitErr, exec.ErrWaitDelay) {
		waitErr = nil
	}
	if err := errors.Join(inErr, outErr, killErr, waitErr); err != nil {
		return fmt.Errorf("stop extraction helper: %w", err)
	}
	return nil
}

type boundedStderr struct {
	mu   sync.Mutex
	data []byte
}

func (s *boundedStderr) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(data)
	if remaining := maxStderrBytes - len(s.data); remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		s.data = append(s.data, data...)
	}
	return n, nil
}

func readLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var data []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > limit-len(data) {
			return nil, errors.New("protocol frame exceeds limit")
		}
		data = append(data, part...)
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			if errors.Is(err, io.EOF) && len(data) != 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}
}

func validateResult(r extract.Result, source extract.Source, limits extract.Limits) error {
	if (r.Complete && r.Omission != "") || (!r.Complete && (r.Omission == "" || len(r.Declarations) != 0)) ||
		r.WorkUnits < 0 || r.WorkUnits > limits.MaxWorkUnits || len(r.Declarations) > limits.MaxDeclarations {
		return errors.New("extract worker: inconsistent result")
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > limits.MaxOutputBytes {
		return errors.New("extract worker: result exceeds output limit")
	}
	for _, d := range r.Declarations {
		if d.Name == "" || d.Kind == "" || d.Signature == "" || d.StartByte < 0 || d.EndByte > len(source.Content) ||
			d.StartByte >= d.EndByte || d.StartLine < 1 || d.EndLine < d.StartLine {
			return errors.New("extract worker: invalid declaration")
		}
	}
	return nil
}
