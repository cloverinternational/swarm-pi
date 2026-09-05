import { createHash, randomUUID } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, renameSync, writeFileSync, rmSync } from "node:fs";
import { join, relative, resolve } from "node:path";

export type Mode = "never" | "manual" | "auto";
export type TriggerReason = "manual" | "tool_call_threshold" | "error_resolution" | "llm_nudge";
export type CuratorState = "active" | "stale" | "archived" | "pinned" | "consolidated";
export interface CuratorRunRequest { prompt: string; preview: boolean; consolidate: boolean; timeoutMs: number; maxTurns: number; }
export interface CuratorRunResult { output: string; }
export interface CuratorRunner { (request: CuratorRunRequest): Promise<CuratorRunResult> }
export interface Config {
  mode?: Mode; dir?: string; toolCallThreshold?: number; errorResolutionThreshold?: number; nudgeInterval?: number;
  minInstructionsLength?: number; toolCallBudget?: number; workingBudget?: number; maxNudgeIgnores?: number;
  staleAfterDays?: number; archiveAfterDays?: number; lockTimeoutMs?: number; reviewHook?: ReviewHook;
  previewOnly?: boolean; requireReadBeforeWrite?: boolean; curatorRunner?: CuratorRunner; curatorMinRunGapMs?: number;
  curatorIdleDelayMs?: number; curatorConsolidate?: boolean; curatorTimeoutMs?: number; curatorMaxTurns?: number;
  protectSkill?: (name: string) => boolean; accountingExempt?: boolean;
}
export interface Metrics { turns: number; toolCalls: number; errors: number; resolved: number; nudges: number; nudgeIgnores: number; reviews: number; mutations: number; skilled: boolean; budgetCalls: number; reviewRequired: boolean; }
export interface ReviewHook { (event: { action: string; name?: string; revision?: string; reason?: string }): void }
export interface Skill { name: string; description: string; instructions: string; tags: string[]; category?: string; version: string; path: string; updatedAt: string; }
export interface SkillEntry { type: "pi-swarm-autogen-state"; data: State; }
export interface Revision { id: string; parent?: string; action: string; createdAt: string; files: Record<string, string>; blobs?: Record<string, string>; }
type StoredRevision = Revision & { blobs: Record<string, string> };
export interface State { skills: Record<string, { version: string; uses: number; lastUsed?: string; archived?: boolean; hash?: string; curatorState?: CuratorState; pinned?: boolean; absorbedInto?: string; archiveReason?: string }>; turns: number; toolCalls: number; errors: number; resolved: number; lastNudgeTurn: number; reviews: number; nudges: number; nudgeIgnores: number; mutations: number; skilled: boolean; budgetCalls: number; reviewRequired: boolean; focusedTask?: boolean; curatorLastRun?: string; curatorLastReport?: string; }
export const defaultState = (): State => ({ skills: {}, turns: 0, toolCalls: 0, errors: 0, resolved: 0, lastNudgeTurn: 0, reviews: 0, nudges: 0, nudgeIgnores: 0, mutations: 0, skilled: false, budgetCalls: 0, reviewRequired: false, focusedTask: false });
const HISTORY_FORMAT = 1;
const SUPPORT_ROOTS = new Set(["references", "templates", "scripts", "assets"]);

export const skillSchema = {
  type: "object", additionalProperties: false, required: ["skill"],
  properties: {
    skill: { type: "string", pattern: "^[a-z0-9]+(?:-[a-z0-9]+)*$", maxLength: 64 },
    args: { type: "string" },
  },
} as const;

export const skillManageSchema = {
  type: "object", additionalProperties: false, required: ["action"],
  properties: {
    action: { type: "string", enum: ["create", "patch", "view", "list", "read_file", "write_file", "absorb_files", "review", "history", "undo", "archive", "pin", "unpin", "metrics"] },
    name: { type: "string", pattern: "^[a-z0-9]+(-[a-z0-9]+)*$", maxLength: 64 }, description: { type: "string" }, instructions: { type: "string" },
    append: { type: "boolean" }, tags: { type: "string" }, category: { type: "string" }, file_path: { type: "string" }, file_content: { type: "string" },
    review_reason: { type: "string" }, pruning_reason: { type: "string" }, absorbed_into: { type: "string" }, dropped_files: { type: "string" }, from_skill: { type: "string" }, file_paths: { type: "string" }, expected_revision: { type: "string" }, revision: { type: "string" }, pinned: { type: "boolean" }, offset: { type: "integer", minimum: 0 }, limit: { type: "integer", minimum: 1 },
  },
} as const;

const safeName = (n: unknown) => typeof n === "string" && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(n) && n.length <= 64 && n !== "archive";
const now = () => new Date().toISOString();
const clone = <T>(x: T): T => structuredClone(x);
const pathExists = (path: string) => { try { lstatSync(path); return true; } catch (error: any) { if (error?.code === "ENOENT") return false; throw error; } };
const sleepBuffer = new Int32Array(new SharedArrayBuffer(4));
const sleepSync = (milliseconds: number) => Atomics.wait(sleepBuffer, 0, 0, milliseconds);
type LockOwner = { pid: number; token: string; createdAt: string; processStart?: string };
type HeldLock = { token: string; depth: number };
const LOCKS = Symbol.for("pi-swarm-autogen-held-locks");
const lockRoot = globalThis as typeof globalThis & { [LOCKS]?: Map<string, HeldLock> };
const heldLocks = lockRoot[LOCKS] ?? (lockRoot[LOCKS] = new Map<string, HeldLock>());
const CURATOR_RUNS = Symbol.for("pi-swarm-autogen-curator-runs");
const curatorRunRoot = globalThis as typeof globalThis & { [CURATOR_RUNS]?: Map<string, Promise<any>> };
const curatorRuns = curatorRunRoot[CURATOR_RUNS] ?? (curatorRunRoot[CURATOR_RUNS] = new Map<string, Promise<any>>());
function processStart(pid: number): string | undefined {
  if (process.platform !== "linux") return;
  try {
    const stat = readFileSync(`/proc/${pid}/stat`, "utf8");
    const fields = stat.slice(stat.lastIndexOf(")") + 2).trim().split(/\s+/);
    return fields[19];
  } catch { return; }
}
function ownerAlive(owner: LockOwner) {
  if (!Number.isInteger(owner.pid) || owner.pid <= 0) return false;
  try { process.kill(owner.pid, 0); }
  catch (error: any) { return error?.code === "EPERM"; }
  const actualStart = processStart(owner.pid);
  return !owner.processStart || !actualStart || owner.processStart === actualStart;
}

export class AutoSkillManager {
  private state: State = defaultState();
  private readonly viewed = new Set<string>();
  private curatorRunning?: Promise<any>;
  readonly config: Required<Pick<Config, "mode" | "dir" | "toolCallThreshold" | "errorResolutionThreshold" | "nudgeInterval" | "minInstructionsLength" | "toolCallBudget" | "workingBudget" | "maxNudgeIgnores" | "staleAfterDays" | "archiveAfterDays" | "lockTimeoutMs" | "previewOnly" | "requireReadBeforeWrite" | "curatorMinRunGapMs" | "curatorIdleDelayMs" | "curatorConsolidate" | "curatorTimeoutMs" | "curatorMaxTurns" | "accountingExempt">> & { reviewHook?: ReviewHook; curatorRunner?: CuratorRunner; protectSkill?: (name: string) => boolean };
  constructor(config: Config = {}, private readonly persist?: (entry: SkillEntry) => void) {
    const home = process.env.HOME ?? process.cwd();
    const mode = config.mode ?? (process.env.SWARM_AUTOGEN_MODE as Mode) ?? "never";
    if (!["never", "manual", "auto"].includes(mode)) throw new Error(`invalid autogen mode: ${mode}`);
    this.config = {
      mode, dir: config.dir ?? process.env.SWARM_AUTOGEN_DIR ?? join(home, ".swarm", "skills", "autogen"),
      toolCallThreshold: config.toolCallThreshold ?? 15, errorResolutionThreshold: config.errorResolutionThreshold ?? 1,
      nudgeInterval: Math.max(1, config.nudgeInterval ?? 15), minInstructionsLength: config.minInstructionsLength ?? 200,
      toolCallBudget: config.toolCallBudget ?? 5, workingBudget: config.workingBudget ?? 90,
      maxNudgeIgnores: config.maxNudgeIgnores ?? 3, staleAfterDays: config.staleAfterDays ?? 30,
      archiveAfterDays: config.archiveAfterDays ?? 90, lockTimeoutMs: Math.max(10, config.lockTimeoutMs ?? 5000),
      previewOnly: config.previewOnly ?? false, requireReadBeforeWrite: config.requireReadBeforeWrite ?? false,
      curatorMinRunGapMs: Math.max(1, config.curatorMinRunGapMs ?? 24 * 60 * 60 * 1000),
      curatorIdleDelayMs: Math.max(1, config.curatorIdleDelayMs ?? 60 * 1000),
      curatorConsolidate: config.curatorConsolidate ?? false, curatorTimeoutMs: Math.max(1000, config.curatorTimeoutMs ?? 5 * 60 * 1000),
      curatorMaxTurns: Math.max(1, config.curatorMaxTurns ?? 8), reviewHook: config.reviewHook,
      curatorRunner: config.curatorRunner, protectSkill: config.protectSkill, accountingExempt: config.accountingExempt ?? false,
    };
    this.mergeCuratorState();
  }
  snapshot(): State { return clone(this.state); }
  restore(state: State) { this.state = { ...defaultState(), ...clone(state), skills: { ...(state.skills ?? {}) }, nudges: state.nudges ?? 0, nudgeIgnores: state.nudgeIgnores ?? 0, mutations: state.mutations ?? 0 }; }
  metrics(): Metrics { return { turns: this.state.turns, toolCalls: this.state.toolCalls, errors: this.state.errors, resolved: this.state.resolved, nudges: this.state.nudges, nudgeIgnores: this.state.nudgeIgnores, reviews: this.state.reviews, mutations: this.state.mutations, skilled: this.state.skilled, budgetCalls: this.state.budgetCalls, reviewRequired: this.state.reviewRequired }; }
  rehydrate(entries: readonly unknown[]) { this.state = defaultState(); const e = [...entries].reverse().find((x: any) => x?.type === "pi-swarm-autogen-state" || x?.type === "custom" && x?.customType === "pi-swarm-autogen-state") as SkillEntry | undefined; if (e?.data) this.restore(e.data); this.mergeCuratorState(); }
  private commit() { this.persist?.({ type: "pi-swarm-autogen-state", data: this.snapshot() }); }
  private curatorStatePath() { return join(this.config.dir, ".history", "curator-state.json"); }
  private mergeCuratorState() {
    const path = this.curatorStatePath();
    if (!pathExists(path)) return;
    try {
      const disk = JSON.parse(readFileSync(path, "utf8")) as Pick<State, "skills" | "curatorLastRun" | "curatorLastReport">;
      for (const [name, meta] of Object.entries(disk.skills ?? {})) {
        this.state.skills[name] = { ...(this.state.skills[name] ?? { version: meta.version, uses: 0 }), ...meta };
      }
      if (disk.curatorLastRun) this.state.curatorLastRun = disk.curatorLastRun;
      if (disk.curatorLastReport) this.state.curatorLastReport = disk.curatorLastReport;
    } catch { throw new Error("invalid persisted curator state"); }
  }
  private persistCuratorState() {
    this.safePath(this.config.dir, ".history/curator-state.json");
    mkdirSync(join(this.config.dir, ".history"), { recursive: true, mode: 0o755 });
    const path = this.curatorStatePath(), temporary = `${path}.${process.pid}.${Date.now()}.tmp`;
    const data = { skills: this.state.skills, curatorLastRun: this.state.curatorLastRun, curatorLastReport: this.state.curatorLastReport };
    writeFileSync(temporary, `${JSON.stringify(data, null, 2)}\n`, { mode: 0o600 });
    renameSync(temporary, path);
  }
  private dir(name: string) { return join(this.config.dir, name); }
  private file(name: string) { return join(this.dir(name), "SKILL.md"); }
  private packageRoot(name: string) {
    const active = this.dir(name);
    const archiveRoot = join(this.config.dir, "archive");
    const legacy = new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}-\\d+$`);
    if (pathExists(archiveRoot) && lstatSync(archiveRoot).isSymbolicLink()) throw new Error("symlink rejected for archive placement");
    const archived = pathExists(archiveRoot)
      ? readdirSync(archiveRoot).filter(entry => entry === name || legacy.test(entry)).sort().map(entry => join(archiveRoot, entry))
      : [];
    if (pathExists(active) && lstatSync(active).isSymbolicLink()) throw new Error(`symlink rejected for skill package: ${name}`);
    for (const path of archived) if (lstatSync(path).isSymbolicLink()) throw new Error(`symlink rejected for archived skill package: ${name}`);
    if (pathExists(active) && archived.length) throw new Error(`skill ${name} exists in both active and archived placement`);
    if (archived.length > 1) throw new Error(`skill ${name} has multiple archived placements`);
    return pathExists(active) ? active : archived[0];
  }
  private parse(name: string): Skill {
    const root = this.packageRoot(name);
    if (!root || root !== this.dir(name)) throw new Error(`active skill ${name} does not exist`);
    const content = readFileSync(join(root, "SKILL.md"), "utf8"), match = content.match(/^---\n([\s\S]*?)\n---\n?([\s\S]*)$/);
    if (!match) throw new Error(`invalid SKILL.md for ${name}`);
    const fields: Record<string,string> = {}; for (const line of match[1].split("\n")) { const i = line.indexOf(":"); if (i > 0) fields[line.slice(0,i).trim()] = line.slice(i+1).trim(); }
    return { name: fields.name || name, description: fields.description || "", instructions: match[2].trimEnd(), tags: fields.tags ? fields.tags.split(",").map(x=>x.trim()).filter(Boolean) : [], category: fields.category || undefined, version: fields.version || "1.0.0", path: root, updatedAt: fields.updated_at || now() };
  }
  private body(skill: Skill) { return `---\nname: ${skill.name}\ndescription: ${skill.description}\nversion: ${skill.version}\n${skill.tags.length ? `tags: ${skill.tags.join(", ")}\n` : ""}${skill.category ? `category: ${skill.category}\n` : ""}updated_at: ${skill.updatedAt}\n---\n\n${skill.instructions}\n`; }
  private hashBytes(data: string | Buffer) { return createHash("sha256").update(data).digest("hex"); }
  private packageFiles(name: string): Record<string, string> {
    const out: Record<string,string> = {};
    const root = this.packageRoot(name);
    if (!root) return out;
    const walk = (directory: string, prefix = "") => {
      for (const e of readdirSync(directory, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
        const p = join(directory, e.name), rel = prefix ? `${prefix}/${e.name}` : e.name;
        const info = lstatSync(p);
        if (info.isSymbolicLink()) throw new Error(`symlink rejected in skill package: ${rel}`);
        if (info.isDirectory()) walk(p, rel);
        else if (info.isFile()) out[rel] = this.hashBytes(readFileSync(p));
        else throw new Error(`unsupported filesystem entry in skill package: ${rel}`);
      }
    };
    walk(root);
    return out;
  }
  private copyPackage(source: string, destination: string) {
    mkdirSync(destination, { recursive: true, mode: 0o755 });
    const walk = (from: string, to: string, prefix = "") => {
      for (const entry of readdirSync(from, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
        const src = join(from, entry.name), dst = join(to, entry.name), rel = prefix ? `${prefix}/${entry.name}` : entry.name;
        const info = lstatSync(src);
        if (info.isSymbolicLink()) throw new Error(`symlink rejected in skill package: ${rel}`);
        if (info.isDirectory()) { mkdirSync(dst, { mode: info.mode & 0o777 }); walk(src, dst, rel); }
        else if (info.isFile()) writeFileSync(dst, readFileSync(src), { mode: info.mode & 0o777 });
        else throw new Error(`unsupported filesystem entry in skill package: ${rel}`);
      }
    };
    walk(source, destination);
  }
  private supportFiles(name: string) { return Object.keys(this.packageFiles(name)).filter(p => SUPPORT_ROOTS.has(p.split("/")[0])); }
  private snapshotRevision(name: string, action: string, parent?: string): Revision {
    const root = this.packageRoot(name);
    if (!root) throw new Error(`skill ${name} does not exist`);
    const files = this.packageFiles(name), blobs: Record<string,string> = {};
    for (const path of Object.keys(files)) blobs[path] = readFileSync(join(root, path)).toString("base64");
    const canonical = JSON.stringify({ format: HISTORY_FORMAT, skill: name, parent: parent ?? "", action, files });
    return { id: this.hashBytes(canonical), parent, action, createdAt: now(), files, blobs };
  }
  private revisionPath(name: string, id: string) { return join(this.config.dir, ".history", "revisions", name, `${id}.json`); }
  private loadRevision(name: string, id: string): StoredRevision {
    if (!/^[a-f0-9]{64}$/.test(id)) throw new Error("untrusted revision identifier");
    const path = this.revisionPath(name, id);
    this.safePath(this.config.dir, `.history/revisions/${name}/${id}.json`);
    if (!existsSync(path) || lstatSync(path).isSymbolicLink()) throw new Error("untrusted revision manifest");
    const stored = JSON.parse(readFileSync(path, "utf8")) as Revision & { format?: number };
    if (stored.format !== HISTORY_FORMAT || stored.id !== id || !stored.files || !stored.blobs) throw new Error("untrusted revision manifest");
    const canonical = JSON.stringify({ format: HISTORY_FORMAT, skill: name, parent: stored.parent ?? "", action: stored.action, files: stored.files });
    if (this.hashBytes(canonical) !== id) throw new Error("untrusted revision manifest");
    const blobFiles = Object.fromEntries(Object.entries(stored.blobs).map(([path, value]) => {
      if (path !== "SKILL.md" && !SUPPORT_ROOTS.has(path.split("/")[0]) ||
        path.includes("\\") || path.split("/").some(part => !part || part === "." || part === "..") ||
        typeof value !== "string") throw new Error("untrusted revision path");
      return [path, this.hashBytes(Buffer.from(value, "base64"))];
    }));
    if (JSON.stringify(stored.files) !== JSON.stringify(blobFiles)) throw new Error("untrusted revision manifest");
    return stored as StoredRevision;
  }
  private saveRevision(name: string, action: string, parent?: string) {
    const rev = this.snapshotRevision(name, action, parent);
    const root = join(this.config.dir, ".history", "revisions", name);
    this.safePath(this.config.dir, `.history/revisions/${name}`);
    mkdirSync(root, { recursive: true, mode: 0o755 });
    try { writeFileSync(this.revisionPath(name, rev.id), JSON.stringify({ format: HISTORY_FORMAT, ...rev }, null, 2) + "\n", { flag: "wx", mode: 0o444 }); }
    catch (e: any) { if (e?.code !== "EEXIST") throw e; this.loadRevision(name, rev.id); }
    this.safePath(this.config.dir, ".history/heads");
    mkdirSync(join(this.config.dir, ".history", "heads"), { recursive: true });
    const head = join(this.config.dir, ".history", "heads", name), temporary = `${head}.${process.pid}.${Date.now()}.tmp`;
    writeFileSync(temporary, rev.id + "\n", { mode: 0o644 });
    renameSync(temporary, head);
    return rev;
  }
  private diskHead(name: string) {
    const hp = join(this.config.dir, ".history", "heads", name);
    this.safePath(this.config.dir, `.history/heads/${name}`);
    if (!pathExists(hp)) return this.saveRevision(name, "external").id;
    if (lstatSync(hp).isSymbolicLink()) throw new Error("untrusted history HEAD");
    const id = readFileSync(hp, "utf8").trim();
    const rev = this.loadRevision(name, id);
    if (JSON.stringify(rev.files) !== JSON.stringify(this.packageFiles(name))) throw new Error("disk package does not match history HEAD");
    return id;
  }
  private safePath(root: string, value: string) {
    const p = resolve(root, value), r = relative(root, p);
    if (r === ".." || r.startsWith("../") || r.startsWith("..\\")) throw new Error("path escapes skill package");
    if (pathExists(root) && lstatSync(root).isSymbolicLink()) throw new Error("symlinks are not permitted");
    let cur = root;
    for (const part of r.split(/[\\/]/).filter(Boolean)) { cur = join(cur, part); if (pathExists(cur) && lstatSync(cur).isSymbolicLink()) throw new Error("symlinks are not permitted"); }
    return p;
  }
  private lockOwner(path: string): LockOwner | undefined {
    try { return JSON.parse(readFileSync(join(path, "owner.json"), "utf8")) as LockOwner; }
    catch { return; }
  }
  private reapStaleLock(path: string) {
    let age = 0;
    try { age = Date.now() - lstatSync(path).mtimeMs; } catch { return true; }
    const owner = this.lockOwner(path);
    if (owner && ownerAlive(owner)) return false;
    if (!owner && age < 1000) return false;
    const reaper = join(path, "reap");
    try { writeFileSync(reaper, `${process.pid}\n`, { flag: "wx", mode: 0o600 }); }
    catch (error: any) { return error?.code === "ENOENT"; }
    const confirmed = this.lockOwner(path);
    if (confirmed && ownerAlive(confirmed)) {
      try { rmSync(reaper, { force: true }); } catch { /* another owner is authoritative */ }
      return false;
    }
    rmSync(path, { recursive: true, force: true });
    return true;
  }
  private acquireSkillLock(name: string) {
    const root = join(this.config.dir, ".history", "locks");
    mkdirSync(this.config.dir, { recursive: true, mode: 0o755 });
    this.safePath(this.config.dir, ".history/locks");
    mkdirSync(root, { recursive: true, mode: 0o755 });
    const path = join(root, `${name}.lock`);
    const held = heldLocks.get(path);
    if (held) { held.depth++; return { path, token: held.token }; }
    const deadline = Date.now() + this.config.lockTimeoutMs;
    for (;;) {
      const token = randomUUID();
      try {
        mkdirSync(path, { mode: 0o700 });
        const owner: LockOwner = { pid: process.pid, token, createdAt: now(), processStart: processStart(process.pid) };
        writeFileSync(join(path, "owner.json"), `${JSON.stringify(owner)}\n`, { flag: "wx", mode: 0o600 });
        heldLocks.set(path, { token, depth: 1 });
        return { path, token };
      } catch (error: any) {
        if (error?.code !== "EEXIST") {
          if (pathExists(path) && !this.lockOwner(path)) rmSync(path, { recursive: true, force: true });
          throw error;
        }
        if (this.reapStaleLock(path)) continue;
        if (Date.now() >= deadline) throw new Error(`timed out waiting for skill mutation lock: ${name}`);
        sleepSync(Math.min(10, Math.max(1, deadline - Date.now())));
      }
    }
  }
  private releaseSkillLock(lock: { path: string; token: string }) {
    const held = heldLocks.get(lock.path);
    if (!held || held.token !== lock.token) throw new Error("skill mutation lock ownership changed");
    held.depth--;
    if (held.depth > 0) return;
    const owner = this.lockOwner(lock.path);
    if (!owner || owner.token !== lock.token) throw new Error("skill mutation lock ownership changed");
    heldLocks.delete(lock.path);
    rmSync(lock.path, { recursive: true });
  }
  private withSkillLocks<T>(names: string[], operation: () => T): T {
    const locks: Array<{ path: string; token: string }> = [];
    try {
      for (const name of [...new Set(names)].sort()) locks.push(this.acquireSkillLock(name));
      return operation();
    } finally {
      for (const lock of locks.reverse()) this.releaseSkillLock(lock);
    }
  }
  private mutateActivePackage(name: string, action: string, parent: string | undefined, create: boolean, mutate: (stage: string) => void) {
    const root = this.dir(name), stamp = `${process.pid}-${Date.now()}`;
    const stage = `${root}.stage-${stamp}`, backup = `${root}.backup-${stamp}`;
    this.safePath(this.config.dir, name);
    if (create) mkdirSync(stage, { recursive: true, mode: 0o755 });
    else this.copyPackage(root, stage);
    try {
      mutate(stage);
      if (create) renameSync(stage, root);
      else {
        renameSync(root, backup);
        try { renameSync(stage, root); }
        catch (error) { renameSync(backup, root); throw error; }
      }
      try {
        const revision = this.saveRevision(name, action, parent);
        rmSync(backup, { recursive: true, force: true });
        return revision;
      } catch (error) {
        rmSync(root, { recursive: true, force: true });
        if (!create && pathExists(backup)) renameSync(backup, root);
        throw error;
      }
    } finally {
      rmSync(stage, { recursive: true, force: true });
    }
  }
  private write(skill: Skill, action: string, parent?: string) {
    const create = action === "create";
    const rev = this.mutateActivePackage(skill.name, action, parent, create, stage => {
      writeFileSync(join(stage, "SKILL.md"), this.body(skill), { mode: 0o644 });
    });
    const old = this.state.skills[skill.name];
    this.state.skills[skill.name] = { ...(old ?? { uses: 0 }), version: skill.version, hash: rev.id, lastUsed: now(), curatorState: old?.pinned ? "pinned" : "active", pinned: old?.pinned ?? false };
    this.state.mutations++; this.config.reviewHook?.({ action, name: skill.name, revision: rev.id });
  }
  private assertEnabled() { if (this.config.mode === "never") throw new Error("autogenerated skills are disabled (mode=never)"); }
  private assertMutationAllowed(action: string) { if (this.config.mode !== "auto") return; if (action === "review") return; const budget = this.state.skilled ? this.config.workingBudget : this.config.toolCallBudget; if (this.state.reviewRequired) throw new Error("autogen review required before mutation; call SkillManage(action=\"review\")"); if (this.state.budgetCalls >= budget) { if (!this.state.skilled) throw new Error(`autogen onboarding budget exceeded (${budget}); create or use a skill`); if (this.state.nudgeIgnores >= this.config.maxNudgeIgnores) { this.state.reviewRequired = true; throw new Error("autogen review required before mutation; call SkillManage(action=\"review\")"); } } }
  execute(input: any): any {
    this.assertEnabled(); const action = input?.action;
    if (action === "list") return { skills: this.list() };
    if (!safeName(input?.name) && !["review", "metrics"].includes(action)) throw new Error("name must match lowercase skill identifier syntax");
    const lockActions = new Set(["create", "patch", "view", "read_file", "write_file", "absorb_files", "history", "undo", "archive", "pin", "unpin"]);
    if (lockActions.has(action)) {
      const names = [String(input.name)];
      if (action === "absorb_files" && safeName(input.from_skill)) names.push(String(input.from_skill));
      if (action === "archive" && safeName(input.absorbed_into)) names.push(String(input.absorbed_into));
      const stateful = !["read_file", "history"].includes(action);
      if (stateful) names.push("curator-state");
      return this.withSkillLocks(names, () => {
        if (stateful) this.mergeCuratorState();
        const result = this.executeLocked(input);
        if (stateful && !this.config.previewOnly) this.persistCuratorState();
        return result;
      });
    }
    return this.executeLocked(input);
  }
  private executeLocked(input: any): any {
    const action = input?.action;
    const mutators = new Set(["create", "patch", "write_file", "absorb_files", "undo", "archive", "pin", "unpin"]);
    if (this.config.previewOnly && mutators.has(action)) throw new Error(`curator preview is read-only; ${action} is denied`);
    const existingMutators = new Set(["patch", "write_file", "absorb_files", "undo", "archive"]);
    if (!input.curator_internal && this.config.requireReadBeforeWrite && existingMutators.has(action)) {
      const name = String(input.name ?? "");
      const meta = this.state.skills[name];
      if (meta?.pinned || meta?.curatorState === "pinned") throw new Error(`skill ${name} is pinned; autonomous review cannot modify it`);
      if (!this.viewed.has(name)) throw new Error(`skill ${name} must be viewed before autonomous modification`);
    }
    if (action === "review") {
      const reason = String(input.review_reason ?? "").trim();
      if (!reason) throw new Error("review requires a non-empty review_reason");
      this.state.reviews++; this.state.reviewRequired = false; this.state.nudgeIgnores = 0;
      this.config.reviewHook?.({ action, reason }); this.commit(); return { reviewed: true, reason };
    }
    if (action === "metrics") return { metrics: this.metrics() };
    const name = input.name as string;
    if (action === "pin" || action === "unpin") { const skill = this.parse(name); const old = this.state.skills[name] ?? { version: skill.version, uses: 0 }; old.pinned = action === "pin"; old.curatorState = old.pinned ? "pinned" : "active"; this.state.skills[name] = old; this.config.reviewHook?.({ action, name, revision: old.hash }); this.commit(); return { name, pinned: old.pinned, state: old.curatorState }; }
    if (action === "create") { this.assertMutationAllowed(action); this.safePath(this.config.dir, name); if (pathExists(this.dir(name))) throw new Error(`skill ${name} already exists; view and patch it instead`); if (!input.description || !input.instructions) throw new Error("description and instructions are required"); if (input.instructions.length < this.config.minInstructionsLength) throw new Error(`instructions must contain at least ${this.config.minInstructionsLength} characters`); const s: Skill = { name, description: input.description, instructions: input.instructions, tags: String(input.tags ?? "").split(",").map((x:string)=>x.trim()).filter(Boolean), category: input.category, version: "1.0.0", path: this.dir(name), updatedAt: now() }; this.write(s, "create"); this.commit(); return { skill: s, revision: this.state.skills[name].hash, expected_revision: this.state.skills[name].hash }; }
    if (action === "view") {
      const s = this.parse(name), offset = input.offset ?? 0, limit = input.limit ?? 80000;
      const hash = this.diskHead(name);
      this.viewed.add(name);
      if (!this.config.previewOnly) {
        this.state.skills[name] = { ...(this.state.skills[name] ?? { uses: 0 }), version: s.version, hash, lastUsed: now() };
        this.commit();
      }
      return { skill: { ...s, instructions: [...s.instructions].slice(offset, offset + limit) }, revision: hash, expected_revision: hash, version: s.version };
    }
    if (action === "patch") { this.assertMutationAllowed(action); if (this.config.mode !== "auto" && this.config.mode !== "manual") throw new Error("disabled"); const s = this.parse(name); const currentRevision = this.diskHead(name); if (!input.expected_revision) throw new Error(`expected_revision is required for existing skill ${name}`); if (input.expected_revision !== currentRevision) throw new Error(`revision conflict: expected ${input.expected_revision}, current ${currentRevision}`); s.instructions = input.append ? `${s.instructions}\n\n${input.instructions ?? ""}` : (input.instructions || s.instructions); if (input.description) s.description = input.description; if (input.tags) s.tags = [...new Set([...s.tags, ...String(input.tags).split(",").map(x=>x.trim())])]; const parts = s.version.split("."); s.version = parts.length === 3 && /^\d+$/.test(parts[2]) ? `${parts[0]}.${parts[1]}.${Number(parts[2]) + 1}` : s.version; s.updatedAt = now(); this.write(s, "patch", currentRevision); this.commit(); return { skill: s, revision: this.state.skills[name].hash, version: s.version }; }
    if (action === "absorb_files") {
      this.assertMutationAllowed(action);
      const from = String(input.from_skill ?? ""), source = this.dir(from), destination = this.dir(name);
      if (!safeName(from) || from === name || !existsSync(source)) throw new Error("from_skill must name an existing, different skill");
      const current = this.diskHead(name);
      if (!input.expected_revision) throw new Error(`expected_revision is required for existing skill ${name}`);
      if (input.expected_revision !== current) throw new Error(`revision conflict: expected ${input.expected_revision}, current ${current}`);
      const requested = input.file_paths ? String(input.file_paths).split(",").map((x: string) => x.trim()).filter(Boolean) : this.supportFiles(from);
      const plans: { p: string, src: string, dst: string }[] = [];
      for (const raw of requested) {
        const p = raw.replaceAll("\\", "/"), first = p.split("/")[0], src = resolve(source, p), dst = resolve(destination, p);
        if (!SUPPORT_ROOTS.has(first) || !existsSync(src) || !lstatSync(src).isFile()) throw new Error(`invalid support file: ${raw}`);
        this.safePath(source, p); this.safePath(destination, p); plans.push({ p, src, dst });
      }
      const stage = `${destination}.absorb-${process.pid}-${Date.now()}`, backup = `${destination}.absorb-backup-${process.pid}-${Date.now()}`;
      const copied: string[] = [];
      try {
        this.copyPackage(destination, stage);
        for (const x of plans) {
          const q = this.safePath(stage, x.p);
          mkdirSync(resolve(q, ".."), { recursive: true });
          const data = readFileSync(x.src);
          writeFileSync(q, data);
          if (this.hashBytes(data) !== this.hashBytes(readFileSync(q))) throw new Error(`hash verification failed for ${x.p}`);
          copied.push(x.p);
        }
        renameSync(destination, backup);
        try { renameSync(stage, destination); }
        catch (error) { renameSync(backup, destination); throw error; }
      } catch (error) {
        rmSync(stage, { recursive: true, force: true });
        throw error;
      }
      let rev: Revision;
      try { rev = this.saveRevision(name, "absorb_files", current); }
      catch (error) {
        rmSync(destination, { recursive: true, force: true });
        renameSync(backup, destination);
        throw error;
      }
      rmSync(backup, { recursive: true, force: true });
      this.state.skills[name] = { ...(this.state.skills[name] ?? { version: this.parse(name).version, uses: 0 }), hash: rev.id, lastUsed: now() };
      this.state.mutations++; this.commit();
      return { absorbed: copied.length, files: copied, revision: rev.id };
    }
    if (action === "archive") {
      if (!input.curator_internal) this.assertMutationAllowed(action);
      const meta = this.state.skills[name]; if (meta?.pinned || meta?.curatorState === "pinned") throw new Error(`skill ${name} is pinned`);
      const expected = String(input.expected_revision ?? ""); const current = this.diskHead(name);
      if (!expected || expected !== current) throw new Error(`expected_revision is required and must match current revision ${current}`);
      const sourceFiles = this.supportFiles(name), absorbedInto = input.absorbed_into ? String(input.absorbed_into) : "";
      if (absorbedInto && !safeName(absorbedInto)) throw new Error("absorbed_into must name a valid skill");
      const dropped = String(input.dropped_files ?? "").split(",").map((x: string) => x.trim()).filter(Boolean);
      const reason = String(input.pruning_reason ?? "").trim();
      if (!absorbedInto && !reason) throw new Error("archive requires absorbed_into or nonblank pruning_reason");
      if (absorbedInto && (!existsSync(this.file(absorbedInto)) || absorbedInto === name)) throw new Error("absorbed_into must name a different existing skill");
      if (sourceFiles.length && absorbedInto) {
        const targetFiles = this.supportFiles(absorbedInto);
        const missing = sourceFiles.filter(p => !targetFiles.includes(p) || this.hashBytes(readFileSync(join(this.dir(name), p))) !== this.hashBytes(readFileSync(join(this.dir(absorbedInto), p))));
        if (missing.some(p => !dropped.includes(p))) throw new Error(`archive refused: support files not preserved; use absorb_files first (${missing.join(", ")})`);
      }
      if (dropped.length && !reason) throw new Error("dropped_files requires pruning_reason");
      const s = this.parse(name), target = join(this.config.dir, "archive", name);
      this.safePath(this.config.dir, `archive/${name}`);
      if (pathExists(target)) throw new Error(`archive placement already exists for ${name}`);
      mkdirSync(join(this.config.dir, "archive"), { recursive: true });
      renameSync(s.path, target);
      let provenance: Revision;
      try { provenance = this.saveRevision(name, "archive", current); }
      catch (error) { renameSync(target, s.path); throw error; }
      this.state.skills[name] = { ...(meta ?? { version: s.version, uses: 0 }), archived: true, curatorState: "archived", absorbedInto, archiveReason: reason, hash: provenance.id };
      this.state.mutations++;
      this.config.reviewHook?.({ action, name, revision: provenance.id, reason: reason || `absorbed into ${absorbedInto}` });
      this.commit(); return { archived: name, path: target, absorbed_into: absorbedInto, pruning_reason: input.pruning_reason, revision: provenance.id };
    }
    if (action === "history") {
      const headPath = join(this.config.dir, ".history", "heads", name);
      this.safePath(this.config.dir, `.history/heads/${name}`);
      if (!existsSync(headPath)) return { revisions: [] };
      const revisions: Revision[] = [], seen = new Set<string>();
      let cursor = readFileSync(headPath, "utf8").trim();
      while (cursor) {
        if (seen.has(cursor)) throw new Error("untrusted revision history cycle");
        seen.add(cursor);
        const revision = this.loadRevision(name, cursor);
        revisions.push(revision);
        cursor = revision.parent ?? "";
      }
      return { revisions: revisions.map(r => ({ id: r.id, parent: r.parent, action: r.action, createdAt: r.createdAt, files: r.files })) };
    }
    if (action === "undo") {
      this.assertMutationAllowed(action);
      const id = String(input.revision ?? ""), expected = String(input.expected_revision ?? "");
      if (!/^[a-f0-9]{64}$/.test(id)) throw new Error("undo requires a valid revision hash");
      const p = this.revisionPath(name, id); if (!existsSync(p)) throw new Error(`revision ${id} not found`);
      const root = this.dir(name);
      const currentRoot = this.packageRoot(name);
      if (!currentRoot) throw new Error(`skill ${name} does not exist`);
      const current = this.diskHead(name); if (!expected || expected !== current) throw new Error(`expected_revision is required and must match current revision ${current}`);
      let cursor = current, ancestor = false;
      while (cursor) { if (cursor === id) { ancestor = true; break; } const cp = this.revisionPath(name, cursor); if (!existsSync(cp)) break; cursor = this.loadRevision(name, cursor).parent ?? ""; }
      if (!ancestor) throw new Error(`undo target ${id} is not an ancestor of current HEAD ${current}`);
      const rev = this.loadRevision(name, id);
      const stage = `${root}.undo-${process.pid}-${Date.now()}`, backup = `${root}.undo-backup-${process.pid}`;
      mkdirSync(stage, { recursive: true });
      try {
        for (const [path, encoded] of Object.entries(rev.blobs)) {
          if (path !== "SKILL.md" && !SUPPORT_ROOTS.has(path.split("/")[0]) || path.includes("\\") || path.split("/").some(x => !x || x === "." || x === "..")) throw new Error("untrusted revision path");
          const target = this.safePath(stage, path); mkdirSync(resolve(target, ".."), { recursive: true }); writeFileSync(target, Buffer.from(encoded, "base64"), { mode: 0o644 });
        }
        renameSync(currentRoot, backup);
        try { renameSync(stage, root); } catch (e) { renameSync(backup, currentRoot); throw e; }
      } catch (e) { rmSync(stage, { recursive: true, force: true }); throw e; }
      let s: Skill, next: Revision;
      try { s = this.parse(name); next = this.saveRevision(name, "undo", current); }
      catch (error) {
        rmSync(root, { recursive: true, force: true });
        renameSync(backup, currentRoot);
        throw error;
      }
      rmSync(backup, { recursive: true, force: true });
      this.state.skills[name] = { ...(this.state.skills[name] ?? { uses: 0 }), version: s.version, hash: next.id, lastUsed: now(), archived: false, curatorState: "active" };
      this.state.mutations++;
      this.config.reviewHook?.({ action, name, revision: next.id });
      this.commit(); return { restored: id, revision: next.id };
    }
    if (action === "read_file" || action === "write_file") {
      if (action === "write_file") this.assertMutationAllowed(action);
      const raw = String(input.file_path ?? ""), first = raw.replaceAll("\\", "/").split("/")[0];
      if (raw.includes("\\")) throw new Error("support paths must use forward slashes");
      const p = this.safePath(this.dir(name), raw);
      if (!SUPPORT_ROOTS.has(first)) throw new Error("support path must stay under references/, templates/, scripts/, or assets/");
      if (action === "read_file") return { path: p, content: readFileSync(p, "utf8"), revision: this.diskHead(name) };
      const current = this.diskHead(name);
      if (!input.expected_revision || input.expected_revision !== current) throw new Error(`expected_revision is required and must match current revision ${current}`);
      const rev = this.mutateActivePackage(name, "write_file", current, false, stage => {
        const target = this.safePath(stage, raw);
        mkdirSync(resolve(target, ".."), { recursive: true });
        writeFileSync(target, input.file_content ?? "", { mode: 0o644 });
      });
      this.state.skills[name] = { ...(this.state.skills[name] ?? { uses: 0 }), version: this.parse(name).version, hash: rev.id, lastUsed: now() };
      this.state.mutations++;
      this.config.reviewHook?.({ action, name, revision: rev.id });
      this.commit(); return { path: p, written: true, revision: rev.id };
    }
    throw new Error(`unknown action ${action}`);
  }
  list(): Skill[] { if (!existsSync(this.config.dir)) return []; return readdirSync(this.config.dir, { withFileTypes: true }).filter((e) => e.isDirectory() && safeName(e.name) && existsSync(this.file(e.name))).map((e) => this.parse(e.name)); }
  observeTool(success: boolean, toolName?: string, input?: any) {
    // The SDK charges the budget at before-execute time, including calls that
    // later fail. Pi exposes the result here, so charge every observed
    // non-exempt attempt and persist failures as well.
    this.state.toolCalls++;
    const normalized = String(toolName ?? "").toLowerCase();
    const operations = Array.isArray(input?.operations) ? input.operations : [input];
    if (success && normalized.replace(/[^a-z0-9]/g, "") === "taskmanage" && operations.some((operation: any) => operation?.status === "in_progress" || operation?.active === true || operation?.focused === true)) this.state.focusedTask = true;
    if (this.state.focusedTask && !this.isExempt(toolName, input)) this.state.budgetCalls++;
    if (success) {
      if (this.state.errors > this.state.resolved) this.state.resolved++;
    } else this.state.errors++;
    const skillName = input?.name ?? input?.skill ?? input?.skill_name;
    if (success && toolName && /^(skill|skillmanage|swarmskill)$/i.test(toolName)) {
      this.withSkillLocks(["curator-state"], () => {
        this.mergeCuratorState();
        // SwarmSkill may come from the general loader rather than autogen's
        // registry. Create metadata before recording usage so observer hooks are
        // never able to crash on an unregistered skill.
        if (typeof skillName === "string") {
          const entry = this.state.skills[skillName] ?? { version: "unknown", uses: 0 };
          entry.uses = (entry.uses ?? 0) + 1;
          entry.lastUsed = now();
          if (!entry.pinned) { entry.curatorState = "active"; entry.archived = false; }
          this.state.skills[skillName] = entry;
        }
        this.state.skilled = true;
        this.state.focusedTask = true;
        this.state.budgetCalls = 0;
        this.state.nudgeIgnores = 0;
        this.state.reviewRequired = false;
        this.persistCuratorState();
        this.commit();
      });
    } else this.commit();
  }
  /** Run the deterministic part of curator maintenance. It is deliberately
   * cadence-limited and never archives pinned skills. */
  curate(at = Date.now(), options: { force?: boolean; recordCadence?: boolean } = {}) {
    const cadence = this.withSkillLocks(["curator-state"], () => {
      this.mergeCuratorState();
      const last = this.state.curatorLastRun ? Date.parse(this.state.curatorLastRun) : 0;
      if (!options.force && !this.state.curatorLastRun) {
        this.state.curatorLastRun = new Date(at).toISOString();
        this.persistCuratorState();
        return "seeded";
      }
      return !options.force && last && at - last < this.config.curatorMinRunGapMs ? "skip" : "run";
    });
    if (cadence !== "run") {
      if (cadence === "seeded") this.commit();
      return { ran: false, archived: [] as string[], stale: [] as string[] };
    }
    const stale: string[] = [], archived: string[] = [];
    for (const name of Object.keys(this.state.skills)) {
      try {
        const outcome = this.withSkillLocks([name, "curator-state"], () => {
          this.mergeCuratorState();
          const meta = this.state.skills[name];
          if (!meta || meta.pinned || meta.curatorState === "pinned" || meta.archived || this.config.protectSkill?.(name))
            return "skip";
          const used = Date.parse(meta.lastUsed ?? "") || 0;
          const age = used ? (at - used) / 86400000 : 0;
          if (age < this.config.staleAfterDays) return "skip";
          meta.curatorState = "stale";
          if (age >= this.config.archiveAfterDays && existsSync(this.file(name))) {
            this.executeLocked({
              action: "archive",
              name,
              expected_revision: this.diskHead(name),
              pruning_reason: "stale automatic archive",
              curator_internal: true,
            });
            this.persistCuratorState();
            return "archived";
          }
          this.persistCuratorState();
          return "stale";
        });
        if (outcome === "archived") archived.push(name);
        else if (outcome === "stale") stale.push(name);
      } catch {
        this.withSkillLocks([name, "curator-state"], () => {
          this.mergeCuratorState();
          const meta = this.state.skills[name];
          if (!meta || meta.pinned || meta.curatorState === "pinned" || meta.archived || this.config.protectSkill?.(name)) return;
          meta.curatorState = "stale";
          this.persistCuratorState();
          if (!stale.includes(name)) stale.push(name);
        });
      }
    }
    this.withSkillLocks(["curator-state"], () => {
      this.mergeCuratorState();
      if (options.recordCadence !== false) this.state.curatorLastRun = new Date(at).toISOString();
      this.persistCuratorState();
    });
    this.commit();
    return { ran: true, archived, stale };
  }
  curatorPrompt(options: { preview?: boolean; consolidate?: boolean; at?: number } = {}) {
    const at = options.at ?? Date.now();
    const inventory = this.list().map(skill => {
      const meta = this.state.skills[skill.name] ?? { uses: 0 };
      const ageDays = meta.lastUsed ? Math.max(0, (at - Date.parse(meta.lastUsed)) / 86400000) : null;
      const recommendation = meta.pinned || this.config.protectSkill?.(skill.name) ? "none" :
        ageDays !== null && ageDays >= this.config.archiveAfterDays ? "archive" :
        ageDays !== null && ageDays >= this.config.staleAfterDays ? "mark_stale" : "none";
      return {
        name: skill.name, version: skill.version, description: skill.description,
        revision: meta.hash, uses: meta.uses, last_used_at: meta.lastUsed,
        state: meta.curatorState ?? "active", pinned: !!meta.pinned,
        support_files: this.supportFiles(skill.name), deterministic_recommendation: recommendation,
      };
    });
    return [
      "You are the Swarm autogenerated-skills curator subagent.",
      options.preview ? "PREVIEW MODE: analyze only. SkillManage mutations are denied." :
        "APPLY MODE: inspect before changing. Existing skills must be viewed before any mutation.",
      "Protect pinned, built-in, external, scheduled, and referenced skills. Treat every skill as a complete package.",
      "Prefer one class-level umbrella. Carry support files with absorb_files byte-for-byte before archiving a source.",
      "Never archive without absorbed_into or a specific pruning_reason. Use expected_revision from view for every existing-package mutation.",
      options.consolidate ? "Semantic consolidation is enabled. Merge only when two skills genuinely cover the same reusable class." :
        "Semantic consolidation is disabled. Report recommendations without merging skills.",
      "Return a final YAML report with exactly this shape:",
      "status: preview|applied|unchanged|failed",
      "actions:",
      "  - skill: <name>",
      "    action: none|patch|consolidate|mark_stale|archive",
      "    reason: <specific evidence>",
      "summary: <one sentence>",
      "Inventory:",
      JSON.stringify(inventory, null, 2),
    ].join("\n");
  }
  async runCurator(options: { preview?: boolean; consolidate?: boolean; automatic?: boolean; at?: number } = {}) {
    if (this.curatorRunning) return this.curatorRunning;
    const runKey = resolve(this.config.dir);
    const sharedRun = curatorRuns.get(runKey);
    if (sharedRun) return sharedRun;
    const run = async () => {
      let orchestrationLock: { path: string; token: string };
      try { orchestrationLock = this.acquireSkillLock("curator-run"); }
      catch (error) {
        if (error instanceof Error && error.message.includes("timed out waiting for skill mutation lock"))
          return { ran: false, reason: "already_running", preview: options.preview ?? false, consolidated: false };
        throw error;
      }
      try {
        const at = options.at ?? Date.now(), preview = options.preview ?? false;
        const consolidate = options.consolidate ?? this.config.curatorConsolidate;
        if (options.automatic) {
          const cadence = this.withSkillLocks(["curator-state"], () => {
            this.mergeCuratorState();
            const last = this.state.curatorLastRun ? Date.parse(this.state.curatorLastRun) : 0;
            if (!last) {
              this.state.curatorLastRun = new Date(at).toISOString();
              this.persistCuratorState();
              return "seeded";
            }
            return at - last < this.config.curatorMinRunGapMs ? "skip" : "run";
          });
          if (cadence === "seeded") {
            this.commit();
            return { ran: false, reason: "cadence_seeded", preview, consolidated: false };
          }
          if (cadence === "skip") return { ran: false, reason: "cadence", preview, consolidated: false };
        } else this.withSkillLocks(["curator-state"], () => this.mergeCuratorState());
        if (preview) {
          if (!this.config.curatorRunner) throw new Error("curator preview requires a configured curator runner");
          const before = JSON.stringify(this.list().map(skill => [skill.name, this.packageFiles(skill.name)]));
          const result = await this.config.curatorRunner({
            prompt: this.curatorPrompt({ preview: true, consolidate, at }), preview: true, consolidate,
            timeoutMs: this.config.curatorTimeoutMs, maxTurns: this.config.curatorMaxTurns,
          });
          const after = JSON.stringify(this.list().map(skill => [skill.name, this.packageFiles(skill.name)]));
          if (before !== after) throw new Error("curator preview mutated the skill library");
          return { ran: true, preview: true, consolidated: false, output: result.output };
        }
        let output = "";
        if (consolidate) {
          if (!this.config.curatorRunner) throw new Error("curator consolidation requires a configured curator runner");
          const result = await this.config.curatorRunner({
            prompt: this.curatorPrompt({ preview: false, consolidate: true, at }), preview: false, consolidate: true,
            timeoutMs: this.config.curatorTimeoutMs, maxTurns: this.config.curatorMaxTurns,
          });
          output = result.output;
          this.withSkillLocks(["curator-state"], () => this.mergeCuratorState());
        }
        const deterministic = this.curate(at, { force: true, recordCadence: true });
        this.withSkillLocks(["curator-state"], () => {
          this.mergeCuratorState();
          this.state.curatorLastReport = output || JSON.stringify(deterministic);
          this.persistCuratorState();
        });
        this.commit();
        return { ...deterministic, preview: false, consolidated: consolidate, output };
      } finally {
        this.releaseSkillLock(orchestrationLock);
      }
    };
    const currentRun = run().finally(() => {
      this.curatorRunning = undefined;
      if (curatorRuns.get(runKey) === currentRun) curatorRuns.delete(runKey);
    });
    this.curatorRunning = currentRun;
    curatorRuns.set(runKey, currentRun);
    return this.curatorRunning;
  }
  budgetStatus() { const budget = this.state.skilled ? this.config.workingBudget : this.config.toolCallBudget; return { used: this.state.budgetCalls, budget, skilled: this.state.skilled, reviewRequired: this.state.reviewRequired, nudgeIgnores: this.state.nudgeIgnores, maxNudgeIgnores: this.config.maxNudgeIgnores }; }
  budgetWidgetLines() { const s = this.budgetStatus(); const state = s.reviewRequired ? "REVIEW REQUIRED" : s.skilled ? "working" : "onboarding"; return [`Autogen skill budget: ${s.used}/${s.budget} · ${state}${s.reviewRequired ? " · use SkillManage review or Skill" : ""}`]; }
  private isExempt(toolName: string | undefined, input: any) {
    const n = String(toolName ?? "").toLowerCase().replace(/[^a-z0-9]/g, "");
    if (/^(skill|skillmanage|swarmskill|taskmanage|taskcreate|taskupdate|tasklist|taskget|todowrite|todoread|todo|enterplanmode|exitplanmode|plan|planmode|askuserquestion|requestapproval|pushagentupdate|submitfeedback|read|grep|find|glob|ls|listdir|lsp|websearch|webfetch|browser|xsearch|xaiwebsearch|fetch)$/.test(n)) return true;
    return n === "bash" && typeof input?.command === "string" && /^(pwd|ls|find|grep|rg|git\s+(status|log|diff|show)|cat|head|tail|wc|which|type|echo|printf)(\s|$)/i.test(input.command.trim());
  }
  gateTool(toolName: string, input: any = {}): { block?: true; message?: string; reason?: string } | undefined {
    if (this.config.mode !== "auto") return;
    const n = String(toolName ?? "").toLowerCase().replace(/[^a-z0-9]/g, "");
    if (!n || this.isExempt(toolName, input)) return;
    if (n === "bash" && typeof input?.command === "string" && /^(pwd|ls|find|grep|rg|git\s+(status|log|diff|show)|cat|head|tail|wc|which|type|echo|printf)(\s|$)/i.test(input.command.trim())) return;
    const budget = this.state.skilled ? this.config.workingBudget : this.config.toolCallBudget;
    if (!this.state.focusedTask) return;
    if (this.state.reviewRequired) return { block: true, reason: "Autogen review required before mutation; call SkillManage(action=\"review\") or invoke a reusable skill." };
    // Swarm's onboarding tier is a hard gate as soon as the budget is spent.
    if (!this.state.skilled && this.state.budgetCalls >= budget) return { block: true, reason: `Skill required before continuing: onboarding budget of ${budget} non-exempt tool calls was exceeded.` };
    // Pi's tool_call result only supports block/reason. Working-tier nudges
    // are therefore counted and attached once from tool_result/observeTurn.
    if (this.state.skilled && this.state.budgetCalls >= budget) return;
    return;
  }
  invokeSkill(name: string, args = "") {
    if (!safeName(name)) throw new Error("skill must match lowercase skill identifier syntax");
    return this.withSkillLocks([name, "curator-state"], () => {
      this.mergeCuratorState();
      const skill = this.parse(name);
      let content = skill.instructions;
      if (args) content = content.replaceAll("{{arg}}", args);
      const existing = this.state.skills[name] ?? { uses: 0 };
      this.state.skills[name] = { ...existing, version: skill.version, uses: existing.uses + 1, lastUsed: now() };
      if (!this.state.skills[name].pinned) { this.state.skills[name].curatorState = "active"; this.state.skills[name].archived = false; }
      this.state.focusedTask = true;
      this.state.skilled = true;
      this.state.budgetCalls = 0;
      this.state.nudgeIgnores = 0;
      this.state.reviewRequired = false;
      this.persistCuratorState();
      this.commit();
      return { skill: name, version: skill.version, path: skill.path, instructions: content };
    });
  }
  observeTurn(): string | undefined {
    this.state.turns++; this.curate();
    let message: string | undefined;
    if (this.config.mode === "auto" && this.state.turns > 1 && this.state.turns - this.state.lastNudgeTurn >= this.config.nudgeInterval &&
      (this.state.toolCalls >= this.config.toolCallThreshold || this.state.resolved >= this.config.errorResolutionThreshold)) {
      const budget = this.state.skilled ? this.config.workingBudget : this.config.toolCallBudget;
      if (this.state.budgetCalls >= budget) {
        if (this.state.skilled) {
          this.state.nudgeIgnores++;
          if (this.state.nudgeIgnores >= this.config.maxNudgeIgnores) this.state.reviewRequired = true;
        } else message = "Skill required before continuing: create or invoke a reusable skill.";
      }
      this.state.lastNudgeTurn = this.state.turns;
      this.state.nudges++;
      if (!message) message = this.state.reviewRequired ? "Autogen review required before mutation. Call SkillManage(action=\"review\") or use a reusable skill." : `Review reusable learning class-first: patch an existing skill or add a support file before creating one. Existing skills: ${this.list().map(s=>s.name).join(", ") || "none"}. A no-mutation review is valid.`;
    }
    this.commit(); return message;
  }
}

export class CuratorOrchestrator {
  private timer?: NodeJS.Timeout;
  constructor(readonly manager: AutoSkillManager) {}
  busy() {
    if (this.timer) clearTimeout(this.timer);
    this.timer = undefined;
  }
  idle() {
    if (this.timer || this.manager.config.mode === "never" || this.manager.config.accountingExempt) return;
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.manager.runCurator({ automatic: true }).catch(error => {
        this.manager.config.reviewHook?.({ action: "curator_failed", reason: error instanceof Error ? error.message : String(error) });
      });
    }, this.manager.config.curatorIdleDelayMs);
    this.timer.unref?.();
  }
  run(options: { preview?: boolean; consolidate?: boolean; automatic?: boolean; at?: number } = {}) {
    this.busy();
    return this.manager.runCurator(options);
  }
  dispose() { this.busy(); }
}

const autogenByPi = new WeakMap<object, AutoSkillManager>();
export function registerAutoSkills(pi: any, config: Config = {}) {
  const owner = pi as object;
  const existing = autogenByPi.get(owner);
  if (existing) return existing;
  const manager = new AutoSkillManager(config, (entry) => pi.appendEntry(entry.type, entry.data));
  if (manager.config.mode === "never") {
    autogenByPi.set(owner, manager);
    return manager;
  }
  const curator = new CuratorOrchestrator(manager);
  const register = (event: string, handler: any) => { const globalRegister = (globalThis as any).__piSwarmRegisterHook; return typeof globalRegister === "function" ? globalRegister(pi, "autogenskills", event, handler) : pi.on(event, handler); };
  const updateBudgetWidget = (ctx: any) => ctx?.ui?.setWidget?.("swarm-autogen-budget", manager.config.mode === "never" ? undefined : manager.budgetWidgetLines(), { placement: "belowEditor" });
  const isSubagent = (ctx: any) => ctx?.isSubAgent || ctx?.isSubagent || ctx?.agent?.isSubAgent;
  register("tool_call", (e: any, ctx: any) => { updateBudgetWidget(ctx); return manager.config.accountingExempt || isSubagent(ctx) ? undefined : manager.gateTool(e.toolName ?? e.tool_name, e.input ?? e.params); });
  register("before_agent_start", (e: any) => {
    curator.busy();
    if (manager.config.mode === "never") return;
    const skills = manager.list();
    const index = skills.length ? skills.map(s => `- ${s.name} (v${s.version}): ${s.description}`).join("\n") : "(none)";
    const guidance = `## Swarm Skills\nBefore complex work, check the available skills. Use the Skill tool to invoke a matching skill before other tools.\nAvailable autogenerated skills:\n${index}\nUse SkillManage(action=\"view\") for full instructions and SkillManage(action=\"review\") when nothing reusable was learned.`;
    return { systemPrompt: `${e.systemPrompt ?? ""}\n\n${guidance}` };
  });
  register("tool_result", (e: any, ctx: any) => {
    if (manager.config.accountingExempt || isSubagent(ctx)) return;
    manager.observeTool(!e.isError && !e.error, e.toolName ?? e.tool_name, e.input ?? e.params);
    // Lifecycle nudges are model-visible messages attached to the completed
    // tool result, not UI-only notifications at turn_end.
    const nudge = manager.observeTurn(); updateBudgetWidget(ctx);
    return nudge ? { content: [...(Array.isArray(e.content) ? e.content : []), { type: "text", text: nudge }] } : undefined;
  });
  register("turn_end", (_e: any, ctx: any) => { updateBudgetWidget(ctx); curator.idle(); });
  register("session_start", (_e: any, ctx: any) => {
    // getEntries() includes the whole session tree and can resurrect state
    // from a sibling branch. Only the active branch is authoritative.
    const branch = ctx.sessionManager?.getBranch?.();
    manager.rehydrate(Array.isArray(branch) ? branch : []);
    updateBudgetWidget(ctx);
    curator.idle();
  });
  register("session_shutdown", () => curator.dispose());
  pi.registerTool({ name: "Skill", label: "Invoke skill", description: "Invoke a matching reusable skill before performing the task. The skill instructions are returned for you to follow.", parameters: skillSchema, async execute(_id: string, params: any) {
    try { return { content: [{ type: "text", text: JSON.stringify(manager.invokeSkill(params.skill, params.args ?? "")) }], details: {} }; }
    catch (e) { return { content: [{ type: "text", text: JSON.stringify({ error: e instanceof Error ? e.message : String(e) }) }], isError: true, details: {} }; }
  } });
  pi.registerTool({ name: "SkillManage", label: "Manage autogenerated skills", description: "Create, review, patch, inspect, and archive reusable autogenerated skill packages. Prefer patching an existing umbrella; never overwrite skills.", parameters: skillManageSchema, async execute(_id: string, params: any) { try { return { content: [{ type: "text", text: JSON.stringify(manager.execute(params)) }], details: {} }; } catch (e) { return { content: [{ type: "text", text: JSON.stringify({ error: e instanceof Error ? e.message : String(e) }) }], isError: true, details: {} }; } } });
  pi.registerCommand?.("curator", {
    description: "Preview or apply autogenerated-skill curator maintenance",
    handler: async (args: string, ctx: any) => {
      const words = args.trim().split(/\s+/).filter(Boolean);
      const action = words[0] || "preview";
      if (action !== "preview" && action !== "apply") {
        ctx.ui?.notify?.("Usage: /curator preview|apply [--consolidate]", "error");
        return;
      }
      try {
        const result = await curator.run({ preview: action === "preview", consolidate: words.includes("--consolidate") });
        ctx.ui?.notify?.(result.output || JSON.stringify(result), "info");
      } catch (error) {
        ctx.ui?.notify?.(`Curator failed: ${error instanceof Error ? error.message : String(error)}`, "error");
      }
    },
  });
  autogenByPi.set(owner, manager);
  return manager;
}
