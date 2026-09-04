import { SkillLoader, type SkillLoaderOptions } from "../../skills/src/index.ts";

export interface SwarmSkillsOptions extends SkillLoaderOptions { watch?: boolean; }

export function registerSwarmSkills(pi: any, options: SwarmSkillsOptions = {}) {
  const loader = new SkillLoader({ cwd: options.cwd ?? process.cwd(), ...options });
  let current = loader.load();
  const refresh = () => { current = loader.load(); };
  if (options.watch) loader.watch(refresh);
  pi.on?.("session_start", refresh);
  pi.on?.("before_agent_start", (event: any) => {
    if (options.closed && !options.allowedNames?.length) return;
    const rows = current.skills.map(skill => `- ${skill.name}: ${skill.description}`).join("\n") || "(none)";
    return { systemPrompt: `${event.systemPrompt ?? ""}\n\n## Swarm Skill Loader\nInvoke a matching skill explicitly with SwarmSkill before acting.\n${rows}` };
  });
  pi.registerCommand?.("swarm-skills", { description: "List discovered Swarm skills and their sources", handler: async (_args: string, ctx: any) => {
    current = loader.load();
    const text = current.skills.map(s => `${s.name} [${s.source}] — ${s.description}`).join("\n") || "No skills discovered";
    ctx.ui?.notify?.(text, "info");
  }});
  pi.registerTool?.({
    name: "SwarmSkill", label: "Invoke Swarm skill",
    description: "Invoke an explicitly selected skill discovered by the Swarm-compatible loader.",
    parameters: { type: "object", required: ["skill"], additionalProperties: false, properties: { skill: { type: "string" }, args: { type: "string" } } },
    async execute(_id: string, params: any) {
      current = loader.load();
      const skill = current.skills.find(s => s.name === params?.skill);
      if (!skill) return { content: [{ type: "text", text: JSON.stringify({ error: `skill not found: ${params?.skill}` }) }], isError: true };
      const instructions = params.args ? skill.instructions.replaceAll("{{arg}}", String(params.args)) : skill.instructions;
      return { content: [{ type: "text", text: JSON.stringify({ skill: skill.name, source: skill.source, path: skill.filePath, instructions, supportFiles: skill.supportFiles }) }], details: { source: skill.source, path: skill.filePath } };
    },
  });
  return { loader, getResult: () => current };
}

export default function swarmSkillsExtension(pi: any) { return registerSwarmSkills(pi); }
