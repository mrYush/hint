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
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	r := console.NewLineReader(pr)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if _, err := r.ReadLine(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadLine on a silent pipe = %v, want context.Canceled", err)
	}

	go func() { _, _ = io.WriteString(pw, "later\n") }()
	got, err := r.ReadLine(context.Background())
	if err != nil || got != "later" {
		t.Fatalf("ReadLine after cancel = %q, %v; want %q", got, err, "later")
	}
}

func TestLineReader_DiscardPending(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	r := console.NewLineReader(pr)

	// A line typed while nobody was asking.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = r.ReadLine(ctx) // starts the goroutine, returns at once
	go func() { _, _ = io.WriteString(pw, "stray\n") }()
	time.Sleep(50 * time.Millisecond)

	r.DiscardPending()

	answered := make(chan string, 1)
	go func() {
		got, _ := r.ReadLine(context.Background())
		answered <- got
	}()
	select {
	case got := <-answered:
		t.Fatalf("stray line %q was handed out as an answer", got)
	case <-time.After(50 * time.Millisecond):
	}
	_, _ = io.WriteString(pw, "real\n")
	if got := <-answered; got != "real" {
		t.Fatalf("ReadLine = %q, want the fresh line", got)
	}
}

func TestLineReader_DiscardPendingObservesEOF(t *testing.T) {
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
