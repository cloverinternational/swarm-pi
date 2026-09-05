import { createId, ID, JsonObject, newId } from "./contracts.js";

export type AgentKind = "pi-session" | "pi-headless" | "external";
export type AgentLifecycle = "starting" | "idle" | "running" | "waiting" | "stopped" | "failed";
export type JobState = "queued" | "leased" | "running" | "succeeded" | "failed" | "cancelled";

export interface AgentBinding {
  readonly id: ID; readonly kind: AgentKind; readonly workspace: string;
  readonly conversationId?: string; readonly capabilityProfile?: ID;
  readonly status: AgentLifecycle; readonly lastHeartbeatAt?: string;
  readonly revision: number; readonly createdAt: string; readonly updatedAt: string;
}
export interface AgentRegistration { readonly id?: ID; readonly kind: AgentKind; readonly workspace: string; readonly conversationId?: string; readonly capabilityProfile?: ID; }
export interface JobTarget { readonly agentId?: ID; readonly profileId?: ID; readonly workflowId?: ID; }
export interface JobRequest { readonly prompt: string; readonly target: JobTarget; }
export interface Job {
  readonly id: ID; readonly request: JobRequest; readonly policyProfile?: ID;
  readonly state: JobState; readonly attempt: number; readonly idempotencyKey: string;
  readonly createdAt: string; readonly updatedAt: string;
}
export interface CreateJobRequest { readonly request: JobRequest; readonly policyProfile?: ID; readonly idempotencyKey: string; }

export type ControlPlaneEventType = "agent.registered" | "agent.heartbeat" | "agent.stopped" | "job.created" | "job.cancelled" | "job.retried" | "job.leased" | "job.completed" | "job.recovered";
export interface ControlPlaneEvent { readonly id: ID; readonly sequence: number; readonly type: ControlPlaneEventType; readonly entityId: ID; readonly occurredAt: string; readonly correlationId?: ID; readonly actor: string; readonly data?: JsonObject; readonly redacted: true; }
export interface EventSubscription { readonly events: readonly ControlPlaneEvent[]; readonly nextSequence: number; }

export type ControlPlaneErrorCode = "invalid_request" | "not_found" | "conflict" | "already_terminal";
export class ControlPlaneError extends Error {
  constructor(readonly code: ControlPlaneErrorCode, message: string) { super(message); this.name = "ControlPlaneError"; }
}

/** Transport-neutral control-plane boundary. Implementations own state and event ordering. */
export interface ControlPlaneInspection {
  listAgents(): Promise<readonly AgentBinding[]>;
  listJobs(): Promise<readonly Job[]>;
}

export interface ControlPlane {
  registerAgent(request: AgentRegistration, actor: string): Promise<AgentBinding>;
  heartbeatAgent(id: ID, actor: string, at?: string): Promise<AgentBinding>;
  stopAgent(id: ID, actor: string): Promise<AgentBinding>;
  createJob(request: CreateJobRequest, actor: string): Promise<Job>;
  getJob(id: ID): Promise<Job>;
  cancelJob(id: ID, actor: string): Promise<Job>;
  retryJob(id: ID, actor: string): Promise<Job>;
  readEvents(afterSequence?: number, limit?: number): Promise<EventSubscription>;
}

const copy = <T>(value: T): T => structuredClone(value);
const now = (): string => new Date().toISOString();

/** Deterministic, process-local fake for contract tests and adapter development. */
export class InProcessControlPlane implements ControlPlane, ControlPlaneInspection {
  private readonly agents = new Map<ID, AgentBinding>();
  private readonly jobs = new Map<ID, Job>();
  private readonly idempotency = new Map<string, ID>();
  private readonly events: ControlPlaneEvent[] = [];
  private sequence = 0;

  async registerAgent(request: AgentRegistration, actor: string): Promise<AgentBinding> {
    if (!request.workspace.trim()) throw new ControlPlaneError("invalid_request", "workspace is required");
    const timestamp = now();
    const agent: AgentBinding = { id: request.id ?? newId("agent"), kind: request.kind, workspace: request.workspace, conversationId: request.conversationId, capabilityProfile: request.capabilityProfile, status: "starting", revision: 1, createdAt: timestamp, updatedAt: timestamp };
    if (this.agents.has(agent.id)) throw new ControlPlaneError("conflict", `agent ${agent.id} already exists`);
    this.agents.set(agent.id, agent); this.emit("agent.registered", agent.id, actor); return copy(agent);
  }
  async heartbeatAgent(id: ID, actor: string, at = now()): Promise<AgentBinding> {
    const current = this.agent(id); const next: AgentBinding = { ...current, status: current.status === "starting" ? "idle" : current.status, lastHeartbeatAt: at, revision: current.revision + 1, updatedAt: now() };
    this.agents.set(id, next); this.emit("agent.heartbeat", id, actor); return copy(next);
  }
  async stopAgent(id: ID, actor: string): Promise<AgentBinding> { const current = this.agent(id); const next = { ...current, status: "stopped" as const, revision: current.revision + 1, updatedAt: now() }; this.agents.set(id, next); this.emit("agent.stopped", id, actor); return copy(next); }
  async createJob(request: CreateJobRequest, actor: string): Promise<Job> {
    if (!request.request.prompt.trim() || !request.idempotencyKey.trim()) throw new ControlPlaneError("invalid_request", "prompt and idempotencyKey are required");
    const existing = this.idempotency.get(request.idempotencyKey); if (existing) return this.getJob(existing);
    const timestamp = now(); const job: Job = { id: newId("job"), request: copy(request.request), policyProfile: request.policyProfile, state: "queued", attempt: 0, idempotencyKey: request.idempotencyKey, createdAt: timestamp, updatedAt: timestamp };
    this.jobs.set(job.id, job); this.idempotency.set(job.idempotencyKey, job.id); this.emit("job.created", job.id, actor); return copy(job);
  }
  async listAgents(): Promise<readonly AgentBinding[]> { return copy([...this.agents.values()]); }
  async listJobs(): Promise<readonly Job[]> { return copy([...this.jobs.values()]); }
  async getJob(id: ID): Promise<Job> { const job = this.jobs.get(id); if (!job) throw new ControlPlaneError("not_found", `job ${id} not found`); return copy(job); }
  async cancelJob(id: ID, actor: string): Promise<Job> { const job = await this.getJob(id); if (["succeeded", "failed", "cancelled"].includes(job.state)) throw new ControlPlaneError("already_terminal", `job ${id} is terminal`); const next = { ...job, state: "cancelled" as const, updatedAt: now() }; this.jobs.set(id, next); this.emit("job.cancelled", id, actor); return copy(next); }
  async retryJob(id: ID, actor: string): Promise<Job> { const job = await this.getJob(id); if (!["failed", "cancelled"].includes(job.state)) throw new ControlPlaneError("conflict", `job ${id} is not retryable`); const next = { ...job, state: "queued" as const, attempt: job.attempt + 1, updatedAt: now() }; this.jobs.set(id, next); this.emit("job.retried", id, actor); return copy(next); }
  async readEvents(afterSequence = 0, limit = 100): Promise<EventSubscription> { if (!Number.isSafeInteger(afterSequence) || afterSequence < 0 || !Number.isSafeInteger(limit) || limit < 1) throw new ControlPlaneError("invalid_request", "invalid event cursor or limit"); const events = this.events.filter(event => event.sequence > afterSequence).slice(0, limit); return { events: copy(events), nextSequence: events.at(-1)?.sequence ?? afterSequence }; }
  private agent(id: ID): AgentBinding { const agent = this.agents.get(id); if (!agent) throw new ControlPlaneError("not_found", `agent ${id} not found`); return agent; }
  private emit(type: ControlPlaneEventType, entityId: ID, actor: string): void { this.events.push({ id: newId("event"), sequence: ++this.sequence, type, entityId, occurredAt: now(), actor, redacted: true }); }
}
