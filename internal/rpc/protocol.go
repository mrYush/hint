package rpc

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// ProtocolVersion is the only version this server speaks. A client sends
// it in hello; a mismatch is refused rather than negotiated down, so a
// newer client cannot silently talk an older dialect.
const ProtocolVersion = 1

// jsonRPC is the envelope version. It is not ProtocolVersion: one names
// the framing, the other names hint's methods.
const jsonRPC = "2.0"

// JSON-RPC error codes. The -32700/-32600 range is the spec; the -32000
// range is hint's. A client tells a missing session from a broken server
// by the code, not by the message text.
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603

	CodeUnauthorized = -32001
	CodeProtocol     = -32002
	CodeNotReady     = -32003
	CodeBusy         = -32004
	CodeNotFound     = -32005
)

// request is one line from the client. ID is absent on a notification;
// this server does not send notifications inbound, and a request without
// an id is rejected.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	hasID   bool
}

func (r *request) UnmarshalJSON(b []byte) error {
	var probe struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	*r = request{JSONRPC: probe.JSONRPC, ID: probe.ID, Method: probe.Method, Params: probe.Params}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	_, r.hasID = keys["id"]
	return nil
}

// response is one line back to the client.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// notification is a line with no id: an event, or the end of a turn.
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Error is the error object of a JSON-RPC response. A Client returns it
// as the error of the call that failed, so a caller can match on Code.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func errCode(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Decisions a client may send to permission.reply.
const (
	DecisionAllow  = "allow"
	DecisionDeny   = "deny"
	DecisionAlways = "always"
)

type helloParams struct {
	ProtocolVersion int    `json:"protocol_version"`
	Token           string `json:"token,omitempty"`
}

type helloResult struct {
	ProtocolVersion int    `json:"protocol_version"`
	Server          string `json:"server"`
	Version         string `json:"version,omitempty"`
}

type sessionParams struct {
	ID string `json:"id,omitempty"`
}

// SessionView is what session.open and session.attach return: enough
// for a client to announce what it continues and to render the history.
// Messages is never null on the wire.
type SessionView struct {
	ID       string             `json:"id"`
	Created  time.Time          `json:"created"`
	Messages []agentapi.Message `json:"messages"`
	Warnings []string           `json:"warnings,omitempty"`
	// Profile and Model are the provider the session answers with.
	Profile string `json:"profile,omitempty"`
	Model   string `json:"model,omitempty"`
	// Parent is set for a fork.
	Parent *Parent `json:"parent,omitempty"`
}

type profileParams struct {
	SessionID string `json:"session_id"`
	Profile   string `json:"profile"`
	Model     string `json:"model,omitempty"`
}

type profilesResult struct {
	Profiles []ProfileInfo `json:"profiles"`
}

type completeCancelParams struct {
	ID string `json:"id"`
}

type modelsParams struct {
	Profile string `json:"profile"`
}

type modelsResult struct {
	Models []string `json:"models"`
}

type listResult struct {
	Sessions []Info `json:"sessions"`
}

type okResult struct {
	OK bool `json:"ok"`
}

type turnParams struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type turnResult struct {
	TurnID string `json:"turn_id"`
}

type permissionParams struct {
	TurnID   string `json:"turn_id"`
	CallID   string `json:"call_id"`
	Decision string `json:"decision"`
}

type cancelParams struct {
	TurnID string `json:"turn_id"`
}

// eventParams is the body of an "event" notification.
type eventParams struct {
	TurnID    string `json:"turn_id"`
	SessionID string `json:"session_id"`
	Event     any    `json:"event"`
}

// doneParams is the body of a "turn.done" notification.
type doneParams struct {
	TurnID       string `json:"turn_id"`
	SessionID    string `json:"session_id"`
	FinishReason string `json:"finish_reason,omitempty"`
}
