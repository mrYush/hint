package builtin_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestWriteFile_CreatesWithParents(t *testing.T) {
	root := newRoot(t, nil)
	tl := builtin.NewWriteFile(root)
	if tl.Class() != agentapi.ClassWrite {
		t.Fatalf("class = %s", tl.Class())
	}

	got := ok(t, run(t, tl, `{"path":"a/b/c.txt","content":"hello\n"}`))
	if got != "Created a/b/c.txt (6 bytes)" {
		t.Fatalf("result: %q", got)
	}
	if readBack(t, root, "a/b/c.txt") != "hello\n" {
		t.Fatal("content not written")
	}

	got = ok(t, run(t, tl, `{"path":"a/b/c.txt","content":"bye"}`))
	if got != "Overwrote a/b/c.txt (3 bytes, was 6)" {
		t.Fatalf("result: %q", got)
	}
	if readBack(t, root, "a/b/c.txt") != "bye" {
		t.Fatal("content not overwritten")
	}

	// Empty content is a legitimate file.
	ok(t, run(t, tl, `{"path":"empty","content":""}`))
	if readBack(t, root, "empty") != "" {
		t.Fatal("empty file not written")
	}
}

func TestWriteFile_PreservesModeAndLeavesNoTemp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes on Windows")
	}
	root := newRoot(t, map[string]string{"run.sh": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(root.Dir(), "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	tl := builtin.NewWriteFile(root)
	ok(t, run(t, tl, `{"path":"run.sh","content":"#!/bin/sh\necho hi\n"}`))

	info, err := os.Stat(filepath.Join(root.Dir(), "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(root.Dir())
	for _, e := range entries {
		if e.Name() != "run.sh" {
			t.Fatalf("stray file left behind: %s", e.Name())
		}
	}
}

func TestWriteFile_WritesThroughSymlinkInsideRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	root := newRoot(t, map[string]string{"real/config.yaml": "old\n"})
	link := filepath.Join(root.Dir(), "config.yaml")
	if err := os.Symlink(filepath.Join(root.Dir(), "real", "config.yaml"), link); err != nil {
		t.Fatal(err)
	}
	ok(t, run(t, builtin.NewWriteFile(root), `{"path":"config.yaml","content":"new\n"}`))

	if readBack(t, root, "real/config.yaml") != "new\n" {
		t.Fatal("target of the symlink not updated")
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced by a regular file: %v %v", info, err)
	}
}

func TestWriteFile_Errors(t *testing.T) {
	root := newRoot(t, map[string]string{"dir/x": ""})
	tl := builtin.NewWriteFile(root)
	tests := []struct{ name, args, want string }{
		{"missing path", `{"content":"x"}`, "path is required"},
		{"directory", `{"path":"dir","content":"x"}`, "is a directory"},
		{"outside", `{"path":"../x","content":"x"}`, "outside the working directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { failed(t, run(t, tl, tt.args), tt.want) })
	}
}
