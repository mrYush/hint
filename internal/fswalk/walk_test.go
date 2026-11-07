package fswalk

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"testing"
	"time"
)

type wideFS struct{ read, closed int }

func (f *wideFS) Open(name string) (fs.File, error) {
	if name != "." {
		return nil, fs.ErrNotExist
	}
	return &wideDir{fs: f}, nil
}

type wideDir struct {
	fs       *wideFS
	position int
}

func (*wideDir) Stat() (fs.FileInfo, error) { return fakeInfo{name: ".", dir: true}, nil }
func (*wideDir) Read([]byte) (int, error)   { return 0, io.EOF }
func (d *wideDir) Close() error             { d.fs.closed++; return nil }
func (d *wideDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if n <= 0 || n > 32 {
		return nil, fmt.Errorf("unbounded ReadDir(%d)", n)
	}
	d.fs.read += n
	out := make([]fs.DirEntry, n)
	for i := range out {
		out[i] = fs.FileInfoToDirEntry(fakeInfo{name: fmt.Sprintf("file-%09d", d.position)})
		d.position++
	}
	return out, nil
}

type fakeInfo struct {
	name string
	dir  bool
}

func (f fakeInfo) Name() string { return f.name }
func (fakeInfo) Size() int64    { return 0 }
func (f fakeInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir | 0700
	}
	return 0600
}
func (fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool      { return f.dir }
func (fakeInfo) Sys() any           { return nil }

func TestInfiniteWidthUsesOneBoundedBatch(t *testing.T) {
	filesystem := &wideFS{}
	visited := 0
	err := WalkDir(filesystem, ".", Limits{}, func(name string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name != "." {
			visited++
			if visited == 3 {
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil || visited != 3 || filesystem.read != 32 {
		t.Fatalf("visited=%d read=%d err=%v", visited, filesystem.read, err)
	}
	if filesystem.closed != 2 {
		t.Fatalf("stat/walk handles not closed: %d", filesystem.closed)
	}
}

func TestIgnoredEntriesStillConsumeWorkBudget(t *testing.T) {
	filesystem := &wideFS{}
	err := WalkDir(filesystem, ".", Limits{MaxEntries: 50}, func(string, fs.DirEntry, error) error { return nil })
	if !errors.Is(err, ErrLimit) || filesystem.read != 64 {
		t.Fatalf("read=%d err=%v", filesystem.read, err)
	}
}

type failingFS struct{ phase string }

func (f failingFS) Stat(name string) (fs.FileInfo, error) {
	if f.phase == "stat" {
		return nil, fs.ErrPermission
	}
	return fakeInfo{name: name, dir: true}, nil
}
func (f failingFS) Open(name string) (fs.File, error) {
	if name == "a" && f.phase == "open" {
		return nil, fs.ErrPermission
	}
	return &failingDir{name: name}, nil
}

type failingDir struct{ name string }

func (d *failingDir) Stat() (fs.FileInfo, error) { return fakeInfo{name: d.name, dir: true}, nil }
func (*failingDir) Read([]byte) (int, error)     { return 0, io.EOF }
func (*failingDir) Close() error                 { return nil }
func (d *failingDir) ReadDir(int) ([]fs.DirEntry, error) {
	if d.name == "a" {
		return nil, fs.ErrPermission
	}
	return []fs.DirEntry{fs.FileInfoToDirEntry(fakeInfo{name: "a", dir: true}), fs.FileInfoToDirEntry(fakeInfo{name: "b"})}, io.EOF
}

func TestErrorCallbacksHonorSkipControls(t *testing.T) {
	for _, phase := range []string{"stat", "open", "read"} {
		for _, skip := range []error{fs.SkipDir, fs.SkipAll} {
			t.Run(phase+"/"+skip.Error(), func(t *testing.T) {
				visitedB := false
				reported := false
				err := WalkDir(failingFS{phase: phase}, ".", Limits{}, func(name string, _ fs.DirEntry, err error) error {
					if err != nil {
						reported = true
						return skip
					}
					visitedB = visitedB || name == "b"
					return nil
				})
				if err != nil || !reported {
					t.Fatalf("callback control leaked: %v reported=%v", err, reported)
				}
				if want := phase != "stat" && errors.Is(skip, fs.SkipDir); visitedB != want {
					t.Fatalf("following sibling visited=%v want=%v", visitedB, want)
				}
			})
		}
	}
}

type lifecycleFS struct {
	batch    []fs.DirEntry
	closeErr error
	closed   int
}

func (*lifecycleFS) Stat(string) (fs.FileInfo, error) { return fakeInfo{name: ".", dir: true}, nil }
func (f *lifecycleFS) Open(string) (fs.File, error)   { return &lifecycleDir{owner: f}, nil }

type lifecycleDir struct {
	owner *lifecycleFS
	read  bool
}

func (*lifecycleDir) Stat() (fs.FileInfo, error) { return fakeInfo{name: ".", dir: true}, nil }
func (*lifecycleDir) Read([]byte) (int, error)   { return 0, io.EOF }
func (d *lifecycleDir) Close() error             { d.owner.closed++; return d.owner.closeErr }
func (d *lifecycleDir) ReadDir(int) ([]fs.DirEntry, error) {
	if d.read || len(d.owner.batch) == 0 {
		return nil, io.EOF
	}
	d.read = true
	return d.owner.batch, nil
}

func TestBeforeBatchSkipsEmptyEOF(t *testing.T) {
	for _, count := range []int{0, 1, 32} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			filesystem := &lifecycleFS{}
			for i := range count {
				filesystem.batch = append(filesystem.batch, fs.FileInfoToDirEntry(fakeInfo{name: fmt.Sprint(i)}))
			}
			calls := 0
			err := WalkDir(filesystem, ".", Limits{BeforeBatch: func(_ string, entries []fs.DirEntry) error {
				calls++
				if len(entries) != count || len(entries) == 0 {
					t.Fatalf("unexpected batch: %d", len(entries))
				}
				return nil
			}}, func(string, fs.DirEntry, error) error { return nil })
			want := 0
			if count > 0 {
				want = 1
			}
			if err != nil || calls != want || filesystem.closed != 1 {
				t.Fatalf("calls=%d closed=%d err=%v", calls, filesystem.closed, err)
			}
		})
	}
}

func TestSkipAllPreservesCloseFailure(t *testing.T) {
	closeErr := errors.New("close failure")
	callbackErr := errors.New("visit failure")
	for _, visitErr := range []error{fs.SkipAll, callbackErr} {
		filesystem := &lifecycleFS{batch: []fs.DirEntry{fs.FileInfoToDirEntry(fakeInfo{name: "a"}), fs.FileInfoToDirEntry(fakeInfo{name: "b"})}, closeErr: closeErr}
		visits := 0
		err := WalkDir(filesystem, ".", Limits{}, func(name string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if name != "." {
				visits++
				return visitErr
			}
			return nil
		})
		if !errors.Is(err, closeErr) || errors.Is(err, fs.SkipAll) || visits != 1 || filesystem.closed != 1 {
			t.Fatalf("visits=%d closed=%d err=%v", visits, filesystem.closed, err)
		}
		if errors.Is(visitErr, callbackErr) && !errors.Is(err, callbackErr) {
			t.Fatalf("lost callback error: %v", err)
		}
	}
}
