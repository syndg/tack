package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	snapshotCmd.AddCommand(snapshotCreateCmd, snapshotUpdateCmd, snapshotCheckCmd)
	rootCmd.AddCommand(snapshotCmd)
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage Daytona sandbox snapshots",
}

var snapshotCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a snapshot from the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if cfg.Sandbox.Provider != "daytona" {
			return fmt.Errorf("snapshots are only supported with the daytona provider (current: %s)", cfg.Sandbox.Provider)
		}

		// Get repo URL
		repoURL, err := gitRemoteURL()
		if err != nil {
			return fmt.Errorf("getting repo URL: %w", err)
		}

		// Hash the lockfile
		lockHash, lockFile := hashLockfile()

		fmt.Printf("Repo:     %s\n", repoURL)
		if lockFile != "" {
			fmt.Printf("Lockfile: %s (hash: %s)\n", lockFile, lockHash[:12])
		}
		fmt.Printf("Post-create: %v\n", cfg.Sandbox.PostCreate)
		fmt.Println()

		// Print the Daytona snapshot creation instructions.
		// Actual snapshot creation requires the Daytona CLI or API.
		fmt.Println("To create the snapshot, run:")
		fmt.Println()
		fmt.Printf("  daytona snapshot create \\\n")
		fmt.Printf("    --repo %s \\\n", repoURL)
		if len(cfg.Sandbox.PostCreate) > 0 {
			fmt.Printf("    --post-create %q \\\n", strings.Join(cfg.Sandbox.PostCreate, " && "))
		}
		if lockHash != "" {
			fmt.Printf("    --label deck.lockfile-hash=%s \\\n", lockHash[:12])
		}
		fmt.Printf("    --name deck-snapshot\n")
		fmt.Println()
		fmt.Println("Then set the snapshot in your config:")
		fmt.Println("  deck config set sandbox.daytona.snapshot <snapshot-id>")

		return nil
	},
}

var snapshotUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Rebuild the snapshot (after lockfile changes)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if cfg.Sandbox.Daytona.Snapshot == "" {
			return fmt.Errorf("no snapshot configured — run `deck snapshot create` first")
		}

		lockHash, lockFile := hashLockfile()
		if lockFile == "" {
			fmt.Println("No lockfile found — snapshot may not need updating.")
			return nil
		}

		fmt.Printf("Current lockfile: %s (hash: %s)\n", lockFile, lockHash[:12])
		fmt.Printf("Snapshot:         %s\n", cfg.Sandbox.Daytona.Snapshot)
		fmt.Println()
		fmt.Println("Rebuild the snapshot with the latest dependencies:")
		fmt.Println("  daytona snapshot rebuild", cfg.Sandbox.Daytona.Snapshot)

		return nil
	},
}

var snapshotCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check if the snapshot is stale",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if cfg.Sandbox.Daytona.Snapshot == "" {
			fmt.Println("No snapshot configured.")
			return nil
		}

		lockHash, lockFile := hashLockfile()
		if lockFile == "" {
			fmt.Println("No lockfile found — cannot check staleness.")
			return nil
		}

		fmt.Printf("Snapshot:          %s\n", cfg.Sandbox.Daytona.Snapshot)
		fmt.Printf("Current lockfile:  %s\n", lockFile)
		fmt.Printf("Lockfile hash:     %s\n", lockHash[:12])
		fmt.Println()
		fmt.Println("Compare this hash with the snapshot's deck.lockfile-hash label.")
		fmt.Println("If they differ, run: deck snapshot update")

		return nil
	},
}

// gitRemoteURL returns the origin remote URL.
func gitRemoteURL() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// hashLockfile finds and hashes the project lockfile.
// Returns (hash, filename) or ("", "") if no lockfile found.
func hashLockfile() (string, string) {
	cwd, _ := os.Getwd()
	lockfiles := []string{
		"bun.lockb", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
		"go.sum", "Cargo.lock", "Gemfile.lock", "poetry.lock", "uv.lock",
		"requirements.txt", "Pipfile.lock",
	}

	for _, name := range lockfiles {
		path := filepath.Join(cwd, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		return hash, name
	}
	return "", ""
}
