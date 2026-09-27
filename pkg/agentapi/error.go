package agentapi

import (
	"context"
	"errors"
	"fmt"
)

// ErrUnknownKind is returned by the Validate methods when a value carries a
// Kind this build does not define. It is a sentinel so that a decoder of a
// newer session file or RPC message can tell "written by a newer version,
// skip it" from "malformed, fail" with errors.Is — the forward-compatibility
// rule stated in this package's doc comment.
//
// It is unrelated to [ErrUnknown], which classifies a failure whose cause
// could not be determined.
var ErrUnknownKind = errors.New("agentapi: unknown kind")

// ErrorKind classifies a failure by what the caller should do about it, not
// by where it came from. The router (WP0.3) switches on it to decide between
// retrying, falling back to another provider, and giving up; the agent loop
// switches on it to decide whether to compact and retry.
type ErrorKind string

const (
	// ErrUnknown is a failure that could not be classified. Treated as fatal.
	ErrUnknown ErrorKind = "unknown"
	// ErrNetwork is a transport failure: DNS, connection refused, TLS, a
	// broken stream. The provider may be unreachable — fall back.
	ErrNetwork ErrorKind = "network"
	// ErrTimeout is a deadline exceeded while waiting on the provider.
	ErrTimeout ErrorKind = "timeout"
	// ErrRateLimited is a 429 or an equivalent quota rejection. Retry after
	// RetryAfterSeconds, then fall back.
	ErrRateLimited ErrorKind = "rate_limited"
	// ErrUnavailable is a 5xx or a provider-side outage. Fall back.
	ErrUnavailable ErrorKind = "unavailable"
	// ErrAuth is a rejected or missing credential (401, 403). Retrying will
	// not help and the fallback profile has its own credentials, so the
	// router may still try it, but the user has to be told.
	ErrAuth ErrorKind = "auth"
	// ErrInvalidRequest is a request the provider refused to parse or accept
	// (400). Our bug or a dialect mismatch — never retried.
	ErrInvalidRequest ErrorKind = "invalid_request"
	// ErrModelNotFound is a model the provider does not serve. The fallback
	// profile names a different model, so falling back can succeed.
	ErrModelNotFound ErrorKind = "model_not_found"
	// ErrContextOverflow is a request longer than the model's window. The
	// agent loop reacts by compacting and retrying rather than by switching
	// providers.
	ErrContextOverflow ErrorKind = "context_overflow"
	// ErrContentFiltered is a response withheld by a safety filter. Not
	// retried; surfaced to the user.
	ErrContentFiltered ErrorKind = "content_filtered"
	// ErrCanceled is the caller's own context being cancelled. Never retried
	// and never reported as a provider failure.
	ErrCanceled ErrorKind = "canceled"
	// ErrTurnLimit is the agent loop (WP0.4) hitting its iteration cap
	// without the model reaching a final answer. Not retryable — repeating
	// the same request would hit the same cap — and not a provider failure,
	// so falling back to another provider would not help either.
	ErrTurnLimit ErrorKind = "turn_limit"
)

// Retryable reports whether repeating the same request against the same
// provider could plausibly succeed.
//
// Every kind is listed explicitly rather than folded into the default. Go has
// no compile-time exhaustiveness check for a string enum, so the guarantee is
// two-part: the exhaustive linter (enabled in .golangci.yml) flags a missing
// case, and TestErrorKindRoutingIsExhaustive fails if a kind has no row in
// the routing table. Without both, a new kind would silently default to
// "give up", which for a fallback-worthy failure means offline mode quietly
// stops working.
func (k ErrorKind) Retryable() bool {
	switch k {
	case ErrNetwork, ErrTimeout, ErrRateLimited, ErrUnavailable:
		return true
	case ErrAuth, ErrInvalidRequest, ErrModelNotFound, ErrContextOverflow,
		ErrContentFiltered, ErrCanceled, ErrTurnLimit, ErrUnknown:
		return false
	default:
		return false
	}
}

// Fallbackable reports whether the router should try the fallback provider.
// It is deliberately wider than [ErrorKind.Retryable]: a bad credential or an
// unknown model is fatal for this profile but not for the next one, which has
// its own credentials and names its own model.
func (k ErrorKind) Fallbackable() bool {
	switch k {
	case ErrNetwork, ErrTimeout, ErrRateLimited, ErrUnavailable, ErrAuth, ErrModelNotFound:
		return true
	case ErrInvalidRequest, ErrContextOverflow, ErrContentFiltered, ErrCanceled,
		ErrTurnLimit, ErrUnknown:
		return false
	default:
		return false
	}
}

// Valid reports whether k is one of the defined kinds.
func (k ErrorKind) Valid() bool {
	switch k {
	case ErrUnknown, ErrNetwork, ErrTimeout, ErrRateLimited, ErrUnavailable,
		ErrAuth, ErrInvalidRequest, ErrModelNotFound, ErrContextOverflow,
		ErrContentFiltered, ErrCanceled, ErrTurnLimit:
		return true
	default:
		return false
	}
}

// Error is a classified failure. It implements the error interface and is
// also a wire type: it travels inside [ChatEvent] and [Event], so a remote
// client can react to a failure the same way an in-process caller does.
//
// The wrapped cause is not serialised — it survives errors.Is and errors.As
// in-process and is flattened into Message on the wire.
type Error struct {
	// Kind classifies the failure. Always set.
	Kind ErrorKind `json:"kind"`
	// Message is the human-readable description.
	Message string `json:"message"`
	// Provider is the profile name the failure came from, if any.
	Provider string `json:"provider,omitempty"`
	// StatusCode is the HTTP status, when the failure came from an HTTP call.
	StatusCode int `json:"status_code,omitempty"`
	// RetryAfterSeconds is the provider's own back-off hint, when it sent one.
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`

	// cause is the underlying error, kept for errors.Is/errors.As. Unexported
	// so that it is neither serialised nor part of the public shape.
	cause error
}

// NewError returns an *Error of the given kind.
func NewError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// WrapError returns an *Error of the given kind wrapping cause. The cause's
// text is appended to the message so that the wire form stays informative.
func WrapError(kind ErrorKind, cause error, format string, args ...any) *Error {
	msg := fmt.Sprintf(format, args...)
	if cause != nil {
		msg = msg + ": " + cause.Error()
	}
	return &Error{Kind: kind, Message: msg, cause: cause}
}

// WithProvider returns a copy of e attributed to the named provider profile.
func (e *Error) WithProvider(name string) *Error {
	if e == nil {
		return nil
	}
	c := *e
	c.Provider = name
	return &c
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Provider != "" {
		return fmt.Sprintf("%s: %s: %s", e.Provider, e.Kind, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

// Unwrap returns the wrapped cause, so errors.Is and errors.As reach it.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Is reports whether target is an *Error of the same kind. It lets callers
// write errors.Is(err, agentapi.NewError(agentapi.ErrTimeout, "")) and, more
// usefully, makes [KindOf] robust against wrapping.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return e != nil && t != nil && e.Kind == t.Kind
}

// Retryable reports whether the failure is worth retrying against the same
// provider. A nil *Error is not retryable.
func (e *Error) Retryable() bool {
	return e != nil && e.Kind.Retryable()
}

// Fallbackable reports whether the router should try the fallback provider.
func (e *Error) Fallbackable() bool {
	return e != nil && e.Kind.Fallbackable()
}

// KindOf returns the [ErrorKind] of err, unwrapping as needed. It returns
// ErrCanceled for a cancelled or expired context and ErrUnknown for anything
// this package cannot classify, so callers can switch on the result without a
// nil check.
func KindOf(err error) ErrorKind {
	if err == nil {
		return ""
	}
	// Cancellation is checked before any provider's own classification. An
	// HTTP client reports a cancelled request as a transport failure, and a
	// provider wrapping that as ErrNetwork would otherwise make the router
	// treat the user's Ctrl-C as a reason to retry against the fallback
	// profile with the same dead context.
	switch {
	case errors.Is(err, context.Canceled):
		return ErrCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTimeout
	}
	var e *Error
	if errors.As(err, &e) && e != nil {
		return e.Kind
	}
	return ErrUnknown
}
