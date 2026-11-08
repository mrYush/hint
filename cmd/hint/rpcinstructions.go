package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/internal/project"
	hinttext "github.com/mrYush/hint/internal/text"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/internal/workspace"
	"github.com/mrYush/hint/pkg/agentapi"
)

const (
	repoInstructionBytes = 1 << 20
	repoInstructionFiles = 128
	repoInstructionDepth = 64
	repoRuleEntries      = 256
)

type repoInstructions struct {
	mu        sync.Mutex
	prepared  *repoPreparation
	delivered [sha256.Size]byte
}

type repoPreparation struct {
	root        workspace.RootInfo
	target      string
	sources     []project.InstructionSource
	fingerprint [sha256.Size]byte
	lease       workspace.Lease
}

type repoPrepareArgs struct {
	Repo    string `json:"repo" jsonschema_description:"Explicitly attached secondary root ID or alias."`
	Path    string `json:"path,omitempty" jsonschema_description:"Exact intended write/edit path, or . before a command in the root."`
	EntryID string `json:"entry_id,omitempty" jsonschema_description:"Indexed target handle for long or non-UTF-8 paths; mutually exclusive with path."`
}

type repoPrepareTool struct{ session *rpcSession }

func (*repoPrepareTool) Name() string { return "repo_prepare" }
func (*repoPrepareTool) Description() string {
	return "Prepare trusted instructions for one secondary root and target path before modifying it. Call this in a separate model round before write_file, edit_file, or bash; preparation and mutation in the same batch are refused. Only one secondary target is prepared at a time. Instructions are loaded into the next system prompt through rooted reads; use instructions with repo to read omitted sections. Root membership and permissions are unchanged."
}
func (*repoPrepareTool) InputSchema() json.RawMessage { return tool.MustSchema(repoPrepareArgs{}) }
func (*repoPrepareTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (t *repoPrepareTool) Run(ctx context.Context, id string, raw json.RawMessage) (agentapi.ToolResult, error) {
	var in repoPrepareArgs
	if err := tool.DecodeArgs(raw, &in); err != nil {
		return tool.InvalidArgs(id, t.Name(), err), nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return tool.InvalidArgs(id, t.Name(), err), nil
	}
	if fields == nil {
		return tool.InvalidArgs(id, t.Name(), errors.New("arguments must be an object")), nil
	}
	for _, name := range []string{"repo", "path", "entry_id"} {
		if value, ok := fields[name]; ok && string(value) == "null" {
			return tool.InvalidArgs(id, t.Name(), fmt.Errorf("%s must be a string", name)), nil
		}
	}
	if in.EntryID != "" {
		if _, present := fields["path"]; present {
			return tool.InvalidArgs(id, t.Name(), errors.New("entry_id and path are mutually exclusive")), nil
		}
		if t.session.eng.mapIndex == nil {
			return agentapi.ErrorResult(id, t.Name(), "entry_id requires the repository index"), nil
		}
		lease, err := t.session.eng.mapIndex.ResolveEntry(ctx, t.session.workspace, in.Repo, in.EntryID)
		if err != nil {
			return agentapi.ErrorResult(id, t.Name(), err.Error()), nil
		}
		in.Path = lease.RelativePath
	} else if _, present := fields["entry_id"]; present {
		return tool.InvalidArgs(id, t.Name(), errors.New("entry_id cannot be empty")), nil
	}
	prepared, err := t.session.loadRepoInstructions(ctx, in.Repo, in.Path)
	if err != nil {
		return agentapi.ErrorResult(id, t.Name(), err.Error()), nil
	}
	if prepared.root.Primary {
		return agentapi.TextResult(id, t.Name(), "Primary-root instructions already use the startup instruction policy."), nil
	}
	t.session.repoInstructions.mu.Lock()
	t.session.repoInstructions.prepared = prepared
	t.session.repoInstructions.delivered = [sha256.Size]byte{}
	t.session.repoInstructions.mu.Unlock()
	return agentapi.TextResult(id, t.Name(), fmt.Sprintf("Prepared %q for %q. Its instructions enter the next model request; mutate only in a subsequent tool batch.", prepared.root.Alias, prepared.target)), nil
}

// loadRepoInstructions reads only the explicit root and target ancestry.
// Discovery limits fail closed rather than silently discarding local rules.
func (s *rpcSession) loadRepoInstructions(ctx context.Context, repo, target string) (*repoPreparation, error) {
	root, info, err := s.workspace.ToolRoot(repo)
	if err != nil {
		return nil, err
	}
	lease, err := s.workspace.Resolve(info.ID, target)
	if err != nil {
		return nil, err
	}
	path := lease.Path
	dir := filepath.Dir(path)
	if stat, statErr := lease.Stat(); statErr == nil && stat.IsDir() {
		dir = path
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	dirs := project.InstructionDirs(root.Dir(), dir)
	if len(dirs) > repoInstructionDepth || len(path) > 16<<10 {
		return nil, errors.New("repository instruction ancestry exceeds its bounded preparation limit")
	}
	canonical, err := canonicalInstructionPath(root, path)
	if err != nil {
		return nil, err
	}
	canonicalDir := filepath.Dir(canonical)
	if info, err := root.Stat(canonical); err == nil && info.IsDir() {
		canonicalDir = canonical
	}
	ordered := project.InstructionDirs(root.Dir(), canonicalDir)
	seenDirs := make(map[string]bool, len(ordered))
	for _, dir := range ordered {
		seenDirs[dir] = true
	}
	for _, dir := range dirs {
		actual, err := canonicalInstructionPath(root, dir)
		if err != nil {
			return nil, err
		}
		if !seenDirs[actual] {
			ordered = append(ordered, actual)
			seenDirs[actual] = true
		}
	}
	dirs = ordered
	if len(dirs) > repoInstructionDepth {
		return nil, errors.New("repository instruction ancestry exceeds its bounded preparation limit")
	}
	p := &repoPreparation{root: info, target: lease.RelativePath, lease: lease}
	remaining := repoInstructionBytes
	read := func(path string) (string, bool, error) {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		abs, err := root.ResolveExact(path)
		if err != nil {
			return "", false, err
		}
		file, err := root.Open(abs)
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		before, err := file.Stat()
		if err != nil {
			return "", false, errors.Join(err, file.Close())
		}
		if !before.Mode().IsRegular() {
			return "", false, errors.Join(errors.New("repository instruction is not a regular file"), file.Close())
		}
		if before.Size() > int64(remaining) {
			return "", false, errors.Join(errors.New("repository instructions exceed the 1 MiB preparation limit"), file.Close())
		}
		data, readErr := io.ReadAll(io.LimitReader(file, int64(remaining)+1))
		after, statErr := file.Stat()
		if err := errors.Join(readErr, statErr, file.Close()); err != nil {
			return "", false, err
		}
		if len(data) > remaining || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return "", false, workspace.ErrChanged
		}
		remaining -= len(data)
		return string(data), true, nil
	}
	add := func(path, content string) error {
		if len(p.sources) >= repoInstructionFiles {
			return errors.New("repository instruction file count exceeds its preparation limit")
		}
		p.sources = append(p.sources, project.InstructionSource{Path: path, Content: content})
		return nil
	}
	for _, dir := range dirs {
		for _, name := range project.InstructionNames {
			path := filepath.Join(dir, name)
			content, present, err := read(path)
			if err != nil {
				return nil, fmt.Errorf("prepare instructions %q: %w", path, err)
			}
			if !present {
				continue
			}
			if err := add(path, content); err != nil {
				return nil, err
			}
			break
		}
		for _, sub := range project.RuleDirs {
			ruleDir := filepath.Join(dir, sub)
			abs, err := root.ResolveExact(ruleDir)
			if err != nil {
				return nil, err
			}
			file, err := root.Open(abs)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			var names []string
			count := 0
			for {
				entries, readErr := file.ReadDir(32)
				count += len(entries)
				if count > repoRuleEntries {
					return nil, errors.Join(errors.New("repository rule directory exceeds its bounded preparation limit"), file.Close())
				}
				for _, entry := range entries {
					if entry.Type().IsRegular() && (filepath.Ext(entry.Name()) == ".md" || filepath.Ext(entry.Name()) == ".mdc") {
						names = append(names, entry.Name())
					}
				}
				if readErr != nil {
					if !errors.Is(readErr, io.EOF) {
						return nil, errors.Join(readErr, file.Close())
					}
					break
				}
				if err := ctx.Err(); err != nil {
					return nil, errors.Join(err, file.Close())
				}
			}
			if err := file.Close(); err != nil {
				return nil, err
			}
			sort.Strings(names)
			for _, name := range names {
				path := filepath.Join(ruleDir, name)
				content, present, err := read(path)
				if err != nil {
					return nil, err
				}
				if !present {
					return nil, workspace.ErrChanged
				}
				rule, _ := project.ParseRule(path, dir, content)
				if rule.Conditional() && p.target != "." && !rule.Matches(lease.Path) && !rule.Matches(canonical) {
					continue
				}
				if err := add(path, content); err != nil {
					return nil, err
				}
			}
		}
	}
	hash := sha256.New()
	var frame [8]byte
	write := func(value string) {
		binary.BigEndian.PutUint64(frame[:], uint64(len(value)))
		_, _ = hash.Write(frame[:])
		_, _ = hash.Write([]byte(value))
	}
	write(info.ID)
	write(p.target)
	binary.BigEndian.PutUint64(frame[:], lease.MembershipRevision)
	_, _ = hash.Write(frame[:])
	for _, source := range p.sources {
		write(source.Path)
		write(source.Content)
	}
	copy(p.fingerprint[:], hash.Sum(nil))
	if err := lease.Publish(func() error { return nil }); err != nil {
		return nil, err
	}
	return p, nil
}

func canonicalInstructionPath(root tool.Root, path string) (string, error) {
	existing := path
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", os.ErrNotExist
		}
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	tail, err := filepath.Rel(existing, path)
	if err != nil {
		return "", err
	}
	return root.ResolveExact(filepath.Join(real, tail))
}

type repoPreamble struct{ session *rpcSession }

func (s *rpcSession) workspacePreamble() agent.Preamble {
	if s.workspace == nil {
		return s.pre
	}
	return &repoPreamble{session: s}
}

func (p *repoPreamble) Prefix(ctx context.Context, history []agentapi.Message) ([]agentapi.Message, error) {
	s := p.session
	base, err := s.pre.Prefix(ctx, history)
	if err != nil {
		return nil, err
	}
	s.repoInstructions.mu.Lock()
	prepared := s.repoInstructions.prepared
	s.repoInstructions.mu.Unlock()
	if prepared == nil {
		return base, nil
	}
	current, err := s.loadRepoInstructions(ctx, prepared.root.ID, prepared.target)
	if err != nil {
		s.repoInstructions.mu.Lock()
		s.repoInstructions.delivered = [sha256.Size]byte{}
		s.repoInstructions.mu.Unlock()
		base[0] = agentapi.SystemMessage(base[0].Text() + "\nThe prepared secondary instruction scope is unavailable. Secondary mutations are disabled; call repo_prepare again after addressing the root or instruction failure.\n")
		// The primary root remains usable; the explicit prompt notice and
		// revoked fingerprint let the agent recover through repo_prepare.
		return base, nil //nolint:nilerr
	}
	s.agMu.Lock()
	window := s.agLimits.MaxContextTokens
	s.agMu.Unlock()
	budget := min(4096, max(1024, project.InstructionBudgetFor(window)/2))
	text, err := current.prompt(ctx, budget)
	if err != nil {
		return nil, err
	}
	base[0] = agentapi.SystemMessage(base[0].Text() + text)
	s.repoInstructions.mu.Lock()
	if s.repoInstructions.prepared == prepared {
		s.repoInstructions.prepared = current
		s.repoInstructions.delivered = current.fingerprint
	}
	s.repoInstructions.mu.Unlock()
	return base, nil
}

// prompt shares one bounded rendered envelope across all scoped documents.
// Full source documents remain available through the instructions tool.
func (p *repoPreparation) prompt(ctx context.Context, budget int) (string, error) {
	var content strings.Builder
	for _, source := range p.sources {
		id := workspace.EntryID(p.root.ID, strings.TrimPrefix(source.Path, p.root.Path+string(filepath.Separator)))
		display := hinttext.Cut(strings.TrimPrefix(source.Path, p.root.Path+string(filepath.Separator)), 128)
		fmt.Fprintf(&content, "## %s %s\n%s\n\n", id, strconv.Quote(display), source.Content)
	}
	path := "repo:" + p.root.ID
	header := fmt.Sprintf("\nTrusted instructions for root %s and target %s. Read full documents with instructions using this repo and the document ID as path.\n", p.root.ID, workspace.EntryID(p.root.ID, p.target))
	overhead := len(project.RenderInstructions([]project.Instruction{{Path: path, Size: content.Len(), Outlined: true, Truncated: true}}))
	available := budget - len(header) - overhead
	if available < 64 {
		return "", errors.New("secondary instruction envelope cannot fit the context budget")
	}
	instructions, _ := project.LayoutSources(ctx, []project.InstructionSource{{Path: path, Content: content.String()}}, available)
	rendered := header + project.RenderInstructions(instructions)
	if len(rendered) > budget {
		return "", errors.New("secondary instruction envelope exceeded its context budget")
	}
	return rendered, nil
}

func (s *rpcSession) requireRepoInstructions(ctx context.Context, info workspace.RootInfo, target string) error {
	if info.Primary {
		return nil
	}
	current, err := s.loadRepoInstructions(ctx, info.ID, target)
	if err != nil {
		return err
	}
	s.repoInstructions.mu.Lock()
	defer s.repoInstructions.mu.Unlock()
	if s.repoInstructions.prepared == nil || s.repoInstructions.prepared.root.ID != current.root.ID || s.repoInstructions.prepared.target != current.target || s.repoInstructions.delivered != current.fingerprint {
		return fmt.Errorf("call repo_prepare with repo %q and path %q in a preceding model round before this mutation; instructions are missing or changed", info.Alias, current.target)
	}
	return nil
}

type repoInstructionTool struct {
	agentapi.Tool
	session *rpcSession
}

func (t *repoInstructionTool) InputSchema() json.RawMessage {
	var schema map[string]any
	if err := json.Unmarshal(t.Tool.InputSchema(), &schema); err != nil {
		panic(err)
	}
	schema["properties"].(map[string]any)["repo"] = map[string]any{"type": "string", "description": "Prepared secondary root ID/alias; omission reads primary instructions."}
	data, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return data
}

func (t *repoInstructionTool) Run(ctx context.Context, id string, raw json.RawMessage) (agentapi.ToolResult, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return tool.InvalidArgs(id, t.Name(), err), nil
	}
	var repo string
	if err := json.Unmarshal(fields["repo"], &repo); err != nil && fields["repo"] != nil {
		return tool.InvalidArgs(id, t.Name(), err), nil
	}
	delete(fields, "repo")
	args, err := json.Marshal(fields)
	if err != nil {
		return tool.InvalidArgs(id, t.Name(), err), nil
	}
	if repo == "" {
		return t.Tool.Run(ctx, id, args)
	}
	_, info, err := t.session.workspace.ToolRoot(repo)
	if err != nil {
		return agentapi.ErrorResult(id, t.Name(), err.Error()), nil
	}
	if info.Primary {
		return t.Tool.Run(ctx, id, args)
	}
	t.session.repoInstructions.mu.Lock()
	prepared := t.session.repoInstructions.prepared
	t.session.repoInstructions.mu.Unlock()
	if prepared == nil || prepared.root.ID != info.ID {
		return agentapi.ErrorResult(id, t.Name(), "call repo_prepare for this root and target first"), nil
	}
	current, err := t.session.loadRepoInstructions(ctx, info.ID, prepared.target)
	if err != nil {
		return agentapi.ErrorResult(id, t.Name(), err.Error()), nil
	}
	paths := make([]string, len(current.sources))
	for i, source := range current.sources {
		paths[i] = source.Path
	}
	var named string
	if fields["path"] != nil && json.Unmarshal(fields["path"], &named) == nil {
		for _, source := range current.sources {
			if named == workspace.EntryID(info.ID, strings.TrimPrefix(source.Path, info.Path+string(filepath.Separator))) {
				fields["path"], _ = json.Marshal(source.Path)
				args, err = json.Marshal(fields)
				if err != nil {
					return tool.InvalidArgs(id, t.Name(), err), nil
				}
				break
			}
		}
	}
	return builtin.NewInstructions(paths, current.sources...).Run(ctx, id, args)
}
