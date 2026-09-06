import { isHookEnabled, persistHookState, registerHook, toggleHook } from "../../hook-state.ts";
import { AnnoyedStore } from "./store.ts";
import { AnnoyanceNudgeState } from "../../lib/swarm-annoyance-nudge.ts";

/**
 * Swarm's builtin annoyance-nudge hook (internal/hooks/builtin/annoyance_nudge.go)
 * ported 1:1 — see .pi/lib/swarm-annoyance-nudge.ts. Swarm collects every
 * hook context produced while executing one assistant turn's tool calls and
 * appends ONE standalone RoleUser message after the tool-result message
 * (agent_tools.go). Pi's equivalent slot is a steered custom message: the
 * steering queue drains right after turn_end, before the next model call.
 */
export function registerAnnoyanceNudgeHook(pi: any) {
  const state = new AnnoyanceNudgeState();
  const pending: string[] = [];
  registerHook(pi, "annoyance", "tool_result", async (event: any, ctx: any) => {
    const conversation = ctx?.sessionManager?.getSessionId?.() ?? ctx?.sessionId ?? "session";
    const reminder = state.onToolResult(conversation, { toolName: event?.toolName ?? "", isError: event?.isError, content: event?.content, details: event?.details });
    if (reminder) pending.push(reminder);
    return undefined; // never mutate the tool result; the nudge is its own message
  });
  registerHook(pi, "annoyance", "turn_end", async () => {
    if (pending.length === 0) return undefined;
    const content = pending.splice(0).join("\n\n");
    // While the agent is streaming, Pi routes deliverAs:"steer" to
    // agent.steer() only when triggerTurn !== false; with triggerTurn:false
    // it parks the message in _pendingCustomMessages, which is flushed after
    // the run and never enters the in-flight context. steer() does not start
    // a turn — the loop drains the steering queue right after turn_end.
    pi.sendMessage?.({ customType: "swarm-hook-context", content, display: false }, { deliverAs: "steer", triggerTurn: true });
    return undefined;
  });
  pi.registerCommand?.("annoyed", { description: "Control or inspect the local Annoyed board", handler: async (args: string, ctx: any) => {
    const [command, id, value] = args.trim().split(/\s+/, 3);
    if (command === "on" || command === "off") {
      const enabled = toggleHook("annoyance", command === "on");
      persistHookState(pi);
      ctx?.ui?.notify?.(`Annoyance nudge ${enabled ? "on" : "off"}`, "info"); return;
    }
    const store = new AnnoyedStore();
    try {
      if (command === "move") { await store.update(id, { status: value as any }); ctx?.ui?.notify?.(`Moved ${id} to ${value}`, "info"); return; }
      if (command === "read" && id) { const issue = (await store.list()).find(x => x.id === id); if (!issue) throw new Error(`issue not found: ${id}`); ctx?.ui?.notify?.(`${issue.id} · ${issue.status} · ${issue.severity}\\n${issue.title}\\n\\n${issue.issue}\\n\\nObserved: ${issue.observed || "—"}\\nExpected: ${issue.expected || "—"}\\nOccurrences: ${issue.occurrences}`, "info"); return; }
      const issues = await store.list(command && command !== "list" && command !== "all" ? command as any : undefined);
      const columns = ["backlog", "triage", "accepted", "in_progress", "verified", "wont_fix", "duplicate"];
      const lines = columns.map(column => { const rows = issues.filter(x => x.status === column); return `${column} (${rows.length})${rows.slice(0, 8).map(x => `\\n  ${x.id} · ${x.severity} · ${x.title}`).join("")}`; });
      ctx?.ui?.notify?.(`${isHookEnabled("annoyance") ? "Nudge: on" : "Nudge: off"}\\n\\n${lines.join("\\n\\n")}\\n\\nUse /annoyed read <id> for details.\\nJSON: ${store.exportPath}`, "info");
    } catch (error) { ctx?.ui?.notify?.(error instanceof Error ? error.message : String(error), "error"); }
    finally { store.close(); }
  }});
  return { state };
}

// The directory's index.ts is the canonical auto-discovered entry point. This
// default is intentionally inert so Pi does not register the hook twice when it
// also discovers sibling files in the extension directory.
export default function annoyanceNudgeExtension(_pi: any) {}
