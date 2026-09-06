import { createHash, randomUUID } from "node:crypto";
import { realpath } from "node:fs/promises";
import { spawn } from "node:child_process";
import { relative, resolve, isAbsolute } from "node:path";

export type Operation = "read" | "write" | "execute" | "network" | "credential";

/** Explicit unattended-worker boundary. Omitted limits are intentionally conservative. */
export interface AutonomyPolicy {
  workspace: string;
  tools: readonly string[];
  network: { enabled: boolean; allowedHosts: readonly string[] };
  mutation: { enabled: boolean; approval: "deny" | "broker" | "autoApprove" };
  approval: { requiredFor: readonly Operation[] };
  budget: { maxAttempts: number; maxTokens?: number; maxCost?: number; timeoutMs: number };
  recursion: { maxDepth: number; maxChildren: number };
}

export function createAutonomyPolicy(input: Partial<AutonomyPolicy> & Pick<AutonomyPolicy, "workspace">): AutonomyPolicy {
  const p: AutonomyPolicy = {
    workspace: resolve(input.workspace), tools: [...(input.tools ?? [])],
    network: { enabled: input.network?.enabled ?? false, allowedHosts: [...(input.network?.allowedHosts ?? [])] },
    mutation: { enabled: input.mutation?.enabled ?? false, approval: input.mutation?.approval ?? "deny" },
    approval: { requiredFor: [...(input.approval?.requiredFor ?? ["write", "execute", "network"])] },
    budget: { maxAttempts: input.budget?.maxAttempts ?? 1, maxTokens: input.budget?.maxTokens, maxCost: input.budget?.maxCost, timeoutMs: input.budget?.timeoutMs ?? 30_000 },
    recursion: { maxDepth: input.recursion?.maxDepth ?? 0, maxChildren: input.recursion?.maxChildren ?? 0 },
  };
  if (p.budget.maxAttempts < 1 || p.budget.timeoutMs < 1 || p.recursion.maxDepth < 0 || p.recursion.maxChildren < 0) throw new PolicyError("limits.invalid", "autonomy limits are invalid");
  if (p.mutation.approval === "autoApprove" && !p.mutation.enabled) throw new PolicyError("approval.invalid", "auto approval requires mutation enabled");
  return Object.freeze(p);
}
export type Decision = "allow" | "deny" | "approval_required";
export interface PolicyConfig {
  workspace: string;
  allowedTools?: readonly string[];
  allowMutation?: boolean;
  allowNetwork?: boolean;
  allowedHosts?: readonly string[];
  allowedCredentialEnv?: readonly string[];
  requireApprovalFor?: readonly Operation[];
  maxOutputBytes?: number;
  timeoutMs?: number;
}
export interface Request { tool: string; operation: Operation; path?: string; command?: string; host?: string; credentialEnv?: string; approvalToken?: string; }
export interface AuditRecord { id: string; at: string; tool: string; operation: Operation; decision: Decision; reason: string; resource?: string; }
export class PolicyError extends Error { constructor(public readonly code: string, message: string) { super(message); this.name = "PolicyError"; } }

const SECRET = /(token|password|passwd|secret|credential|authorization|cookie|api[_-]?key|private[_-]?key)/i;
const norm = (s: string) => s.trim().replaceAll("\\", "/");
const hostOnly = (host: string) => host.toLowerCase().replace(/:\d+$/, "").replace(/^\[|\]$/g, "");

export class Policy {
  readonly config: Required<Pick<PolicyConfig, "workspace" | "allowMutation" | "allowNetwork" | "maxOutputBytes" | "timeoutMs">> & PolicyConfig;
  private readonly records: AuditRecord[] = [];
  constructor(config: PolicyConfig) {
    if (!config.workspace) throw new PolicyError("workspace.required", "workspace is required");
    this.config = { ...config, workspace: resolve(config.workspace), allowMutation: config.allowMutation ?? false, allowNetwork: config.allowNetwork ?? false, maxOutputBytes: config.maxOutputBytes ?? 1024 * 1024, timeoutMs: config.timeoutMs ?? 30_000 };
    if (this.config.maxOutputBytes < 1 || this.config.timeoutMs < 1) throw new PolicyError("limits.invalid", "limits must be positive");
  }
  audit(): readonly AuditRecord[] { return this.records.map(x => ({ ...x })); }
  private record(r: Request, decision: Decision, reason: string, resource?: string) { const row = { id: randomUUID(), at: new Date().toISOString(), tool: r.tool, operation: r.operation, decision, reason, resource: resource && SECRET.test(resource) ? "[REDACTED]" : resource }; this.records.push(row); if (this.records.length > 1000) this.records.shift(); return row; }
  private fail(r: Request, code: string, reason: string, resource?: string): never { this.record(r, "deny", reason, resource); throw new PolicyError(code, reason); }
  authorize(r: Request): AuditRecord {
    if (this.config.allowedTools && !this.config.allowedTools.includes(r.tool)) return this.fail(r, "tool.denied", `tool ${r.tool} is not allowlisted`);
    if (r.operation === "write" && !this.config.allowMutation) return this.fail(r, "mutation.denied", "mutation is disabled");
    if (r.operation === "network") { if (!this.config.allowNetwork) return this.fail(r, "network.denied", "network access is disabled"); if (!r.host) return this.fail(r, "network.host.required", "network host is required"); const h = hostOnly(r.host); if (!(this.config.allowedHosts ?? []).some(x => hostOnly(x) === h)) return this.fail(r, "network.host.denied", `host ${h} is not allowlisted`, h); }
    if (r.operation === "credential") { if (!r.credentialEnv || !/^[A-Z_][A-Z0-9_]*$/.test(r.credentialEnv)) return this.fail(r, "credential.invalid", "credential must name an environment variable"); if (!(this.config.allowedCredentialEnv ?? []).includes(r.credentialEnv)) return this.fail(r, "credential.denied", `credential ${r.credentialEnv} is not allowlisted`, r.credentialEnv); }
    if (r.operation === "execute" && /(^|\s)(sudo|shutdown|reboot|mkfs|dd)(\s|$)/i.test(r.command ?? "") && r.approvalToken !== this.approvalToken(r)) return this.fail(r, "approval.required", "dangerous process requires approval");
    if ((this.config.requireApprovalFor ?? []).includes(r.operation) && r.approvalToken !== this.approvalToken(r)) { this.record(r, "approval_required", "explicit approval required"); throw new PolicyError("approval.required", "explicit approval required"); }
    if (r.path && (r.operation === "read" || r.operation === "write")) { if (!isAbsolute(r.path)) return this.fail(r, "path.absolute", "filesystem path must be absolute"); const p = resolve(r.path); const root = this.config.workspace; if (relative(root, p).startsWith("..") || relative(root, p) === "") return this.fail(r, "path.boundary", "path must be inside workspace", p); }
    return this.record(r, "allow", "authorized", r.path ?? r.host ?? r.command);
  }
  approvalToken(r: Request): string { return createHash("sha256").update(JSON.stringify({ tool: r.tool, operation: r.operation, path: r.path, command: r.command, host: r.host, credentialEnv: r.credentialEnv })).digest("hex"); }
  async authorizePath(r: Request): Promise<string> { this.authorize(r); if (!r.path) throw new PolicyError("path.required", "path is required"); const p = resolve(r.path); try { const actual = await realpath(p); const root = await realpath(this.config.workspace); if (relative(root, actual).startsWith("..") || actual === root) this.fail(r, "path.boundary", "resolved path must be inside workspace", actual); return actual; } catch (e) { if (e instanceof PolicyError) throw e; if (r.operation === "write") { const parent = await realpath(resolve(p, "..")); const root = await realpath(this.config.workspace); if (relative(root, parent).startsWith("..")) this.fail(r, "path.boundary", "parent must be inside workspace", parent); return p; } throw new PolicyError("path.missing", "path does not exist"); } }
}

export interface ProcessResult { stdout: string; stderr: string; code: number | null; timedOut: boolean; truncated: boolean; }
export async function runProcess(policy: Policy, request: Request, args: readonly string[] = [], signal?: AbortSignal): Promise<ProcessResult> {
  policy.authorize({ ...request, operation: "execute" }); if (!request.command) throw new PolicyError("command.required", "command is required");
  return await new Promise((resolveResult, reject) => { const child = spawn(request.command!, [...args], { shell: false, env: {}, stdio: ["ignore", "pipe", "pipe"] }); let out = "", err = "", bytes = 0, truncated = false, timedOut = false; const take = (chunk: Buffer, target: "out" | "err") => { const room = policy.config.maxOutputBytes - bytes; if (room <= 0) { truncated = true; return; } const text = chunk.subarray(0, room).toString(); bytes += Buffer.byteLength(text); if (target === "out") out += text; else err += text; if (text.length < chunk.length) truncated = true; }; child.stdout.on("data", c => take(c, "out")); child.stderr.on("data", c => take(c, "err")); const timer = setTimeout(() => { timedOut = true; child.kill("SIGTERM"); setTimeout(() => child.kill("SIGKILL"), 100).unref(); }, policy.config.timeoutMs); const abort = () => child.kill("SIGTERM"); signal?.addEventListener("abort", abort, { once: true }); child.on("error", e => { clearTimeout(timer); signal?.removeEventListener("abort", abort); reject(e); }); child.on("close", code => { clearTimeout(timer); signal?.removeEventListener("abort", abort); resolveResult({ stdout: out, stderr: err, code, timedOut, truncated }); }); });
}
