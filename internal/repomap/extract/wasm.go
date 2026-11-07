package extract

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/zema1/wasitter"
)

// These release artifacts include the upstream runtime and its matching bridge.
// They are pinned, checksummed and licensed in grammars/; no runtime downloads occur.
//
//go:embed grammars/wasitter-*.wasm grammars/THIRD_PARTY_NOTICES.txt grammars/grammar-registry.json
var grammarModules embed.FS

func nonGoPolicy(language string) string {
	switch language {
	case "python", "javascript":
		return "wasitter-0.1.0-" + language + "-0.25.0-declarations-v1"
	case "typescript", "tsx":
		return "wasitter-0.1.0-" + language + "-0.23.2-declarations-v1"
	case "rust":
		return "wasitter-0.1.0-rust-0.24.2-declarations-v1"
	default:
		return ""
	}
}

type wasmSession struct {
	name     string
	runtime  *wasitter.Runtime
	language *wasitter.Language
	parser   *wasitter.Parser
	cancel   context.CancelFunc
}

func (e *Engine) initializeWASM(ctx context.Context, language string) (resultErr error) {
	if e.wasm != nil && e.wasm.name == language {
		return nil
	}
	if err := e.Close(); err != nil {
		return fmt.Errorf("close previous grammar: %w", err)
	}
	module, err := grammarModules.ReadFile("grammars/wasitter-" + language + ".wasm")
	if err != nil {
		return fmt.Errorf("load pinned grammar: %w", err)
	}
	// Retain one runtime only; initialization cancellation must not tie its later
	// lifetime to the first request's context.
	lifetime, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	r, err := wasitter.NewRuntimeWithOptions(lifetime, module, wasitter.RuntimeOptions{
		RuntimeConfig: wazero.NewRuntimeConfig().WithMemoryLimitPages(1024).WithCloseOnContextDone(true),
	})
	if err != nil {
		cancel()
		return fmt.Errorf("initialize grammar runtime: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, r.Close())
			cancel()
		}
	}()
	l, err := r.LoadLanguage(language)
	if err != nil {
		return fmt.Errorf("load grammar language: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, l.Close())
		}
	}()
	p, err := wasitter.NewParserWithRuntime(r)
	if err != nil {
		return fmt.Errorf("create grammar parser: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, p.Close())
		}
	}()
	if err := p.SetLanguage(l); err != nil {
		return fmt.Errorf("set grammar language: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("initialize grammar: %w", err)
	}
	e.wasm = &wasmSession{name: language, runtime: r, language: l, parser: p, cancel: cancel}
	return nil
}

func (e *Engine) extractNonGo(source Source, language string, b *builder) (resultErr error) {
	ctx, cancel := context.WithTimeout(b.ctx, 2*time.Second)
	defer cancel()
	if err := e.initializeWASM(ctx, language); err != nil {
		b.omission = wasmOmission(ctx)
		return nil
	}
	defer func() {
		// Failed or canceled guest executions cannot safely reenter the cache.
		if ctx.Err() != nil || resultErr != nil || b.omission == "parse_failed" {
			resultErr = errors.Join(resultErr, e.Close())
		}
	}()
	tree, err := e.wasm.parser.ParseContext(ctx, source.Content, nil)
	if err != nil {
		b.omission = wasmOmission(ctx)
		return nil
	}
	defer func() { resultErr = errors.Join(resultErr, tree.Close()) }()
	root := tree.RootNode()
	if root.IsNull() || root.HasError() || root.IsMissing() {
		b.omission = "parse_failed"
		return nil
	}
	if err := walkWASM(root, source.Content, "", false, b, 0); err != nil {
		b.omission = wasmOmission(ctx)
		return nil
	}
	if ctx.Err() != nil {
		b.omission = "deadline_exceeded"
	}
	return nil
}

func wasmOmission(ctx context.Context) string {
	if ctx.Err() != nil {
		return "deadline_exceeded"
	}
	return "parse_failed"
}

// walkWASM visits declarations and containers, deliberately excluding executable
// bodies and initializers; this also avoids one host call per expression node.
func walkWASM(n wasitter.Node, source []byte, enclosing string, members bool, b *builder, depth int) error {
	if !b.step() {
		return nil
	}
	if depth > 256 {
		b.omission = "work_budget_exceeded"
		return nil
	}
	typ, err := n.TypeE()
	if err != nil {
		return fmt.Errorf("read node type: %w", err)
	}
	kind, container := declarationKind(typ)
	name := n.ChildByFieldName("name")
	if typ == "impl_item" {
		name = n.ChildByFieldName("type")
	}
	if kind != "" && !name.IsNull() {
		start, end := int(n.StartByte()), int(n.EndByte())
		if start < 0 || end < start || end > len(source) {
			return errors.New("invalid declaration range")
		}
		body := n.ChildByFieldName("body")
		headerEnd := end
		if !body.IsNull() {
			headerEnd = int(body.StartByte())
		}
		if headerEnd < start || headerEnd > end {
			return errors.New("invalid declaration header range")
		}
		if typ == "variable_declarator" {
			value := n.ChildByFieldName("value")
			if !value.IsNull() {
				headerEnd = int(value.StartByte())
				if value.Type() == "arrow_function" || value.Type() == "function_expression" || value.Type() == "generator_function" {
					kind = "function"
					valueBody := value.ChildByFieldName("body")
					if !valueBody.IsNull() {
						headerEnd = int(valueBody.StartByte())
					}
				}
			}
		}
		if typ == "const_item" || typ == "static_item" || typ == "public_field_definition" {
			value := n.ChildByFieldName("value")
			if !value.IsNull() {
				headerEnd = int(value.StartByte())
			}
		}
		sig, err := sanitizedHeader(n, source, start, headerEnd, b, 0)
		if err != nil {
			return err
		}
		d := Decl{Kind: kind, Name: name.Content(source), Signature: strings.TrimSpace(strings.TrimRight(sig, " \t\r\n=;")), Enclosing: enclosing,
			StartByte: start, EndByte: end, StartLine: int(n.StartPoint().Row) + 1, EndLine: int(n.EndPoint().Row) + 1}
		if kind == "function" && members {
			d.Kind = "method"
		}
		b.add(d)
		if !container {
			return nil
		}
		enclosing = name.Content(source)
		members = kind == "class" || kind == "interface"
	} else if typ == "impl_item" && !name.IsNull() {
		enclosing = name.Content(source)
		members = true
	}
	// Only syntactic containers can contain declarations in this policy.
	if !container && !declarationContainer(typ) {
		return nil
	}
	count, err := n.NamedChildCountE()
	if err != nil {
		return fmt.Errorf("read node children: %w", err)
	}
	for i := 0; i < count; i++ {
		if b.omission != "" || b.ctx.Err() != nil {
			return b.ctx.Err()
		}
		child := n.NamedChild(i)
		if child.IsNull() {
			return errors.New("missing enumerated syntax child")
		}
		if err := walkWASM(child, source, enclosing, members, b, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func declarationKind(typ string) (kind string, container bool) {
	switch typ {
	case "function_definition", "function_declaration", "generator_function_declaration", "function_item", "function_signature_item":
		return "function", false
	case "method_definition", "method_signature", "abstract_method_signature":
		return "method", false
	case "class_definition", "class_declaration", "abstract_class_declaration":
		return "class", true
	case "interface_declaration", "trait_item":
		return "interface", true
	case "struct_item", "enum_item", "enum_declaration", "type_alias_declaration", "type_item":
		return "type", false
	case "variable_declarator", "public_field_definition":
		return "variable", false
	case "const_item", "static_item":
		return "constant", false
	case "mod_item", "internal_module":
		return "module", true
	default:
		return "", false
	}
}

func declarationContainer(typ string) bool {
	switch typ {
	case "program", "module", "source_file", "block", "statement_block", "class_body", "interface_body", "declaration_list", "impl_item", "export_statement", "ambient_declaration", "lexical_declaration", "variable_declaration", "decorated_definition":
		return true
	default:
		return false
	}
}

// sanitizedHeader removes nested callable bodies from default arguments while
// preserving the surrounding signature; even a header can contain executable code.
func sanitizedHeader(n wasitter.Node, source []byte, start, end int, b *builder, depth int) (string, error) {
	if end < start || end > len(source) {
		return "", errors.New("invalid signature range")
	}
	if end-start > b.limits.MaxOutputBytes {
		b.omission = "output_budget_exceeded"
		return "", nil
	}
	if depth > 256 || !b.step() {
		b.omission = "work_budget_exceeded"
		return "", nil
	}
	var out strings.Builder
	position := start
	count, err := n.NamedChildCountE()
	if err != nil {
		return "", fmt.Errorf("read signature children: %w", err)
	}
	for i := 0; i < count; i++ {
		child := n.NamedChild(i)
		if child.IsNull() {
			return "", errors.New("missing signature syntax child")
		}
		cs, ce := int(child.StartByte()), int(child.EndByte())
		if cs >= end {
			break
		}
		if cs < position {
			continue
		}
		if ce > end {
			ce = end
		}
		out.Write(source[position:cs])
		typ := child.Type()
		body := child.ChildByFieldName("body")
		if (typ == "arrow_function" || typ == "function_expression" || typ == "lambda" || typ == "closure_expression") && !body.IsNull() && int(body.StartByte()) < ce {
			bs, be := int(body.StartByte()), int(body.EndByte())
			if bs < cs || be > ce {
				return "", errors.New("invalid nested callable body")
			}
			prefix, err := sanitizedHeader(child, source, cs, bs, b, depth+1)
			if err != nil {
				return "", err
			}
			out.WriteString(prefix)
			out.WriteString("…")
			out.Write(source[be:ce])
		} else {
			s, err := sanitizedHeader(child, source, cs, ce, b, depth+1)
			if err != nil {
				return "", err
			}
			out.WriteString(s)
		}
		position = ce
	}
	out.Write(source[position:end])
	return out.String(), nil
}
