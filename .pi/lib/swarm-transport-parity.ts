/**
 * Transport-level parity between `pi -p` and `swarm -p`.
 *
 * Swarm's OpenAI-compatible request shape differs from Pi's provider defaults in
 * ways that carry no semantic difference for the model but break byte-level
 * parity of the JSON crossing the model boundary. Every knob here maps onto a
 * documented Pi model/compat field; nothing patches Pi internals.
 *
 *  - tools[]        Swarm sorts bytewise (Go `sort.Strings`). Pi emits
 *                   registration order. `setActiveTools` preserves the order it
 *                   is given, so the active list is re-issued sorted.
 *  - function.strict Pi's openai-completions provider emits `strict: false`
 *                   unless `compat.supportsStrictMode === false`. No Pi-Swarm
 *                   tool opts into constrained sampling, so `false` is the only
 *                   value ever produced and dropping the key is lossless.
 *  - max_tokens     Swarm sends `max_tokens`; Pi defaults to
 *                   `max_completion_tokens` unless `compat.maxTokensField`.
 *  - store          Swarm never sends `store`; Pi sends `store: false` when
 *                   `compat.supportsStore` is truthy.
 *  - temperature    Swarm always sends `temperature: 0`.
 *  - reasoning_effort Swarm always sends `reasoning_effort: "high"`.
 *                   Both ride on `model.samplingParams`, which the provider
 *                   `Object.assign`s onto the request.
 *  - user content   Pi wraps every user turn as `[{type:"text"}]`; Swarm sends
 *                   a plain string when there are no attachments.
 *  - key order      Go's encoding/json emits struct fields in declaration
 *                   order (internal/provider/openai/models.go
 *                   ChatCompletionRequest); Pi's provider builds its params
 *                   literal in a different order. Same JSON value, different
 *                   bytes. `before_provider_request` lets us re-key the
 *                   payload right before it is serialised.
 */

export const SWARM_TEMPERATURE = 0;
export const SWARM_REASONING_EFFORT = "high";

/** internal/provider/openai/models.go ChatCompletionRequest field order. */
export const SWARM_CHAT_REQUEST_KEY_ORDER = [
  "model", "messages", "max_tokens", "max_completion_tokens", "temperature", "top_p", "n",
  "stream", "stream_options", "stop", "presence_penalty", "frequency_penalty", "user",
  "tools", "tool_choice", "response_format", "parallel_tool_calls", "text", "reasoning",
  "reasoning_effort",
] as const;

/**
 * Rebuild a payload with keys in Swarm's wire order. Keys Swarm's struct does
 * not know keep their relative order after the known ones. Returns undefined
 * when the order is already correct so callers can leave the payload alone.
 */
export function swarmPayloadKeyOrder<T extends Record<string, unknown>>(payload: T): T | undefined {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return undefined;
  const present = Object.keys(payload);
  const rank = new Map<string, number>(SWARM_CHAT_REQUEST_KEY_ORDER.map((key, index) => [key, index]));
  const ordered = [...present].sort((a, b) => {
    const ra = rank.get(a), rb = rank.get(b);
    if (ra !== undefined && rb !== undefined) return ra - rb;
    if (ra !== undefined) return -1;
    if (rb !== undefined) return 1;
    return present.indexOf(a) - present.indexOf(b);
  });
  if (ordered.every((key, index) => key === present[index])) return undefined;
  const next: Record<string, unknown> = {};
  for (const key of ordered) next[key] = payload[key];
  return next as T;
}

/** Go encoding/json emits map[string]any keys sorted at every level; arrays keep order. */
export function sortKeysDeep<T>(value: T): T {
  if (Array.isArray(value)) return value.map(sortKeysDeep) as T;
  if (!value || typeof value !== "object") return value;
  const out: Record<string, unknown> = {};
  for (const key of Object.keys(value as Record<string, unknown>).sort(bytewiseCompare)) out[key] = sortKeysDeep((value as Record<string, unknown>)[key]);
  return out as T;
}

/**
 * Swarm's tool JSON Schemas are `map[string]any` on the wire, so their keys
 * are bytewise-sorted recursively. Pi forwards schemas as authored. Returns
 * a payload with every `tools[].function.parameters` re-keyed, or undefined.
 */
export function swarmToolSchemaKeyOrder<T extends { tools?: unknown }>(payload: T): T | undefined {
  const tools = payload?.tools;
  if (!Array.isArray(tools) || tools.length === 0) return undefined;
  let changed = false;
  const next = tools.map((tool: any) => {
    const parameters = tool?.function?.parameters;
    if (!parameters || typeof parameters !== "object") return tool;
    const sorted = sortKeysDeep(parameters);
    if (JSON.stringify(sorted) === JSON.stringify(parameters)) return tool;
    changed = true;
    return { ...tool, function: { ...tool.function, parameters: sorted } };
  });
  return changed ? { ...payload, tools: next } : undefined;
}

/** Everything `before_provider_request` needs: key order at the top and inside tool schemas. */
export function alignProviderPayload<T extends Record<string, unknown>>(payload: T): T | undefined {
  // Custom (extension) messages only become role:"user" in convertToLlm,
  // which runs after the `context` event, so collapse again here where every
  // message already carries its provider role.
  const collapsed = Array.isArray(payload.messages) ? collapseUserText(payload.messages as MessageLike[]) : undefined;
  const withMessages = collapsed ? { ...payload, messages: collapsed } : payload;
  const withSchemas = swarmToolSchemaKeyOrder(withMessages) ?? withMessages;
  const ordered = swarmPayloadKeyOrder(withSchemas);
  if (ordered) return ordered;
  return withSchemas === payload ? undefined : withSchemas;
}

export function bytewiseCompare(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

/** Return the Swarm-ordered tool list; `undefined` when already ordered. */
export function swarmToolOrder(active: readonly string[]): string[] | undefined {
  const sorted = [...active].sort(bytewiseCompare);
  return sorted.every((name, index) => name === active[index]) ? undefined : sorted;
}

export interface ModelLike {
  compat?: Record<string, unknown>;
  samplingParams?: Record<string, unknown>;
  [key: string]: unknown;
}

/** Apply Swarm's request-option defaults onto a Pi model object in place. */
export function applySwarmModelCompat(model: ModelLike | undefined): boolean {
  if (!model || typeof model !== "object") return false;
  let changed = false;
  const compat = (model.compat ??= {});
  const compatDefaults: Record<string, unknown> = {
    supportsStrictMode: false,
    supportsStore: false,
    maxTokensField: "max_tokens",
  };
  for (const [key, value] of Object.entries(compatDefaults)) {
    if (compat[key] !== value) { compat[key] = value; changed = true; }
  }
  const sampling = (model.samplingParams ??= {});
  const samplingDefaults: Record<string, unknown> = {
    temperature: SWARM_TEMPERATURE,
    reasoning_effort: SWARM_REASONING_EFFORT,
  };
  for (const [key, value] of Object.entries(samplingDefaults)) {
    if (!(key in sampling)) { sampling[key] = value; changed = true; }
  }
  return changed;
}

type TextBlock = { type: "text"; text: string };
export interface MessageLike { role?: string; content?: unknown; [key: string]: unknown }

/** Collapse text-only user content arrays into Swarm's plain-string form. */
export function collapseUserText(messages: readonly MessageLike[]): MessageLike[] | undefined {
  let changed = false;
  const next = messages.map(message => {
    if (message.role !== "user" || !Array.isArray(message.content)) return message;
    const blocks = message.content as unknown[];
    if (blocks.length === 0 || !blocks.every(block => isTextBlock(block))) return message;
    changed = true;
    return { ...message, content: (blocks as TextBlock[]).map(block => block.text).join("") };
  });
  return changed ? next : undefined;
}

function isTextBlock(block: unknown): block is TextBlock {
  return Boolean(block) && typeof block === "object" && (block as TextBlock).type === "text" && typeof (block as TextBlock).text === "string";
}

// ---------------------------------------------------------------------------
// Capability manifest — port of swarm-tui/internal/chat/capability_manifest.go
// ---------------------------------------------------------------------------

export const DEFAULT_CAPABILITY_MANIFEST_TOOL_LIMIT = 40;
export interface ToolSummary { name: string; description?: string }

/** capability_manifest.go firstSentence — byte-index semantics mirrored. */
export function firstSentence(description: string | undefined): string {
  const text = (description ?? "").trim();
  if (text === "") return "Available for this turn.";
  const bytes = Buffer.from(text, "utf8");
  for (let i = 0; i < bytes.length; i++) {
    const c = bytes[i];
    if (c !== 0x2e && c !== 0x21 && c !== 0x3f) continue; // . ! ?
    const next = i + 1;
    if (next >= bytes.length || /\s/.test(String.fromCharCode(bytes[next]))) return bytes.subarray(0, next).toString("utf8").trim();
  }
  const runes = [...text];
  if (runes.length > 160) return runes.slice(0, 160).join("").trim() + "…";
  return text;
}

/** capability_manifest.go buildCapabilityManifest. */
export function buildCapabilityManifest(modeID: string, tools: readonly ToolSummary[], maxTools = DEFAULT_CAPABILITY_MANIFEST_TOOL_LIMIT): string {
  const sorted = [...tools].sort((a, b) => { const x = a.name.toLowerCase(), y = b.name.toLowerCase(); return x < y ? -1 : x > y ? 1 : 0; });
  if (maxTools <= 0) maxTools = DEFAULT_CAPABILITY_MANIFEST_TOOL_LIMIT;
  let out = "<effective_capabilities>\n";
  out += "This is a request-scoped summary of tools actually exposed to the model";
  const mode = modeID.trim();
  if (mode !== "") out += ` (mode: ${mode})`;
  out += ". Tool schemas remain authoritative; this summary grants no permissions.\n";
  if (sorted.length === 0) return out + "No tools are available for this turn.\n</effective_capabilities>";
  const visible = sorted.length > maxTools ? sorted.slice(0, maxTools) : sorted;
  for (const tool of visible) out += `- \`${tool.name}\`: ${firstSentence(tool.description)}\n`;
  const omitted = sorted.length - visible.length;
  if (omitted > 0) out += `- ${omitted} additional tool omitted; use the tool search/discovery capability when available.\n`;
  return out + "</effective_capabilities>";
}

/** capability_manifest.go capabilityManifestSignature: mode + sorted tool names. */
export function capabilityManifestSignature(modeID: string, tools: readonly ToolSummary[]): string {
  return `${modeID.trim()}|${[...tools.map(t => t.name)].sort(bytewiseCompare).join(",")}`;
}

/**
 * Swarm appends the manifest to the CURRENT user turn only when the signature
 * changed since the last emission for this conversation, and keeps it on that
 * turn's request-history entry for every provider call within the turn. The
 * durable transcript stays clean, so earlier turns never carry it.
 */
export class CapabilityManifestState {
  private lastSignature?: string;
  private turnKey?: string;
  apply(messages: readonly MessageLike[], modeID: string, tools: readonly ToolSummary[]): MessageLike[] | undefined {
    let index = -1;
    for (let i = messages.length - 1; i >= 0; i--) if (messages[i].role === "user") { index = i; break; }
    if (index < 0) return undefined;
    const current = messages[index];
    const key = turnKeyOf(current, index);
    const signature = capabilityManifestSignature(modeID, tools);
    if (this.turnKey !== key) {
      if (this.lastSignature === signature) return undefined;
      this.lastSignature = signature;
      this.turnKey = key;
    }
    const manifest = buildCapabilityManifest(modeID, tools);
    const content = current.content;
    let next: unknown;
    if (typeof content === "string") next = content + "\n\n" + manifest;
    else if (Array.isArray(content) && content.length > 0 && content.every(isTextBlock)) next = (content as TextBlock[]).map(b => b.text).join("") + "\n\n" + manifest;
    else return undefined;
    const out = [...messages];
    out[index] = { ...current, content: next };
    return out;
  }
}

function turnKeyOf(message: MessageLike, index: number): string {
  const ts = (message as { timestamp?: unknown }).timestamp;
  return `${index}:${typeof ts === "number" || typeof ts === "string" ? ts : ""}`;
}
