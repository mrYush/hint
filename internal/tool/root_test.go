package tool_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mrYush/hint/internal/tool"
)

func TestRoot_Resolve(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	real := root.Dir() // TempDir may itself sit behind a symlink (macOS /var)

	tests := []struct {
		name    string
		in      string
		want    string
		outside bool
	}{
		{"empty is root", "", real, false},
		{"dot is root", ".", real, false},
		{"relative", "sub/a.txt", filepath.Join(real, "sub", "a.txt"), false},
		{"relative with dot segments", "sub/../sub/./b.txt", filepath.Join(real, "sub", "b.txt"), false},
		{"absolute inside", filepath.Join(real, "sub"), filepath.Join(real, "sub"), false},
		{"missing file under existing dir", "sub/new/deep.txt", filepath.Join(real, "sub", "new", "deep.txt"), false},
		{"parent escape", "../x", "", true},
		{"deep parent escape", "sub/../../x", "", true},
		{"absolute outside", filepath.Dir(real), "", true},
		{"sibling prefix trick", real + "-other/x", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := root.Resolve(tt.in)
			if tt.outside {
				if !errors.Is(err, tool.ErrOutsideRoot) {
					t.Fatalf("Resolve(%q) = %q, %v; want ErrOutsideRoot", tt.in, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRoot_SymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	outside := t.TempDir()
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "file-link")); err != nil {
		t.Fatal(err)
	}
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"link", "link/x.txt", "link/new/y.txt", "file-link"} {
		if _, err := root.Resolve(p); !errors.Is(err, tool.ErrOutsideRoot) {
			t.Errorf("Resolve(%q) err = %v, want ErrOutsideRoot", p, err)
		}
	}

	// A symlink that stays inside the root is fine.
	if err := os.MkdirAll(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "inner")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Resolve("inner/z.txt"); err != nil {
		t.Errorf("inside symlink rejected: %v", err)
	}
}

func TestRoot_Rel(t *testing.T) {
	dir := t.TempDir()
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Rel(filepath.Join(root.Dir(), "a", "b")); got != filepath.Join("a", "b") {
		t.Fatalf("Rel = %q", got)
	}
	if got := root.Rel(root.Dir()); got != "." {
		t.Fatalf("Rel(root) = %q", got)
	}
	if got := root.Rel("/elsewhere"); got != "/elsewhere" {
		t.Fatalf("Rel(outside) = %q", got)
	}
}

func TestNewRoot_Errors(t *testing.T) {
	if _, err := tool.NewRoot(""); err == nil {
		t.Error("empty dir accepted")
	}
	if _, err := tool.NewRoot(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing dir accepted")
	}
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.NewRoot(f); err == nil {
		t.Error("file accepted as root")
	}
}
