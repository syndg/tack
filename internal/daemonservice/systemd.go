package daemonservice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type systemdProvider struct {
	opts Options
}

func (p *systemdProvider) Install(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(p.unitPath()), 0o755); err != nil {
		return fmt.Errorf("creating systemd user directory: %w", err)
	}
	if err := os.MkdirAll(p.opts.LogDir, 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}
	if err := os.WriteFile(p.unitPath(), []byte(p.unit()), 0o644); err != nil {
		return fmt.Errorf("writing systemd unit: %w", err)
	}
	if _, err := p.opts.Runner.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %w", err)
	}
	if _, err := p.opts.Runner.Run(ctx, "systemctl", "--user", "enable", SystemdUnit); err != nil {
		return fmt.Errorf("systemctl --user enable: %w", err)
	}
	return nil
}

func (p *systemdProvider) Start(ctx context.Context) error {
	_, err := p.opts.Runner.Run(ctx, "systemctl", "--user", "start", SystemdUnit)
	return wrapCommand("systemctl --user start", err)
}

func (p *systemdProvider) Stop(ctx context.Context) error {
	_, err := p.opts.Runner.Run(ctx, "systemctl", "--user", "stop", SystemdUnit)
	return wrapCommand("systemctl --user stop", err)
}

func (p *systemdProvider) Restart(ctx context.Context) error {
	_, err := p.opts.Runner.Run(ctx, "systemctl", "--user", "restart", SystemdUnit)
	return wrapCommand("systemctl --user restart", err)
}

func (p *systemdProvider) Reload(ctx context.Context) error {
	_, err := p.opts.Runner.Run(ctx, "systemctl", "--user", "reload-or-restart", SystemdUnit)
	return wrapCommand("systemctl --user reload-or-restart", err)
}

func (p *systemdProvider) Status(ctx context.Context) (Status, error) {
	installedOut, installedErr := p.opts.Runner.Run(ctx, "systemctl", "--user", "is-enabled", SystemdUnit)
	runningOut, runningErr := p.opts.Runner.Run(ctx, "systemctl", "--user", "is-active", SystemdUnit)
	statusOut, _ := p.opts.Runner.Run(ctx, "systemctl", "--user", "status", SystemdUnit, "--no-pager")
	status := Status{
		Provider:   "systemd",
		Installed:  installedErr == nil || strings.Contains(statusOut, "Loaded: loaded"),
		Enabled:    strings.TrimSpace(installedOut) == "enabled",
		Running:    runningErr == nil && strings.TrimSpace(runningOut) == "active",
		Healthy:    healthCheck(ctx, p.opts.Listen),
		ConfigPath: p.unitPath(),
		LogPath:    "journalctl --user -u " + SystemdUnit,
		Listen:     p.opts.Listen,
	}
	if strings.TrimSpace(statusOut) != "" {
		status.Details = append(status.Details, firstLines(statusOut, 8)...)
	}
	return status, nil
}

func (p *systemdProvider) unitPath() string {
	return filepath.Join(linuxUserConfigDir(), "systemd", "user", SystemdUnit)
}

func (p *systemdProvider) unit() string {
	env := ""
	if p.opts.ConfigPath != "" {
		env = "Environment=TACK_USER_CONFIG_PATH=" + systemdEscapeEnv(p.opts.ConfigPath) + "\n"
	}
	return fmt.Sprintf(`[Unit]
Description=Tack daemon

[Service]
Type=simple
%sExecStart=%s daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, env, systemdQuote(p.opts.Executable))
}
