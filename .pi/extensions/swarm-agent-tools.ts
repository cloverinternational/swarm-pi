import { AgentManager, createPiRunner } from "../../agents/src/index.ts";
import { applySwarmSurface } from "../lib/swarm-tool-surface.ts";
import { AGENT_MANAGER_SYMBOL, AGENT_TOOLS_SYMBOL, SwarmAgentTools, type ToolResult } from "../lib/swarm-agent-tools.ts";
import { newErrorID } from "../lib/swarm-bash.ts";

type Pi = any;
const registrations = new WeakSet<object>();
const methods: Record<string, keyof SwarmAgentTools> = {
  BackgroundTask: "backgroundTask", Subagent: "subagent", SubagentOutput: "taskOutput", TaskOutput: "taskOutput",
  Delegate: "delegate", DelegateOutput: "delegateOutput", multi_agent_wait: "multiWait", wait_for_agent: "waitForAgent",
};
// Pi flags a tool result as failed only when execute() throws; the message
// becomes the result content verbatim (docs/extensions.md "Signaling errors").
const text = (r: ToolResult) => { if (r.isError) throw new Error(r.text); return { content: [{ type: "text", text: r.text }], details: r.details ?? {} }; };

export function registerSwarmAgentTools(pi: Pi, options: { manager?: AgentManager; cwd?: string } = {}): SwarmAgentTools {
  const host = pi as Record<PropertyKey, any>;
  if (host[AGENT_TOOLS_SYMBOL]) return host[AGENT_TOOLS_SYMBOL];
  const manager = options.manager ?? host[AGENT_MANAGER_SYMBOL] ?? new AgentManager({ cwd: options.cwd ?? pi.getCwd?.() ?? process.cwd(), concurrency: 4, runner: createPiRunner(pi) });
  host[AGENT_MANAGER_SYMBOL] = manager;
  manager.addEventSink(event => {
    if (!event.background || typeof pi?.sendUserMessage !== "function") return;
    const result = event.result;
    const output = result.output !== undefined ? `\noutput: ${result.output}` : "";
    const error = result.error !== undefined ? `\nerror: ${result.error}` : "";
    pi.sendUserMessage(`[agent completed] id=${result.id} status=${result.status}${output}${error}`, { deliverAs: "followUp" });
  });
  const logic = new SwarmAgentTools(manager);
  host[AGENT_TOOLS_SYMBOL] = logic;
  if (registrations.has(pi as object)) return logic;
  registrations.add(pi as object);
  for (const [name, method] of Object.entries(methods)) {
    pi.registerTool?.(applySwarmSurface({
      name, label: name, description: "", parameters: {},
      async execute(_callId: string, params: Record<string, unknown>) {
        try { return text(await (logic[method] as any).call(logic, params)); }
        catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          if (message.startsWith("Error executing ")) throw err;
          throw new Error(`Error executing ${name}: ${message} (error_id=${newErrorID()})`);
        }
      },
    }));
  }
  return logic;
}

export default function swarmAgentToolsExtension(pi: Pi): void { registerSwarmAgentTools(pi); }
