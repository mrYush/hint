package config

import (
	"path/filepath"
	"testing"
)

// The WP0.12 knobs: whether the budget was set by anyone (the project
// package scales the default to the model's window, never an explicit
// value) and the opt-in summarize switch.
func TestInstructions_ExplicitBudgetAndSummarize(t *testing.T) {
	withKey := func(extra map[string]string) func(string) (string, bool) {
		env := map[string]string{"OPENAI_API_KEY": "k"}
		for k, v := range extra {
			env[k] = v
		}
		return lookup(env)
	}

	t.Run("defaults: budget not explicit, summarize off", func(t *testing.T) {
		opts := dirs(t)
		opts.LookupEnv = withKey(nil)
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Instructions.Budget != DefaultInstructionBudget || cfg.Instructions.BudgetExplicit || cfg.Instructions.Summarize {
			t.Errorf("Instructions = %+v", cfg.Instructions)
		}
	})

	t.Run("a file sets both", func(t *testing.T) {
		opts := dirs(t)
		opts.LookupEnv = withKey(nil)
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), "instructions:\n  budget: 1000\n  summarize: true\n")
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Instructions.Budget != 1000 || !cfg.Instructions.BudgetExplicit || !cfg.Instructions.Summarize {
			t.Errorf("Instructions = %+v", cfg.Instructions)
		}
	})

	t.Run("the variable makes the budget explicit; a bad boolean is ignored", func(t *testing.T) {
		opts := dirs(t)
		opts.LookupEnv = withKey(map[string]string{"HINT_INSTRUCTION_BUDGET": "2000", "HINT_INSTRUCTIONS_SUMMARIZE": "yes please"})
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Instructions.Budget != 2000 || !cfg.Instructions.BudgetExplicit || cfg.Instructions.Summarize {
			t.Errorf("Instructions = %+v", cfg.Instructions)
		}
		hasWarning(t, cfg, "HINT_INSTRUCTIONS_SUMMARIZE")
	})

	t.Run("an ignored variable leaves the budget default", func(t *testing.T) {
		opts := dirs(t)
		opts.LookupEnv = withKey(map[string]string{"HINT_INSTRUCTION_BUDGET": "lots"})
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Instructions.BudgetExplicit {
			t.Errorf("a value that was ignored must not count as explicit: %+v", cfg.Instructions)
		}
	})

	t.Run("the flag wins and can switch a file's summarize off", func(t *testing.T) {
		opts := dirs(t)
		opts.LookupEnv = withKey(map[string]string{"HINT_INSTRUCTIONS_SUMMARIZE": "true"})
		writeFile(t, filepath.Join(opts.WorkingDir, ".hint", "config.yaml"), "instructions:\n  summarize: true\n")
		opts.Flags.SummarizeInstructions = "false"
		opts.Flags.InstructionBudget = "4096"
		cfg, err := LoadFrom(opts)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Instructions.Summarize || cfg.Instructions.Budget != 4096 || !cfg.Instructions.BudgetExplicit {
			t.Errorf("Instructions = %+v", cfg.Instructions)
		}
		opts.Flags.SummarizeInstructions = "maybe"
		if _, err := LoadFrom(opts); err == nil {
			t.Error("a flag that is not a boolean must be an error")
		}
	})
}
