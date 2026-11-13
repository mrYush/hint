package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// signalError preserves the exit status of the signal that ended a command.
type signalError struct {
	signal os.Signal
	code   int
}

func (e *signalError) Error() string { return e.signal.String() }

func signalContext(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	received := make(chan os.Signal, 1)
	signal.Notify(received, signals...)
	go func() {
		select {
		case sig := <-received:
			code := 130
			if sig == syscall.SIGTERM {
				code = 143
			}
			cancel(&signalError{signal: sig, code: code})
		case <-ctx.Done():
		}
	}()
	return ctx, func() { signal.Stop(received); cancel(context.Canceled) }
}
