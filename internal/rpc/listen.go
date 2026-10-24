package rpc

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// RuntimeDir is where a `hint serve` process puts its socket and token:
// $XDG_RUNTIME_DIR/hint when that is set, otherwise
// %LOCALAPPDATA%\hint on Windows and ~/.local/state/hint elsewhere.
// lookupEnv is injected so tests never read the process environment.
func RuntimeDir(home string, lookupEnv func(string) (string, bool)) string {
	if lookupEnv != nil {
		if dir, ok := lookupEnv("XDG_RUNTIME_DIR"); ok && dir != "" {
			return filepath.Join(dir, "hint")
		}
		if dir, ok := lookupEnv("LOCALAPPDATA"); ok && dir != "" {
			return filepath.Join(dir, "hint")
		}
	}
	return filepath.Join(home, ".local", "state", "hint")
}

// DefaultListen is the address Serve uses when the user did not pass one.
// Unix everywhere the plan names a socket; loopback TCP on Windows, where
// a Unix socket is not the usual way to reach a user daemon.
func DefaultListen(dir string) (network, addr string) {
	if runtime.GOOS == "windows" {
		return "tcp", "127.0.0.1:0"
	}
	return "unix", filepath.Join(dir, "hint.sock")
}

// Listen opens network/addr. TCP is refused unless the host is loopback:
// the token file is the only authenticator, and it is not a credential
// that should cross a machine. A stale Unix socket whose peer is gone is
// removed; a socket another process still accepts is left alone.
func Listen(network, addr string) (net.Listener, error) {
	switch network {
	case "tcp":
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("rpc: %w", err)
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("rpc: tcp listen must be a loopback address, not %q", host)
		}
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("rpc: listen %s: %w", addr, err)
		}
		return lis, nil
	case "unix":
		if err := os.MkdirAll(filepath.Dir(addr), 0o700); err != nil {
			return nil, fmt.Errorf("rpc: %w", err)
		}
		if err := clearStaleSocket(addr); err != nil {
			return nil, err
		}
		lis, err := net.Listen("unix", addr)
		if err != nil {
			return nil, fmt.Errorf("rpc: listen %s: %w", addr, err)
		}
		if err := os.Chmod(addr, 0o600); err != nil {
			_ = lis.Close()
			return nil, fmt.Errorf("rpc: %w", err)
		}
		return lis, nil
	default:
		return nil, fmt.Errorf("rpc: unknown network %q", network)
	}
}

// clearStaleSocket removes addr when nothing is accepting on it. A live
// server answers a dial; that socket is not ours to delete.
func clearStaleSocket(addr string) error {
	if _, err := os.Stat(addr); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	d := net.Dialer{Timeout: 100 * time.Millisecond}
	conn, err := d.Dial("unix", addr)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("rpc: %s is already served", addr)
	}
	if rmErr := os.Remove(addr); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return fmt.Errorf("rpc: remove stale socket %s: %w", addr, rmErr)
	}
	return nil
}

// WriteToken creates dir (0700) and writes a fresh token to auth.token
// with mode 0600. The returned string is what hello must present.
func WriteToken(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("rpc: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rpc: token: %w", err)
	}
	token := hex.EncodeToString(buf)
	path := filepath.Join(dir, "auth.token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("rpc: token: %w", err)
	}
	return token, nil
}

// ReadToken returns the token a running server wrote with WriteToken.
func ReadToken(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "auth.token"))
	if err != nil {
		return "", fmt.Errorf("rpc: token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// ReadEndpoint returns what WriteEndpoint recorded: where the server that
// owns dir listens.
func ReadEndpoint(dir string) (network, addr string, err error) {
	data, err := os.ReadFile(filepath.Join(dir, "listen"))
	if err != nil {
		return "", "", fmt.Errorf("rpc: endpoint: %w", err)
	}
	network, addr, ok := strings.Cut(strings.TrimSpace(string(data)), " ")
	if !ok || network == "" || addr == "" {
		return "", "", fmt.Errorf("rpc: endpoint: malformed %s", filepath.Join(dir, "listen"))
	}
	return network, addr, nil
}

// WriteEndpoint records where the server listens, next to the token, so a
// client can find a port that was chosen as :0.
func WriteEndpoint(dir, network, addr string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("rpc: %w", err)
	}
	path := filepath.Join(dir, "listen")
	body := network + " " + addr + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("rpc: endpoint: %w", err)
	}
	return nil
}
