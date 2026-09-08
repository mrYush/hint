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
	activation *project.Activation
	// notices is where a loaded rule and a layout warning are announced.
	notices io.Writer
	trace   func(format string, args ...any)

	mu     sync.Mutex
	key    string // paths of the active rules the cached prompt was built for
	prompt string
	warned map[string]bool
}

func newPreamble(pc *project.Context, registry *tool.Registry, notices io.Writer, trace func(string, ...any)) *preamble {
	p := &preamble{pc: pc, registry: registry, activation: project.NewActivation(pc.Rules), notices: notices, trace: trace, warned: map[string]bool{}}
	// The prompt every turn opens with: the always-loaded files and the
	// index of what a touch may still bring in.
	p.prompt = systemPrompt(pc, pc.Instructions, p.activation.Pending())
	return p
}

// Prefix implements agent.Preamble.
func (p *preamble) Prefix(ctx context.Context, messages []agentapi.Message) ([]agentapi.Message, error) {
	var touched []string
	for _, m := range messages {
		if m.Role != agentapi.RoleAssistant {
			continue
		}
		for _, c := range m.ToolCalls {
			t, ok := p.registry.Lookup(c.Name)
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
		for _, w := range warnings {
			if !p.warned[w] {
				p.warned[w] = true
				fmt.Fprintf(p.notices, "hint: warning: %s\n", w)
			}
		}
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
