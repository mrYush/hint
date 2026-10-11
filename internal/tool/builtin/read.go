package builtin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

const (
	readFileName = "read_file"
	// defaultReadLimit is how many lines one call returns when the model
	// does not say.
	defaultReadLimit = 2000
	// maxLineLength caps a single displayed line; minified assets and data
	// dumps have lines that would otherwise eat the whole output budget.
	maxLineLength = 2000
	// maxReadSize refuses files that would take too long to even count
	// the lines of.
	maxReadSize = 50 << 20
)

const readFileDescription = `Read a text file from the working directory and return its contents with line numbers.

Use it to look at source, configuration or documentation before answering or editing. Paths are relative to the working directory. Large files are paged: by default the first 2000 lines are returned, and the footer tells you the offset to continue from. Lines longer than 2000 characters are cut. Binary files are refused; use list_dir or glob to find files first.`

type readFileArgs struct {
	Path   string   `json:"path" jsonschema_description:"Path of the file to read, relative to the working directory"`
	Offset tool.Int `json:"offset,omitempty" jsonschema_description:"1-based line number to start from (default 1)"`
	Limit  tool.Int `json:"limit,omitempty" jsonschema_description:"Maximum number of lines to return (default 2000)"`
}

var readFileSchema = tool.MustSchema(readFileArgs{})

type readFile struct {
	root tool.Root
}

func newReadFile(root tool.Root, o options) agentapi.Tool {
	return tool.WithLimits(&readFile{root: root}, o.limits)
}

func (*readFile) Name() string                 { return readFileName }
func (*readFile) Description() string          { return readFileDescription }
func (*readFile) InputSchema() json.RawMessage { return readFileSchema }
func (*readFile) Class() agentapi.ActionClass  { return agentapi.ClassRead }

// Run implements agentapi.Tool.
func (t *readFile) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in readFileArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.InvalidArgs(callID, readFileName, err), nil
	}
	if strings.TrimSpace(in.Path) == "" {
		return tool.InvalidArgs(callID, readFileName, errors.New("path is required")), nil
	}
	if in.Offset < 0 || in.Limit < 0 {
		return tool.InvalidArgs(callID, readFileName, errors.New("offset and limit must not be negative")), nil
	}
	offset := max(int(in.Offset), 1)
	limit := int(in.Limit)
	if limit == 0 {
		limit = defaultReadLimit
	}

	abs, err := t.root.Resolve(in.Path)
	if err != nil {
		return agentapi.ErrorResult(callID, readFileName, err.Error()), nil
	}
	rel := t.root.Rel(abs)

	f, err := os.Open(abs)
	if err != nil {
		return agentapi.ErrorResult(callID, readFileName, pathError(t.root, abs, err)), nil
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return agentapi.ToolResult{}, fmt.Errorf("read_file: stat %s: %w", rel, err)
	}
	if info.IsDir() {
		return agentapi.ErrorResult(callID, readFileName, fmt.Sprintf("%s is a directory; use list_dir", rel)), nil
	}
	if info.Size() > maxReadSize {
		return agentapi.ErrorResult(callID, readFileName,
			fmt.Sprintf("%s is too large to read (%d bytes); use grep to search it", rel, info.Size())), nil
	}
	if bin, err := isBinary(f); err != nil {
		return agentapi.ToolResult{}, fmt.Errorf("read_file: probe %s: %w", rel, err)
	} else if bin {
		return agentapi.ErrorResult(callID, readFileName, fmt.Sprintf("%s is a binary file and cannot be shown as text", rel)), nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return agentapi.ToolResult{}, fmt.Errorf("read_file: rewind %s: %w", rel, err)
	}

	text, total, shown, err := numberLines(ctx, f, offset, limit)
	if err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return agentapi.ErrorResult(callID, readFileName, fmt.Sprintf("%s has a line too long to read", rel)), nil
		}
		if ctx.Err() != nil {
			return agentapi.ToolResult{}, ctx.Err()
		}
		return agentapi.ToolResult{}, fmt.Errorf("read_file: %s: %w", rel, err)
	}

	switch {
	case total == 0:
		return agentapi.TextResult(callID, readFileName, "(empty file)"), nil
	case shown == 0:
		return agentapi.ErrorResult(callID, readFileName,
			fmt.Sprintf("offset %d is past the end of %s (%d lines)", offset, rel, total)), nil
	}

	var out strings.Builder
	out.WriteString(text)
	last := offset + shown - 1
	if last < total {
		fmt.Fprintf(&out, "\n(showing lines %d-%d of %d; call again with offset=%d to continue)", offset, last, total, last+1)
	}
	return agentapi.TextResult(callID, readFileName, out.String()), nil
}

// numberLines renders lines [offset, offset+limit) of r in cat -n style and
// keeps counting to the end so the footer can report the total. It returns
// the rendered text, the file's line count, and how many lines were
// rendered.
func numberLines(ctx context.Context, r io.Reader, offset, limit int) (text string, total, shown int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)

	var b strings.Builder
	for sc.Scan() {
		total++
		if total < offset || shown >= limit {
			continue
		}
		if total%1000 == 0 && ctx.Err() != nil {
			return "", 0, 0, ctx.Err()
		}
		line := strings.TrimSuffix(sc.Text(), "\r")
		if len(line) > maxLineLength {
			line = line[:maxLineLength] + "... [line truncated]"
		}
		fmt.Fprintf(&b, "%6d\t%s\n", total, line)
		shown++
	}
	if err := sc.Err(); err != nil {
		return "", 0, 0, err
	}
	return b.String(), total, shown, nil
}

// Touches implements tool.Toucher: the one file the call names.
func (t *readFile) Touches(args json.RawMessage) []string {
	var in readFileArgs
	if err := tool.DecodeArgs(args, &in); err != nil || in.Path == "" {
		return nil
	}
	abs, err := t.root.Resolve(in.Path)
	if err != nil {
		return nil
	}
	return []string{abs}
}
