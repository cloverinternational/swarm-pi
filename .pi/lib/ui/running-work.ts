import { readFileSync } from "node:fs";
import { matchesKey } from "@earendil-works/pi-tui";
import { openRunningWorkView } from "./running-work-view.ts";

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
  /** Live snapshot of the process output; used while the work is still running. */
  readOutput?: () => string;
  transcriptPath?: string;
}

const KEY = Symbol.for("pi-swarm-running-work");
export const MAX_VISIBLE_RUNNING_WORK = 6;
export const RECENT_WORK_MS = 15 * 60 * 1000;
type State = { items: Map<string, RunningWorkItem>; listeners: Set<() => void>; expanded: boolean; selected: number; sessionId?: string };
const state = (): State => {
  const root = globalThis as typeof globalThis & { [KEY]?: State };
  return root[KEY] ?? (root[KEY] = { items: new Map(), listeners: new Set(), expanded: false, selected: 0 });
};

export function runningWorkSnapshot(): RunningWorkItem[] { return [...state().items.values()].sort((a, b) => a.startedAt - b.startedAt); }
export function browsableRunningWork(now = Date.now()): RunningWorkItem[] {
  // Live work is never evicted by recent completions: cap the live set first, then add recent history.
  const live = runningWorkSnapshot().filter(item => item.status === "queued" || item.status === "running");
  const recent = runningWorkSnapshot().filter(item => !(item.status === "queued" || item.status === "running") &&
    (item.endedAt ?? item.startedAt) <= now && now - (item.endedAt ?? item.startedAt) < RECENT_WORK_MS);
  return [...live, ...recent].sort((a, b) => a.startedAt - b.startedAt).slice(-Math.max(MAX_VISIBLE_RUNNING_WORK, live.length));
}
/** A reload retains this session's work; switching sessions must not carry its history. */
export function scopeRunningWorkToSession(sessionId: string | undefined): void {
  if (!sessionId) return;
  const current = state();
  if (current.sessionId && current.sessionId !== sessionId) {
    current.items.clear(); current.selected = 0; current.expanded = false;
    current.listeners.forEach(listener => listener());
  }
  current.sessionId = sessionId;
}
/** Keep the drawer useful when many background commands have accumulated. */
export function visibleRunningWork(items = runningWorkSnapshot()): RunningWorkItem[] {
  return items.length <= MAX_VISIBLE_RUNNING_WORK ? items : items.slice(-MAX_VISIBLE_RUNNING_WORK);
}
export function setRunningWork(item: RunningWorkItem): void { state().items.set(item.id, { ...item }); state().listeners.forEach(listener => listener()); }
export function removeRunningWork(id: string): void { state().items.delete(id); state().listeners.forEach(listener => listener()); }
export function onRunningWorkChange(listener: () => void): () => void { state().listeners.add(listener); return () => state().listeners.delete(listener); }
export function runningWorkExpanded(): boolean { return state().expanded; }
export function setRunningWorkExpanded(expanded: boolean): void { state().expanded = expanded; state().listeners.forEach(listener => listener()); }
export function toggleRunningWorkExpanded(): boolean { setRunningWorkExpanded(!runningWorkExpanded()); return runningWorkExpanded(); }
/** Selection clamped to the currently visible list so a stale index (after items finished or were removed) still resolves to a real row. */
export function runningWorkSelection(): number {
  return Math.max(0, Math.min(state().selected, browsableRunningWork().length - 1));
}
export function moveRunningWorkSelection(delta: number): void {
  const items = browsableRunningWork();
  if (!items.length) return;
  state().selected = Math.max(0, Math.min(items.length - 1, runningWorkSelection() + delta));
  state().listeners.forEach(listener => listener());
}
export function selectedRunningWork(): RunningWorkItem | undefined {
  const items = browsableRunningWork();
  return items.length ? items[runningWorkSelection()] : undefined;
}
/** Enter can arrive as CR/LF or Kitty keyboard protocol CSI-u when Pi enables enhanced key reporting. */
export function isRunningWorkEnter(data: string): boolean {
  return data === "\r" || data === "\n" || data === "\x1b[13u" || /^\x1b\[13;[1-9]u$/.test(data);
}
/** Keep inspection text safe for Pi's strict line-width renderer. */
export function safeInspectionText(value: string, width = 120): string {
  // Child transcripts and shell output can contain OSC hyperlinks/ANSI color
  // sequences. They are useful in a pipe but unsafe in editor prefill text:
  // Pi's editor validates visible width and can terminate the TUI on a line
  // that is only a few cells over the terminal boundary.
  const plain = String(value)
    .replace(/\x1b\][^\x07]*(?:\x07|\x1b\\)/g, "")
    .replace(/\x1b\[[0-?]*[ -\/]*[@-~]/g, "");
  const limit = Math.max(1, Math.min(120, Math.floor(Number(width) || 120)));
  return plain.split("\n").flatMap((line) => {
    if (!line) return [""];
    // Measure display cells, not UTF-16 units: slicing by code unit can split a
    // surrogate pair, and CJK/emoji occupy two columns each.
    const chunks: string[] = [];
    let chunk = "";
    let used = 0;
    for (const char of line) {
      const cell = isWideChar(char.codePointAt(0)!) ? 2 : 1;
      if (used + cell > limit) { chunks.push(chunk); chunk = ""; used = 0; }
      chunk += char;
      used += cell;
    }
    if (chunk) chunks.push(chunk);
    return chunks.length ? chunks : [""];
  }).join("\n");
}
function isWideChar(code: number): boolean {
  return (code >= 0x1100 && code <= 0x115f) || (code >= 0x2e80 && code <= 0x9fff) || (code >= 0xac00 && code <= 0xd7a3)
    || (code >= 0xf900 && code <= 0xfaff) || (code >= 0xff00 && code <= 0xff60) || (code >= 0x1f300 && code <= 0x1faff)
    || (code >= 0x20000 && code <= 0x3fffd);
}
/** `readOutput` is supplied by the caller and can throw once its buffer is disposed. */
function liveOutput(item: RunningWorkItem): string | undefined {
  try { return item.readOutput?.() ?? item.output; } catch { return item.output; }
}
function openInspection(ctx: any, title: string, body: string): void {
  const safeBody = safeInspectionText(body);
  try {
    if (typeof ctx?.ui?.editor === "function") {
      // Catch both synchronous UI failures and rejected editor promises. A
      // missing/closing child view must never become an uncaught rejection.
      Promise.resolve(ctx.ui.editor(title, safeBody)).catch(() => ctx?.ui?.notify?.("Unable to open work inspection", "warning"));
    } else ctx?.ui?.notify?.(safeBody, "info");
  } catch { ctx?.ui?.notify?.("Unable to open work inspection", "warning"); }
}
export function handleRunningWorkInput(data: string, ctx?: any): boolean {
  if (data === "\x02") {
    const detach = (globalThis as any)[Symbol.for("pi-swarm-background-bash-detach")];
    if (typeof detach === "function" && detach()) return true;
    const wait = (globalThis as any)[Symbol.for("pi-swarm-wait-for-agent-background")];
    if (typeof wait === "function" && wait()) return true;
  }
  if (!browsableRunningWork().length) return false;
  // Match Pi's own input decoding (including Kitty/CSI modifier forms), not
  // only the legacy terminal byte sequence used by one terminal.
  const down = matchesKey(data, "down");
  const up = matchesKey(data, "up");
  if (!runningWorkExpanded() && down && !ctx?.editor?.getText?.()) return openRunningWorkView(ctx);
  if (!runningWorkExpanded()) return false;
  if (down) { moveRunningWorkSelection(1); return true; }
  if (up) { moveRunningWorkSelection(-1); return true; }
  if (data === "\x1b") { setRunningWorkExpanded(false); return true; }
  if (isRunningWorkEnter(data)) {
    const item = selectedRunningWork();
    if (item) {
      // Re-read the transcript on every open so inspecting a running
      // sub-agent shows its current conversation, not a stale snapshot.
      if (item.transcriptPath) {
        const header = `${item.label}\nstatus: ${item.status}\ntime: ${formatRunningWorkDuration(item)}\ntask: ${item.detail}\n\n`;
        // A child can finish or clean up its session file between the footer
        // render and Enter. Keep the inspection useful instead of exposing a
        // raw ENOENT from the deleted transcript path.
        const transcript = readTranscript(item.transcriptPath);
        const fallback = liveOutput(item) ?? "(conversation transcript is unavailable; no buffered output remains)";
        const body = header + (transcript ?? fallback);
        openInspection(ctx, `${item.label} · conversation`, body);
      }
      else {
        // A running item has no final `output` yet; prefer the live snapshot
        // so inspecting an in-flight command shows what it is doing right now.
        const live = liveOutput(item);
        const body = `${item.label}\nstatus: ${item.status}\ntime: ${formatRunningWorkDuration(item)}\ntokens: ${item.tokens ?? "not reported"}\n${item.kind === "bash" ? "command" : "task"}: ${item.detail}\n\n${live?.trim() ? live : "(no output yet)"}`;
        openInspection(ctx, `${item.label} · output`, body);
      }
    }
    return true;
  }
  // Once the user starts editing a prompt, the work drawer must get out of
  // the way immediately. Keep navigation/inspection keys above, but let the
  // editor receive ordinary input after collapsing the drawer.
  if (runningWorkExpanded()) setRunningWorkExpanded(false);
  return false;
}
function readTranscript(path: string): string | undefined {
  try {
    const rows = readFileSync(path, "utf8").split("\n").filter(Boolean).slice(-160);
    const out: string[] = [];
    for (const row of rows) {
      try {
        const message = JSON.parse(row)?.message;
        if (!message?.role) continue;
        const content = Array.isArray(message.content) ? message.content.map((part: any) => part?.text ?? part?.content ?? (part?.type === "toolCall" ? `[tool: ${part.name ?? "unknown"}]` : "")).filter(Boolean).join("\n") : String(message.content ?? "");
        if (content.trim()) out.push(`${String(message.role).toUpperCase()}\n${content.trim()}`);
      } catch { /* ignore partial records */ }
    }
    return out.length ? out.join("\n\n────────────────────────────────────────\n\n") : "(conversation transcript is empty)";
  } catch (error) {
    // ENOENT is an expected cleanup race for completed/extinct children. Do
    // not render an absolute path or filesystem exception in the TUI.
    if ((error as NodeJS.ErrnoException)?.code === "ENOENT") return undefined;
    return `(conversation transcript unavailable: ${error instanceof Error ? error.message : String(error)})`;
  }
}
export function formatRunningWorkDuration(item: RunningWorkItem, now = Date.now()): string {
  const seconds = Math.max(0, Math.floor(((item.endedAt ?? now) - item.startedAt) / 1000));
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s`;
}

/** Compact one-line label; full command/task detail stays in inspection. */
export function runningWorkListLabel(item: RunningWorkItem, maxPreview = 36): string {
  if (item.kind !== "bash") return `${item.kind} ${item.label}`;
  const preview = String(item.detail ?? "")
    .replace(/\x1b\][^\x07]*(?:\x07|\x1b\\)/g, "")
    .replace(/\x1b\[[0-?]*[ -\/]*[@-~]/g, "")
    .replace(/[\x00-\x1f\x7f-\x9f]/g, " ")
    .replace(/\s+/g, " ").trim();
  if (!preview) return "bash";
  const chars = [...preview];
  const limit = Math.max(1, maxPreview);
  return `bash · ${chars.length > limit ? `${chars.slice(0, limit - 1).join("")}…` : preview}`;
}

export function runningWorkFooterHints(): string {
  const hints = ["↑/↓ select", "Enter inspect", "Esc close"];
  const globals = globalThis as any;
  if (typeof globals[Symbol.for("pi-swarm-background-bash-detach")] === "function"
    || typeof globals[Symbol.for("pi-swarm-wait-for-agent-background")] === "function") {
    hints.push("Ctrl+B detach/wait");
  }
  return hints.join("  ");
}
