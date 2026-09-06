import {
  CapabilityManifestState,
  alignProviderPayload,
  applySwarmModelCompat,
  collapseUserText,
  swarmToolOrder,
} from "../lib/swarm-transport-parity.ts";
import { gateActiveTools } from "../lib/swarm-tool-gating.ts";
import { currentPlanModeController } from "./swarm-plan-mode.ts";

type Pi = any;

const registrations = new WeakSet<object>();

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

  pi.on?.("session_start", (_event: unknown, ctx: any) => { interactive = ctx?.hasUI === true; alignTools(); alignModel(ctx); });
  pi.on?.("model_select", (_event: unknown, ctx: any) => { alignModel(ctx); });
  // Tool sets and model can change mid-session (extensions, /model, /tools).
  // Re-check right before every request; both operations are idempotent.
  pi.on?.("before_agent_start", (_event: unknown, ctx: any) => { if (typeof ctx?.hasUI === "boolean") interactive = ctx.hasUI; alignTools(); alignModel(ctx); });
  pi.on?.("context", (event: { messages: any[] }) => {
    let messages: any[] = event.messages ?? [];
    let changed = false;
    const withManifest = manifest.apply(messages, currentMode(), activeToolSummaries());
    if (withManifest) { messages = withManifest; changed = true; }
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
