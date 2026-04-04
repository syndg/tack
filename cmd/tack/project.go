package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
	"github.com/syndg/tack/internal/config"
)

func init() {
	projectCmd.AddCommand(projectAddCmd, projectListCmd, projectInspectCmd, projectRelinkCmd, projectRemoveCmd)
	rootCmd.AddCommand(projectCmd)
}

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage registered Tack projects",
}

var projectAddCmd = &cobra.Command{
	Use:   "add [path]",
	Short: "Register a project with the daemon",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root := "."
		if len(args) == 1 {
			root = args[0]
		}
		rootPath, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		configPath := config.ProjectConfigPath(rootPath)
		projectUUID, err := ensureStableProjectID(rootPath)
		if err != nil {
			return err
		}
		c, err := newDaemonClient(cmd, false)
		if err != nil {
			return err
		}
		project, err := c.RegisterProject(cmd.Context(), client.ProjectRegistration{
			ProjectID:  projectUUID,
			RootPath:   rootPath,
			ConfigPath: configPath,
		})
		if err != nil {
			return err
		}
		fmt.Printf("Registered %s at %s\n", project.ID, project.RootPath)
		return nil
	},
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, false)
		if err != nil {
			return err
		}
		projects, err := c.ListProjects(cmd.Context())
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tNAME\tROOT")
		for _, project := range projects {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", project.ID, project.Name, project.RootPath)
		}
		return w.Flush()
	},
}

var projectInspectCmd = &cobra.Command{
	Use:   "inspect [project-id]",
	Short: "Show one registered project",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, false)
		if err != nil {
			return err
		}
		pid := projectID
		if len(args) == 1 {
			pid = args[0]
		}
		if pid == "" {
			pid, err = resolveTargetProjectID(cmd, c)
			if err != nil {
				return err
			}
		}
		project, err := c.GetProject(cmd.Context(), pid)
		if err != nil {
			return err
		}
		fmt.Printf("ID:         %s\n", project.ID)
		fmt.Printf("Name:       %s\n", project.Name)
		fmt.Printf("Root:       %s\n", project.RootPath)
		fmt.Printf("Config:     %s\n", project.ConfigPath)
		return nil
	},
}

var projectRelinkCmd = &cobra.Command{
	Use:   "relink <project-id> <path>",
	Short: "Relink a moved repo to an existing project ID",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		rootPath, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		projectUUID, err := ensureStableProjectID(rootPath)
		if err != nil {
			return err
		}
		if projectUUID != args[0] {
			return fmt.Errorf("repo at %s belongs to project %s, not %s", rootPath, projectUUID, args[0])
		}
		c, err := newDaemonClient(cmd, false)
		if err != nil {
			return err
		}
		project, err := c.RelinkProject(cmd.Context(), args[0], rootPath, config.ProjectConfigPath(rootPath))
		if err != nil {
			return err
		}
		fmt.Printf("Relinked %s to %s\n", project.ID, project.RootPath)
		return nil
	},
}

var projectRemoveCmd = &cobra.Command{
	Use:   "remove <project-id>",
	Short: "Remove a registered project and its runtime state",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, false)
		if err != nil {
			return err
		}
		if err := c.RemoveProject(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Removed %s\n", args[0])
		return nil
	},
}

func ensureStableProjectID(rootPath string) (string, error) {
	projectIDPath := config.ProjectIDPath(rootPath)
	if data, err := os.ReadFile(projectIDPath); err == nil {
		return string(bytesTrimSpace(data)), nil
	}
	if err := os.MkdirAll(filepath.Dir(projectIDPath), 0o755); err != nil {
		return "", fmt.Errorf("creating project metadata dir: %w", err)
	}
	projectUUID := uuid.NewString()
	if err := os.WriteFile(projectIDPath, []byte(projectUUID+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing project id: %w", err)
	}
	return projectUUID, nil
}

func bytesTrimSpace(data []byte) []byte {
	for len(data) > 0 && (data[len(data)-1] == '\n' || data[len(data)-1] == '\r' || data[len(data)-1] == ' ' || data[len(data)-1] == '\t') {
		data = data[:len(data)-1]
	}
	for len(data) > 0 && (data[0] == '\n' || data[0] == '\r' || data[0] == ' ' || data[0] == '\t') {
		data = data[1:]
	}
	return data
}
