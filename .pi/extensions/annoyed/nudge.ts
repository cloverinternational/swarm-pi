import { createHash } from "node:crypto";
import { isHookEnabled, persistHookState, registerHook, toggleHook } from "../../hook-state.ts";
import { AnnoyedStore } from "./store.ts";

const normalize = (value: unknown) => String(value ?? "").toLowerCase().replace(/[^a-z0-9]/g, "");
const toolName = (event: any) => event?.toolName ?? event?.tool_name ?? event?.tool ?? "";
const outputText = (event: any) => {
  const value = event?.error ?? event?.toolOutput ?? event?.tool_output ?? event?.result ?? event?.content;
  if (typeof value === "string") return value;
  try { return JSON.stringify(value ?? ""); } catch { return ""; }
};
// Never classify ordinary tool prose as failure. A successful read containing
// words like "error" or "fallback" is not product friction. Prefer Pi's
// structured terminal status and only use narrowly-recognized timeout/output
// diagnostics when the host explicitly marks the result as degraded.
const failed = (event: any) => event?.isError === true || event?.error != null || event?.result?.isError === true || event?.result?.error != null || event?.details?.isError === true || (event?.timedOut === true) || (event?.exitCode != null && Number(event.exitCode) !== 0);
const hash = (value: string) => createHash("sha256").update(value).digest("hex").slice(0, 32);
const safe = (value: unknown, max = 500) => String(value ?? "").replace(/[\x00-\x1f\x7f]/g, " ").replace(/\s+/g, " ").trim().slice(0, max);

export function registerAnnoyanceNudgeHook(pi: any) {
  const lastByKey = new Map<string, string>();
  const handler = async (event: any, ctx: any) => {
    const name = toolName(event); if (!name || normalize(name) === "annoyed" || !failed(event)) return;
    if (event?.error_type === "tool.blocked_by_hook" || event?.error_type === "permission_denied" || event?.cancelled === true) return;
    const conversation = ctx?.sessionId ?? ctx?.sessionManager?.getSessionId?.() ?? "session";
    const reason = safe(outputText(event), 1000); const fingerprint = hash(`${normalize(name)}\0${reason}`);
    const key = `${conversation}\0${normalize(name)}`;
    if (lastByKey.get(key) === fingerprint) return;
    lastByKey.set(key, fingerprint);
    const message = `[ANNOYANCE REVIEW]\nTool: "${safe(name, 100)}"\nFailure fingerprint: "${fingerprint}"\n\nDecide whether this is a real product defect, not merely invalid input, an expected test failure, permission denial, or cancellation. If actionable, call annoyed ONCE with the mechanism, observed and expected behavior, bounded non-secret evidence, and objective acceptance tests. Include the exact reproduction shape and one near-miss that must remain allowed. Do not report vague frustration or duplicate this fingerprint.`;
    // Return model-visible guidance from tool_result without modifying the result.
    return { content: [...(Array.isArray(event.content) ? event.content : []), { type: "text", text: message }], details: { ...(event.details ?? {}), annoyanceNudge: { fingerprint, tool: name } } };
  };
  registerHook(pi, "annoyance", "tool_result", handler);
  registerHook(pi, "annoyance", "tool_execution_end", handler);
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
  return { lastByKey };
}

// The directory's index.ts is the canonical auto-discovered entry point. This
// default is intentionally inert so Pi does not register the hook twice when it
// also discovers sibling files in the extension directory.
export default function annoyanceNudgeExtension(_pi: any) {}
