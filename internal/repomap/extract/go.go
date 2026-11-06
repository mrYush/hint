package extract

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
)

func extractGo(source Source, b *builder) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, source.Path, source.Content, parser.SkipObjectResolution|parser.AllErrors)
	if err != nil {
		b.omission = "parse_failed"
		return nil
	}
	// Count every parsed node, including bodies, before accepting any declarations.
	ast.Inspect(file, func(n ast.Node) bool { return n != nil && b.step() })
	if b.omission != "" || b.ctx.Err() != nil {
		return nil
	}
	for _, declaration := range file.Decls {
		if !b.step() {
			break
		}
		switch n := declaration.(type) {
		case *ast.FuncDecl:
			sig := *n
			sig.Body = nil
			d := goDecl(fset, n, "function", n.Name.Name, file.Name.Name)
			if n.Recv != nil && len(n.Recv.List) > 0 {
				d.Kind = "method"
				d.Enclosing = goPrinted(fset, n.Recv.List[0].Type, b)
			}
			d.Signature = goPrinted(fset, &sig, b)
			b.add(d)
		case *ast.GenDecl:
			for _, spec := range n.Specs {
				if !b.step() {
					break
				}
				switch s := spec.(type) {
				case *ast.TypeSpec:
					d := goDecl(fset, s, "type", s.Name.Name, file.Name.Name)
					d.Signature = "type " + goPrinted(fset, s, b)
					b.add(d)
				case *ast.ValueSpec:
					kind := "variable"
					if n.Tok == token.CONST {
						kind = "constant"
					}
					for _, name := range s.Names {
						d := goDecl(fset, s, kind, name.Name, file.Name.Name)
						// Initializers may contain function bodies and are deliberately omitted.
						clean := &ast.ValueSpec{Names: []*ast.Ident{name}, Type: s.Type}
						d.Signature = n.Tok.String() + " " + goPrinted(fset, clean, b)
						b.add(d)
					}
				}
			}
		}
	}
	return nil
}

func goDecl(fset *token.FileSet, n ast.Node, kind, name, enclosing string) Decl {
	start, end := fset.Position(n.Pos()), fset.Position(n.End())
	return Decl{Kind: kind, Name: name, Enclosing: enclosing, StartByte: start.Offset, EndByte: end.Offset, StartLine: start.Line, EndLine: end.Line}
}

func goPrinted(fset *token.FileSet, node ast.Node, b *builder) string {
	w := &limitedBuffer{limit: b.limits.MaxOutputBytes}
	if err := printer.Fprint(w, fset, node); err != nil {
		b.omission = "output_budget_exceeded"
		return ""
	}
	return w.String()
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("signature exceeds output budget")
	}
	return b.Buffer.Write(p)
}
