package console_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mrYush/hint/internal/console"
)

func TestLineReader_ReadsLinesAndThenEOF(t *testing.T) {
	t.Parallel()
	r := console.NewLineReader(strings.NewReader("one\r\ntwo\nthree"))
	ctx := context.Background()

	for _, want := range []string{"one", "two", "three"} {
		got, err := r.ReadLine(ctx)
		if err != nil || got != want {
			t.Fatalf("ReadLine = %q, %v; want %q", got, err, want)
		}
	}
	// The terminal error is sticky: every later call answers at once.
	for i := 0; i < 2; i++ {
		if _, err := r.ReadLine(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("call %d after the end = %v, want io.EOF", i, err)
		}
	}
}

func TestLineReader_CancelLeavesReaderUsable(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		pr, pw := io.Pipe()
		defer func() { _ = pw.Close() }()
		r := console.NewLineReader(pr)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		canceled := make(chan error, 1)
		go func() {
			_, err := r.ReadLine(ctx)
			canceled <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-canceled; !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadLine on a silent pipe = %v, want context.Canceled", err)
		}

		go func() {
			_, _ = io.WriteString(pw, "later\n")
			_ = pw.Close()
		}()
		requireLine(t, r, pw, "later")
	})
}

func TestLineReader_DiscardPending(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		pr, pw := io.Pipe()
		defer func() { _ = pw.Close() }()
		r := console.NewLineReader(pr)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := r.ReadLine(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadLine = %v, want context.Canceled", err)
		}
		if _, err := io.WriteString(pw, "stray\n"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait() // The reader is blocked offering the stray line.
		r.DiscardPending()

		go func() {
			_, _ = io.WriteString(pw, "real\n")
			_ = pw.Close()
		}()
		requireLine(t, r, pw, "real")
	})
}

func requireLine(t *testing.T, r *console.LineReader, input *io.PipeWriter, want string) {
	t.Helper()
	// Close input and drain independently of a failed assertion. The reader
	// must deliver its terminal error before the synctest bubble can exit.
	defer func() {
		_ = input.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for {
			_, err := r.ReadLine(ctx)
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				t.Errorf("draining input: %v", err)
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	got, err := r.ReadLine(ctx)
	if err != nil || got != want {
		t.Fatalf("ReadLine = %q, %v; want %q", got, err, want)
	}
	if got, err := r.ReadLine(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadLine after the answer = %q, %v; want EOF", got, err)
	}
}

func TestLineReader_DiscardPendingObservesEOF(t *testing.T) {
	t.Parallel()
	// The bubble waits until the reader goroutine is blocked offering EOF,
	// so the test does not depend on a sleep. Dropping that error deadlocks
	// the bubble: the goroutine has exited and ReadLine would wait forever.
	synctest.Test(t, func(t *testing.T) {
		r := console.NewLineReader(strings.NewReader(""))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancel()
		if _, err := r.ReadLine(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadLine = %v, want context.Canceled", err)
		}
		synctest.Wait()
		r.DiscardPending()
		if _, err := r.ReadLine(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("ReadLine after DiscardPending = %v, want io.EOF", err)
		}
	})
}

// TestLineReader_DiscardPendingLeavesBufferedAnswer checks that input
// already sitting in the stream is the next answer. DiscardPending must
// not start the reader and take it.
func TestLineReader_DiscardPendingLeavesBufferedAnswer(t *testing.T) {
	t.Parallel()
	r := console.NewLineReader(strings.NewReader("2\n"))
	r.DiscardPending()
	got, err := r.ReadLine(context.Background())
	if err != nil || got != "2" {
		t.Fatalf("ReadLine = %q, %v; want %q", got, err, "2")
	}
	if _, err := r.ReadLine(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadLine after the answer = %v, want io.EOF", err)
	}
}
