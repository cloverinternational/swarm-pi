import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, readdirSync, renameSync, statSync, writeFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";

export type Mode = "never" | "manual" | "auto";
export type TriggerReason = "manual" | "tool_call_threshold" | "error_resolution" | "llm_nudge";
export type CuratorState = "active" | "stale" | "archived" | "pinned" | "consolidated";
export interface Config { mode?: Mode; dir?: string; toolCallThreshold?: number; errorResolutionThreshold?: number; nudgeInterval?: number; minInstructionsLength?: number; toolCallBudget?: number; workingBudget?: number; maxNudgeIgnores?: number; staleAfterDays?: number; archiveAfterDays?: number; reviewHook?: ReviewHook; }
export interface Metrics { turns: number; toolCalls: number; errors: number; resolved: number; nudges: number; nudgeIgnores: number; reviews: number; mutations: number; skilled: boolean; budgetCalls: number; reviewRequired: boolean; }
export interface ReviewHook { (event: { action: string; name?: string; revision?: string; reason?: string }): void }
export interface Skill { name: string; description: string; instructions: string; tags: string[]; category?: string; version: string; path: string; updatedAt: string; }
export interface SkillEntry { type: "pi-swarm-autogen-state"; data: State; }
export interface Revision { id: string; parent?: string; action: string; createdAt: string; files: Record<string, string>; blobs?: Record<string, string>; }
export interface State { skills: Record<string, { version: string; uses: number; lastUsed?: string; archived?: boolean; hash?: string; curatorState?: CuratorState; pinned?: boolean }>; turns: number; toolCalls: number; errors: number; resolved: number; lastNudgeTurn: number; reviews: number; nudges: number; nudgeIgnores: number; mutations: number; skilled: boolean; budgetCalls: number; reviewRequired: boolean; }
export const defaultState = (): State => ({ skills: {}, turns: 0, toolCalls: 0, errors: 0, resolved: 0, lastNudgeTurn: 0, reviews: 0, nudges: 0, nudgeIgnores: 0, mutations: 0, skilled: false, budgetCalls: 0, reviewRequired: false });
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
    action: { type: "string", enum: ["create", "patch", "view", "list", "read_file", "write_file", "review", "history", "undo", "archive", "pin", "unpin", "metrics"] },
    name: { type: "string", pattern: "^[a-z0-9]+(-[a-z0-9]+)*$", maxLength: 64 }, description: { type: "string" }, instructions: { type: "string" },
    append: { type: "boolean" }, tags: { type: "string" }, category: { type: "string" }, file_path: { type: "string" }, file_content: { type: "string" },
    review_reason: { type: "string" }, expected_revision: { type: "string" }, revision: { type: "string" }, pinned: { type: "boolean" }, offset: { type: "integer", minimum: 0 }, limit: { type: "integer", minimum: 1 },
  },
} as const;

const safeName = (n: unknown) => typeof n === "string" && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(n) && n.length <= 64 && n !== "archive";
const now = () => new Date().toISOString();
const clone = <T>(x: T): T => structuredClone(x);

export class AutoSkillManager {
  private state: State = defaultState();
  readonly config: Required<Pick<Config, "mode" | "dir" | "toolCallThreshold" | "errorResolutionThreshold" | "nudgeInterval" | "minInstructionsLength" | "toolCallBudget" | "workingBudget" | "maxNudgeIgnores" | "staleAfterDays" | "archiveAfterDays">> & { reviewHook?: ReviewHook };
  constructor(config: Config = {}, private readonly persist?: (entry: SkillEntry) => void) {
    const home = process.env.HOME ?? process.cwd();
    const mode = config.mode ?? (process.env.SWARM_AUTOGEN_MODE as Mode) ?? "never";
    if (!["never", "manual", "auto"].includes(mode)) throw new Error(`invalid autogen mode: ${mode}`);
    this.config = { mode, dir: config.dir ?? process.env.SWARM_AUTOGEN_DIR ?? join(home, ".swarm", "skills", "autogen"), toolCallThreshold: config.toolCallThreshold ?? 15, errorResolutionThreshold: config.errorResolutionThreshold ?? 1, nudgeInterval: Math.max(1, config.nudgeInterval ?? 15), minInstructionsLength: config.minInstructionsLength ?? 200, toolCallBudget: config.toolCallBudget ?? 15, workingBudget: config.workingBudget ?? 90, maxNudgeIgnores: config.maxNudgeIgnores ?? 3, staleAfterDays: config.staleAfterDays ?? 30, archiveAfterDays: config.archiveAfterDays ?? 90, reviewHook: config.reviewHook };
  }
  snapshot(): State { return clone(this.state); }
  restore(state: State) { this.state = { ...defaultState(), ...clone(state), skills: { ...(state.skills ?? {}) }, nudges: state.nudges ?? 0, nudgeIgnores: state.nudgeIgnores ?? 0, mutations: state.mutations ?? 0 }; }
  metrics(): Metrics { return { turns: this.state.turns, toolCalls: this.state.toolCalls, errors: this.state.errors, resolved: this.state.resolved, nudges: this.state.nudges, nudgeIgnores: this.state.nudgeIgnores, reviews: this.state.reviews, mutations: this.state.mutations, skilled: this.state.skilled, budgetCalls: this.state.budgetCalls, reviewRequired: this.state.reviewRequired }; }
  rehydrate(entries: readonly unknown[]) { const e = [...entries].reverse().find((x: any) => x?.type === "pi-swarm-autogen-state") as SkillEntry | undefined; if (e?.data) this.restore(e.data); }
  private commit() { this.persist?.({ type: "pi-swarm-autogen-state", data: this.snapshot() }); }
  private dir(name: string) { return join(this.config.dir, name); }
  private file(name: string) { return join(this.dir(name), "SKILL.md"); }
  private parse(name: string): Skill {
    const content = readFileSync(this.file(name), "utf8"), match = content.match(/^---\n([\s\S]*?)\n---\n?([\s\S]*)$/);
    if (!match) throw new Error(`invalid SKILL.md for ${name}`);
    const fields: Record<string,string> = {}; for (const line of match[1].split("\n")) { const i = line.indexOf(":"); if (i > 0) fields[line.slice(0,i).trim()] = line.slice(i+1).trim(); }
    return { name: fields.name || name, description: fields.description || "", instructions: match[2].trimEnd(), tags: fields.tags ? fields.tags.split(",").map(x=>x.trim()).filter(Boolean) : [], category: fields.category || undefined, version: fields.version || "1.0.0", path: this.dir(name), updatedAt: fields.updated_at || now() };
  }
  private body(skill: Skill) { return `---\nname: ${skill.name}\ndescription: ${skill.description}\nversion: ${skill.version}\n${skill.tags.length ? `tags: ${skill.tags.join(", ")}\n` : ""}${skill.category ? `category: ${skill.category}\n` : ""}updated_at: ${skill.updatedAt}\n---\n\n${skill.instructions}\n`; }
  private hashBytes(data: string | Buffer) { return createHash("sha256").update(data).digest("hex"); }
  private packageFiles(name: string): Record<string, string> { const out: Record<string,string> = {}; const walk = (root: string, prefix = "") => { if (!existsSync(root)) return; for (const e of readdirSync(root, { withFileTypes: true })) { const p = join(root, e.name), rel = prefix ? `${prefix}/${e.name}` : e.name; if (e.isDirectory()) walk(p, rel); else if (e.isFile()) out[rel] = this.hashBytes(readFileSync(p)); } }; walk(this.dir(name)); return out; }
  private snapshotRevision(name: string, action: string, parent?: string): Revision { const files = this.packageFiles(name), blobs: Record<string,string> = {}; for (const path of Object.keys(files)) blobs[path] = readFileSync(join(this.dir(name), path)).toString("base64"); const canonical = JSON.stringify({ format: HISTORY_FORMAT, skill: name, parent: parent ?? "", action, files }); return { id: this.hashBytes(canonical), parent, action, createdAt: now(), files, blobs }; }
  private saveRevision(name: string, action: string, parent?: string) { const rev = this.snapshotRevision(name, action, parent); const root = join(this.config.dir, ".history", "revisions", name); mkdirSync(root, { recursive: true, mode: 0o755 }); try { writeFileSync(join(root, `${rev.id}.json`), JSON.stringify({ format: HISTORY_FORMAT, ...rev }, null, 2) + "\n", { flag: "wx", mode: 0o444 }); } catch (e: any) { if (e?.code !== "EEXIST") throw e; } mkdirSync(join(this.config.dir, ".history", "heads"), { recursive: true }); writeFileSync(join(this.config.dir, ".history", "heads", name), rev.id + "\n"); return rev; }
  private write(skill: Skill, action: string) { mkdirSync(this.dir(skill.name), { recursive: true, mode: 0o755 }); const parent = this.state.skills[skill.name]?.hash; const body = this.body(skill); const tmp = `${this.file(skill.name)}.${process.pid}.tmp`; writeFileSync(tmp, body, { mode: 0o644 }); renameSync(tmp, this.file(skill.name)); const rev = this.saveRevision(skill.name, action, parent); const old = this.state.skills[skill.name]; this.state.skills[skill.name] = { ...(old ?? { uses: 0 }), version: skill.version, hash: rev.id, lastUsed: now(), curatorState: old?.pinned ? "pinned" : "active", pinned: old?.pinned ?? false }; this.state.mutations++; this.config.reviewHook?.({ action, name: skill.name, revision: rev.id }); }
  private assertEnabled() { if (this.config.mode === "never") throw new Error("autogenerated skills are disabled (mode=never)"); }
  private assertMutationAllowed(action: string) { if (this.config.mode !== "auto") return; if (action === "review") return; const budget = this.state.skilled ? this.config.workingBudget : this.config.toolCallBudget; if (this.state.reviewRequired) throw new Error("autogen review required before mutation; call SkillManage(action=\"review\")"); if (this.state.budgetCalls >= budget) { if (!this.state.skilled) throw new Error(`autogen onboarding budget exceeded (${budget}); create or use a skill`); if (this.state.nudgeIgnores >= this.config.maxNudgeIgnores) { this.state.reviewRequired = true; throw new Error("autogen review required before mutation; call SkillManage(action=\"review\")"); } } }
  execute(input: any): any {
    this.assertEnabled(); const action = input?.action;
    if (action === "list") return { skills: this.list() };
    if (!safeName(input?.name) && !["review", "metrics"].includes(action)) throw new Error("name must match lowercase skill identifier syntax");
    if (action === "review") { this.state.reviews++; this.state.reviewRequired = false; this.state.nudgeIgnores = 0; const reason = input.review_reason ?? "no reusable learning"; this.config.reviewHook?.({ action, reason }); this.commit(); return { reviewed: true, reason }; }
    if (action === "metrics") return { metrics: this.metrics() };
    const name = input.name as string;
    if (action === "pin" || action === "unpin") { const skill = this.parse(name); const old = this.state.skills[name] ?? { version: skill.version, uses: 0 }; old.pinned = action === "pin"; old.curatorState = old.pinned ? "pinned" : "active"; this.state.skills[name] = old; this.config.reviewHook?.({ action, name, revision: old.hash }); this.commit(); return { name, pinned: old.pinned, state: old.curatorState }; }
    if (action === "create") { this.assertMutationAllowed(action); if (existsSync(this.file(name))) throw new Error(`skill ${name} already exists; view and patch it instead`); if (!input.description || !input.instructions) throw new Error("description and instructions are required"); if (input.instructions.length < this.config.minInstructionsLength) throw new Error(`instructions must contain at least ${this.config.minInstructionsLength} characters`); const s: Skill = { name, description: input.description, instructions: input.instructions, tags: String(input.tags ?? "").split(",").map((x:string)=>x.trim()).filter(Boolean), category: input.category, version: "1.0.0", path: this.dir(name), updatedAt: now() }; this.write(s, "create"); this.commit(); return { skill: s, revision: this.state.skills[name].hash }; }
    if (action === "view") { const s = this.parse(name); const offset = input.offset ?? 0, limit = input.limit ?? 80000; const hash = this.state.skills[name]?.hash ?? this.snapshotRevision(name, "external").id; this.state.skills[name] = { ...(this.state.skills[name] ?? { uses: 0 }), version: s.version, hash, lastUsed: now() }; this.commit(); return { skill: { ...s, instructions: [...s.instructions].slice(offset, offset + limit) }, revision: hash, version: s.version }; }
    if (action === "patch") { this.assertMutationAllowed(action); if (this.config.mode !== "auto" && this.config.mode !== "manual") throw new Error("disabled"); const s = this.parse(name); const currentRevision = this.state.skills[name]?.hash ?? this.snapshotRevision(name, "external").id; if (input.expected_revision && input.expected_revision !== currentRevision && input.expected_revision !== s.version) throw new Error(`revision conflict: expected ${input.expected_revision}, current ${currentRevision}`); s.instructions = input.append ? `${s.instructions}\n\n${input.instructions ?? ""}` : (input.instructions || s.instructions); if (input.description) s.description = input.description; if (input.tags) s.tags = [...new Set([...s.tags, ...String(input.tags).split(",").map(x=>x.trim())])]; const parts = s.version.split("."); s.version = parts.length === 3 && /^\d+$/.test(parts[2]) ? `${parts[0]}.${parts[1]}.${Number(parts[2]) + 1}` : s.version; s.updatedAt = now(); this.write(s, "patch"); this.commit(); return { skill: s, revision: this.state.skills[name].hash, version: s.version }; }
    if (action === "archive") { const meta = this.state.skills[name]; if (meta?.pinned || meta?.curatorState === "pinned") throw new Error(`skill ${name} is pinned`); const s = this.parse(name); const target = join(this.config.dir, ".archive", `${name}-${Date.now()}`); mkdirSync(join(this.config.dir, ".archive"), { recursive: true }); renameSync(s.path, target); delete this.state.skills[name]; this.commit(); return { archived: name, path: target }; }
    if (action === "history") { const root = join(this.config.dir, ".history", "revisions", name); if (!existsSync(root)) return { revisions: [] }; const revisions = readdirSync(root).filter(x => x.endsWith(".json")).map(x => JSON.parse(readFileSync(join(root, x), "utf8")) as Revision).sort((a,b) => b.createdAt.localeCompare(a.createdAt)); return { revisions: revisions.map(r => ({ id: r.id, parent: r.parent, action: r.action, createdAt: r.createdAt, files: r.files })) }; }
    if (action === "undo") { const id = String(input.revision ?? ""); if (!/^[a-f0-9]{64}$/.test(id)) throw new Error("undo requires a valid revision hash"); const p = join(this.config.dir, ".history", "revisions", name, `${id}.json`); if (!existsSync(p)) throw new Error(`revision ${id} not found`); const rev = JSON.parse(readFileSync(p, "utf8")) as Revision; if (!rev.blobs) throw new Error("revision has no restorable file snapshot"); for (const [path, encoded] of Object.entries(rev.blobs)) { const target = resolve(this.dir(name), path); mkdirSync(resolve(target, ".."), { recursive: true }); writeFileSync(target, Buffer.from(encoded, "base64"), { mode: 0o644 }); } const s = this.parse(name); const next = this.saveRevision(name, "undo", this.state.skills[name]?.hash); this.state.skills[name] = { ...(this.state.skills[name] ?? { uses: 0 }), version: s.version, hash: next.id, lastUsed: now() }; this.commit(); return { restored: id, revision: next.id }; }
    if (action === "read_file" || action === "write_file") { if (action === "write_file") this.assertMutationAllowed(action); const raw = String(input.file_path ?? ""); const p = resolve(this.dir(name), raw); const first = raw.replaceAll("\\", "/").split("/")[0]; if (!SUPPORT_ROOTS.has(first) || relative(this.dir(name), p).startsWith("..")) throw new Error("support path must stay under references/, templates/, scripts/, or assets/"); if (action === "read_file") return { path: p, content: readFileSync(p, "utf8"), revision: this.state.skills[name]?.hash }; mkdirSync(resolve(p, ".."), { recursive: true }); writeFileSync(p, input.file_content ?? "", { mode: 0o644 }); const rev = this.saveRevision(name, "write_file", this.state.skills[name]?.hash); this.state.skills[name] = { ...(this.state.skills[name] ?? { uses: 0 }), version: this.parse(name).version, hash: rev.id, lastUsed: now() }; this.commit(); return { path: p, written: true, revision: rev.id }; }
    throw new Error(`unknown action ${action}`);
  }
  list(): Skill[] { if (!existsSync(this.config.dir)) return []; return readdirSync(this.config.dir, { withFileTypes: true }).filter((e) => e.isDirectory() && safeName(e.name) && existsSync(this.file(e.name))).map((e) => this.parse(e.name)); }
  observeTool(success: boolean, toolName?: string, input?: any) {
    if (success) {
      this.state.toolCalls++;
      const normalized = String(toolName ?? "").toLowerCase();
      if (!/skill|task|plan|askuser|approval|pushagent|submitfeedback|read|grep|find|ls|search|browser|fetch/.test(normalized)) this.state.budgetCalls++;
      if (this.state.errors > this.state.resolved) this.state.resolved++;
    } else this.state.errors++;
    const skillName = input?.name ?? input?.skill ?? input?.skill_name;
    if (success && toolName && /^(skill|skillmanage|swarmskill)$/i.test(toolName)) {
      if (typeof skillName === "string" && this.state.skills[skillName]) this.state.skills[skillName].uses++;
      this.state.skilled = true;
      this.state.budgetCalls = 0;
      this.state.nudgeIgnores = 0;
      this.state.reviewRequired = false;
      this.state.skills[skillName].lastUsed = now();
      this.commit();
    } else if (success) this.commit();
  }
  gateTool(toolName: string, input: any = {}): { block: true; reason: string } | undefined {
    if (this.config.mode !== "auto") return;
    const n = String(toolName ?? "").toLowerCase().replace(/[^a-z0-9]/g, "");
    if (!n || /skill|skillmanage|task|plan|askuser|approval|pushagent|submitfeedback|read|grep|find|ls|search|browser|fetch/.test(n)) return;
    if (n === "bash" && typeof input?.command === "string" && /^(pwd|ls|find|grep|rg|git\s+(status|log|diff|show)|cat|head|tail|wc|which|type|echo|printf)(\s|$)/i.test(input.command.trim())) return;
    const budget = this.state.skilled ? this.config.workingBudget : this.config.toolCallBudget;
    if (this.state.reviewRequired) return { block: true, reason: "Autogen review required before mutation; call SkillManage(action=\"review\") or invoke a reusable skill." };
    if (this.state.budgetCalls >= budget && !this.state.skilled) return { block: true, reason: `Skill required before continuing: onboarding budget of ${budget} non-exempt tool calls was exceeded.` };
    if (this.state.budgetCalls >= budget && this.state.skilled && this.state.nudgeIgnores > this.config.maxNudgeIgnores) {
      this.state.reviewRequired = true; this.commit();
      return { block: true, reason: "Autogen review required before mutation; call SkillManage(action=\"review\") or invoke a reusable skill." };
    }
    return;
  }
  invokeSkill(name: string, args = "") {
    if (!safeName(name)) throw new Error("skill must match lowercase skill identifier syntax");
    const skill = this.parse(name);
    let content = skill.instructions;
    if (args) content = content.replaceAll("{{arg}}", args);
    const existing = this.state.skills[name] ?? { uses: 0 };
    this.state.skills[name] = { ...existing, version: skill.version, uses: existing.uses + 1, lastUsed: now() };
    this.commit();
    return { skill: name, version: skill.version, path: skill.path, instructions: content };
  }
  observeTurn(): string | undefined { this.state.turns++; if (this.config.mode !== "auto" || this.state.turns <= 1 || this.state.turns - this.state.lastNudgeTurn < this.config.nudgeInterval || (this.state.toolCalls < this.config.toolCallThreshold && this.state.resolved < this.config.errorResolutionThreshold)) return; const budget = this.state.skilled ? this.config.workingBudget : this.config.toolCallBudget; if (this.state.budgetCalls >= budget) { this.state.nudgeIgnores++; if (this.state.skilled && this.state.nudgeIgnores > this.config.maxNudgeIgnores) this.state.reviewRequired = true; if (!this.state.skilled) return "Skill required before continuing: create or invoke a reusable skill."; } this.state.lastNudgeTurn = this.state.turns; this.state.nudges++; this.commit(); return this.state.reviewRequired ? "Autogen review required before mutation. Call SkillManage(action=\"review\") or use a reusable skill." : `Review reusable learning class-first: patch an existing skill or add a support file before creating one. Existing skills: ${this.list().map(s=>s.name).join(", ") || "none"}. A no-mutation review is valid.`; }
}

export function registerAutoSkills(pi: any, config: Config = {}) {
  const manager = new AutoSkillManager(config, (entry) => pi.appendEntry(entry.type, entry.data));
  const register = (event: string, handler: any) => { const globalRegister = (globalThis as any).__piSwarmRegisterHook; return typeof globalRegister === "function" ? globalRegister(pi, "autogenskills", event, handler) : pi.on(event, handler); };
  register("session_start", (_e: any, ctx: any) => manager.rehydrate(ctx.sessionManager?.getEntries?.() ?? []));
  register("tool_call", (e: any) => manager.gateTool(e.toolName ?? e.tool_name, e.input ?? e.params));
  register("before_agent_start", (e: any) => {
    if (manager.config.mode === "never") return;
    const skills = manager.list();
    const index = skills.length ? skills.map(s => `- ${s.name} (v${s.version}): ${s.description}`).join("\n") : "(none)";
    const guidance = `## Swarm Skills\nBefore complex work, check the available skills. Use the Skill tool to invoke a matching skill before other tools.\nAvailable autogenerated skills:\n${index}\nUse SkillManage(action=\"view\") for full instructions and SkillManage(action=\"review\") when nothing reusable was learned.`;
    return { systemPrompt: `${e.systemPrompt ?? ""}\n\n${guidance}` };
  });
  register("tool_result", (e: any) => manager.observeTool(!e.isError && !e.error && !e.result?.isError, e.toolName ?? e.tool_name, e.input ?? e.params));
  register("turn_end", (_e: any, ctx: any) => { const nudge = manager.observeTurn(); if (nudge) ctx.ui?.notify(nudge, "info"); });
  pi.registerTool({ name: "Skill", label: "Invoke skill", description: "Invoke a matching reusable skill before performing the task. The skill instructions are returned for you to follow.", parameters: skillSchema, async execute(_id: string, params: any) {
    try { return { content: [{ type: "text", text: JSON.stringify(manager.invokeSkill(params.skill, params.args ?? "")) }], details: {} }; }
    catch (e) { return { content: [{ type: "text", text: JSON.stringify({ error: e instanceof Error ? e.message : String(e) }) }], isError: true, details: {} }; }
  } });
  pi.registerTool({ name: "SkillManage", label: "Manage autogenerated skills", description: "Create, review, patch, inspect, and archive reusable autogenerated skill packages. Prefer patching an existing umbrella; never overwrite skills.", parameters: skillManageSchema, async execute(_id: string, params: any) { try { return { content: [{ type: "text", text: JSON.stringify(manager.execute(params)) }], details: {} }; } catch (e) { return { content: [{ type: "text", text: JSON.stringify({ error: e instanceof Error ? e.message : String(e) }) }], isError: true, details: {} }; } } });
  return manager;
}
