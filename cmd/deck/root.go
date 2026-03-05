package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/syndg/deck/internal/config"
	"github.com/syndg/deck/internal/daemon"
)

var (
	cfgPath   string
	daemonURL string
)

var rootCmd = &cobra.Command{
	Use:   "deck",
	Short: "Deck - agentic workflow orchestrator",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "~/.config/deck/config.yaml", "config file path")
	rootCmd.PersistentFlags().StringVar(&daemonURL, "daemon-url", "http://localhost:9800", "daemon HTTP address")

	rootCmd.AddCommand(daemonCmd)
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start the Deck daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		cfg.ExpandPaths()

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
