import { join, resolve } from "node:path";
import { lstatSync } from "node:fs";
import { runLegacyConfigMigration } from "../runtime/swarm-configmigrate.ts";
import { SkillLoader, generateRankedAvailableSkillsXML, loadSkillFromDir, rankSkillsForContext, type LoadedSkill, type SkillLoaderOptions, type SkillLoadResult } from "../../../packages/context/skills/src/index.ts";
import { effectiveSelection } from "./swarm-prompt-context-config.ts";

/** Canonical registry shared by discovery, Forge catalogues, and Skill invocation. */
export class SwarmSkillRegistry {
  readonly loader: SkillLoader;
  private result: SkillLoadResult;
  private closed: boolean;
  private allowedNames?: string[];
  constructor(readonly options: SkillLoaderOptions = {}) {
    this.closed = !!options.closed;
    this.allowedNames = options.allowedNames ? [...options.allowedNames] : options.allowedSkills ? [...options.allowedSkills] : undefined;
    this.loader = new SkillLoader({ ...options, closed: this.closed, allowedNames: this.allowedNames, allowedSkills: this.allowedNames });
    // Both swarm binaries run configmigrate.Run() before any config or skill
    // load; legacy ~/.swarmos/skills land under ~/.swarm/skills first.
    runLegacyConfigMigration({ home: options.home });
    this.result = this.loader.load();
  }
  enforcePolicy(closed: boolean, allowed?: string[]) {
    const before = JSON.stringify([this.closed, this.allowedNames]);
    this.closed ||= closed;
    if (allowed) this.allowedNames = this.allowedNames ? this.allowedNames.filter(n => allowed.includes(n)) : [...allowed];
    // Swarm's Registry is an in-memory map filled once by DiscoverAll; it is
    // never re-discovered from disk mid-session. Only a policy change reloads.
    if (JSON.stringify([this.closed, this.allowedNames]) === before) return;
    this.loader.configure({ closed: this.closed, allowedNames: this.allowedNames });
    this.refresh();
  }
  refresh() { this.result = this.loader.load(); return this.result; }
  /**
   * autogenskills history.go refreshSkill: after every revision transaction
   * (success or in-transaction failure) the registry entry for `name` is
   * replaced from the ACTIVE autogen package, or unloaded when the package is
   * archived/absent — even if the name came from a project/builtin root.
   */
  refreshSkill(name: string, autogenDir: string) {
    const active = join(autogenDir, name);
    let isActive = false;
    try { const info = lstatSync(active); isActive = info.isDirectory() && !info.isSymbolicLink(); } catch { isActive = false; }
    const rest = this.result.skills.filter(s => s.name !== name);
    if (!isActive) { this.result = { ...this.result, skills: rest }; return; }
    const skill = loadSkillFromDir(active, "autogen");
    if (skill.name !== name) throw new Error(`autogenskills: SKILL.md name ${JSON.stringify(skill.name)} does not match package name ${JSON.stringify(name)}`);
    if (this.allowedNames && !this.allowedNames.includes(name)) { this.result = { ...this.result, skills: rest }; return; }
    this.result = { ...this.result, skills: [...rest, skill].sort((a, b) => a.name.localeCompare(b.name)) };
  }
  list() { return this.result.skills; }
  find(name: string) { return this.result.skills.find(s => s.name === name); }
  /** skills_manager.go GetPromptContextForQuery ranks loader.List() unfiltered: disable-model-invocation skills still appear in <available_skills> (only the Skill tool refuses them). */
  catalog(query = "", allowed?: readonly string[]) {
    const skills = allowed ? effectiveSelection(this.result.skills, { mode: "allowlist", names: [...allowed] }) : this.result.skills;
    return generateRankedAvailableSkillsXML(rankSkillsForContext(skills, query));
  }
  /**
   * tools/skilltools/skill_tool.go Invoke: resolve, refuse
   * disable-model-invocation, prefix "Base directory for this skill: <Path>"
   * for on-disk skills (builtins have Path "builtin:<name>"), then
   * SubstituteArguments (named {{argName}} from frontmatter `arguments`
   * by position, then positional {{N}}; both over strings.Fields(args)) and
   * SubstituteVariables (${SWARM_SKILL_DIR} = Path, ${SWARM_SESSION_ID}).
   * The TUI's SessionIDGetter returns "" (sdk_integration.go), so callers
   * should pass "" unless they mirror a different host.
   */
  invoke(name: string, args = "", sessionId = "") {
    const skill = this.find(name);
    if (!skill) throw new Error(`skill ${JSON.stringify(name)} not found in registry`);
    if (skill.disableModelInvocation) throw new Error(`skill ${JSON.stringify(name)} cannot be used with the Skill tool due to disable-model-invocation`);
    const builtin = skill.source === "builtin";
    const path = builtin ? `builtin:${skill.name}` : skill.dir;
    let content = skill.instructions;
    if (content === "") return { ...skill, instructions: content, text: `Skill ${JSON.stringify(name)} has no instructions content.` };
    if (!builtin) content = `Base directory for this skill: ${path}\n\n${content}`;
    if (args !== "") {
      const values = args.split(/\s+/).filter(Boolean);
      (skill.arguments ?? []).forEach((argName, i) => { if (i < values.length) content = content.replaceAll(`{{${argName}}}`, values[i]); });
      content = content.replace(/\{\{(\d+)\}\}/g, (match, n) => { const i = Number(n); return i >= 1 && i <= values.length ? values[i - 1] : match; });
    }
    content = content.replaceAll("${SWARM_SKILL_DIR}", path).replaceAll("${SWARM_SESSION_ID}", sessionId);
    return { ...skill, instructions: content, text: content };
  }
}
export type { LoadedSkill, SkillLoaderOptions, SkillLoadResult };

const registries = new WeakMap<object, Map<string, { registry: SwarmSkillRegistry; signature: string; cliPaths: string[]; allowed?: string[] }>>();
const envAllowed = () => process.env.SWARM_SKILLS_ALLOWED?.split(",").map(s => s.trim()).filter(Boolean);
/** Normalize discovery inputs before comparing registry identity.  The loader applies
 * these defaults too, so omitted values must not look like conflicting paths.
 * Keep this limited to discovery roots: policy is intentionally enforced below
 * by intersection (narrowing), rather than becoming part of identity.
 */
const discoverySignature = (o: SkillLoaderOptions, cliPaths: string[]) => {
  const cwd = resolve(o.cwd ?? process.cwd());
  const home = resolve(o.home ?? process.env.HOME ?? cwd);
  const managedDir = o.managedDir ?? process.env.SWARM_MANAGED_SKILLS_DIR ?? "";
  return JSON.stringify({
    cwd,
    home,
    managedDir: managedDir ? resolve(managedDir) : "",
    installDir: resolve(o.installDir ?? join(home, ".swarmos", "skills")),
    autogenDir: resolve(o.autogenDir ?? join(process.env.SWARM_HOME || join(home, ".swarm"), "skills", "autogen")),
    cliPaths: cliPaths.map(path => resolve(path)).sort(),
  });
};
export function getSwarmSkillRegistry(pi: object, options: SkillLoaderOptions = {}) {
  const cwd = resolve(options.cwd ?? process.cwd());
  const supplied = options.allowedNames ?? options.allowedSkills;
  const allowed = supplied ?? envAllowed();
  const cliPaths = [...(options.cliPaths ?? [])].map(path => resolve(path)).sort();
  const effective: SkillLoaderOptions = { ...options, cwd, cliPaths, closed: options.closed ?? process.env.SWARM_SKILLS_CLOSED === "1", allowedNames: allowed, allowedSkills: allowed };
  let byCwd = registries.get(pi);
  if (!byCwd) { byCwd = new Map(); registries.set(pi, byCwd); }
  const signature = discoverySignature(effective, cliPaths);
  const existing = byCwd.get(cwd);
  if (!existing) {
    const registry = new SwarmSkillRegistry(effective);
    byCwd.set(cwd, { registry, signature, cliPaths, allowed: allowed ? [...allowed] : undefined });
    return registry;
  }
  if (existing.signature !== signature || JSON.stringify(existing.cliPaths) !== JSON.stringify(cliPaths)) throw new Error("conflicting Swarm skill discovery paths for the same Pi and cwd");
  const narrowed = allowed ? (existing.allowed ? existing.allowed.filter(n => allowed.includes(n)) : [...allowed]) : existing.allowed;
  existing.registry.enforcePolicy(!!effective.closed, narrowed);
  return existing.registry;
}
