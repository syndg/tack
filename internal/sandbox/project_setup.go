package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

type ProjectSetupRunner func(ctx context.Context, command string) (ExecResult, error)

func RunProjectSetup(ctx context.Context, setup ProjectSetup, run ProjectSetupRunner, logger *slog.Logger) error {
	if len(setup.Commands) == 0 && len(setup.Verify) == 0 {
		return nil
	}
	if verifiesPass, err := verifyProjectSetup(ctx, setup.Verify, run, logger); err != nil {
		return err
	} else if verifiesPass {
		if logger != nil {
			logger.Info("project setup verify passed, skipping setup commands")
		}
		return nil
	}
	for _, cmd := range setup.Commands {
		if logger != nil {
			logger.Info("running project setup command", "command", cmd)
		}
		result, err := run(ctx, cmd)
		if err != nil {
			return fmt.Errorf("running project setup command %q: %w", cmd, err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("project setup command %q failed (exit %d): %s", cmd, result.ExitCode, compactStderr(result))
		}
	}
	if len(setup.Verify) > 0 {
		verifiesPass, err := verifyProjectSetup(ctx, setup.Verify, run, logger)
		if err != nil {
			return err
		}
		if !verifiesPass {
			return fmt.Errorf("project setup verification failed after running setup commands")
		}
	}
	return nil
}

func verifyProjectSetup(ctx context.Context, commands []string, run ProjectSetupRunner, logger *slog.Logger) (bool, error) {
	if len(commands) == 0 {
		return false, nil
	}
	for _, cmd := range commands {
		if logger != nil {
			logger.Info("running project setup verify", "command", cmd)
		}
		result, err := run(ctx, cmd)
		if err != nil {
			return false, fmt.Errorf("running project setup verify %q: %w", cmd, err)
		}
		if result.ExitCode != 0 {
			if logger != nil {
				logger.Info("project setup verify failed", "command", cmd, "exit_code", result.ExitCode)
			}
			return false, nil
		}
	}
	return true, nil
}

func compactStderr(result ExecResult) string {
	text := strings.TrimSpace(result.Stderr)
	if text == "" {
		text = strings.TrimSpace(result.Stdout)
	}
	if text == "" {
		return "no output"
	}
	return text
}
