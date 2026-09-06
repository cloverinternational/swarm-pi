import { AnnoyedStore, type AnnoyedStatus, type AnnoyedSeverity } from "./store.ts";
import { registerAnnoyanceNudgeHook } from "./nudge.ts";
import { withSwarmToolSurface } from "../../lib/swarm-tool-surface.ts";

const schema = {
  type: "object", required: ["issue"], additionalProperties: false,
  properties: {
    issue: { type: "string", description: "Concrete product friction or defect" },
    title: { type: "string" }, category: { type: "string", description: "hook_false_positive, tool_failure, inefficiency, misleading_error, missing_capability, or other" },
    severity: { type: "string", enum: ["low", "medium", "high", "critical"] },
    observed: { type: "string" }, expected: { type: "string" }, evidence: { type: "array", items: { type: "string" }, maxItems: 32 },
    acceptance_tests: { type: "array", items: { type: "string" }, maxItems: 32 }, tags: { type: "array", items: { type: "string" }, maxItems: 24 },
  },
};
const text = (value: unknown) => typeof value === "string" ? value : "";
const notify = (ctx: any, message: string, level: "info" | "warning" | "error" = "info") => ctx?.ui?.notify?.(message, level);

export default function annoyedExtension(rawPi: any) {
  const pi = withSwarmToolSurface(rawPi);
  registerAnnoyanceNudgeHook(pi);
  const store = new AnnoyedStore();
  pi.registerTool({
    name: "annoyed", label: "Annoyed", description: "Record actionable product friction in the local Annoyed kanban board. Use once for a concrete defect; do not report successful output prose, expected failures, permission denials, or cancellations.", parameters: schema,
    async execute(toolCallId: string, params: any, signal: AbortSignal, _onUpdate: unknown, ctx: any) {
      if (signal.aborted) throw new Error("annoyed: cancelled");
      const entries = ctx?.sessionManager?.getBranch?.() ?? ctx?.sessionManager?.getEntries?.();
      const conversationId = ctx?.sessionManager?.getSessionId?.() ?? ctx?.sessionId;
      const result = await store.upsert({
        issue: text(params.issue), title: text(params.title), category: text(params.category) || "other", severity: params.severity,
        observed: text(params.observed), expected: text(params.expected), evidence: params.evidence, acceptanceTests: params.acceptance_tests,
        tags: params.tags, conversationId, projectCwd: pi.getCwd?.() ?? process.cwd(), transcript: Array.isArray(entries) ? entries.slice(-80) : undefined,
        metadata: { toolCallId }, source: "pi-annoyed-tool",
      });
      return { content: [{ type: "text", text: `${result.duplicate ? "Duplicate recorded" : "Issue recorded"}: ${result.issue.id} (${result.issue.status})\nBoard: ${store.exportPath}` }], details: { issue: result.issue, duplicate: result.duplicate, database: store.databasePath } };
    },
  });
  pi.on?.("session_shutdown", () => store.close());
  return store;
}
