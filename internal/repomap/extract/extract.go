// Package extract produces bounded declaration maps from source bytes.
// Parsing must run in a supervised worker: context cancellation is cooperative,
// and does not preempt the standard Go parser or every grammar implementation.
package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// Source is an already authorized, complete file observation.
type Source struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// Limits bound admitted input, serialized result bytes, declarations and visited nodes.
// The caller additionally enforces a process deadline and memory reservation.
type Limits struct {
	MaxInputBytes   int `json:"max_input_bytes"`
	MaxOutputBytes  int `json:"max_output_bytes"`
	MaxDeclarations int `json:"max_declarations"`
	MaxWorkUnits    int `json:"max_work_units"`
}

// DefaultLimits returns the initial per-file extraction profile.
func DefaultLimits() Limits {
	return Limits{MaxInputBytes: 1 << 20, MaxOutputBytes: 256 << 10, MaxDeclarations: 4096, MaxWorkUnits: 200000}
}

// Decl identifies a declaration in the observed source, without its function body.
type Decl struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Signature string `json:"signature"`
	Enclosing string `json:"enclosing,omitempty"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// Result is complete only when the entire admitted source was extracted.
// An omission returns no declarations, so callers cannot publish partial coverage.
type Result struct {
	Declarations []Decl `json:"declarations"`
	Complete     bool   `json:"complete"`
	Omission     string `json:"omission,omitempty"`
	WorkUnits    int    `json:"work_units"`
}

// Language identifies explicitly supported source extensions.
func Language(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py", ".pyi":
		return "python"
	case ".js", ".mjs", ".cjs", ".jsx":
		return "javascript"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".rs":
		return "rust"
	default:
		return ""
	}
}

// PolicyVersion includes the adapter and extraction policy used for cache identity.
func PolicyVersion(language string) string {
	if language == "go" {
		return "go-ast-" + runtime.Version() + "-declarations-v1"
	}
	if language != "" {
		return nonGoPolicy(language)
	}
	return ""
}

// Extract returns declarations or an explicit all-or-nothing omission.
// MaxOutputBytes counts the actual JSON serialization of Result, not signatures alone.
func Extract(ctx context.Context, source Source, limits Limits) (Result, error) {
	e := &Engine{}
	result, err := e.Extract(ctx, source, limits)
	return result, errors.Join(err, e.Close())
}

// Engine reuses at most one non-Go parser/runtime between sequential jobs.
// It is not safe for concurrent calls; the supervised worker serializes access.
// Close releases cached guest memory and is safe to call repeatedly.
type Engine struct {
	wasm *wasmSession
}

// Close releases the cached runtime and permits subsequent reuse of the Engine.
func (e *Engine) Close() error {
	if e.wasm == nil {
		return nil
	}
	s := e.wasm
	e.wasm = nil
	err := errors.Join(s.parser.Close(), s.language.Close(), s.runtime.Close())
	s.cancel()
	return err
}

// Extract uses the same bounded, all-or-nothing contract as the package function.
func (e *Engine) Extract(ctx context.Context, source Source, limits Limits) (Result, error) {
	if limits.MaxInputBytes <= 0 || limits.MaxInputBytes > 8<<20 ||
		limits.MaxOutputBytes < 256 || limits.MaxOutputBytes > 4<<20 ||
		limits.MaxDeclarations <= 0 || limits.MaxDeclarations > 65536 ||
		limits.MaxWorkUnits <= 0 || limits.MaxWorkUnits > 2000000 {
		return Result{}, errors.New("extract declarations: invalid limits")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("extract declarations: %w", err)
	}
	language := Language(source.Path)
	if language == "" {
		return Result{Omission: "unsupported"}, nil
	}
	if len(source.Content) > limits.MaxInputBytes {
		return Result{Omission: "oversized"}, nil
	}
	b := &builder{ctx: ctx, limits: limits}
	var err error
	if language == "go" {
		err = extractGo(source, b)
	} else {
		err = e.extractNonGo(source, language, b)
	}
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("extract declarations: %w", ctx.Err())
	}
	if err != nil {
		return Result{}, fmt.Errorf("extract declarations: %w", err)
	}
	if b.omission != "" {
		return Result{Omission: b.omission, WorkUnits: b.work}, nil
	}
	result := Result{Declarations: b.decls, Complete: true, WorkUnits: b.work}
	data, err := json.Marshal(result)
	if err != nil {
		return Result{}, fmt.Errorf("encode extraction result: %w", err)
	}
	if len(data) > limits.MaxOutputBytes {
		return Result{Omission: "output_budget_exceeded", WorkUnits: b.work}, nil
	}
	return result, nil
}

type builder struct {
	ctx      context.Context
	limits   Limits
	decls    []Decl
	work     int
	bytes    int
	omission string
}

func (b *builder) step() bool {
	if b.omission != "" || b.ctx.Err() != nil {
		return false
	}
	if b.work == b.limits.MaxWorkUnits {
		b.omission = "work_budget_exceeded"
		return false
	}
	b.work++
	return true
}

func (b *builder) add(d Decl) {
	if b.omission != "" {
		return
	}
	if len(b.decls) == b.limits.MaxDeclarations {
		b.omission = "work_budget_exceeded"
		return
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		b.omission = "parse_failed"
		return
	}
	b.bytes += len(encoded) + 1
	if b.bytes > b.limits.MaxOutputBytes {
		b.omission = "output_budget_exceeded"
		return
	}
	b.decls = append(b.decls, d)
}
