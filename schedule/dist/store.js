import { chmod, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { validateCron } from "./cron.js";
function validDate(value, field, required) {
    if ((value === undefined || value === "") && !required)
        return undefined;
    if (typeof value !== "string")
        throw new Error(`${field} must be an ISO date`);
    const date = new Date(value);
    if (!Number.isFinite(date.getTime()))
        throw new Error(`${field} must be an ISO date`);
    return date;
}
function decode(value, index) {
    if (!value || typeof value !== "object" || Array.isArray(value)) {
        throw new Error(`task ${index} must be an object`);
    }
    const raw = value;
    if (typeof raw.id !== "string" || !/^(?:task|wakeup)-[a-zA-Z0-9-]{8,}$/.test(raw.id)) {
        throw new Error(`task ${index} has an invalid id`);
    }
    if (typeof raw.prompt !== "string" || !raw.prompt.trim())
        throw new Error(`task ${raw.id} has no prompt`);
    if (raw.prompt.length > 100_000)
        throw new Error(`task ${raw.id} prompt is too long`);
    if (raw.durable !== true)
        throw new Error(`persisted task ${raw.id} must be durable`);
    if (typeof raw.recurring !== "boolean")
        throw new Error(`task ${raw.id} recurring must be boolean`);
    const createdAt = validDate(raw.created_at, "created_at", true);
    const lastFiredAt = validDate(raw.last_fired_at, "last_fired_at", false);
    const nextFireAt = validDate(raw.next_fire_at, "next_fire_at", true);
    return {
        id: raw.id,
        prompt: raw.prompt,
        cron: validateCron(String(raw.cron ?? "")),
        recurring: raw.recurring,
        durable: true,
        createdAt,
        lastFiredAt,
        nextFireAt,
        agentId: typeof raw.agent_id === "string" && raw.agent_id ? raw.agent_id : undefined,
    };
}
function encode(task) {
    return {
        id: task.id,
        prompt: task.prompt,
        cron: task.cron,
        recurring: task.recurring,
        durable: task.durable,
        created_at: task.createdAt.toISOString(),
        ...(task.lastFiredAt ? { last_fired_at: task.lastFiredAt.toISOString() } : {}),
        ...(task.agentId ? { agent_id: task.agentId } : {}),
        next_fire_at: task.nextFireAt.toISOString(),
    };
}
export class ScheduleStore {
    path;
    constructor(path) {
        this.path = path;
    }
    async load() {
        let text;
        try {
            text = await readFile(this.path, "utf8");
        }
        catch (error) {
            if (error.code === "ENOENT")
                return [];
            throw new Error(`failed to read schedule store: ${String(error)}`, { cause: error });
        }
        let raw;
        try {
            raw = JSON.parse(text);
        }
        catch (error) {
            throw new Error(`schedule store is not valid JSON: ${this.path}`, { cause: error });
        }
        if (!Array.isArray(raw))
            throw new Error("schedule store root must be an array");
        const tasks = raw.map(decode);
        const ids = new Set();
        for (const task of tasks) {
            if (ids.has(task.id))
                throw new Error(`schedule store contains duplicate id ${task.id}`);
            ids.add(task.id);
        }
        return tasks;
    }
    async save(tasks) {
        const durable = tasks.filter((task) => task.durable).sort((a, b) => a.id.localeCompare(b.id));
        if (!durable.length) {
            await rm(this.path, { force: true });
            return;
        }
        await mkdir(dirname(this.path), { recursive: true, mode: 0o700 });
        const temporary = `${this.path}.tmp-${process.pid}-${crypto.randomUUID()}`;
        try {
            await writeFile(temporary, `${JSON.stringify(durable.map(encode), null, 2)}\n`, {
                encoding: "utf8",
                mode: 0o600,
                flag: "wx",
            });
            await rename(temporary, this.path);
            await chmod(this.path, 0o600);
        }
        finally {
            await rm(temporary, { force: true });
        }
    }
}
