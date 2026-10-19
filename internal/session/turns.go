package session

import (
	"errors"
	"fmt"

	"github.com/mrYush/hint/pkg/agentapi"
)

// ErrNoSuchTurn reports a turn number a conversation does not have.
var ErrNoSuchTurn = errors.New("session: no such turn")

// Turns counts the user turns of a conversation: one per RoleUser
// message. It is the count a client arrives at from the history it was
// shown, which is why a fork names a turn and not a message index — the
// tool messages between two questions are the core's, and a client may
// never have seen them.
func Turns(messages []agentapi.Message) int {
	n := 0
	for _, m := range messages {
		if m.Role == agentapi.RoleUser {
			n++
		}
	}
	return n
}

// UpToTurn returns the conversation through the end of turn n, counted
// from 1: everything before the user message that opens turn n+1. The
// result is a copy. n must name a turn the conversation has; 0 and a
// number past the last turn are [ErrNoSuchTurn].
func UpToTurn(messages []agentapi.Message, n int) ([]agentapi.Message, error) {
	if n < 1 {
		return nil, fmt.Errorf("%w: turns are counted from 1, got %d", ErrNoSuchTurn, n)
	}
	seen := 0
	for i, m := range messages {
		if m.Role != agentapi.RoleUser {
			continue
		}
		seen++
		if seen == n+1 {
			return append([]agentapi.Message(nil), messages[:i]...), nil
		}
	}
	if seen < n {
		return nil, fmt.Errorf("%w: the conversation has %d turns, not %d", ErrNoSuchTurn, seen, n)
	}
	return append([]agentapi.Message(nil), messages...), nil
}
