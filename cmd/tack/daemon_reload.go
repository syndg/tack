package main

import (
	"context"
	"fmt"
)

func reloadDaemonLiveThenService(ctx context.Context) error {
	if c, err := newDaemonClient(rootCmd, false); err == nil {
		if err := c.Reload(ctx); err == nil {
			return nil
		}
	}
	provider, err := newDaemonServiceProvider()
	if err != nil {
		return err
	}
	if err := provider.Reload(ctx); err != nil {
		return fmt.Errorf("live reload and service reload failed: %w", err)
	}
	return nil
}
