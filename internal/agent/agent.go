// Package agent implements the WP0.4 tool-calling loop: model responds,
// tool calls run — independent reads together, everything else in order,
// under the WP0.11 schedule — results feed back, repeat until the model
// produces a final answer or a limit is hit. See docs/plan/phase-0-mvp-cli.md.
package agent

import (
	"context"
	"sort"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Limits bound one call to [Agent.RunTurn].
type Limits struct {
	// MaxIterations caps the number of tool-calling rounds in a single turn.
	// A "round" is one batch of tool calls from one assistant message; a
	// turn that only ever gets plain text never counts against this.
	MaxIterations int
	// MaxContextTokens is the model's context window, used to trigger
	// proactive compaction before a request is even sent. Zero disables
	// proactive compaction; reactive compaction on
	// [agentapi.ErrContextOverflow] still applies regardless.
	MaxContextTokens int
	// CompactThreshold is the occupancy fraction of MaxContextTokens that
	// triggers proactive compaction, e.g. 0.8 for "at 80% of the window".
	CompactThreshold float64
	// MaxParallelTools caps how many read-class tool calls of one batch run
	// at the same time. Zero or one keeps the WP0.4 chain: every call waits
	// for the previous one. Writes and commands never overlap regardless.
	MaxParallelTools int
}

// DefaultLimits returns the limits an Agent starts with, matching the
// WP0.4 checklist: 25 iterations, a 128k window (GPT-4o class), compact at
// 80% occupancy; and WP0.11's fan-out of eight reads at once — local file
// reads are I/O-bound, and more goroutines than that buy nothing on the
// smallest target (a Raspberry Pi) while making the tool_start/tool_end
// interleaving harder to follow.
func DefaultLimits() Limits {
	return Limits{MaxIterations: 25, MaxContextTokens: 128_000, CompactThreshold: 0.8, MaxParallelTools: 8}
}

// Compactor summarizes older turns into a single system message when the
// context window fills up. The default, returned by [New], asks the same
// [agentapi.ChatProvider] the agent talks to; tests inject a stub instead.
type Compactor interface {
	// Compact returns a text summary standing in for messages.
	Compact(ctx context.Context, messages []agentapi.Message) (summary string, err error)
}

// Authorizer decides whether a tool call may run, before it does. It is
// the loop's view of the permission layer (WP0.6), declared here — on the
// consumer's side, as small as the loop needs — so that internal/agent
// depends on no permission package and a test can stand in a fake.
//
// Review is called for every call the loop is about to run; when it
// reports ask == true the loop emits the request as [agentapi.EventPermission]
// and then blocks in Authorize until there is an answer. Read-class calls
// and calls an earlier grant covers come back with ask == false and are
// never announced.
type Authorizer interface {
	// Review says whether the user has to be asked about call and, if so,
	// what to show them.
	Review(ctx context.Context, call agentapi.ToolCall, tool agentapi.Tool) (req agentapi.PermissionRequest, ask bool)
	// Authorize asks. allowed reports the answer; err is non-nil only when
	// no answer could be obtained — ctx ended, or the channel to the user
	// broke — and the call is then not allowed either way.
	Authorize(ctx context.Context, req agentapi.PermissionRequest) (allowed bool, err error)
}

// Preamble rebuilds the system prefix of a request from the conversation
// so far. It is the loop's view of WP0.12's split rules — a rule loads
// once a tool has touched a path it applies to — declared here, on the
// consumer's side, so that internal/agent knows nothing of rule files:
// the loop only promises to ask before every request, so a rule a tool
// batch just triggered is in the very next prompt, and to swap the
// leading run of system messages for whatever comes back.
type Preamble interface {
	// Prefix returns the system messages to put in front of messages,
	// which is the whole conversation including any earlier prefix. An
	// error keeps the prefix the request already has.
	Prefix(ctx context.Context, messages []agentapi.Message) ([]agentapi.Message, error)
}

// Agent runs the tool-calling loop over one [agentapi.ChatProvider].
//
// An Agent is stateless between turns: [Agent.RunTurn] takes the full
// history as an argument and never retains it. Keeping a conversation
// across turns is a session's job (WP0.7), not this package's.
type Agent struct {
	provider  agentapi.ChatProvider
	tools     map[string]agentapi.Tool
	limits    Limits
	compactor Compactor
	estimator Estimator
	// authorizer gates write- and execute-class calls; nil runs every
	// call unasked, which is what a test without permissions wants and
	// what cmd/hint must never do.
	authorizer Authorizer
	// preamble rebuilds the system prefix before each request; nil keeps
	// the prefix the caller passed in for the whole turn.
	preamble Preamble
}

// Option configures an [Agent] built by [New].
//
// Functional options — the same shape as [internal/provider/router.Option]
// and the openai client's constructor — let New keep a single required
// argument (the provider) while every other knob (tools, limits, compactor,
// estimator) stays optional and self-documenting at the call site, instead
// of a config struct where the zero value of an unset field is ambiguous
// with a deliberate zero (MaxContextTokens: 0 means "disable proactive
// compaction", not "forgot to set it").
type Option func(*Agent)

// WithTools registers the tools the model may call this turn. Unset means
// no tools: the provider gets an empty [agentapi.ToolSchema] list and the
// loop only ever sees [agentapi.FinishStop]-shaped turns.
func WithTools(tools ...agentapi.Tool) Option {
	return func(a *Agent) {
		for _, t := range tools {
			a.tools[t.Name()] = t
		}
	}
}

// WithLimits overrides the default [Limits].
func WithLimits(l Limits) Option {
	return func(a *Agent) { a.limits = l }
}

// WithCompactor overrides the default [Compactor], which asks the same
// provider for a plain-text summary. Tests use this to inject a stub that
// does not make a real call.
func WithCompactor(c Compactor) Option {
	return func(a *Agent) { a.compactor = c }
}

// WithAuthorizer gates tool calls through a — the permission layer. Unset
// means no gate: every call runs as soon as the model asks for it.
func WithAuthorizer(a Authorizer) Option {
	return func(ag *Agent) { ag.authorizer = a }
}

// WithPreamble lets p rebuild the system prefix before every request of a
// turn. Unset, the prefix the caller passed in stays as it is.
func WithPreamble(p Preamble) Option {
	return func(a *Agent) { a.preamble = p }
}

// WithEstimator overrides the default character-based token [Estimator].
func WithEstimator(e Estimator) Option {
	return func(a *Agent) { a.estimator = e }
}

// New builds an Agent that runs turns against p.
func New(p agentapi.ChatProvider, opts ...Option) *Agent {
	a := &Agent{
		provider:  p,
		tools:     make(map[string]agentapi.Tool),
		limits:    DefaultLimits(),
		compactor: &llmCompactor{provider: p},
		estimator: charEstimator{},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// schemas returns the ToolSchema of every registered tool, sorted by name
// so the request sent to the provider is deterministic across calls — map
// iteration order is not, and a flaky request shape would make scripted
// provider tests flaky along with it.
func (a *Agent) schemas() []agentapi.ToolSchema {
	if len(a.tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(a.tools))
	for name := range a.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	schemas := make([]agentapi.ToolSchema, 0, len(names))
	for _, name := range names {
		schemas = append(schemas, agentapi.SchemaOf(a.tools[name]))
	}
	return schemas
}
