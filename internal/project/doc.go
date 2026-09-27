// Package project derives the per-run project context the CLI puts in
// front of the model: the instruction files a project keeps for agents
// (HINT.md and its compatible names), an overview of the working
// directory's shape, and the ignore rules that keep both — and the file
// tools' own walks — away from build output and dependency trees.
//
// Three seams are deliberately small so later work packages can replace
// one without touching the others:
//
//   - [Ignorer] answers "skip this entry?" for a walk. [Rules] builds one
//     per directory: git's own answer (via `git ls-files`) inside a
//     repository when git is installed, the [Basic] hidden-and-dependency
//     rule everywhere else. The tools in internal/tool/builtin share it,
//     so list_dir, glob and grep prune the same entries the overview does.
//   - [Overview] renders the directory's shape for the prompt. [Tree] is
//     today's implementation; [None] leaves discovery to the tools. The
//     interface is what would let the agent itself pick a shape later.
//   - [ReadInstructions] reads the project's instruction files under one
//     shared byte budget, so a pathological file cannot crowd the
//     conversation out of the context window.
//
// [Load] wires the three together for the working directory. Nothing here
// is cached across runs: the preamble is rebuilt every time, which is why
// the session store does not persist it (see internal/session).
package project
