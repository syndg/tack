/**
 * Tack Agent Pi Extension
 *
 * Runtime adapter for the agent orchestration protocol.
 * Handles escalation, task completion, and file scope enforcement.
 *
 * Hooks:
 *   - tool_call:  Enforces file scope from TACK_FILE_SCOPE env var
 *
 * Tools:
 *   - tack_escalate:   Escalate an issue to the human operator
 *   - tack_done:       Task completion signal
 */

import { Type } from "@sinclair/typebox";

// --- Environment ---

const DAEMON_URL = process.env.TACK_DAEMON_URL ?? "http://127.0.0.1:9800";
const AGENT_TOKEN = process.env.TACK_DAEMON_TOKEN ?? process.env.TACK_AGENT_TOKEN ?? "";
const AGENT_NAME = process.env.TACK_AGENT_NAME ?? "unknown";
const AGENT_ROLE = process.env.TACK_AGENT_ROLE ?? "";
const OBJECTIVE_ID = process.env.TACK_OBJECTIVE_ID ?? "";
const PROJECT_ID = process.env.TACK_PROJECT_ID ?? "";
const STREAM_ID = process.env.TACK_STREAM_ID ?? "";
const FILE_SCOPE = (process.env.TACK_FILE_SCOPE ?? "")
  .split(",")
  .map((s) => s.trim())
  .filter(Boolean);

// --- Helpers ---

async function tackFetch(
  path: string,
  opts: RequestInit = {}
): Promise<Response> {
  return fetch(`${DAEMON_URL}${path}`, {
    ...opts,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${AGENT_TOKEN}`,
      ...(PROJECT_ID ? { "X-Tack-Project-ID": PROJECT_ID } : {}),
      ...(opts.headers as Record<string, string>),
    },
  });
}

function matchesScope(filePath: string, patterns: string[]): boolean {
  if (patterns.length === 0) return true;
  for (const pattern of patterns) {
    if (pattern.endsWith("/**")) {
      const prefix = pattern.slice(0, -3);
      if (filePath.startsWith(prefix)) return true;
    } else if (pattern.includes("*")) {
      const regex = new RegExp(
        "^" + pattern.replace(/\*/g, "[^/]*").replace(/\?/g, "[^/]") + "$"
      );
      if (regex.test(filePath)) return true;
    } else {
      if (filePath === pattern || filePath.startsWith(pattern + "/"))
        return true;
    }
  }
  return false;
}

// --- Extension Factory ---

export default function tackExtension(pi: any) {
  // --- Lifecycle Hooks ---

  // Planner system prompt augmentation
  pi.on(
    "before_agent_start",
    async (event: { prompt: string; systemPrompt: string }) => {
      let systemPrompt = event.systemPrompt;
      if (AGENT_ROLE === "planner") {
        systemPrompt +=
          `\n\n## CRITICAL: Output Format
Your final response MUST contain a YAML plan inside a fenced code block. Use exactly this format:

\`\`\`yaml
streams:
  - title: "stream name"
    description: "what this stream does"
    file_scope:
      - "src/path/**"
    dependencies: []
quality_gates:
  - "command to validate"
\`\`\`

You may include analysis and reasoning text before the YAML block, but the YAML block is REQUIRED and must be valid YAML matching the schema above. The orchestrator parses this block to create the execution plan.`;
      }

      return { systemPrompt };
    }
  );

  // Enforce file scope: block write/edit operations outside allowed paths
  pi.on(
    "tool_call",
    async (event: { toolName: string; input: Record<string, unknown> }) => {
      if (FILE_SCOPE.length === 0) return;

      const writeTools = [
        "bash",
        "write",
        "edit",
        "Write",
        "Edit",
        "file_write",
        "file_edit",
      ];
      if (!writeTools.includes(event.toolName)) return;

      const filePath =
        (event.input.file_path as string) ??
        (event.input.path as string) ??
        (event.input.file as string) ??
        "";
      if (!filePath) return;

      if (!matchesScope(filePath, FILE_SCOPE)) {
        return {
          block: true,
          reason: `File ${filePath} is outside allowed scope: ${FILE_SCOPE.join(", ")}`,
        };
      }
    }
  );

  // --- Tools ---

  // tack_escalate: Escalate an issue to the human operator
  pi.registerTool({
    name: "tack_escalate",
    label: "Tack Escalate",
    description:
      "Escalate an issue to the human operator. Use when you encounter a blocker " +
      "that you cannot resolve within your file scope.",
    parameters: Type.Object({
      reason: Type.String({ description: "Reason for escalation" }),
    }),
    async execute(
      _toolCallId: string,
      params: { reason: string }
    ) {
      const resp = await tackFetch("/mail", {
        method: "POST",
        body: JSON.stringify({
          from: AGENT_NAME,
          to: "@human",
          subject: "Escalation",
          body: params.reason,
          type: "escalation",
          priority: "high",
          objective: OBJECTIVE_ID,
          stream: STREAM_ID,
        }),
      });
      if (!resp.ok) {
        return {
          content: [
            {
              type: "text" as const,
              text: `Failed to escalate: ${resp.status}`,
            },
          ],
          isError: true,
        };
      }
      return {
        content: [
          {
            type: "text" as const,
            text: `Escalated to human: ${params.reason}`,
          },
        ],
      };
    },
  });

  // tack_done: Task completion signal
  pi.registerTool({
    name: "tack_done",
    label: "Tack Done",
    description: "Signal that the current task is complete",
    promptGuidelines: [
      "Call tack_done when you have finished your assigned task to signal completion to the orchestrator.",
    ],
    parameters: Type.Object({
      summary: Type.String({
        description: "Summary of what was accomplished",
      }),
    }),
    async execute(
      _toolCallId: string,
      params: { summary: string },
      _signal: any,
      _onUpdate: any,
      ctx: any
    ) {
      // Use Pi's notify to signal completion — the TACK_DONE: prefix is
      // detected by the Go process handler via JSONL events.
      if (ctx?.ui?.notify) {
        ctx.ui.notify(`TACK_DONE:${params.summary}`);
      }
      return {
        content: [
          {
            type: "text" as const,
            text: `Task complete: ${params.summary}`,
          },
        ],
      };
    },
  });
}
