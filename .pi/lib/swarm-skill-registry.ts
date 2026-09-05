import { resolve } from "node:path";
import { SkillLoader, generateAvailableSkillsXML, type LoadedSkill, type SkillLoaderOptions, type SkillLoadResult } from "../../skills/src/index.ts";

/** One registry shared by the model-facing Skill tool, prompt catalog, and /skill command. */
export class SwarmSkillRegistry {
  readonly loader: SkillLoader;
  private result: SkillLoadResult;
  private closed = false;
  private allowedNames?: string[];
  constructor(options: SkillLoaderOptions = {}) { this.closed = !!options.closed; this.allowedNames = options.allowedNames; this.loader = new SkillLoader({ ...options, closed: this.closed, allowedNames: this.allowedNames }); this.result = this.loader.load(); }
  enforcePolicy(closed: boolean, allowedNames?: string[]) {
    this.closed = this.closed || closed;
    if (allowedNames) this.allowedNames = this.allowedNames ? this.allowedNames.filter(name => allowedNames.includes(name)) : [...allowedNames];
    this.loader.configure({ closed: this.closed, allowedNames: this.allowedNames });
    this.refresh();
  }
  refresh(): SkillLoadResult { this.result = this.loader.load(); return this.result; }
  list(): LoadedSkill[] { return this.result.skills; }
  find(name: string): LoadedSkill | undefined { return this.result.skills.find(s => s.name === name); }
  catalog(): string { return generateAvailableSkillsXML(this.result.skills.filter(s => !s.disableModelInvocation)); }
  invoke(name: string, args = ""): LoadedSkill {
    const skill = this.find(name);
    if (!skill) throw new Error(`skill not found: ${name}`);
    if (skill.disableModelInvocation) throw new Error(`skill invocation is disabled: ${name}`);
    return { ...skill, instructions: args ? skill.instructions.replaceAll("{{arg}}", args) : skill.instructions };
  }
}

export type { LoadedSkill, SkillLoaderOptions, SkillLoadResult };

const registries = new WeakMap<object, Map<string, SwarmSkillRegistry>>();
export function getSwarmSkillRegistry(pi: object, options: SkillLoaderOptions = {}): SwarmSkillRegistry {
  // All entry points must derive the same policy when callers omit options;
  // otherwise prompt construction and invocation can observe different
  // registries depending on extension initialization order.
  const cwd = options.cwd ?? process.cwd();
  const explicitAllowed = options.allowedNames ?? (process.env.SWARM_SKILLS_ALLOWED?.split(",").map(s => s.trim()).filter(Boolean));
  const effective: SkillLoaderOptions = {
    ...options, cwd,
    closed: options.closed ?? (process.env.SWARM_SKILLS_CLOSED === "1"),
    cliPaths: options.cliPaths ?? [],
    allowedNames: explicitAllowed,
    allowedSkills: explicitAllowed,
  };
  // Exactly one policy/registry per Pi and working directory. Later callers
  // cannot accidentally replace a stricter registry with a permissive one.
  const key = resolve(cwd);
  let byCwd = registries.get(pi);
  if (!byCwd) { byCwd = new Map(); registries.set(pi, byCwd); }
  let registry = byCwd.get(key);
  if (!registry) { registry = new SwarmSkillRegistry(effective); byCwd.set(key, registry); }
  registry.enforcePolicy(!!effective.closed, effective.allowedNames);
  return registry;
}
