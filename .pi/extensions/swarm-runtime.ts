import { resolve } from "node:path";
import { Policy } from "../../policy/src/index.ts";
import { AgentManager, registerAgents } from "../../agents/src/index.ts";
import { MCPManager } from "../../mcp/src/index.ts";
import { registerSwarmPrompt } from "./swarm-prompt.ts";

const RUNTIME = Symbol.for("pi-swarm-runtime");
type RuntimeState = { initialized: boolean; cwd: string; agents?: AgentManager; policy?: Policy; mcp?: MCPManager; };

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
  state.policy = new Policy({ workspace: cwd, allowedTools: options.allowedTools, allowMutation: options.allowMutation, allowNetwork: options.allowNetwork });

  // Dedicated extensions own tool registration; the umbrella only owns shared
  // policy/identity and must never register duplicate tools.

  state.agents = registerAgents(pi, new AgentManager({ cwd, concurrency: 4 }));
  pi.on?.("session_shutdown", () => state.mcp?.close());
  const manifests = pi.mcpManifests ?? [];
  if (Array.isArray(manifests)) state.mcp = new MCPManager(manifests, { closed: options.closed ?? true, registerTool: tool => pi.registerTool(tool) });
  pi.registerCommand?.("swarm-runtime", { description: "Inspect integrated Pi-Swarm runtime", handler: async (_args: string, ctx: any) => ctx.ui?.notify?.(`Pi-Swarm runtime active at ${cwd}; mcp=${manifests.length}`, "info") });
  return state;
}

export default function swarmRuntimeExtension(pi: Pi) { return registerSwarmRuntime(pi); }
