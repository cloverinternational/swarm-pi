export type HookGroup = "taskmanage" | "autogenskills" | "swarm-prompt" | "hook-controls";
export type HookOutcome = "executed" | "blocked" | "failed" | "skipped";
export interface HookRecord { id: string; group: HookGroup; event: string; at: string; enabled: boolean; outcome: HookOutcome; tool?: string; toolCallId?: string; reason?: string; }
interface State { enabled: Record<string, boolean>; visible: boolean; recent: HookRecord[]; counts: { registered: number; executed: number; blocked: number; failed: number; skipped: number }; }

const KEY = Symbol.for("pi-swarm-hook-state");
type Shared = { state: State; pi?: any };
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
export function isHookEnabled(group: string) { normalizeState(); return shared.state.enabled[group] !== false; }
export function toggleHook(group: string, enabled?: boolean) { shared.state.enabled[group] = enabled ?? !isHookEnabled(group); return isHookEnabled(group); }
export function setHookVisibility(visible?: boolean) { shared.state.visible = visible ?? !shared.state.visible; return shared.state.visible; }
const terminalByCall = new Set<string>();
function eventInfo(payload: any) { return { tool: payload?.toolName ?? payload?.tool_name, toolCallId: payload?.toolCallId ?? payload?.tool_call_id }; }
export function recordHook(group: HookGroup, event: string, payload?: any, outcome: HookOutcome = "executed", reason?: string) {
  const info = eventInfo(payload), record: HookRecord = { id: `${Date.now()}-${Math.random().toString(36).slice(2)}`, group, event, at: new Date().toISOString(), enabled: isHookEnabled(group), outcome, ...info, ...(reason ? { reason } : {}) };
  shared.state.recent.push(record); shared.state.recent = shared.state.recent.slice(-200);
  shared.state.counts[outcome]++;
  if (!shared.state.visible || !shared.pi) return record;
  const callId = info.toolCallId, isPost = event === "tool_result" || event === "tool_execution_end";
  if (isPost && callId) { if (terminalByCall.has(String(callId))) return record; terminalByCall.add(String(callId)); }
  // Successful hook events are routine telemetry and must not become chat
  // messages. Only exceptional outcomes deserve a compact, transient footer
  // status; the complete event history remains available through /hooks.
  if ((outcome === "blocked" || outcome === "failed") && (event === "tool_call" || isPost)) {
    const phase = isPost ? "post" : "pre";
    const suffix = reason ? ` · ${reason}` : "";
    shared.pi.setStatus?.("swarm-hooks", `${phase} ${group} · ${info.tool ?? "tool"} · ${outcome}${suffix}`);
  }
  return record;
}
(globalThis as any).__piSwarmRegisterHook = registerHook;

export function registerHook(pi: any, group: HookGroup, event: string, handler: any) {
  normalizeState();
  shared.state.counts.registered++;
  pi.on(event, async (payload: any, ctx: any) => {
    if (!isHookEnabled(group)) { recordHook(group, event, payload, "skipped", "hook group disabled"); persistHookState(pi); return; }
    recordHook(group, event, payload, "executed");
    try {
      const result = await handler(payload, ctx);
      if (result?.block === true) recordHook(group, event, payload, "blocked", result.reason ?? "blocked by hook");
      persistHookState(pi);
      return result;
    } catch (error) {
      recordHook(group, event, payload, "failed", error instanceof Error ? error.message : String(error)); persistHookState(pi); throw error;
    }
  });
}
export function hookGroups() {
  return ["taskmanage", "autogenskills", "swarm-prompt", "hook-controls"] as const;
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
  const e = [...entries].reverse().find(x => x?.type === "pi-swarm-hook-state")?.data;
  if (e) { shared.state.enabled = { ...(e.enabled ?? {}) }; if (typeof e.visible === "boolean") shared.state.visible = e.visible; if (Array.isArray(e.recent)) shared.state.recent = e.recent.slice(-200); if (e.counts && typeof e.counts === "object") shared.state.counts = { ...shared.state.counts, ...e.counts }; }
}
