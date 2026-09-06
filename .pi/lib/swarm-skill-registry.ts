import { join, resolve } from "node:path";
import { SkillLoader, generateRankedAvailableSkillsXML, rankSkillsForContext, type LoadedSkill, type SkillLoaderOptions, type SkillLoadResult } from "../../skills/src/index.ts";

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
    this.result = this.loader.load();
  }
  enforcePolicy(closed: boolean, allowed?: string[]) {
    this.closed ||= closed;
    if (allowed) this.allowedNames = this.allowedNames ? this.allowedNames.filter(n => allowed.includes(n)) : [...allowed];
    this.loader.configure({ closed: this.closed, allowedNames: this.allowedNames });
    this.refresh();
  }
  refresh() { this.result = this.loader.load(); return this.result; }
  list() { return this.result.skills; }
  find(name: string) { return this.result.skills.find(s => s.name === name); }
  catalog(query = "") { return generateRankedAvailableSkillsXML(rankSkillsForContext(this.result.skills.filter(s => !s.disableModelInvocation), query)); }
  /**
   * skilltools/skill_tool.go Invoke: resolve, refuse disable-model-invocation,
   * prefix "Base directory for this skill: <dir>" for on-disk skills, then
   * SubstituteArguments ({{N}} positional; named {{arg}} kept for Pi callers)
   * and SubstituteVariables (${SWARM_SKILL_DIR}, ${SWARM_SESSION_ID}).
   */
  invoke(name: string, args = "", sessionId = "") {
    const skill = this.find(name);
    if (!skill) throw new Error(`skill ${JSON.stringify(name)} not found in registry`);
    if (skill.disableModelInvocation) throw new Error(`skill ${JSON.stringify(name)} cannot be used with the Skill tool due to disable-model-invocation`);
    const builtin = skill.source === "builtin";
    const path = builtin ? skill.location : skill.dir;
    let content = skill.instructions;
    if (content === "") return { ...skill, instructions: content, text: `Skill ${JSON.stringify(name)} has no instructions content.` };
    if (!builtin) content = `Base directory for this skill: ${path}\n\n${content}`;
    if (args) {
      const values = args.split(/\s+/).filter(Boolean);
      content = content.replaceAll("{{arg}}", args);
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
    installDir: resolve(o.installDir ?? join(home, ".swarm", "skills")),
    autogenDir: resolve(o.autogenDir ?? join(home, ".swarm", "skills", "autogen")),
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
