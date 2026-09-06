/**
 * Modular Swarm-compatible Plan Mode core.
 *
 * This module is deliberately host-independent. Pi/TUI adapters should depend on
 * these contracts; this core must not import Pi, BubbleTea, or filesystem UI code.
 * The lifecycle mirrors vendor/swarm-sdk/internal/plan and its first-tool hook.
 */
import { createHash } from "node:crypto";
import { closeSync, fstatSync, lstatSync, mkdirSync, openSync, readFileSync, readlinkSync, readSync, renameSync, writeFileSync } from "node:fs";
import { basename, dirname, isAbsolute, join, relative, resolve } from "node:path";
import { PROBLEM_BREAKDOWN_PROMPT, SIMULATION_REMINDER_MESSAGE } from "./swarm-plan-mode-texts.ts";
import { goJSON } from "./swarm-bgprocess.ts";

export type PlanModeState = "idle" | "active" | "awaiting_approval";
export type PlanApproval = {
  approved: boolean;
  editedPlan?: string;
  clearContext?: boolean;
  feedback?: string;
};

export interface PlanModeSnapshot {
  state: PlanModeState;
  firstToolUsed: boolean;
  everUsed: boolean;
  lastEntryAt?: string;
  planId?: string;
  planIdHistory: string[];
  interactionOccurred: boolean;
}

export interface PlanModeEvents {
  onEnter?: (snapshot: PlanModeSnapshot) => void;
  onExit?: (snapshot: PlanModeSnapshot) => void;
  onInteraction?: (snapshot: PlanModeSnapshot) => void;
  onApprovalState?: (state: PlanModeState) => void;
}

export const DEFAULT_PLAN_FILE = "plan.md";
export const MAX_SUBMITTED_PLAN_BYTES = 1 << 20;
/** hooks/builtin: the SDK-registered hook name (Hook.Name()) and the direct-call alias. */
export const PLAN_MODE_FIRST_TOOL_HOOK = "plan-mode-first-tool-hook";
export const SIMULATION_HOOK = "simulation";
/** simulation.go PlanExitDetected — exact-case tool names. */
export const PLAN_EXIT_TOOLS: readonly string[] = ["plan", "Plan", "enter_plan_mode", "exit_plan_mode", "create_plan", "TodoWrite", "task_create"];
export const planExitDetected = (toolName: string) => PLAN_EXIT_TOOLS.includes(toolName);
export const simulationReminderMessage = () => SIMULATION_REMINDER_MESSAGE;

const sha256 = (text: string) => `sha256:${createHash("sha256").update(text).digest("hex")}`;
const normalizeTool = (name: string) => name.toLowerCase().replaceAll("_", "");

/**
 * plan_mode_first_tool.go ProblemBreakdownPrompt — byte-exact (generated).
 * Swarm has NO plan-mode system-prompt addition: the TUI broker flips only the
 * App's operating mode, never the SDK's, so neither a mode SystemInstruction
 * nor a changed capability manifest reaches the wire.
 */
export const problemBreakdownPrompt = PROBLEM_BREAKDOWN_PROMPT;

export class PlanModeController {
  private snapshot: PlanModeSnapshot;
  private readonly events: PlanModeEvents;

  constructor(events: PlanModeEvents = {}, initial?: Partial<PlanModeSnapshot>) {
    this.events = events;
    this.snapshot = {
      state: "idle",
      firstToolUsed: false,
      everUsed: false,
      interactionOccurred: false,
      ...initial,
      planIdHistory: [...(initial?.planIdHistory ?? [])],
    };
  }

  snapshotOf(): PlanModeSnapshot { return { ...this.snapshot, planIdHistory: [...this.snapshot.planIdHistory] }; }
  hydrate(snapshot: PlanModeSnapshot): void { this.snapshot = { ...snapshot, planIdHistory: [...snapshot.planIdHistory] }; }
  isActive(): boolean { return this.snapshot.state === "active" || this.snapshot.state === "awaiting_approval"; }
  currentPlanId(): string | undefined { return this.snapshot.planId; }

  enter(): PlanModeSnapshot {
    const id = `plan-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
    this.snapshot = { ...this.snapshot, state: "active", firstToolUsed: false, everUsed: true, lastEntryAt: new Date().toISOString(), planId: id, planIdHistory: [...this.snapshot.planIdHistory, id], interactionOccurred: false };
    this.events.onEnter?.(this.snapshotOf());
    this.events.onApprovalState?.("active");
    return this.snapshotOf();
  }

  /** Returns the one-shot breakdown injection for the first post-enter tool. */
  beforeTool(toolName: string): { inject?: string; snapshot: PlanModeSnapshot } {
    const normalized = normalizeTool(toolName);
    if (normalized === "enterplanmode") return { snapshot: this.snapshotOf() };
    if (normalized === "askuserquestion") {
      if (this.isActive()) {
        this.snapshot.interactionOccurred = true;
        this.events.onInteraction?.(this.snapshotOf());
      }
    }
    if (!this.isActive() || this.snapshot.firstToolUsed) return { snapshot: this.snapshotOf() };
    this.snapshot.firstToolUsed = true;
    return { inject: problemBreakdownPrompt, snapshot: this.snapshotOf() };
  }

  beginApproval(): PlanModeSnapshot {
    if (this.snapshot.state !== "active") throw new Error("exit_plan_mode requires active plan mode");
    this.snapshot.state = "awaiting_approval";
    this.events.onApprovalState?.("awaiting_approval");
    return this.snapshotOf();
  }

  finishApproval(response: PlanApproval): PlanModeSnapshot {
    if (response.approved) {
      this.snapshot = { ...this.snapshot, state: "idle", firstToolUsed: false, planId: undefined };
      this.events.onExit?.(this.snapshotOf());
      this.events.onApprovalState?.("idle");
    } else {
      this.snapshot = { ...this.snapshot, state: "active" };
      this.events.onApprovalState?.("active");
    }
    return this.snapshotOf();
  }
}


export interface PlanFileConfig { workspace: string; planFileName?: string; storagePath?: string; }

/** Go os.PathError text for the syscalls plan.go reaches. */
const goPathError = (op: string, path: string, error: any) => new Error(`${op} ${path}: ${error?.code === "ENOENT" ? "no such file or directory" : error?.code === "EACCES" ? "permission denied" : error?.code === "ENOTDIR" ? "not a directory" : error?.code === "EISDIR" ? "is a directory" : error?.code === "ELOOP" ? "too many levels of symbolic links" : String(error?.message ?? error)}`);
const q = (value: string) => JSON.stringify(value);

/** plan.go ValidatePlanContent. */
export function validatePlanContent(content: string, source = "plan"): string {
  if (source === "") source = "plan";
  if (Buffer.byteLength(content, "utf8") > MAX_SUBMITTED_PLAN_BYTES) throw new Error(`${source} exceeds the ${MAX_SUBMITTED_PLAN_BYTES}-byte limit`);
  if (content.includes("\0") || content.includes("\uFFFD")) throw new Error(`${source} is not valid UTF-8 text`);
  const trimmed = content.trim();
  if (trimmed === "") throw new Error(`${source} is empty`);
  return trimmed;
}

/**
 * plan.go ReadSubmittedPlanFile: workspace-rooted (os.Root) open of the
 * submitted path with Go's exact error wording. Symlink escapes surface as
 * os.Root's "openat <path>: path escapes from parent".
 */
export function readSubmittedPlan(config: PlanFileConfig, submittedPath: string): string {
  const input = submittedPath.trim();
  if (!input) throw new Error("plan file path is empty");
  const rootPath = resolve(config.workspace);
  let candidate: string;
  if (!isAbsolute(input)) candidate = goClean(input);
  else candidate = relative(rootPath, resolve(input)) || ".";
  if (isAbsolute(candidate) || candidate === ".." || candidate.startsWith("../")) throw new Error(`plan file ${q(input)} is outside workspace ${q(rootPath)}`);
  const full = join(rootPath, candidate);
  // os.Root refuses any symlink that resolves outside the root.
  let cursor = rootPath;
  for (const part of candidate.split("/").filter(Boolean)) {
    cursor = join(cursor, part);
    let info; try { info = lstatSync(cursor); } catch (error) { throw new Error(`open plan file ${q(input)} inside workspace ${q(rootPath)}: ${goPathError("openat", candidate, error).message}`); }
    if (info.isSymbolicLink()) {
      let target; try { target = resolve(dirname(cursor), readlinkSync(cursor)); } catch { target = cursor; }
      const rel = relative(rootPath, target);
      if (rel === ".." || rel.startsWith("../") || isAbsolute(rel)) throw new Error(`open plan file ${q(input)} inside workspace ${q(rootPath)}: openat ${candidate}: path escapes from parent`);
    }
  }
  let fd: number;
  try { fd = openSync(full, "r"); } catch (error) { throw new Error(`open plan file ${q(input)} inside workspace ${q(rootPath)}: ${goPathError("openat", candidate, error).message}`); }
  try {
    const info = fstatSync(fd);
    if (!info.isFile()) throw new Error(`plan file ${q(input)} is not a regular file`);
    if (info.size > MAX_SUBMITTED_PLAN_BYTES) throw new Error(`plan file ${q(input)} exceeds the ${MAX_SUBMITTED_PLAN_BYTES}-byte limit`);
    const buffer = Buffer.alloc(MAX_SUBMITTED_PLAN_BYTES + 1);
    const n = readSync(fd, buffer, 0, buffer.length, 0);
    if (n > MAX_SUBMITTED_PLAN_BYTES) throw new Error(`plan file ${q(input)} exceeds the ${MAX_SUBMITTED_PLAN_BYTES}-byte limit`);
    return validatePlanContent(buffer.subarray(0, n).toString("utf8"), `plan file ${q(input)}`);
  } finally { closeSync(fd); }
}

/** Go filepath.Clean for slash paths. */
function goClean(value: string): string {
  const rooted = value.startsWith("/"); const parts: string[] = [];
  for (const part of value.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") { if (parts.length && parts[parts.length - 1] !== "..") parts.pop(); else if (!rooted) parts.push(".."); continue; }
    parts.push(part);
  }
  const out = (rooted ? "/" : "") + parts.join("/");
  return out === "" ? "." : out;
}

/** plan.go planFilePath: the canonical copy the user reviews (conversation storage when a session id is known). */
export function planFilePath(config: PlanFileConfig, sessionId?: string, conversationsDir?: string): string {
  const name = config.planFileName || DEFAULT_PLAN_FILE;
  if (sessionId && conversationsDir) return join(conversationsDir, sessionId, name);
  return join(config.workspace, name);
}
/** plan.go ReadPlanFile: "" when absent. */
export function readPlanFile(path: string): string {
  try { return readFileSync(path, "utf8").trim(); }
  catch (error: any) { if (error?.code === "ENOENT") return ""; throw error; }
}
/** plan.go WritePlanFile: MkdirAll(0755) + atomicfile.Write(content+"\n", 0644). */
export function writePlanFile(path: string, content: string): void {
  mkdirSync(dirname(path), { recursive: true, mode: 0o755 });
  const temporary = join(dirname(path), `.${basename(path)}.${process.pid}.${Date.now()}.tmp`);
  writeFileSync(temporary, content + "\n", { mode: 0o644 });
  renameSync(temporary, path);
}

export function planDigest(content: string): string { return sha256(content); }

export interface PlanApprovalBroker { requestApproval(plan: string): Promise<PlanApproval>; }
export class HeadlessPlanApprovalBroker implements PlanApprovalBroker {
  async requestApproval(plan: string): Promise<PlanApproval> { return { approved: true, editedPlan: plan, clearContext: false }; }
}

/** tools.go EnterPlanModeTool.Execute — byte-exact. */
export function enterPlanToolResult(workspace: string): string {
  return [
    "# PLAN MODE — ACTIVE",
    "",
    "You are now in PLAN MODE. Explore, resolve the decision tree with the user, write a local plan, then submit it for approval.",
    "",
    "## CEREMONY",
    "- Plan mode guides planning and approval; it does not change normal tool permissions",
    "- Ordinary workspace, task, credential, and safety controls still apply",
    `- Keep the plan file inside the active workspace: ${workspace}`,
    "",
    "## WORKFLOW",
    "1. Explore the relevant code and constraints",
    "- INTERROGATE: Resolve the decision tree with the user before writing the plan",
    "  - Ask one question at a time with your recommended answer",
    "  - If the codebase can answer it, explore it yourself instead of asking",
    "2. Write the agreed plan to a meaningful local Markdown or text file",
    "3. Call exit_plan_mode with plan_file set to that workspace-local path",
    "",
    "## CALLING exit_plan_mode",
    "- Recommended: pass plan_file with a relative or absolute workspace-local path",
    "- Compatibility: pass plan content directly in the plan parameter",
    "- The user approves/edits/rejects your plan before you implement",
  ].join("\n");
}

/** tools.go ExitPlanModeTool.Execute result maps — json.MarshalIndent(map) ⇒ sorted keys, HTML-escaped. */
export const exitPlanApprovedResult = (finalPlan: string, clearContext: boolean, headless = false) => goJSON({
  approved: true, clear_context: clearContext, edited_plan: finalPlan,
  message: headless ? "Plan approved (headless mode). Proceed with implementation." : "Plan approved. You may now begin implementing. Follow the approved plan precisely.",
}, 2);
export const exitPlanRejectedResult = (feedback: string) => goJSON({
  approved: false, feedback,
  message: `Plan rejected.\n\nFeedback: ${feedback}\n\nRevise your plan and call exit_plan_mode again.`,
}, 2);
export const exitPlanNoPlanError = (legacyPath: string) => `exit_plan_mode: no plan found. Provide plan_file for a workspace-local text file or provide the plan parameter directly. Legacy canonical path checked: ${legacyPath}.`;

export function validatePlanApprovalResponse(response: PlanApproval): PlanApproval {
  if (typeof response.approved !== "boolean") throw new Error("approval response must include approved boolean");
  if (!response.approved && !(response.feedback ?? "").trim()) return { ...response, feedback: "User rejected the plan. Please revise your approach." };
  return response;
}
