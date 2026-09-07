/** Small, session-backed status line for the native Pi editor. */
import { formatRunningWorkDuration, onRunningWorkChange, runningWorkExpanded, runningWorkSelection, visibleRunningWork } from "../../lib/ui/running-work.ts";
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
type Shared = { metrics?: ConversationMetrics; pi?: any; ctx?: any; timer?: ReturnType<typeof setInterval>; frame: number; registered?: WeakSet<object>; footer?: MetricsFooter };
const root = globalThis as typeof globalThis & { [ROOT_KEY]?: Shared };
const shared: Shared = root[ROOT_KEY] ?? (root[ROOT_KEY] = { frame: 0 });

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

export function currentModelLabel(ctx: any): string | undefined {
  const model = ctx?.model;
  if (!model?.id && !model?.provider) return undefined;
  return `${model.provider ?? "unknown"}/${model.id ?? "unknown"}`;
}

export function footerIdentityLine(state: "running" | "idle", model?: string): string {
  return `${state === "running" ? "●" : "○"}  ${model ? `model ${model}` : "model unavailable"}`;
}

export function footerMetricsLine(walltime: string, outputTokens: number, segments: readonly string[] = [], hint?: string): string {
  return [walltime, `↓ ${outputTokens.toLocaleString()} tok`, ...segments, hint].filter(Boolean).join("  ·  ");
}

function styleIdentityLine(line: string, state: "running" | "idle", theme: any): string {
  const marker = state === "running" ? theme.fg("success", "●") : theme.fg("muted", "○");
  const model = line.replace(/^[●○]  /, "");
  return `${marker}  ${model.startsWith("model ") ? `${theme.fg("muted", "model")} ${theme.fg("accent", model.slice(6))}` : theme.fg("muted", model)}`;
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

function persist() {
  shared.metrics!.updatedAt = new Date().toISOString();
  shared.pi?.appendEntry?.(ENTRY, { ...shared.metrics });
}

function currentWalltime(now = Date.now()): number {
  const m = shared.metrics ?? blank();
  return m.walltimeMs + (m.active && m.wallStartedAt ? Math.max(0, now - m.wallStartedAt) : 0);
}

class MetricsFooter {
  constructor(private readonly theme: any, private readonly onInvalidate: () => void) {}
  render(width: number): string[] {
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
    if (!runningWorkExpanded() || !work.length) {
      const activeBash = work.some(item => item.kind === "bash" && item.status === "running");
      const identity = footerIdentityLine(state, model);
      const metrics = footerMetricsLine(formatWalltime(currentWalltime()), m.outputTokens, segments, activeBash ? "Ctrl+B background Bash" : undefined);
      return [
        ...wrapFooterText(identity, width).map((row) => styleIdentityLine(row, state, this.theme)),
        ...wrapFooterText(metrics, width).map((row) => styleMetricsLine(row, this.theme)),
      ];
    }
    const header = `Running work (${work.length})  Down select  Enter inspect  Esc close`;
    const rows = wrapFooterText(header, width).map((row) => this.theme.fg("accent", row));
    for (const [index, item] of work.entries()) {
      const marker = index === runningWorkSelection() ? this.theme.fg("accent", "❯") : " ";
      const glyph = item.status === "running" ? this.theme.fg("success", "●") : item.status === "completed" ? this.theme.fg("success", "✓") : item.status === "failed" ? this.theme.fg("error", "✗") : this.theme.fg("warning", "■");
      const tokens = item.tokens === undefined ? "tokens —" : `tokens ${item.tokens.toLocaleString()}`;
      // Bash commands can be long, sensitive, and numerous. The footer is a
      // status surface, not a command transcript; keep the command itself in
      // the inspection view instead of rendering it for every process.
      const label = item.kind === "bash" ? "bash" : `${item.kind} ${item.label}`;
      const row = `${marker} ${glyph} ${label}  ${item.status}  ${formatRunningWorkDuration(item)}  ${tokens}`;
      rows.push(...wrapFooterText(row, width));
    }
    const metrics = footerMetricsLine(formatWalltime(currentWalltime()), m.outputTokens, segments);
    return [...rows, ...wrapFooterText(metrics, width).map((row) => styleMetricsLine(row, this.theme))];
  }
  dispose() {}
  invalidate() { this.onInvalidate(); }
}

function render(ctx?: any) {
  // Keep this a single native footer line; unlike a widget it cannot push or
  // scroll the user's input box and never becomes transcript content.
  if (!shared.footer) ctx?.ui?.setFooter?.((tui: any, theme: any) => (shared.footer = new MetricsFooter(theme, () => tui?.requestRender?.())));
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
  const refreshWork = () => shared.ctx?.ui?.requestRender?.();
  const workStop = onRunningWorkChange(refreshWork);
  pi.on?.("session_start", (_event: any, ctx: any) => {
      const previous = [...sessionEntries(ctx)].reverse().find((entry: any) => (entry?.type === "custom" && entry?.customType === ENTRY) || entry?.type === ENTRY)?.data;
    shared.metrics = normalize(previous);
    shared.ctx = ctx;
    render(ctx);
    if (!shared.timer) shared.timer = setInterval(() => { shared.frame++; render(shared.ctx); }, 500);
    (shared.timer as any)?.unref?.();
  });
  pi.on?.("agent_start", (_event: any, ctx: any) => {
    const m = shared.metrics ?? (shared.metrics = blank());
    if (!m.active) { m.active = true; m.wallStartedAt = Date.now(); }
    shared.ctx = ctx ?? shared.ctx;
    working(shared.ctx, true);
    saveAndRender(shared.ctx);
  });
  pi.on?.("agent_end", (_event: any, ctx: any) => {
    const m = shared.metrics ?? (shared.metrics = blank());
    if (m.active) { m.walltimeMs = currentWalltime(); m.active = false; m.wallStartedAt = undefined; }
    shared.ctx = ctx ?? shared.ctx;
    working(shared.ctx, false);
    saveAndRender(shared.ctx);
  });
  pi.on?.("message_end", (event: any, ctx: any) => {
    const output = usageOutput(event) || usageOutput(event?.message);
    if (output) { const m = shared.metrics ?? (shared.metrics = blank()); m.outputTokens += output; saveAndRender(ctx ?? shared.ctx); }
  });
  pi.on?.("session_shutdown", () => {
    if (shared.metrics?.active) { shared.metrics.walltimeMs = currentWalltime(); shared.metrics.active = false; shared.metrics.wallStartedAt = undefined; persist(); }
    if (shared.timer) { clearInterval(shared.timer); shared.timer = undefined; }
    shared.footer = undefined;
    workStop();
  });
  pi.registerCommand?.("metrics", { description: "Show this conversation's walltime and output tokens", handler: async (_args: string, ctx: any) => { const m = shared.metrics ?? blank(); ctx.ui?.notify?.(`Conversation: ${formatWalltime(currentWalltime())} walltime · ${m.outputTokens.toLocaleString()} output tokens`, "info"); } });
}
