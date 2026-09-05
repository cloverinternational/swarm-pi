import { createHash } from "node:crypto";
import { mkdir } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
const execFileAsync = promisify(execFile);
/** Build a real Pi child-session runner. The child is deliberately prevented
 * from recursively spawning this control surface; the parent owns orchestration. */
export function createPiRunner(pi) {
    return async (ctx) => {
        if (typeof pi?.exec !== "function")
            return defaultRunner(ctx);
        const sessionDir = `${ctx.cwd}/.pi/agent-sessions`;
        await mkdir(sessionDir, { recursive: true });
        const sessionPath = `${sessionDir}/${ctx.spec.sessionId ?? ctx.spec.id}.jsonl`;
        const args = ["--mode", "text", "--print", "--session", sessionPath, "--exclude-tools", "Agent,AgentControl", "-p", ctx.task];
        if (ctx.provider)
            args.unshift("--provider", ctx.provider);
        if (ctx.model)
            args.unshift("--model", ctx.model);
        const result = await pi.exec("pi", args, { cwd: ctx.cwd, signal: ctx.signal });
        if (result?.killed || result?.code !== 0)
            throw new Error(`child pi failed (${result?.killed ? "killed" : `exit ${result?.code}`}): ${(result?.stderr ?? "").trim()}`);
        return String(result?.stdout ?? "").trim();
    };
}
const clone = (v) => structuredClone(v);
const unique = (xs) => [...new Set(xs.filter(Boolean))];
const stableId = (parentId, sessionId, task) => `agent-${createHash("sha256").update(`${parentId ?? "root"}\n${sessionId}\n${task}`).digest("hex").slice(0, 20)}`;
export class AgentManager {
    options;
    agents = new Map();
    active = 0;
    queue = [];
    profiles = new Map();
    presets = new Map();
    constructor(options = {}) {
        this.options = options;
        for (const p of options.profiles ?? [])
            this.profiles.set(p.name, clone(p));
        for (const [name, p] of Object.entries(options.presets ?? {}))
            this.presets.set(name, { ...clone(p), name: p.name || name });
    }
    addProfile(profile) { this.profiles.set(profile.name, clone(profile)); }
    addPreset(name, preset) { this.presets.set(name, { ...clone(preset), name: preset.name || name }); }
    profile(name) { const p = this.profiles.get(name); return p && clone(p); }
    resolve(spec) {
        const parent = spec.parentId ? this.agents.get(spec.parentId) : undefined;
        const parentSpec = parent?.spec;
        const profile = spec.profile ? this.profiles.get(spec.profile) : parentSpec?.profile ? this.profiles.get(parentSpec.profile) : undefined;
        const preset = spec.preset ? this.presets.get(spec.preset) : undefined;
        const parentPreset = parentSpec?.preset ? this.presets.get(parentSpec.preset) : undefined;
        return { profile: profile ?? preset, provider: spec.provider ?? preset?.provider ?? parentSpec?.provider ?? parentPreset?.provider, model: spec.model ?? preset?.model ?? parentSpec?.model ?? parentPreset?.model, capabilities: unique([...(parentPreset?.capabilities ?? []), ...(parentPreset?.capabilities ?? []), ...(preset?.capabilities ?? []), ...(profile?.capabilities ?? []), ...(spec.capabilities ?? [])]), worktree: spec.worktree ?? preset?.worktree ?? false };
    }
    spawn(spec) {
        if (!spec.task?.trim())
            throw new Error("agent task is required");
        const sessionId = spec.sessionId ?? (spec.parentId ? `session:${spec.parentId}` : `session:${stableId(spec.parentId, "root", spec.task)}`);
        const id = spec.id ?? stableId(spec.parentId, sessionId, spec.task);
        if (this.agents.has(id))
            throw new Error(`agent ${id} already exists`);
        const abort = new AbortController();
        const steering = [];
        const listeners = new Set();
        let resolve;
        const promise = new Promise(r => resolve = r);
        const entry = { spec: { ...spec, id }, abort, steering, listeners, promise, result: undefined };
        this.agents.set(id, entry);
        const run = () => { this.active++; void this.execute(spec, sessionId, entry).then(r => { entry.result = r; resolve(r); for (const l of listeners)
            l(clone(r)); }).finally(() => { this.active--; this.drain(); }); };
        if (this.active < Math.max(1, this.options.concurrency ?? 4))
            run();
        else
            this.queue.push(run);
        return { id, parentId: spec.parentId, sessionId, wait: (timeoutMs) => timeout(promise, timeoutMs), cancel: () => { if (entry.result)
                return false; abort.abort(); return true; }, steer: (instruction) => { if (entry.result || !instruction.trim())
                return false; steering.push(instruction); return true; } };
    }
    drain() { const limit = Math.max(1, this.options.concurrency ?? 4); while (this.active < limit && this.queue.length)
        this.queue.shift()(); }
    async execute(spec, sessionId, entry) {
        const started = Date.now(), startedAt = new Date(started).toISOString(), resolved = this.resolve(spec);
        let cwd = this.options.cwd ?? process.cwd();
        if (resolved.worktree)
            cwd = await this.createWorktree(cwd, spec.id);
        const instructions = [...(resolved.profile?.systemPrompt ? [resolved.profile.systemPrompt] : []), ...(resolved.capabilities ?? []).map(c => `Capability: ${c}`)];
        try {
            const output = await (this.options.runner ?? defaultRunner)({ signal: entry.abort.signal, spec: { ...spec, id: spec.id, sessionId }, profile: resolved.profile, provider: resolved.provider, model: resolved.model, task: spec.task, cwd, instructions, steering: entry.steering });
            const result = { id: spec.id, status: entry.abort.signal.aborted ? "cancelled" : "completed", output, startedAt, completedAt: new Date().toISOString(), durationMs: Date.now() - started };
            return result;
        }
        catch (e) {
            return { id: spec.id, status: entry.abort.signal.aborted ? "cancelled" : "failed", error: e instanceof Error ? e.message : String(e), startedAt, completedAt: new Date().toISOString(), durationMs: Date.now() - started };
        }
    }
    async createWorktree(cwd, id) { const path = `${cwd}/.pi-worktrees/${id}`; await mkdir(`${cwd}/.pi-worktrees`, { recursive: true }); await execFileAsync("git", ["worktree", "add", "--detach", path, "HEAD"], { cwd, timeout: 15000 }); return path; }
    get(id) { const r = this.agents.get(id)?.result; return r && clone(r); }
    control(id, action, value, timeoutMs) { const e = this.agents.get(id); if (!e)
        throw new Error(`agent ${id} not found`); if (action === "wait")
        return timeout(e.promise, timeoutMs); if (action === "cancel") {
        if (e.result)
            return false;
        e.abort.abort();
        return true;
    } if (e.result || !value?.trim())
        return false; e.steering.push(value); return true; }
    list() { return [...this.agents.values()].flatMap(x => x.result ? [clone(x.result)] : []); }
    onComplete(id, listener) { const e = this.agents.get(id); if (!e)
        throw new Error(`agent ${id} not found`); e.listeners.add(listener); if (e.result)
        listener(clone(e.result)); return () => e.listeners.delete(listener); }
}
async function defaultRunner(ctx) { if (ctx.signal.aborted)
    throw new DOMException("Aborted", "AbortError"); return `[${ctx.provider ?? "default"}/${ctx.model ?? "default"}] ${ctx.task}`; }
function timeout(promise, ms) { if (ms === undefined)
    return promise; return Promise.race([promise, new Promise((_, reject) => setTimeout(() => reject(new Error("agent wait timed out")), ms))]); }
export function registerAgents(pi, manager = new AgentManager()) {
    pi.registerTool({ name: "Agent", label: "Run agent", description: "Start a child agent in the inherited session context.", parameters: { type: "object", required: ["task"], properties: { task: { type: "string" }, profile: { type: "string" }, provider: { type: "string" }, model: { type: "string" }, background: { type: "boolean" }, worktree: { type: "boolean" } } }, async execute(_id, input, ctx) { const parentId = ctx?.agentId ?? ctx?.sessionId; const h = manager.spawn({ ...input, parentId, sessionId: ctx?.sessionId }); const result = input.background ? { handle: h.id, sessionId: h.sessionId } : await h.wait(); return { content: [{ type: "text", text: JSON.stringify(result) }], details: result }; } });
    pi.registerTool({ name: "AgentControl", label: "Control agent", description: "Wait, cancel, or steer a background agent.", parameters: { type: "object", required: ["id", "action"], properties: { id: { type: "string" }, action: { type: "string", enum: ["wait", "cancel", "steer"] }, instruction: { type: "string" }, timeoutMs: { type: "number" } } }, async execute(_id, input) { const value = input.action === "wait" ? await manager.control(input.id, "wait", undefined, input.timeoutMs) : manager.control(input.id, input.action, input.instruction); return { content: [{ type: "text", text: JSON.stringify(value) }], details: value }; } });
    return manager;
}
