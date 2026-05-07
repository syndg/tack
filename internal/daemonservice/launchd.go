package daemonservice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type launchdProvider struct {
	opts Options
}

func (p *launchdProvider) Install(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(p.plistPath()), 0o755); err != nil {
		return fmt.Errorf("creating launchd directory: %w", err)
	}
	if err := os.MkdirAll(p.opts.LogDir, 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}
	if err := os.WriteFile(p.plistPath(), []byte(p.plist()), 0o644); err != nil {
		return fmt.Errorf("writing launchd plist: %w", err)
	}
	_, err := p.opts.Runner.Run(ctx, "launchctl", "bootstrap", "gui/"+userUID(), p.plistPath())
	if err != nil && !strings.Contains(err.Error(), "Bootstrap failed: 5") {
		return fmt.Errorf("launchctl bootstrap: %w", err)
	}
	_, err = p.opts.Runner.Run(ctx, "launchctl", "enable", "gui/"+userUID()+"/"+LaunchdID)
	if err != nil {
		return fmt.Errorf("launchctl enable: %w", err)
	}
	return nil
}

func (p *launchdProvider) Start(ctx context.Context) error {
	_, err := p.opts.Runner.Run(ctx, "launchctl", "kickstart", "-k", "gui/"+userUID()+"/"+LaunchdID)
	return wrapCommand("launchctl kickstart", err)
}

func (p *launchdProvider) Stop(ctx context.Context) error {
	_, err := p.opts.Runner.Run(ctx, "launchctl", "kill", "TERM", "gui/"+userUID()+"/"+LaunchdID)
	return wrapCommand("launchctl kill", err)
}

func (p *launchdProvider) Restart(ctx context.Context) error {
	return p.Start(ctx)
}

func (p *launchdProvider) Reload(ctx context.Context) error {
	_, err := p.opts.Runner.Run(ctx, "launchctl", "kickstart", "-k", "gui/"+userUID()+"/"+LaunchdID)
	return wrapCommand("launchctl kickstart", err)
}

func (p *launchdProvider) Status(ctx context.Context) (Status, error) {
	out, err := p.opts.Runner.Run(ctx, "launchctl", "print", "gui/"+userUID()+"/"+LaunchdID)
	installed := err == nil
	running := strings.Contains(out, "state = running") || strings.Contains(out, "pid =")
	status := Status{
		Provider:   "launchd",
		Installed:  installed,
		Enabled:    installed,
		Running:    running,
		Healthy:    healthCheck(ctx, p.opts.Listen),
		ConfigPath: p.plistPath(),
		LogPath:    p.logPath(),
		Listen:     p.opts.Listen,
	}
	if strings.TrimSpace(out) != "" {
		status.Details = append(status.Details, firstLines(out, 8)...)
	}
	return status, nil
}

func (p *launchdProvider) plistPath() string {
	return filepath.Join(userHomeDir(), "Library", "LaunchAgents", LaunchdID+".plist")
}

func (p *launchdProvider) logPath() string {
	return filepath.Join(p.opts.LogDir, Name+".log")
}

func (p *launchdProvider) plist() string {
	env := ""
	if p.opts.ConfigPath != "" {
		env = fmt.Sprintf("\n  <key>EnvironmentVariables</key>\n  <dict>\n    <key>TACK_USER_CONFIG_PATH</key>\n    <string>%s</string>\n  </dict>", xmlEscape(p.opts.ConfigPath))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
%s
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>daemon</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, LaunchdID, env, xmlEscape(p.opts.Executable), xmlEscape(p.logPath()), xmlEscape(p.logPath()))
}
