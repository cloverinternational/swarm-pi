import { isHookEnabled, persistHookState, toggleHook } from "../../../lib/runtime/hook-state.ts";
import { AnnoyedStore } from "./store.ts";

/**
 * Swarm's builtin annoyance-nudge hook (internal/hooks/builtin/annoyance_nudge.go)
 * ported 1:1 — see .pi/lib/swarm-annoyance-nudge.ts. The hook itself now runs
 * inside the ordered builtin pipeline (.pi/extensions/swarm-builtin-hooks.ts,
 * post priority 20) so its reminder shares the single per-turn hook message
 * with the other post-tool hooks, exactly like agent_tools.go. This module
 * keeps the /annoyed command.
 */
export function registerAnnoyanceNudgeHook(pi: any) {
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
  return {};
}

// The directory's index.ts is the canonical auto-discovered entry point. This
// default is intentionally inert so Pi does not register the hook twice when it
// also discovers sibling files in the extension directory.
export default function annoyanceNudgeExtension(_pi: any) {}
