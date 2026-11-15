package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/config"
)

// environment contains process inputs that must be replaceable in tests.
// The constructor reads process state once; callers use the snapshot.
type environment struct {
	err       error
	home      string
	cwd       string
	lookupEnv func(string) (string, bool)
	args      []string
	now       func() time.Time
}

func processEnvironment() (environment, error) {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		values[key] = value
	}
	env := environment{
		lookupEnv: func(key string) (string, bool) {
			if runtime.GOOS == "windows" {
				key = strings.ToUpper(key)
			}
			value, ok := values[key]
			return value, ok
		},
		args: append([]string(nil), os.Args...),
		now:  time.Now,
	}
	env.cwd, env.err = os.Getwd()
	if env.err != nil {
		env.err = fmt.Errorf("working directory: %w", env.err)
		return env, env.err
	}
	var err error
	env.home, err = os.UserHomeDir()
	if err != nil || env.home == "" {
		env.err = fmt.Errorf("home directory is unavailable: %w", errHomeUnavailable(err))
		return env, env.err
	}
	env.err = env.validate()
	return env, env.err
}

func (e environment) lookup(name string) (string, bool) {
	if e.lookupEnv == nil {
		return "", false
	}
	return e.lookupEnv(name)
}

func errHomeUnavailable(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("no home directory configured")
}

func (e environment) validate() error {
	if e.err != nil {
		return e.err
	}
	if e.cwd == "" || !filepath.IsAbs(e.cwd) {
		return fmt.Errorf("working directory must be absolute")
	}
	if e.home == "" || !filepath.IsAbs(e.home) {
		return fmt.Errorf("home directory must be absolute")
	}
	return nil
}

// configOptions resolves configuration using the same process snapshot
// as the command, rather than reading a second set of global values.
func (e environment) configOptions(flags config.Flags, draft bool) (config.Options, error) {
	if err := e.validate(); err != nil {
		return config.Options{}, err
	}
	return config.Options{
		HomeDir: e.home, WorkingDir: e.cwd, LookupEnv: e.lookup,
		Flags: flags, Draft: draft, Keyring: config.OSKeyring{},
	}, nil
}

func (e environment) cacheDir() string {
	return e.cacheDirFor(runtime.GOOS)
}

func (e environment) cacheDirFor(goos string) string {
	switch goos {
	case "windows":
		value, _ := e.lookup("LocalAppData")
		if !filepath.IsAbs(value) {
			return ""
		}
		return value
	case "darwin", "ios":
		return filepath.Join(e.home, "Library", "Caches")
	case "plan9":
		return filepath.Join(e.home, "lib", "cache")
	default:
		if value, ok := e.lookup("XDG_CACHE_HOME"); ok && value != "" {
			if !filepath.IsAbs(value) {
				return ""
			}
			return value
		}
		return filepath.Join(e.home, ".cache")
	}
}

func (e environment) summaryCacheDir() string {
	base, _ := e.lookup("XDG_CACHE_HOME")
	if base == "" {
		base = filepath.Join(e.home, ".cache")
	}
	if !filepath.IsAbs(base) {
		return ""
	}
	return filepath.Join(base, "hint", "instructions")
}
