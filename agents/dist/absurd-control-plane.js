import { newId } from "../../runtime-contracts/dist/contracts.js";
import { ControlPlaneError } from "../../runtime-contracts/dist/control-plane.js";
import { GENERAL_AGENT_TASK } from "../../runtime-contracts/dist/general-agent.js";
/** Absurd/Postgres production implementation. It intentionally has no local fallback. */
export class AbsurdControlPlane {
    absurd;
    ownerFor;
    agents = new Map();
    jobs = new Map();
    keys = new Map();
    seq = 0;
    events = [];
    constructor(absurd, ownerFor = () => undefined) {
        this.absurd = absurd;
        this.ownerFor = ownerFor;
    }
    async registerAgent(r, actor) { if (!r.workspace.trim())
        throw new ControlPlaneError("invalid_request", "workspace is required"); const id = r.id ?? newId("agent"), t = new Date().toISOString(); const a = { id, kind: r.kind, workspace: r.workspace, conversationId: r.conversationId, capabilityProfile: r.capabilityProfile, status: "idle", revision: 1, createdAt: t, updatedAt: t }; if (this.agents.has(id))
        throw new ControlPlaneError("conflict", "agent already exists"); this.agents.set(id, a); this.emit("agent.registered", id, actor); return a; }
    async heartbeatAgent(id, actor, at) { const a = this.agent(id); const n = { ...a, lastHeartbeatAt: at ?? new Date().toISOString(), status: "idle", revision: a.revision + 1, updatedAt: new Date().toISOString() }; this.agents.set(id, n); return n; }
    async stopAgent(id, actor) { const a = this.agent(id); const n = { ...a, status: "stopped", revision: a.revision + 1, updatedAt: new Date().toISOString() }; this.agents.set(id, n); return n; }
    async createJob(r, actor) { if (!r.request.prompt.trim() || !r.idempotencyKey.trim())
        throw new ControlPlaneError("invalid_request", "prompt and idempotencyKey are required"); const old = this.keys.get(r.idempotencyKey); if (old)
        return this.getJob(old); const t = new Date().toISOString(), j = { id: newId("job"), request: r.request, policyProfile: r.policyProfile, state: "queued", attempt: 0, idempotencyKey: r.idempotencyKey, createdAt: t, updatedAt: t }; this.jobs.set(j.id, j); this.keys.set(j.idempotencyKey, j.id); this.emit("job.created", j.id, actor); return j; }
    async dispatch(id) { const j = await this.getJob(id); if (j.state !== "queued")
        return j; const a = j.request.target.agentId ? this.agents.get(j.request.target.agentId) : undefined; const s = await this.absurd.spawn(GENERAL_AGENT_TASK, { prompt: j.request.prompt, workspace: a?.workspace ?? "", sessionId: a?.conversationId ?? "", conversationId: a?.conversationId ?? "", provider: "configured", model: "configured" }, { idempotencyKey: j.idempotencyKey, headers: { jobId: j.id, ...(this.ownerFor(j) ? { ownerSessionId: this.ownerFor(j) } : {}) } }); const n = { ...j, state: "running", attempt: s.attempt, updatedAt: new Date().toISOString(), request: { ...j.request, target: { ...j.request.target, workflowId: s.taskID } } }; this.jobs.set(id, n); return n; }
    async getJob(id) { const j = this.jobs.get(id); if (!j)
        throw new ControlPlaneError("not_found", "job not found"); return j; }
    async cancelJob(id, actor) { const j = await this.getJob(id); if (["succeeded", "failed", "cancelled"].includes(j.state))
        throw new ControlPlaneError("already_terminal", "job is terminal"); if (j.request.target.workflowId)
        await this.absurd.cancelTask(j.request.target.workflowId); const n = { ...j, state: "cancelled", updatedAt: new Date().toISOString() }; this.jobs.set(id, n); this.emit("job.cancelled", id, actor); return n; }
    async retryJob(id, actor) { const j = await this.getJob(id); if (j.state !== "failed" && j.state !== "cancelled")
        throw new ControlPlaneError("conflict", "job is not retryable"); return this.createJob({ ...j, idempotencyKey: j.idempotencyKey + "-retry-" + (j.attempt + 1) }, actor); }
    async readEvents(after = 0, limit = 100) { const e = this.events.filter(x => x.sequence > after).slice(0, limit); return { events: e, nextSequence: e.at(-1)?.sequence ?? after }; }
    async listAgents() { return [...this.agents.values()]; }
    async listJobs() { return [...this.jobs.values()]; }
    agent(id) { const a = this.agents.get(id); if (!a)
        throw new ControlPlaneError("not_found", "agent not found"); return a; }
    emit(type, entityId, actor) { this.events.push({ id: newId("event"), sequence: ++this.seq, type, entityId, occurredAt: new Date().toISOString(), actor, redacted: true }); }
}
