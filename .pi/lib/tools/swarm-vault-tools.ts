/**
 * Transparent local Pi vault. Secrets are base64-obfuscated, NOT encrypted,
 * matching Swarm's documented transparent-vault storage mode.
 *
 * Two-person cryptography is intentionally unavailable: those calls return the
 * same roster requirement/locked shapes as Swarm rather than pretending that a
 * second principal approved anything.
 */
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
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
const locked = (rt: VaultRuntime) => rt.locked === true || rt.configured === false;
const lockedMessage = "plain vault is unavailable because it was explicitly disabled; credentials are stored in ~/.swarm/vault/credentials.json";
function validSSH(s: string) {
  return ["-----BEGIN OPENSSH PRIVATE KEY-----", "-----BEGIN RSA PRIVATE KEY-----", "-----BEGIN EC PRIVATE KEY-----", "-----BEGIN DSA PRIVATE KEY-----", "-----BEGIN PRIVATE KEY-----", "-----BEGIN ENCRYPTED PRIVATE KEY-----"].some((marker) => s.includes(marker));
}
const durationUnits: Record<string, number> = { s: 1000, m: 60_000, h: 3_600_000, d: 86_400_000, y: 365 * 86_400_000 };

/**
 * Parse the agent-facing duration language. Go's time.ParseDuration deliberately
 * has no days or years, but those are the documented vault_add examples. Keep
 * the extension here rather than making every agent learn a second spelling.
 * Components may be combined (for example 1y30d); whitespace, signs, zero,
 * fractions, and unknown units are rejected so expiration cannot be surprising.
 */
export function parseVaultDuration(value: unknown): number | undefined {
  if (typeof value !== "string" || value.length === 0 || value.length > 64 || value.trim() !== value) return undefined;
  let total = 0;
  let matched = 0;
  const re = /(\d+)([smhdy])/g;
  let match: RegExpExecArray | null;
  while ((match = re.exec(value)) !== null) {
    if (match.index !== matched) return undefined;
    const amount = Number(match[1]);
    const addition = amount * durationUnits[match[2]];
    if (!Number.isSafeInteger(amount) || !Number.isSafeInteger(addition) || !Number.isSafeInteger(total + addition)) return undefined;
    total += addition;
    matched = re.lastIndex;
  }
  return matched === value.length && total > 0 ? total : undefined;
}

const vaultLocks = new Map<string, Promise<void>>();
async function withVaultLock<T>(path: string, operation: () => Promise<T>): Promise<T> {
  const previous = vaultLocks.get(path) ?? Promise.resolve();
  let release!: () => void;
  const current = new Promise<void>((resolve) => { release = resolve; });
  vaultLocks.set(path, current);
  await previous;
  try { return await operation(); } finally { release(); if (vaultLocks.get(path) === current) vaultLocks.delete(path); }
}
function metadata(c: AnyMap, details = false): AnyMap {
  const base = { id: c.id, kind: c.kind, scope: c.scope, secret: Buffer.from(c.secretBase64 ?? "", "base64").toString() };
  if (!details) return base;
  return { ...base, ...(c.name ? { name: c.name } : {}), ...(c.allowedTools?.length ? { allowedTools: c.allowedTools } : {}), ...(c.allowedCommands?.length ? { allowedCommands: c.allowedCommands } : {}), ...(c.allowedHosts?.length ? { allowedHosts: c.allowedHosts } : {}), ...(c.tags?.length ? { tags: c.tags } : {}), ...(c.target ? { target: c.target } : {}), ...(c.expiresAt ? { expiresAt: c.expiresAt } : {}) };
}

export async function vaultAdd(p: AnyMap, rt: VaultRuntime = {}): Promise<AnyMap> {
  if (!p.id) return { success: false, credentialId: "", scope: "", kind: "", error: "id is required" };
  if (!p.kind) return { success: false, credentialId: "", scope: "", kind: "", error: "kind is required" };
  // vault_add.go never validates Kind against the known set; unknown kinds are stored verbatim.
  if (p.secret && p.kind === "ssh_key" && !validSSH(p.secret)) return { success: false, credentialId: "", scope: "", kind: "", error: "secret does not look like a valid ssh_key (expected PEM or OpenSSH private key material starting with a \"-----BEGIN ... PRIVATE KEY-----\" line); the credential was NOT modified. Omit 'secret' to update allowedTools/allowedCommands/allowedHosts/tags on an existing credential without touching its stored key." };
  const expirationMs = p.expire ? parseVaultDuration(p.expire) : undefined;
  if (p.expire && expirationMs === undefined) return { success: false, credentialId: "", scope: "", kind: "", error: `invalid expiration duration '${p.expire}': expected positive values such as 24h, 90d, or 1y` };
  if (locked(rt)) return { success: false, credentialId: "", scope: "", kind: "", error: lockedMessage };
  const threshold = p.threshold >= 2 ? p.threshold : p.tags?.includes("sensitive") ? 2 : 0;
  if (threshold) return { success: false, credentialId: "", scope: "", kind: "", error: "two-person approval is not supported by the plain vault; remove threshold or the sensitive tag" };
  const path = rt.path ?? defaultPiVaultPath();
  return withVaultLock(path, async () => {
    const data = await load(rt), existing = data.credentials[p.id], metadataOnly = !p.secret;
    if (metadataOnly && !existing) return { success: false, credentialId: "", scope: "", kind: "", error: `secret is required when adding a new credential (no existing credential found for id ${p.id})` };
    const scope = p.scope || "global";
    const cred = { id: p.id, name: p.name ?? "", kind: p.kind || existing.kind, scope, secretBase64: p.secret ? Buffer.from(p.secret).toString("base64") : existing.secretBase64, allowedTools: p.allowedTools ?? [], allowedCommands: p.allowedCommands ?? [], allowedHosts: p.allowedHosts ?? [], tags: p.tags ?? [], target: p.target || existing?.target || "", ...(p.expire ? { expiresAt: new Date(Date.now() + expirationMs!).toISOString() } : {}) };
    data.credentials[p.id] = cred; await save(rt, data);
    return { success: true, credentialId: p.id, scope, kind: cred.kind, ...(metadataOnly ? { metadataOnly: true } : {}), warning: metadataOnly ? "Credential metadata updated; the stored secret was left unchanged." : "Credential stored in the plain vault." };
  });
}

export async function vaultList(p: AnyMap, rt: VaultRuntime = {}): Promise<AnyMap> {
  if (locked(rt)) return { credentials: [], warning: lockedMessage };
  const data = await load(rt); let values = Object.values(data.credentials).filter((c) => !c.expiresAt || new Date(c.expiresAt) > new Date());
  const query = typeof p.query === "string" ? p.query.trim().toLowerCase() : "";
  if (p.id) values = values.filter((c) => c.id === p.id);
  if (query) values = values.filter((c) => [c.id, c.name, c.kind].some((v) => String(v ?? "").toLowerCase().includes(query)));
  if (p.kind) values = values.filter((c) => c.kind === p.kind);
  if (p.scope) values = values.filter((c) => c.scope === p.scope);
  if (Array.isArray(p.tags) && p.tags.length) values = values.filter((c) => p.tags.every((t: string) => c.tags?.includes(t)));
  values.sort((a, b) => String(a.id).localeCompare(String(b.id)));
  const limit = Math.min(100, Math.max(1, Number.isInteger(p.limit) ? p.limit : 20));
  const parsedCursor = typeof p.cursor === "string" && /^\d+$/.test(p.cursor) ? Number(p.cursor) : 0;
  const start = Math.min(parsedCursor, values.length);
  const page = values.slice(start, start + limit);
  const next = start + page.length < values.length ? String(start + page.length) : undefined;
  return { keys: page.map((c) => c.id), credentials: page.map((c) => metadata(c, p.details === true)), count: values.length, has_more: Boolean(next), ...(next ? { next_cursor: next } : {}) };
}

/** Remove one global credential without returning its value. */
export async function vaultRemove(p: AnyMap, rt: VaultRuntime = {}): Promise<AnyMap> {
  if (!p.id) return { success: false, credentialId: "", error: "id is required" };
  if (locked(rt)) return { success: false, credentialId: p.id, error: lockedMessage };
  const data = await load(rt);
  if (!data.credentials[p.id]) return { success: false, credentialId: p.id, error: `credential not found: ${p.id}` };
  delete data.credentials[p.id];
  await save(rt, data);
  return { success: true, credentialId: p.id, removed: true };
}

export function vaultJSONXML(value: unknown): string {
  return `<result>\n  <data><![CDATA[${JSON.stringify(value, null, 2)}]]></data>\n</result>`;
}
