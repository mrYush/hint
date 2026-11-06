package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDeclarations(t *testing.T) {
	tests := []struct {
		path, source string
		names        []string
	}{
		{"sample.go", "package sample\ntype Store[T any] struct { Value T }\nfunc (s *Store[T]) Get(key string) T { panic(\"BODY_SENTINEL\") }\nfunc Open[T any](x T) T { return x }\nvar Fn = func() { panic(\"BODY_SENTINEL\") }\nconst Count = 1\n", []string{"Store", "Get", "Open", "Fn", "Count"}},
		{"sample.py", "class Store:\n    def get(self, key: str) -> str:\n        return 'BODY_SENTINEL'\n\ndef café(x: int = 1) -> int:\n    return x\n", []string{"Store", "get", "café"}},
		{"sample.js", "export function open(x) { return 'BODY_SENTINEL'; }\nclass Store { get(k) { return 'BODY_SENTINEL'; } }\nconst arrow = (x) => { return 'BODY_SENTINEL'; };\n", []string{"open", "Store", "get", "arrow"}},
		{"sample.ts", "interface Store { get(k: string): string; }\nexport function open<T>(x: T): T { throw 'BODY_SENTINEL'; }\ntype ID = string;\n", []string{"Store", "get", "open", "ID"}},
		{"sample.tsx", "export function View({name}: {name: string}) { return <div>BODY_SENTINEL</div>; }\n", []string{"View"}},
		{"sample.rs", "pub struct Store { value: String }\nimpl Store { pub fn get(&self) -> &str { \"BODY_SENTINEL\" } }\npub fn open(x: i32) -> i32 { x }\n", []string{"Store", "get", "open"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := Extract(context.Background(), Source{Path: tt.path, Content: []byte(tt.source)}, DefaultLimits())
			if err != nil || !got.Complete || got.Omission != "" {
				t.Fatalf("Extract = %+v, %v", got, err)
			}
			if len(got.Declarations) != len(tt.names) {
				t.Fatalf("declarations = %+v; want names %v", got.Declarations, tt.names)
			}
			for i, d := range got.Declarations {
				if d.Name != tt.names[i] || d.StartByte < 0 || d.EndByte > len(tt.source) || d.StartByte >= d.EndByte {
					t.Errorf("declaration %d: %+v", i, d)
				}
				if d.StartLine != 1+strings.Count(tt.source[:d.StartByte], "\n") || d.EndLine != 1+strings.Count(tt.source[:d.EndByte], "\n") {
					t.Errorf("incorrect line coordinates: %+v", d)
				}
				if strings.Contains(d.Signature, "BODY_SENTINEL") || d.Signature == "" {
					t.Errorf("body leaked or signature missing: %+v", d)
				}
				if !strings.Contains(tt.source[d.StartByte:d.EndByte], d.Name) {
					t.Errorf("range excludes name: %+v", d)
				}
			}
		})
	}
}

func TestSignatureAndEnclosing(t *testing.T) {
	for _, tt := range []struct{ path, source, name, signature, enclosing string }{
		{"x.go", "package p\nfunc (s *Store) Get(x int) string { return \"hidden\" }", "Get", "func (s *Store) Get(x int) string", "*Store"},
		{"x.py", "class Store:\n    def get(self, x: int) -> str:\n        return 'hidden'\n", "get", "def get(self, x: int) -> str:", "Store"},
		{"x.js", "class Store { get(x) { return 'hidden'; } }", "get", "get(x)", "Store"},
		{"x.rs", "struct Store; impl Store { pub fn get(&self) -> i32 { 1 } }", "get", "pub fn get(&self) -> i32", "Store"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			r, err := Extract(context.Background(), Source{tt.path, []byte(tt.source)}, DefaultLimits())
			if err != nil || !r.Complete {
				t.Fatalf("Extract = %+v, %v", r, err)
			}
			for _, d := range r.Declarations {
				if d.Name == tt.name {
					if d.Signature != tt.signature || d.Enclosing != tt.enclosing || d.Kind != "method" {
						t.Errorf("declaration = %+v", d)
					}
					return
				}
			}
			t.Fatalf("missing %s", tt.name)
		})
	}
}

func TestNestedCallableBodiesAreRedacted(t *testing.T) {
	for _, tt := range []struct{ path, source string }{
		{"x.js", "function open(callback = () => { return 'BODY_SENTINEL'; }) { return 'BODY_SENTINEL'; }"},
		{"x.ts", "const open = (callback = () => { return 'BODY_SENTINEL'; }) => { return 'BODY_SENTINEL'; };"},
		{"x.py", "def open(callback=lambda: 'BODY_SENTINEL'):\n    return 'BODY_SENTINEL'\n"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			r, err := Extract(context.Background(), Source{tt.path, []byte(tt.source)}, DefaultLimits())
			if err != nil || !r.Complete || len(r.Declarations) != 1 {
				t.Fatalf("Extract = %+v, %v", r, err)
			}
			if strings.Contains(r.Declarations[0].Signature, "BODY_SENTINEL") {
				t.Fatalf("nested body leaked: %s", r.Declarations[0].Signature)
			}
		})
	}
}

func TestMalformedNeverPublishesPartialDeclarations(t *testing.T) {
	for _, tt := range []struct{ path, source string }{
		{"x.go", "package p; func Good() {}\nfunc Bad("},
		{"x.py", "def good():\n    pass\ndef bad(:\n"},
		{"x.js", "function good() {} function bad( {"},
		{"x.ts", "function good(): void {} interface Bad { broken:"},
		{"x.tsx", "function Good() { return <div>; }"},
		{"x.rs", "fn good() {} fn bad( {"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			r, err := Extract(context.Background(), Source{tt.path, []byte(tt.source)}, DefaultLimits())
			if err != nil || r.Complete || r.Omission != "parse_failed" || len(r.Declarations) != 0 {
				t.Fatalf("Extract = %+v, %v", r, err)
			}
		})
	}
}

func TestLimits(t *testing.T) {
	for _, path := range []string{"x.go", "x.js"} {
		source := "function f() {} function g() {}"
		if path == "x.go" {
			source = "package p; func f() {}; func g() {}"
		}
		for _, tt := range []struct {
			name, omission string
			change         func(*Limits)
		}{
			{"input", "oversized", func(l *Limits) { l.MaxInputBytes = 1 }},
			{"declarations", "work_budget_exceeded", func(l *Limits) { l.MaxDeclarations = 1 }},
			{"work", "work_budget_exceeded", func(l *Limits) { l.MaxWorkUnits = 1 }},
			{"output", "output_budget_exceeded", func(l *Limits) { l.MaxOutputBytes = 256 }},
		} {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				limits := DefaultLimits()
				tt.change(&limits)
				r, err := Extract(context.Background(), Source{path, []byte(source)}, limits)
				if err != nil || r.Complete || r.Omission != tt.omission || len(r.Declarations) != 0 {
					t.Fatalf("Extract = %+v, %v", r, err)
				}
				encoded, err := json.Marshal(r)
				if err != nil || len(encoded) > limits.MaxOutputBytes {
					t.Fatalf("omission exceeds serialized budget: %d, %v", len(encoded), err)
				}
			})
		}
	}
}

func TestLanguageAndCancellation(t *testing.T) {
	for _, path := range []string{"x.go", "x.py", "x.js", "x.ts", "x.tsx", "x.rs"} {
		if Language(path) == "" || PolicyVersion(Language(path)) == "" {
			t.Errorf("missing policy for %s", path)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Extract(ctx, Source{path, nil}, DefaultLimits()); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: cancellation = %v", path, err)
		}
	}
	r, err := Extract(context.Background(), Source{"x.bin", nil}, DefaultLimits())
	if err != nil || r.Complete || r.Omission != "unsupported" {
		t.Fatalf("unsupported = %+v, %v", r, err)
	}
}

func BenchmarkExtract(b *testing.B) {
	for _, tt := range []struct{ path, prefix, unit string }{
		{"x.go", "package p\n", "func f%d(x int) int { return x + 1 }\n"},
		{"x.py", "", "def f%d(x: int) -> int:\n    return x + 1\n"},
		{"x.js", "", "function f%d(x) { return x + 1; }\n"},
		{"x.ts", "", "function f%d(x: number): number { return x + 1; }\n"},
		{"x.tsx", "", "function F%d() { return <div>Hello</div>; }\n"},
		{"x.rs", "", "fn f%d(x: i32) -> i32 { x + 1 }\n"},
	} {
		var source strings.Builder
		source.WriteString(tt.prefix)
		for i := range 100 {
			fmt.Fprintf(&source, tt.unit, i)
		}
		input := Source{tt.path, []byte(source.String())}
		b.Run(tt.path, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(input.Content)))
			for b.Loop() {
				r, err := Extract(context.Background(), input, DefaultLimits())
				if err != nil || !r.Complete || len(r.Declarations) != 100 {
					b.Fatalf("Extract = %+v, %v", r, err)
				}
			}
		})
	}
}
