import type { ControlPlaneInspection } from "../../runtime-contracts/src/control-plane.ts";

type Pi = { registerTool(tool: unknown): void; registerCommand?(name: string, spec: { description: string; handler: (args: string, ctx: any) => Promise<void> }): void };
const schema = { type: "object", additionalProperties: false, properties: {} };
const result = (value: unknown) => ({ content: [{ type: "text", text: JSON.stringify(value, null, 2) }], details: value });

/** Read-only bounded control-plane view. Mutations remain RPC/control-plane concerns. */
export function registerControlPanel(pi: Pi, inspection: ControlPlaneInspection): void {
  const snapshot = async () => {
    const [agents, jobs] = await Promise.all([inspection.listAgents(), inspection.listJobs()]);
    return {
      agents: agents.map(({ id, kind, workspace, status, lastHeartbeatAt, revision }) => ({ id, kind, workspace, status, lastHeartbeatAt, revision })),
      jobs: jobs.map(({ id, request, state, attempt, createdAt, updatedAt }) => ({ id, target: request.target, state, attempt, createdAt, updatedAt })),
    };
  };
  pi.registerTool({ name: "control_plane_status", label: "Control plane status", description: "Show a bounded read-only control-plane dashboard without exposing prompts.", parameters: schema, async execute() { return result(await snapshot()); } });
  pi.registerCommand?.("control-panel", { description: "Show the durable control-plane dashboard", handler: async (_args, ctx) => { const view = await snapshot(); ctx.ui?.notify?.(`Control plane: ${view.agents.length} agents, ${view.jobs.length} jobs\n${view.jobs.map(j => `${j.id} · ${j.state}`).join("\n") || "No jobs"}`, "info"); } });
}
export default function controlPanelExtension(pi: Pi, inspection?: ControlPlaneInspection): void { if (inspection) registerControlPanel(pi, inspection); }
