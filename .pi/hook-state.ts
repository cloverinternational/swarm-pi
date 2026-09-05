import { addHookObservation } from "./hook-observations.ts";

export type HookGroup = "taskmanage" | "autogenskills" | "swarm-prompt" | "disk-hooks" | "annoyance";
export type HookOutcome = "executed" | "blocked" | "failed" | "skipped";
export interface HookRecord { id: string; group: HookGroup; event: string; at: string; enabled: boolean; outcome: HookOutcome; tool?: string; toolCallId?: string; reason?: string; output?: string; }
interface State { enabled: Record<string, boolean>; visible: boolean; recent: HookRecord[]; counts: { registered: number; executed: number; blocked: number; failed: number; skipped: number }; }

const KEY = Symbol.for("pi-swarm-hook-state");
type Shared = { state: State; pi?: any; present?: (data: any) => void };
const root = globalThis as typeof globalThis & { [KEY]?: Shared };
const shared: Shared = root[KEY] ?? (root[KEY] = { state: { enabled: {}, visible: true, recent: [], counts: { registered: 0, executed: 0, blocked: 0, failed: 0, skipped: 0 } } });
// Pi /reload can retain Symbol.for state from an older extension module. Normalize
// it before any handler registration so upgrades never fail on missing fields.
function normalizeState() {
  if (!shared.state || typeof shared.state !== "object") shared.state = { enabled: {}, visible: true, recent: [], counts: { registered: 0, executed: 0, blocked: 0, failed: 0, skipped: 0 } };
  const state = shared.state as Partial<State>;
  state.enabled = state.enabled && typeof state.enabled === "object" ? state.enabled : {};
  state.visible = typeof state.visible === "boolean" ? state.visible : true;
  state.recent = Array.isArray(state.recent) ? state.recent.slice(-200) : [];
  state.counts = { registered: 0, executed: 0, blocked: 0, failed: 0, skipped: 0, ...(state.counts ?? {}) };
  shared.state = state as State;
}
normalizeState();

export function hookState() { normalizeState(); return shared.state; }
export function setHookPi(pi: any) { shared.pi = pi; }
export function setHookPresenter(present?: (data: any) => void) { shared.present = present; }
export function isHookEnabled(group: string) { normalizeState(); return shared.state.enabled[group] !== false; }
export function toggleHook(group: string, enabled?: boolean) { shared.state.enabled[group] = enabled ?? !isHookEnabled(group); return isHookEnabled(group); }
export function setHookVisibility(visible?: boolean) { shared.state.visible = visible ?? !shared.state.visible; return shared.state.visible; }
export function hookRowsVisible() { return shared.state.visible; }
// Terminal events are delivered once per hook group. Deduplicate the two Pi
// terminal notifications without suppressing the post row for later groups.
function eventInfo(payload: any) { return { tool: payload?.toolName ?? payload?.tool_name, toolCallId: payload?.toolCallId ?? payload?.tool_call_id }; }
// Pi's hook rows name the hook, not the host adapter or tool. These are the
// stable names used by the Swarm TUI; adapters may provide hookName explicitly.
function displayHookName(group: HookGroup, event: string, payload: any): string {
  if (typeof payload?.hookName === "string" && payload.hookName.trim()) return payload.hookName.trim();
  if (group === "autogenskills") return "autogenskills-budget-enforcement";
  if (group === "taskmanage") return payload?.hookName ?? (event === "tool_call" ? "task-enforcement-hook" : "task-maintenance-reminder-hook");
  if (group === "swarm-prompt") return "swarm-prompt";
  if (group === "annoyance") return "annoyance-nudge";
  return group;
}
export function recordHook(group: HookGroup, event: string, payload?: any, outcome: HookOutcome = "executed", reason?: string, output?: string) {
  const info = eventInfo(payload), record: HookRecord = { id: `${Date.now()}-${Math.random().toString(36).slice(2)}`, group, event, at: new Date().toISOString(), enabled: isHookEnabled(group), outcome, ...info, ...(reason ? { reason } : {}), ...(output ? { output } : {}) };
  shared.state.recent.push(record); shared.state.recent = shared.state.recent.slice(-200);
  shared.state.counts[outcome]++;
  // Successful hook observations are internal telemetry in Swarm. Only show
  // actionable outcomes in the normal Pi transcript; retain every record in
  // durable state for /hooks and diagnostics. This prevents routine lines such
  // as "read · allowed" and "bash · completed" from becoming chat noise.
  // Keep tool pre/post observations visible when requested; lifecycle and
  // prompt hooks remain telemetry-only unless they block or fail.
  // Publish tool-bound observations to the renderer data plane. This is
  // independent of visibility because the native tool renderer owns display.
  const callId = info.toolCallId;
  const toolName = info.tool;
  const isPost = event === "tool_result";
  if (callId && toolName && (event === "tool_call" || isPost)) {
    addHookObservation({ hookName: displayHookName(group, event, payload), phase: isPost ? "after" : "before", outcome, toolCallId: String(callId), toolName: String(toolName), output, reason, at: record.at });
  }
  // Rendering is owned by the per-tool renderer bridge. Never emit a queued
  // message here: queued messages cannot occupy the tool's visual slot.
  return record;
}

(globalThis as any).__piSwarmRegisterHook = registerHook;

export function registerHook(pi: any, group: HookGroup, event: string, handler: any) {
  normalizeState();
  shared.state.counts.registered++;
  pi.on(event, async (payload: any, ctx: any) => {
    if (!isHookEnabled(group)) { recordHook(group, event, payload, "skipped", "hook group disabled"); persistHookState(pi); return; }
    try {
      const result = await handler(payload, ctx);
      const hookPayload = result?.hookName ? { ...payload, hookName: result.hookName } : payload;
      recordHook(
        group,
        event,
        hookPayload,
        result?.block === true ? "blocked" : "executed",
        result?.block === true ? result.reason ?? "blocked by hook" : undefined,
        result?.hookOutput,
      );
      persistHookState(pi);
      // Hook messages are not transcript messages. Pi treats a returned
      // `message` as model-visible context, which produced prose such as
      // "hook completed successfully" after every successful hook. Only
      // return actual middleware data (prompt changes) or a hard block.
      if (result?.block === true) return { block: true, reason: result.reason };
      // Pi's native event contracts are the boundary: only return a valid
      // middleware patch for the event that requested it. In particular, a
      // hook's diagnostic `message` is never returned from tool_call, where it
      // would become a synthetic model message.
      if (event === "before_agent_start") {
        if (result?.systemPrompt === undefined && result?.message === undefined) return undefined;
        // Pi requires before_agent_start.message to be a CustomMessage, not a
        // plain string. Convert coordinator guidance at this adapter boundary;
        // keeping the coordinator host-agnostic preserves its unit-test API.
        const message = typeof result?.message === "string"
          ? { customType: "swarm-task-hook", content: result.message, display: false }
          : result?.message;
        return { ...(result?.systemPrompt !== undefined ? { systemPrompt: result.systemPrompt } : {}), ...(message !== undefined ? { message } : {}) };
      }
      if (event === "tool_result" && result && (result.content !== undefined || result.details !== undefined || result.isError !== undefined)) {
        return { ...(result.content !== undefined ? { content: result.content } : {}), ...(result.details !== undefined ? { details: result.details } : {}), ...(result.isError !== undefined ? { isError: result.isError } : {}) };
      }
      // Pi discards return values from ordinary lifecycle events such as
      // turn_end. ContinueWithMessage-style nudges are intentional model
      // context, so queue them explicitly as hidden custom messages.
      if (event === "turn_end" && typeof result?.message === "string" && result.message.trim()) {
        pi.sendMessage?.({ customType: "swarm-hook-nudge", content: result.message, display: false }, { triggerTurn: false });
      }
      return undefined;
    } catch (error) {
      recordHook(group, event, payload, "failed", error instanceof Error ? error.message : String(error)); persistHookState(pi); throw error;
    }
  });
}
export function hookGroups() {
  return ["taskmanage", "autogenskills", "swarm-prompt", "disk-hooks", "annoyance"] as const;
}
export function renderHookLines() {
  const s = shared.state;
  const rows = hookGroups().map(g => `${isHookEnabled(g) ? "●" : "○"} ${g}`);
  const recent = s.recent.slice(-8).map(r => `  ${r.outcome.toUpperCase()} ${r.event} → ${r.group}${r.tool ? ` · ${r.tool}` : ""}`);
  return ["Swarm hooks (Ctrl+H toggles visibility)", ...rows, `Registered ${s.counts.registered} · Executed ${s.counts.executed} · Blocked ${s.counts.blocked} · Failed ${s.counts.failed} · Skipped ${s.counts.skipped}`, "", "Recent:", ...recent];
}
export function persistHookState(pi: any) { pi.appendEntry?.("pi-swarm-hook-state", { enabled: shared.state.enabled, visible: shared.state.visible, recent: shared.state.recent, counts: shared.state.counts }); }
export function hookTelemetry() { return { counts: { ...shared.state.counts }, recent: shared.state.recent.slice(-200) }; }
export function restoreHookState(entries: readonly any[]) {
  // Extensions register before session_start; do not erase this session's
  // registration count while restoring persisted telemetry.
  const registered = shared.state.counts?.registered ?? 0;
  shared.state.enabled = {};
  shared.state.visible = true;
  shared.state.recent = [];
  shared.state.counts = { registered, executed: 0, blocked: 0, failed: 0, skipped: 0 };
  const e = [...entries].reverse().find(x => x?.type === "pi-swarm-hook-state" || x?.type === "custom" && x?.customType === "pi-swarm-hook-state")?.data;
  if (e) { shared.state.enabled = { ...(e.enabled ?? {}) }; if (typeof e.visible === "boolean") shared.state.visible = e.visible; if (Array.isArray(e.recent)) shared.state.recent = e.recent.slice(-200); if (e.counts && typeof e.counts === "object") shared.state.counts = { ...shared.state.counts, ...e.counts, registered: Math.max(registered, Number(e.counts.registered) || 0) }; }
}
