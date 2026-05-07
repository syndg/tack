package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/daemonservice"
)

func init() {
	daemonCmd.AddCommand(
		daemonInstallCmd,
		daemonStartCmd,
		daemonStopCmd,
		daemonRestartCmd,
		daemonStatusCmd,
		daemonReloadCmd,
	)
}

var daemonInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the Tack daemon user service",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := newDaemonServiceProvider()
		if err != nil {
			return err
		}
		if err := provider.Install(cmd.Context()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "daemon service installed")
		return nil
	},
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Tack daemon user service",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := newDaemonServiceProvider()
		if err != nil {
			return err
		}
		if err := provider.Start(cmd.Context()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "daemon service started")
		return nil
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the Tack daemon user service",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := newDaemonServiceProvider()
		if err != nil {
			return err
		}
		if err := provider.Stop(cmd.Context()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "daemon service stopped")
		return nil
	},
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the Tack daemon user service",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := newDaemonServiceProvider()
		if err != nil {
			return err
		}
		if err := provider.Restart(cmd.Context()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "daemon service restarted")
		return nil
	},
}

var daemonReloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload the Tack daemon user service",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := newDaemonServiceProvider()
		if err != nil {
			return err
		}
		if err := provider.Reload(cmd.Context()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "daemon service reloaded")
		return nil
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the Tack daemon user service status",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := newDaemonServiceProvider()
		if err != nil {
			return err
		}
		status, err := provider.Status(cmd.Context())
		if err != nil {
			return err
		}
		renderDaemonServiceStatus(cmd, status)
		return nil
	},
}

func newDaemonServiceProvider() (daemonservice.Provider, error) {
	cfg, err := loadUserConfigOnly()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return daemonservice.New(daemonservice.Options{
		ConfigPath: userConfigPath(),
		Listen:     cfg.Daemon.Listen,
	})
}

func renderDaemonServiceStatus(cmd *cobra.Command, status daemonservice.Status) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Provider: %s\n", status.Provider)
	fmt.Fprintf(out, "Installed: %t\n", status.Installed)
	fmt.Fprintf(out, "Enabled: %t\n", status.Enabled)
	fmt.Fprintf(out, "Running: %t\n", status.Running)
	fmt.Fprintf(out, "Healthy: %t\n", status.Healthy)
	fmt.Fprintf(out, "Listen: %s\n", status.Listen)
	fmt.Fprintf(out, "Config: %s\n", status.ConfigPath)
	fmt.Fprintf(out, "Logs: %s\n", status.LogPath)
	if len(status.Details) > 0 {
		fmt.Fprintf(out, "Details:\n%s\n", strings.Join(status.Details, "\n"))
	}
}
