import { ID, JsonObject } from "./contracts.js";
export type AgentKind = "pi-session" | "pi-headless" | "external";
export type AgentLifecycle = "starting" | "idle" | "running" | "waiting" | "stopped" | "failed";
export type JobState = "queued" | "leased" | "running" | "succeeded" | "failed" | "cancelled";
export interface AgentBinding {
    readonly id: ID;
    readonly kind: AgentKind;
    readonly workspace: string;
    readonly conversationId?: string;
    readonly capabilityProfile?: ID;
    readonly status: AgentLifecycle;
    readonly lastHeartbeatAt?: string;
    readonly revision: number;
    readonly createdAt: string;
    readonly updatedAt: string;
}
export interface AgentRegistration {
    readonly id?: ID;
    readonly kind: AgentKind;
    readonly workspace: string;
    readonly conversationId?: string;
    readonly capabilityProfile?: ID;
}
export interface JobTarget {
    readonly agentId?: ID;
    readonly profileId?: ID;
    readonly workflowId?: ID;
}
export interface JobRequest {
    readonly prompt: string;
    readonly target: JobTarget;
}
export interface Job {
    readonly id: ID;
    readonly request: JobRequest;
    readonly policyProfile?: ID;
    readonly state: JobState;
    readonly attempt: number;
    readonly idempotencyKey: string;
    readonly createdAt: string;
    readonly updatedAt: string;
}
export interface CreateJobRequest {
    readonly request: JobRequest;
    readonly policyProfile?: ID;
    readonly idempotencyKey: string;
}
export type ControlPlaneEventType = "agent.registered" | "agent.heartbeat" | "agent.stopped" | "job.created" | "job.cancelled" | "job.retried" | "job.leased" | "job.completed" | "job.recovered";
export interface ControlPlaneEvent {
    readonly id: ID;
    readonly sequence: number;
    readonly type: ControlPlaneEventType;
    readonly entityId: ID;
    readonly occurredAt: string;
    readonly correlationId?: ID;
    readonly actor: string;
    readonly data?: JsonObject;
    readonly redacted: true;
}
export interface EventSubscription {
    readonly events: readonly ControlPlaneEvent[];
    readonly nextSequence: number;
}
export type ControlPlaneErrorCode = "invalid_request" | "not_found" | "conflict" | "already_terminal";
export declare class ControlPlaneError extends Error {
    readonly code: ControlPlaneErrorCode;
    constructor(code: ControlPlaneErrorCode, message: string);
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
/** Deterministic, process-local fake for contract tests and adapter development. */
export declare class InProcessControlPlane implements ControlPlane, ControlPlaneInspection {
    private readonly agents;
    private readonly jobs;
    private readonly idempotency;
    private readonly events;
    private sequence;
    registerAgent(request: AgentRegistration, actor: string): Promise<AgentBinding>;
    heartbeatAgent(id: ID, actor: string, at?: string): Promise<AgentBinding>;
    stopAgent(id: ID, actor: string): Promise<AgentBinding>;
    createJob(request: CreateJobRequest, actor: string): Promise<Job>;
    listAgents(): Promise<readonly AgentBinding[]>;
    listJobs(): Promise<readonly Job[]>;
    getJob(id: ID): Promise<Job>;
    cancelJob(id: ID, actor: string): Promise<Job>;
    retryJob(id: ID, actor: string): Promise<Job>;
    readEvents(afterSequence?: number, limit?: number): Promise<EventSubscription>;
    private agent;
    private emit;
}
