import { resolve } from "node:path";
import { SkillLoader } from "../../skills/src/index.ts";
import { Policy } from "../../policy/src/index.ts";
import { Scheduler } from "../../schedule/src/index.ts";
import { AgentManager, registerAgents } from "../../agents/src/index.ts";
import { MCPManager } from "../../mcp/src/index.ts";
import { registerTaskManageExtension } from "./taskmanage.ts";
import { registerAutoSkills } from "../../autogenskills/src/index.ts";
import { registerSwarmPrompt } from "./swarm-prompt.ts";
import { registerSwarmSkills } from "./swarm-skills.ts";
import { registerDiskHooks } from "./swarm-disk-hooks.ts";

const RUNTIME = Symbol.for("pi-swarm-runtime");
type RuntimeState = { initialized: boolean; cwd: string; skills?: ReturnType<typeof registerSwarmSkills>; scheduler?: Scheduler; agents?: AgentManager; policy?: Policy; mcp?: MCPManager; };

type Pi = any;

/** Single integration boundary for the Pi-Swarm extensions. */
export function registerSwarmRuntime(pi: Pi, options: { cwd?: string; closed?: boolean; allowedTools?: string[]; allowedSkills?: string[]; allowMutation?: boolean; allowNetwork?: boolean } = {}): RuntimeState {
  const root = globalThis as any;
  const existing = root[RUNTIME] as RuntimeState | undefined;
  if (existing?.initialized && existing.cwd === resolve(options.cwd ?? process.cwd())) return existing;
  const cwd = resolve(options.cwd ?? pi.getCwd?.() ?? process.cwd());
  const state: RuntimeState = { initialized: true, cwd };
  root[RUNTIME] = state;

  // Establish prompt and resource policy before feature registration.
  registerSwarmPrompt(pi);
  state.skills = registerSwarmSkills(pi, { cwd, closed: options.closed, allowedNames: options.allowedSkills });
  state.policy = new Policy({ workspace: cwd, allowedTools: options.allowedTools, allowMutation: options.allowMutation, allowNetwork: options.allowNetwork });

  // Register canonical task/hooks/interaction surface exactly once.
  // When loaded as the umbrella extension, the dedicated taskmanage and
  // autogenskills extensions may also be discovered by Pi. Those extensions
  // remain the canonical registrars; the umbrella only owns shared lifecycle
  // state and must not register duplicate tools.
  // Pi disallows invoking extension actions while extensions are loading;
  // always register the canonical surfaces here and let the host suppress
  // duplicate extension files through its own discovery configuration.
  registerTaskManageExtension(pi, { enforcementMode: "advise" });
  registerAutoSkills(pi, { mode: (process.env.SWARM_AUTOGEN_MODE as any) ?? "auto" });
  registerDiskHooks(pi, { cwd });

  state.scheduler = new Scheduler({ workDir: cwd, sink: async prompt => {
    if (typeof pi.sendUserMessage === "function") await pi.sendUserMessage(prompt, { deliverAs: "followUp" });
    else pi.appendEntry?.("pi-swarm-schedule-fired", { prompt, at: new Date().toISOString() });
  }});
  state.scheduler.start().catch(error => pi.appendEntry?.("pi-swarm-runtime-error", { subsystem: "scheduler", error: String(error) }));
  // Schedule tools are registered by the schedule extension in the normal path;
  // expose lifecycle ownership here so shutdown is centralized.
  pi.on?.("session_shutdown", () => state.scheduler?.stop());

  state.agents = registerAgents(pi, new AgentManager({ cwd, concurrency: 4 }));
  pi.on?.("session_shutdown", () => state.mcp?.close());
  const manifests = pi.mcpManifests ?? [];
  if (Array.isArray(manifests)) state.mcp = new MCPManager(manifests, { closed: options.closed ?? true, registerTool: tool => pi.registerTool(tool) });
  pi.registerCommand?.("swarm-runtime", { description: "Inspect integrated Pi-Swarm runtime", handler: async (_args: string, ctx: any) => ctx.ui?.notify?.(`Pi-Swarm runtime active at ${cwd}; skills=${state.skills?.getResult().skills.length ?? 0}; mcp=${manifests.length}; scheduler=on`, "info") });
  return state;
}

export default function swarmRuntimeExtension(pi: Pi) { return registerSwarmRuntime(pi); }
