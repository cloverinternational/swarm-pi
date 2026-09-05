import { Absurd } from "absurd-sdk";
import { type ID } from "../../runtime-contracts/dist/contracts.js";
import { type ControlPlane, type AgentBinding, type AgentRegistration, type CreateJobRequest, type Job, type EventSubscription } from "../../runtime-contracts/dist/control-plane.js";
/** Absurd/Postgres production implementation. It intentionally has no local fallback. */
export declare class AbsurdControlPlane implements ControlPlane {
    private readonly absurd;
    private readonly ownerFor;
    private agents;
    private jobs;
    private keys;
    private seq;
    private events;
    constructor(absurd: Absurd, ownerFor?: (job: Job) => string | undefined);
    registerAgent(r: AgentRegistration, actor: string): Promise<AgentBinding>;
    heartbeatAgent(id: ID, actor: string, at?: string): Promise<{
        lastHeartbeatAt: string;
        status: "idle";
        revision: number;
        updatedAt: string;
        id: ID;
        kind: import("../../runtime-contracts/dist/control-plane.js").AgentKind;
        workspace: string;
        conversationId?: string;
        capabilityProfile?: ID;
        createdAt: string;
    }>;
    stopAgent(id: ID, actor: string): Promise<{
        status: "stopped";
        revision: number;
        updatedAt: string;
        id: ID;
        kind: import("../../runtime-contracts/dist/control-plane.js").AgentKind;
        workspace: string;
        conversationId?: string;
        capabilityProfile?: ID;
        lastHeartbeatAt?: string;
        createdAt: string;
    }>;
    createJob(r: CreateJobRequest, actor: string): Promise<Job>;
    dispatch(id: ID): Promise<Job>;
    getJob(id: ID): Promise<Job>;
    cancelJob(id: ID, actor: string): Promise<{
        state: "cancelled";
        updatedAt: string;
        id: ID;
        request: import("../../runtime-contracts/dist/control-plane.js").JobRequest;
        policyProfile?: ID;
        attempt: number;
        idempotencyKey: string;
        createdAt: string;
    }>;
    retryJob(id: ID, actor: string): Promise<Job>;
    readEvents(after?: number, limit?: number): Promise<EventSubscription>;
    listAgents(): Promise<AgentBinding[]>;
    listJobs(): Promise<Job[]>;
    private agent;
    private emit;
}
