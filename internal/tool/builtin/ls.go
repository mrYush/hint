package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

const (
	listDirName = "list_dir"
	// maxListEntries caps a listing; beyond it the model is told to narrow
	// the path or use glob.
	maxListEntries = 500
)

const listDirDescription = `List the files and directories under a path in the working directory, recursively, as an indented tree.

Use it to get oriented in a project. Hidden entries (dot-files), dependency or build directories (node_modules, vendor, target, dist, __pycache__) and, inside a git repository, anything .gitignore excludes are skipped. Large trees are cut at 500 entries; list a subdirectory or use glob for a narrower view.`

type listDirArgs struct {
	Path string `json:"path,omitempty" jsonschema_description:"Directory to list, relative to the working directory (default: the working directory itself)"`
}

var listDirSchema = tool.MustSchema(listDirArgs{})

type listDir struct {
	root  tool.Root
	rules project.Rules
}

func newListDir(root tool.Root, o options) agentapi.Tool {
	return tool.WithLimits(&listDir{root: root, rules: o.rules()}, o.limits)
}

func (*listDir) Name() string                 { return listDirName }
func (*listDir) Description() string          { return listDirDescription }
func (*listDir) InputSchema() json.RawMessage { return listDirSchema }
func (*listDir) Class() agentapi.ActionClass  { return agentapi.ClassRead }

// Run implements agentapi.Tool.
func (t *listDir) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in listDirArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.InvalidArgs(callID, listDirName, err), nil
	}

	abs, err := t.root.Resolve(in.Path)
	if err != nil {
		return agentapi.ErrorResult(callID, listDirName, err.Error()), nil
	}
	rel := t.root.Rel(abs)

	info, err := os.Stat(abs)
	if err != nil {
		return agentapi.ErrorResult(callID, listDirName, pathError(t.root, abs, err)), nil
	}
	if !info.IsDir() {
		return agentapi.ErrorResult(callID, listDirName, fmt.Sprintf("%s is a file, not a directory; use read_file", rel)), nil
	}

	// git answering for the listed directory is best effort: when it
	// cannot, the walk keeps the hidden-and-dependency rule, which is what
	// it applied before .gitignore support. cmd/hint reports the same
	// failure once per run when it loads the project context.
	ig, _ := t.rules.Ignorer(ctx, abs)

	var b strings.Builder
	fmt.Fprintf(&b, "%s%c\n", rel, filepath.Separator)
	entries := 0
	truncated := false
	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if path == abs {
			return err
		}
		if err != nil {
			// An unreadable subtree is reported inline rather than
			// failing the whole listing.
			fmt.Fprintf(&b, "%s%s (unreadable)\n", indentFor(abs, path), filepath.Base(path))
			entries++
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ig.Ignored(relSlash(abs, path), d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entries >= maxListEntries {
			truncated = true
			return filepath.SkipAll
		}
		entries++
		name := d.Name()
		if d.IsDir() {
			name += string(filepath.Separator)
		}
		fmt.Fprintf(&b, "%s%s\n", indentFor(abs, path), name)
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, ctx.Err()) && ctx.Err() != nil {
			return agentapi.ToolResult{}, walkErr
		}
		return agentapi.ErrorResult(callID, listDirName, pathError(t.root, abs, walkErr)), nil
	}

	if entries == 0 {
		return agentapi.TextResult(callID, listDirName, fmt.Sprintf("%s%c\n(empty directory)", rel, filepath.Separator)), nil
	}
	if truncated {
		fmt.Fprintf(&b, "\n(listing cut at %d entries; list a subdirectory or use glob for a narrower view)", maxListEntries)
	}
	return agentapi.TextResult(callID, listDirName, b.String()), nil
}

// relSlash is path relative to base, slash-separated, as an Ignorer
// expects it.
func relSlash(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// indentFor returns two spaces per directory level of path below base.
func indentFor(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return "  "
	}
	depth := strings.Count(rel, string(filepath.Separator)) + 1
	return strings.Repeat("  ", depth)
}
