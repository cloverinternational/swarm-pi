import { convertToLlm, estimateTokens, prepareCompaction, serializeConversation } from "@earendil-works/pi-coding-agent";

const COMPACTION_TARGET = 330_000;
const SAFETY_MARGIN = 5_000;
const DEFAULT_KEEP_RECENT = 20_000;

/** Enforce the organization target using Pi's turn boundary, which can persist a
 * compaction entry and continue the same agent run. Native compaction remains the
 * fallback for unsupported models and provider overflow. */
export default function mandatoryCompaction(pi: any): void {
  const owners = ((globalThis as any)[Symbol.for("pi-swarm-mandatory-compaction-owners")] ??= new WeakSet<object>()) as WeakSet<object>;
  if (owners.has(pi)) return;
  owners.add(pi);
  let compactedAtThreshold = false;

  pi.on("turn_end", async (event: any, ctx: any) => {
    const usage = ctx.getContextUsage?.();
    const model = ctx.model;
    if (!usage || usage.tokens == null || !model || model.contextWindow < COMPACTION_TARGET) return;
    if (usage.tokens < COMPACTION_TARGET - SAFETY_MARGIN) {
      compactedAtThreshold = false;
      return;
    }
    if (compactedAtThreshold) return;

    const branch = ctx.sessionManager?.getBranch?.();
    if (!Array.isArray(branch) || branch.length === 0) return;
    // Boundary context intentionally exposes no settings API. Pi's public prepareCompaction
    // accepts effective settings; use stable defaults rather than reading private internals.
    const keepRecentTokens = DEFAULT_KEEP_RECENT;
    const reserveTokens = 16_384;
    const preparation = prepareCompaction(branch, { enabled: true, keepRecentTokens, reserveTokens });
    if (!preparation) return;
    // Latch before the expensive summarizer call, including failures/no-progress.
    // Re-arm only after reliable usage falls below the policy band.
    compactedAtThreshold = true;

    const allMessages = [...preparation.messagesToSummarize, ...preparation.turnPrefixMessages];
    const conversation = serializeConversation(convertToLlm(allMessages));
    if (!conversation.trim()) return;

    const prior = preparation.previousSummary ? `\n\nPrevious summary:\n${preparation.previousSummary}` : "";
    const prompt = `Summarize this conversation so another assistant can continue the user's work. Preserve goals, constraints, decisions, important facts, completed work, blockers, and next steps. Be concise but retain actionable detail.${prior}\n\n<conversation>\n${conversation}\n</conversation>`;
    try {
      const response = await ctx.modelRegistry.complete(model, {
        messages: [{ role: "user", content: [{ type: "text", text: prompt }], timestamp: Date.now() }],
      }, { maxTokens: Math.min(8192, model.maxTokens || 8192), signal: ctx.signal, cacheRetention: "none" });
      if (response.stopReason !== "stop") return;
      const summary = response.content.filter((part: any) => part.type === "text").map((part: any) => part.text).join("\n").trim();
      if (!summary) return;

      const keptStart = branch.findIndex((entry: any) => entry.id === preparation.firstKeptEntryId);
      if (keptStart < 0) return;
      const keptTokens = branch.slice(keptStart).reduce((total: number, entry: any) => {
        if (entry.type !== "message") return total;
        return total + estimateTokens(entry.message);
      }, 0);
      const summaryTokens = Math.ceil(summary.length / 4);
      if (keptTokens + summaryTokens >= COMPACTION_TARGET - SAFETY_MARGIN) return;

      const compaction = {
        type: "compaction" as const,
        summary,
        firstKeptEntryId: preparation.firstKeptEntryId,
        details: preparation.fileOps,
        usage: response.usage,
      };
      const existingEntries = event.entries ?? [];
      const hasCompaction = existingEntries.some((entry: any) => entry.type === "compaction");
      if (hasCompaction) return;
      return {
        entries: [...existingEntries, compaction, {
          type: "custom_message" as const,
          customType: "org-compaction-continuation",
          content: "The context was compacted to enforce the organization’s 330k-token target. Continue the current task from the summary and retained recent context.",
          display: false,
        }],
        continue: true,
      };
    } catch {
      // Native compaction/overflow recovery remains available; never spin on a failed summary.
      return;
    }
  });
}
