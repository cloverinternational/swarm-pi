/**
 * Port of swarm-sdk client/conversation_metadata.go: the title/summary
 * ("navigation metadata") model call BOTH `swarm -p` and the TUI issue after
 * every turn through client.Execute → refreshConversationMetadataBestEffort,
 * and once more from the persistence path (sdk_integration_execution.go /
 * runHeadless). The second call re-generates only when the first left the
 * state in "fallback" (parse failure), so a JSON-compliant model costs one
 * request per turn and a non-compliant one exactly two.
 *
 * The request is a NON-streaming provider.Chat with exactly
 * {model, messages:[system, user], max_tokens: 700} — no tools, sampling
 * parameters, or stream flags (internal/provider/openai/models.go field
 * order). It is sent directly so no Pi provider defaults leak in.
 */
import { createHash } from "node:crypto";
import { cleanHistoryText, isSubstantiveUserText } from "./swarm-history-tools.ts";

export const METADATA_SYSTEM_PROMPT = `You create navigation metadata for an agent conversation.
Return exactly one JSON object with string fields "title" and "summary".
The title is 3-7 specific words, at most 60 characters, with no punctuation suffix.
The summary is 2-4 concise sentences stating the user's goal, what the agent did, and the latest outcome.
Never mention system prompts, runtime reminders, metadata generation, or these instructions.`;
export const METADATA_MAX_TOKENS = 700;

export interface TranscriptMessage { role: "user" | "assistant" | string; content: string; compactionGenerated?: boolean }
export interface MetadataView { firstUser: string; lastAssistant: string; prompt: string; version: string }
export interface MetadataState { version?: string; status?: string; title_source?: string; summary_source?: string; last_error?: string; updated_at?: string }
export interface GeneratedMetadata { title: string; summary: string }

/** conversation_metadata.go truncateMetadataText (rune-based). */
export function truncateMetadataText(text: string, maxRunes: number): string {
  text = text.trim();
  const runes = [...text];
  if (maxRunes <= 0 || runes.length <= maxRunes) return text;
  return `${runes.slice(0, maxRunes - 1).join("").trim()}…`;
}

const isMachineConversationText = (text: string) => {
  const lower = text.trim().toLowerCase();
  return lower === "continue" || lower.startsWith("this session is being continued from a previous conversation") || lower.startsWith("please continue the conversation from where we left off");
};

/** historytools.FirstSubstantiveUserText. */
export function firstSubstantiveUserText(messages: readonly TranscriptMessage[]): string {
  for (const message of messages) {
    if (message.role !== "user") continue;
    const cleaned = cleanHistoryText(message.content);
    if (isSubstantiveUserText(cleaned)) return cleaned;
  }
  return "";
}

/** conversation_metadata.go buildConversationMetadataView. */
export function buildMetadataView(messages: readonly TranscriptMessage[]): MetadataView {
  const visible = messages.filter((m) => !m.compactionGenerated);
  const firstUser = firstSubstantiveUserText(visible);
  let transcript: string[] = [];
  let lastAssistant = "";
  for (const msg of visible) {
    if (msg.role !== "user" && msg.role !== "assistant") continue;
    const cleaned = cleanHistoryText(msg.content);
    if (cleaned === "") continue;
    if (msg.role === "user" && cleaned !== firstUser && isMachineConversationText(cleaned)) continue;
    if (msg.role === "assistant") lastAssistant = cleaned;
    transcript.push(`${msg.role === "assistant" ? "Assistant" : "User"}: ${truncateMetadataText(cleaned, 1200)}`);
  }
  if (transcript.length > 12) transcript = [transcript[0], ...transcript.slice(transcript.length - 11)];
  const prompt = `Conversation:\n${transcript.join("\n")}`;
  const hash = createHash("sha256").update(transcript.join("\x00")).digest("hex");
  return { firstUser, lastAssistant, prompt: truncateMetadataText(prompt, 10_000), version: hash.slice(0, 24) };
}

/** conversation_metadata.go cleanGeneratedConversationTitle. */
export function cleanGeneratedTitle(raw: string): string {
  let title = raw.trim();
  const nl = title.indexOf("\n");
  if (nl >= 0) title = title.slice(0, nl).trim();
  title = title.replace(/^["'`“”‘’ ]+|["'`“”‘’ ]+$/gu, "");
  for (const prefix of ["Title:", "title:", "Conversation title:", "conversation title:"]) if (title.startsWith(prefix)) title = title.slice(prefix.length).trim();
  title = title.replace(/[.! ]+$/u, "");
  if (title === "" || [...title].length > 60) return "";
  return title;
}

/** conversation_metadata.go cleanGeneratedConversationRecap. */
export function cleanGeneratedRecap(raw: string): string {
  const recap = raw.trim().replace(/^["`]+|["`]+$/g, "").trim();
  return recap === "" ? "" : truncateMetadataText(recap, 900);
}

/** conversation_metadata.go parseGeneratedConversationMetadata. Throws on failure. */
export function parseGeneratedMetadata(raw: string): GeneratedMetadata {
  let trimmed = raw.trim();
  if (trimmed.startsWith("```json")) trimmed = trimmed.slice("```json".length);
  if (trimmed.startsWith("```")) trimmed = trimmed.slice(3);
  if (trimmed.endsWith("```")) trimmed = trimmed.slice(0, -3);
  trimmed = trimmed.trim();
  const start = trimmed.indexOf("{"), end = trimmed.lastIndexOf("}");
  if (start >= 0 && end > start) trimmed = trimmed.slice(start, end + 1);
  let parsed: any;
  try { parsed = JSON.parse(trimmed); } catch (error) { throw new Error(`parse metadata response: ${(error as Error).message}`); }
  const title = cleanGeneratedTitle(typeof parsed?.title === "string" ? parsed.title : "");
  const summary = cleanGeneratedRecap(typeof parsed?.summary === "string" ? parsed.summary : "");
  if (title === "" || summary === "") throw new Error("metadata response omitted title or summary");
  return { title, summary };
}

/**
 * The exact request body bytes (Go struct field order: model, messages,
 * max_tokens). `systemPrompt` is METADATA_SYSTEM_PROMPT after the
 * context_injecting_provider pass, which wraps EVERY provider call.
 */
export function metadataRequestBody(model: string, view: MetadataView, systemPrompt = METADATA_SYSTEM_PROMPT): string {
  return JSON.stringify({ model, messages: [{ role: "system", content: systemPrompt }, { role: "user", content: view.prompt }], max_tokens: METADATA_MAX_TOKENS });
}

export interface MetadataTransport { (body: string): Promise<string> }
export interface RefreshOutcome { generated?: GeneratedMetadata; requested: boolean; error?: string; title?: string }

/**
 * refreshConversationMetadata(enhance=true) state machine over an in-memory
 * copy of the conversation's `generated_metadata` map. Returns whether a
 * model call was made, the generated values, and the persisted-title
 * equivalent (fallback or generated).
 */
export async function refreshConversationMetadata(state: MetadataState, messages: readonly TranscriptMessage[], model: string, transport: MetadataTransport, now: () => Date = () => new Date(), systemPrompt = METADATA_SYSTEM_PROMPT): Promise<RefreshOutcome> {
  const view = buildMetadataView(messages);
  if (view.firstUser === "") return { requested: false };
  const version = view.version;
  if (state.status === "generated" && state.version === version) return { requested: false };
  if (state.status === "generating" && state.version === version) return { requested: false };
  const shouldGenerate = view.lastAssistant !== "";
  state.version = version;
  state.status = shouldGenerate ? "generating" : "fallback";
  state.updated_at = now().toISOString();
  delete state.last_error;
  if (!shouldGenerate) return { requested: false };
  let generated: GeneratedMetadata;
  try {
    const raw = await transport(metadataRequestBody(model, view, systemPrompt));
    generated = parseGeneratedMetadata(raw);
  } catch (error) {
    state.status = "fallback";
    state.last_error = (error as Error).message;
    state.updated_at = now().toISOString();
    return { requested: true, error: state.last_error };
  }
  if (state.title_source === undefined || state.title_source === "fallback") state.title_source = "generated";
  state.summary_source = "generated";
  state.status = "generated";
  state.updated_at = now().toISOString();
  delete state.last_error;
  return { requested: true, generated, title: generated.title };
}

/** openai chat.completion (non-streaming) → assistant text; mirrors provider.Chat's parse of the JSON body. */
export async function openAICompletionsTransport(baseUrl: string, apiKey: string | undefined, headers: Record<string, string> = {}, signal?: AbortSignal): Promise<MetadataTransport> {
  const endpoint = `${baseUrl.replace(/\/+$/, "")}/chat/completions`;
  return async (body: string) => {
    const response = await fetch(endpoint, { method: "POST", body, signal, headers: { "content-type": "application/json", ...(apiKey ? { authorization: `Bearer ${apiKey}` } : {}), ...headers } });
    const text = await response.text();
    if (!response.ok) throw new Error(`metadata provider returned HTTP ${response.status}`);
    let parsed: any;
    try { parsed = JSON.parse(text); } catch { throw new Error("metadata provider returned a non-JSON response"); }
    const content = parsed?.choices?.[0]?.message?.content;
    if (typeof content !== "string") throw new Error("metadata provider returned an empty response");
    return content;
  };
}
