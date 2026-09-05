import type { ControlPlane } from "./control-plane.js";
import type { ControlTaskInterface } from "./control-task.js";
import type { GoalLoopControlPlane } from "./goal-loop.js";
export declare class DaemonUnavailableError extends Error {
    readonly cause?: unknown | undefined;
    readonly code = "unavailable";
    constructor(message?: string, cause?: unknown | undefined);
}
export interface DaemonRpcOptions {
    socketPath: string;
    token: string;
    timeoutMs?: number;
}
export declare class DaemonRpcClient {
    private readonly options;
    constructor(options: DaemonRpcOptions);
    /** Safe lifecycle hook for Pi; calls use short-lived sockets. */
    close(): void;
    call<T>(method: string, params?: unknown): Promise<T>;
}
export declare function createDaemonControlPlane(client: DaemonRpcClient): ControlPlane;
export declare function createDaemonControlTask(client: DaemonRpcClient): ControlTaskInterface;
export declare function createDaemonGoalLoop(client: DaemonRpcClient): GoalLoopControlPlane;
