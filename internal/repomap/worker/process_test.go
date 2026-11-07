package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/repomap/extract"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("HINT_EXTRACT_TEST_HELPER") != "1" {
		return
	}
	switch os.Getenv("HINT_EXTRACT_TEST_MODE") {
	case "sleep":
		time.Sleep(time.Minute)
	case "corrupt":
		fmt.Fprintln(os.Stdout, "not json")
	case "inconsistent":
		fmt.Fprintln(os.Stdout, `{"result":{"complete":false,"declarations":[]}}`)
	case "oversized":
		fmt.Fprintln(os.Stdout, strings.Repeat("x", maxResponseBytes+1))
	case "exit":
		os.Exit(7)
	case "stderr":
		fmt.Fprintln(os.Stderr, strings.Repeat("x", 65536))
		if err := Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(8)
		}
	default:
		if err := Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(8)
		}
	}
	os.Exit(0)
}

func helper(t *testing.T, mode string) *Process {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := &Process{
		Command: []string{executable, "-test.run=^TestHelperProcess$"},
		Environ: func() []string {
			return append(os.Environ(), "HINT_EXTRACT_TEST_HELPER=1", "HINT_EXTRACT_TEST_MODE="+mode)
		},
		Timeout: 5 * time.Second,
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return p
}

func TestPersistentWorkerAndReset(t *testing.T) {
	p := helper(t, "normal")
	source := extract.Source{Path: "x.go", Content: []byte("package p; func Open() {}")}
	var pid int
	for range 2 {
		r, err := p.Extract(context.Background(), source, extract.DefaultLimits())
		if err != nil || !r.Complete || len(r.Declarations) != 1 || r.Declarations[0].Name != "Open" {
			t.Fatalf("Extract = %+v, %v", r, err)
		}
		if pid != 0 && pid != p.cmd.Process.Pid {
			t.Fatal("worker was not reused")
		}
		pid = p.cmd.Process.Pid
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.cmd != nil {
		t.Fatal("closed worker retained process")
	}
	if _, err := p.Extract(context.Background(), source, extract.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if p.cmd.Process.Pid == pid {
		t.Fatal("closed process was reused")
	}
}

func TestWorkerFailuresAreReaped(t *testing.T) {
	for _, mode := range []string{"sleep", "corrupt", "oversized", "inconsistent", "exit"} {
		t.Run(mode, func(t *testing.T) {
			p := helper(t, mode)
			if mode == "sleep" {
				p.Timeout = 50 * time.Millisecond
			}
			_, err := p.Extract(context.Background(), extract.Source{Path: "x.go", Content: []byte("package p")}, extract.DefaultLimits())
			if err == nil {
				t.Fatal("expected worker error")
			}
			if mode == "sleep" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout error = %v", err)
			}
			if p.cmd != nil {
				t.Fatal("failed worker not reset/reaped")
			}
		})
	}
}

func TestCallerCancellationAndQueuedCancellation(t *testing.T) {
	p := helper(t, "sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := p.Extract(ctx, extract.Source{Path: "x.go", Content: []byte("package p")}, extract.DefaultLimits())
	if !errors.Is(err, context.DeadlineExceeded) || p.cmd != nil {
		t.Fatalf("caller cancellation = %v, process=%v", err, p.cmd)
	}
	// Reserve the worker to ensure a queued caller can cancel without waiting
	// for the active request or creating a goroutine around Mutex.Lock.
	p.gate <- struct{}{}
	defer func() { <-p.gate }()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel2()
	if _, err := p.Extract(ctx2, extract.Source{}, extract.DefaultLimits()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued cancellation = %v", err)
	}
}

func TestStderrIsBounded(t *testing.T) {
	p := helper(t, "stderr")
	if _, err := p.Extract(context.Background(), extract.Source{Path: "x.go", Content: []byte("package p")}, extract.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	p.stderr.mu.Lock()
	n := len(p.stderr.data)
	p.stderr.mu.Unlock()
	if n != maxStderrBytes {
		t.Fatalf("stderr retained %d bytes", n)
	}
}

func TestServeRejectsIncompleteAndOversizedFrames(t *testing.T) {
	for _, text := range []string{`{"source":`, strings.Repeat("x", maxRequestBytes+1)} {
		var out bytes.Buffer
		if err := Serve(context.Background(), strings.NewReader(text), &out); err == nil {
			t.Fatal("Serve accepted an incomplete/oversized frame")
		}
	}
}

func TestServeDoesNotReadSourcePath(t *testing.T) {
	input, err := json.Marshal(request{Source: extract.Source{Path: "/path/that/must/not/be/read.go", Content: []byte("package p; func BytesOnly() {}")}, Limits: extract.DefaultLimits()})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Serve(context.Background(), bytes.NewReader(append(input, '\n')), &out); err != nil {
		t.Fatal(err)
	}
	var got response
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Result.Complete || got.Result.Declarations[0].Name != "BytesOnly" {
		t.Fatalf("response = %+v", got)
	}
}
