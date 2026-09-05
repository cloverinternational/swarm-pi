import { newId } from "./contracts.js";
export class ControlPlaneError extends Error {
    code;
    constructor(code, message) {
        super(message);
        this.code = code;
        this.name = "ControlPlaneError";
    }
}
const copy = (value) => structuredClone(value);
const now = () => new Date().toISOString();
/** Deterministic, process-local fake for contract tests and adapter development. */
export class InProcessControlPlane {
    agents = new Map();
    jobs = new Map();
    idempotency = new Map();
    events = [];
    sequence = 0;
    async registerAgent(request, actor) {
        if (!request.workspace.trim())
            throw new ControlPlaneError("invalid_request", "workspace is required");
        const timestamp = now();
        const agent = { id: request.id ?? newId("agent"), kind: request.kind, workspace: request.workspace, conversationId: request.conversationId, capabilityProfile: request.capabilityProfile, status: "starting", revision: 1, createdAt: timestamp, updatedAt: timestamp };
        if (this.agents.has(agent.id))
            throw new ControlPlaneError("conflict", `agent ${agent.id} already exists`);
        this.agents.set(agent.id, agent);
        this.emit("agent.registered", agent.id, actor);
        return copy(agent);
    }
    async heartbeatAgent(id, actor, at = now()) {
        const current = this.agent(id);
        const next = { ...current, status: current.status === "starting" ? "idle" : current.status, lastHeartbeatAt: at, revision: current.revision + 1, updatedAt: now() };
        this.agents.set(id, next);
        this.emit("agent.heartbeat", id, actor);
        return copy(next);
    }
    async stopAgent(id, actor) { const current = this.agent(id); const next = { ...current, status: "stopped", revision: current.revision + 1, updatedAt: now() }; this.agents.set(id, next); this.emit("agent.stopped", id, actor); return copy(next); }
    async createJob(request, actor) {
        if (!request.request.prompt.trim() || !request.idempotencyKey.trim())
            throw new ControlPlaneError("invalid_request", "prompt and idempotencyKey are required");
        const existing = this.idempotency.get(request.idempotencyKey);
        if (existing)
            return this.getJob(existing);
        const timestamp = now();
        const job = { id: newId("job"), request: copy(request.request), policyProfile: request.policyProfile, state: "queued", attempt: 0, idempotencyKey: request.idempotencyKey, createdAt: timestamp, updatedAt: timestamp };
        this.jobs.set(job.id, job);
        this.idempotency.set(job.idempotencyKey, job.id);
        this.emit("job.created", job.id, actor);
        return copy(job);
    }
    async listAgents() { return copy([...this.agents.values()]); }
    async listJobs() { return copy([...this.jobs.values()]); }
    async getJob(id) { const job = this.jobs.get(id); if (!job)
        throw new ControlPlaneError("not_found", `job ${id} not found`); return copy(job); }
    async cancelJob(id, actor) { const job = await this.getJob(id); if (["succeeded", "failed", "cancelled"].includes(job.state))
        throw new ControlPlaneError("already_terminal", `job ${id} is terminal`); const next = { ...job, state: "cancelled", updatedAt: now() }; this.jobs.set(id, next); this.emit("job.cancelled", id, actor); return copy(next); }
    async retryJob(id, actor) { const job = await this.getJob(id); if (!["failed", "cancelled"].includes(job.state))
        throw new ControlPlaneError("conflict", `job ${id} is not retryable`); const next = { ...job, state: "queued", attempt: job.attempt + 1, updatedAt: now() }; this.jobs.set(id, next); this.emit("job.retried", id, actor); return copy(next); }
    async readEvents(afterSequence = 0, limit = 100) { if (!Number.isSafeInteger(afterSequence) || afterSequence < 0 || !Number.isSafeInteger(limit) || limit < 1)
        throw new ControlPlaneError("invalid_request", "invalid event cursor or limit"); const events = this.events.filter(event => event.sequence > afterSequence).slice(0, limit); return { events: copy(events), nextSequence: events.at(-1)?.sequence ?? afterSequence }; }
    agent(id) { const agent = this.agents.get(id); if (!agent)
        throw new ControlPlaneError("not_found", `agent ${id} not found`); return agent; }
    emit(type, entityId, actor) { this.events.push({ id: newId("event"), sequence: ++this.sequence, type, entityId, occurredAt: now(), actor, redacted: true }); }
}
