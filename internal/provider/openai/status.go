package openai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// asAgentError reports whether err wraps an *agentapi.Error, filling target.
func asAgentError(err error, target **agentapi.Error) bool {
	return errors.As(err, target)
}

// maxErrorBody caps how much of a failed response is read for the error
// message. A misbehaving gateway can answer with an HTML page or a log dump;
// the classification needs a sentence, not the whole document.
const maxErrorBody = 64 * 1024

// wireError is the error envelope of the OpenAI dialect: either
// {"error": {"message": ..., "type": ..., "code": ...}} on a failed request,
// or the same object inline in a stream chunk from some gateways.
type wireError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	// Code is a json.Number-ish free-form field: OpenAI sends a string,
	// some gateways an integer. RawMessage swallows both.
	Code json.RawMessage `json:"code"`
}

type wireErrorEnvelope struct {
	Error *wireError `json:"error"`
}

// statusError maps a non-2xx HTTP response onto *agentapi.Error. It consumes
// (part of) the body for the provider's own message.
func statusError(resp *http.Response) *agentapi.Error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	msg := errorMessage(body)

	kind := kindForStatus(resp.StatusCode, msg)
	e := agentapi.NewError(kind, "%s", messageOrStatus(msg, resp))
	e.StatusCode = resp.StatusCode
	if kind == agentapi.ErrRateLimited {
		e.RetryAfterSeconds = retryAfterSeconds(resp.Header.Get("Retry-After"))
	}
	return e
}

func kindForStatus(status int, msg string) agentapi.ErrorKind {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return agentapi.ErrAuth
	case status == http.StatusNotFound:
		// Both "no such model" and "no such route" land here; either way the
		// fallback profile names its own model and URL.
		return agentapi.ErrModelNotFound
	case status == http.StatusTooManyRequests:
		return agentapi.ErrRateLimited
	case status >= 500:
		return agentapi.ErrUnavailable
	case status == http.StatusBadRequest && isContextOverflow(msg):
		return agentapi.ErrContextOverflow
	case status == http.StatusBadRequest:
		return agentapi.ErrInvalidRequest
	default:
		return agentapi.ErrUnknown
	}
}

// isContextOverflow recognises the request-too-long refusal inside a 400
// body. There is no structured code for it across dialects, so this is a
// substring match over the phrasings the supported providers actually use.
func isContextOverflow(msg string) bool {
	m := strings.ToLower(msg)
	for _, needle := range []string{
		"context length",
		"context_length_exceeded",
		"maximum context",
		"context window",
		"too many tokens",
		"input is too long",
	} {
		if strings.Contains(m, needle) {
			return true
		}
	}
	return false
}

// errorMessage extracts the provider's message from an error body, falling
// back to the raw body when it is not the JSON envelope.
func errorMessage(body []byte) string {
	var env wireErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error != nil {
		return env.Error.describe()
	}
	// Gateways in front of the API answer in their own shapes; the common
	// ones are {"error":"Unauthorized"}, a string where OpenAI sends an
	// object, and {"detail":"Access denied"} (api-bar, FastAPI), which
	// would otherwise reach the user as raw JSON. NestJS sends both,
	// {"message":"Invalid API key","error":"Unauthorized"}, where the
	// useful part is the message and the error is the status text; so the
	// longer fields win and the error is kept as a suffix when it differs.
	var flat struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(body, &flat); err == nil {
		errText := strings.TrimSpace(flat.Error)
		for _, msg := range []string{flat.Message, flat.Detail} {
			if msg = strings.TrimSpace(msg); msg == "" {
				continue
			}
			if errText != "" && !strings.EqualFold(errText, msg) {
				return msg + " (" + errText + ")"
			}
			return msg
		}
		if errText != "" {
			return errText
		}
	}
	return strings.TrimSpace(string(body))
}

func (e *wireError) describe() string {
	msg := strings.TrimSpace(e.Message)
	code := strings.Trim(strings.TrimSpace(string(e.Code)), `"`)
	switch {
	case msg != "" && code != "" && code != "null":
		return msg + " (" + code + ")"
	case msg != "":
		return msg
	case code != "" && code != "null":
		return "error code " + code
	default:
		return ""
	}
}

func messageOrStatus(msg string, resp *http.Response) string {
	if msg != "" {
		return msg
	}
	return "HTTP " + resp.Status
}

// retryAfterSeconds parses a Retry-After header, which the standard allows
// to be either a delay in seconds or an HTTP date. Zero means "no usable
// hint".
func retryAfterSeconds(header string) int {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0
		}
		return secs
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := time.Until(t); d > 0 {
			return int(d.Round(time.Second) / time.Second)
		}
	}
	return 0
}

// transportError maps a failed round trip (no HTTP response at all) onto
// *agentapi.Error. Cancellation and deadline win over the transport's own
// wrapping — see agentapi.KindOf.
func transportError(err error) *agentapi.Error {
	if kind := agentapi.KindOf(err); kind == agentapi.ErrCanceled || kind == agentapi.ErrTimeout {
		return agentapi.WrapError(kind, err, "request aborted")
	}
	return agentapi.WrapError(agentapi.ErrNetwork, err, "request failed")
}
