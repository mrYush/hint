package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/processtree"
)

// cloneRepo never invokes a shell, initializes submodules, or stores a URL with
// embedded credentials. The destination must be new; failed clones remain
// unattached for inspection rather than recursively deleting a user-named path.
func cloneRepo(ctx context.Context, source, destination string, environ []string) error {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "ssh") || !safeCloneUser(parsed) || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("clone requires an https:// or ssh:// URL without passwords, HTTPS userinfo, query or fragment")
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("clone destination: %w", err)
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return fmt.Errorf("clone requires a new destination directory: %w", err)
	}
	parent, stop := context.WithTimeout(ctx, 10*time.Minute)
	defer stop()
	args := []string{"-c", "credential.helper=", "-c", "core.hooksPath=" + os.DevNull, "-c", "protocol.ext.allow=never", "-c", "protocol.file.allow=never", "clone", "--no-recurse-submodules", "--", source, destination}
	cmd := exec.CommandContext(parent, "git", args...)
	cmd.Dir = filepath.Dir(destination)
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_") || (strings.HasPrefix(key, "SSH_") && key != "SSH_AUTH_SOCK") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -oBatchMode=yes", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	cmd.WaitDelay = time.Second
	// Output can echo URLs and credential-helper diagnostics, so it is neither
	// persisted nor returned. The process exit/cancellation is enough to retry.
	if err := processtree.Run(cmd); err != nil {
		if parent.Err() != nil {
			return fmt.Errorf("clone stopped; incomplete destination retained: %w", parent.Err())
		}
		return fmt.Errorf("git clone failed; incomplete destination retained; check URL, network and local authentication")
	}
	return nil
}

func safeCloneUser(u *url.URL) bool {
	if strings.HasPrefix(u.Hostname(), "-") {
		return false
	}
	if u.User == nil {
		return true
	}
	if u.Scheme != "ssh" {
		return false
	}
	if _, password := u.User.Password(); password {
		return false
	}
	username := u.User.Username()
	if username == "" || strings.HasPrefix(username, "-") {
		return false
	}
	for _, c := range username {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}
