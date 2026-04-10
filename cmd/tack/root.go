package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/daemon"
	"github.com/syndg/tack/internal/daemonauth"
)

var (
	cfgPath   string
	daemonURL string
	projectID string
)

var rootCmd = &cobra.Command{
	Use:   "tack",
	Short: "Tack - agentic workflow orchestrator",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "", "project config path override (default: walk up for .tack/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&daemonURL, "daemon-url", "http://localhost:9800", "daemon HTTP address")
	rootCmd.PersistentFlags().StringVar(&projectID, "project", "", "target registered project ID or path")

	rootCmd.AddCommand(daemonCmd)
}

// loadConfig resolves the two-layer config: project (walk-up or --config override) + user.
func loadConfig() (*config.Config, error) {
	projectCfg, err := config.ResolveProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	userCfg := config.UserConfigPath
	if v := os.Getenv("TACK_USER_CONFIG_PATH"); v != "" {
		userCfg = v
	}
	cfg, err := config.Load(projectCfg, userCfg)
	if err != nil {
		return nil, err
	}
	cfg.ExpandPaths()
	return cfg, nil
}

func loadUserConfigOnly() (*config.Config, error) {
	cfg, err := config.Load("", userConfigPath())
	if err != nil {
		return nil, err
	}
	cfg.ExpandPaths()
	return cfg, nil
}

func newDaemonClient(cmd *cobra.Command, requireProject bool) (*client.Client, error) {
	c := client.New(daemonURL)
	if token, err := daemonauth.Load(); err != nil {
		return nil, err
	} else if token != "" {
		c.SetAuthToken(token)
	}
	if !requireProject {
		return c, nil
	}
	pid, err := resolveTargetProjectID(cmd, c)
	if err != nil {
		return nil, err
	}
	c.SetProjectID(pid)
	return c, nil
}

func applyDaemonAuth(req *http.Request) error {
	token, err := daemonauth.Load()
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

func resolveTargetProjectID(cmd *cobra.Command, c *client.Client) (string, error) {
	if projectID != "" {
		if looksLikePath(projectID) {
			project, err := c.ResolveProjectByPath(cmd.Context(), projectID)
			if err != nil {
				return "", err
			}
			return project.ID, nil
		}
		return projectID, nil
	}
	root, err := currentProjectRoot()
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", fmt.Errorf("not inside a registered Tack project; run tack init or pass --project")
	}
	project, err := c.ResolveProjectByPath(cmd.Context(), root)
	if err != nil {
		return "", err
	}
	return project.ID, nil
}

func currentProjectRoot() (string, error) {
	if cfgPath != "" {
		configPath, err := config.ResolveProjectConfig(cfgPath)
		if err != nil {
			return "", err
		}
		if configPath != "" {
			return filepath.Dir(filepath.Dir(configPath)), nil
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return config.FindProjectRoot(cwd), nil
}

func looksLikePath(v string) bool {
	return strings.HasPrefix(v, ".") || strings.HasPrefix(v, "~") || strings.Contains(v, "/")
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start the Tack daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadUserConfigOnly()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		d, err := daemon.New(cfg)
		if err != nil {
			return fmt.Errorf("creating daemon: %w", err)
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		errCh := make(chan error, 1)
		go func() {
			errCh <- d.Start()
		}()

		select {
		case err := <-errCh:
			if err != nil {
				return fmt.Errorf("daemon exited with error: %w", err)
			}
			slog.Info("daemon stopped")
			return nil
		case sig := <-sigCh:
			slog.Info("received signal, shutting down", "signal", sig)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := d.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutting down daemon: %w", err)
		}

		if err := <-errCh; err != nil {
			return fmt.Errorf("daemon exited with error: %w", err)
		}

		slog.Info("daemon stopped")
		return nil
	},
}
