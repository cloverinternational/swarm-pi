import { AgentManager, createPiRunner } from "../../../packages/tools/agents/src/index.ts";
import { applySwarmSurface } from "../../lib/runtime/swarm-tool-surface.ts";
import { AGENT_MANAGER_SYMBOL, AGENT_TOOLS_SYMBOL, AgentToolValidationError, SwarmAgentTools, WAIT_FOR_AGENT_BACKGROUND, type ToolResult } from "../../lib/tools/swarm-agent-tools.ts";
import { newErrorID } from "../../lib/tools/swarm-bash.ts";
import { createSessionWakeup } from "../../lib/runtime/session-wakeup.ts";

type Pi = any;
const registrations = new WeakSet<object>();
const methods: Record<string, keyof SwarmAgentTools> = {
  BackgroundTask: "backgroundTask", Subagent: "subagent", SubagentOutput: "taskOutput", TaskOutput: "taskOutput",
  Delegate: "delegate", DelegateOutput: "delegateOutput", multi_agent_wait: "multiWait", wait_for_agent: "waitForAgent",
};
// Pi flags a tool result as failed only when execute() throws; the message
// becomes the result content verbatim (docs/extensions.md "Signaling errors").
// A Go ToolResult{IsError: true} reaches the wire as its Output verbatim (no
// "Error executing" envelope) while still counting as a failure for hooks;
// a returned Go error gets the agent_tools.go + sdkerr envelope.
class PlainToolFailure extends Error {}
const text = (r: ToolResult) => { if (r.isError) throw new PlainToolFailure(r.text); return { content: [{ type: "text", text: r.text }], details: r.details ?? {} }; };

export function registerSwarmAgentTools(pi: Pi, options: { manager?: AgentManager; cwd?: string } = {}): SwarmAgentTools {
  const host = pi as Record<PropertyKey, any>;
  if (host[AGENT_TOOLS_SYMBOL]) return host[AGENT_TOOLS_SYMBOL];
  const manager = options.manager ?? host[AGENT_MANAGER_SYMBOL] ?? new AgentManager({ cwd: options.cwd ?? pi.getCwd?.() ?? process.cwd(), concurrency: 4, runner: createPiRunner(pi) });
  host[AGENT_MANAGER_SYMBOL] = manager;
  const logic = new SwarmAgentTools(manager, options.cwd ?? pi.getCwd?.() ?? process.cwd(), pi.getSessionId?.() ?? pi.sessionId ?? "");
  const wake = createSessionWakeup(pi);
  pi.on?.("session_start", (_event: unknown, ctx: any) => {
    logic.setSessionId(ctx?.sessionManager?.getSessionId?.() ?? ctx?.sessionId ?? pi.getSessionId?.() ?? pi.sessionId ?? "");
  });
  logic.setBackgroundCompletionHandler(async result => {
    const summary = result.status === "completed" ? "completed" : result.status;
    const message = { customType: "swarm-agent-complete", content: `[agent completed] id=${result.id} status=${summary}`, display: true, details: { agent_id: result.id, status: result.status, background: true } };
    await wake.send(message);
  }, () => wake.capture());
  (globalThis as any)[WAIT_FOR_AGENT_BACKGROUND] = () => logic.requestWaitBackground();
  host[AGENT_TOOLS_SYMBOL] = logic;
  if (registrations.has(pi as object)) return logic;
  registrations.add(pi as object);
  for (const [name, method] of Object.entries(methods)) {
    pi.registerTool?.(applySwarmSurface({
      name, label: name, description: "", parameters: {},
      async execute(callId: string, params: Record<string, unknown>) {
        try { return text(await (logic[method] as any).call(logic, params, callId)); }
        catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          if (message.startsWith("Error executing ") || err instanceof PlainToolFailure) throw err;
          // registry_impl.go: Validate failures are sdkerr-wrapped a second time.
          if (err instanceof AgentToolValidationError) throw new Error(`Error executing ${name}: validation failed for ${name}: ${message} (error_id=${newErrorID()}) (error_id=${newErrorID()})`);
          throw new Error(`Error executing ${name}: ${message} (error_id=${newErrorID()})`);
        }
      },
    }));
  }
  return logic;
}

export default function swarmAgentToolsExtension(pi: Pi): void { registerSwarmAgentTools(pi); }
