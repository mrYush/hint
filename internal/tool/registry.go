package tool

import (
	"fmt"
	"sort"
	"sync"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Registry is the set of tools a run offers the model, keyed by name.
//
// It validates a tool once, at registration, so that a malformed schema or
// a duplicate name fails at startup rather than as a provider 400 in the
// middle of a turn. Its Tools and Schemas accessors are sorted by name, so
// the request sent to a provider is deterministic regardless of
// registration order.
//
// The registry is safe for concurrent use. The MCP tools are registered
// lazily on the first turn, while another session may already be running a
// turn and looking a call up; the lock keeps that read from racing the
// one-time write.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]agentapi.Tool
}

// NewRegistry builds a registry holding tools, failing on the first one
// that does not register.
func NewRegistry(tools ...agentapi.Tool) (*Registry, error) {
	r := &Registry{tools: make(map[string]agentapi.Tool, len(tools))}
	for _, t := range tools {
		if err := r.Register(t); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Register adds t. It rejects a nil tool, a name already taken, an unknown
// permission class, and a schema [agentapi.ToolSchema.Validate] would not
// send.
func (r *Registry) Register(t agentapi.Tool) error {
	if t == nil {
		return fmt.Errorf("tool: register nil tool")
	}
	if err := agentapi.SchemaOf(t).Validate(); err != nil {
		return fmt.Errorf("tool: register: %w", err)
	}
	if !t.Class().Valid() {
		return fmt.Errorf("tool: register %q: unknown action class %q", t.Name(), t.Class())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.tools[t.Name()]; dup {
		return fmt.Errorf("tool: register %q: name already registered", t.Name())
	}
	if r.tools == nil {
		r.tools = make(map[string]agentapi.Tool)
	}
	r.tools[t.Name()] = t
	return nil
}

// Lookup returns the tool registered under name.
func (r *Registry) Lookup(name string) (agentapi.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Len returns the number of registered tools.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Names returns the registered names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Tools returns the registered tools sorted by name — the slice to hand to
// the agent loop's WithTools option.
func (r *Registry) Tools() []agentapi.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]agentapi.Tool, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name])
	}
	return out
}

// Schemas returns the [agentapi.ToolSchema] of every registered tool,
// sorted by name — the list a provider request carries.
func (r *Registry) Schemas() []agentapi.ToolSchema {
	tools := r.Tools()
	out := make([]agentapi.ToolSchema, 0, len(tools))
	for _, t := range tools {
		out = append(out, agentapi.SchemaOf(t))
	}
	return out
}
