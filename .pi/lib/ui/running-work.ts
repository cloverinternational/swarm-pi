export type RunningWorkKind = "subagent" | "bash";
export type RunningWorkStatus = "queued" | "running" | "completed" | "failed" | "cancelled";

export interface RunningWorkItem {
  id: string;
  kind: RunningWorkKind;
  label: string;
  status: RunningWorkStatus;
  startedAt: number;
  endedAt?: number;
  tokens?: number;
  detail: string;
  output?: string;
}

const KEY = Symbol.for("pi-swarm-running-work");
type State = { items: Map<string, RunningWorkItem>; listeners: Set<() => void>; expanded: boolean; selected: number };
const state = (): State => {
  const root = globalThis as typeof globalThis & { [KEY]?: State };
  return root[KEY] ?? (root[KEY] = { items: new Map(), listeners: new Set(), expanded: false, selected: 0 });
};

export function runningWorkSnapshot(): RunningWorkItem[] { return [...state().items.values()].sort((a, b) => a.startedAt - b.startedAt); }
export function setRunningWork(item: RunningWorkItem): void { state().items.set(item.id, { ...item }); state().listeners.forEach(listener => listener()); }
export function removeRunningWork(id: string): void { state().items.delete(id); state().listeners.forEach(listener => listener()); }
export function onRunningWorkChange(listener: () => void): () => void { state().listeners.add(listener); return () => state().listeners.delete(listener); }
export function runningWorkExpanded(): boolean { return state().expanded; }
export function setRunningWorkExpanded(expanded: boolean): void { state().expanded = expanded; state().listeners.forEach(listener => listener()); }
export function toggleRunningWorkExpanded(): boolean { setRunningWorkExpanded(!runningWorkExpanded()); return runningWorkExpanded(); }
export function runningWorkSelection(): number { return state().selected; }
export function moveRunningWorkSelection(delta: number): void {
  const items = runningWorkSnapshot();
  if (!items.length) return;
  state().selected = Math.max(0, Math.min(items.length - 1, state().selected + delta));
  state().listeners.forEach(listener => listener());
}
export function selectedRunningWork(): RunningWorkItem | undefined { return runningWorkSnapshot()[state().selected]; }
export function handleRunningWorkInput(data: string, ctx?: any): boolean {
  if (data === "\x02") {
    const detach = (globalThis as any)[Symbol.for("pi-swarm-background-bash-detach")];
    if (typeof detach === "function" && detach()) return true;
    const wait = (globalThis as any)[Symbol.for("pi-swarm-wait-for-agent-background")];
    if (typeof wait === "function" && wait()) return true;
  }
  const items = runningWorkSnapshot();
  if (!items.length) return false;
  const down = data === "\x1b[B" || data === "\x1b[1;B";
  const up = data === "\x1b[A" || data === "\x1b[1;A";
  if (!runningWorkExpanded() && down && !ctx?.editor?.getText?.()) { toggleRunningWorkExpanded(); return true; }
  if (!runningWorkExpanded()) return false;
  if (down) { moveRunningWorkSelection(1); return true; }
  if (up) { moveRunningWorkSelection(-1); return true; }
  if (data === "\x1b") { setRunningWorkExpanded(false); return true; }
  if (data === "\r" || data === "\n") {
    const item = selectedRunningWork();
    if (item) ctx?.ui?.notify?.(`${item.label}\nstatus: ${item.status}\ntime: ${formatRunningWorkDuration(item)}\ntokens: ${item.tokens ?? "not reported"}\n\n${item.output || item.detail}`, "info");
    return true;
  }
  return false;
}
export function formatRunningWorkDuration(item: RunningWorkItem, now = Date.now()): string {
  const seconds = Math.max(0, Math.floor(((item.endedAt ?? now) - item.startedAt) / 1000));
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s`;
}
