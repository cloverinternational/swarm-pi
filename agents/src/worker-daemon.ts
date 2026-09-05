import { Absurd, type AbsurdOptions, type TaskContext } from "absurd-sdk";
import { GENERAL_AGENT_TASK, type GeneralAgentInput, type GeneralAgentOutput } from "../../runtime-contracts/dist/general-agent.js";
import { registerGeneralAgentTask, type GeneralAgentRuntimeFactory } from "./general-agent-adapter.js";

export type WorkerDaemonState = "stopped" | "starting" | "healthy" | "stopping" | "failed";
export interface WorkerDaemonOptions { db: NonNullable<AbsurdOptions["db"]>; queueName?: string; workerId?: string; claimTimeout?: number; pollInterval?: number; createRuntime: GeneralAgentRuntimeFactory; absurdFactory?: (options: { db: NonNullable<AbsurdOptions["db"]>; queueName: string }) => Absurd; }
export interface WorkerDaemon { readonly state: WorkerDaemonState; readonly workerId: string; start(): Promise<void>; stop(): Promise<void>; health(): { state: WorkerDaemonState; workerId: string; queueName: string }; }

/** Minimal one-agent Absurd worker. The runtime factory remains injectable so no live Postgres is needed in unit tests. */
export function createWorkerDaemon(options: WorkerDaemonOptions): WorkerDaemon {
  const queueName = options.queueName ?? process.env.ABSURD_QUEUE ?? "pi-swarm";
  const workerId = options.workerId ?? `pi-swarm-worker:${process.pid}`;
  let state: WorkerDaemonState = "stopped";
  let client: Absurd | undefined;
  let worker: { close(): Promise<void> } | undefined;
  return {
    get state() { return state; }, workerId,
    health: () => ({ state, workerId, queueName }),
    async start() {
      if (state === "healthy" || state === "starting") return;
      state = "starting";
      try {
        client = options.absurdFactory?.({ db: options.db, queueName }) ?? new Absurd({ db: options.db, queueName });
        registerGeneralAgentTask(client, options.createRuntime);
        worker = await client.startWorker({ concurrency: 1, workerId, claimTimeout: options.claimTimeout, pollInterval: options.pollInterval, onError: () => undefined });
        state = "healthy";
      } catch (error) { state = "failed"; await client?.close().catch(() => undefined); client = undefined; throw error; }
    },
    async stop() {
      if (state === "stopped") return;
      state = "stopping";
      await worker?.close().catch(() => undefined);
      await client?.close().catch(() => undefined);
      worker = undefined; client = undefined; state = "stopped";
    },
  };
}

export type GeneralAgentTaskRegistration = { input: GeneralAgentInput; output: GeneralAgentOutput; context: TaskContext };

export function installWorkerDaemonSignals(daemon: WorkerDaemon, signals: NodeJS.Signals[] = ["SIGINT", "SIGTERM"]): () => void {
  const handlers = signals.map(signal => { const handler = () => { void daemon.stop(); }; process.once(signal, handler); return [signal, handler] as const; });
  return () => { for (const [signal, handler] of handlers) process.off(signal, handler); };
}
