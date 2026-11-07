package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenContentionIsBoundedAndRecovers(t *testing.T) {
	s, path := openTestStore(t, Options{})
	conn, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := conn.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				t.Error(err)
			}
		}
	}()
	start := time.Now()
	other, err := Open(path, Options{OperationTimeout: 30 * time.Millisecond})
	elapsed := time.Since(start)
	if other != nil {
		_ = other.Close()
		t.Fatal("opened locked catalog")
	}
	if !errors.Is(err, ErrBusy) || !errors.Is(err, context.DeadlineExceeded) || elapsed > 400*time.Millisecond {
		t.Fatalf("open took %v: %v", elapsed, err)
	}
	if _, err := conn.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false
	other, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := other.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	locked = true
	if err := other.Enqueue(t.Context(), "r_test", []byte("work.go")); !errors.Is(err, ErrBusy) {
		t.Fatalf("write under lock: %v", err)
	}
	if _, err := conn.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false
	if err := other.Enqueue(t.Context(), "r_test", []byte("work.go")); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCancellationAndPermanentFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	path := filepath.Join(t.TempDir(), "cancel", "index.db")
	if _, err := OpenContext(ctx, path, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled open touched disk: %v", err)
	}
	broken := filepath.Join(t.TempDir(), "broken.db")
	if err := os.WriteFile(broken, []byte(strings.Repeat("not sqlite", 512)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(broken, Options{}); !errors.Is(err, ErrInitialization) || errors.Is(err, ErrBusy) {
		t.Fatalf("bad database: %v", err)
	}
}

func TestSharedCatalogRejectsConflictingAllowances(t *testing.T) {
	s, path := openTestStore(t, Options{MaxBytes: 1 << 20, MaxPins: 4})
	for _, opts := range []Options{{MaxBytes: 2 << 20, MaxPins: 4}, {MaxBytes: 512 << 10, MaxPins: 4}, {MaxBytes: 1 << 20, MaxPins: 5}} {
		other, err := Open(path, opts)
		if other != nil {
			_ = other.Close()
			t.Fatal("conflicting owner admitted")
		}
		if !errors.Is(err, ErrConfigConflict) {
			t.Fatalf("allowances: %v", err)
		}
	}
	file, syms := testSource("keep.go", "Keep")
	putTestSource(t, s, file, syms)
	if got, err := s.Status(t.Context(), file.RootID); err != nil || got.Indexed != 1 {
		t.Fatalf("original owner disrupted: %+v %v", got, err)
	}
}

func TestCatalogProcessPeer(t *testing.T) {
	path := os.Getenv("HINT_INDEX_TEST_PATH")
	if path == "" {
		t.Skip("subprocess helper")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	gate := os.Getenv("HINT_INDEX_TEST_GATE")
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	s, err := OpenContext(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	for i := range 20 {
		file, symbols := testSource(fmt.Sprintf("%s-%02d.go", os.Getenv("HINT_INDEX_TEST_PEER"), i), "Keep")
		for {
			_, err := s.PutSource(ctx, file, symbols)
			if err == nil {
				break
			}
			if !errors.Is(err, ErrBusy) {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(2 * time.Millisecond):
			}
		}
	}
}

func TestIndependentProcessesInitializeMigrateAndWrite(t *testing.T) {
	for _, migrate := range []bool{false, true} {
		t.Run(fmt.Sprint(migrate), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.db")
			if migrate {
				s, err := Open(path, Options{})
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.db.ExecContext(t.Context(), "DROP TABLE catalog_limits; DROP INDEX frontier_ready; ALTER TABLE frontier DROP COLUMN not_before; PRAGMA user_version=2;")
				if closeErr := s.Close(); err != nil || closeErr != nil {
					t.Fatalf("fixture: %v %v", err, closeErr)
				}
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			gate := filepath.Join(t.TempDir(), "start")
			var commands []*exec.Cmd
			var outputs []*bytes.Buffer
			for i := range 2 {
				command := exec.CommandContext(ctx, executable, "-test.run=^TestCatalogProcessPeer$", "-test.v")
				command.Env = append(os.Environ(), "HINT_INDEX_TEST_PATH="+path, "HINT_INDEX_TEST_GATE="+gate, fmt.Sprintf("HINT_INDEX_TEST_PEER=%d", i))
				output := new(bytes.Buffer)
				command.Stdout, command.Stderr = output, output
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				commands = append(commands, command)
				outputs = append(outputs, output)
			}
			if err := os.WriteFile(gate, nil, 0600); err != nil {
				t.Fatal(err)
			}
			for i, command := range commands {
				if err := command.Wait(); err != nil {
					t.Errorf("peer %d: %v\n%s", i, err, outputs[i])
				}
			}
			if t.Failed() {
				return
			}
			s, err := Open(path, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			}()
			status, err := s.Status(t.Context(), "r_test")
			if err != nil || status.Indexed != 40 || status.Generation != 40 {
				t.Fatalf("lost concurrent writes: %+v %v", status, err)
			}
		})
	}
}
