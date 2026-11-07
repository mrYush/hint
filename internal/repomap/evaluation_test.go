package repomap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/repomap/extract"
	"github.com/mrYush/hint/internal/repomap/worker"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
)

// TestCorpusHelper is entered only by this test's explicitly configured subprocess.
func TestCorpusHelper(t *testing.T) {
	if os.Getenv("HINT_CORPUS_HELPER") != "1" {
		return
	}
	if err := worker.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

type evaluationRunner struct {
	process *worker.Process
	calls   int
}

func (r *evaluationRunner) Extract(ctx context.Context, source extract.Source, limits extract.Limits) (extract.Result, error) {
	r.calls++
	return r.process.Extract(ctx, source, limits)
}
func (r *evaluationRunner) Close() error { return r.process.Close() }

func TestCorpusEvaluation(t *testing.T) {
	path := os.Getenv("HINT_INDEX_CORPUS")
	if path == "" {
		t.Skip("set HINT_INDEX_CORPUS to the pinned corpus directory for local measurements")
	}
	data, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Sources []struct {
			Files []struct {
				Path, SHA256 string
				Lines        int
			}
		}
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	var paths []string
	lines := 0
	languages := make(map[string]int)
	for _, source := range manifest.Sources {
		for _, file := range source.Files {
			if !filepath.IsLocal(file.Path) {
				t.Fatal("non-local corpus path")
			}
			body, err := os.ReadFile(filepath.Join(path, file.Path))
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(body)
			if hex.EncodeToString(digest[:]) != file.SHA256 {
				t.Fatalf("corpus checksum mismatch: %s", file.Path)
			}
			paths = append(paths, file.Path)
			lines += file.Lines
			languages[extract.Language(file.Path)]++
		}
	}
	if lines < 100000 || len(paths) > 2048 || len(languages) != 5 {
		t.Fatalf("corpus scope: %d lines, %d files, %d languages", lines, len(paths), len(languages))
	}
	root, err := tool.NewRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	space := registry.NewSession()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := &evaluationRunner{process: &worker.Process{Command: []string{exe, "-test.run=^TestCorpusHelper$"}, Environ: func() []string { return append(os.Environ(), "HINT_CORPUS_HELPER=1") }, Timeout: 3 * time.Second}}
	catalog := filepath.Join(t.TempDir(), "index.db")
	m, err := New(context.Background(), Options{Path: catalog, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(m.Close(), registry.Close(), root.Close()); err != nil {
			t.Error(err)
		}
	})
	m.Track(space)
	report := map[string]any{"files": len(paths), "lines": lines, "languages": languages, "note": "Manual focused indexing; background auto profile remains unvalidated; filesystem cache is not dropped."}
	if os.Getenv("HINT_INDEX_CORPUS_BASELINE") == "1" {
		time.Sleep(2 * time.Second)
		report["baseline"] = true
	} else {
		for _, phase := range []string{"cold_catalog", "warm_catalog"} {
			start, before := time.Now(), runner.calls
			failures := make(map[string]string)
			for _, path := range paths {
				lease, err := space.Resolve("", path)
				if err == nil {
					err = m.refresh(context.Background(), lease)
				}
				if err != nil {
					failures[path] = err.Error()
				}
			}
			roots, _ := space.Snapshot()
			counts, err := m.store.Status(context.Background(), roots[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			report[phase] = map[string]any{"elapsed_ms": float64(time.Since(start).Microseconds()) / 1000, "extractor_calls": runner.calls - before, "counts": counts, "failures": failures}
		}
		var latencies []float64
		var resultBytes, resultEntries int
		for range 20 {
			start := time.Now()
			data, err := m.query(context.Background(), space, Request{Path: ".", Symbol: "Session", Match: "exact", Limit: 20}, 8192)
			if err != nil {
				t.Fatal(err)
			}
			latencies = append(latencies, float64(time.Since(start).Microseconds())/1000)
			var response Response
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatal(err)
			}
			resultBytes, resultEntries = len(data), len(response.Entries)
		}
		sort.Float64s(latencies)
		report["exact_query"] = map[string]any{"repetitions": 20, "median_ms": (latencies[9] + latencies[10]) / 2, "p95_ms": latencies[18], "result_bytes": resultBytes, "entries": resultEntries}
		info, err := os.Stat(catalog)
		if err != nil {
			t.Fatal(err)
		}
		report["catalog_bytes"] = info.Size()
		roots, _ := space.Snapshot()
		var after []byte
		omissions := make(map[string]string)
		for {
			page, err := m.store.ScanEntries(context.Background(), roots[0].ID, after, 32, 256<<10)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range page.Files {
				if file.Reason != "" {
					omissions[string(file.Path)] = file.Reason
				}
			}
			if page.Exhausted {
				break
			}
			after = page.Next
		}
		report["omissions"] = omissions
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if destination := os.Getenv("HINT_INDEX_CORPUS_REPORT"); destination != "" {
		if err := os.WriteFile(destination, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(string(encoded))
}
