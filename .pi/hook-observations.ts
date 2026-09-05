export type HookPhase = "before" | "after";
export type HookOutcome = "executed" | "blocked" | "failed" | "skipped";

export interface HookExecutionObservation {
  hookName: string;
  phase: HookPhase;
  outcome: HookOutcome;
  toolCallId: string;
  toolName: string;
  output?: string;
  reason?: string;
  at: string;
}

export interface ToolHookObservations {
  pre: HookExecutionObservation[];
  post: HookExecutionObservation[];
  resultSeen: boolean;
  executionEndSeen: boolean;
}

type ObservationRoot = { calls: Map<string, ToolHookObservations>; seen: Set<string>; listeners: Map<string, Set<() => void>> };
const ROOT = Symbol.for("pi-swarm-hook-observations");
const globalRoot = globalThis as typeof globalThis & { [ROOT]?: ObservationRoot };
const root: ObservationRoot = globalRoot[ROOT] ?? (globalRoot[ROOT] = { calls: new Map(), seen: new Set(), listeners: new Map() });
const { calls, seen, listeners } = root;
const LIMIT = 256;
function trace(stage: string, data: Record<string, unknown>) { if (process.env.SWARM_HOOK_TRACE === "1") try { console.error(JSON.stringify({ stage, ...data })); } catch {} }

function bucket(id: string): ToolHookObservations {
  let value = calls.get(id);
  if (!value) {
    value = { pre: [], post: [], resultSeen: false, executionEndSeen: false };
    calls.set(id, value);
  }
  return value;
}

export function addHookObservation(observation: HookExecutionObservation): boolean {
  const key = `${observation.toolCallId}:${observation.phase}:${observation.hookName}`;
  trace("observation", { tool: observation.toolName, toolCallId: observation.toolCallId, hook: observation.hookName, phase: observation.phase });
  if (seen.has(key)) return false;
  seen.add(key);
  const value = bucket(observation.toolCallId);
  value[observation.phase === "before" ? "pre" : "post"].push(observation);
  for (const listener of listeners.get(observation.toolCallId) ?? []) { trace("invalidate", { toolCallId: observation.toolCallId }); listener(); }
  while (calls.size > LIMIT) {
    const first = calls.keys().next().value;
    if (first) calls.delete(first);
    else break;
  }
  return true;
}

export function getHookObservations(toolCallId: string): ToolHookObservations {
  return calls.get(toolCallId) ?? { pre: [], post: [], resultSeen: false, executionEndSeen: false };
}

export function subscribeHookObservations(toolCallId: string, listener: () => void): () => void {
  let set = listeners.get(toolCallId);
  if (!set) listeners.set(toolCallId, set = new Set());
  set.add(listener);
  return () => { set?.delete(listener); if (set?.size === 0) listeners.delete(toolCallId); };
}

export function markToolResult(toolCallId: string): void { bucket(toolCallId).resultSeen = true; }
export function markExecutionEnd(toolCallId: string): void { bucket(toolCallId).executionEndSeen = true; }
export function clearHookObservations(toolCallId: string): void {
  calls.delete(toolCallId);
  listeners.delete(toolCallId);
  for (const key of [...seen]) if (key.startsWith(`${toolCallId}:`)) seen.delete(key);
}
export function resetHookObservations(): void { calls.clear(); seen.clear(); }
