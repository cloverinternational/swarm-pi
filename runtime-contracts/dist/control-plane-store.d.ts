import { ControlPlaneEvent, ControlPlane, ControlPlaneInspection, AgentBinding, AgentRegistration, CreateJobRequest, Job } from "./control-plane.js";
import type { ID } from "./contracts.js";
export interface JobLease {
    readonly jobId: ID;
    readonly attempt: number;
    readonly leaseToken: string;
    readonly ownerId: string;
    readonly expiresAt: string;
}
export interface DurableControlPlaneOptions {
    readonly lockTimeoutMs?: number;
    readonly leaseMs?: number;
    readonly ownerId?: string;
}
/** Durable, cross-process serialized implementation. The lock directory is the atomic compare-and-swap primitive. */
export declare class FileControlPlane implements ControlPlane, ControlPlaneInspection {
    readonly path: string;
    private readonly lockPath;
    private readonly ownerId;
    private readonly leaseMs;
    private readonly lockTimeoutMs;
    constructor(path: string, options?: DurableControlPlaneOptions);
    private locked;
    private event;
    registerAgent(request: AgentRegistration, actor: string): Promise<AgentBinding>;
    heartbeatAgent(id: ID, actor: string, at?: string): Promise<AgentBinding>;
    stopAgent(id: ID, actor: string): Promise<AgentBinding>;
    createJob(request: CreateJobRequest, actor: string): Promise<Job>;
    listAgents(): Promise<readonly AgentBinding[]>;
    listJobs(): Promise<readonly Job[]>;
    getJob(id: ID): Promise<Job>;
    cancelJob(id: ID, actor: string): Promise<Job>;
    retryJob(id: ID, actor: string): Promise<Job>;
    private transition;
    readEvents(afterSequence?: number, limit?: number): Promise<{
        events: ControlPlaneEvent[];
        nextSequence: number;
    }>;
    claimJob(ownerId?: string, leaseMs?: number): Promise<JobLease | undefined>;
    renewLease(lease: JobLease, leaseMs?: number): Promise<JobLease>;
    completeLease(lease: JobLease, state: "succeeded" | "failed"): Promise<Job>;
    recoverExpiredLeases(): Promise<number>;
}
