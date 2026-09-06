/**
 * Transparent local Pi vault. Secrets are base64-obfuscated, NOT encrypted,
 * matching Swarm's documented transparent-vault storage mode.
 *
 * Two-person cryptography is intentionally unavailable: those calls return the
 * same roster requirement/locked shapes as Swarm rather than pretending that a
 * second principal approved anything.
 */
import { randomBytes } from "node:crypto";
import { spawn } from "node:child_process";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";

type AnyMap = Record<string, any>;
type RecordFile = { version: 1; credentials: Record<string, AnyMap> };
export interface VaultRuntime { path?: string; configured?: boolean; locked?: boolean }
const kinds = new Set(["api_key", "bearer_token", "ssh_key", "aws_access_key", "aws_secret_key", "password", "env_var"]);
/**
 * Swarm's global transparent store (vault/autoload.go DefaultAutoLoadPaths).
 * vault_unlock.go AutoLoadInto installs a provider only when this file
 * exists; otherwise the NoOp provider stays and every vault tool reports
 * "vault is locked". Pi mirrors that: absent file ⇒ locked.
 */
export const defaultPiVaultPath = () => resolve(process.env.HOME ?? process.cwd(), ".swarm/vault/credentials.json");
const empty = (): RecordFile => ({ version: 1, credentials: {} });
// vault/transparent.go disk format (version "2" cleartext; "1" base64 values).
const fromDisk = (id: string, tc: AnyMap, version: string): AnyMap => ({
  id, name: tc.name ?? "", kind: tc.kind, scope: tc.scope || "global",
  secretBase64: version === "1" ? String(tc.value ?? "") : Buffer.from(String(tc.value ?? "")).toString("base64"),
  allowedTools: tc.allowedTools ?? [], allowedCommands: tc.allowedCommands ?? [], allowedHosts: tc.allowedHosts ?? [], tags: tc.tags ?? [],
  target: tc.injectTarget ?? "", ...(tc.expiresAt ? { expiresAt: tc.expiresAt } : {}),
});
const toDisk = (c: AnyMap): AnyMap => ({
  kind: c.kind, value: Buffer.from(c.secretBase64 ?? "", "base64").toString(),
  ...(c.allowedTools?.length ? { allowedTools: c.allowedTools } : {}), ...(c.allowedCommands?.length ? { allowedCommands: c.allowedCommands } : {}), ...(c.allowedHosts?.length ? { allowedHosts: c.allowedHosts } : {}), ...(c.tags?.length ? { tags: c.tags } : {}),
  injectMethod: c.kind === "ssh_key" || c.target?.startsWith("/") ? "file" : "env", ...(c.target ? { injectTarget: c.target } : {}),
  ...(c.scope ? { scope: c.scope } : {}), ...(c.expiresAt ? { expiresAt: c.expiresAt } : {}), ...(c.name ? { name: c.name } : {}),
});
async function load(rt: VaultRuntime): Promise<RecordFile> {
  try {
    const disk = JSON.parse(await readFile(rt.path ?? defaultPiVaultPath(), "utf8"));
    if (disk?.version === 1 && disk.credentials) return disk; // pre-transparent Pi format
    const version = String(disk?.version ?? "2");
    return { version: 1, credentials: Object.fromEntries(Object.entries(disk?.credentials ?? {}).map(([id, tc]) => [id, fromDisk(id, tc as AnyMap, version)])) };
  } catch { return empty(); }
}
async function save(rt: VaultRuntime, data: RecordFile) {
  const path = rt.path ?? defaultPiVaultPath(); await mkdir(dirname(path), { recursive: true, mode: 0o700 });
  const disk = { version: "2", updatedAt: new Date().toISOString(), credentials: Object.fromEntries(Object.entries(data.credentials).map(([id, c]) => [id, toDisk(c)])) };
  const tmp = `${path}.${process.pid}.tmp`; await writeFile(tmp, JSON.stringify(disk, null, 2), { mode: 0o600 }); await rename(tmp, path);
}
// An explicit `path` is a configured store (NewTransparentStorage accepts a
// not-yet-existing file); the autoload default only installs when it exists.
const locked = (rt: VaultRuntime) => rt.locked === true || rt.configured === false || (rt.locked === undefined && rt.configured === undefined && rt.path === undefined && !existsSync(defaultPiVaultPath()));
const sensitive = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
function validSSH(s: string) {
  return ["-----BEGIN OPENSSH PRIVATE KEY-----", "-----BEGIN RSA PRIVATE KEY-----", "-----BEGIN EC PRIVATE KEY-----", "-----BEGIN DSA PRIVATE KEY-----", "-----BEGIN PRIVATE KEY-----", "-----BEGIN ENCRYPTED PRIVATE KEY-----"].some((marker) => s.includes(marker));
}
function durationMs(value: string): number | undefined { const m = /^(\d+)(h|m|s)$/.exec(value); return m ? Number(m[1]) * ({ h: 3600000, m: 60000, s: 1000 } as AnyMap)[m[2]] : undefined; }
function metadata(c: AnyMap): AnyMap { return { id: c.id, name: c.name ?? "", kind: c.kind, scope: c.scope, ...(c.allowedTools?.length ? { allowedTools: c.allowedTools } : {}), ...(c.allowedCommands?.length ? { allowedCommands: c.allowedCommands } : {}), ...(c.allowedHosts?.length ? { allowedHosts: c.allowedHosts } : {}), ...(c.tags?.length ? { tags: c.tags } : {}) }; }

export async function vaultAdd(p: AnyMap, rt: VaultRuntime = {}): Promise<AnyMap> {
  if (!p.id) return { success: false, credentialId: "", scope: "", kind: "", error: "id is required" };
  if (!p.kind) return { success: false, credentialId: "", scope: "", kind: "", error: "kind is required" };
  // vault_add.go never validates Kind against the known set; unknown kinds are stored verbatim.
  if (p.secret && p.kind === "ssh_key" && !validSSH(p.secret)) return { success: false, credentialId: "", scope: "", kind: "", error: "secret does not look like a valid ssh_key (expected PEM or OpenSSH private key material starting with a \"-----BEGIN ... PRIVATE KEY-----\" line); the credential was NOT modified. Omit 'secret' to update allowedTools/allowedCommands/allowedHosts/tags on an existing credential without touching its stored key." };
  if (p.expire) { const ms = durationMs(p.expire); if (!ms) return { success: false, credentialId: "", scope: "", kind: "", error: `invalid expiration duration '${p.expire}': time: invalid duration \"${p.expire}\"` }; }
  if (locked(rt)) return { success: false, credentialId: "", scope: "", kind: "", error: "vault is locked — tell the user to unlock the vault by typing /vault in the TUI (or Settings → Vault, or 'swarmos vault unlock' in CLI) before adding credentials" };
  const threshold = p.threshold >= 2 ? p.threshold : p.tags?.includes("sensitive") ? 2 : 0;
  if (threshold) return { success: false, credentialId: "", scope: "", kind: "", error: "two-person storage requires a team roster (recipients.txt) and a user identity; run 'swarmos vault init --project --team' and add members with 'swarmos vault allow' first" };
  const data = await load(rt), existing = data.credentials[p.id], metadataOnly = !p.secret;
  if (metadataOnly && !existing) return { success: false, credentialId: "", scope: "", kind: "", error: `secret is required when adding a new credential (no existing credential found for id ${p.id})` };
  const scope = p.scope || "global";
  const cred = { id: p.id, name: p.name ?? "", kind: p.kind || existing.kind, scope, secretBase64: p.secret ? Buffer.from(p.secret).toString("base64") : existing.secretBase64, allowedTools: p.allowedTools ?? [], allowedCommands: p.allowedCommands ?? [], allowedHosts: p.allowedHosts ?? [], tags: p.tags ?? [], target: p.target || existing?.target || "", ...(p.expire ? { expiresAt: new Date(Date.now() + durationMs(p.expire)!).toISOString() } : {}) };
  data.credentials[p.id] = cred; await save(rt, data);
  return { success: true, credentialId: p.id, scope, kind: cred.kind, ...(metadataOnly ? { metadataOnly: true } : {}), warning: metadataOnly ? "Credential metadata updated (allowedTools/allowedCommands/allowedHosts/tags/target); the stored secret was left unchanged." : "Credential stored. It can now be used with vault_exec." };
}

export async function vaultList(p: AnyMap, rt: VaultRuntime = {}): Promise<AnyMap> {
  if (locked(rt)) return { credentials: [], warning: "vault is locked — no credentials available. Tell the user to unlock the vault by typing /vault in the TUI (or Settings → Vault, or 'swarmos vault unlock' in CLI)." };
  const data = await load(rt); let values = Object.values(data.credentials).filter((c) => !c.expiresAt || new Date(c.expiresAt) > new Date());
  if (p.kind) values = values.filter((c) => c.kind === p.kind); if (p.scope) values = values.filter((c) => c.scope === p.scope); if (p.tags) values = values.filter((c) => p.tags.every((t: string) => c.tags?.includes(t)));
  return { credentials: values.map(metadata) };
}

function splitCommand(value: string): string[] {
  const out: string[] = []; let cur = "", quote = "";
  for (const c of value) { if ((c === "'" || c === "\"")) { if (!quote) quote = c; else if (quote === c) quote = ""; else cur += c; } else if (/\s/.test(c) && !quote) { if (cur) out.push(cur), cur = ""; } else cur += c; }
  if (cur) out.push(cur); return out;
}
function elapsed(ms: number) { if (ms < 1000) return `${ms}ms`; return `${(ms / 1000).toFixed(ms % 1000 ? 3 : 0)}s`; }
function execute(command: string, args: string[], options: AnyMap): Promise<{ stdout: string; stderr: string; code: number; duration: number; error?: string }> {
  return new Promise((done) => { const started = Date.now(), child = spawn(command, args, options), stdout: Buffer[] = [], stderr: Buffer[] = []; let timer: NodeJS.Timeout;
    child.stdout?.on("data", (x) => stdout.push(x)); child.stderr?.on("data", (x) => stderr.push(x));
    child.on("error", (e) => done({ stdout: "", stderr: "", code: -1, duration: Date.now() - started, error: e.message }));
    child.on("spawn", () => { timer = setTimeout(() => child.kill("SIGKILL"), options.timeout); });
    child.on("close", (code) => { clearTimeout(timer); done({ stdout: Buffer.concat(stdout).toString(), stderr: Buffer.concat(stderr).toString(), code: code ?? -1, duration: Date.now() - started }); });
  });
}
export async function vaultExec(p: AnyMap, rt: VaultRuntime = {}): Promise<AnyMap> {
  if (!p.credentialId) return { stdout: "", stderr: "", exitCode: 0, duration: "", redactedCount: 0, safeToParse: false, error: "credentialId is required" };
  if (!p.command) return { stdout: "", stderr: "", exitCode: 0, duration: "", redactedCount: 0, safeToParse: false, error: "command is required" };
  if (locked(rt)) return { stdout: "", stderr: "", exitCode: 0, duration: "", redactedCount: 0, safeToParse: false, error: "vault is locked — no credentials available. Tell the user to unlock the vault by typing /vault in the TUI (or Settings → Vault, or 'swarmos vault unlock' in CLI). Use vault_list first to see available credentials." };
  const data = await load(rt), c = data.credentials[p.credentialId];
  if (!c) return { stdout: "", stderr: "", exitCode: -1, duration: "", redactedCount: 0, safeToParse: false, status: "failed", error: `credential not found: ${p.credentialId}` };
  if (p.twoPersonRequestId) return { stdout: "", stderr: "", exitCode: -1, duration: "", redactedCount: 0, safeToParse: false, status: "failed", error: "two-person finalize failed: two-person storage requires a team roster (ensure a distinct second person approved via vault_approve)" };
  const pieces = p.args?.length ? [p.command, ...p.args] : splitCommand(p.command), command = pieces[0], args = pieces.slice(1);
  if (c.allowedCommands?.length && !c.allowedCommands.some((pattern: string) => new RegExp("^" + pattern.split("*").map(sensitive).join(".*") + "$").test([command, ...args].join(" ")))) {
    if (p.approvalId) return { stdout: "", stderr: "", exitCode: 0, duration: "", redactedCount: 0, safeToParse: false, error: "approval failed: interactive host approval is unavailable in Pi" };
    const approvalId = "approval_" + randomBytes(10).toString("hex");
    return { stdout: "", stderr: "", exitCode: 0, duration: "", redactedCount: 0, safeToParse: false, status: "needs_approval", approvalId, warning: "This credential requires host approval. After the host records approval, call vault_exec again with the same command and approvalId set to the value above." };
  }
  const secret = Buffer.from(c.secretBase64, "base64").toString(), env = { ...process.env }; let cleanup: string | undefined;
  if (c.kind === "ssh_key" || c.target?.startsWith("/")) { const path = c.target || `/tmp/pi-vault-${process.pid}-${randomBytes(4).toString("hex")}`; await writeFile(path, secret, { mode: 0o600 }); cleanup = path; (env as AnyMap).SSH_KEY_PATH = path; }
  else (env as AnyMap)[c.target || c.id.toUpperCase().replace(/[^A-Z0-9]+/g, "_")] = secret;
  const result = await execute(command, args, { cwd: p.workingDir || undefined, env, stdio: ["ignore", "pipe", "pipe"], timeout: (p.timeout > 0 ? p.timeout : 60) * 1000 });
  if (cleanup) { const { unlink } = await import("node:fs/promises"); await unlink(cleanup).catch(() => {}); }
  if (result.error) return { stdout: "", stderr: "", exitCode: -1, duration: "", redactedCount: 0, safeToParse: false, status: "failed", error: result.error };
  let count = 0; const replace = (x: string) => x.replace(new RegExp(sensitive(secret), "g"), () => (count++, "[REDACTED]")); const stdout = replace(result.stdout), stderr = replace(result.stderr);
  return { stdout, stderr, exitCode: result.code, duration: elapsed(result.duration), redactedCount: count, ...(count ? { redactionHints: ["credential value"] } : {}), safeToParse: count === 0, status: "ok", warning: "Credential value was never exposed. Output was scanned for accidental secret leakage." };
}
export function vaultApprove(p: AnyMap, rt: VaultRuntime = {}): AnyMap {
  if (!p.requestId) return { satisfied: false, error: "requestId is required" };
  if (locked(rt)) return { satisfied: false, error: "vault is locked — unlock it (type /vault in the TUI) before approving" };
  return { satisfied: false, requestId: p.requestId, error: "two-person storage requires a team roster (recipients.txt) and a user identity; run 'swarmos vault init --project --team' and add members with 'swarmos vault allow' first" };
}
export function vaultTwoPersonStatus(p: AnyMap, rt: VaultRuntime = {}): AnyMap {
  if (!p.requestId) return { approvals: 0, remaining: 0, satisfied: false, error: "requestId is required" };
  if (locked(rt)) return { approvals: 0, remaining: 0, satisfied: false, error: "vault is locked — unlock it (type /vault in the TUI) before checking status" };
  return { requestId: p.requestId, approvals: 0, remaining: 0, satisfied: false, expired: true, error: "unknown, already finalized, or expired request", warning: "This request is no longer pending. If it was finalized, the command already ran; if it expired, start over with vault_exec." };
}

export function vaultJSONXML(value: unknown): string {
  return `<result>\n  <data><![CDATA[${JSON.stringify(value, null, 2)}]]></data>\n</result>`;
}
