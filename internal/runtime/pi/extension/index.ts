/**
 * Deck Agent Pi Extension
 *
 * Hooks:
 *   - context: Injects unread mail into context before each LLM call
 *   - tool_call: Enforces file scope from DECK_FILE_SCOPE env var
 *   - agent_end: Cleanup/reporting
 *
 * Tools:
 *   - deck_mail_send: Send mail to another agent or @human
 *   - deck_status: Report status via mail to @human
 *   - deck_escalate: Escalate to @lead or custom target
 *   - deck_done: Signal task completion
 */

const DAEMON_URL = process.env.DECK_DAEMON_URL ?? "http://127.0.0.1:9800";
const AGENT_TOKEN = process.env.DECK_AGENT_TOKEN ?? "";
const AGENT_NAME = process.env.DECK_AGENT_NAME ?? "unknown";
const FILE_SCOPE = (process.env.DECK_FILE_SCOPE ?? "")
  .split(",")
  .map((s) => s.trim())
  .filter(Boolean);

// --- Helpers ---

async function deckFetch(
  path: string,
  opts: RequestInit = {}
): Promise<Response> {
  return fetch(`${DAEMON_URL}${path}`, {
    ...opts,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${AGENT_TOKEN}`,
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

// --- Hooks ---

export const hooks = {
  /**
   * Fetch unread mail and inject into context before each LLM call.
   */
  async context(): Promise<string | null> {
    try {
      const resp = await deckFetch(`/mail/${AGENT_NAME}/unread`);
      if (!resp.ok) return null;
      const messages = await resp.json();
      if (!Array.isArray(messages) || messages.length === 0) return null;

      const mailBlock = messages
        .map(
          (m: { from: string; content: string; type?: string }) =>
            `[Mail from ${m.from}${m.type ? ` (${m.type})` : ""}]: ${m.content}`
        )
        .join("\n");

      return `\n--- Unread Mail ---\n${mailBlock}\n--- End Mail ---\n`;
    } catch {
      return null;
    }
  },

  /**
   * Enforce file scope: block write/edit operations outside allowed paths.
   */
  async tool_call(tool: {
    name: string;
    args: Record<string, unknown>;
  }): Promise<{ allow: boolean; reason?: string }> {
    if (FILE_SCOPE.length === 0) return { allow: true };

    const writeTools = ["Write", "Edit", "write", "edit", "file_write", "file_edit"];
    if (!writeTools.includes(tool.name)) return { allow: true };

    const filePath =
      (tool.args.file_path as string) ??
      (tool.args.path as string) ??
      (tool.args.file as string) ??
      "";
    if (!filePath) return { allow: true };

    if (!matchesScope(filePath, FILE_SCOPE)) {
      return {
        allow: false,
        reason: `File ${filePath} is outside allowed scope: ${FILE_SCOPE.join(", ")}`,
      };
    }

    return { allow: true };
  },

  /**
   * Cleanup hook when agent ends.
   */
  async agent_end(result: {
    success: boolean;
    summary?: string;
  }): Promise<void> {
    try {
      await deckFetch("/mail", {
        method: "POST",
        body: JSON.stringify({
          from: AGENT_NAME,
          to: "@human",
          content: result.success
            ? `Agent completed: ${result.summary ?? "done"}`
            : `Agent failed: ${result.summary ?? "unknown error"}`,
          type: "status",
        }),
      });
    } catch {
      // Best-effort cleanup
    }
  },
};

// --- Tools ---

export const tools = {
  /**
   * Send mail to another agent or @human.
   */
  deck_mail_send: {
    description: "Send a message to another agent or @human via the Deck mail system",
    parameters: {
      type: "object",
      properties: {
        to: {
          type: "string",
          description: "Recipient agent name or @human",
        },
        content: {
          type: "string",
          description: "Message content",
        },
        type: {
          type: "string",
          enum: ["message", "status", "escalation", "question"],
          description: "Message type (default: message)",
        },
      },
      required: ["to", "content"],
    },
    async execute(args: { to: string; content: string; type?: string }) {
      const resp = await deckFetch("/mail", {
        method: "POST",
        body: JSON.stringify({
          from: AGENT_NAME,
          to: args.to,
          content: args.content,
          type: args.type ?? "message",
        }),
      });
      if (!resp.ok) {
        const body = await resp.text();
        throw new Error(`Failed to send mail: ${resp.status} ${body}`);
      }
      return { success: true, message: `Mail sent to ${args.to}` };
    },
  },

  /**
   * Report status to @human.
   */
  deck_status: {
    description: "Report current status to @human",
    parameters: {
      type: "object",
      properties: {
        message: {
          type: "string",
          description: "Status message",
        },
      },
      required: ["message"],
    },
    async execute(args: { message: string }) {
      const resp = await deckFetch("/mail", {
        method: "POST",
        body: JSON.stringify({
          from: AGENT_NAME,
          to: "@human",
          content: args.message,
          type: "status",
        }),
      });
      if (!resp.ok) {
        throw new Error(`Failed to send status: ${resp.status}`);
      }
      return { success: true };
    },
  },

  /**
   * Escalate to @lead or a custom target.
   */
  deck_escalate: {
    description:
      "Escalate an issue to the lead agent or a specific target",
    parameters: {
      type: "object",
      properties: {
        reason: {
          type: "string",
          description: "Reason for escalation",
        },
        to: {
          type: "string",
          description: "Escalation target (default: @lead)",
        },
      },
      required: ["reason"],
    },
    async execute(args: { reason: string; to?: string }) {
      const target = args.to ?? "@lead";
      const resp = await deckFetch("/mail", {
        method: "POST",
        body: JSON.stringify({
          from: AGENT_NAME,
          to: target,
          content: `ESCALATION: ${args.reason}`,
          type: "escalation",
        }),
      });
      if (!resp.ok) {
        throw new Error(`Failed to escalate: ${resp.status}`);
      }
      return { success: true, escalated_to: target };
    },
  },

  /**
   * Signal task completion.
   */
  deck_done: {
    description: "Signal that the current task is complete",
    parameters: {
      type: "object",
      properties: {
        summary: {
          type: "string",
          description: "Summary of what was accomplished",
        },
      },
      required: ["summary"],
    },
    async execute(args: { summary: string }) {
      // Signal completion via Pi's notify mechanism
      // The DECK_DONE: prefix is detected by the Go process handler
      if (typeof globalThis !== "undefined" && (globalThis as any).pi?.ui?.notify) {
        (globalThis as any).pi.ui.notify(`DECK_DONE:${args.summary}`);
      }
      return { success: true, summary: args.summary };
    },
  },
};
