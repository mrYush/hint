package main

import (
 "testing"
 "time"
)

func TestInjectedEnvironment(t *testing.T) {
 fixed := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
 e := environment{
  home: "/tmp/hint-home",
  cwd: "/tmp/project",
  lookupEnv: func(name string) (string, bool) {
   if name == "XDG_DATA_HOME" { return "/tmp/state", true }
   return "", false
  },
  args: []string{"hint", "--version"},
  now: func() time.Time { return fixed },
 }
 if e.home != "/tmp/hint-home" || e.cwd != "/tmp/project" { t.Fatal("environment snapshot changed") }
 if got, ok := e.lookup("XDG_DATA_HOME"); !ok || got != "/tmp/state" { t.Fatalf("lookup: %q %v", got, ok) }
 if !e.now().Equal(fixed) { t.Fatal("injected clock was ignored") }
 if len(e.args) != 2 { t.Fatal("injected args missing") }
}

func TestEnvironmentMissingLookup(t *testing.T) {
 var e environment
 if value, ok := e.lookup("ANY"); ok || value != "" { t.Fatalf("unexpected lookup %q %v", value, ok) }
}
