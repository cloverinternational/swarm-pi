import { METADATA_SYSTEM_PROMPT, type MetadataState, type TranscriptMessage, openAICompletionsTransport, refreshConversationMetadata } from "../../lib/state/swarm-conversation-metadata.ts";
import { injectContextBlocks } from "../../lib/context/swarm-context.ts";
import { currentContextBlocks } from "../10-context/swarm-prompt.ts";

/**
 * After every agent run, issue Swarm's conversation title/summary model call
 * (client/conversation_metadata.go) exactly as `swarm -p` and the TUI do:
 * once from client.Execute and once more from the persistence path, the
 * second being a no-op unless the first failed to parse. See
 * .pi/lib/state/swarm-conversation-metadata.ts for the wire contract.
 */
type Pi = any;
const ENTRY = "pi-swarm-conversation-metadata";
const registrations = new WeakSet<object>();

const textOf = (content: unknown): string => typeof content === "string" ? content : Array.isArray(content) ? content.filter((b: any) => b?.type === "text" && typeof b.text === "string").map((b: any) => b.text).join("") : "";
/**
 * stream.go: a turn whose only content is reasoning is stored with the
 * reasoning AS its text (see swarmMessageShapes), so the metadata excerpt
 * lists it as an assistant line.
 */
const assistantTextOf = (content: unknown): string => {
  const text = textOf(content);
  if (text.trim() !== "" || !Array.isArray(content)) return text;
  return content.filter((b: any) => b?.type === "thinking" && typeof b.thinking === "string").map((b: any) => b.thinking).join("");
};
/** Swarm persists hook/extension-injected turns as RoleUser; tool results are not transcript lines. */
export function transcriptOf(messages: readonly any[]): TranscriptMessage[] {
  const out: TranscriptMessage[] = [];
  for (const m of messages) {
    if (!m || typeof m !== "object") continue;
    if (m.role === "user" || m.role === "custom") out.push({ role: "user", content: textOf(m.content) });
    else if (m.role === "assistant") out.push({ role: "assistant", content: assistantTextOf(m.content) });
  }
  return out;
}

export function registerSwarmConversationMetadata(pi: Pi): void {
  if (registrations.has(pi as object)) return;
  registrations.add(pi as object);
  if (process.env.PI_SWARM_NO_CONVERSATION_METADATA === "1") return;
  let state: MetadataState = {};
  const persist = () => { try { pi.appendEntry?.(ENTRY, { ...state }); } catch { /* headless without session */ } };
  pi.on?.("session_start", (_event: unknown, ctx: any) => {
    state = {};
    const entries: any[] = ctx?.sessionManager?.getBranch?.() ?? ctx?.sessionManager?.getEntries?.() ?? [];
    const last = [...entries].reverse().find((e) => e?.type === "custom" && e?.customType === ENTRY);
    if (last?.data && typeof last.data === "object") state = { ...last.data };
  });
  pi.on?.("agent_end", async (event: { messages?: any[] }, ctx: any) => {
    const model = ctx?.model ?? pi.getModel?.();
    if (!model?.baseUrl || (model.api && model.api !== "openai-completions")) return;
    let auth: { apiKey?: string; headers?: Record<string, string> } = {};
    try { auth = (await ctx?.modelRegistry?.getApiKeyAndHeaders?.(model)) ?? {}; } catch { /* unauthenticated provider: Swarm would fail the call the same way */ }
    const transport = await openAICompletionsTransport(model.baseUrl, auth.apiKey, auth.headers ?? {});
    // context_injecting_provider.go wraps the provider, so the metadata call's
    // system prompt carries the same cached/ephemeral context blocks.
    const { cached, ephemeral } = currentContextBlocks(ctx?.cwd ?? pi.getCwd?.() ?? process.cwd());
    const systemPrompt = injectContextBlocks(METADATA_SYSTEM_PROMPT, cached, ephemeral);
    // conversation_metadata.go walks the WHOLE conversation; Pi's agent_end
    // carries only this prompt's messages, so prefer the session branch.
    const branch: any[] | undefined = ctx?.sessionManager?.getBranch?.()?.filter((e: any) => e?.type === "message").map((e: any) => e.message);
    const messages = transcriptOf(branch?.length ? branch : event?.messages ?? []);
    // client.Execute → refreshConversationMetadataBestEffort, then the
    // persistence-path RefreshConversationMetadata (version-idempotent).
    for (let attempt = 0; attempt < 2; attempt++) {
      const outcome = await refreshConversationMetadata(state, messages, model.id, transport, undefined, systemPrompt);
      if (outcome.title) { try { ctx?.sessionManager?.setName?.(outcome.title); } catch { /* optional */ } }
      if (!outcome.requested || !outcome.error) break;
    }
    persist();
  });
}

export default function swarmConversationMetadataExtension(pi: Pi): void { registerSwarmConversationMetadata(pi); }
