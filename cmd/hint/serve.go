package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/rpc"
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
this process's stdin. Another terminal joins with hint --connect.`,
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
	warnYolo(opts.mode)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fmt.Fprintln(os.Stderr, "hint: rpc on stdio")
	srv := &rpc.Server{Engine: eng, Version: buildVersion(), Stop: cancel}
	defer srv.Close()
	return srv.ServeStdio(ctx, os.Stdin, os.Stdout)
}

func serveListen(ctx context.Context, opts options, listen string) error {
	eng, cleanup, err := newRPCEngine(ctx, opts, os.Stderr)
	if err != nil {
		return err
	}
	defer cleanup()
	warnYolo(opts.mode)

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
	defer func() { _ = lis.Close() }()
	if lis.Addr().Network() == "unix" {
		defer func() { _ = os.Remove(lis.Addr().String()) }()
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

// warnYolo is the server's share of warnMode: a server has no stdin of
// its own to warn about, but --yolo deserves the same loud line.
func warnYolo(mode permission.Mode) {
	if mode == permission.ModeYolo {
		fmt.Fprintln(os.Stderr, "hint: WARNING: --yolo: file edits and shell commands will run WITHOUT confirmation")
	}
}

// parseListen reads a unix:/path or tcp:host:port address; empty is the
// platform default under dir.
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
