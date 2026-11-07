package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLeaseTypedOpen(t *testing.T) {
	_, session, root := newWorkspace(t)
	write(t, filepath.Join(root.Dir(), "file"), "contents")
	for _, test := range []struct {
		name, path string
		open       func(Lease) (*os.File, error)
		want       error
	}{
		{"regular", "file", Lease.OpenRegular, nil},
		{"directory", ".", Lease.OpenDirectory, nil},
		{"reject-directory", ".", Lease.OpenRegular, ErrNotRegular},
		{"reject-file", "file", Lease.OpenDirectory, ErrNotDirectory},
	} {
		t.Run(test.name, func(t *testing.T) {
			lease, err := session.Resolve("", test.path)
			if err != nil {
				t.Fatal(err)
			}
			file, err := test.open(lease)
			if file != nil {
				defer func() {
					if err := file.Close(); err != nil {
						t.Error(err)
					}
				}()
			}
			if !errors.Is(err, test.want) || (file == nil) != (test.want != nil) {
				t.Fatalf("open: file=%v err=%v; want %v", file, err, test.want)
			}
			if test.want == nil {
				info, err := file.Stat()
				if err != nil {
					t.Fatal(err)
				}
				if info.IsDir() != (test.path == ".") {
					t.Fatalf("incorrect descriptor type: %v", info.Mode())
				}
			}
		})
	}
}
