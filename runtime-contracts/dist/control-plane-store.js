import { mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { ControlPlaneError } from "./control-plane.js";
const CURRENT_SCHEMA = 1;
const clone = (v) => structuredClone(v);
const iso = () => new Date().toISOString();
const fail = (code, message) => { throw new ControlPlaneError(code, message); };
const required = (value, message) => value ?? fail("not_found", message);
function migrate(raw) {
    if (!raw || typeof raw !== "object" || Array.isArray(raw))
        fail("invalid_request", "control-plane store root must be an object");
    const value = raw;
    if (value.schemaVersion !== CURRENT_SCHEMA)
        fail("invalid_request", `unsupported control-plane schema ${String(value.schemaVersion)}`);
    if (!Array.isArray(value.agents) || !Array.isArray(value.jobs) || !Array.isArray(value.leases) || !Array.isArray(value.events) || !Number.isSafeInteger(value.nextSequence))
        fail("invalid_request", "invalid control-plane store schema");
    return clone(value);
}
const empty = () => ({ schemaVersion: CURRENT_SCHEMA, agents: [], jobs: [], leases: [], events: [], nextSequence: 0 });
/** Durable, cross-process serialized implementation. The lock directory is the atomic compare-and-swap primitive. */
export class FileControlPlane {
    path;
    lockPath;
    ownerId;
    leaseMs;
    lockTimeoutMs;
    constructor(path, options = {}) {
        this.path = path;
        this.lockPath = `${path}.lock`;
        this.ownerId = options.ownerId ?? `daemon:${process.pid}`;
        this.leaseMs = options.leaseMs ?? 30_000;
        this.lockTimeoutMs = options.lockTimeoutMs ?? 10_000;
    }
    async locked(fn) {
        const deadline = Date.now() + this.lockTimeoutMs;
        await mkdir(dirname(this.path), { recursive: true, mode: 0o700 });
        while (true) {
            try {
                await mkdir(this.lockPath, { mode: 0o700 });
                break;
            }
            catch (e) {
                if (e.code !== "EEXIST" || Date.now() >= deadline)
                    throw new ControlPlaneError("conflict", "control-plane store is busy");
                await new Promise(r => setTimeout(r, 5));
            }
        }
        try {
            let state = empty();
            try {
                state = migrate(JSON.parse(await readFile(this.path, "utf8")));
            }
            catch (e) {
                if (e.code !== "ENOENT")
                    throw e;
            }
            const result = await fn(state);
            const tmp = `${this.path}.tmp-${process.pid}-${crypto.randomUUID()}`;
            await writeFile(tmp, `${JSON.stringify(state)}\n`, { encoding: "utf8", mode: 0o600, flag: "wx" });
            await rename(tmp, this.path);
            await rm(tmp, { force: true });
            return result;
        }
        finally {
            await rm(this.lockPath, { recursive: true, force: true });
        }
    }
    event(state, type, entityId, actor) { state.events.push({ id: `event:${crypto.randomUUID()}`, sequence: ++state.nextSequence, type, entityId, occurredAt: iso(), actor, redacted: true }); }
    async registerAgent(request, actor) { return this.locked(s => { if (!request.workspace.trim())
        fail("invalid_request", "workspace is required"); const id = request.id ?? `agent:${crypto.randomUUID()}`; if (s.agents.some(a => a.id === id))
        fail("conflict", `agent ${id} already exists`); const t = iso(); const a = { id, kind: request.kind, workspace: request.workspace, conversationId: request.conversationId, capabilityProfile: request.capabilityProfile, status: "starting", revision: 1, createdAt: t, updatedAt: t }; s.agents.push(a); this.event(s, "agent.registered", id, actor); return clone(a); }); }
    async heartbeatAgent(id, actor, at = iso()) { return this.locked(s => { const a = required(s.agents.find(x => x.id === id), `agent ${id} not found`); const n = { ...a, status: a.status === "starting" ? "idle" : a.status, lastHeartbeatAt: at, revision: a.revision + 1, updatedAt: iso() }; s.agents[s.agents.indexOf(a)] = n; this.event(s, "agent.heartbeat", id, actor); return clone(n); }); }
    async stopAgent(id, actor) { return this.locked(s => { const a = required(s.agents.find(x => x.id === id), `agent ${id} not found`); const n = { ...a, status: "stopped", revision: a.revision + 1, updatedAt: iso() }; s.agents[s.agents.indexOf(a)] = n; this.event(s, "agent.stopped", id, actor); return clone(n); }); }
    async createJob(request, actor) { return this.locked(s => { if (!request.request.prompt.trim() || !request.idempotencyKey.trim())
        fail("invalid_request", "prompt and idempotencyKey are required"); const old = s.jobs.find(j => j.idempotencyKey === request.idempotencyKey); if (old)
        return clone(old); const t = iso(); const j = { id: `job:${crypto.randomUUID()}`, request: clone(request.request), policyProfile: request.policyProfile, state: "queued", attempt: 0, idempotencyKey: request.idempotencyKey, createdAt: t, updatedAt: t }; s.jobs.push(j); this.event(s, "job.created", j.id, actor); return clone(j); }); }
    async listAgents() { return this.locked(s => clone(s.agents)); }
    async listJobs() { return this.locked(s => clone(s.jobs)); }
    async getJob(id) { return this.locked(s => { const j = required(s.jobs.find(x => x.id === id), `job ${id} not found`); return clone(j); }); }
    async cancelJob(id, actor) { return this.locked(s => this.transition(s, id, "cancelled", actor, "job.cancelled")); }
    async retryJob(id, actor) { return this.locked(s => { const j = required(s.jobs.find(x => x.id === id), `job ${id} not found`); if (j.state !== "failed" && j.state !== "cancelled")
        fail("conflict", `job ${id} is not retryable`); const n = { ...j, state: "queued", attempt: j.attempt + 1, updatedAt: iso() }; s.jobs[s.jobs.indexOf(j)] = n; this.event(s, "job.retried", id, actor); return clone(n); }); }
    transition(s, id, state, actor, type) { const j = required(s.jobs.find(x => x.id === id), `job ${id} not found`); if (["succeeded", "failed", "cancelled"].includes(j.state))
        fail("already_terminal", `job ${id} is terminal`); const n = { ...j, state, updatedAt: iso() }; s.jobs[s.jobs.indexOf(j)] = n; this.event(s, type, id, actor); return clone(n); }
    async readEvents(afterSequence = 0, limit = 100) { return this.locked(s => { if (!Number.isSafeInteger(afterSequence) || afterSequence < 0 || !Number.isSafeInteger(limit) || limit < 1)
        fail("invalid_request", "invalid event cursor or limit"); const e = s.events.filter(x => x.sequence > afterSequence).slice(0, limit); return { events: clone(e), nextSequence: e.at(-1)?.sequence ?? afterSequence }; }); }
    async claimJob(ownerId = this.ownerId, leaseMs = this.leaseMs) { return this.locked(s => { const now = Date.now(); s.leases = s.leases.filter(l => Date.parse(l.expiresAt) > now); const j = s.jobs.find(x => x.state === "queued" && !s.leases.some(l => l.jobId === x.id) && (!x.request.target.agentId || x.request.target.agentId === ownerId)); if (!j)
        return undefined; const lease = { jobId: j.id, attempt: j.attempt + 1, leaseToken: crypto.randomUUID(), ownerId, expiresAt: new Date(now + leaseMs).toISOString() }; s.leases.push(lease); s.jobs[s.jobs.indexOf(j)] = { ...j, state: "leased", attempt: lease.attempt, updatedAt: iso() }; this.event(s, "job.leased", j.id, ownerId); return clone(lease); }); }
    async renewLease(lease, leaseMs = this.leaseMs) { return this.locked(s => { const l = required(s.leases.find(x => x.jobId === lease.jobId && x.leaseToken === lease.leaseToken && x.ownerId === lease.ownerId && lease.ownerId === this.ownerId), "lease lost"); if (Date.parse(l.expiresAt) <= Date.now())
        fail("conflict", "lease lost"); const n = { ...l, expiresAt: new Date(Date.now() + leaseMs).toISOString() }; s.leases[s.leases.indexOf(l)] = n; return clone(n); }); }
    async completeLease(lease, state) { return this.locked(s => { const l = s.leases.find(x => x.jobId === lease.jobId && x.leaseToken === lease.leaseToken && x.ownerId === lease.ownerId && lease.ownerId === this.ownerId); if (!l || Date.parse(l.expiresAt) <= Date.now())
        fail("conflict", "lease lost"); const j = required(s.jobs.find(x => x.id === lease.jobId), `job ${lease.jobId} not found`); if (["succeeded", "failed", "cancelled"].includes(j.state))
        fail("already_terminal", "job is terminal"); const n = { ...j, state, updatedAt: iso() }; s.jobs[s.jobs.indexOf(j)] = n; s.leases = s.leases.filter(x => x !== l); this.event(s, "job.completed", j.id, lease.ownerId); return clone(n); }); }
    async recoverExpiredLeases() { return this.locked(s => { const now = Date.now(); let count = 0; for (const l of s.leases.filter(x => Date.parse(x.expiresAt) <= now)) {
        const j = s.jobs.find(x => x.id === l.jobId);
        if (j && j.state === "leased") {
            s.jobs[s.jobs.indexOf(j)] = { ...j, state: "queued", updatedAt: iso() };
            this.event(s, "job.recovered", j.id, "daemon");
            count++;
        }
        s.leases = s.leases.filter(x => x !== l);
    } return count; }); }
}
