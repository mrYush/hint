package main

import (
 "fmt"
 "path/filepath"
 "github.com/mrYush/hint/internal/config"
 "os"
  "time"
)

// environment contains process inputs that must be replaceable in tests.
// The constructor reads process state once; callers use the snapshot.
type environment struct {
 home string
 cwd string
 lookupEnv func(string) (string, bool)
 args []string
 now func() time.Time
}

func processEnvironment() (environment, error) {
 cwd, err := os.Getwd()
 if err != nil { return environment{}, fmt.Errorf("working directory: %w", err) }
 home, err := os.UserHomeDir()
 if err != nil || home == "" {
  // Never allow an empty home to turn session and log paths into
  // relative paths inside the current project.
  return environment{}, fmt.Errorf("home directory is unavailable: %w", errHomeUnavailable(err))
 }
 env := environment{home:home, cwd:cwd, lookupEnv:os.LookupEnv, args:append([]string(nil),os.Args...), now:time.Now}
 return env, env.validate()
}

func (e environment) lookup(name string) (string,bool) {
 if e.lookupEnv == nil { return "",false }
 return e.lookupEnv(name)
}

func errHomeUnavailable(err error) error {
 if err != nil { return err }
 return fmt.Errorf("no home directory configured")
}

func (e environment) validate() error {
 if e.cwd == "" || !filepath.IsAbs(e.cwd) { return fmt.Errorf("working directory must be absolute") }
 if e.home == "" || !filepath.IsAbs(e.home) { return fmt.Errorf("home directory must be absolute") }
 return nil
}

// configOptions resolves configuration using the same process snapshot
// as the command, rather than reading a second set of global values.
func (e environment) configOptions(flags config.Flags, draft bool) (config.Options, error) {
 if err := e.validate(); err != nil { return config.Options{}, err }
 return config.Options{
  HomeDir:e.home, WorkingDir:e.cwd, LookupEnv:e.lookup,
  Flags:flags, Draft:draft, Keyring:config.OSKeyring{},
 }, nil
}
