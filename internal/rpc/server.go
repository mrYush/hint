package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

// DefaultPermissionWait is how long a turn waits for permission.reply
// before the call is denied. A client that has gone away must not leave
// a tool blocked on a question nobody will see.
const DefaultPermissionWait = 2 * time.Minute

// Server accepts connections and runs turns on Engine. A non-empty Token
// is required in hello; an empty Token is the stdio mode, where holding
// the pipe is the credential.
type Server struct {
	Engine Engine
	// Token, when set, must match hello's token.
	Token string
	// Version is reported by hello. Empty is fine.
	Version string
	// PermissionWait overrides DefaultPermissionWait when set.
	PermissionWait time.Duration
	// Stop, when set, is called by the shutdown method so the process
	// can cancel the context Serve is using.
	Stop func()

	mu       sync.Mutex
	sessions map[string]*held
	turns    map[string]*liveTurn
	conns    map[*conn]struct{}
	closed   bool
}

// held is one session and whether a turn is running on it.
type held struct {
	session Session
	turnID  string
}

// liveTurn is the in-flight turn a connection owns.
type liveTurn struct {
	id        string
	sessionID string
	cancel    context.CancelFunc
	box       *mailbox
	owner     *conn
}

// Serve accepts on lis until ctx ends or Shutdown, then closes lis.
// Connections started before the close run until their own context ends.
func (s *Server) Serve(ctx context.Context, lis net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = lis.Close()
	}()
	defer s.dropSessions()

	var err error
	for {
		nc, accErr := lis.Accept()
		if accErr != nil {
			if ctx.Err() != nil || s.isClosed() {
				return nil
			}
			err = fmt.Errorf("rpc: accept: %w", accErr)
			break
		}
		go func() { _ = s.serveConn(ctx, nc, nc, nc) }()
	}
	return err
}

// ServeStdio runs one connection on r and w. Closing is the caller's:
// cancelling ctx does not unblock a read on a pipe, so the parent ends
// the process by closing the pipe or by the turn methods returning.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	return s.serveOne(ctx, nil, r, w)
}

// ServeConn runs one connection to completion: until the peer closes it,
// ctx ends, or the client sends shutdown. The in-process CLI uses it on
// one end of a net.Pipe, so the same bytes flow whether the client is in
// this process or across a socket.
func (s *Server) ServeConn(ctx context.Context, nc net.Conn) error {
	return s.serveConn(ctx, nc, nc, nc)
}

func (s *Server) serveConn(ctx context.Context, nc net.Conn, r io.Reader, w io.Writer) error {
	defer func() { _ = nc.Close() }()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = nc.Close()
		case <-done:
		}
	}()
	return s.serveOne(ctx, nc, r, w)
}

func (s *Server) serveOne(ctx context.Context, nc net.Conn, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := &conn{
		ctx:    ctx,
		cancel: cancel,
		nc:     nc,
		dec:    json.NewDecoder(r),
		enc:    json.NewEncoder(w),
		srv:    s,
	}
	s.track(c, true)
	defer s.track(c, false)
	defer s.cancelOwned(c)
	return c.loop()
}

// Shutdown refuses new accepts. Serve's listener is closed by the caller
// cancelling the context passed to Serve; this marks the server so a
// shutdown request from a client has the same effect through OnShutdown.
func (s *Server) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) track(c *conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns == nil {
		s.conns = map[*conn]struct{}{}
	}
	if add {
		s.conns[c] = struct{}{}
		return
	}
	delete(s.conns, c)
}

// Close releases the sessions the server holds. Serve does it when the
// listener closes; a caller that only used ServeConn does it itself once
// the last connection is gone.
func (s *Server) Close() {
	s.dropSessions()
}

func (s *Server) dropSessions() {
	s.mu.Lock()
	helds := s.sessions
	s.sessions = nil
	s.mu.Unlock()
	for _, h := range helds {
		_ = h.session.Close()
	}
}

func (s *Server) cancelOwned(c *conn) {
	s.mu.Lock()
	var turns []*liveTurn
	for _, t := range s.turns {
		if t.owner == c {
			turns = append(turns, t)
		}
	}
	s.mu.Unlock()
	for _, t := range turns {
		t.cancel()
	}
}

type conn struct {
	ctx     context.Context
	cancel  context.CancelFunc
	nc      net.Conn
	dec     *json.Decoder
	enc     *json.Encoder
	srv     *Server
	wmu     sync.Mutex
	helloed bool
	// completes are the complete requests in flight on this connection,
	// by the client's id, so complete.cancel and a newer complete can
	// end them. cmu guards it.
	cmu       sync.Mutex
	completes map[string]context.CancelFunc
}

func (c *conn) loop() error {
	for {
		var req request
		if err := c.dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) || c.ctx.Err() != nil {
				return nil //nolint:nilerr // the peer hanging up is how a connection ends, not a failure
			}
			var syn *json.SyntaxError
			if errors.As(err, &syn) || errors.Is(err, io.ErrUnexpectedEOF) {
				_ = c.writeError(json.RawMessage("null"), errCode(CodeParse, "parse error"))
			}
			return nil
		}
		c.dispatch(req)
	}
}

func (c *conn) dispatch(req request) {
	if req.JSONRPC != jsonRPC || req.Method == "" || !req.hasID || string(req.ID) == "null" {
		id := req.ID
		if !req.hasID || len(id) == 0 {
			id = json.RawMessage("null")
		}
		_ = c.writeError(id, errCode(CodeInvalidRequest, "invalid request"))
		return
	}
	if req.Method != "hello" && !c.helloed {
		_ = c.writeError(req.ID, errCode(CodeNotReady, "hello is required first"))
		return
	}
	switch req.Method {
	case "hello":
		c.hello(req)
	case "session.list":
		c.sessionList(req)
	case "session.open":
		c.sessionOpen(req)
	case "session.attach":
		c.sessionAttach(req)
	case "session.profile":
		c.sessionProfile(req)
	case "session.fork":
		c.sessionFork(req)
	case "profile.list":
		c.profileList(req)
	case "profile.models":
		c.profileModels(req)
	case "complete":
		c.complete(req)
	case "complete.cancel":
		c.completeCancel(req)
	case "complete.settings":
		c.completeSettings(req)
	case "turn.start":
		c.turnStart(req)
	case "permission.reply":
		c.permissionReply(req)
	case "turn.cancel":
		c.turnCancel(req)
	case "shutdown":
		c.shutdown(req)
	default:
		_ = c.writeError(req.ID, errCode(CodeMethodNotFound, "method %q not found", req.Method))
	}
}

func (c *conn) hello(req request) {
	var p helloParams
	if err := unmarshalParams(req.Params, &p); err != nil {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "%v", err))
		return
	}
	if p.ProtocolVersion != ProtocolVersion {
		_ = c.writeError(req.ID, errCode(CodeProtocol, "protocol_version %d is not supported", p.ProtocolVersion))
		return
	}
	if c.srv.Token != "" && !tokenEqual(c.srv.Token, p.Token) {
		_ = c.writeError(req.ID, errCode(CodeUnauthorized, "token rejected"))
		return
	}
	c.helloed = true
	_ = c.writeResult(req.ID, helloResult{ProtocolVersion: ProtocolVersion, Server: "hint", Version: c.srv.Version})
}

func (c *conn) sessionList(req request) {
	infos, err := c.srv.Engine.List(c.ctx)
	if err != nil {
		_ = c.writeError(req.ID, errCode(CodeInternal, "%v", err))
		return
	}
	if infos == nil {
		infos = []Info{}
	}
	_ = c.writeResult(req.ID, listResult{Sessions: infos})
}

func (c *conn) sessionOpen(req request) {
	var opts OpenOptions
	if err := unmarshalParams(req.Params, &opts); err != nil {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "%v", err))
		return
	}
	sess, err := c.srv.Engine.Open(c.ctx, opts)
	if err != nil {
		_ = c.writeError(req.ID, errCode(CodeInternal, "%v", err))
		return
	}
	sess = c.srv.hold(sess)
	_ = c.writeResult(req.ID, sessionView(sess))
}

func (c *conn) sessionAttach(req request) {
	var p sessionParams
	if err := unmarshalParams(req.Params, &p); err != nil || p.ID == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "id is required"))
		return
	}
	if sess := c.srv.lookup(p.ID); sess != nil {
		_ = c.writeResult(req.ID, sessionView(sess))
		return
	}
	sess, err := c.srv.Engine.Attach(c.ctx, p.ID)
	if err != nil {
		_ = c.writeError(req.ID, errCode(CodeNotFound, "%v", err))
		return
	}
	sess = c.srv.hold(sess)
	_ = c.writeResult(req.ID, sessionView(sess))
}

func sessionView(sess Session) SessionView {
	messages := sess.Messages()
	if messages == nil {
		messages = []agentapi.Message{}
	}
	ref := sess.Profile()
	view := SessionView{ID: sess.ID(), Created: sess.Created(), Messages: messages, Warnings: sess.Warnings(), Profile: ref.Profile, Model: ref.Model}
	if parent, ok := sess.Parent(); ok {
		view.Parent = &parent
	}
	return view
}

// sessionProfile switches a held session's provider profile. A session
// with a turn in flight is busy: the agent of that turn is already
// talking to a provider, and the switch waits for turn.done.
func (c *conn) sessionProfile(req request) {
	var p profileParams
	if err := unmarshalParams(req.Params, &p); err != nil || p.SessionID == "" || p.Profile == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "session_id and profile are required"))
		return
	}
	sess := c.srv.lookup(p.SessionID)
	if sess == nil {
		_ = c.writeError(req.ID, errCode(CodeNotFound, "session %q is not open on this server; attach it first", p.SessionID))
		return
	}
	if c.srv.busy(p.SessionID) {
		_ = c.writeError(req.ID, errCode(CodeBusy, "session %s has a turn in flight; switch after turn.done", p.SessionID))
		return
	}
	ref, err := sess.SetProfile(c.ctx, ProfileRef{Profile: p.Profile, Model: p.Model})
	if err != nil {
		_ = c.writeError(req.ID, engineError(err))
		return
	}
	_ = c.writeResult(req.ID, ref)
}

// sessionFork starts a new session from a cut of another's history. The
// source is read as it stands, so one with a turn in flight is busy:
// the cut would hold half an answer.
func (c *conn) sessionFork(req request) {
	var opts ForkOptions
	if err := unmarshalParams(req.Params, &opts); err != nil || opts.ID == "" || opts.Turn < 0 {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "id is required; turn is counted from 1, or 0 for the whole conversation"))
		return
	}
	if c.srv.busyPrefix(opts.ID) {
		_ = c.writeError(req.ID, errCode(CodeBusy, "session %s has a turn in flight; fork after turn.done", opts.ID))
		return
	}
	sess, err := c.srv.Engine.Fork(c.ctx, opts)
	if err != nil {
		_ = c.writeError(req.ID, engineError(err))
		return
	}
	sess = c.srv.hold(sess)
	_ = c.writeResult(req.ID, sessionView(sess))
}

// complete runs beside the connection's loop: a completion waits on a
// provider, and the loop must stay free for permission.reply and
// turn.cancel meanwhile. One completion at a time per connection — a
// newer request cancels the older, whose answer the client no longer
// wants.
func (c *conn) complete(req request) {
	var p CompleteRequest
	if err := unmarshalParams(req.Params, &p); err != nil || p.ID == "" || p.Text == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "id and text are required"))
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.cmu.Lock()
	for id, old := range c.completes {
		old()
		delete(c.completes, id)
	}
	if c.completes == nil {
		c.completes = map[string]context.CancelFunc{}
	}
	c.completes[p.ID] = cancel
	c.cmu.Unlock()
	go func() {
		defer func() {
			cancel()
			c.cmu.Lock()
			if c.completes[p.ID] != nil {
				delete(c.completes, p.ID)
			}
			c.cmu.Unlock()
		}()
		res, err := c.srv.Engine.Complete(ctx, p)
		switch {
		case ctx.Err() != nil:
			_ = c.writeError(req.ID, errCode(CodeInternal, "completion %s was cancelled", p.ID))
		case err != nil:
			_ = c.writeError(req.ID, engineError(err))
		default:
			res.ID = p.ID
			_ = c.writeResult(req.ID, res)
		}
	}()
}

func (c *conn) completeCancel(req request) {
	var p completeCancelParams
	if err := unmarshalParams(req.Params, &p); err != nil || p.ID == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "id is required"))
		return
	}
	c.cmu.Lock()
	cancel := c.completes[p.ID]
	delete(c.completes, p.ID)
	c.cmu.Unlock()
	if cancel != nil {
		cancel()
	}
	// A cancel for a completion that already answered is not a mistake:
	// the two crossed on the wire.
	_ = c.writeResult(req.ID, okResult{OK: true})
}

func (c *conn) completeSettings(req request) {
	s, err := c.srv.Engine.CompletionSettings(c.ctx)
	if err != nil {
		_ = c.writeError(req.ID, errCode(CodeInternal, "%v", err))
		return
	}
	_ = c.writeResult(req.ID, s)
}

// profileModels runs beside the connection's loop, as complete does: the
// listing is a request to the profile's endpoint, and permission.reply
// and turn.cancel must not wait for it.
func (c *conn) profileModels(req request) {
	var p modelsParams
	if err := unmarshalParams(req.Params, &p); err != nil || p.Profile == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "profile is required"))
		return
	}
	go func() {
		models, err := c.srv.Engine.Models(c.ctx, p.Profile)
		if err != nil {
			_ = c.writeError(req.ID, engineError(err))
			return
		}
		if models == nil {
			models = []string{}
		}
		_ = c.writeResult(req.ID, modelsResult{Models: models})
	}()
}

func (c *conn) profileList(req request) {
	infos, err := c.srv.Engine.Profiles(c.ctx)
	if err != nil {
		_ = c.writeError(req.ID, errCode(CodeInternal, "%v", err))
		return
	}
	if infos == nil {
		infos = []ProfileInfo{}
	}
	_ = c.writeResult(req.ID, profilesResult{Profiles: infos})
}

// engineError maps what an Engine reports to a JSON-RPC error: a thing
// that does not exist, a request that cannot be honoured, or a failure.
func engineError(err error) *Error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errCode(CodeNotFound, "%v", err)
	case errors.Is(err, ErrInvalid):
		return errCode(CodeInvalidParams, "%v", err)
	default:
		return errCode(CodeInternal, "%v", err)
	}
}

func (c *conn) turnStart(req request) {
	var p turnParams
	if err := unmarshalParams(req.Params, &p); err != nil || p.SessionID == "" || p.Text == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "session_id and text are required"))
		return
	}
	sess := c.srv.lookup(p.SessionID)
	if sess == nil {
		_ = c.writeError(req.ID, errCode(CodeNotFound, "session %q is not open on this server; attach it first", p.SessionID))
		return
	}
	turnID, err := newID()
	if err != nil {
		_ = c.writeError(req.ID, errCode(CodeInternal, "%v", err))
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	box := newMailbox(c.srv.wait())
	lt := &liveTurn{id: turnID, sessionID: sess.ID(), cancel: cancel, box: box, owner: c}
	if !c.srv.beginTurn(lt) {
		cancel()
		_ = c.writeError(req.ID, errCode(CodeBusy, "session %s already has a turn in flight", sess.ID()))
		return
	}
	events, err := sess.RunTurn(ctx, p.Text, box)
	if err != nil {
		c.srv.finishTurn(turnID)
		cancel()
		_ = c.writeError(req.ID, errCode(CodeInternal, "%v", err))
		return
	}
	// The id goes back before the turn ends so the client can reply to
	// permissions and cancel while events are still arriving. RunTurn
	// must return before it needs a receiver; the pump is that receiver
	// and it starts only after this response is written.
	if err := c.writeResult(req.ID, turnResult{TurnID: turnID}); err != nil {
		cancel()
		go func() {
			for range events {
			}
			c.srv.finishTurn(turnID)
		}()
		return
	}
	go c.pump(lt, events)
}

func (c *conn) pump(lt *liveTurn, events <-chan agentapi.Event) {
	defer lt.cancel()
	finish := ""
	for ev := range events {
		if ev.Kind == agentapi.EventTurnEnd {
			finish = string(ev.FinishReason)
		}
		if err := c.writeNote(notification{
			JSONRPC: jsonRPC,
			Method:  "event",
			Params:  eventParams{TurnID: lt.id, SessionID: lt.sessionID, Event: ev},
		}); err != nil {
			// Nobody hears the rest, but the session's goroutines send
			// until the turn ends: read them out, and only then let the
			// session take another turn.
			lt.cancel()
			for range events {
			}
			c.srv.finishTurn(lt.id)
			return
		}
	}
	// The session is free before the client hears turn.done: the contract
	// lets the next turn.start follow at once, so this turn must not still
	// count as in flight when that request arrives.
	c.srv.finishTurn(lt.id)
	_ = c.writeNote(notification{
		JSONRPC: jsonRPC,
		Method:  "turn.done",
		Params:  doneParams{TurnID: lt.id, SessionID: lt.sessionID, FinishReason: finish},
	})
}

func (c *conn) permissionReply(req request) {
	var p permissionParams
	if err := unmarshalParams(req.Params, &p); err != nil || p.TurnID == "" || p.CallID == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "turn_id, call_id and decision are required"))
		return
	}
	var decision permission.Decision
	switch p.Decision {
	case DecisionAllow:
		decision = permission.Allow
	case DecisionDeny, "":
		decision = permission.Deny
	case DecisionAlways:
		decision = permission.AllowAlways
	default:
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "decision must be allow, deny or always"))
		return
	}
	lt := c.srv.turnByID(p.TurnID)
	if lt == nil || lt.owner != c {
		_ = c.writeError(req.ID, errCode(CodeNotFound, "turn %q is not owned by this connection", p.TurnID))
		return
	}
	if !lt.box.deliver(p.CallID, decision) {
		_ = c.writeError(req.ID, errCode(CodeNotFound, "no permission question is waiting for call %q", p.CallID))
		return
	}
	_ = c.writeResult(req.ID, okResult{OK: true})
}

func (c *conn) turnCancel(req request) {
	var p cancelParams
	if err := unmarshalParams(req.Params, &p); err != nil || p.TurnID == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "turn_id is required"))
		return
	}
	lt := c.srv.turnByID(p.TurnID)
	if lt == nil || lt.owner != c {
		_ = c.writeError(req.ID, errCode(CodeNotFound, "turn %q is not owned by this connection", p.TurnID))
		return
	}
	lt.cancel()
	_ = c.writeResult(req.ID, okResult{OK: true})
}

func (c *conn) shutdown(req request) {
	_ = c.writeResult(req.ID, okResult{OK: true})
	c.srv.markClosed()
	if c.srv.Stop != nil {
		c.srv.Stop()
	}
	c.cancel()
}

func (c *conn) writeResult(id json.RawMessage, result any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(response{JSONRPC: jsonRPC, ID: id, Result: result})
}

func (c *conn) writeError(id json.RawMessage, e *Error) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(response{JSONRPC: jsonRPC, ID: id, Error: e})
}

func (c *conn) writeNote(n notification) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(n)
}

func (s *Server) wait() time.Duration {
	if s.PermissionWait > 0 {
		return s.PermissionWait
	}
	return DefaultPermissionWait
}

func (s *Server) hold(sess Session) Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]*held{}
	}
	if old := s.sessions[sess.ID()]; old != nil {
		// A second attach lost the race. Keep the session that may
		// already have a turn, and drop the duplicate handle — unless the
		// engine handed back the held one itself, as it does for an id
		// given by prefix.
		if sess != old.session {
			_ = sess.Close()
		}
		return old.session
	}
	s.sessions[sess.ID()] = &held{session: sess}
	return sess
}

// busy reports whether the held session id has a turn in flight. An id
// the server does not hold is not busy: nothing of it is running here.
func (s *Server) busy(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.sessions[id]
	return h != nil && h.turnID != ""
}

// busyPrefix is busy for an id that may be a prefix, as session.fork's
// is: any held session it could name with a turn in flight counts. An
// ambiguous prefix is refused as busy rather than read half-written.
func (s *Server) busyPrefix(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, h := range s.sessions {
		if h.turnID != "" && strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

func (s *Server) lookup(id string) Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h := s.sessions[id]; h != nil {
		return h.session
	}
	return nil
}

// beginTurn marks the session busy and records the turn. It reports
// whether this connection won the session.
func (s *Server) beginTurn(lt *liveTurn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.sessions[lt.sessionID]
	if h == nil || h.turnID != "" {
		return false
	}
	h.turnID = lt.id
	if s.turns == nil {
		s.turns = map[string]*liveTurn{}
	}
	s.turns[lt.id] = lt
	return true
}

func (s *Server) finishTurn(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lt := s.turns[id]
	if lt == nil {
		return
	}
	delete(s.turns, id)
	if h := s.sessions[lt.sessionID]; h != nil && h.turnID == id {
		h.turnID = ""
	}
}

func (s *Server) turnByID(id string) *liveTurn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turns[id]
}

func unmarshalParams(raw json.RawMessage, dest any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	return nil
}

func newID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func tokenEqual(want, got string) bool {
	if len(want) != len(got) {
		return false
	}
	var diff byte
	for i := 0; i < len(want); i++ {
		diff |= want[i] ^ got[i]
	}
	return diff == 0
}

// mailbox is the Prompter a turn hands to the engine. Prompt waits until
// deliver, the context ends, or the wait expires — expiry denies, which
// is the same answer as a client that never speaks.
type mailbox struct {
	wait time.Duration
	mu   sync.Mutex
	pend map[string]chan permission.Decision
}

func newMailbox(wait time.Duration) *mailbox {
	return &mailbox{wait: wait, pend: map[string]chan permission.Decision{}}
}

func (m *mailbox) Prompt(ctx context.Context, ask permission.Ask) (permission.Decision, error) {
	ch := make(chan permission.Decision, 1)
	m.mu.Lock()
	if existing, ok := m.pend[ask.Request.CallID]; ok {
		ch = existing
	} else {
		m.pend[ask.Request.CallID] = ch
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.pend, ask.Request.CallID)
		m.mu.Unlock()
	}()
	timer := time.NewTimer(m.wait)
	defer timer.Stop()
	select {
	case d := <-ch:
		return d, nil
	case <-ctx.Done():
		return permission.Deny, ctx.Err()
	case <-timer.C:
		return permission.Deny, nil
	}
}

func (m *mailbox) deliver(callID string, d permission.Decision) bool {
	m.mu.Lock()
	ch := m.pend[callID]
	if ch == nil {
		// The client answered as soon as it saw the event, which can
		// be before Prompt has registered. Keep the decision; Prompt
		// picks the same channel up.
		ch = make(chan permission.Decision, 1)
		m.pend[callID] = ch
	}
	m.mu.Unlock()
	select {
	case ch <- d:
		return true
	default:
		return false
	}
}
