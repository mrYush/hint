package index

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mrYush/hint/internal/workspace"
)

func TestEntryIDMatchesWorkspaceNativePaths(t *testing.T) {
	const slashID = "e_Bh6Rh2dc_ZHiRWDR_f8q7MHmyTbaA8hDs94iYefXt9w"
	if got := EntryID("r_test", filepath.Join("src", "file.go")); got != slashID {
		t.Fatalf("stable native identity changed: %s", got)
	}
	wantBackslash := "e_bzzQzDZ9A37GCL6CALMhXVYcXiQcIwcDhoV8L5JhmoE"
	if runtime.GOOS == "windows" {
		wantBackslash = slashID
	}
	if got := EntryID("r_test", `src\file.go`); got != wantBackslash {
		t.Fatalf("backslash identity changed: %s", got)
	}
	for _, path := range []string{"src/file.go", filepath.Join("src", "file.go"), `src\file.go`, `C:literal`, " spaced ", "raw\xff"} {
		if got, want := EntryID("r_test", path), workspace.EntryID("r_test", path); got != want {
			t.Errorf("native path %q: index=%q workspace=%q", path, got, want)
		}
	}
}

func TestCatalogPlatformPaths(t *testing.T) {
	s, _ := openTestStore(t, Options{})
	paths := []struct {
		path string
		want bool
	}{
		{"src/file.go", true}, {" spaced ", true}, {"raw\xff", true},
		{".", false}, {"..", false}, {"a/../b", false}, {"a//b", false}, {"/absolute", false}, {"bad\x00path", false},
		{`src\file.go`, runtime.GOOS != "windows"}, {`C:relative`, runtime.GOOS != "windows"},
		{`C:/absolute`, runtime.GOOS != "windows"}, {`..\outside`, runtime.GOOS != "windows"},
	}
	for _, test := range paths {
		t.Run(test.path, func(t *testing.T) {
			file, _ := testSource(test.path, "Name")
			file.State = StatePending
			_, err := s.PutSource(t.Context(), file, nil)
			if (err == nil) != test.want {
				t.Fatalf("catalog path admission: err=%v want admitted=%v", err, test.want)
			}
			if test.path != "." {
				err := s.Enqueue(t.Context(), file.RootID, file.Path)
				if (err == nil) != test.want {
					t.Fatalf("frontier path admission: err=%v want admitted=%v", err, test.want)
				}
			}
			if test.want {
				got, err := s.EntryLookup(t.Context(), file.RootID, workspace.EntryID(file.RootID, test.path))
				if err != nil || string(got.Path) != test.path {
					t.Fatalf("workspace lookup lost path bytes: %+v %v", got, err)
				}
			}
		})
	}
}
