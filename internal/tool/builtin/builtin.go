package builtin

import (
	"os/exec"

	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// options holds the knobs shared by every constructor in this package.
type options struct {
	limits tool.Limits
	// rg is the ripgrep executable, or "" to use the pure-Go search.
	rg    string
	rgSet bool
	// git is the git executable the pure-Go walks ask for ignore rules,
	// or "" to apply project.Basic alone.
	git    string
	gitSet bool
	todos  *TodoList
}

// rules returns the ignore rules the pure-Go walks of list_dir, glob and
// grep apply.
func (o options) rules() project.Rules { return project.NewRules(o.git) }

// Option configures a tool constructor.
type Option func(*options)

// WithLimits overrides [tool.DefaultLimits] for the tools being built.
func WithLimits(l tool.Limits) Option {
	return func(o *options) { o.limits = l }
}

// WithRipgrep sets the ripgrep executable glob and grep shell out to. An
// empty path disables ripgrep and forces the pure-Go implementation; when
// the option is not given, rg is looked up in PATH once at construction.
func WithRipgrep(path string) Option {
	return func(o *options) { o.rg, o.rgSet = path, true }
}

// WithGit sets the git executable the pure-Go walks of list_dir, glob and
// grep use to honour .gitignore inside a repository (ripgrep reads
// .gitignore itself). An empty path disables git and leaves only the
// hidden-and-dependency rule; when the option is not given, git is looked
// up in PATH once at construction.
func WithGit(path string) Option {
	return func(o *options) { o.git, o.gitSet = path, true }
}

// WithTodoList makes the todo tool write into list instead of a fresh one,
// so a caller that renders the plan elsewhere can read it back.
func WithTodoList(list *TodoList) Option {
	return func(o *options) { o.todos = list }
}

func buildOptions(opts []Option) options {
	o := options{limits: tool.DefaultLimits()}
	for _, opt := range opts {
		opt(&o)
	}
	if !o.rgSet {
		if p, err := exec.LookPath("rg"); err == nil {
			o.rg = p
		}
	}
	if !o.gitSet {
		if p, err := exec.LookPath("git"); err == nil {
			o.git = p
		}
	}
	if o.todos == nil {
		o.todos = NewTodoList()
	}
	return o
}

// ReadOnly returns the tools that only observe the working directory —
// read_file, list_dir, glob, grep — plus todo, which touches nothing but
// the agent's own plan. These are the tools cmd/hint registers before the
// permission layer (WP0.6) exists: none of them can change the user's
// files or run a command.
func ReadOnly(root tool.Root, opts ...Option) []agentapi.Tool {
	o := buildOptions(opts)
	return []agentapi.Tool{
		newReadFile(root, o),
		newListDir(root, o),
		newGlob(root, o),
		newGrep(root, o),
		newTodo(o),
	}
}

// All returns every built-in tool, including the write and execute class
// ones. Register these only behind the permission layer.
func All(root tool.Root, opts ...Option) []agentapi.Tool {
	o := buildOptions(opts)
	return []agentapi.Tool{
		newReadFile(root, o),
		newWriteFile(root, o),
		newEditFile(root, o),
		newListDir(root, o),
		newGlob(root, o),
		newGrep(root, o),
		newBash(root, o),
		newTodo(o),
	}
}

// Individual constructors, for a caller that wants one tool with its own
// options. Each returns the tool already wrapped by tool.WithLimits.

// NewReadFile returns the read_file tool.
func NewReadFile(root tool.Root, opts ...Option) agentapi.Tool {
	return newReadFile(root, buildOptions(opts))
}

// NewWriteFile returns the write_file tool.
func NewWriteFile(root tool.Root, opts ...Option) agentapi.Tool {
	return newWriteFile(root, buildOptions(opts))
}

// NewEditFile returns the edit_file tool.
func NewEditFile(root tool.Root, opts ...Option) agentapi.Tool {
	return newEditFile(root, buildOptions(opts))
}

// NewListDir returns the list_dir tool.
func NewListDir(root tool.Root, opts ...Option) agentapi.Tool {
	return newListDir(root, buildOptions(opts))
}

// NewGlob returns the glob tool.
func NewGlob(root tool.Root, opts ...Option) agentapi.Tool {
	return newGlob(root, buildOptions(opts))
}

// NewGrep returns the grep tool.
func NewGrep(root tool.Root, opts ...Option) agentapi.Tool {
	return newGrep(root, buildOptions(opts))
}

// NewBash returns the bash tool.
func NewBash(root tool.Root, opts ...Option) agentapi.Tool {
	return newBash(root, buildOptions(opts))
}

// NewTodo returns the todo tool.
func NewTodo(opts ...Option) agentapi.Tool {
	return newTodo(buildOptions(opts))
}
