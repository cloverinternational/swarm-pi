import {
  CapabilityManifestState,
  alignProviderPayload,
  applySwarmModelCompat,
  collapseUserText,
  swarmToolOrder,
  swarmMessageShapes,
} from "../../lib/runtime/swarm-transport-parity.ts";
import { gateActiveTools } from "../../lib/runtime/swarm-tool-gating.ts";
import { loadSwarmCanonicalTools } from "../../lib/runtime/swarm-tool-surface.ts";
import { HOOK_SLOT_TYPE, type HookSlotPart } from "../../lib/runtime/swarm-builtin-hooks-runtime.ts";
import { effectiveSelection, loadPromptContextConfig } from "../../lib/context/swarm-prompt-context-config.ts";

type Pi = any;

const registrations = new WeakSet<object>();

/**
 * Provenance Swarm threads through tool contexts (tools.OwnerConversationID /
 * UserMessageFromContext) and that apply_patch snapshots record for Undo.
 * The conversation id follows conversation/manager.go generateID:
 * "YYYYMMDD-HHMMSS-<6 lowercase alphanumerics>" (local time).
 */
export const CONVERSATION_ID = Symbol.for("pi-swarm-conversation-id");
export const LAST_USER_MESSAGE = Symbol.for("pi-swarm-last-user-message");
export function swarmConversationId(now = new Date()): string {
  const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789";
  const pad = (n: number) => String(n).padStart(2, "0");
  let suffix = "";
  for (let i = 0; i < 6; i++) suffix += alphabet[Math.floor(Math.random() * alphabet.length)];
  return `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}-${suffix}`;
}

/**
 * Make Pi's OpenAI-compatible request JSON match `swarm -p` at the transport
 * layer: bytewise tool ordering, no `strict`, `max_tokens`, no `store`,
 * `temperature: 0`, `reasoning_effort: "high"`, plain-string user content.
 * See `.pi/lib/runtime/swarm-transport-parity.ts` for the per-field rationale.
 */
export function registerSwarmTransportParity(pi: Pi): void {
  if (registrations.has(pi as object)) return;
  registrations.add(pi as object);

  // Hide Pi-only tools from the model surface and mirror Swarm's environment
  // gates (interactive-only + xAI-credentialed tools), then sort bytewise.
  let interactive = false;
  const alignTools = () => {
    const active: string[] = pi.getActiveTools?.() ?? [];
    const available: string[] = (pi.getAllTools?.() ?? []).map((tool: any) => tool.name).filter((name: any): name is string => typeof name === "string");
    const configured = loadPromptContextConfig(pi.getCwd?.() ?? process.cwd(), undefined, { persistMigration: false }).tools;
    // Default mode preserves Pi's current runtime selection. An allowlist is
    // authoritative and may re-enable a tool after a prior selection changed
    // the active set; restore-defaults explicitly restores all registrations.
    const selected = configured?.mode === "allowlist"
      ? effectiveSelection(available.length ? available : active, configured)
      : active;
    const gated = gateActiveTools(selected, { interactive }) ?? selected;
    const ordered = swarmToolOrder(gated) ?? (gated === active ? undefined : gated);
    if (ordered) pi.setActiveTools?.(ordered);
  };
  const alignModel = (ctx: any) => {
    applySwarmModelCompat(ctx?.model ?? pi.getModel?.(), { interactive });
  };
  // Swarm appends an <effective_capabilities> manifest to the user turn when
  // the (mode, tool set) signature changes (capability_manifest.go). The
  // mode is sdk.GetOperatingMode() — the SDK client's operating mode, which
  // enter_plan_mode does NOT touch (plan_broker.go flips only the App's
  // a.operatingMode), so a plan-mode turn still reads "act" and re-emits no
  // manifest. Verified against the TUI capture (tui-probe plan-enter/write).
  const manifest = new CapabilityManifestState();
  // capability_manifest.go summarises the descriptions the model is sent,
  // i.e. the canonical Swarm text — not the description a Pi extension
  // registered locally (overlaySwarmToolSchemas rewrites those on the wire).
  const activeToolSummaries = () => {
    const active = new Set<string>(pi.getActiveTools?.() ?? []);
    const canonical = loadSwarmCanonicalTools();
    return (pi.getAllTools?.() ?? []).filter((tool: any) => active.has(tool.name)).map((tool: any) => ({ name: tool.name, description: canonical.get(tool.name)?.description ?? tool.description }));
  };
  const currentMode = () => "act";

  pi.on?.("session_start", (_event: unknown, ctx: any) => { interactive = ctx?.hasUI === true; (globalThis as any)[CONVERSATION_ID] = swarmConversationId(); alignTools(); alignModel(ctx); });
  pi.on?.("model_select", (_event: unknown, ctx: any) => { alignModel(ctx); });
  // Tool sets and model can change mid-session (extensions, /model, /tools).
  // Re-check right before every request; both operations are idempotent.
  pi.on?.("before_agent_start", (_event: unknown, ctx: any) => { if (typeof ctx?.hasUI === "boolean") interactive = ctx.hasUI; alignTools(); alignModel(ctx); });
  pi.on?.("context", (event: { messages: any[] }) => {
    let messages: any[] = event.messages ?? [];
    let changed = false;
    const slots = expandHookSlots(messages);
    if (slots) { messages = slots; changed = true; }
    const replayed = replayHookOrder(messages);
    if (replayed) { messages = replayed; changed = true; }
    // Swarm's registry only holds the tools it exposes; a Pi tool that is
    // registered but gated off the surface (e.g. `bash` in the interactive
    // TUI) is unknown to the model and must error like one.
    const shaped = swarmMessageShapes(messages, new Set<string>(pi.getActiveTools?.() ?? (pi.getAllTools?.() ?? []).map((tool: any) => tool.name)));
    if (shaped) { messages = shaped; changed = true; }
    const withManifest = manifest.apply(messages, currentMode(), activeToolSummaries());
    if (withManifest) { messages = withManifest; changed = true; }
    // The user turn as the model sees it (manifest included) is what Swarm
    // records as the snapshot "because:" provenance; hook reminders are not
    // user turns.
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i];
      if (m?.role !== "user") continue;
      const text = typeof m.content === "string" ? m.content : Array.isArray(m.content) ? m.content.filter((b: any) => b?.type === "text").map((b: any) => b.text).join("") : "";
      if (text.startsWith("<system-reminder")) continue;
      (globalThis as any)[LAST_USER_MESSAGE] = text;
      break;
    }
    const collapsed = collapseUserText(messages);
    if (collapsed) { messages = collapsed; changed = true; }
    return changed ? { messages } : undefined;
  });
  // Last hop before serialisation: top-level keys in Go struct order and tool
  // schemas with bytewise-sorted keys (Go map encoding), so the bytes match,
  // not just the parsed value (docs/extensions.md before_provider_request —
  // returning a value replaces the payload).
  pi.on?.("before_provider_request", (event: { payload: Record<string, unknown> }) => {
    // Background-process notifications injected mid-run are RoleSystem in
    // Swarm (app_messaging.go RichMessageInjector); Pi custom messages reach
    // the wire as "user". Promote the exact texts the bgprocess extension
    // registered (idle-time wakes stay "user", as in startBgProcessWakeCmd).
    const systemTexts: Set<string> | undefined = (globalThis as any)[Symbol.for("pi-swarm-background-system-texts")];
    let payload = event.payload;
    if (systemTexts?.size && Array.isArray(payload?.messages)) {
      let changed = false;
      const messages = (payload.messages as any[]).map((m) => {
        const content = typeof m?.content === "string" ? m.content : Array.isArray(m?.content) ? m.content.filter((b: any) => b?.type === "text").map((b: any) => b.text).join("") : "";
        if (m?.role === "user" && systemTexts.has(content)) { changed = true; return { ...m, role: "system", content }; }
        return m;
      });
      if (changed) payload = { ...payload, messages };
    }
    return alignProviderPayload(payload, { interactive }) ?? (payload === event.payload ? undefined : payload);
  });
}

export default function swarmTransportParityExtension(pi: Pi): void { registerSwarmTransportParity(pi); }

const systemTextSet = (): Set<string> => ((globalThis as any)[Symbol.for("pi-swarm-background-system-texts")] ??= new Set<string>());

/**
 * One steered hook-slot message carries every part Swarm emits in the slot
 * after a turn's tool results (agent_tools.go hook message, then the TUI's
 * RichMessageInjector RoleSystem notifications). Expand it back into
 * separate messages; system parts are promoted by before_provider_request.
 *
 * Persistence: agent_tools.go documents the post-tool hook message as a
 * durable history entry, and that is what Pi does. The interactive TUI
 * intermittently loses it from ~/.swarm/conversations/<id>.json (2 of 3
 * identical runs; Swarm-Code/mono#481, a lost-update race) — a defect, not
 * a contract, so `ephemeralPostToolContext` stays off by default.
 */
export function expandHookSlots(messages: readonly any[], options: { ephemeralPostToolContext?: boolean } = {}): any[] | undefined {
  let changed = false;
  const out: any[] = [];
  let lastPrompt = -1;
  messages.forEach((m, i) => { if (m?.role === "user") lastPrompt = i; });
  messages.forEach((m, i) => {
    const parts: HookSlotPart[] | undefined = m?.role === "custom" && m?.customType === HOOK_SLOT_TYPE ? m?.details?.parts : undefined;
    if (!Array.isArray(parts)) { out.push(m); return; }
    const completedTurn = options.ephemeralPostToolContext === true && i < lastPrompt;
    const kept = completedTurn ? parts.filter(part => part.role !== "user") : parts;
    if (kept.length === parts.length && parts.length <= 1) { out.push(m); return; }
    changed = true;
    for (const part of kept) {
      if (part.role === "system") systemTextSet().add(part.text);
      out.push({ ...m, content: part.text, customType: part.role === "system" ? "swarm-background-notification" : m.customType, details: undefined });
    }
  });
  return changed ? out : undefined;
}

/**
 * Swarm persists a user_prompt_submit hook message before the prompt it was
 * emitted for, so every run after the live one replays [hook, prompt] where
 * the live run sent [prompt, hook]. Mirror that flip for completed turns.
 */
export function replayHookOrder(messages: readonly any[]): any[] | undefined {
  const out = [...messages];
  let changed = false;
  // The live turn spans every provider call until the NEXT user prompt, so
  // only pairs that precede the latest prompt are replayed from persistence.
  let lastPrompt = -1;
  out.forEach((m, i) => { if (m?.role === "user") lastPrompt = i; });
  for (let i = 0; i + 1 < out.length; i++) {
    const prompt = out[i], hook = out[i + 1];
    if (prompt?.role !== "user" || hook?.role !== "custom" || hook?.customType !== HOOK_SLOT_TYPE) continue;
    if (i >= lastPrompt) continue; // live turn: keep prompt → hook
    out[i] = hook; out[i + 1] = prompt; changed = true; i++;
  }
  return changed ? out : undefined;
}
