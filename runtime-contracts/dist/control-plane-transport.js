import { createServer } from "node:net";
import { timingSafeEqual } from "node:crypto";
import { chmod, rm } from "node:fs/promises";
import { ControlPlaneError } from "./control-plane.js";
const retryable = new Set(["conflict"]);
const equalSecret = (a, b) => { const aa = Buffer.from(a), bb = Buffer.from(b); return aa.length === bb.length && timingSafeEqual(aa, bb); };
const bad = (code, message) => { throw new ControlPlaneError(code, message); };
/** Line-delimited JSON-RPC over a permissioned Unix socket. One request per line. */
export class LocalControlPlaneServer {
    plane;
    options;
    server;
    constructor(plane, options) {
        this.plane = plane;
        this.options = options;
        if (!options.token)
            throw new Error("local transport token is required");
    }
    async listen() { await rm(this.options.socketPath, { force: true }); this.server = createServer(socket => this.handle(socket)); await new Promise((resolve, reject) => { this.server.once("error", reject); this.server.listen(this.options.socketPath, () => resolve()); }); await chmod(this.options.socketPath, 0o600); }
    async close() { await new Promise(resolve => this.server?.close(() => resolve()) ?? resolve()); await rm(this.options.socketPath, { force: true }); }
    handle(socket) { let buffer = Buffer.alloc(0); const max = this.options.maxRequestBytes ?? 256 * 1024; socket.on("data", chunk => { buffer = Buffer.concat([buffer, chunk]); if (buffer.length > max) {
        socket.destroy();
        return;
    } let nl; while ((nl = buffer.indexOf(10)) >= 0) {
        const line = buffer.subarray(0, nl).toString();
        buffer = buffer.subarray(nl + 1);
        void this.dispatch(line).then(r => socket.write(JSON.stringify(r) + "\n"));
    } }); }
    async dispatch(line) { let request; try {
        const raw = JSON.parse(line);
        if (!raw || typeof raw !== "object" || Array.isArray(raw) || typeof raw.request_id !== "string" || !raw.request_id || typeof raw.method !== "string")
            throw new Error();
        request = raw;
    }
    catch {
        return { request_id: "", ok: false, error: { code: "invalid_request", message: "malformed JSON-RPC request", retryable: false } };
    } if (typeof request.auth !== "string" || !equalSecret(request.auth, this.options.token))
        return { request_id: request.request_id, ok: false, error: { code: "unauthorized", message: "authentication failed", retryable: false } }; try {
        return { request_id: request.request_id, ok: true, result: await this.call(request.method, request.params) };
    }
    catch (e) {
        if (e instanceof ControlPlaneError)
            return { request_id: request.request_id, ok: false, error: { code: e.code, message: e.message, retryable: retryable.has(e.code) } };
        return { request_id: request.request_id, ok: false, error: { code: "internal", message: "control-plane request failed", retryable: false } };
    } }
    async call(method, params) { const p = (params && typeof params === "object" ? params : {}); switch (method) {
        case "daemon.get_status": return { ready: true, agents: await this.plane.listAgents?.().then((x) => x.length), jobs: await this.plane.listJobs?.().then((x) => x.length) };
        case "agent.register": return this.plane.registerAgent(p, String(p.actor ?? "local"));
        case "agent.heartbeat": return this.plane.heartbeatAgent(String(p.id), String(p.actor ?? "local"), p.at);
        case "agent.stop": return this.plane.stopAgent(String(p.id), String(p.actor ?? "local"));
        case "job.create": return this.plane.createJob(p, String(p.actor ?? "local"));
        case "job.get": return this.plane.getJob(String(p.id));
        case "job.status": return this.plane.status ? this.plane.status(String(p.id)) : this.plane.getJob(String(p.id));
        case "job.dispatch": return this.plane.dispatch(String(p.id), String(p.actor ?? "daemon"));
        case "job.cancel": return this.plane.cancelJob(String(p.id), String(p.actor ?? "local"));
        case "job.retry": return this.plane.retryJob(String(p.id), String(p.actor ?? "local"));
        case "events.subscribe": return this.plane.readEvents(p.after_sequence, p.limit);
        default: bad("invalid_request", `unknown method ${method}`);
    } }
}
