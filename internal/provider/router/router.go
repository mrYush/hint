// Package router implements failover between two agentapi.ChatProviders: a
// primary profile and an optional fallback. It is itself a ChatProvider, so
// the caller streams from it exactly as from a single provider and never sees
// the seam.
//
// Routing policy (recorded in docs/plan/phase-0-mvp-cli.md, WP0.3):
//
//   - A failure is eligible for retry or failover only while the primary has
//     produced no user-visible events (text, thinking, tool calls). Once
//     output reached the user, switching providers would splice two
//     unrelated answers; the failure is forwarded instead.
//   - Retryable failures (network, timeout, 429, 5xx) get up to two extra
//     attempts against the same provider, honouring Retry-After with a
//     jittered exponential backoff otherwise.
//   - Failures for which ErrorKind.Fallbackable holds then switch to the
//     fallback profile, with a notice on the configured writer.
//   - Cancellation is never retried and never fails over: agentapi.KindOf
//     classifies it before any transport wrapping can misreport it.
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

const (
	// extraAttempts is how many times one provider is re-tried after its
	// first failure, before the router considers falling back.
	extraAttempts = 2
	// backoffBase is the first retry delay when the provider sent no
	// Retry-After hint; subsequent delays double.
	backoffBase = time.Second
	// maxRetryDelay caps a delay whatever its source. A provider demanding a
	// longer pause is not worth waiting for interactively — the router moves
	// on instead of hanging the run.
	maxRetryDelay = 30 * time.Second
)

// ReadyChecker is implemented by providers that can cheaply verify their
// endpoint is alive before a stream is attempted. The interface is declared
// here, on the consumer side, per the usual Go convention: the router is the
// only code that needs it, and any provider satisfying the shape opts in
// implicitly.
type ReadyChecker interface {
	Ready(ctx context.Context) error
}

// Router is an agentapi.ChatProvider with retry and failover.
type Router struct {
	primary  agentapi.ChatProvider
	fallback agentapi.ChatProvider
	notice   io.Writer
	// sleep is swapped out by tests; the default honours ctx.
	sleep func(ctx context.Context, d time.Duration) error
}

// Option configures a Router.
type Option func(*Router)

// WithFallback sets the profile to switch to when the primary fails before
// producing output. Nil leaves the router in pass-through mode.
func WithFallback(p agentapi.ChatProvider) Option {
	return func(r *Router) { r.fallback = p }
}

// WithNotice sets where the failover notice is written. The router never
// assumes a terminal — the CLI passes os.Stderr, tests a buffer. Nil
// discards notices.
func WithNotice(w io.Writer) Option {
	return func(r *Router) { r.notice = w }
}

// withSleep replaces the backoff sleeper in tests.
func withSleep(f func(ctx context.Context, d time.Duration) error) Option {
	return func(r *Router) { r.sleep = f }
}

// New returns a Router over the primary provider.
func New(primary agentapi.ChatProvider, opts ...Option) *Router {
	r := &Router{
		primary: primary,
		sleep:   ctxSleep,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Name implements agentapi.ChatProvider. It is deliberately the primary's
// static name: a Router is shared between concurrent turns, so a "currently
// active profile" would be a data race between them. Which profile actually
// answered a failure is carried by Error.Provider, and a switch is announced
// by the failover notice.
func (r *Router) Name() string { return r.primary.Name() }

// Stream implements agentapi.ChatProvider. It always returns a channel and a
// nil error: connecting happens asynchronously, and every failure — including
// a failure to start — arrives on the channel as the terminal ChatError after
// the retry and failover policy is exhausted.
func (r *Router) Stream(ctx context.Context, req agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
	out := make(chan agentapi.ChatEvent)
	go func() {
		defer close(out)
		r.run(ctx, req, out)
	}()
	return out, nil
}

// run drives one turn: primary with retries, then fallback with retries.
func (r *Router) run(ctx context.Context, req agentapi.ChatRequest, out chan<- agentapi.ChatEvent) {
	err := r.attemptWithRetries(ctx, r.primary, req, out)
	if err == nil {
		return
	}
	if r.fallback == nil || !err.Fallbackable() {
		r.deliver(ctx, out, agentapi.ChatEvent{Kind: agentapi.ChatError, Err: err})
		return
	}

	_, _ = fmt.Fprintf(r.noticeWriter(), "hint: provider %q failed (%s), falling back to %q\n",
		r.primary.Name(), err.Kind, r.fallback.Name())

	// A provider that can preflight (native Ollama) does so here: failing
	// fast with one clear message beats a second opaque transport error
	// from a daemon that is not running.
	if rc, ok := r.fallback.(ReadyChecker); ok {
		if readyErr := rc.Ready(ctx); readyErr != nil {
			r.deliver(ctx, out, agentapi.ChatEvent{Kind: agentapi.ChatError, Err: asError(readyErr, r.fallback.Name())})
			return
		}
	}

	if err := r.attemptWithRetries(ctx, r.fallback, req, out); err != nil {
		r.deliver(ctx, out, agentapi.ChatEvent{Kind: agentapi.ChatError, Err: err})
	}
}

// attemptWithRetries streams from one provider, retrying failures that
// happened before any user-visible output. It returns nil when the turn is
// settled from the router's point of view (success, cancel, or a failure that
// was already forwarded because output had been delivered), and the terminal
// error when the caller may fail over.
func (r *Router) attemptWithRetries(ctx context.Context, p agentapi.ChatProvider, req agentapi.ChatRequest, out chan<- agentapi.ChatEvent) *agentapi.Error {
	for attempt := 0; ; attempt++ {
		errEvent, settled := r.attempt(ctx, p, req, out)
		if settled {
			return nil
		}
		if errEvent.Kind == agentapi.ErrCanceled || ctx.Err() != nil {
			// The user's cancel is not a provider failure: forward and stop.
			// The nil return is deliberate (nolint nilerr): the terminal
			// event was delivered, so the turn is settled.
			r.deliver(ctx, out, agentapi.ChatEvent{Kind: agentapi.ChatError, Err: errEvent})
			return nil //nolint:nilerr
		}
		if attempt >= extraAttempts || !errEvent.Retryable() {
			return errEvent
		}
		if err := r.sleep(ctx, retryDelay(attempt, errEvent)); err != nil {
			return errEvent
		}
	}
}

// attempt runs one stream from p, forwarding events to out. settled=true
// means the turn is over from the router's point of view — the stream
// succeeded, or its failure was already forwarded. settled=false hands the
// terminal error to the retry policy.
func (r *Router) attempt(ctx context.Context, p agentapi.ChatProvider, req agentapi.ChatRequest, out chan<- agentapi.ChatEvent) (*agentapi.Error, bool) {
	ch, err := p.Stream(ctx, req)
	if err != nil {
		return asError(err, p.Name()), false
	}

	delivered := false
	// Usage is withheld until the terminal event: if this attempt failed
	// after a usage chunk and is retried, the merged stream must not carry
	// two usage events (the contract allows at most one).
	var usage *agentapi.ChatEvent

	for ev := range ch {
		switch ev.Kind {
		case agentapi.ChatError:
			if !delivered {
				// Nothing user-visible is lost; the retry/failover policy
				// decides what happens next.
				drain(ch)
				return asError(ev.Err, p.Name()), false
			}
			// Mid-answer failure: switching providers now would splice two
			// answers, so the error is the user's to see. WP0.4 may retry
			// the whole turn.
			r.deliver(ctx, out, ev)
			drain(ch)
			return nil, true
		case agentapi.ChatUsage:
			usage = &ev
		case agentapi.ChatDone:
			if usage != nil {
				r.deliver(ctx, out, *usage)
			}
			r.deliver(ctx, out, ev)
			drain(ch)
			return nil, true
		case agentapi.ChatTextDelta, agentapi.ChatThinkingDelta, agentapi.ChatToolCall:
			// User-visible output: past this point the stream is committed
			// to this provider.
			delivered = true
			r.deliver(ctx, out, ev)
		default:
			// A ChatEvent kind added after this router was written. Assume
			// it is user-visible — retrying after forwarding it could
			// splice two answers, the failure mode this router exists to
			// prevent.
			delivered = true
			r.deliver(ctx, out, ev)
		}
	}
	// The provider closed its channel without a terminal event, which the
	// contract forbids. Treat it as that provider's internal failure.
	e := agentapi.NewError(agentapi.ErrUnknown, "provider closed the stream without a terminal event").
		WithProvider(p.Name())
	if delivered {
		r.deliver(ctx, out, agentapi.ChatEvent{Kind: agentapi.ChatError, Err: e})
		return nil, true
	}
	return e, false
}

// deliver forwards one event, giving up when the consumer cancelled.
func (r *Router) deliver(ctx context.Context, out chan<- agentapi.ChatEvent, ev agentapi.ChatEvent) {
	select {
	case out <- ev:
	case <-ctx.Done():
		if ev.Terminal() {
			// Best effort so a still-listening consumer gets the terminal.
			select {
			case out <- ev:
			default:
			}
		}
	}
}

func (r *Router) noticeWriter() io.Writer {
	if r.notice == nil {
		return io.Discard
	}
	return r.notice
}

// drain consumes the rest of an abandoned provider stream so its goroutine
// can finish and close the response body.
func drain(ch <-chan agentapi.ChatEvent) {
	for range ch {
	}
}

// retryDelay computes the pause before the next attempt: the provider's own
// Retry-After hint when present, otherwise an exponential backoff with a
// downward jitter (so a fleet of clients does not retry in lockstep), both
// capped at maxRetryDelay.
func retryDelay(attempt int, err *agentapi.Error) time.Duration {
	var d time.Duration
	if err != nil && err.RetryAfterSeconds > 0 {
		d = time.Duration(err.RetryAfterSeconds) * time.Second
	} else {
		d = backoffBase << attempt
		d -= time.Duration(rand.Int63n(int64(d / 4))) // up to -25% jitter
	}
	if d > maxRetryDelay {
		d = maxRetryDelay
	}
	return d
}

// ctxSleep waits for d or until ctx is done, whichever comes first.
func ctxSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// asError coerces any error into an attributed *agentapi.Error.
func asError(err error, provider string) *agentapi.Error {
	var e *agentapi.Error
	if !errors.As(err, &e) {
		e = agentapi.WrapError(agentapi.KindOf(err), err, "provider failure")
	}
	if e.Provider == "" {
		e = e.WithProvider(provider)
	}
	return e
}
