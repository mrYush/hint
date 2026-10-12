package rpc

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the only version this server speaks. A client sends
// it in hello; a mismatch is refused rather than negotiated down, so a
// newer client cannot silently talk an older dialect.
const ProtocolVersion = 1

// jsonRPC is the envelope version. It is not ProtocolVersion: one names
// the framing, the other names hint's methods.
const jsonRPC = "2.0"

// JSON-RPC error codes. The -32700/-32600 range is the spec; the -32000
// range is hint's.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603

	codeUnauthorized = -32001
	codeProtocol     = -32002
	codeNotReady     = -32003
	codeBusy         = -32004
	codeNotFound     = -32005
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
	Error   *rpcError       `json:"error,omitempty"`
}

// notification is a line with no id: an event, or the end of a turn.
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func errCode(code int, format string, args ...any) *rpcError {
	return &rpcError{Code: code, Message: fmt.Sprintf(format, args...)}
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
