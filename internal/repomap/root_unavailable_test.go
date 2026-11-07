package repomap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrYush/hint/internal/tool"
)

func TestMapDistinguishesUnavailableRootFromMissingScope(t *testing.T) {
	for _, mode := range []string{"missing-file", "missing-root", "replaced-root", "continuation"} {
		t.Run(mode, func(t *testing.T) {
			m, space, dir := queryFixture(t)
			indexQueryFile(t, m, space, dir, "x.go", "package p; func A() {}; func B() {}")
			target := tool.WithLimits(&mapTool{manager: m, space: space}, tool.DefaultLimits())
			args, want := `{"path":"missing.go"}`, "scope_not_found"
			if mode != "missing-file" {
				args, want = `{}`, "root_unavailable"
				if mode == "continuation" {
					page := runMap(t, target, `{"limit":1}`, MaxOutputBytes)
					if page.Page.NextCursor == "" {
						t.Fatal("missing continuation")
					}
					raw, err := json.Marshal(Request{Cursor: page.Page.NextCursor})
					if err != nil {
						t.Fatal(err)
					}
					args, want = string(raw), "stale_cursor"
				}
				moved := filepath.Join(t.TempDir(), "moved")
				if err := os.Rename(dir, moved); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if mode == "replaced-root" {
						if err := os.Remove(dir); err != nil {
							t.Error(err)
						}
					}
					if err := os.Rename(moved, dir); err != nil {
						t.Error(err)
					}
				})
				if mode == "replaced-root" {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := target.Run(t.Context(), "map", json.RawMessage(args))
			if err != nil || !result.IsError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			var body struct {
				Error struct {
					Code      string `json:"code"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(result.Text()), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != want || body.Error.Retryable != (want == "root_unavailable") {
				t.Fatalf("want %s: %s", want, result.Text())
			}
			if mode == "continuation" {
				m.mu.Lock()
				pending := len(m.queries)
				m.mu.Unlock()
				if pending != 0 {
					t.Fatalf("retained invalid query: %d", pending)
				}
			}
		})
	}
}
