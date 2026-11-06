package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// preamble is the agent.Preamble of a run: it rebuilds the system prompt
// before every request so that a split rule (WP0.12) is in the prompt as
// soon as a tool has touched a path it applies to.
//
// The set of loaded rules is derived from the conversation itself — every
// tool call in the history is asked, through tool.Touches, which files it
// named — so a continued session (-c) loads on its first request whatever
// the earlier run had loaded, and the session format carries nothing new.
// A rule once loaded stays loaded for the run, which is what keeps it in
// the prompt after compaction has summarized the calls that loaded it.
type preamble struct {
	pc         *project.Context
	registry   *tool.Registry
	lookup     func(string) (agentapi.Tool, bool)
	activation *project.Activation
	// notices is where a loaded rule and a layout warning are announced.
	notices io.Writer
	trace   func(format string, args ...any)
	redact  func(string) string

	mu       sync.Mutex
	key      string // paths of the active rules the cached prompt was built for
	prompt   string
	warned   map[string]bool
	revision uint64
}

func newPreamble(pc *project.Context, registry *tool.Registry, notices io.Writer, trace func(string, ...any), redact func(string) string) *preamble {
	snapshot := pc.Snapshot()
	p := &preamble{pc: pc, registry: registry, activation: project.NewActivation(snapshot.Rules), notices: notices, trace: trace, redact: redact, warned: map[string]bool{}, revision: snapshot.Revision}
	for _, warning := range snapshot.Warnings {
		p.warned[warning] = true
	}
	// The prompt every turn opens with: the always-loaded files and the
	// index of what a touch may still bring in.
	p.prompt = systemPrompt(pc, snapshot.Instructions, p.activation.Pending())
	return p
}

// refresh rebuilds the initial prompt only after configured MCP resources
// join the project context during the first turn's lazy initialization.
func (p *preamble) refresh(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := p.pc.Snapshot()
	if snapshot.Revision == p.revision {
		return
	}
	// AddInstructionSources has already reported the no-rules warnings.
	for _, warning := range snapshot.Warnings {
		p.warned[warning] = true
	}
	active := p.activation.Active()
	instructions := snapshot.Instructions
	if len(active) > 0 {
		var warnings []string
		instructions, warnings = p.pc.Layout(ctx, active)
		p.warn(warnings)
	}
	p.prompt = systemPrompt(p.pc, instructions, p.activation.Pending())
	p.key = activeKey(active)
	p.revision = snapshot.Revision
}

func (p *preamble) warn(warnings []string) {
	for _, warning := range warnings {
		if p.warned[warning] {
			continue
		}
		p.warned[warning] = true
		if p.redact != nil {
			warning = p.redact(warning)
		}
		fmt.Fprintf(p.notices, "hint: warning: %s\n", warning)
	}
}

// Prefix implements agent.Preamble.
func (p *preamble) Prefix(ctx context.Context, messages []agentapi.Message) ([]agentapi.Message, error) {
	var touched []string
	for _, m := range messages {
		if m.Role != agentapi.RoleAssistant {
			continue
		}
		for _, c := range m.ToolCalls {
			lookup := p.lookup
			if lookup == nil {
				lookup = p.registry.Lookup
			}
			t, ok := lookup(c.Name)
			if !ok {
				continue
			}
			touched = append(touched, tool.Touches(c, t)...)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.activation.Touch(touched...) {
		fmt.Fprintf(p.notices, "hint: rule %q loaded from %s\n", r.Title, r.Path)
	}
	active := p.activation.Active()
	key := activeKey(active)
	if key != p.key {
		instructions, warnings := p.pc.Layout(ctx, active)
		p.warn(warnings)
		p.prompt = systemPrompt(p.pc, instructions, p.activation.Pending())
		p.key = key
		if p.trace != nil {
			p.trace("system prompt rebuilt with %d rules:\n%s", len(active), p.prompt)
		}
	}
	return []agentapi.Message{agentapi.SystemMessage(p.prompt)}, nil
}

// initial is the prefix a turn starts with; Prefix replaces it before the
// first request.
func (p *preamble) initial() []agentapi.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return []agentapi.Message{agentapi.SystemMessage(p.prompt)}
}

func activeKey(rules []project.Rule) string {
	paths := make([]string, len(rules))
	for i, r := range rules {
		paths[i] = r.Path
	}
	return strings.Join(paths, "\x00")
}
