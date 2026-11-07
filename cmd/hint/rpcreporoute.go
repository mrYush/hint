package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/internal/workspace"
	"github.com/mrYush/hint/pkg/agentapi"
)

// sessionTools binds built-in file operations to live session membership.
// Construction does not open all roots or materialize their files.
func (s *rpcSession) sessionTools() []agentapi.Tool {
	tools := s.eng.registry.Tools()
	if s.workspace == nil {
		return tools
	}
	roots, _ := s.workspace.Snapshot()
	expanded := len(roots) > 1
	var mapTools []agentapi.Tool
	handles := false
	if s.eng.mapIndex != nil {
		mapTools = s.eng.mapIndex.Tools(s.workspace)
		for _, t := range mapTools {
			handles = handles || t.Name() == "repo_map"
		}
	}
	for i, original := range tools {
		if original.Name() == "instructions" {
			if expanded {
				tools[i] = tool.Redacting(&repoInstructionTool{Tool: original, session: s}, s.eng.secrets)
			}
			continue
		}
		factory := repoToolFactory(original.Name())
		if factory == nil {
			continue
		}
		tools[i] = tool.Redacting(&repoRoutedTool{Tool: original, session: s, factory: factory, expanded: expanded, handles: handles}, s.eng.secrets)
	}
	if expanded {
		tools = append(tools, tool.Redacting(tool.WithLimits(&repoPrepareTool{session: s}, tool.DefaultLimits()), s.eng.secrets))
	}
	for _, t := range mapTools {
		tools = append(tools, tool.Redacting(t, s.eng.secrets))
	}
	return tools
}

type repoFactory func(tool.Root, ...builtin.Option) agentapi.Tool

func (s *rpcSession) lookupSessionTool(name string) (agentapi.Tool, bool) {
	t, ok := s.eng.registry.Lookup(name)
	if !ok {
		return nil, false
	}
	if factory := repoToolFactory(name); factory != nil && s.workspace != nil {
		return &repoRoutedTool{Tool: t, session: s, factory: factory}, true
	}
	return t, true
}

func repoToolFactory(name string) repoFactory {
	switch name {
	case "read_file":
		return builtin.NewReadFile
	case "write_file":
		return builtin.NewWriteFile
	case "edit_file":
		return builtin.NewEditFile
	case "list_dir":
		return builtin.NewListDir
	case "glob":
		return builtin.NewGlob
	case "grep":
		return builtin.NewGrep
	case "bash":
		return builtin.NewBash
	default:
		return nil
	}
}

type repoRoutedTool struct {
	agentapi.Tool
	session  *rpcSession
	factory  repoFactory
	expanded bool
	handles  bool
}

func (t *repoRoutedTool) Description() string {
	description := t.Tool.Description()
	if t.expanded {
		description += "\nOptional repo selects an attached root by ID or alias; omission selects primary. Prepare a secondary target with repo_prepare before writes or commands."
	}
	if t.handles && t.Name() != "bash" {
		description += " Optional entry_id selects an indexed target instead of path."
	}
	return description
}

func (t *repoRoutedTool) InputSchema() json.RawMessage {
	if !t.expanded && !t.handles {
		return t.Tool.InputSchema()
	}
	var schema map[string]any
	if err := json.Unmarshal(t.Tool.InputSchema(), &schema); err != nil {
		panic(err)
	}
	properties, _ := schema["properties"].(map[string]any)
	if t.expanded {
		properties["repo"] = map[string]any{"type": "string", "description": "Attached root ID or alias; default primary."}
	}
	if t.handles && t.Name() != "bash" {
		properties["entry_id"] = map[string]any{"type": "string", "description": "Opaque entry handle, mutually exclusive with path."}
		if required, ok := schema["required"].([]any); ok {
			filtered := make([]any, 0, len(required))
			for _, name := range required {
				if name == "path" {
					schema["anyOf"] = []any{map[string]any{"required": []string{"path"}}, map[string]any{"required": []string{"entry_id"}}}
				} else {
					filtered = append(filtered, name)
				}
			}
			schema["required"] = filtered
		}
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return encoded
}

type routedCall struct {
	tool   agentapi.Tool
	args   json.RawMessage
	info   workspace.RootInfo
	lease  workspace.Lease
	target string
}

func (t *repoRoutedTool) route(ctx context.Context, args json.RawMessage) (routedCall, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return routedCall{}, err
	}
	if fields == nil {
		return routedCall{}, errors.New("arguments must be an object")
	}
	var repo, entry, path string
	for name, target := range map[string]*string{"repo": &repo, "entry_id": &entry, "path": &path} {
		if raw, ok := fields[name]; ok {
			if string(raw) == "null" {
				return routedCall{}, fmt.Errorf("%s must be a string", name)
			}
			if err := json.Unmarshal(raw, target); err != nil {
				return routedCall{}, fmt.Errorf("%s must be a string", name)
			}
		}
	}
	_, entryPresent := fields["entry_id"]
	_, pathPresent := fields["path"]
	if entryPresent && (entry == "" || pathPresent || t.Name() == "bash") {
		return routedCall{}, errors.New("entry_id requires a file tool and cannot be combined with path")
	}
	root, info, err := t.session.workspace.ToolRoot(repo)
	if err != nil {
		return routedCall{}, err
	}
	lease, err := t.session.workspace.Resolve(info.ID, ".")
	if err != nil {
		return routedCall{}, err
	}
	root = root.WithExactPaths()
	if entry != "" {
		if t.session.eng.mapIndex == nil {
			return routedCall{}, errors.New("entry_id requires the repository index")
		}
		target, lookupErr := t.session.eng.mapIndex.ResolveEntry(ctx, t.session.workspace, info.ID, entry)
		if lookupErr != nil {
			return routedCall{}, lookupErr
		}
		const alias = "__hint_selected_entry__"
		root = root.WithPathAlias(alias, target.Path)
		path = target.RelativePath
		fields["path"], _ = json.Marshal(alias)
	}
	delete(fields, "repo")
	delete(fields, "entry_id")
	raw, err := json.Marshal(fields)
	if err != nil {
		return routedCall{}, err
	}
	options := []builtin.Option{builtin.WithEnviron(t.session.eng.environ), builtin.WithRipgrep("")}
	if !info.Primary {
		if _, err := root.Stat(filepath.Join(root.Dir(), ".git")); errors.Is(err, os.ErrNotExist) {
			// An explicitly attached ordinary folder has its own policy scope,
			// matching the index rather than inheriting an ancestor repository.
			options = append(options, builtin.WithGit(""))
		} else if err != nil {
			return routedCall{}, fmt.Errorf("inspect root Git boundary: %w", err)
		}
	}
	selected := t.factory(root, options...)
	return routedCall{tool: selected, args: raw, info: info, lease: lease, target: path}, nil
}

func (t *repoRoutedTool) Run(ctx context.Context, id string, args json.RawMessage) (agentapi.ToolResult, error) {
	call, err := t.route(ctx, args)
	if err != nil {
		return agentapi.ErrorResult(id, t.Name(), err.Error()), nil
	}
	if t.Class() != agentapi.ClassRead {
		if err := t.session.requireRepoInstructions(ctx, call.info, call.target); err != nil {
			return agentapi.ErrorResult(id, t.Name(), err.Error()), nil
		}
	}
	result, err := call.tool.Run(ctx, id, call.args)
	if err != nil {
		return result, err
	}
	if err = call.lease.Publish(func() error { return nil }); err != nil {
		return agentapi.ErrorResult(id, t.Name(), err.Error()), nil
	}
	return result, nil
}

func (t *repoRoutedTool) Describe(ctx context.Context, args json.RawMessage) (tool.Description, error) {
	call, err := t.route(ctx, args)
	if err != nil {
		return tool.Description{}, err
	}
	req := tool.Describe(ctx, agentapi.ToolCall{Name: t.Name(), Arguments: call.args}, call.tool)
	return tool.Description{Summary: fmt.Sprintf("[%s] %s", call.info.Alias, req.Summary), Detail: req.Detail, Path: fmt.Sprintf("%s:%s", call.info.Alias, req.Path)}, nil
}

func (t *repoRoutedTool) Touches(args json.RawMessage) []string {
	call, err := t.route(context.Background(), args)
	if err != nil {
		return nil
	}
	return tool.Touches(agentapi.ToolCall{Name: t.Name(), Arguments: call.args}, call.tool)
}
