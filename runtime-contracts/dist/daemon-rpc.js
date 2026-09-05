import { connect } from "node:net";
import { randomUUID } from "node:crypto";
export class DaemonUnavailableError extends Error {
    cause;
    code = "unavailable";
    constructor(message = "daemon is unavailable", cause) {
        super(message);
        this.cause = cause;
        this.name = "DaemonUnavailableError";
    }
}
export class DaemonRpcClient {
    options;
    constructor(options) {
        this.options = options;
        if (!options.socketPath)
            throw new Error("daemon socket is required");
        if (!options.token)
            throw new Error("daemon token is required");
    }
    /** Safe lifecycle hook for Pi; calls use short-lived sockets. */
    close() { }
    async call(method, params) {
        const request_id = randomUUID();
        const line = JSON.stringify({ request_id, method, params, auth: this.options.token }) + "\n";
        return new Promise((resolve, reject) => {
            let socket;
            let timer;
            let buf = "";
            const fail = (e) => { if (timer)
                clearTimeout(timer); socket?.destroy(); reject(new DaemonUnavailableError("daemon is unavailable", e)); };
            try {
                socket = connect(this.options.socketPath);
            }
            catch (e) {
                fail(e);
                return;
            }
            timer = setTimeout(() => fail(new Error("RPC timeout")), this.options.timeoutMs ?? 5000);
            socket.once("error", fail);
            socket.on("data", chunk => { buf += chunk.toString(); const i = buf.indexOf("\n"); if (i < 0)
                return; try {
                const r = JSON.parse(buf.slice(0, i));
                if (timer)
                    clearTimeout(timer);
                socket?.end();
                if (r.ok)
                    resolve(r.result);
                else {
                    const e = Object.assign(new Error(r.error?.message ?? "daemon request failed"), { code: r.error?.code ?? "internal", retryable: r.error?.retryable ?? false });
                    reject(e);
                }
            }
            catch (e) {
                fail(e);
            } });
            socket.on("connect", () => socket.write(line));
        });
    }
}
export function createDaemonControlPlane(client) {
    return { registerAgent: (r, a) => client.call("agent.register", { ...r, actor: a }), heartbeatAgent: (id, a, at) => client.call("agent.heartbeat", { id, actor: a, at }), stopAgent: (id, a) => client.call("agent.stop", { id, actor: a }), createJob: (r, a) => client.call("job.create", { ...r, actor: a }), getJob: id => client.call("job.get", { id }), cancelJob: (id, a) => client.call("job.cancel", { id, actor: a }), retryJob: (id, a) => client.call("job.retry", { id, actor: a }), readEvents: (after, limit) => client.call("events.subscribe", { after_sequence: after, limit }) };
}
export function createDaemonControlTask(client) {
    const c = (method, p) => client.call(method, p);
    return { goalCreate: r => c("goal.create", r), goalGet: id => c("goal.get", { id }), taskCreate: r => c("task.create", r), taskGet: id => c("task.get", { id }), taskStatus: id => c("task.status", { id }), taskCancel: id => c("task.cancel", { id }), runCreate: r => c("run.create", r), runGet: id => c("run.get", { id }), runStatus: id => c("run.status", { id }), runCancel: id => c("run.cancel", { id }) };
}
export function createDaemonGoalLoop(client) {
    const c = (m, p) => client.call(m, p);
    return { goalCreate: i => c("goal.create", i), goalStatus: id => c("goal.status", { id }), goalPause: id => c("goal.pause", { id }), goalResume: id => c("goal.resume", { id }), goalComplete: id => c("goal.complete", { id }), loopCreate: i => c("loop.create", i), loopStatus: id => c("loop.status", { id }), loopPause: id => c("loop.pause", { id }), loopResume: id => c("loop.resume", { id }), loopStop: id => c("loop.stop", { id }) };
}
