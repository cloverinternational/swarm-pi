import { join } from "node:path";
import { humanReadableCron, nextCronTime, parseDelay, validateCron } from "./cron.js";
import { ScheduleStore } from "./store.js";
import type { CreateScheduleInput, PromptSink, ScheduleClock, ScheduledTask, WakeupInput } from "./types.js";

const MAX_TIMER_MS = 2_147_000_000;
const MAX_PROMPT_LENGTH = 100_000;
const DEFAULT_CLOCK: ScheduleClock = {
  now: () => new Date(),
  setTimeout: (callback, delayMs) => setTimeout(callback, delayMs),
  clearTimeout: (timer) => clearTimeout(timer),
};

export interface SchedulerOptions {
  workDir?: string;
  store?: ScheduleStore;
  sink: PromptSink;
  clock?: ScheduleClock;
  onError?: (error: unknown) => void;
  idFactory?: (kind: "task" | "wakeup") => string;
}

function copy(task: ScheduledTask): ScheduledTask {
  return {
    ...task,
    createdAt: new Date(task.createdAt),
    nextFireAt: new Date(task.nextFireAt),
    lastFiredAt: task.lastFiredAt ? new Date(task.lastFiredAt) : undefined,
  };
}

function prompt(value: string): string {
  if (typeof value !== "string" || !value.trim()) throw new Error("prompt parameter is required");
  if (value.length > MAX_PROMPT_LENGTH) throw new Error(`prompt exceeds ${MAX_PROMPT_LENGTH} characters`);
  return value;
}

function agentId(value: string | undefined): string | undefined {
  if (value === undefined || value === "") return undefined;
  if (typeof value !== "string" || value.length > 256 || /[\0\r\n]/.test(value)) {
    throw new Error("agent_id must be a single string of at most 256 characters");
  }
  return value;
}

export class Scheduler {
  private readonly tasks = new Map<string, ScheduledTask>();
  private readonly store: ScheduleStore;
  private readonly clock: ScheduleClock;
  private readonly sink: PromptSink;
  private readonly onError: (error: unknown) => void;
  private readonly idFactory: (kind: "task" | "wakeup") => string;
  private timer?: ReturnType<typeof setTimeout>;
  private started = false;
  private mutation: Promise<void> = Promise.resolve();
  private firing: Promise<void> = Promise.resolve();

  constructor(options: SchedulerOptions) {
    if (typeof options.sink !== "function") throw new Error("prompt sink is required");
    this.store = options.store ?? new ScheduleStore(join(options.workDir ?? process.cwd(), ".swarm", "scheduled_tasks.json"));
    this.clock = options.clock ?? DEFAULT_CLOCK;
    this.sink = options.sink;
    this.onError = options.onError ?? ((error) => console.error("[schedule]", error));
    // cron_create.go generateTaskID / schedule_wakeup.go: "<kind>-<time.Now().UnixNano()>".
    this.idFactory = options.idFactory ?? ((kind) => `${kind}-${BigInt(Date.now()) * 1_000_000n + process.hrtime.bigint() % 1_000_000n}`);
  }

  async start(): Promise<void> {
    await this.exclusive(async () => {
      if (this.started) return;
      const loaded = await this.store.load();
      for (const task of loaded) this.tasks.set(task.id, task);
      this.started = true;
      this.arm();
    });
  }

  async stop(): Promise<void> {
    await this.exclusive(async () => {
      this.started = false;
      if (this.timer) this.clock.clearTimeout(this.timer);
      this.timer = undefined;
      this.tasks.clear();
    });
  }

  async create(input: CreateScheduleInput): Promise<ScheduledTask> {
    return this.exclusive(async () => {
      this.assertStarted();
      const now = this.clock.now();
      const cron = validateCron(input.cron);
      const task: ScheduledTask = {
        id: this.uniqueId("task"),
        prompt: prompt(input.prompt),
        cron,
        recurring: input.recurring ?? false,
        durable: input.durable ?? false,
        createdAt: now,
        nextFireAt: nextCronTime(cron, now),
        agentId: agentId(input.agentId),
      };
      this.tasks.set(task.id, task);
      try {
        await this.persist();
      } catch (error) {
        this.tasks.delete(task.id);
        throw error;
      }
      this.arm();
      return copy(task);
    });
  }

  async scheduleWakeup(input: WakeupInput): Promise<ScheduledTask & { delay: string }> {
    return this.exclusive(async () => {
      this.assertStarted();
      const now = this.clock.now();
      const delayMs = parseDelay(input.delay);
      const fireAt = new Date(now.getTime() + delayMs);
      const task: ScheduledTask = {
        id: this.uniqueId("wakeup"),
        prompt: prompt(input.prompt),
        cron: `${fireAt.getMinutes()} ${fireAt.getHours()} ${fireAt.getDate()} ${fireAt.getMonth() + 1} *`,
        recurring: false,
        durable: false,
        createdAt: now,
        nextFireAt: fireAt,
      };
      this.tasks.set(task.id, task);
      this.arm();
      return { ...copy(task), delay: input.delay };
    });
  }

  async remove(id: string): Promise<ScheduledTask> {
    return this.exclusive(async () => {
      this.assertStarted();
      if (typeof id !== "string" || !id.trim()) throw new Error("id parameter is required");
      const task = this.tasks.get(id);
      if (!task) throw new Error(`task '${id}' not found`);
      this.tasks.delete(id);
      try {
        await this.persist();
      } catch (error) {
        this.tasks.set(id, task);
        throw error;
      }
      this.arm();
      return copy(task);
    });
  }

  list(): ScheduledTask[] {
    return [...this.tasks.values()]
      .sort((a, b) => a.nextFireAt.getTime() - b.nextFireAt.getTime() || a.id.localeCompare(b.id))
      .map(copy);
  }

  /** Wait until pending persistence and prompt delivery have settled. */
  async idle(): Promise<void> {
    await this.mutation;
    await this.firing;
    await this.mutation;
  }

  describe(task: ScheduledTask): Record<string, unknown> {
    return {
      id: task.id,
      cron: task.cron,
      human_schedule: humanReadableCron(task.cron),
      type: task.recurring ? "recurring" : "one-shot",
      persistence: task.durable ? "durable" : "session",
      next_fire_at: task.nextFireAt.toISOString(),
      ...(task.agentId ? { agent_id: task.agentId } : {}),
    };
  }

  private assertStarted(): void {
    if (!this.started) throw new Error("scheduler is not started");
  }

  private uniqueId(kind: "task" | "wakeup"): string {
    for (let attempt = 0; attempt < 10; attempt++) {
      const id = this.idFactory(kind);
      if (/^(?:task|wakeup)-[a-zA-Z0-9-]{8,}$/.test(id) && !this.tasks.has(id)) return id;
    }
    throw new Error("failed to generate a unique schedule id");
  }

  private async persist(): Promise<void> {
    await this.store.save([...this.tasks.values()]);
  }

  private arm(): void {
    if (this.timer) this.clock.clearTimeout(this.timer);
    this.timer = undefined;
    if (!this.started || !this.tasks.size) return;
    const next = Math.min(...[...this.tasks.values()].map((task) => task.nextFireAt.getTime()));
    const delay = Math.max(0, Math.min(next - this.clock.now().getTime(), MAX_TIMER_MS));
    this.timer = this.clock.setTimeout(() => {
      this.timer = undefined;
      const firing = this.fireDue();
      this.firing = firing.then(() => undefined, (error) => this.onError(error));
    }, delay);
    (this.timer as { unref?: () => void }).unref?.();
  }

  private async fireDue(): Promise<void> {
    const due = await this.exclusive(async () => {
      if (!this.started) return [];
      const now = this.clock.now();
      const claimed: ScheduledTask[] = [];
      for (const task of this.tasks.values()) {
        if (task.nextFireAt.getTime() > now.getTime()) continue;
        claimed.push(copy(task));
        if (task.recurring) {
          task.lastFiredAt = now;
          task.nextFireAt = nextCronTime(task.cron, now);
        } else {
          this.tasks.delete(task.id);
        }
      }
      if (claimed.length) await this.persist();
      this.arm();
      return claimed;
    });
    await Promise.allSettled(due.map(async (task) => {
      try {
        await this.sink(task.prompt, task);
      } catch (error) {
        this.onError(error);
      }
    }));
  }

  private exclusive<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.mutation.then(operation, operation);
    this.mutation = result.then(() => undefined, () => undefined);
    return result;
  }
}
