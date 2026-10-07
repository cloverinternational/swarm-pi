/** Small, session-backed status line for the native Pi editor. */
import { basename } from "node:path";
import { visibleWidth, truncateToWidth } from "@earendil-works/pi-tui";
import { onRunningWorkChange, visibleRunningWork } from "../../.pi/lib/ui/running-work.ts";
import { openRunningWorkView } from "../../.pi/lib/ui/running-work-view.ts";
import { onAgentSettled } from "../../.pi/lib/runtime/agent-settled.ts";
import { scheduleIdleStatus } from "../../.pi/lib/runtime/schedule-status.ts";
export interface ConversationMetrics {
  version: 1;
  walltimeMs: number;
  wallStartedAt?: number;
  outputTokens: number;
  active: boolean;
  updatedAt: string;
}

const ENTRY = "pi-conversation-metrics";
const ROOT_KEY = Symbol.for("pi-swarm-conversation-metrics");
/**
 * Pi exposes exactly one native footer slot (`ctx.ui.setFooter`). This extension
 * owns it; other extensions contribute text segments through this process-wide
 * registry instead of competing for the slot (autogenskills registers its
 * budget/skill/context segment here). Providers return `undefined` to hide.
 */
export const FOOTER_SEGMENTS_KEY = Symbol.for("pi-swarm-footer-segments");
type FooterSegments = Map<string, () => string | undefined>;
export function footerSegments(): FooterSegments {
  const g = globalThis as typeof globalThis & { [FOOTER_SEGMENTS_KEY]?: FooterSegments };
  return g[FOOTER_SEGMENTS_KEY] ?? (g[FOOTER_SEGMENTS_KEY] = new Map());
}
type Shared = { metrics?: ConversationMetrics; pi?: any; ctx?: any; timer?: ReturnType<typeof setInterval>; frame: number; generation: number; registered?: WeakSet<object>; footer?: MetricsFooter };
const root = globalThis as typeof globalThis & { [ROOT_KEY]?: Shared };
const shared: Shared = root[ROOT_KEY] ?? (root[ROOT_KEY] = { frame: 0, generation: 0 });

const blank = (): ConversationMetrics => ({ version: 1, walltimeMs: 0, outputTokens: 0, active: false, updatedAt: new Date().toISOString() });

function normalize(value: any): ConversationMetrics {
  return { ...blank(), ...(value && typeof value === "object" ? value : {}), version: 1,
    walltimeMs: Math.max(0, Number(value?.walltimeMs) || 0),
    outputTokens: Math.max(0, Number(value?.outputTokens) || 0),
    active: value?.active === true,
    wallStartedAt: Number.isFinite(Number(value?.wallStartedAt)) ? Number(value.wallStartedAt) : undefined };
}

export function formatWalltime(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(seconds / 3600), m = Math.floor(seconds % 3600 / 60), s = seconds % 60;
  return h ? `${h}h ${String(m).padStart(2, "0")}m` : `${m}m ${String(s).padStart(2, "0")}s`;
}

export function wrapFooterText(text: string, width: number): string[] {
  if (width <= 0 || text.length <= width) return [text];
  const lines: string[] = [];
  let current = "";
  for (const word of text.split(/(\s+)/)) {
    if (current && current.length + word.length > width) {
      lines.push(current.trimEnd());
      current = "";
    }
    if (word.length > width) {
      if (current) { lines.push(current.trimEnd()); current = ""; }
      for (let i = 0; i < word.length; i += width) lines.push(word.slice(i, i + width));
    } else current += word;
  }
  if (current) lines.push(current.trimEnd());
  return lines.length ? lines : [""];
}

const ANSI_PATTERN = /^\x1b\[[0-9;]*m/;
/** Terminal cells a string occupies, ignoring ANSI SGR sequences and counting common wide (CJK/emoji) code points as two cells. */
export function footerVisibleWidth(text: string): number {
  let width = 0;
  for (let i = 0; i < text.length;) {
    const ansi = ANSI_PATTERN.exec(text.slice(i));
    if (ansi) { i += ansi[0].length; continue; }
    const code = text.codePointAt(i)!;
    width += cellWidth(code);
    i += code > 0xffff ? 2 : 1;
  }
  return width;
}
// Combining marks, variation selectors and ZWJ occupy no cell of their own; counting
// them made `e\u0301` measure 2 and emoji ZWJ sequences measure 4+, truncating early.
function cellWidth(code: number): number {
  return isZeroWidthCodePoint(code) ? 0 : isWideCodePoint(code) ? 2 : 1;
}
function isZeroWidthCodePoint(code: number): boolean {
  return (code >= 0x0300 && code <= 0x036f) || (code >= 0x1ab0 && code <= 0x1aff) || (code >= 0x1dc0 && code <= 0x1dff)
    || (code >= 0x20d0 && code <= 0x20ff) || (code >= 0xfe00 && code <= 0xfe0f) || (code >= 0xfe20 && code <= 0xfe2f)
    || code === 0x200b || code === 0x200c || code === 0x200d || code === 0xfeff
    || (code >= 0xe0100 && code <= 0xe01ef);
}
function isWideCodePoint(code: number): boolean {
  return (code >= 0x1100 && code <= 0x115f) || (code >= 0x2e80 && code <= 0x9fff) || (code >= 0xac00 && code <= 0xd7a3)
    || (code >= 0xf900 && code <= 0xfaff) || (code >= 0xfe30 && code <= 0xfe4f) || (code >= 0xff00 && code <= 0xff60)
    || (code >= 0xffe0 && code <= 0xffe6) || (code >= 0x1f300 && code <= 0x1faff) || (code >= 0x20000 && code <= 0x3fffd);
}
/**
 * Truncate a possibly-styled line so its visible width never exceeds the
 * terminal width. Pi crashes the whole TUI (`Rendered line exceeds terminal
 * width`) on any oversized footer row, so every rendered line passes through
 * this clamp. ANSI sequences are preserved and never split mid-escape.
 */
export function clampFooterRow(row: string, width: number): string {
  if (width <= 0) return "";
  if (footerVisibleWidth(row) <= width) return row;
  let out = "";
  let used = 0;
  let hadAnsi = false;
  for (let i = 0; i < row.length;) {
    const ansi = ANSI_PATTERN.exec(row.slice(i));
    if (ansi) { out += ansi[0]; i += ansi[0].length; hadAnsi = true; continue; }
    const code = row.codePointAt(i)!;
    const cell = cellWidth(code);
    // A zero-width mark belongs to the character before it, so it must ride along
    // rather than be dropped or counted against the budget.
    if (cell > 0 && used + cell > width) break;
    out += String.fromCodePoint(code);
    used += cell;
    i += code > 0xffff ? 2 : 1;
  }
  return hadAnsi ? out + "\x1b[0m" : out;
}

export function currentModelLabel(ctx: any): string | undefined {
  const model = ctx?.model;
  if (!model?.id && !model?.provider) return undefined;
  return `${model.provider ?? "unknown"}/${model.id ?? "unknown"}`;
}

export function footerIdentityLine(state: "running" | "idle", model?: string): string {
  return `${state === "running" ? "●" : "○"}  ${model ? `model ${model}` : "model unavailable"}`;
}

export function footerMetricsLine(walltime: string, outputTokens: number, segments: readonly string[] = []): string {
  return [walltime, `↓ ${outputTokens.toLocaleString()} tok`, ...segments].filter(Boolean).join("  ·  ");
}

function styleIdentityLine(line: string, state: "running" | "idle", theme: any): string {
  const marker = state === "running" ? theme.fg("success", "●") : theme.fg("muted", "○");
  const model = line.replace(/^[●○]  /, "");
  const styledModel = model.startsWith("model ") ? theme.fg("muted", "model") + " " + theme.fg("accent", model.slice(6)) : theme.fg("muted", model);
  return marker + "  " + styledModel;
}
function styleMetricsLine(line: string, theme: any): string {
  return line.replace(/(\d+h \d+m|\d+m \d+s)/, (time) => theme.fg("dim", time)).replace(/(↓ [\d,]+ tok)/, (tokens) => theme.fg("dim", tokens));
}

function usageOutput(message: any): number {
  const usage = message?.usage ?? message?.data?.usage ?? message?.message?.usage;
  return Math.max(0, Number(usage?.output ?? usage?.outputTokens ?? usage?.completion_tokens ?? usage?.completionTokens) || 0);
}

function sessionEntries(ctx: any): readonly any[] {
  return ctx?.sessionManager?.getEntries?.() ?? ctx?.sessionManager?.getBranch?.() ?? [];
}

/**
 * True while a ctx can still be dereferenced. Pi invalidates every ctx getter
 * (`ui`, `model`, ...) after session replacement or reload, so a captured ctx
 * throws on its next property access. Assigning a stale ctx into `shared`
 * would poison the footer, the interval and `persist()` for the rest of the
 * process, because `shared` survives reload on `globalThis`.
 */
function ctxAlive(ctx: any): boolean {
  if (!ctx) return false;
  try { return ctx.ui != null; } catch { return false; }
}

function persist() {
  shared.metrics!.updatedAt = new Date().toISOString();
  try { shared.pi?.appendEntry?.(ENTRY, { ...shared.metrics }); }
  catch { /* stale after session replacement or reload; the next session re-persists */ }
}

function currentWalltime(now = Date.now()): number {
  const m = shared.metrics ?? blank();
  return m.walltimeMs + (m.active && m.wallStartedAt ? Math.max(0, now - m.wallStartedAt) : 0);
}

class MetricsFooter {
  private readonly unsubscribeBranch?: () => void;
  constructor(private readonly theme: any, private readonly onInvalidate: () => void, private readonly footerData?: any) {
    this.unsubscribeBranch = footerData?.onBranchChange?.(onInvalidate);
  }
  render(width: number): string[] {
    try { return this.renderRows(Math.max(1, Math.floor(Number(width) || 80))); }
    catch (error) {
      // A footer defect must never take down the whole TUI, but swallowing it
      // silently hides real regressions, so report it once per session.
      // The notify itself is guarded: `shared.ctx` may be stale during the
      // reload window, and a throw from this catch would escape into the TUI's
      // render timer as an uncaughtException and kill the process. A dead ctx
      // means the render error is that transitional staleness, not a footer
      // defect; the next session_start re-renders with a fresh ctx.
      if (!ctxAlive(shared.ctx)) return [];
      if (!this.reportedRenderError) {
        this.reportedRenderError = true;
        shared.ctx.ui.notify?.(`Metrics footer disabled after render error: ${error instanceof Error ? error.message : error}`, "warning");
      }
      return [];
    }
  }
  private reportedRenderError = false;
  private renderRows(width: number): string[] {
    const m = shared.metrics ?? blank();
    const state = m.active ? "running" : "idle";
    const model = currentModelLabel(shared.ctx);
    const segments: string[] = [];
    for (const [, provider] of footerSegments()) {
      let text: string | undefined;
      try { text = provider(); } catch { text = undefined; }
      if (text) segments.push(text);
    }
    const work = visibleRunningWork();
    const fg = (color: string, text: string) => this.theme.fg(color, text);
    const clean = (text: unknown) => String(text ?? "").replace(/[\x00-\x1f\x7f-\x9f]/g, "");
    const inner = Math.max(1, width - 2);
    const fit = (text: string, cells = inner) => truncateToWidth(text, Math.max(1, cells));
    const pair = (left: string, right: string): string[] => {
      const room = inner - visibleWidth(right) - 3;
      if (room >= Math.min(visibleWidth(left), 12)) {
        const l = fit(left, room);
        return [l + " ".repeat(Math.max(1, inner - visibleWidth(l) - visibleWidth(right))) + right];
      }
      return [fit(left), fit(right)];
    };
    const branch = clean(this.footerData?.getGitBranch?.());
    const workspace = clean(basename(shared.ctx?.cwd ?? "")) || "workspace";
    const location = this.theme.bold(fg("success", workspace)) + (branch ? fg("dim", " / " + branch) : "");
    const current = shared.ctx?.model;
    const identity = this.theme.bold(fg("accent", clean(current?.id) || "model unavailable"))
      + (width >= 90 && current?.provider ? fg("dim", " · " + clean(current.provider)) : "");
    const waiting = m.active ? undefined : scheduleIdleStatus(shared.ctx?.sessionManager?.getSessionId?.());
    const status = fg(m.active ? "success" : waiting ? "accent" : "dim", m.active ? "WORKING" : waiting ?? "IDLE");
    const rows = pair(location, identity + "   " + status);
    const usage = shared.ctx?.getContextUsage?.();
    const percent = typeof usage?.percent === "number" && Number.isFinite(usage.percent) ? usage.percent : undefined;
    const pressure = percent !== undefined && percent >= 90 ? "error" : percent !== undefined && percent >= 70 ? "warning" : "accent";
    const filled = percent === undefined ? 0 : Math.max(0, Math.min(8, Math.round(percent / 100 * 8)));
    const gauge = width >= 70 && percent !== undefined
      ? fg(pressure, "▰".repeat(filled)) + fg("dim", "▱".repeat(8 - filled)) + " " : "";
    const context = fg("dim", "Context ") + gauge + fg(pressure, percent === undefined ? "—" : `${Math.round(percent)}%`);
    const output = m.outputTokens >= 1000 ? `${(m.outputTokens / 1000).toFixed(1)}k` : String(m.outputTokens);
    const metrics = fg("accent", `↓ ${output}`) + fg("dim", ` out  ·  ${formatWalltime(currentWalltime())} active`);
    rows.push(...pair(context, metrics));
    // Keep complete extension groups together instead of splitting labels by word.
    let detail = "";
    for (const segment of segments) {
      const next = detail ? detail + "  ·  " + clean(segment) : clean(segment);
      if (detail && visibleWidth(next) > inner) { rows.push(fg("dim", fit(detail))); detail = clean(segment); }
      else detail = next;
    }
    if (detail) rows.push(fg("dim", fit(detail)));
    if (work.length) rows.push(fg("dim", fit(`↓ browse work (${work.length})`)));
    return rows.map(row => {
      const fitted = fit(row);
      const padded = " " + fitted + " ".repeat(Math.max(0, width - 1 - visibleWidth(fitted)));
      return truncateToWidth(padded, width);
    });
  }
  dispose() { this.unsubscribeBranch?.(); }
  invalidate() { this.onInvalidate(); }
}

function render(ctx?: any) {
  // This owns compact metrics; the focused work browser uses Pi custom UI
  // to replace the prompt editor rather than growing this footer.
  if (!shared.footer) ctx?.ui?.setFooter?.((tui: any, theme: any, footerData: any) => (shared.footer = new MetricsFooter(theme, () => tui?.requestRender?.(), footerData)));
  ctx?.ui?.requestRender?.();
}

function working(ctx: any, visible: boolean) {
  const ui = ctx?.ui;
  if (!ui) return;
  ui.setWorkingMessage?.(visible ? "Thinking" : undefined);
  ui.setWorkingIndicator?.(visible ? { frames: ["·", "•", "●", "•"], intervalMs: 120 } : undefined);
  ui.setWorkingVisible?.(visible);
}

function saveAndRender(ctx: any) { persist(); render(ctx); }

export default function conversationMetricsExtension(pi: any) {
  // Guard per ExtensionAPI instance, not per process: Pi's /reload re-evaluates
  // this module and hands it a fresh `pi`, while `globalThis` (and therefore
  // `shared`) survives. A process-wide boolean made the factory return early on
  // reload, so no handlers, no /metrics command and no footer were registered.
  const registered = shared.registered ?? (shared.registered = new WeakSet<object>());
  if (registered.has(pi)) return;
  registered.add(pi);
  shared.pi = pi;
  const refreshWork = () => { const ctx = shared.ctx; if (ctxAlive(ctx)) ctx.ui?.requestRender?.(); };
  const workStop = onRunningWorkChange(refreshWork);
  pi.on?.("session_start", (_event: any, ctx: any) => {
    shared.generation++;
      const previous = [...sessionEntries(ctx)].reverse().find((entry: any) => (entry?.type === "custom" && entry?.customType === ENTRY) || entry?.type === ENTRY)?.data;
    shared.metrics = normalize(previous);
    shared.ctx = ctx;
    render(ctx);
    if (!shared.timer) shared.timer = setInterval(() => {
      const ctx = shared.ctx;
      if (!ctx) return;
      // A ghost event can poison shared.ctx after the runner was invalidated
      // (e.g. a queued continuation racing a reload); drop it and wait for the
      // next session_start instead of throwing inside this timer callback.
      if (!ctxAlive(ctx)) { shared.ctx = undefined; return; }
      shared.frame++;
      render(ctx);
    }, 500);
    (shared.timer as any)?.unref?.();
  });
  pi.on?.("agent_start", (_event: any, ctx: any) => {
    const m = shared.metrics ?? (shared.metrics = blank());
    if (!m.active) { m.active = true; m.wallStartedAt = Date.now(); }
    // A ghost event after invalidation passes a stale ctx; assigning it bare
    // would poison shared.ctx for every later render (the assignment cannot
    // throw, the next property access does).
    shared.ctx = ctxAlive(ctx) ? ctx : shared.ctx;
    if (!ctxAlive(shared.ctx)) return;
    working(shared.ctx, true);
    saveAndRender(shared.ctx);
  });
  onAgentSettled(pi, (_event: any, ctx: any) => {
    const m = shared.metrics ?? (shared.metrics = blank());
    if (m.active) { m.walltimeMs = currentWalltime(); m.active = false; m.wallStartedAt = undefined; }
    shared.ctx = ctxAlive(ctx) ? ctx : shared.ctx;
    if (!ctxAlive(shared.ctx)) return;
    working(shared.ctx, false);
    saveAndRender(shared.ctx);
  });
  pi.on?.("message_end", (event: any, ctx: any) => {
    const output = usageOutput(event) || usageOutput(event?.message);
    if (output) { const m = shared.metrics ?? (shared.metrics = blank()); m.outputTokens += output; saveAndRender(ctx ?? shared.ctx); }
  });
  pi.on?.("session_shutdown", () => {
    shared.generation++;
    if (shared.metrics?.active) { shared.metrics.walltimeMs = currentWalltime(); shared.metrics.active = false; shared.metrics.wallStartedAt = undefined; persist(); }
    if (shared.timer) { clearInterval(shared.timer); shared.timer = undefined; }
    shared.footer = undefined;
    shared.ctx = undefined;
    shared.pi = undefined;
    workStop();
  });
  pi.registerCommand?.("metrics", { description: "Show this conversation's walltime and output tokens", handler: async (_args: string, ctx: any) => { const m = shared.metrics ?? blank(); ctx.ui?.notify?.(`Conversation: ${formatWalltime(currentWalltime())} walltime · ${m.outputTokens.toLocaleString()} output tokens`, "info"); } });
  pi.registerCommand?.("work", { description: "Browse recent Bash commands and subagents", handler: async (_args: string, ctx: any) => {
    if (ctx.mode !== "tui") { ctx.ui?.notify?.("Work browser requires interactive TUI mode", "warning"); return; }
    if (!visibleRunningWork().length) { ctx.ui?.notify?.("No work items in this Pi session yet", "info"); return; }
    if (!openRunningWorkView(ctx)) ctx.ui?.notify?.("Work browser is already open or unavailable", "warning");
  } });
}
