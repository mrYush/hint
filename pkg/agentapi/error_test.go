package agentapi_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

// errorKindRouting is the routing table the WP0.3 router and the WP0.4 agent
// loop branch on. It is pinned here rather than re-derived at each call site,
// and TestErrorKindRoutingIsExhaustive checks that no kind is missing from it.
var errorKindRouting = []struct {
	kind         agentapi.ErrorKind
	retryable    bool
	fallbackable bool
}{
	{agentapi.ErrNetwork, true, true},
	{agentapi.ErrTimeout, true, true},
	{agentapi.ErrRateLimited, true, true},
	{agentapi.ErrUnavailable, true, true},
	{agentapi.ErrAuth, false, true},
	{agentapi.ErrModelNotFound, false, true},
	{agentapi.ErrInvalidRequest, false, false},
	{agentapi.ErrContextOverflow, false, false},
	{agentapi.ErrContentFiltered, false, false},
	{agentapi.ErrCanceled, false, false},
	{agentapi.ErrUnknown, false, false},
}

func TestErrorKindRouting(t *testing.T) {
	for _, c := range errorKindRouting {
		t.Run(string(c.kind), func(t *testing.T) {
			if got := c.kind.Retryable(); got != c.retryable {
				t.Errorf("Retryable() = %v, want %v", got, c.retryable)
			}
			if got := c.kind.Fallbackable(); got != c.fallbackable {
				t.Errorf("Fallbackable() = %v, want %v", got, c.fallbackable)
			}
		})
	}
}

// TestErrorKindRoutingIsExhaustive fails if a kind is declared in error.go
// without a row in the routing table above.
//
// Go cannot enumerate the values of a string enum at run time, and its
// compiler has no exhaustiveness check, so the kinds are read out of the
// package's own source with go/ast. The exhaustive linter catches the same
// mistake, but only when someone runs it; this keeps the guarantee inside
// `go test`, where an unclassified kind would otherwise default to "give up"
// — and for a fallback-worthy failure that means offline mode quietly stops
// working with nothing turning red.
func TestErrorKindRoutingIsExhaustive(t *testing.T) {
	declared := declaredConstants(t, "error.go", "ErrorKind")
	if len(declared) == 0 {
		t.Fatal("found no ErrorKind constants; the source scan is broken, not the code")
	}

	classified := make(map[string]bool, len(errorKindRouting))
	for _, c := range errorKindRouting {
		classified[string(c.kind)] = true
	}
	for name, value := range declared {
		if !classified[value] {
			t.Errorf("%s (%q) has no row in errorKindRouting; decide its Retryable/Fallbackable", name, value)
		}
		if !agentapi.ErrorKind(value).Valid() {
			t.Errorf("%s (%q) is declared but ErrorKind.Valid() rejects it", name, value)
		}
	}
	if len(errorKindRouting) != len(declared) {
		t.Errorf("routing table has %d rows, %d kinds declared", len(errorKindRouting), len(declared))
	}

	// An undefined kind must be rejected rather than silently treated as a
	// known one.
	for _, k := range []agentapi.ErrorKind{"", "quota", "Network"} {
		if k.Valid() {
			t.Errorf("%q must not be a valid kind", k)
		}
	}
}

// declaredConstants parses one file of the package under test and returns the
// name-to-value mapping of its string constants of the given type.
//
// Reading the source is unusual in a test, but it is the only way to make an
// enum check that a newly added constant cannot slip past: any hand-written
// list of kinds is exactly the thing that gets forgotten.
func declaredConstants(t *testing.T, filename, typeName string) map[string]string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", filename, err)
	}

	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != typeName {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquoting %s: %v", lit.Value, err)
				}
				out[name.Name] = value
			}
		}
	}
	return out
}

func TestErrorWrapping(t *testing.T) {
	cause := fmt.Errorf("dial tcp 127.0.0.1:11434: %w", io.EOF)
	err := agentapi.WrapError(agentapi.ErrNetwork, cause, "reaching ollama")

	if !errors.Is(err, io.EOF) {
		t.Error("errors.Is must reach through the wrapped cause")
	}
	if got, want := agentapi.KindOf(err), agentapi.ErrNetwork; got != want {
		t.Errorf("KindOf() = %q, want %q", got, want)
	}
	if !err.Retryable() || !err.Fallbackable() {
		t.Error("a network failure must be both retryable and fallbackable")
	}

	// The cause's text is folded into the message so that the wire form,
	// which cannot carry the chain, stays informative.
	if got := err.Error(); got == "" || !strings.Contains(got, "EOF") {
		t.Errorf("Error() = %q, want it to mention the cause", got)
	}

	// And it survives one more level of wrapping by a caller.
	outer := fmt.Errorf("turn failed: %w", err)
	if got, want := agentapi.KindOf(outer), agentapi.ErrNetwork; got != want {
		t.Errorf("KindOf(wrapped) = %q, want %q", got, want)
	}
}

func TestKindOfClassifiesContext(t *testing.T) {
	cases := map[string]struct {
		err  error
		want agentapi.ErrorKind
	}{
		"nil":               {nil, ""},
		"canceled":          {context.Canceled, agentapi.ErrCanceled},
		"deadline exceeded": {context.DeadlineExceeded, agentapi.ErrTimeout},
		"wrapped canceled":  {fmt.Errorf("stream: %w", context.Canceled), agentapi.ErrCanceled},
		"foreign":           {io.ErrUnexpectedEOF, agentapi.ErrUnknown},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := agentapi.KindOf(c.err); got != c.want {
				t.Errorf("KindOf() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestErrorIsMatchesByKind(t *testing.T) {
	err := agentapi.NewError(agentapi.ErrRateLimited, "429 from api-bar")

	if !errors.Is(err, agentapi.NewError(agentapi.ErrRateLimited, "any text")) {
		t.Error("errors.Is must match on kind, ignoring the message")
	}
	if errors.Is(err, agentapi.NewError(agentapi.ErrAuth, "")) {
		t.Error("errors.Is must not match a different kind")
	}
	if errors.Is(err, io.EOF) {
		t.Error("errors.Is must not match a foreign error")
	}
}

func TestErrorWithProviderCopies(t *testing.T) {
	base := agentapi.NewError(agentapi.ErrTimeout, "deadline exceeded")
	attributed := base.WithProvider("api-bar")

	if base.Provider != "" {
		t.Error("WithProvider must not mutate the receiver")
	}
	if attributed.Provider != "api-bar" {
		t.Errorf("Provider = %q, want %q", attributed.Provider, "api-bar")
	}
	if !strings.Contains(attributed.Error(), "api-bar") {
		t.Errorf("Error() = %q, want it to name the provider", attributed.Error())
	}
}

func TestNilErrorIsSafe(t *testing.T) {
	// A *Error is carried in event structs where it is nil most of the time;
	// the predicates must not panic on it.
	var e *agentapi.Error
	if e.Retryable() || e.Fallbackable() {
		t.Error("a nil *Error must be neither retryable nor fallbackable")
	}
	if e.Unwrap() != nil {
		t.Error("a nil *Error must unwrap to nil")
	}
	if e.WithProvider("x") != nil {
		t.Error("WithProvider on a nil *Error must stay nil")
	}
	_ = e.Error()
}
