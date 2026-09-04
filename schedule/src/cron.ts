import { Cron, CronPattern } from "croner";

const COMMON_SCHEDULES: Readonly<Record<string, string>> = {
  "*/1 * * * *": "every minute",
  "*/5 * * * *": "every 5 minutes",
  "*/15 * * * *": "every 15 minutes",
  "*/30 * * * *": "every 30 minutes",
  "0 * * * *": "every hour",
  "0 */2 * * *": "every 2 hours",
  "0 0 * * *": "daily at midnight",
  "0 0 */1 * *": "daily at midnight",
  "0 9 * * 1-5": "weekdays at 9am",
  "0 9 * * *": "daily at 9am",
};

export function validateCron(expression: string): string {
  const normalized = expression.trim().replace(/\s+/g, " ");
  if (!normalized) throw new Error("cron parameter is required");
  if (normalized.length > 256) throw new Error("cron expression is too long");
  if (normalized.split(" ").length !== 5) {
    throw new Error(`invalid cron expression: ${expression}`);
  }
  try {
    new CronPattern(normalized, undefined, { mode: "5-part" });
  } catch {
    throw new Error(`invalid cron expression: ${expression}`);
  }
  return normalized;
}

export function nextCronTime(expression: string, after: Date): Date {
  const cron = new Cron(validateCron(expression), { paused: true, mode: "5-part" });
  const next = cron.nextRun(after);
  cron.stop();
  if (!next || !Number.isFinite(next.getTime())) {
    throw new Error(`cron expression has no future occurrence: ${expression}`);
  }
  return next;
}

export function humanReadableCron(expression: string): string {
  return COMMON_SCHEDULES[expression] ?? expression;
}

const DURATION_PART = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/gy;
const UNIT_MS: Readonly<Record<string, number>> = {
  ns: 1e-6,
  us: 1e-3,
  "µs": 1e-3,
  ms: 1,
  s: 1_000,
  m: 60_000,
  h: 3_600_000,
};

/** Parse the positive subset of Go's time.ParseDuration syntax used by Swarm. */
export function parseDelay(value: string, maxMs = 365 * 24 * 60 * 60 * 1_000): number {
  const input = value.trim();
  if (!input) throw new Error("delay parameter is required");
  let total = 0;
  let offset = 0;
  DURATION_PART.lastIndex = 0;
  for (;;) {
    const match = DURATION_PART.exec(input);
    if (!match) break;
    if (match.index !== offset) throw new Error(`invalid delay format: ${value}`);
    total += Number(match[1]) * UNIT_MS[match[2]];
    offset = DURATION_PART.lastIndex;
  }
  if (offset !== input.length || !Number.isFinite(total) || total <= 0) {
    throw new Error(`invalid delay format: ${value}`);
  }
  const rounded = Math.ceil(total);
  if (rounded > maxMs) throw new Error(`delay exceeds maximum of ${maxMs}ms`);
  return rounded;
}
