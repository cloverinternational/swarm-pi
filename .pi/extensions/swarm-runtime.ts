import { resolve } from "node:path";
import { Policy } from "../../policy/src/index.ts";
import { AgentManager, createPiRunner, registerAgents } from "../../agents/src/index.ts";
import { MCPManager } from "../../mcp/src/index.ts";
import { registerSwarmPrompt } from "./swarm-prompt.ts";
import { DaemonRpcClient, createDaemonControlTask, createDaemonGoalLoop } from "../../runtime-contracts/src/daemon-rpc.ts";
import { registerGoalLoop } from "../../runtime-contracts/src/goal-loop.ts";
import { registerControlTaskTools } from "./control-task-tools.ts";
import { withDefaultToolRenderer } from "../lib/swarm-tool-renderer.ts";

type RuntimeState = { initialized: boolean; cwd: string; agents?: AgentManager; policy?: Policy; mcp?: MCPManager; daemon?: DaemonRpcClient; daemonStatus: "configured" | "unavailable"; };
const runtimeByPi = new WeakMap<object, RuntimeState>();

type Pi = any;

/** Single integration boundary for the Pi-Swarm extensions. */
export function registerSwarmRuntime(pi: Pi, options: { cwd?: string; closed?: boolean; allowedTools?: string[]; allowedSkills?: string[]; allowMutation?: boolean; allowNetwork?: boolean; daemonSocket?: string; daemonToken?: string; daemonTimeoutMs?: number } = {}): RuntimeState {
  const cwd = resolve(options.cwd ?? pi.getCwd?.() ?? process.cwd());
  const existing = runtimeByPi.get(pi as object);
  if (existing?.initialized && existing.cwd === cwd) return existing;
  const socketPath = options.daemonSocket ?? process.env.PI_SWARM_DAEMON_SOCKET;
  const token = options.daemonToken ?? process.env.PI_SWARM_DAEMON_TOKEN;
  const configured = Boolean(socketPath && token);
  const state: RuntimeState = { initialized: true, cwd, daemonStatus: configured ? "configured" : "unavailable" };
  // Construction is inert; DaemonRpcClient opens a socket only when a call is made.
  const unavailable = (name: string) => new Proxy({}, { get: () => async () => { throw new Error("Pi-Swarm daemon unavailable: configure PI_SWARM_DAEMON_SOCKET and PI_SWARM_DAEMON_TOKEN (" + name + ")"); } }) as any;
  const daemon = configured ? new DaemonRpcClient({ socketPath: socketPath!, token: token!, timeoutMs: options.daemonTimeoutMs }) : undefined;
  state.daemon = daemon;
  registerGoalLoop(pi, daemon ? createDaemonGoalLoop(daemon) : unavailable("goal/loop"));
  registerControlTaskTools(pi, daemon ? createDaemonControlTask(daemon) : unavailable("goal/task/run"));
  pi.registerTool?.(withDefaultToolRenderer({ name: "daemon_status", label: "daemon status", description: "Show production daemon configuration status.", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: JSON.stringify({ status: state.daemonStatus, configured }) }], details: { status: state.daemonStatus, configured } }) }));
  runtimeByPi.set(pi as object, state);

  // Establish prompt and resource policy before feature registration.
  registerSwarmPrompt(pi);
  state.policy = new Policy({ workspace: cwd, allowedTools: options.allowedTools, allowMutation: options.allowMutation, allowNetwork: options.allowNetwork });

  // Dedicated extensions own tool registration; the umbrella only owns shared
  // policy/identity and must never register duplicate tools.

  state.agents = registerAgents(pi, new AgentManager({ cwd, concurrency: 4, runner: createPiRunner(pi) }));
  pi.on?.("session_shutdown", () => { state.mcp?.close(); state.daemon?.close(); });
  const manifests = pi.mcpManifests ?? [];
  if (Array.isArray(manifests)) state.mcp = new MCPManager(manifests, { closed: options.closed ?? true, registerTool: tool => pi.registerTool(withDefaultToolRenderer(tool as any)) });
  pi.registerCommand?.("swarm-runtime", { description: "Inspect integrated Pi-Swarm runtime", handler: async (_args: string, ctx: any) => ctx.ui?.notify?.(`Pi-Swarm runtime active at ${cwd}; mcp=${manifests.length}`, "info") });
  return state;
}

export default function swarmRuntimeExtension(pi: Pi) { return registerSwarmRuntime(pi); }
