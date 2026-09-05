import { Absurd, type AbsurdOptions, type TaskContext } from "absurd-sdk";
import { type GeneralAgentInput, type GeneralAgentOutput } from "../../runtime-contracts/dist/general-agent.js";
import { type GeneralAgentRuntimeFactory } from "./general-agent-adapter.js";
export type WorkerDaemonState = "stopped" | "starting" | "healthy" | "stopping" | "failed";
export interface WorkerDaemonOptions {
    db: NonNullable<AbsurdOptions["db"]>;
    queueName?: string;
    workerId?: string;
    claimTimeout?: number;
    pollInterval?: number;
    createRuntime: GeneralAgentRuntimeFactory;
    absurdFactory?: (options: {
        db: NonNullable<AbsurdOptions["db"]>;
        queueName: string;
    }) => Absurd;
}
export interface WorkerDaemon {
    readonly state: WorkerDaemonState;
    readonly workerId: string;
    start(): Promise<void>;
    stop(): Promise<void>;
    health(): {
        state: WorkerDaemonState;
        workerId: string;
        queueName: string;
    };
}
/** Minimal one-agent Absurd worker. The runtime factory remains injectable so no live Postgres is needed in unit tests. */
export declare function createWorkerDaemon(options: WorkerDaemonOptions): WorkerDaemon;
export type GeneralAgentTaskRegistration = {
    input: GeneralAgentInput;
    output: GeneralAgentOutput;
    context: TaskContext;
};
export declare function installWorkerDaemonSignals(daemon: WorkerDaemon, signals?: NodeJS.Signals[]): () => void;
