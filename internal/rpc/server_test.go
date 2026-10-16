package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestHelloThenTurnWithPermission(t *testing.T) {
	engine := &fakeEngine{sessions: map[string]*fakeSession{}}
	srv := &Server{Engine: engine, PermissionWait: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, peer := net.Pipe()
	defer func() { _ = client.Close() }()
	go func() { _ = srv.serveConn(ctx, peer, peer, peer) }()

	enc := json.NewEncoder(client)
	dec := json.NewDecoder(client)

	mustSend(t, enc, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "hello",
		"params": map[string]any{"protocol_version": ProtocolVersion},
	})
	hello := mustRecv(t, dec)
	if hello["error"] != nil {
		t.Fatalf("hello: %v", hello["error"])
	}

	mustSend(t, enc, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session.open"})
	opened := mustRecv(t, dec)
	result, _ := opened["result"].(map[string]any)
	sid, _ := result["id"].(string)
	if sid == "" {
		t.Fatalf("session.open = %v", opened)
	}

	mustSend(t, enc, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "turn.start",
		"params": map[string]any{"session_id": sid, "text": "run the tests"},
	})
	started := mustRecv(t, dec)
	turnID, _ := started["result"].(map[string]any)["turn_id"].(string)
	if turnID == "" {
		t.Fatalf("turn.start = %v", started)
	}

	var replied bool
	for {
		msg := mustRecv(t, dec)
		if msg["method"] == "turn.done" {
			break
		}
		if msg["method"] != "event" {
			if id, ok := msg["id"]; ok && id == float64(4) && msg["error"] != nil {
				t.Fatalf("permission.reply: %v", msg["error"])
			}
			continue
		}
		params, _ := msg["params"].(map[string]any)
		ev, _ := params["event"].(map[string]any)
		if ev["kind"] == string(agentapi.EventPermission) && !replied {
			replied = true
			perm, _ := ev["permission"].(map[string]any)
			mustSend(t, enc, map[string]any{
				"jsonrpc": "2.0", "id": 4, "method": "permission.reply",
				"params": map[string]any{
					"turn_id": turnID, "call_id": perm["call_id"], "decision": DecisionAllow,
				},
			})
		}
	}
	if !replied {
		t.Fatal("permission event was not delivered")
	}
	if !engine.sessions[sid].allowed {
		t.Fatal("engine did not observe the allow decision")
	}
}

func TestMethodBeforeHelloIsRejected(t *testing.T) {
	srv := &Server{Engine: &fakeEngine{sessions: map[string]*fakeSession{}}}
	client, peer := net.Pipe()
	defer func() { _ = client.Close() }()
	go func() { _ = srv.serveConn(context.Background(), peer, peer, peer) }()

	enc := json.NewEncoder(client)
	dec := json.NewDecoder(client)
	mustSend(t, enc, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session.list"})
	msg := mustRecv(t, dec)
	errObj, _ := msg["error"].(map[string]any)
	if errObj["code"] != float64(CodeNotReady) {
		t.Fatalf("error = %v", msg["error"])
	}
}

func TestSocketTokenRequired(t *testing.T) {
	srv := &Server{Engine: &fakeEngine{sessions: map[string]*fakeSession{}}, Token: "secret-token"}
	client, peer := net.Pipe()
	defer func() { _ = client.Close() }()
	go func() { _ = srv.serveConn(context.Background(), peer, peer, peer) }()

	enc := json.NewEncoder(client)
	dec := json.NewDecoder(client)
	mustSend(t, enc, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "hello",
		"params": map[string]any{"protocol_version": ProtocolVersion, "token": "nope"},
	})
	msg := mustRecv(t, dec)
	errObj, _ := msg["error"].(map[string]any)
	if errObj["code"] != float64(CodeUnauthorized) {
		t.Fatalf("error = %v", msg["error"])
	}
}

func TestPermissionTimeoutDenies(t *testing.T) {
	engine := &fakeEngine{sessions: map[string]*fakeSession{}}
	srv := &Server{Engine: engine, PermissionWait: 20 * time.Millisecond}
	client, peer := net.Pipe()
	defer func() { _ = client.Close() }()
	go func() { _ = srv.serveConn(context.Background(), peer, peer, peer) }()

	enc := json.NewEncoder(client)
	dec := json.NewDecoder(client)
	mustSend(t, enc, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "hello",
		"params": map[string]any{"protocol_version": ProtocolVersion},
	})
	_ = mustRecv(t, dec)
	mustSend(t, enc, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session.open"})
	opened := mustRecv(t, dec)
	sid := opened["result"].(map[string]any)["id"].(string)

	mustSend(t, enc, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "turn.start",
		"params": map[string]any{"session_id": sid, "text": "please"},
	})
	_ = mustRecv(t, dec)
	for {
		msg := mustRecv(t, dec)
		if msg["method"] == "turn.done" {
			break
		}
	}
	if engine.sessions[sid].allowed {
		t.Fatal("timed-out permission was allowed")
	}
}

func TestSecondTurnIsBusy(t *testing.T) {
	engine := &fakeEngine{sessions: map[string]*fakeSession{}}
	srv := &Server{Engine: engine, PermissionWait: time.Second}
	client, peer := net.Pipe()
	defer func() { _ = client.Close() }()
	go func() { _ = srv.serveConn(context.Background(), peer, peer, peer) }()

	enc := json.NewEncoder(client)
	dec := json.NewDecoder(client)
	mustSend(t, enc, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "hello",
		"params": map[string]any{"protocol_version": ProtocolVersion},
	})
	_ = mustRecv(t, dec)
	mustSend(t, enc, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session.open"})
	opened := mustRecv(t, dec)
	sid := opened["result"].(map[string]any)["id"].(string)

	mustSend(t, enc, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "turn.start",
		"params": map[string]any{"session_id": sid, "text": "first"},
	})
	_ = mustRecv(t, dec) // turn id
	// Drain until the permission event so the turn is inside Prompt.
	for {
		msg := mustRecv(t, dec)
		if msg["method"] == "event" {
			ev, _ := msg["params"].(map[string]any)["event"].(map[string]any)
			if ev["kind"] == string(agentapi.EventPermission) {
				break
			}
		}
	}
	mustSend(t, enc, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "turn.start",
		"params": map[string]any{"session_id": sid, "text": "second"},
	})
	busy := mustRecv(t, dec)
	errObj, _ := busy["error"].(map[string]any)
	if errObj["code"] != float64(CodeBusy) {
		t.Fatalf("second turn = %v", busy)
	}
}

// TestNextTurnStartsRightAfterDone pins the other half of the busy rule:
// turn.done means the session is free, so a turn.start sent the moment
// it arrives is accepted, not refused as busy. A REPL does exactly this
// between one answer and the next question.
func TestNextTurnStartsRightAfterDone(t *testing.T) {
	engine := &fakeEngine{sessions: map[string]*fakeSession{}}
	srv := &Server{Engine: engine, PermissionWait: time.Second}
	client, peer := net.Pipe()
	defer func() { _ = client.Close() }()
	go func() { _ = srv.serveConn(context.Background(), peer, peer, peer) }()

	enc := json.NewEncoder(client)
	dec := json.NewDecoder(client)
	mustSend(t, enc, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "hello",
		"params": map[string]any{"protocol_version": ProtocolVersion},
	})
	_ = mustRecv(t, dec)
	mustSend(t, enc, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session.open"})
	sid := mustRecv(t, dec)["result"].(map[string]any)["id"].(string)

	// runTurn drives one turn to turn.done, allowing its permission
	// question on the way, and returns the turn.start response.
	runTurn := func(id int, text string) map[string]any {
		mustSend(t, enc, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "turn.start",
			"params": map[string]any{"session_id": sid, "text": text},
		})
		started := mustRecv(t, dec)
		turnID, _ := started["result"].(map[string]any)["turn_id"].(string)
		if turnID == "" {
			return started
		}
		for {
			msg := mustRecv(t, dec)
			if msg["method"] == "turn.done" {
				return started
			}
			if msg["method"] != "event" {
				continue
			}
			ev, _ := msg["params"].(map[string]any)["event"].(map[string]any)
			if ev["kind"] == string(agentapi.EventPermission) {
				perm, _ := ev["permission"].(map[string]any)
				mustSend(t, enc, map[string]any{
					"jsonrpc": "2.0", "id": id + 1, "method": "permission.reply",
					"params": map[string]any{"turn_id": turnID, "call_id": perm["call_id"], "decision": DecisionAllow},
				})
			}
		}
	}
	if first := runTurn(3, "first"); first["error"] != nil {
		t.Fatalf("first turn: %v", first["error"])
	}
	if second := runTurn(5, "second"); second["error"] != nil {
		t.Fatalf("turn.start right after turn.done = %v; want it accepted", second["error"])
	}
}

func TestListenRejectsNonLoopback(t *testing.T) {
	if _, err := Listen("tcp", "0.0.0.0:0"); err == nil {
		t.Fatal("Listen accepted a non-loopback address")
	}
}

func TestWriteTokenIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	token, err := WriteToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("empty token")
	}
	info, err := os.Stat(filepath.Join(dir, "auth.token"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestHelloGolden(t *testing.T) {
	srv := &Server{Engine: &fakeEngine{sessions: map[string]*fakeSession{}}, Version: "test"}
	client, peer := net.Pipe()
	defer func() { _ = client.Close() }()
	go func() { _ = srv.serveConn(context.Background(), peer, peer, peer) }()

	if _, err := io.WriteString(client, `{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol_version":1}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/hello-result.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]any
	if err := json.Unmarshal(want, &golden); err != nil {
		t.Fatal(err)
	}
	gb, _ := json.Marshal(golden)
	ab, _ := json.Marshal(got)
	if string(ab) != string(gb) {
		t.Fatalf("hello result\n got %s\nwant %s", ab, gb)
	}
}

func mustSend(t *testing.T, enc *json.Encoder, v any) {
	t.Helper()
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
}

func mustRecv(t *testing.T, dec *json.Decoder) map[string]any {
	t.Helper()
	var msg map[string]any
	if err := dec.Decode(&msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

type fakeEngine struct {
	sessions map[string]*fakeSession
}

func (e *fakeEngine) List(context.Context) ([]Info, error) { return nil, nil }

func (e *fakeEngine) Open(_ context.Context, opts OpenOptions) (Session, error) {
	s := &fakeSession{id: "sess-1", ephemeral: opts.Ephemeral}
	e.sessions[s.id] = s
	return s, nil
}

func (e *fakeEngine) Attach(_ context.Context, id string) (Session, error) {
	if s := e.sessions[id]; s != nil {
		return s, nil
	}
	return nil, errors.New("no such session")
}

type fakeSession struct {
	id        string
	ephemeral bool
	allowed   bool
}

func (s *fakeSession) ID() string                   { return s.id }
func (s *fakeSession) Created() time.Time           { return time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC) }
func (s *fakeSession) Messages() []agentapi.Message { return nil }
func (s *fakeSession) Warnings() []string           { return nil }
func (s *fakeSession) Close() error                 { return nil }

func (s *fakeSession) RunTurn(ctx context.Context, text string, ask permission.Prompter) (<-chan agentapi.Event, error) {
	ch := make(chan agentapi.Event)
	go func() {
		defer close(ch)
		ch <- agentapi.Event{Kind: agentapi.EventTurnStart}
		req := agentapi.PermissionRequest{CallID: "call-1", Tool: "bash", Class: agentapi.ClassExecute, Summary: "run " + text}
		ch <- agentapi.Event{Kind: agentapi.EventPermission, Permission: &req}
		decision, _ := ask.Prompt(ctx, permission.Ask{Request: req})
		s.allowed = decision == permission.Allow || decision == permission.AllowAlways
		finish := agentapi.FinishStop
		if ctx.Err() != nil {
			finish = agentapi.FinishCanceled
		}
		ch <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: finish}
	}()
	return ch, nil
}
