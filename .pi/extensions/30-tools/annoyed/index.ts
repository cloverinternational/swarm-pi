import { AnnoyedStore, type AnnoyedStatus, type AnnoyedSeverity } from "./store.ts";
import { registerAnnoyanceNudgeHook } from "./nudge.ts";
import { withSwarmToolSurface } from "../../../lib/runtime/swarm-tool-surface.ts";
import { randomBytes } from "node:crypto";
import { annoyedPublicTitle, annoyedRepository, annoyedResultXML, normalizeAnnoyedSeverity, publishAnnoyedIssue } from "../../../lib/tools/swarm-annoyed-publish.ts";

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
      // annoyed.go Run: validate, publish through gh, then the XML result.
      // Pi's local board is a host-side side effect the model never sees.
      const errorId = () => `err_${randomBytes(10).toString("hex")}`;
      const fail = (message: string): never => { throw new Error(`Error executing annoyed: ${message} (error_id=${errorId()})`); };
      const issue = text(params?.issue).trim();
      if (issue === "") return fail("annoyed: issue is required");
      const severity = normalizeAnnoyedSeverity(params?.severity);
      let repository: string;
      try { repository = annoyedRepository(); } catch (e) { return fail((e as Error).message); }
      const entries = ctx?.sessionManager?.getBranch?.() ?? ctx?.sessionManager?.getEntries?.();
      const conversationId = ctx?.sessionManager?.getSessionId?.() ?? ctx?.sessionId;
      const result = await store.upsert({
        issue: text(params.issue), title: text(params.title), category: text(params.category) || "other", severity: params.severity,
        observed: text(params.observed), expected: text(params.expected), evidence: params.evidence, acceptanceTests: params.acceptance_tests,
        tags: params.tags, conversationId, projectCwd: pi.getCwd?.() ?? process.cwd(), transcript: Array.isArray(entries) ? entries.slice(-80) : undefined,
        metadata: { toolCallId }, source: "pi-annoyed-tool",
      });
      const body = [`## Agent complaint\n\n${issue}`, params?.observed ? `## Observed\n\n${text(params.observed)}` : "", params?.expected ? `## Expected\n\n${text(params.expected)}` : "",
        Array.isArray(params?.evidence) && params.evidence.length ? `## Evidence\n\n${params.evidence.map((e: unknown) => `- ${String(e)}`).join("\n")}` : "",
        Array.isArray(params?.acceptance_tests) && params.acceptance_tests.length ? `## Acceptance tests\n\n${params.acceptance_tests.map((e: unknown) => `- ${String(e)}`).join("\n")}` : "",
        `## Local board\n\n${result.issue.id} (${result.issue.status}) — ${store.exportPath}`].filter(Boolean).join("\n\n");
      let publicationURL: string;
      try { publicationURL = await publishAnnoyedIssue(repository, annoyedPublicTitle(issue, severity), body); } catch (e) { return fail((e as Error).message); }
      return { content: [{ type: "text", text: annoyedResultXML(issue, publicationURL, repository, severity) }], details: { issue: result.issue, duplicate: result.duplicate, database: store.databasePath, publication_url: publicationURL, repository } };
    },
  });
  pi.on?.("session_shutdown", () => store.close());
  return store;
}
