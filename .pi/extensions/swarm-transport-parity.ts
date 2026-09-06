import {
  CapabilityManifestState,
  alignProviderPayload,
  applySwarmModelCompat,
  collapseUserText,
  swarmToolOrder,
  swarmMessageShapes,
} from "../lib/swarm-transport-parity.ts";
import { gateActiveTools } from "../lib/swarm-tool-gating.ts";
import { currentPlanModeController } from "./swarm-plan-mode.ts";

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
 * See `.pi/lib/swarm-transport-parity.ts` for the per-field rationale.
 */
export function registerSwarmTransportParity(pi: Pi): void {
  if (registrations.has(pi as object)) return;
  registrations.add(pi as object);

  // Hide Pi-only tools from the model surface and mirror Swarm's environment
  // gates (interactive-only + xAI-credentialed tools), then sort bytewise.
  let interactive = false;
  const alignTools = () => {
    const active: string[] = pi.getActiveTools?.() ?? [];
    const gated = gateActiveTools(active, { interactive }) ?? active;
    const ordered = swarmToolOrder(gated) ?? (gated === active ? undefined : gated);
    if (ordered) pi.setActiveTools?.(ordered);
  };
  const alignModel = (ctx: any) => {
    applySwarmModelCompat(ctx?.model ?? pi.getModel?.());
  };
  // Swarm appends an <effective_capabilities> manifest to the user turn when
  // the (mode, tool set) signature changes (capability_manifest.go). Mode is
  // "act" unless plan mode is active — mirrors the TUI operating mode ids.
  const manifest = new CapabilityManifestState();
  const activeToolSummaries = () => {
    const active = new Set<string>(pi.getActiveTools?.() ?? []);
    return (pi.getAllTools?.() ?? []).filter((tool: any) => active.has(tool.name)).map((tool: any) => ({ name: tool.name, description: tool.description }));
  };
  const currentMode = () => (currentPlanModeController()?.isActive() ? "plan" : "act");

  pi.on?.("session_start", (_event: unknown, ctx: any) => { interactive = ctx?.hasUI === true; (globalThis as any)[CONVERSATION_ID] = swarmConversationId(); alignTools(); alignModel(ctx); });
  pi.on?.("model_select", (_event: unknown, ctx: any) => { alignModel(ctx); });
  // Tool sets and model can change mid-session (extensions, /model, /tools).
  // Re-check right before every request; both operations are idempotent.
  pi.on?.("before_agent_start", (_event: unknown, ctx: any) => { if (typeof ctx?.hasUI === "boolean") interactive = ctx.hasUI; alignTools(); alignModel(ctx); });
  pi.on?.("context", (event: { messages: any[] }) => {
    let messages: any[] = event.messages ?? [];
    let changed = false;
    const shaped = swarmMessageShapes(messages, new Set((pi.getAllTools?.() ?? []).map((tool: any) => tool.name)));
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
  pi.on?.("before_provider_request", (event: { payload: Record<string, unknown> }) => alignProviderPayload(event.payload));
}

export default function swarmTransportParityExtension(pi: Pi): void { registerSwarmTransportParity(pi); }
