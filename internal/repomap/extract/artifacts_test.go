package extract

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPinnedGrammarDigests(t *testing.T) {
	for language, want := range map[string]string{
		"python":     "0b4396e688a08f9d2c6a11de2b018bdda63eef7b59497ee1fd56481cc96f5423",
		"javascript": "7abe2b54935fc1bf33bdcb8f48434676b801bcf62eca104b19b1dd1af18bfa5b",
		"typescript": "e0da1e255655f8e9c078b7f69a9c45198bb2d8e98d5d50e708204101e586346a",
		"tsx":        "764556f90021d52e4cabebf8019d9c729aeb05542f6722a6c818a7cbafb61a18",
		"rust":       "2cdd61ffe54efabf991b04f8ef9915e60822e8bd61e5f0fd677aa84061346ade",
	} {
		data, err := grammarModules.ReadFile("grammars/wasitter-" + language + ".wasm")
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
			t.Errorf("%s checksum = %s, want %s", language, got, want)
		}
	}
}

func TestWASMDeadlineDuringInitialization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err := Extract(ctx, Source{"x.ts", []byte(strings.Repeat("function f(x: number): number { return x; }\n", 1000))}, DefaultLimits())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("initialization deadline = %v", err)
	}
}

func TestMinifiedAndDecoratedSources(t *testing.T) {
	for _, tt := range []struct {
		path, text string
		count      int
	}{
		{"x.js", "function a(){return()=>{throw'BODY_SENTINEL'}}class B{m(){return 1}}const c=x=>x+1", 4},
		{"x.py", "@decorator\ndef open(x):\n    return x\n", 1},
		{"x.rs", "#[derive(Debug)] pub struct S; impl S { fn get(&self) -> &str { r#\"BODY_SENTINEL\"# } }", 2},
	} {
		t.Run(tt.path, func(t *testing.T) {
			r, err := Extract(context.Background(), Source{tt.path, []byte(tt.text)}, DefaultLimits())
			if err != nil || !r.Complete || len(r.Declarations) != tt.count {
				t.Fatalf("Extract = %+v, %v", r, err)
			}
			for _, d := range r.Declarations {
				if strings.Contains(d.Signature, "BODY_SENTINEL") {
					t.Errorf("body leaked: %+v", d)
				}
			}
		})
	}
}
