package main

import (
 "fmt"
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
 return environment{home:home, cwd:cwd, lookupEnv:os.LookupEnv, args:append([]string(nil),os.Args...), now:time.Now}, nil
}

func (e environment) lookup(name string) (string,bool) {
 if e.lookupEnv == nil { return "",false }
 return e.lookupEnv(name)
}

func errHomeUnavailable(err error) error {
 if err != nil { return err }
 return fmt.Errorf("no home directory configured")
}
