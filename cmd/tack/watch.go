package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/domain"
)

var watchVerbose bool
var watchSummary bool
var watchStream string
var watchAgent string
var watchObjective string

func init() {
	watchCmd.Flags().BoolVar(&watchVerbose, "verbose", false, "show all tool calls and message content")
	watchCmd.Flags().BoolVar(&watchSummary, "summary", false, "show only objective-level status changes")
	watchCmd.Flags().StringVar(&watchStream, "stream", "", "filter by stream ID")
	watchCmd.Flags().StringVar(&watchAgent, "agent", "", "filter by agent ID")
	watchCmd.Flags().StringVar(&watchObjective, "objective", "", "filter by objective ID")
	rootCmd.AddCommand(watchCmd)
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Live stream of agent activity",
	RunE: func(cmd *cobra.Command, args []string) error {
		eventsURL := effectiveDaemonURL() + "/events"
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, eventsURL, nil)
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		if c, err := newDaemonClient(cmd, false); err == nil {
			if pid, err := resolveTargetProjectID(cmd, c); err == nil {
				req.Header.Set("X-Tack-Project-ID", pid)
			}
		}
		if err := applyDaemonAuth(req); err != nil {
			return err
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("connecting to %s: %w", eventsURL, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status: %d", resp.StatusCode)
		}

		fmt.Printf("Connected to %s — watching events (ctrl+c to stop)\n\n", effectiveDaemonURL())

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var event domain.Event
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			if !matchesWatchFilter(event) {
				continue
			}

			formatted := formatWatchEvent(event)
			if formatted != "" {
				fmt.Println(formatted)
			}
		}

		return scanner.Err()
	},
}

func matchesWatchFilter(event domain.Event) bool {
	if watchObjective != "" && !strings.HasPrefix(event.Objective, watchObjective) {
		return false
	}
	if watchStream != "" && !strings.HasPrefix(event.Stream, watchStream) {
		return false
	}
	if watchAgent != "" && !strings.HasPrefix(event.Agent, watchAgent) {
		return false
	}
	return true
}

func formatWatchEvent(event domain.Event) string {
	ts := event.CreatedAt.Format("15:04:05")
	stream := shortID(event.Stream)
	payload := watchPayload(event)
	role := stringPayload(payload, "role")
	if role == "" {
		role = shortID(event.Agent)
	}

	switch event.Type {
	case domain.EventAgentActivity:
		switch stringPayload(payload, "kind") {
		case "agent.tool_start":
			summary := stringPayload(payload, "summary")
			if watchSummary {
				return fmt.Sprintf("%s [%s] %s: %s", ts, stream, role, summary)
			}
			if watchVerbose {
				content := stringPayload(payload, "content")
				if len(content) > 200 {
					content = content[:200] + "..."
				}
				return fmt.Sprintf("%s [%s] %s: %s %s", ts, stream, role, summary, content)
			}
			return fmt.Sprintf("%s [%s] %s: %s", ts, stream, role, summary)
		case "agent.tool_end":
			if watchSummary {
				return ""
			}
			summary := stringPayload(payload, "summary")
			if boolPayload(payload, "is_error") {
				return fmt.Sprintf("%s [%s] %s: %s", ts, stream, role, summary)
			}
			if !watchVerbose {
				return ""
			}
			return fmt.Sprintf("%s [%s] %s: %s", ts, stream, role, summary)
		case "agent.message":
			if watchSummary {
				return ""
			}
			if !watchVerbose {
				return ""
			}
			content := stringPayload(payload, "summary")
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			return fmt.Sprintf("%s [%s] %s: %s", ts, stream, role, content)
		case "agent.error":
			return fmt.Sprintf("%s [%s] %s: ERROR %s", ts, stream, role, stringPayload(payload, "summary"))
		}
		return ""

	case domain.EventAgentSpawned:
		return fmt.Sprintf("%s [%s] %s spawned", ts, stream, role)
	case domain.EventAgentCompleted:
		return fmt.Sprintf("%s [%s] %s completed", ts, stream, role)
	case domain.EventAgentFailed:
		return fmt.Sprintf("%s [%s] %s failed", ts, stream, role)

	case domain.EventObjectiveCreated:
		return fmt.Sprintf("%s %s", ts, stringPayload(payload, "summary"))
	case domain.EventObjectiveUpdated:
		return fmt.Sprintf("%s %s", ts, stringPayload(payload, "summary"))

	case domain.EventPlanCreated:
		if watchSummary {
			return fmt.Sprintf("%s plan ready for approval", ts)
		}
		return fmt.Sprintf("%s %s", ts, stringPayload(payload, "summary"))
	case domain.EventPlanApproved:
		return fmt.Sprintf("%s %s", ts, stringPayload(payload, "summary"))

	case domain.EventStreamReady:
		return fmt.Sprintf("%s [%s] %s", ts, stream, stringPayload(payload, "summary"))
	case domain.EventMergeQueued:
		return fmt.Sprintf("%s [%s] %s", ts, stream, stringPayload(payload, "summary"))
	case domain.EventMergeCompleted:
		return fmt.Sprintf("%s [%s] %s", ts, stream, stringPayload(payload, "summary"))
	case domain.EventMergePublished:
		return fmt.Sprintf("%s [%s] %s ✓", ts, stream, stringPayload(payload, "summary"))
	case domain.EventMergeFailed:
		return fmt.Sprintf("%s [%s] %s", ts, stream, stringPayload(payload, "summary"))

	case domain.EventEscalation:
		return fmt.Sprintf("%s [%s] ESCALATION: %s", ts, stream, truncate(stringPayload(payload, "summary"), 100))
	case domain.EventRecoveryAttempt:
		return fmt.Sprintf("%s [%s] %s", ts, stream, stringPayload(payload, "summary"))
	case domain.EventRecoveryBlocked:
		return fmt.Sprintf("%s [%s] recovery blocked: %s", ts, stream, stringPayload(payload, "summary"))
	case domain.EventRecoveryResumed:
		return fmt.Sprintf("%s [%s] recovery resumed: %s", ts, stream, stringPayload(payload, "summary"))

	case domain.EventExecutionStarted:
		if watchSummary {
			return fmt.Sprintf("%s execution started", ts)
		}
		return fmt.Sprintf("%s %s", ts, stringPayload(payload, "summary"))

	case domain.EventMailSent:
		if watchSummary || !watchVerbose {
			return ""
		}
		return fmt.Sprintf("%s [%s] %s", ts, stream, stringPayload(payload, "summary"))
	}

	return ""
}

func watchPayload(event domain.Event) map[string]any {
	var payload map[string]any
	_ = json.Unmarshal([]byte(event.Payload), &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	return payload
}

func stringPayload(payload map[string]any, key string) string {
	v, _ := payload[key].(string)
	return v
}

func boolPayload(payload map[string]any, key string) bool {
	v, _ := payload[key].(bool)
	return v
}

// toolSummary extracts a human-readable summary from tool name + args JSON.
// e.g., "read" + '{"path":"src/index.ts"}' → "read src/index.ts"
// e.g., "bash" + '{"command":"bunx tsc --noEmit"}' → "bash: bunx tsc --noEmit"
func toolSummary(tool, content string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(content), &args); err != nil {
		// Content might be "toolName: rawArgs" format from Pi
		if idx := strings.Index(content, ": "); idx > 0 {
			raw := content[idx+2:]
			// Try parsing the raw part as JSON
			if err2 := json.Unmarshal([]byte(raw), &args); err2 != nil {
				return tool
			}
		} else {
			return tool
		}
	}

	switch tool {
	case "read", "Read":
		if p, ok := args["path"].(string); ok {
			return "read " + p
		}
		if p, ok := args["file_path"].(string); ok {
			return "read " + p
		}
	case "edit", "Edit":
		if p, ok := args["path"].(string); ok {
			return "edit " + p
		}
		if p, ok := args["file_path"].(string); ok {
			return "edit " + p
		}
	case "write", "Write":
		if p, ok := args["path"].(string); ok {
			return "write " + p
		}
		if p, ok := args["file_path"].(string); ok {
			return "write " + p
		}
	case "bash", "Bash":
		if cmd, ok := args["command"].(string); ok {
			if len(cmd) > 80 {
				cmd = cmd[:80] + "..."
			}
			return "$ " + cmd
		}
	case "tack_done":
		if s, ok := args["summary"].(string); ok {
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			return "done: " + s
		}
	case "tack_escalate":
		if r, ok := args["reason"].(string); ok {
			if len(r) > 80 {
				r = r[:80] + "..."
			}
			return "escalate: " + r
		}
	}

	return tool
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
