import { Absurd } from "absurd-sdk";
import { registerGeneralAgentTask } from "./general-agent-adapter.js";
/** Minimal one-agent Absurd worker. The runtime factory remains injectable so no live Postgres is needed in unit tests. */
export function createWorkerDaemon(options) {
    const queueName = options.queueName ?? process.env.ABSURD_QUEUE ?? "pi-swarm";
    const workerId = options.workerId ?? `pi-swarm-worker:${process.pid}`;
    let state = "stopped";
    let client;
    let worker;
    return {
        get state() { return state; }, workerId,
        health: () => ({ state, workerId, queueName }),
        async start() {
            if (state === "healthy" || state === "starting")
                return;
            state = "starting";
            try {
                client = options.absurdFactory?.({ db: options.db, queueName }) ?? new Absurd({ db: options.db, queueName });
                registerGeneralAgentTask(client, options.createRuntime);
                worker = await client.startWorker({ concurrency: 1, workerId, claimTimeout: options.claimTimeout, pollInterval: options.pollInterval, onError: () => undefined });
                state = "healthy";
            }
            catch (error) {
                state = "failed";
                await client?.close().catch(() => undefined);
                client = undefined;
                throw error;
            }
        },
        async stop() {
            if (state === "stopped")
                return;
            state = "stopping";
            await worker?.close().catch(() => undefined);
            await client?.close().catch(() => undefined);
            worker = undefined;
            client = undefined;
            state = "stopped";
        },
    };
}
export function installWorkerDaemonSignals(daemon, signals = ["SIGINT", "SIGTERM"]) {
    const handlers = signals.map(signal => { const handler = () => { void daemon.stop(); }; process.once(signal, handler); return [signal, handler]; });
    return () => { for (const [signal, handler] of handlers)
        process.off(signal, handler); };
}
