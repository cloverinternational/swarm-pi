/** Small, session-backed status line for the native Pi editor. */
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
type Shared = { metrics?: ConversationMetrics; pi?: any; ctx?: any; timer?: ReturnType<typeof setInterval>; frame: number; registered?: boolean; footer?: MetricsFooter };
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
  render(): string[] {
    const m = shared.metrics ?? blank();
    const state = m.active ? "running" : "idle";
    return [this.theme.fg("dim", `${state}  ${formatWalltime(currentWalltime())}  ·  out ${m.outputTokens.toLocaleString()} tok`)];
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
  if (shared.registered) return;
  shared.registered = true;
  shared.pi = pi;
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
  });
  pi.registerCommand?.("metrics", { description: "Show this conversation's walltime and output tokens", handler: async (_args: string, ctx: any) => { const m = shared.metrics ?? blank(); ctx.ui?.notify?.(`Conversation: ${formatWalltime(currentWalltime())} walltime · ${m.outputTokens.toLocaleString()} output tokens`, "info"); } });
}
