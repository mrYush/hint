package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/debuglog"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/internal/tool"
)

func newServeCommand(flags *config.Flags) *cobra.Command {
	var listen string
	var ask, autoEdit, yolo, debug bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the agent on a local socket for other clients",
		Long: `hint serve listens for JSON-RPC clients. On macOS and Linux the
default is a Unix socket; on Windows it is a loopback TCP port. The
auth token is written next to the socket, mode 0600, and every client
must present it in hello.

Permissions are answered by the client that started the turn, not on
this process's stdin.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			opts := options{
				flags: *flags,
				mode:  runMode(ask, autoEdit, yolo),
				debug: debug,
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serveListen(ctx, opts, listen)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "Address to listen on, as unix:/path or tcp:127.0.0.1:port")
	cmd.Flags().BoolVar(&ask, "ask", false, "Confirm every file edit and shell command (the default)")
	cmd.Flags().BoolVar(&autoEdit, "auto-edit", false, "Apply file edits without asking; still confirm shell commands")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "Run every edit and command without asking (dangerous)")
	cmd.Flags().BoolVar(&debug, "debug", false, "Write the run's requests, responses and tool calls (keys masked) to a log file")
	cmd.MarkFlagsMutuallyExclusive("ask", "auto-edit", "yolo")
	return cmd
}

// serveStdio is `hint --mode rpc`: the same server, on the process pipes,
// with no token. The parent that holds the pipes is the credential.
func serveStdio(ctx context.Context, opts options) error {
	eng, cleanup, err := newRPCEngine(ctx, opts, os.Stderr)
	if err != nil {
		return err
	}
	defer cleanup()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fmt.Fprintln(os.Stderr, "hint: rpc on stdio")
	srv := &rpc.Server{Engine: eng, Version: buildVersion(), Stop: cancel}
	return srv.ServeStdio(ctx, os.Stdin, os.Stdout)
}

func serveListen(ctx context.Context, opts options, listen string) error {
	eng, cleanup, err := newRPCEngine(ctx, opts, os.Stderr)
	if err != nil {
		return err
	}
	defer cleanup()

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	dir := rpc.RuntimeDir(home, os.LookupEnv)
	token, err := rpc.WriteToken(dir)
	if err != nil {
		return err
	}
	network, addr, err := parseListen(listen, dir)
	if err != nil {
		return err
	}
	lis, err := rpc.Listen(network, addr)
	if err != nil {
		return err
	}
	defer lis.Close()
	if lis.Addr().Network() == "unix" {
		defer os.Remove(lis.Addr().String())
	}
	if err := rpc.WriteEndpoint(dir, lis.Addr().Network(), lis.Addr().String()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "hint: listening on %s %s\n", lis.Addr().Network(), lis.Addr().String())
	fmt.Fprintf(os.Stderr, "hint: auth token: %s\n", filepath.Join(dir, "auth.token"))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := &rpc.Server{Engine: eng, Token: token, Version: buildVersion(), Stop: cancel}
	return srv.Serve(ctx, lis)
}

func parseListen(listen, dir string) (network, addr string, err error) {
	if listen == "" {
		network, addr = rpc.DefaultListen(dir)
		return network, addr, nil
	}
	network, addr, ok := strings.Cut(listen, ":")
	if !ok || (network != "unix" && network != "tcp") || addr == "" {
		return "", "", fmt.Errorf("--listen must be unix:/path or tcp:127.0.0.1:port, not %q", listen)
	}
	return network, addr, nil
}

// newRPCEngine assembles the provider, tools and project context a server
// shares across sessions. cleanup releases the debug log.
func newRPCEngine(ctx context.Context, opts options, notices *os.File) (*rpcEngine, func(), error) {
	cfg, err := config.Load(opts.flags)
	if err != nil {
		return nil, nil, fmt.Errorf("loading configuration: %w", err)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(notices, "warning: %s\n", w)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("working directory: %w", err)
	}

	var trace *debuglog.Log
	cleanup := func() {}
	if cfg.Debug || opts.debug {
		trace, err = debuglog.Open(debuglog.DefaultDir(home, os.LookupEnv), time.Now(), cfg.Secrets())
		if err != nil {
			fmt.Fprintf(notices, "hint: warning: no debug log: %v\n", err)
		} else {
			cleanup = func() { _ = trace.Close() }
			fmt.Fprintf(notices, "hint: debug log: %s\n", trace.Path())
		}
	}
	var debugLog func(string)
	if trace != nil {
		debugLog = func(line string) { trace.Printf("%s", line) }
	}
	chat, err := provider.Chat(cfg, notices, debugLog)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	root, err := tool.NewRoot(cwd)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("working directory: %w", err)
	}
	global := ""
	if dir := config.GlobalDir(home, os.LookupEnv); dir != "" {
		global = filepath.Join(dir, project.InstructionNames[0])
	}
	var overview project.Overview = project.None{}
	if cfg.Overview.Depth > 0 {
		overview = project.Tree{Depth: cfg.Overview.Depth, MaxEntries: cfg.Overview.MaxEntries}
	}
	pc, err := project.Load(ctx, root.Dir(), projectOptions(assembly{
		chat: chat, cfg: cfg, opts: opts, global: global,
	}, overview)...)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	for _, w := range pc.Warnings {
		fmt.Fprintf(notices, "hint: warning: %s\n", w)
	}
	registry, err := toolRegistry(root, pc.InstructionPaths)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("registering tools: %w", err)
	}
	if opts.mode == permission.ModeYolo {
		fmt.Fprintln(notices, "hint: WARNING: --yolo: file edits and shell commands will run WITHOUT confirmation")
	}
	limits := agent.DefaultLimits()
	if p, err := cfg.Default(); err == nil && p.ContextWindow > 0 {
		limits.MaxContextTokens = p.ContextWindow
	}
	return &rpcEngine{
		cwd: cwd, store: session.NewStore(session.DefaultDir(home, os.LookupEnv)),
		chat: chat, registry: registry, pc: pc, mode: opts.mode, limits: limits, notices: notices,
	}, cleanup, nil
}
