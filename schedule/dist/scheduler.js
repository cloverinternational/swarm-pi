import { join } from "node:path";
import { humanReadableCron, nextCronTime, parseDelay, validateCron } from "./cron.js";
import { ScheduleStore } from "./store.js";
const MAX_TIMER_MS = 2_147_000_000;
const MAX_PROMPT_LENGTH = 100_000;
const DEFAULT_CLOCK = {
    now: () => new Date(),
    setTimeout: (callback, delayMs) => setTimeout(callback, delayMs),
    clearTimeout: (timer) => clearTimeout(timer),
};
function copy(task) {
    return {
        ...task,
        createdAt: new Date(task.createdAt),
        nextFireAt: new Date(task.nextFireAt),
        lastFiredAt: task.lastFiredAt ? new Date(task.lastFiredAt) : undefined,
    };
}
function prompt(value) {
    if (typeof value !== "string" || !value.trim())
        throw new Error("prompt parameter is required");
    if (value.length > MAX_PROMPT_LENGTH)
        throw new Error(`prompt exceeds ${MAX_PROMPT_LENGTH} characters`);
    return value;
}
function agentId(value) {
    if (value === undefined || value === "")
        return undefined;
    if (typeof value !== "string" || value.length > 256 || /[\0\r\n]/.test(value)) {
        throw new Error("agent_id must be a single string of at most 256 characters");
    }
    return value;
}
export class Scheduler {
    tasks = new Map();
    store;
    clock;
    sink;
    onError;
    idFactory;
    timer;
    started = false;
    mutation = Promise.resolve();
    firing = Promise.resolve();
    constructor(options) {
        if (typeof options.sink !== "function")
            throw new Error("prompt sink is required");
        this.store = options.store ?? new ScheduleStore(join(options.workDir ?? process.cwd(), ".swarm", "scheduled_tasks.json"));
        this.clock = options.clock ?? DEFAULT_CLOCK;
        this.sink = options.sink;
        this.onError = options.onError ?? ((error) => console.error("[schedule]", error));
        this.idFactory = options.idFactory ?? ((kind) => `${kind}-${crypto.randomUUID()}`);
    }
    async start() {
        await this.exclusive(async () => {
            if (this.started)
                return;
            const loaded = await this.store.load();
            for (const task of loaded)
                this.tasks.set(task.id, task);
            this.started = true;
            this.arm();
        });
    }
    async stop() {
        await this.exclusive(async () => {
            this.started = false;
            if (this.timer)
                this.clock.clearTimeout(this.timer);
            this.timer = undefined;
            this.tasks.clear();
        });
    }
    async create(input) {
        return this.exclusive(async () => {
            this.assertStarted();
            const now = this.clock.now();
            const cron = validateCron(input.cron);
            const task = {
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
            }
            catch (error) {
                this.tasks.delete(task.id);
                throw error;
            }
            this.arm();
            return copy(task);
        });
    }
    async scheduleWakeup(input) {
        return this.exclusive(async () => {
            this.assertStarted();
            const now = this.clock.now();
            const delayMs = parseDelay(input.delay);
            const fireAt = new Date(now.getTime() + delayMs);
            const task = {
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
    async remove(id) {
        return this.exclusive(async () => {
            this.assertStarted();
            if (typeof id !== "string" || !id.trim())
                throw new Error("id parameter is required");
            const task = this.tasks.get(id);
            if (!task)
                throw new Error(`scheduled task '${id}' not found`);
            this.tasks.delete(id);
            try {
                await this.persist();
            }
            catch (error) {
                this.tasks.set(id, task);
                throw error;
            }
            this.arm();
            return copy(task);
        });
    }
    list() {
        return [...this.tasks.values()]
            .sort((a, b) => a.nextFireAt.getTime() - b.nextFireAt.getTime() || a.id.localeCompare(b.id))
            .map(copy);
    }
    /** Wait until pending persistence and prompt delivery have settled. */
    async idle() {
        await this.mutation;
        await this.firing;
        await this.mutation;
    }
    describe(task) {
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
    assertStarted() {
        if (!this.started)
            throw new Error("scheduler is not started");
    }
    uniqueId(kind) {
        for (let attempt = 0; attempt < 10; attempt++) {
            const id = this.idFactory(kind);
            if (/^(?:task|wakeup)-[a-zA-Z0-9-]{8,}$/.test(id) && !this.tasks.has(id))
                return id;
        }
        throw new Error("failed to generate a unique schedule id");
    }
    async persist() {
        await this.store.save([...this.tasks.values()]);
    }
    arm() {
        if (this.timer)
            this.clock.clearTimeout(this.timer);
        this.timer = undefined;
        if (!this.started || !this.tasks.size)
            return;
        const next = Math.min(...[...this.tasks.values()].map((task) => task.nextFireAt.getTime()));
        const delay = Math.max(0, Math.min(next - this.clock.now().getTime(), MAX_TIMER_MS));
        this.timer = this.clock.setTimeout(() => {
            this.timer = undefined;
            const firing = this.fireDue();
            this.firing = firing.then(() => undefined, (error) => this.onError(error));
        }, delay);
        this.timer.unref?.();
    }
    async fireDue() {
        const due = await this.exclusive(async () => {
            if (!this.started)
                return [];
            const now = this.clock.now();
            const claimed = [];
            for (const task of this.tasks.values()) {
                if (task.nextFireAt.getTime() > now.getTime())
                    continue;
                claimed.push(copy(task));
                if (task.recurring) {
                    task.lastFiredAt = now;
                    task.nextFireAt = nextCronTime(task.cron, now);
                }
                else {
                    this.tasks.delete(task.id);
                }
            }
            if (claimed.length)
                await this.persist();
            this.arm();
            return claimed;
        });
        await Promise.allSettled(due.map(async (task) => {
            try {
                await this.sink(task.prompt, task);
            }
            catch (error) {
                this.onError(error);
            }
        }));
    }
    exclusive(operation) {
        const result = this.mutation.then(operation, operation);
        this.mutation = result.then(() => undefined, () => undefined);
        return result;
    }
}
