package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/config"
)

func TestProcessEnvironmentSnapshot(t *testing.T) {
	t.Setenv("HINT_TEST_SNAPSHOT", "before")
	env, err := processEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HINT_TEST_SNAPSHOT", "after")
	if value, ok := env.lookup("HINT_TEST_SNAPSHOT"); !ok || value != "before" {
		t.Fatalf("snapshot lookup = %q, %v", value, ok)
	}
}

func TestEnvironmentMissingLookup(t *testing.T) {
	var e environment
	if value, ok := e.lookup("ANY"); ok || value != "" {
		t.Fatalf("unexpected lookup %q %v", value, ok)
	}
}

func TestConfigOptionsUseInjectedSources(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	e := environment{home: home, cwd: cwd, lookupEnv: func(name string) (string, bool) {
		if name == "HINT_MODEL" {
			return "test-model", true
		}
		return "", false
	}}
	opts, err := e.configOptions(config.Flags{Model: "flag-model"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if opts.HomeDir != e.home || opts.WorkingDir != e.cwd || !opts.Draft {
		t.Fatalf("options = %+v", opts)
	}
	if v, ok := opts.LookupEnv("HINT_MODEL"); !ok || v != "test-model" {
		t.Fatalf("lookup = %q, %v", v, ok)
	}
	if opts.Flags.Model != "flag-model" {
		t.Fatalf("flags = %+v", opts.Flags)
	}
}

func TestRunRejectsMissingHomeBeforeCoreStartup(t *testing.T) {
	err := run(context.Background(), options{env: environment{cwd: t.TempDir()}}, invocation{}, stdio{})
	if err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("run error = %v", err)
	}
}

func TestCacheDirectories(t *testing.T) {
	home, absolute := t.TempDir(), t.TempDir()
	for _, value := range []string{"", "relative-cache", absolute} {
		t.Run(value, func(t *testing.T) {
			env := environment{home: home, lookupEnv: func(key string) (string, bool) { return value, key == "XDG_CACHE_HOME" }}
			cache := env.cacheDirFor("linux")
			summary := env.summaryCacheDir()
			if value == "relative-cache" {
				if cache != "" || summary != "" {
					t.Fatalf("relative caches: %q, %q", cache, summary)
				}
				return
			}
			if value == "" {
				value = filepath.Join(home, ".cache")
			}
			if cache != value || summary != filepath.Join(value, "hint", "instructions") {
				t.Fatalf("cache paths: %q, %q", cache, summary)
			}
		})
	}
}
