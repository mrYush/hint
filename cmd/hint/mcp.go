package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/mcp"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// mcpServers converts the loaded configuration into the manager's shape.
// The permission string becomes the action class the agent's gate reads;
// an empty one is execute, applied by the config loader and again by the
// manager as a safety net.
func mcpServers(cfg *config.Config) []mcp.ServerConfig {
	out := make([]mcp.ServerConfig, 0, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		out = append(out, mcp.ServerConfig{
			Name:      s.Name,
			Transport: mcp.Transport(s.Transport),
			Command:   s.Command,
			Args:      append([]string(nil), s.Args...),
			Env:       copyStrings(s.Env),
			URL:       s.URL,
			Headers:   copyStrings(s.Headers),

			Permission:     agentapi.ActionClass(s.Permission),
			Timeout:        s.Timeout,
			Tools:          mcp.ToolFilter{Allow: append([]string(nil), s.Tools.Allow...), Deny: append([]string(nil), s.Tools.Deny...)},
			DescriptionMax: s.DescriptionMax,
			Project:        s.Project,
		})
	}
	return out
}

func copyStrings(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ensureMCP starts the configured MCP servers and registers their tools
// once. It runs before the first turn, not while the engine is built, so
// a run that never needs MCP pays no process startup cost. A canceled
// context is retried on the next turn; a registration error is permanent
// and named on every turn until the run restarts.
func (e *rpcEngine) ensureMCP(ctx context.Context) error {
	e.mcpMu.Lock()
	defer e.mcpMu.Unlock()
	if e.mcpLoaded {
		return e.mcpErr
	}
	if e.mcp == nil {
		e.mcpLoaded = true
		return nil
	}
	tools, err := e.mcp.Tools(ctx)
	if err != nil {
		return fmt.Errorf("MCP tools: %w", err)
	}
	warnings := e.mcp.Warnings()
	if e.mcpWarned < len(warnings) {
		secrets := e.secrets()
		for _, w := range warnings[e.mcpWarned:] {
			fmt.Fprintf(e.notices, "hint: warning: %s\n", config.Redact(w, secrets))
		}
		e.mcpWarned = len(warnings)
	}
	for _, t := range tools {
		if err := e.registry.Register(tool.Redacting(t, e.secrets)); err != nil {
			e.mcpErr = fmt.Errorf("registering MCP tools: %w", err)
			e.mcpLoaded = true
			return e.mcpErr
		}
	}
	e.mcpLoaded = true
	return nil
}

// newMCPCommand groups the MCP server commands: list is config-only,
// status connects, and enable records consent for a project server.
func newMCPCommand(flags *config.Flags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Inspect and approve MCP servers",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newMCPListCommand(flags), newMCPStatusCommand(flags), newMCPEnableCommand(flags))
	return cmd
}

func newMCPListCommand(flags *config.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers without connecting to them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cfg, err := config.LoadDraft(*flags)
			if err != nil {
				return fmt.Errorf("loading configuration: %w", err)
			}
			for _, w := range cfg.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			root, err := canonicalDir()
			if err != nil {
				return err
			}
			manager, err := newMCPManager(cfg, root)
			if err != nil {
				return err
			}
			defer func() { _ = manager.Close() }()
			infos := manager.Servers()
			if len(infos) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "hint: no MCP servers configured; see mcp_servers in config.example.yaml")
				return nil
			}
			return printMCPList(cmd.OutOrStdout(), infos)
		},
	}
}

func newMCPStatusCommand(flags *config.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Connect to each MCP server and report its state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cfg, err := config.LoadDraft(*flags)
			if err != nil {
				return fmt.Errorf("loading configuration: %w", err)
			}
			for _, w := range cfg.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			root, err := canonicalDir()
			if err != nil {
				return err
			}
			manager, err := newMCPManager(cfg, root)
			if err != nil {
				return err
			}
			defer func() { _ = manager.Close() }()
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			statuses, err := manager.Status(ctx)
			if err != nil {
				return err
			}
			for _, w := range manager.Warnings() {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", config.Redact(w, cfg.Secrets()))
			}
			if len(statuses) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "hint: no MCP servers configured; see mcp_servers in config.example.yaml")
				return nil
			}
			return printMCPStatus(cmd.OutOrStdout(), statuses)
		},
	}
}

func newMCPEnableCommand(flags *config.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "enable <name>",
		Short: "Approve a project-added MCP server for this repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cfg, err := config.LoadDraft(*flags)
			if err != nil {
				return fmt.Errorf("loading configuration: %w", err)
			}
			for _, w := range cfg.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			root, err := canonicalDir()
			if err != nil {
				return err
			}
			srv, approved, err := enableMCPServer(mcpServers(cfg), root, args[0], mcpConsent(homeDir()))
			if err != nil {
				return err
			}
			if !approved {
				fmt.Fprintf(cmd.OutOrStdout(), "MCP server %q is defined in the global config and is enabled by its presence\n", srv.Name)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "MCP server %q approved for %s; it starts on the next run\n", srv.Name, root)
			return nil
		},
	}
}

// enableMCPServer validates name and records consent when it is a project
// server. It returns the server and whether a new approval was written;
// a global server needs none.
func enableMCPServer(servers []mcp.ServerConfig, root, name string, store mcp.Consent) (mcp.ServerConfig, bool, error) {
	for _, srv := range servers {
		if srv.Name != name {
			continue
		}
		if err := srv.Validate(); err != nil {
			return mcp.ServerConfig{}, false, err
		}
		if !srv.Project {
			return srv, false, nil
		}
		if store == nil {
			return mcp.ServerConfig{}, false, errors.New("mcp: no config directory to record consent in")
		}
		if err := store.Enable(root, srv); err != nil {
			return mcp.ServerConfig{}, false, fmt.Errorf("approving MCP server %q: %w", name, err)
		}
		return srv, true, nil
	}
	return mcp.ServerConfig{}, false, fmt.Errorf("no MCP server named %q", name)
}

func printMCPList(out io.Writer, infos []mcp.ServerInfo) error {
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTRANSPORT\tPERMISSION\tSTATE\tTOOLS")
	for _, info := range infos {
		state := "configured"
		if !info.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", info.Name, info.Transport, info.Permission, state, toolsSummary(info.Tools))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, info := range infos {
		if !info.Enabled && info.Reason != "" {
			fmt.Fprintf(out, "  %s: %s\n", info.Name, info.Reason)
		}
	}
	return nil
}

func printMCPStatus(out io.Writer, statuses []mcp.ServerStatus) error {
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTRANSPORT\tPERMISSION\tSTATE\tPROTOCOL\tSERVER\tTOOLS\tBYTES\tHEALTH")
	for _, st := range statuses {
		protocol, server, health := st.Protocol, st.ServerName, st.Health
		if protocol == "" {
			protocol = "-"
		}
		if server == "" {
			server = "-"
		}
		if health == "" {
			health = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			st.Name, st.Transport, st.Permission, st.State, protocol, server, st.Tools, st.Bytes, health)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, st := range statuses {
		if st.Reason != "" {
			fmt.Fprintf(out, "  %s: %s\n", st.Name, st.Reason)
		}
	}
	return nil
}

func toolsSummary(filter mcp.ToolFilter) string {
	switch {
	case len(filter.Allow) > 0:
		return "allow " + strings.Join(filter.Allow, ", ")
	case len(filter.Deny) > 0:
		return "deny " + strings.Join(filter.Deny, ", ")
	default:
		return "all"
	}
}

// newMCPManager builds a manager for the config-only commands. It does
// not connect until Status or the engine's first turn asks it to.
func newMCPManager(cfg *config.Config, root string) (*mcp.Manager, error) {
	return mcp.NewManager(mcpServers(cfg), mcp.Options{
		Client:      mcp.ClientInfo{Name: "hint", Version: buildVersion()},
		ProjectRoot: root,
		Environ:     config.FilterEnviron(os.Environ(), cfg.SecretEnv(), cfg.Secrets()),
		Consent:     mcpConsent(homeDir()),
		Budget:      mcp.DefaultBudget(),
	})
}

// mcpConsentPath is the consent record outside every repository; an
// empty string means there is no config directory to put it in.
func mcpConsentPath(home string) string {
	if dir := config.GlobalDir(home, os.LookupEnv); dir != "" {
		return filepath.Join(dir, "mcp-consent.json")
	}
	return ""
}

func mcpConsent(home string) mcp.Consent {
	if path := mcpConsentPath(home); path != "" {
		return mcp.NewConsentStore(path)
	}
	return nil
}

// canonicalDir resolves the working directory the way tool.NewRoot does,
// so consent keys match the engine's project root.
func canonicalDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	return real, nil
}
