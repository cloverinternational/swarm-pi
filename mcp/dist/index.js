import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
export class MCPError extends Error {
    kind;
    cause;
    constructor(kind, message, cause) {
        super(message);
        this.kind = kind;
        this.cause = cause;
        this.name = "MCPError";
    }
}
const ID = /^[a-zA-Z0-9_-]{1,128}$/;
export function validateManifest(m, closed = true) {
    const e = [];
    if (!m.id || !ID.test(m.id))
        e.push("id must match [a-zA-Z0-9_-]{1,128}");
    if (!["stdio", "http", "sse"].includes(m.type))
        e.push("type must be stdio, http, or sse");
    if (m.type === "stdio" && !m.command)
        e.push("stdio command is required");
    if (m.type !== "stdio" && !m.url)
        e.push("remote url is required");
    if (m.url)
        try {
            const u = new URL(m.url);
            if (!/^https?:$/.test(u.protocol))
                e.push("url must use http or https");
        }
        catch {
            e.push("url is invalid");
        }
    if (m.tools?.length && m.excludeTools?.length)
        e.push("tools and excludeTools are mutually exclusive");
    if (m.timeoutMs !== undefined && (!Number.isFinite(m.timeoutMs) || m.timeoutMs <= 0))
        e.push("timeoutMs must be positive");
    for (const n of m.environment ?? [])
        if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(n))
            e.push(`invalid environment name: ${n}`);
    for (const [header, env] of Object.entries(m.headers ?? {}))
        if (!(m.environment ?? []).includes(env))
            e.push(`header ${header} references non-allowlisted environment ${env}`);
    if (m.oauth && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(m.oauth.tokenEnv))
        e.push("oauth.tokenEnv is invalid");
    if (closed && m.type === "stdio" && m.command && !m.command.startsWith("/") && !m.command.includes("/"))
        e.push("closed mode requires a pinned stdio command path");
    return e;
}
function allowedTools(m, tools) { return tools.filter(t => !m.tools?.length || m.tools.includes(t.name)).filter(t => !m.excludeTools?.includes(t.name)); }
function timeout(ms, signal) { const c = new AbortController(); const timer = setTimeout(() => c.abort(new MCPError("timeout", `MCP request timed out after ${ms}ms`)), ms); if (signal)
    signal.addEventListener("abort", () => c.abort(signal.reason), { once: true }); return { signal: c.signal, done: () => clearTimeout(timer) }; }
class StdioClient {
    m;
    p;
    next = 1;
    pending = new Map();
    constructor(m) {
        this.m = m;
        const env = {};
        for (const k of m.environment ?? [])
            if (process.env[k] !== undefined)
                env[k] = process.env[k];
        this.p = spawn(m.command, m.args ?? [], { cwd: m.cwd, env: { ...env, PATH: process.env.PATH }, stdio: ["pipe", "pipe", "pipe"] });
        const rl = createInterface({ input: this.p.stdout });
        rl.on("line", line => { try {
            const x = JSON.parse(line);
            if (x.id && this.pending.has(x.id)) {
                const p = this.pending.get(x.id);
                this.pending.delete(x.id);
                x.error ? p.reject(new MCPError("server", x.error.message ?? "MCP server error", x.error)) : p.resolve(x.result);
            }
        }
        catch { } });
        this.p.on("error", e => this.rejectAll(new MCPError("transport", "failed to start MCP server", e)));
        this.p.on("exit", () => this.rejectAll(new MCPError("transport", "MCP server exited")));
    }
    rejectAll(e) { for (const p of this.pending.values())
        p.reject(e); this.pending.clear(); }
    request(method, params, signal) { const id = this.next++; const t = timeout(this.m.timeoutMs ?? 30000, signal); return new Promise((resolve, reject) => { this.pending.set(id, { resolve: v => { t.done(); resolve(v); }, reject: e => { t.done(); reject(e); } }); this.p.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n", e => { if (e)
        reject(new MCPError("transport", "failed to write MCP request", e)); }); t.signal.addEventListener("abort", () => { this.pending.delete(id); reject(t.signal.reason instanceof Error ? t.signal.reason : new MCPError("timeout", "MCP request timed out")); }, { once: true }); }); }
    initialize() { return this.request("initialize", { protocolVersion: "2024-11-05", capabilities: {}, clientInfo: { name: "pi-swarm", version: "1" } }); }
    async listTools() { const r = await this.request("tools/list", {}); return (r?.tools ?? []).map((x) => ({ ...x, serverId: this.m.id })); }
    callTool(name, args, signal) { return this.request("tools/call", { name, arguments: args }, signal); }
    async close() { this.rejectAll(new MCPError("transport", "MCP client closed")); this.p.kill(); }
}
class RemoteClient {
    m;
    constructor(m) {
        this.m = m;
    }
    async request(method, params, signal) { const t = timeout(this.m.timeoutMs ?? 30000, signal); try {
        const headers = { "content-type": "application/json", ...Object.fromEntries(Object.entries(this.m.headers ?? {}).map(([h, e]) => [h, process.env[e] ?? ""])) };
        if (this.m.oauth) {
            const token = process.env[this.m.oauth.tokenEnv];
            if (!token)
                throw new MCPError("auth", `missing OAuth token in ${this.m.oauth.tokenEnv}`);
            headers.authorization = `Bearer ${token}`;
        }
        const r = await fetch(this.m.url, { method: "POST", headers, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }), signal: t.signal });
        if (!r.ok)
            throw new MCPError("transport", `MCP HTTP ${r.status}`);
        const text = await r.text();
        const line = text.split("\n").find(x => x.startsWith("data:"))?.slice(5).trim() ?? text;
        const x = JSON.parse(line);
        if (x.error)
            throw new MCPError("server", x.error.message ?? "MCP server error", x.error);
        return x.result;
    }
    catch (e) {
        if (e instanceof MCPError)
            throw e;
        if (e?.name === "AbortError")
            throw new MCPError("timeout", "MCP request timed out", e);
        throw new MCPError("protocol", "invalid MCP response", e);
    }
    finally {
        t.done();
    } }
    initialize() { return this.request("initialize", { protocolVersion: "2024-11-05", capabilities: {}, clientInfo: { name: "pi-swarm", version: "1" } }); }
    async listTools() { const r = await this.request("tools/list", {}); return (r?.tools ?? []).map((x) => ({ ...x, serverId: this.m.id })); }
    callTool(n, a, s) { return this.request("tools/call", { name: n, arguments: a }, s); }
    close() { return Promise.resolve(); }
}
export function createClient(m, closed = true) { const errors = validateManifest(m, closed); if (errors.length)
    throw new MCPError("config", errors.join("; ")); return m.type === "stdio" ? new StdioClient(m) : new RemoteClient(m); }
export class MCPManager {
    manifests;
    opts;
    clients = new Map();
    discovered = new Map();
    constructor(manifests, opts = {}) {
        this.manifests = manifests;
        this.opts = opts;
        for (const m of manifests) {
            const errs = validateManifest(m, opts.closed ?? true);
            if (errs.length)
                throw new MCPError("config", `${m.id}: ${errs.join("; ")}`);
        }
    }
    manifestsList() { return this.manifests.map(m => ({ ...m, headers: undefined, oauth: m.oauth ? { tokenEnv: m.oauth.tokenEnv, scopes: m.oauth.scopes } : undefined })); }
    async discover(id) { const m = this.manifests.find(x => x.id === id); if (!m)
        throw new MCPError("config", `unknown MCP server ${id}`); if (this.discovered.has(id))
        return this.discovered.get(id); const c = createClient(m, this.opts.closed ?? true); await c.initialize(); const tools = allowedTools(m, await c.listTools()); this.clients.set(id, c); this.discovered.set(id, tools); for (const t of tools)
        this.opts.registerTool?.({ name: `mcp__${id}__${t.name}`, label: t.name, description: t.description ?? `MCP tool ${t.name}`, parameters: t.inputSchema ?? { type: "object" }, execute: (_call, args, signal) => c.callTool(t.name, args, signal) }); return tools; }
    async call(server, tool, args, signal) { if (!this.discovered.has(server))
        await this.discover(server); const c = this.clients.get(server); if (!this.discovered.get(server).some(t => t.name === tool))
        throw new MCPError("denied", `tool ${tool} is not allowed`); return c.callTool(tool, args, signal); }
    async close() { await Promise.all([...this.clients.values()].map(c => c.close())); this.clients.clear(); }
}
export const manifestSchema = { type: "object", additionalProperties: false, required: ["id", "type"], properties: { id: { type: "string" }, type: { type: "string", enum: ["stdio", "http", "sse"] }, command: { type: "string" }, args: { type: "array", items: { type: "string" } }, url: { type: "string" }, environment: { type: "array", items: { type: "string" } }, headers: { type: "object" }, tools: { type: "array", items: { type: "string" } }, excludeTools: { type: "array", items: { type: "string" } }, lazy: { type: "boolean" }, timeoutMs: { type: "number" }, oauth: { type: "object" } } };
export default function mcpExtension(pi) { let manager; pi.on?.("session_start", () => { const manifests = pi.mcpManifests ?? []; manager = new MCPManager(manifests, { closed: pi.mcpClosedMode ?? true, registerTool: t => pi.registerTool(t) }); }); pi.registerCommand?.("mcp", { description: "Discover or close declared MCP servers", handler: async (args) => { if (!manager)
        throw new MCPError("config", "MCP not initialized"); const [op, id] = args.trim().split(/\s+/, 2); if (op === "discover" && id)
        return manager.discover(id); if (op === "close")
        return manager.close(); return manager.manifestsList(); } }); return manager; }
