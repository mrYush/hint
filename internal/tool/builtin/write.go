package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrYush/hint/internal/diff"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

const writeFileName = "write_file"

const writeFileDescription = `Create or overwrite a file in the working directory with the given content.

Use it for new files or when a file needs to be rewritten wholesale; for a targeted change to an existing file prefer edit_file, which shows exactly what changed. Parent directories are created as needed. The write is atomic: the file is either fully replaced or untouched.`

type writeFileArgs struct {
	Path    string `json:"path" jsonschema_description:"Path of the file to write, relative to the working directory"`
	Content string `json:"content" jsonschema_description:"The complete new content of the file"`
}

var writeFileSchema = tool.MustSchema(writeFileArgs{})

type writeFile struct {
	root tool.Root
}

func newWriteFile(root tool.Root, o options) agentapi.Tool {
	return tool.WithLimits(&writeFile{root: root}, o.limits)
}

func (*writeFile) Name() string                 { return writeFileName }
func (*writeFile) Description() string          { return writeFileDescription }
func (*writeFile) InputSchema() json.RawMessage { return writeFileSchema }
func (*writeFile) Class() agentapi.ActionClass  { return agentapi.ClassWrite }

// Run implements agentapi.Tool.
func (t *writeFile) Run(_ context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in writeFileArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.InvalidArgs(callID, writeFileName, err), nil
	}
	if strings.TrimSpace(in.Path) == "" {
		return tool.InvalidArgs(callID, writeFileName, errors.New("path is required")), nil
	}

	abs, err := t.root.Resolve(in.Path)
	if err != nil {
		return agentapi.ErrorResult(callID, writeFileName, err.Error()), nil
	}
	rel := t.root.Rel(abs)

	var previous int64 = -1
	if info, err := os.Stat(abs); err == nil {
		if info.IsDir() {
			return agentapi.ErrorResult(callID, writeFileName, fmt.Sprintf("%s is a directory", rel)), nil
		}
		previous = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return agentapi.ErrorResult(callID, writeFileName, pathError(t.root, abs, err)), nil
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return agentapi.ErrorResult(callID, writeFileName, pathError(t.root, filepath.Dir(abs), err)), nil
	}
	if err := writeAtomic(abs, []byte(in.Content), 0o644); err != nil {
		return agentapi.ErrorResult(callID, writeFileName, pathError(t.root, abs, err)), nil
	}

	if previous < 0 {
		return agentapi.TextResult(callID, writeFileName, fmt.Sprintf("Created %s (%d bytes)", rel, len(in.Content))), nil
	}
	return agentapi.TextResult(callID, writeFileName,
		fmt.Sprintf("Overwrote %s (%d bytes, was %d)", rel, len(in.Content), previous)), nil
}

// Describe implements tool.Describer: the unified diff between the file as
// it is and the content the call would write, without writing anything.
// A binary or oversized current file gets a size-only summary — a diff of
// it would be noise — but the write is still previewed as a write.
func (t *writeFile) Describe(_ context.Context, args json.RawMessage) (tool.Description, error) {
	var in writeFileArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.Description{}, err
	}
	if strings.TrimSpace(in.Path) == "" {
		return tool.Description{}, errors.New("path is required")
	}
	abs, err := t.root.Resolve(in.Path)
	if err != nil {
		return tool.Description{}, err
	}
	rel := t.root.Rel(abs)

	data, info, err := readWhole(abs, maxEditSize)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return tool.Description{
			Summary: fmt.Sprintf("create %s (%d bytes)", rel, len(in.Content)),
			Detail:  diff.Unified(rel, "", in.Content),
			Path:    rel,
		}, nil
	case err != nil && info != nil && !info.IsDir():
		// Too large to diff: still a legitimate overwrite to confirm.
		return tool.Description{
			Summary: fmt.Sprintf("overwrite %s (%d bytes, was %d; too large to diff)", rel, len(in.Content), info.Size()),
			Path:    rel,
		}, nil
	case err != nil:
		return tool.Description{}, fmt.Errorf("%s: %w", rel, err)
	}
	if isBinaryData(data) {
		return tool.Description{
			Summary: fmt.Sprintf("overwrite %s (%d bytes, was %d, binary)", rel, len(in.Content), len(data)),
			Path:    rel,
		}, nil
	}
	edits := diff.Lines(splitKeepNL(string(data)), splitKeepNL(in.Content))
	added, removed := diff.Stat(edits)
	return tool.Description{
		Summary: fmt.Sprintf("overwrite %s (+%d -%d lines)", rel, added, removed),
		Detail:  diff.Unified(rel, string(data), in.Content),
		Path:    rel,
	}, nil
}

// Touches implements tool.Toucher: the one file the call names.
func (t *writeFile) Touches(args json.RawMessage) []string {
	var in writeFileArgs
	if err := tool.DecodeArgs(args, &in); err != nil || in.Path == "" {
		return nil
	}
	abs, err := t.root.Resolve(in.Path)
	if err != nil {
		return nil
	}
	return []string{abs}
}
