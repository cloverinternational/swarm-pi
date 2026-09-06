import {
  PlanModeController,
  type PlanApproval,
  type PlanFileConfig,
  readSubmittedPlan,
  planModeSystemPrompt,
  enterPlanToolResult,
  validatePlanApprovalResponse,
  DEFAULT_PLAN_FILE,
} from "../lib/swarm-plan-mode.ts";

/** Minimal Pi surface used by this adapter; intentionally structural for compatibility. */
export interface PlanModePi {
  getCwd?(): string;
  registerTool(tool: unknown): void;
  appendEntry?(type: string, data?: unknown): void;
  on?(event: string, handler: (event: any, ctx: any) => unknown): void;
  registerCommand?(name: string, spec: { description: string; handler: (args: string, ctx: any) => Promise<void> }): void;
}

const registrations = new WeakMap<object, PlanModeController>();
// Extensions receive distinct `pi` facades, so cross-extension readers (e.g.
// the transport-parity capability manifest) resolve the live controller via a
// process-wide handle instead of re-registering plan-mode tools.
const SHARED_KEY = Symbol.for("pi-swarm-plan-mode-controller");
export function currentPlanModeController(): PlanModeController | undefined {
  return (globalThis as any)[SHARED_KEY];
}
const schema = (properties: Record<string, unknown>, required: string[] = []) => ({ type: "object", properties, required, additionalProperties: false });
const textResult = (text: string, details: unknown = {}) => ({ content: [{ type: "text", text }], details });
const errorResult = (error: unknown) => ({ content: [{ type: "text", text: error instanceof Error ? error.message : String(error) }], isError: true, details: {} });

function workspace(pi: PlanModePi): string { return pi.getCwd?.() ?? process.cwd(); }
function save(pi: PlanModePi, controller: PlanModeController): void { pi.appendEntry?.("pi-swarm-plan-mode", controller.snapshotOf()); }
function restore(pi: PlanModePi, controller: PlanModeController, ctx: any): void {
  const entries = ctx?.sessionManager?.getEntries?.() ?? [];
  const found = [...entries].reverse().find((entry: any) => entry?.type === "pi-swarm-plan-mode" || entry?.customType === "pi-swarm-plan-mode");
  const snapshot = found?.data ?? found?.value;
  if (snapshot?.state) controller.hydrate(snapshot);
  else controller.hydrate(controller.snapshotOf());
}

async function askApproval(ctx: any, plan: string): Promise<PlanApproval> {
  const ui = ctx?.ui;
  if (!ui?.confirm) return { approved: true, editedPlan: plan, clearContext: false };
  const approved = await ui.confirm("Approve Plan", "Approve this plan and begin implementation?");
  if (approved) {
    const clearContext = ui.confirm ? await ui.confirm("Compact Context", "Compact context before implementation?") : false;
    return { approved: true, editedPlan: plan, clearContext };
  }
  const feedback = ui.input ? await ui.input("Plan rejected. What should be revised?") : "User rejected the plan. Please revise your approach.";
  return { approved: false, feedback };
}

export function registerPlanMode(pi: PlanModePi): PlanModeController {
  const existing = registrations.get(pi as object);
  if (existing) return existing;
  const cwd = workspace(pi);
  const controller = new PlanModeController({
    onEnter: () => save(pi, controller),
    onExit: () => save(pi, controller),
    onInteraction: () => save(pi, controller),
    onApprovalState: () => save(pi, controller),
  });
  registrations.set(pi as object, controller);
  (globalThis as any)[SHARED_KEY] = controller;

  pi.on?.("session_start", (_event, ctx) => restore(pi, controller, ctx));
  pi.on?.("before_agent_start", (event) => {
    if (!controller.isActive()) return undefined;
    const current = typeof event.systemPrompt === "string" ? event.systemPrompt : "";
    const addition = planModeSystemPrompt(cwd);
    return { systemPrompt: current.includes("## Plan Mode - ACTIVE") ? current : `${current}\n\n${addition}`.trim() };
  });
  pi.on?.("tool_call", (event) => {
    const result = controller.beforeTool(event?.toolName ?? event?.tool_name ?? "");
    if (result.inject) {
      return { message: { customType: "pi-swarm-plan-breakdown", content: result.inject, display: false } };
    }
    return undefined;
  });

  pi.registerTool({
    name: "enter_plan_mode",
    label: "Enter Plan Mode",
    description: "Enter Plan Mode to explore, resolve user-dependent decisions, write a plan, and submit it for approval. Plan Mode does not change normal permissions.",
    parameters: schema({}),
    async execute() {
      try { controller.enter(); return textResult(enterPlanToolResult(cwd), controller.snapshotOf()); }
      catch (error) { return errorResult(error); }
    },
  });

  pi.registerTool({
    name: "exit_plan_mode",
    label: "Exit Plan Mode",
    description: "Submit a workspace-local Markdown plan for approval. Use plan_file or inline plan, never both. Rejection returns feedback and keeps Plan Mode active.",
    parameters: schema({
      plan: { type: "string", description: "Complete Markdown plan; mutually exclusive with plan_file." },
      plan_file: { type: "string", description: "Workspace-local Markdown/text path; mutually exclusive with plan." },
    }),
    async execute(_id: string, params: any, _signal: AbortSignal, _onUpdate: unknown, ctx: any) {
      try {
        const inline = typeof params?.plan === "string" ? params.plan.trim() : "";
        const file = typeof params?.plan_file === "string" ? params.plan_file.trim() : "";
        if (inline && file) throw new Error("provide either plan or plan_file, not both");
        let plan = inline;
        if (file) plan = readSubmittedPlan({ workspace: cwd } satisfies PlanFileConfig, file);
        if (!plan) {
          const fallback = readSubmittedPlan({ workspace: cwd, planFileName: DEFAULT_PLAN_FILE }, DEFAULT_PLAN_FILE);
          plan = fallback;
        }
        if (!plan) throw new Error("no plan found; provide plan_file or plan");
        controller.beginApproval();
        const response = validatePlanApprovalResponse(await askApproval(ctx, plan));
        controller.finishApproval(response);
        if (!response.approved) return textResult(JSON.stringify({ approved: false, feedback: response.feedback, message: "Plan rejected. Revise your plan and call exit_plan_mode again." }), controller.snapshotOf());
        return textResult(JSON.stringify({ approved: true, edited_plan: response.editedPlan ?? plan, clear_context: response.clearContext ?? false, message: "Plan approved. You may now begin implementing. Follow the approved plan precisely." }), controller.snapshotOf());
      } catch (error) { return errorResult(error); }
    },
  });

  pi.registerCommand?.("plan-mode", { description: "Inspect current Swarm Plan Mode state", handler: async (_args, ctx) => { ctx.ui?.notify?.(JSON.stringify(controller.snapshotOf(), null, 2), "info"); } });
  return controller;
}

export default function swarmPlanModeExtension(pi: PlanModePi): void { registerPlanMode(pi); }
