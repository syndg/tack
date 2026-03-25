package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/daemon"
)

func main() {
	configPath := flag.String("config", "", "project config path override")
	flag.Parse()

	projectCfg := config.ResolveProjectConfig(*configPath)
	cfg, err := config.Load(projectCfg, config.UserConfigPath)
	if err != nil {
		slog.Error("loading config", "error", err)
		os.Exit(1)
	}
	cfg.ExpandPaths()

	d, err := daemon.New(cfg)
	if err != nil {
		slog.Error("creating daemon", "error", err)
		os.Exit(1)
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
			slog.Error("daemon exited with error", "error", err)
			os.Exit(1)
		}
		slog.Info("daemon stopped")
		return
	case sig := <-sigCh:
		slog.Info("received signal, shutting down", "signal", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := d.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
		os.Exit(1)
	}

	if err := <-errCh; err != nil {
		slog.Error("daemon exited with error", "error", err)
		os.Exit(1)
	}

	slog.Info("daemon stopped")
}
