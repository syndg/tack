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
	"github.com/syndg/deck/internal/services/agents"
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

		cfg, err := loadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		logDir := filepath.Join(cfg.Daemon.DataDir, "activity")

		// Find the log file — support prefix matching
		logFile := filepath.Join(logDir, agentID+".jsonl")
		if _, err := os.Stat(logFile); os.IsNotExist(err) {
			// Try prefix match
			entries, _ := os.ReadDir(logDir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), agentID) && strings.HasSuffix(e.Name(), ".jsonl") {
					logFile = filepath.Join(logDir, e.Name())
					break
				}
			}
		}

		f, err := os.Open(logFile)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no activity log found for agent %s", agentID)
			}
			return fmt.Errorf("opening log: %w", err)
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			formatted := formatLogEntry(line)
			if formatted != "" {
				fmt.Println(formatted)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("reading log: %w", err)
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
						formatted := formatLogEntry(line)
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

func formatLogEntry(line string) string {
	var event agents.ActivityEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return ""
	}

	ts := event.Timestamp.Format("15:04:05")

	switch event.Kind {
	case "tool_start":
		if logsVerbose {
			return fmt.Sprintf("%s %s: %s", ts, event.Tool, event.Content)
		}
		return fmt.Sprintf("%s %s", ts, event.Tool)
	case "tool_end":
		if event.IsError {
			return fmt.Sprintf("%s %s (failed, %dms)", ts, event.Tool, event.Duration)
		}
		if logsVerbose {
			return fmt.Sprintf("%s %s done (%dms)", ts, event.Tool, event.Duration)
		}
		return ""
	case "message":
		if logsVerbose {
			content := event.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			return fmt.Sprintf("%s message: %s", ts, content)
		}
		return ""
	case "error":
		return fmt.Sprintf("%s ERROR: %s", ts, event.Content)
	}

	return ""
}
