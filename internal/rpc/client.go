package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

// ErrClosed is returned by every call once the connection has ended.
var ErrClosed = errors.New("rpc: connection closed")

// Client speaks the protocol from the client side over one connection:
// requests get a response, notifications feed the turn they belong to.
// It is the only way the CLI reaches the core, whether the server runs in
// the same process on a net.Pipe or behind a socket.
//
// A Client is safe for concurrent use. One goroutine reads the
// connection and never blocks on a consumer: a turn's events are queued,
// so a client that is busy showing a permission prompt does not stall the
// response to its own reply.
type Client struct {
	rw    io.ReadWriteCloser
	enc   *json.Encoder
	wmu   sync.Mutex
	hello helloResult

	mu      sync.Mutex
	nextID  int64
	pending map[int64]*call
	turns   map[string]*Turn
	err     error
	done    chan struct{}
}

// call is one request waiting for its response. turn is set for
// turn.start: the reader registers the Turn under the id the server
// chose before it reads the next line, so the first event cannot arrive
// for a turn the client does not know yet.
type call struct {
	ch   chan incoming
	turn *Turn
}

// incoming is any line from the server: a response (ID set) or a
// notification (Method set).
type incoming struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *Error          `json:"error"`
}

// Dial performs the handshake on rw and returns a Client once the server
// has accepted it. token is what hello presents; empty for stdio and
// in-process connections. rw is closed when the Client is.
func Dial(ctx context.Context, rw io.ReadWriteCloser, token string) (*Client, error) {
	c := &Client{
		rw:      rw,
		enc:     json.NewEncoder(rw),
		pending: map[int64]*call{},
		turns:   map[string]*Turn{},
		done:    make(chan struct{}),
	}
	go c.read()
	if err := c.call(ctx, "hello", helloParams{ProtocolVersion: ProtocolVersion, Token: token}, &c.hello); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("rpc: hello: %w", err)
	}
	return c, nil
}

// ServerVersion is the build the server reported in hello.
func (c *Client) ServerVersion() string { return c.hello.Version }

// Close ends the connection. Calls in flight fail with ErrClosed and
// open turns see their event channel closed.
func (c *Client) Close() error {
	// Fail first so that waiters see ErrClosed, not whatever the reader
	// gets out of a closed pipe a moment later.
	c.fail(ErrClosed)
	return c.rw.Close()
}

// ListSessions is session.list.
func (c *Client) ListSessions(ctx context.Context) ([]Info, error) {
	var res listResult
	if err := c.call(ctx, "session.list", nil, &res); err != nil {
		return nil, err
	}
	return res.Sessions, nil
}

// OpenSession is session.open.
func (c *Client) OpenSession(ctx context.Context, opts OpenOptions) (SessionView, error) {
	var res SessionView
	err := c.call(ctx, "session.open", opts, &res)
	return res, err
}

// AttachSession is session.attach; id may be a unique prefix.
func (c *Client) AttachSession(ctx context.Context, id string) (SessionView, error) {
	var res SessionView
	err := c.call(ctx, "session.attach", sessionParams{ID: id}, &res)
	return res, err
}

// SetProfile is session.profile: it switches sessionID to ref and returns
// what is in effect.
func (c *Client) SetProfile(ctx context.Context, sessionID string, ref ProfileRef) (ProfileRef, error) {
	var res ProfileRef
	err := c.call(ctx, "session.profile", profileParams{SessionID: sessionID, Profile: ref.Profile, Model: ref.Model}, &res)
	return res, err
}

// ForkSession is session.fork.
func (c *Client) ForkSession(ctx context.Context, opts ForkOptions) (SessionView, error) {
	var res SessionView
	err := c.call(ctx, "session.fork", opts, &res)
	return res, err
}

// Profiles is profile.list.
func (c *Client) Profiles(ctx context.Context) ([]ProfileInfo, error) {
	var res profilesResult
	if err := c.call(ctx, "profile.list", nil, &res); err != nil {
		return nil, err
	}
	return res.Profiles, nil
}

// Shutdown asks the server to stop.
func (c *Client) Shutdown(ctx context.Context) error {
	var res okResult
	return c.call(ctx, "shutdown", nil, &res)
}

// StartTurn is turn.start. It returns once the server has accepted the
// turn; the events follow on Turn.Events.
func (c *Client) StartTurn(ctx context.Context, sessionID, text string) (*Turn, error) {
	t := &Turn{SessionID: sessionID, client: c, events: newQueue[agentapi.Event]()}
	var res turnResult
	if err := c.callTurn(ctx, "turn.start", turnParams{SessionID: sessionID, Text: text}, &res, t); err != nil {
		return nil, err
	}
	return t, nil
}

// call sends one request and decodes its result into out. A server error
// comes back as *Error; a connection that ends first as the reader's
// error.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	return c.callTurn(ctx, method, params, out, nil)
}

func (c *Client) callTurn(ctx context.Context, method string, params, out any, t *Turn) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.nextID++
	id := c.nextID
	cl := &call{ch: make(chan incoming, 1), turn: t}
	c.pending[id] = cl
	c.mu.Unlock()

	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{jsonRPC, id, method, params}
	if err := c.write(req); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("rpc: %s: %w", method, err)
	}

	select {
	case msg := <-cl.ch:
		if msg.Error != nil {
			return msg.Error
		}
		if out == nil || len(msg.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(msg.Result, out); err != nil {
			return fmt.Errorf("rpc: %s: result: %w", method, err)
		}
		return nil
	case <-c.done:
		return c.readErr()
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *Client) write(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(v)
}

// read is the one reader of the connection. It routes responses to the
// call waiting for them and notifications to their turn, and on any
// failure fails everything that is still waiting.
func (c *Client) read() {
	dec := json.NewDecoder(c.rw)
	for {
		var msg incoming
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				err = ErrClosed
			}
			c.fail(err)
			return
		}
		if msg.Method != "" {
			c.notify(msg)
			continue
		}
		id, err := strconv.ParseInt(string(msg.ID), 10, 64)
		if err != nil {
			// A response to nobody: the server echoed an id we never
			// sent (or null for a parse error on our side). Nothing
			// waits for it.
			continue
		}
		c.mu.Lock()
		cl := c.pending[id]
		delete(c.pending, id)
		if cl != nil && cl.turn != nil && msg.Error == nil {
			var res turnResult
			if json.Unmarshal(msg.Result, &res) == nil && res.TurnID != "" {
				cl.turn.ID = res.TurnID
				c.turns[res.TurnID] = cl.turn
			}
		}
		c.mu.Unlock()
		if cl != nil {
			cl.ch <- msg
		}
	}
}

func (c *Client) notify(msg incoming) {
	switch msg.Method {
	case "event":
		var p struct {
			TurnID string         `json:"turn_id"`
			Event  agentapi.Event `json:"event"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return
		}
		if t := c.turn(p.TurnID); t != nil {
			t.events.push(p.Event)
		}
	case "turn.done":
		var p doneParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return
		}
		c.mu.Lock()
		t := c.turns[p.TurnID]
		delete(c.turns, p.TurnID)
		c.mu.Unlock()
		if t != nil {
			t.finish = agentapi.FinishReason(p.FinishReason)
			t.events.close()
		}
	}
}

func (c *Client) turn(id string) *Turn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turns[id]
}

// fail ends every pending call and open turn with err. The first failure
// wins; later ones are no-ops.
func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	turns := c.turns
	c.turns = map[string]*Turn{}
	c.mu.Unlock()
	close(c.done)
	for _, t := range turns {
		t.err = err
		t.events.close()
	}
}

func (c *Client) readErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Turn is one turn the client started. Events delivers the server's
// events in order and is closed after turn.done — or when the connection
// breaks, which Err then reports.
type Turn struct {
	ID        string
	SessionID string
	client    *Client
	events    *queue[agentapi.Event]
	finish    agentapi.FinishReason
	err       error
}

// Events is the turn's event stream. Reading it to the end is how a
// caller learns the turn is over; FinishReason and Err are valid then.
func (t *Turn) Events() <-chan agentapi.Event { return t.events.out }

// FinishReason is what turn.done carried, once Events is closed.
func (t *Turn) FinishReason() agentapi.FinishReason { return t.finish }

// Err is why Events closed without a turn.done, or nil.
func (t *Turn) Err() error { return t.err }

// Reply answers the permission question for callID.
func (t *Turn) Reply(ctx context.Context, callID string, d permission.Decision) error {
	var res okResult
	return t.client.call(ctx, "permission.reply", permissionParams{TurnID: t.ID, CallID: callID, Decision: decisionName(d)}, &res)
}

// Cancel asks the server to stop the turn. Events still runs to its
// turn.done, which reports FinishCanceled.
func (t *Turn) Cancel(ctx context.Context) error {
	var res okResult
	return t.client.call(ctx, "turn.cancel", cancelParams{TurnID: t.ID}, &res)
}

func decisionName(d permission.Decision) string {
	switch d {
	case permission.Allow:
		return DecisionAllow
	case permission.AllowAlways:
		return DecisionAlways
	case permission.Deny:
		return DecisionDeny
	default:
		// An unknown decision is no decision, and no decision denies.
		return DecisionDeny
	}
}

// queue is an unbounded channel: push never blocks, out delivers in
// order. The reader goroutine feeds it so a slow consumer can never hold
// up the connection — and with it the responses that same consumer is
// waiting for.
type queue[T any] struct {
	mu     sync.Mutex
	items  []T
	wake   chan struct{}
	closed bool
	out    chan T
}

func newQueue[T any]() *queue[T] {
	q := &queue[T]{wake: make(chan struct{}, 1), out: make(chan T)}
	go q.drain()
	return q
}

func (q *queue[T]) push(v T) {
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, v)
	}
	q.mu.Unlock()
	q.signal()
}

func (q *queue[T]) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

func (q *queue[T]) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *queue[T]) drain() {
	defer close(q.out)
	for {
		q.mu.Lock()
		if len(q.items) == 0 && q.closed {
			q.mu.Unlock()
			return
		}
		var batch []T
		batch, q.items = q.items, nil
		q.mu.Unlock()
		for _, v := range batch {
			q.out <- v
		}
		if len(batch) == 0 {
			<-q.wake
		}
	}
}
