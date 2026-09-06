/**
 * Modular Swarm-compatible Plan Mode core.
 *
 * This module is deliberately host-independent. Pi/TUI adapters should depend on
 * these contracts; this core must not import Pi, BubbleTea, or filesystem UI code.
 * The lifecycle mirrors upstream/swarm-sdk/internal/plan and its first-tool hook.
 */
import { createHash } from "node:crypto";
import { existsSync, lstatSync, readFileSync, realpathSync, statSync } from "node:fs";
import { isAbsolute, relative, resolve } from "node:path";

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

const sha256 = (text: string) => `sha256:${createHash("sha256").update(text).digest("hex")}`;
const normalizeTool = (name: string) => name.toLowerCase().replaceAll("_", "");

/** Exact upstream-style system guidance, with workspace substituted at runtime. */
export function planModeSystemPrompt(workspace: string): string {
  return `## Plan Mode - ACTIVE

You are in PLAN MODE. This is an approval ceremony for exploring a change,
resolving decisions, and presenting a concrete implementation plan.

Plan mode does not change tool authorization. Normal workspace, task, credential,
permission, and safety controls continue to apply.

### Your Only Job Right Now
Explore → Resolve decisions with the user → Document your plan → Call exit_plan_mode for approval.

### Requirement Discovery (MANDATORY)

Before writing your plan, you MUST resolve the decision tree with the user:

1. Identify the decision tree: From your exploration, list every decision that needs to be made, noting which decisions depend on others.
2. Walk the tree depth-first: Start with the most upstream decision. Resolve it before moving to decisions that depend on it.
3. Ask one question at a time: Use ask_user_question for each unresolved decision. Never batch multiple questions.
4. Provide your recommended answer: For each question, include your recommendation based on what you found in the codebase. The user reviews your draft — they don't write from scratch.
5. Self-service where possible: If a question can be answered by exploring the codebase, explore it instead of asking the user. Only ask the user questions that require their judgment.

A decision is "resolved" when you and the user agree on the answer. Do not write the plan until all critical decisions are resolved.

### Plan File Workflow (IMPORTANT)
1. Explore the relevant code and resolve material decisions first
2. Write the agreed plan to a meaningful text or Markdown file inside the workspace
3. Call exit_plan_mode with plan_file set to that file's path
4. The tool copies the submitted content into conversation storage before approval

Workspace root: ${workspace}

### What to Include in Your Plan
- **Overview**: What is being built/changed and why
- **Files to modify**: Specific files and the changes needed
- **Implementation steps**: Ordered, concrete actions
- **Risks/considerations**: Edge cases, breaking changes, tests needed

### Visual Decisions During Planning

For UI / layout / design / architectural trade-offs, prefer ask_user_question with
type="visual_choice" over describing options in prose.

### Tool Behavior
Plan mode itself neither grants nor removes tool permissions. Use tools according
to their ordinary schemas and the active workspace, task, permission, and safety policies.

---
`;
}

/** Exact first-tool decomposition contract used by the upstream hook. */
export const problemBreakdownPrompt = `[PLAN MODE — PROBLEM BREAKDOWN REQUIRED]

BEFORE YOU EXECUTE ANY TOOL, BREAK DOWN THE PROBLEM.

You are in PLAN MODE. Before working on the problem, break it down almost as if
it was a beginning to CS intro course where you learn how to break down problems
based on what you need, what you expect and how logic works at the lowest level.

For each component document:

WHAT I NEED
- every input, dependency, and precondition
- what must exist before this can work
- what state must be initialized

WHAT I EXPECT
- expected output for each step
- what success looks like
- failure modes and how to detect them

HOW LOGIC WORKS (LOWEST LEVEL)
- trace data flow step-by-step
- identify transformations and invariants
- state assumptions
- identify where the chain could break

Then create a dependency tree. For every decision record:
DECISION, DEPENDS ON, CAN RESOLVE FROM CODEBASE, RECOMMENDED ANSWER, STATUS.
Walk the tree depth-first. For each unresolved decision that cannot be answered
from the codebase, ask one focused ask_user_question with your recommendation,
wait for the response, mark it resolved, and continue.

Do not write the plan until all critical decisions reach resolved status.
Plan mode is an approval ceremony, not a permission boundary. Normal workspace,
task, credential, permission, and safety controls apply exactly as outside plan mode.
`;

export class PlanModeController {
  private snapshot: PlanModeSnapshot;
  private readonly events: PlanModeEvents;

  constructor(events: PlanModeEvents = {}, initial?: Partial<PlanModeSnapshot>) {
    this.events = events;
    this.snapshot = {
      state: "idle",
      firstToolUsed: false,
      everUsed: false,
      planIdHistory: [],
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

function assertContained(workspace: string, candidate: string): void {
  const root = realpathSync(workspace);
  const resolved = resolve(candidate);
  const existing = existsSync(resolved) ? realpathSync(resolved) : resolved;
  const r = relative(root, existing);
  if (r === ".." || r.startsWith(`..${"/"}`) || isAbsolute(r)) throw new Error(`plan file is outside workspace: ${candidate}`);
}

/** Read a workspace-local submitted plan with upstream containment/size rules. */
export function readSubmittedPlan(config: PlanFileConfig, submittedPath: string): string {
  const input = submittedPath.trim();
  if (!input) throw new Error("plan file path is empty");
  const candidate = isAbsolute(input) ? resolve(input) : resolve(config.workspace, input);
  assertContained(config.workspace, candidate);
  if (!existsSync(candidate)) throw new Error(`plan file not found: ${input}`);
  const info = lstatSync(candidate);
  if (!info.isFile()) throw new Error(`plan file is not a regular file: ${input}`);
  if (info.size > MAX_SUBMITTED_PLAN_BYTES) throw new Error(`plan file exceeds ${MAX_SUBMITTED_PLAN_BYTES} bytes`);
  const content = readFileSync(candidate, "utf8");
  if (Buffer.byteLength(content, "utf8") > MAX_SUBMITTED_PLAN_BYTES) throw new Error(`plan file exceeds ${MAX_SUBMITTED_PLAN_BYTES} bytes`);
  if (!content.trim()) throw new Error("plan content is empty");
  return content.trim();
}

export function planDigest(content: string): string { return sha256(content); }

export interface PlanApprovalBroker { requestApproval(plan: string): Promise<PlanApproval>; }
export class HeadlessPlanApprovalBroker implements PlanApprovalBroker {
  async requestApproval(plan: string): Promise<PlanApproval> { return { approved: true, editedPlan: plan, clearContext: false }; }
}

export function enterPlanToolResult(workspace: string): string {
  return [`# PLAN MODE — ACTIVE`, ``, `You are now in PLAN MODE. Explore, resolve the decision tree with the user, write a local plan, then submit it for approval.`, ``, `Plan mode is an approval ceremony, not a permission boundary. Normal workspace, task, credential, and safety controls continue to apply.`, ``, `1. Explore and resolve material decisions first`, `2. Write the agreed plan inside: ${workspace}`, `3. Call exit_plan_mode with plan_file`, ``, `Ask one question at a time with your recommended answer. If the codebase can answer a question, explore it instead of asking.`].join("\\n");
}

export function validatePlanApprovalResponse(response: PlanApproval): PlanApproval {
  if (typeof response.approved !== "boolean") throw new Error("approval response must include approved boolean");
  if (!response.approved && !(response.feedback ?? "").trim()) return { ...response, feedback: "User rejected the plan. Please revise your approach." };
  return response;
}
