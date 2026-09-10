import { randomUUID } from "node:crypto";
import { createSessionWakeup } from "../runtime/session-wakeup.ts";

/** A provider-neutral long-running monitor. The check is an agent instruction,
 * so the target can be a PR, deployment, inbox, filesystem, API, ticket, or
 * any other thing the current session's tools can observe. */
export type Monitor = {
  id: string;
  target: string;
  check: string;
  intervalMs: number;
  action?: string;
  nextAt: number;
  lastCheckedAt?: number;
  status: "active" | "paused";
  attempts: number;
  maxAttempts: number;
  lastObservation?: Observation;
  history: Observation[];
};
export type ObservationStatus = "complete" | "pending" | "blocked" | "failed" | "unavailable" | "invalid";
export type Observation = { status: ObservationStatus; summary: string; evidence: string[]; observedAt: number; retryable: boolean; attempts: number };
export type MonitorOptions = { now?: () => number; append?: (data: unknown) => void; notify?: (message: string, level?: string) => void };

const ENTRY = "pi-swarm-monitor";
const MAX_MONITORS = 100;
const MAX_INTERVAL = 24 * 60 * 60_000;
const result = (value: unknown) => ({ content: [{ type: "text", text: JSON.stringify(value) }], details: value });
const parseInterval = (value: unknown): number => {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value !== "string" || !/^\d+(?:\.\d+)?[smhd]$/.test(value)) throw new Error("interval must look like 30s, 5m, 2h, or 1d");
  const unit = value.at(-1); const amount = Number(value.slice(0, -1));
  return amount * ({ s: 1_000, m: 60_000, h: 3_600_000, d: 86_400_000 } as any)[unit as string];
};
const clean = (value: unknown, label: string, max = 8_000): string => {
  if (typeof value !== "string" || !value.trim() || value.length > max) throw new Error(`${label} must be 1-${max} characters`);
  return value.trim();
};
const bounded = (value: unknown, max = 2_000) => String(value ?? "").replace(/(?:api[_-]?key|token|password|secret)\s*[:=]\s*\S+/gi, "[REDACTED]").slice(0, max);
function observationFromMessage(message: unknown, attempts: number, now: number): Observation | undefined {
  const text = typeof message === "string" ? message : JSON.stringify(message ?? "");
  const match = /(?:status\s*[:=]\s*|\bstatus\b\s+)(complete|pending|blocked|failed|unavailable|invalid)/i.exec(text);
  if (!match) return;
  const status = match[1].toLowerCase() as ObservationStatus;
  const retryable = status === "pending" || status === "blocked" || status === "unavailable" || status === "failed";
  const lines = text.split(/\r?\n/).map(line => bounded(line.trim(), 500)).filter(Boolean).slice(0, 8);
  return { status, summary: bounded(lines[0] ?? status, 500), evidence: lines, observedAt: now, retryable, attempts };
}

export function registerSwarmMonitor(pi: any, options: MonitorOptions = {}) {
  const now = options.now ?? Date.now;
  const monitors = new Map<string, Monitor>();
  const wake = createSessionWakeup(pi);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let live = true;
  const pendingChecks = new Set<string>();
  const notify = options.notify ?? ((message, level = "info") => pi.__swarmMonitorContext?.ui?.notify?.(message, level));
  const append = options.append ?? ((data) => pi.appendEntry?.(ENTRY, data));
  const persist = () => append([...monitors.values()]);
  const describe = (m: Monitor) => ({ ...m, next_check_at: m.nextAt >= 0 && m.nextAt <= 8_640_000_000_000_000 ? new Date(m.nextAt).toISOString() : null, interval: `${m.intervalMs / 1000}s` });
  const arm = () => {
    if (timer) clearTimeout(timer);
    const active = [...monitors.values()].filter(m => m.status === "active");
    if (!live || !active.length) return;
    const delay = Math.max(0, Math.min(...active.map(m => m.nextAt - now())));
    timer = setTimeout(() => void pollDue(), delay);
    timer.unref?.();
  };
  const pollDue = async () => {
    if (!live) return;
    const due = [...monitors.values()].filter(m => m.status === "active" && m.nextAt <= now());
    for (const monitor of due) {
      monitor.lastCheckedAt = now(); monitor.attempts++; pendingChecks.add(monitor.id);
      monitor.nextAt = now() + monitor.intervalMs;
      const action = monitor.action ? `\nIf the condition is met, follow up with this action (only when safe and authorized): ${monitor.action}` : "";
      const prompt = `[MONITOR ${monitor.id}] Check target: ${monitor.target}\nInspection instructions: ${monitor.check}${action}\nReport exactly one status (complete, pending, blocked, failed, unavailable, or invalid), a concise summary, and concrete evidence. Do not claim completion without fresh evidence.`;
      await wake.send({ customType: "swarm-monitor", content: prompt, display: true, details: { monitorId: monitor.id, target: monitor.target } }, wake.capture());
      append({ event: "check-requested", monitorId: monitor.id, target: monitor.target, attempt: monitor.attempts, at: new Date(now()).toISOString() });
    }
    persist(); arm();
  };
  const load = (ctx: any) => {
    const entries = ctx.sessionManager?.getBranch?.() ?? [];
    const last = [...entries].reverse().find((e: any) => e.customType === ENTRY && Array.isArray(e.data));
    if (!last) return;
    monitors.clear();
    for (const raw of last.data) if (raw?.id && raw?.target && raw?.check && Number.isFinite(raw.intervalMs)) monitors.set(raw.id, { ...raw, attempts: Number.isInteger(raw.attempts) ? raw.attempts : 0, maxAttempts: Number.isInteger(raw.maxAttempts) ? raw.maxAttempts : 3, history: Array.isArray(raw.history) ? raw.history.slice(-10) : [], status: raw.status === "paused" ? "paused" : "active" });
  };
  pi.registerTool?.({ name: "monitor_agent", label: "Monitor agent", description: "Monitor any long-running target using periodic agent checks. Targets may be GitHub PRs, deployments, tickets, files, APIs, inboxes, jobs, or anything the session can inspect. Actions never happen unless explicitly included in the monitor action and permitted by normal policy.", parameters: { type: "object", required: ["action"], properties: { action: { type: "string", enum: ["create", "list", "pause", "resume", "cancel", "check"] }, id: { type: "string" }, target: { type: "string" }, check: { type: "string" }, interval: { type: ["string", "number"] }, max_attempts: { type: "integer", minimum: 1, maximum: 5 }, follow_up: { type: "string" } } }, execute: async (_id: string, p: any) => {
    if (p.action === "list") return result([...monitors.values()].map(describe));
    if (p.action === "create") {
      if (monitors.size >= MAX_MONITORS) throw new Error(`monitor limit reached (${MAX_MONITORS})`);
      const intervalMs = parseInterval(p.interval ?? "5m");
      if (intervalMs < 5_000 || intervalMs > MAX_INTERVAL) throw new Error("interval must be between 5s and 24h");
      const maxAttempts = Number.isInteger(p.max_attempts) ? p.max_attempts : 3;
      if (maxAttempts < 1 || maxAttempts > 5) throw new Error("max_attempts must be between 1 and 5");
      const monitor: Monitor = { id: `monitor-${randomUUID()}`, target: clean(p.target, "target"), check: clean(p.check, "check"), ...(p.follow_up === undefined ? {} : { action: clean(p.follow_up, "follow_up") }), intervalMs, nextAt: now(), status: "active", attempts: 0, maxAttempts, history: [] };
      monitors.set(monitor.id, monitor); persist(); arm(); return result(describe(monitor));
    }
    if (typeof p.id !== "string" || !monitors.has(p.id)) throw new Error("monitor id not found");
    const monitor = monitors.get(p.id)!;
    if (p.action === "cancel") { monitors.delete(p.id); persist(); arm(); return result({ cancelled: p.id }); }
    if (p.action === "pause") monitor.status = "paused";
    else if (p.action === "resume") { monitor.status = "active"; monitor.nextAt = now(); }
    else if (p.action === "check") { monitor.nextAt = now(); await pollDue(); return result(describe(monitor)); }
    else throw new Error("action must be create, list, pause, resume, cancel, or check");
    persist(); arm(); return result(describe(monitor));
  } });
  pi.on?.("agent_end", (event: any) => {
    const text = (event?.messages ?? []).map((m: any) => typeof m?.content === "string" ? m.content : JSON.stringify(m?.content ?? "")).join("\n");
    const eventMonitorId = typeof event?.monitorId === "string" ? event.monitorId : undefined;
    const candidates = [...monitors.values()].filter(m => m.id === eventMonitorId || text.includes(m.id) || text.includes(`[MONITOR ${m.id}]`));
    // In headless hosts the delivered turn may omit the injected prompt. If
    // exactly one monitor is due, correlate that turn to it rather than lose
    // the observation; multiple monitors remain fail-closed.
    const dueFallback = [...monitors.values()].filter(m => pendingChecks.has(m.id));
    const matched = candidates.length === 1 ? candidates : candidates.length === 0 && dueFallback.length === 1 ? dueFallback : [];
    for (const monitor of matched) {
      const observation = observationFromMessage(text, monitor.attempts, now());
      if (!observation) continue;
      pendingChecks.delete(monitor.id); monitor.lastObservation = observation; monitor.history = [...monitor.history, observation].slice(-10);
      if (observation.status === "complete" || observation.status === "invalid" || !observation.retryable || monitor.attempts >= monitor.maxAttempts) monitor.nextAt = Number.MAX_SAFE_INTEGER;
      else monitor.nextAt = now() + monitor.intervalMs;
      append({ event: "observation", monitorId: monitor.id, observation });
      notify(`${monitor.target}: ${observation.status}`);
    }
    persist(); arm();
  });
  pi.on?.("session_start", (_event: any, ctx: any) => { pi.__swarmMonitorContext = ctx; live = true; load(ctx); arm(); });
  pi.on?.("session_shutdown", () => { live = false; if (timer) clearTimeout(timer); timer = undefined; });
  return { monitors };
}
