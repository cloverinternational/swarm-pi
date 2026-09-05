import { SkillLoader, generateAvailableSkillsXML, type LoadedSkill, type SkillLoaderOptions, type SkillLoadResult } from "../../skills/src/index.ts";

/** One registry shared by the model-facing Skill tool, prompt catalog, and /skill command. */
export class SwarmSkillRegistry {
  readonly loader: SkillLoader;
  private result: SkillLoadResult;
  constructor(options: SkillLoaderOptions = {}) { this.loader = new SkillLoader({ ...options, closed: options.closed ?? false }); this.result = this.loader.load(); }
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

const registries = new WeakMap<object, SwarmSkillRegistry>();
export function getSwarmSkillRegistry(pi: object, options: SkillLoaderOptions = {}): SwarmSkillRegistry {
  let registry = registries.get(pi);
  if (!registry) { registry = new SwarmSkillRegistry(options); registries.set(pi, registry); }
  return registry;
}
