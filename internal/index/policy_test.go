package index

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPolicyObservationsAreDurableAndRootScoped(t *testing.T) {
	s, path := openTestStore(t, Options{})
	for _, test := range []struct {
		fingerprint string
		epoch       uint64
	}{{"absent", 0}, {"first", 1}, {"first", 1}, {"edited", 2}, {"absent", 3}, {"absent", 3}, {"recreated", 4}} {
		got, err := s.ObservePolicy(t.Context(), "r_test", []byte(".gitignore"), test.fingerprint)
		if err != nil || got != test.epoch {
			t.Fatalf("observe %q: epoch=%d err=%v; want %d", test.fingerprint, got, err, test.epoch)
		}
	}
	if got, err := s.ObservePolicy(t.Context(), "r_other", []byte(".gitignore"), "other"); err != nil || got != 1 {
		t.Fatalf("other root: %d %v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	if got, err := reopened.PolicyRevision(t.Context(), "r_test"); err != nil || got != 4 {
		t.Fatalf("durable epoch: %d %v", got, err)
	}
	if got, err := reopened.ObservePolicy(t.Context(), "r_test", []byte(".gitignore"), "recreated"); err != nil || got != 4 {
		t.Fatalf("unchanged fingerprint after restart: %d %v", got, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := reopened.ObservePolicy(canceled, "r_test", []byte(".gitignore"), "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled observation: %v", err)
	}
	if _, err := reopened.ObservePolicy(t.Context(), "r_test", []byte(".gitignore"), strings.Repeat("x", 257)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized observation: %v", err)
	}
	if got, err := reopened.ObservePolicy(t.Context(), "r_test", []byte(".gitignore"), "recreated"); err != nil || got != 4 {
		t.Fatalf("failed observation changed fingerprint/epoch: %d %v", got, err)
	}
}
