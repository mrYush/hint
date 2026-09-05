package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
