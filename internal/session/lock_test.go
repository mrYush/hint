package session_test

import (
	"errors"
	"testing"

	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/pkg/agentapi"
)

// TestOpenLocksTheSession: a second run continuing the same session gets
// ErrLocked instead of interleaving its records, and Close releases the
// lock for the next run.
func TestOpenLocksTheSession(t *testing.T) {
	store := newStore(t, "1111")
	sess := writeGolden(t, store, "/p")
	path := sess.Path()

	held, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(path); !errors.Is(err, session.ErrLocked) {
		t.Fatalf("second Open = %v, want ErrLocked", err)
	}
	if err := held.AppendMessage(agentapi.UserMessage("still the only writer")); err != nil {
		t.Fatalf("append while locked: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open after Close: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestCreateLocksTheSession: a session being recorded is locked from the
// moment it exists, so a second run that lists it as the latest cannot
// append to it.
func TestCreateLocksTheSession(t *testing.T) {
	store := newStore(t, "2222")
	sess, err := store.Create("/p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(sess.Path()); !errors.Is(err, session.ErrLocked) {
		t.Fatalf("Open a session being created = %v, want ErrLocked", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(sess.Path())
	if err != nil {
		t.Fatalf("Open after Close: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestPeekReadsWhileSessionIsOpen: listing and forking read a session
// without taking its lock, so a run another process records stays
// readable, and the peek cannot append to it.
func TestPeekReadsWhileSessionIsOpen(t *testing.T) {
	store := newStore(t, "3333")
	sess := writeGolden(t, store, "/p")
	path := sess.Path()

	held, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	peeked, err := store.Peek("/p", "3333")
	if err != nil {
		t.Fatalf("Peek while open: %v", err)
	}
	defer func() { _ = peeked.Close() }()
	if peeked.Len() != 3 {
		t.Errorf("Peek Len = %d, want 3", peeked.Len())
	}
	if err := peeked.AppendMessage(agentapi.UserMessage("nope")); err == nil {
		t.Error("a peeked session must not accept an append")
	}
	infos, err := store.List("/p")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Errorf("List = %d sessions, want 1: the lock file is not a session", len(infos))
	}
}
