import { homedir } from "node:os";
import { join } from "node:path";
import {
  PlanModeController,
  type PlanApproval,
  type PlanFileConfig,
  enterPlanToolResult,
  exitPlanApprovedResult,
  exitPlanNoPlanError,
  exitPlanRejectedResult,
  planFilePath,
  readPlanFile,
  readSubmittedPlan,
  validatePlanApprovalResponse,
  validatePlanContent,
  writePlanFile,
} from "../lib/swarm-plan-mode.ts";
import { randomUUID } from "node:crypto";
import { newErrorID } from "../lib/swarm-bash.ts";

/** Minimal Pi surface used by this adapter; intentionally structural for compatibility. */
export interface PlanModePi {
  getCwd?(): string;
  registerTool(tool: unknown): void;
  appendEntry?(type: string, data?: unknown): void;
  on?(event: string, handler: (event: any, ctx: any) => unknown): void;
  registerCommand?(name: string, spec: { description: string; handler: (args: string, ctx: any) => Promise<void> }): void;
}

const registrations = new WeakMap<object, PlanModeController>();
/** conversation.ProcessSessionID(): one uuid per process. */
const SESSION_KEY = Symbol.for("pi-swarm-process-session-id");
export const processSessionId = (): string => ((globalThis as any)[SESSION_KEY] ??= randomUUID());
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

/**
 * app_plan.go: the approval modal offers y (approve) / c (approve + clear
 * context) / n (reject → feedback input; Esc on the input goes back to the
 * choice). handlePlanRejected substitutes "Please revise the plan and try
 * again." for blank feedback BEFORE the broker answers, so the tool's own
 * "User rejected the plan…" default is unreachable from the TUI.
 */
export const PLAN_APPROVAL_CHOICES = ["y: Approve", "c: Approve + Clear Context", "n: Reject / Revise"] as const;
export const PLAN_REJECTED_DEFAULT_FEEDBACK = "Please revise the plan and try again.";
async function askApproval(ctx: any, plan: string): Promise<PlanApproval> {
  const ui = ctx?.ui;
  if (!ui?.select && !ui?.confirm) return { approved: true, editedPlan: plan, clearContext: false };
  for (;;) {
    let choice: string | undefined;
    if (ui.select) choice = await ui.select("Review the plan above and choose an action:", [...PLAN_APPROVAL_CHOICES]);
    else choice = (await ui.confirm("Approve Plan", "Approve this plan and begin implementation?")) ? PLAN_APPROVAL_CHOICES[0] : PLAN_APPROVAL_CHOICES[2];
    if (choice === undefined) throw new Error("plan approval cancelled");
    if (choice.startsWith("y")) return { approved: true, editedPlan: plan, clearContext: false };
    if (choice.startsWith("c")) return { approved: true, editedPlan: plan, clearContext: true };
    const feedback = ui.input ? await ui.input("Rejection feedback (press Enter to send, Esc to go back):") : "";
    if (feedback === undefined) continue; // Esc: back to the choice modal
    const trimmed = String(feedback).trim();
    return { approved: false, feedback: trimmed === "" ? PLAN_REJECTED_DEFAULT_FEEDBACK : trimmed };
  }
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
  // Swarm adds NO plan-mode system prompt (the broker flips only the App's
  // operating mode, never the SDK's) and the first-tool breakdown + simulation
  // hooks run inside the builtin hook pipeline (swarm-builtin-hooks.ts), which
  // reads this controller through the shared handle above.

  // plan.Config as the TUI builds it (sdk_integration.go): WorkDir =
  // workspace root, SessionID = conversation.ProcessSessionID() (a per-process
  // uuid), so the canonical copy lives in <SWARM_HOME|~/.swarm>/conversations/<id>/plan.md.
  const planConfig: PlanFileConfig = { workspace: cwd };
  const canonicalPlanPath = () => planFilePath(planConfig, processSessionId(), join(process.env.SWARM_HOME || join(homedir(), ".swarm"), "conversations"));

  pi.registerTool({
    name: "enter_plan_mode",
    label: "Enter Plan Mode",
    description: "Enter Plan Mode to explore, resolve user-dependent decisions, write a plan, and submit it for approval. Plan Mode does not change normal permissions.",
    parameters: schema({}),
    async execute() {
      // PlanModeEntered runs in the pre-tool hook (plan_mode_first_tool.go);
      // the controller's beforeTool already flipped state before we get here
      // when the builtin pipeline is installed, so enter() is idempotent.
      try { if (!controller.isActive()) controller.enter(); return textResult(enterPlanToolResult(cwd), controller.snapshotOf()); }
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
      // registry_impl.go runs ExitPlanModeTool.Validate BEFORE Execute and
      // reports failures as a Go error (sdkerr envelope, "Error executing …").
      const invalid = (message: string) => { throw new Error(`Error executing exit_plan_mode: validation failed for exit_plan_mode: ${message} (error_id=${newErrorID()})`); };
      if ("plan" in (params ?? {}) && typeof params.plan !== "string") invalid("plan must be a string");
      if ("plan_file" in (params ?? {}) && typeof params.plan_file !== "string") invalid("plan_file must be a string");
      if (typeof params?.plan === "string" && params.plan.trim() !== "" && typeof params?.plan_file === "string" && params.plan_file.trim() !== "") invalid("provide either plan or plan_file, not both");
      // tools.go ExitPlanModeTool.Execute, error for error. Every failure is a
      // tools.NewErrorResult("exit_plan_mode: …") — an IsError result, not a
      // Go error — so it reaches the model as plain text.
      const fail = (message: string) => errorResult(new Error(`exit_plan_mode: ${message}`));
      let planText = typeof params?.plan === "string" ? params.plan.trim() : "";
      const planFile = typeof params?.plan_file === "string" ? params.plan_file.trim() : "";
      if (planFile !== "") {
        try { planText = readSubmittedPlan(planConfig, planFile); }
        catch (error) { return fail(error instanceof Error ? error.message : String(error)); }
      }
      // Compatibility fallback: the canonical session copy from an earlier submission.
      if (planText === "") { try { planText = readPlanFile(canonicalPlanPath()); } catch { /* ReadPlanFile errors are ignored */ } }
      if (planText === "") return errorResult(new Error(exitPlanNoPlanError(canonicalPlanPath())));
      try { planText = validatePlanContent(planText, "plan"); }
      catch (error) { return fail(error instanceof Error ? error.message : String(error)); }
      try { writePlanFile(canonicalPlanPath(), planText); }
      catch (error) { return fail(`copy plan to conversation storage: ${error instanceof Error ? error.message : String(error)}`); }
      let response: PlanApproval;
      try {
        // Swarm marks the exit only after actual approval (PlanModeExited);
        // a rejection keeps plan mode active.
        if (controller.snapshotOf().state === "active") controller.beginApproval();
        response = validatePlanApprovalResponse(await askApproval(ctx, planText));
      } catch (error) { return fail(`approval request failed: ${error instanceof Error ? error.message : String(error)}`); }
      if (!response.approved) {
        controller.finishApproval(response);
        return textResult(exitPlanRejectedResult(response.feedback ?? ""), controller.snapshotOf());
      }
      let finalPlan = planText;
      if (response.editedPlan && response.editedPlan !== planText) {
        try { finalPlan = validatePlanContent(response.editedPlan, "edited plan"); }
        catch (error) { return fail(error instanceof Error ? error.message : String(error)); }
        try { writePlanFile(canonicalPlanPath(), finalPlan); }
        catch (error) { return fail(`persist approved plan: ${error instanceof Error ? error.message : String(error)}`); }
      }
      controller.finishApproval(response);
      return textResult(exitPlanApprovedResult(finalPlan, response.clearContext ?? false), controller.snapshotOf());
    },
  });

  pi.registerCommand?.("plan-mode", { description: "Inspect current Swarm Plan Mode state", handler: async (_args, ctx) => { ctx.ui?.notify?.(JSON.stringify(controller.snapshotOf(), null, 2), "info"); } });
  return controller;
}

export default function swarmPlanModeExtension(pi: PlanModePi): void { registerPlanMode(pi); }
