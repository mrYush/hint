package permission

import (
	"strings"
	"sync"

	"github.com/mrYush/hint/pkg/agentapi"
)

// allowList remembers the command scopes a user answered "always" to. It
// lives for the process: sessions on disk are WP0.7's, and persisting a
// grant beyond the run it was given in is a separate decision.
//
// Only execute-class calls can be granted. A write is confirmed with its
// diff every time: "always" on one edit would either cover only that
// file, whose next diff the user has not seen either, or turn the run
// into --auto-edit with a keystroke — and that switch already exists as
// the flag, where it is visible.
//
// The mutex is for WP0.11, whose parallel groups will consult the list
// from several goroutines.
type allowList struct {
	mu     sync.Mutex
	scopes []string
}

// allows reports whether an earlier grant covers req.
func (l *allowList) allows(req agentapi.PermissionRequest) bool {
	if req.Class != agentapi.ClassExecute {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, scope := range l.scopes {
		if commandMatches(scope, req.Detail) {
			return true
		}
	}
	return false
}

// grant remembers an "always" answer to req. It returns false when the
// request has nothing rememberable — a write, or a command that yields
// no scope — in which case the answer counts as a one-off allow.
func (l *allowList) grant(req agentapi.PermissionRequest) bool {
	if req.Class != agentapi.ClassExecute {
		return false
	}
	scope := CommandScope(req.Detail)
	if scope == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.scopes = append(l.scopes, scope)
	return true
}

// subcommandPrograms are the programs whose first argument names the
// action — the only ones "always" is offered for. A grant is always two
// words, `go test`, `git commit`, `npm run`: wide enough to cover the
// arguments that vary between calls, narrow enough that it never means
// "any go command".
//
// Everything else gets no scope. A bare program name would be the wrong
// grant for two kinds of program: wrappers and interpreters (`sudo`,
// `env`, `bash -c`, `python -c`, `node -e`, `xargs`) run whatever follows,
// and even an innocent-looking one (`curl`, `make`) takes its whole
// behaviour from arguments a prefix grant does not look at. A list of
// programs that are safe to grant by name is never complete, so there is
// none; those calls are confirmed one at a time.
var subcommandPrograms = map[string]bool{
	"go": true, "git": true, "gh": true,
	"npm": true, "npx": true, "pnpm": true, "yarn": true, "bun": true,
	"cargo": true, "rustup": true,
	"docker": true, "kubectl": true, "helm": true,
	"pip": true, "pip3": true, "poetry": true, "uv": true,
	"dotnet": true, "mvn": true, "gradle": true,
	"brew": true, "apt": true, "apt-get": true,
}

// shellMeta are the characters that make a shell run something other
// than the one command the user looked at: separators, pipes,
// redirections, substitutions. A command containing one past its granted
// scope is not covered by that grant.
const shellMeta = ";&|<>$`()\n\r"

// CommandScope derives the allow-list key an "always" answer to cmd
// remembers: `program subcommand` for a program in the subcommand list
// whose next word is not a flag. It returns "" when no safe key exists —
// any other program, a flag or nothing after the program, a leading
// VAR=value assignment, or a scope carrying shell metacharacters — and
// then "always" is not offered.
func CommandScope(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) < 2 || !subcommandPrograms[fields[0]] {
		return ""
	}
	if strings.HasPrefix(fields[1], "-") {
		return ""
	}
	scope := fields[0] + " " + fields[1]
	if strings.ContainsAny(scope, shellMeta) {
		return ""
	}
	return scope
}

// commandMatches reports whether cmd is covered by a granted scope: it is
// the scope exactly, or the scope followed by arguments that contain no
// shell metacharacter. `go test` covers `go test ./...` but neither
// `go testify` nor `go test; rm -rf ~`.
//
// What the arguments themselves do is not inspected: `go test -exec
// 'rm -rf /' ./...` is covered by `go test`. That is the limit of a
// prefix grant, the same one Claude Code's rules have; the user who
// answers "always" is trusting the program, not each future argument.
func commandMatches(scope, cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == scope {
		return true
	}
	if !strings.HasPrefix(cmd, scope+" ") {
		return false
	}
	return !strings.ContainsAny(cmd[len(scope):], shellMeta)
}
