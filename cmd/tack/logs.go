package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/observability"
)

var logsVerbose bool
var logsFollow bool

func init() {
	logsCmd.Flags().BoolVar(&logsVerbose, "verbose", false, "show full tool arguments and message content")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "tail the log file (live)")
	rootCmd.AddCommand(logsCmd)
}

var logsCmd = &cobra.Command{
	Use:   "logs [agent-id]",
	Short: "Show activity log for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentID := args[0]

		cfg, err := loadUserConfigOnly()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		c, err := newDaemonClient(cmd, false)
		if err != nil {
			return err
		}
		pid, err := resolveTargetProjectID(cmd, c)
		if err != nil {
			return err
		}

		logDir := filepath.Join(cfg.Daemon.DataDir, "activity")
		logFile := observability.ProjectLogPath(logDir, pid)

		f, err := os.Open(logFile)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no activity log found for project %s", pid)
			}
			return fmt.Errorf("opening log: %w", err)
		}
		defer f.Close()

		matched := false
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			formatted, ok := formatLogEntry(line, agentID)
			matched = matched || ok
			if formatted != "" {
				fmt.Println(formatted)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("reading log: %w", err)
		}

		if !logsFollow && !matched {
			return fmt.Errorf("no canonical records found for agent %s in project %s", agentID, pid)
		}

		if logsFollow {
			// Tail the file for new entries
			fmt.Println("--- following (ctrl+c to stop) ---")
			for {
				select {
				case <-cmd.Context().Done():
					return nil
				default:
				}
				if scanner.Scan() {
					line := scanner.Text()
					if line != "" {
						formatted, ok := formatLogEntry(line, agentID)
						matched = matched || ok
						if formatted != "" {
							fmt.Println(formatted)
						}
					}
				} else {
					time.Sleep(500 * time.Millisecond)
					// Re-check for new data
					scanner = bufio.NewScanner(f)
				}
			}
		}

		return nil
	},
}

func formatLogEntry(line string, agentPrefix string) (string, bool) {
	var record observability.Record
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return "", false
	}
	if record.AgentID == "" || !strings.HasPrefix(record.AgentID, agentPrefix) {
		return "", false
	}

	ts := record.Timestamp.Format("15:04:05")

	switch record.Kind {
	case "agent.tool_start":
		if logsVerbose {
			return fmt.Sprintf("%s %s: %s", ts, record.Summary, stringDetail(record.Details["content"])), true
		}
		return fmt.Sprintf("%s %s", ts, record.Summary), true
	case "agent.tool_end":
		duration := intDetail(record.Details["duration"])
		if boolDetail(record.Details["is_error"]) {
			return fmt.Sprintf("%s %s (%s)", ts, record.Summary, formatDuration(duration)), true
		}
		if logsVerbose {
			return fmt.Sprintf("%s %s (%s)", ts, record.Summary, formatDuration(duration)), true
		}
		return "", true
	case "agent.message":
		if logsVerbose {
			return fmt.Sprintf("%s message: %s", ts, truncateText(record.Summary, 200)), true
		}
		return "", true
	case "agent.error":
		return fmt.Sprintf("%s ERROR: %s", ts, record.Summary), true
	}

	return "", true
}

func stringDetail(v any) string {
	s, _ := v.(string)
	return s
}

func boolDetail(v any) bool {
	b, _ := v.(bool)
	return b
}

func intDetail(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}

func formatDuration(ms int64) string {
	if ms <= 0 {
		return "0ms"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
