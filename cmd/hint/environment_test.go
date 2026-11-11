package main

import (
 "testing"
 "context"
 "strings"
 "github.com/mrYush/hint/internal/config"
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

func TestConfigOptionsUseInjectedSources(t *testing.T) {
 e := environment{home:"/tmp/hint-home",cwd:"/tmp/project",lookupEnv:func(name string)(string,bool){
  if name == "HINT_MODEL" { return "test-model",true }
  return "",false
 }}
 opts, err := e.configOptions(config.Flags{Model:"flag-model"}, true)
 if err != nil { t.Fatal(err) }
 if opts.HomeDir != e.home || opts.WorkingDir != e.cwd || !opts.Draft { t.Fatalf("options = %+v", opts) }
 if v,ok := opts.LookupEnv("HINT_MODEL"); !ok || v != "test-model" { t.Fatalf("lookup = %q, %v",v,ok) }
 if opts.Flags.Model != "flag-model" { t.Fatalf("flags = %+v",opts.Flags) }
}

func TestRunRejectsMissingHomeBeforeCoreStartup(t *testing.T) {
 err := run(context.Background(), options{env:environment{cwd:"/tmp/project"}}, invocation{}, stdio{})
 if err == nil || !strings.Contains(err.Error(),"home directory") { t.Fatalf("run error = %v",err) }
}
