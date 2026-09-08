/**
 * 1:1 port of the Swarm builtin hooks that produce MODEL-VISIBLE context in
 * `swarm -p`, plus the delivery pipeline that decides where that context
 * lands on the wire. Everything here is pure state; the Pi extension
 * (.pi/lib/runtime/swarm-builtin-hooks.ts) feeds it tool_call / tool_result /
 * prompt events and applies the outputs.
 *
 * Sources of truth (mono/swarm-sdk):
 *   internal/hooks/nudge_budget.go                MetaNudgeBudget, NudgeSessionID
 *   internal/hooks/format.go                      FormatHookContext, NextReminderSeq
 *   internal/hooks/builtin/task_enforcement.go    advise/block gate (priority 95)
 *   internal/hooks/builtin/task_maintenance_reminder.go (priority 90)
 *   internal/skills/autogenskills/hook.go         LifecycleHook (after, priority 91)
 *   internal/skills/autogenskills/budget_enforcement.go (before, priority 90)
 *   swarm-tui/internal/chat/sdk_integration.go    TUI trigger config (5/90/3/5)
 *   swarm-tui/internal/chat/hooks/manager.go      registration + event emission
 *   internal/agent/agent_tools.go                 pre-context "\n\n---\n\n" join,
 *                                                 "Tool '%s' blocked by hook: %s",
 *                                                 post-context RoleUser message
 *
 * Hooks that are registered but wire-inert in headless mode are deliberately
 * NOT modelled: session-start / verification-protocol (outputs only logged
 * by main.go), complexity-detection (fire-and-forget), task-nudge (never on
 * turn one), plan-mode-first-tool (needs enter_plan_mode), protected-branch
 * (opt-in), post-acting / mid-session-recovery (read tool_output as a string
 * while the TUI manager passes a map — verified never to fire).
 */
import { formatHookContext, nextReminderSeq, wrapReminder } from "../policy/swarm-annoyance-nudge.ts";
import {
  isBashReadOnly, isBashTool, isPlanModeTool, isReadOnlyExplorationTool, isSkillTool,
  isTaskManagementTool, isUserInteractionTool, isCodeModeTool, normalizeToolName,
} from "./swarm-toolclass.ts";

// ---------------------------------------------------------------------------
// nudge_budget.go
// ---------------------------------------------------------------------------
export const META_NUDGE_TASK = 10, META_NUDGE_MAINTENANCE = 20, META_NUDGE_SKILL_REVIEW = 30, META_NUDGE_BLOCK = 40;
export const DEFAULT_NUDGE_INTERVAL = 5;

interface MetaNudgeState { turn: number; lastAt: number; lastSeq: number; lastPriority: number }

/** Limits all harness meta-nudges to one per turn window (per session). */
export class MetaNudgeBudget {
  private states = new Map<string, MetaNudgeState>();
  constructor(private readonly interval = DEFAULT_NUDGE_INTERVAL) { if (this.interval === 0) this.interval = DEFAULT_NUDGE_INTERVAL; }
  private state(session: string) { return this.states.get(session) ?? { turn: 0, lastAt: 0, lastSeq: 0, lastPriority: 0 }; }
  recordUserTurn(session: string): number {
    if (session.trim() === "") return 0;
    const s = this.state(session.trim());
    s.turn++; s.lastPriority = 0;
    this.states.set(session.trim(), s);
    return s.turn;
  }
  tryClaim(session: string, priority: number): [number, boolean] {
    if (session.trim() === "") {
      const s = this.state("__unscoped__"); s.lastSeq++; this.states.set("__unscoped__", s);
      return [s.lastSeq, true];
    }
    const key = session.trim();
    const s = this.state(key);
    if (s.turn === 0) return [0, false];
    if (s.lastAt !== 0 && s.turn - s.lastAt < this.interval) return [0, false];
    s.lastAt = s.turn; s.lastSeq++; s.lastPriority = priority;
    this.states.set(key, s);
    return [s.lastSeq, true];
  }
}

// ---------------------------------------------------------------------------
// Task snapshot the hooks read (ii.TodoManager subset)
// ---------------------------------------------------------------------------
export interface HookTask { id: string; subject: string; status: string; active?: boolean; category?: string; owner?: string; dependsOn?: string[] }
export const hasFocusedTask = (tasks: readonly HookTask[]) => tasks.some(t => t.status === "in_progress" && t.active === true);

export interface ToolCallEvent { toolName: string; params: any; toolCallId?: string }
export interface ToolResultEvent extends ToolCallEvent { failed: boolean; output: string }

export interface HookResult { message?: string; block?: boolean }
const CONTINUE: HookResult = {};

// ---------------------------------------------------------------------------
// task_enforcement.go
// ---------------------------------------------------------------------------
export type EnforcementMode = "advise" | "block" | "off";
export const TASK_ENFORCEMENT_HOOK = "task-enforcement-hook";

const HAS_PLAN_PHRASES = [
  "continue with the plan", "continue with plan", "follow the plan", "follow your plan", "stick to the plan",
  "as planned", "according to plan", "according to the plan", "like we planned", "like you planned",
  "per the plan", "per plan", "from the plan", "from your plan", "in the plan", "in your plan",
  "the plan is", "my plan is", "our plan is", "go ahead with the plan", "proceed with the plan",
  "execute the plan", "implement the plan", "do what you planned", "do what you said", "do what we discussed",
  "continue doing", "keep going", "carry on", "proceed", "resume",
];
export const userIndicatesExistingPlan = (message: string) => { const lower = message.toLowerCase(); return HAS_PLAN_PHRASES.some(p => lower.includes(p)); };

export class TaskEnforcementHook {
  readonly name = TASK_ENFORCEMENT_HOOK;
  private counter = 0;
  private planModeUsed = false;
  private userHasPlan = false;
  private lastUserMessage = "";
  constructor(private readonly mode: EnforcementMode = "advise") {}
  /** EventMessageAfterReceive with role user. */
  onUserMessage(content: string): void {
    if (content === "") return;
    this.lastUserMessage = content;
    if (userIndicatesExistingPlan(content)) this.userHasPlan = true;
  }
  /** EventToolBeforeExecute. */
  onToolBefore(event: ToolCallEvent, tasks: readonly HookTask[], budget: MetaNudgeBudget, session: string, isSubAgent = false): HookResult {
    if (this.mode === "off" || isSubAgent) return CONTINUE;
    const toolName = event.toolName;
    if (!toolName) return CONTINUE;
    if (isPlanModeTool(toolName)) this.planModeUsed = true;
    if (isTaskManagementTool(toolName) || isPlanModeTool(toolName) || isSkillTool(toolName) || isUserInteractionTool(toolName) || isCodeModeTool(toolName)) return CONTINUE;
    if (isReadOnlyExplorationTool(toolName)) return CONTINUE;
    if (isBashTool(toolName)) {
      const cmd = event.params?.command;
      if (typeof cmd === "string" && isBashReadOnly(cmd)) return CONTINUE;
    }
    if (hasFocusedTask(tasks)) { this.counter = 0; return CONTINUE; }
    this.counter++;
    if (this.mode === "block") return { block: true, message: wrapReminder(this.name, "block", nextReminderSeq(this.name), this.enforcementMessageContextual()) };
    const [seq, ok] = budget.tryClaim(session, META_NUDGE_BLOCK);
    if (!ok) return CONTINUE;
    return { message: wrapReminder(this.name, "nudge", seq, "No active task is focused; consider a TaskManage create/update before multi-step work.") };
  }
  private enforcementMessageContextual(): string {
    if (this.userHasPlan) return `[TASK ENFORCEMENT - BLOCKED]

═══════════════════════════════════════════════════════════════════════════════
              YOU CANNOT EXECUTE ANY TOOL WITHOUT A TASK
═══════════════════════════════════════════════════════════════════════════════

The user indicated they have a plan, but you need to CREATE TASKS to track it.

                              NO TASK = NO EXECUTION

═══════════════════════════════════════════════════════════════════════════════
                        CREATE TASKS FROM THE PLAN:
═══════════════════════════════════════════════════════════════════════════════

   ╔═══════════════════════════════════════════════════════════════════════╗
   ║  Use TaskManage create operations to break the plan into tasks       ║
   ╚═══════════════════════════════════════════════════════════════════════╝

═══════════════════════════════════════════════════════════════════════════════
                              EXAMPLE:
═══════════════════════════════════════════════════════════════════════════════

  TaskManage create operation:
    subject: "Implement step 1 of the plan"
    category: "acting"
    description: "From the plan: ..."

After creating your task(s), ALL tools will be unlocked.`;
    return `[TASK ENFORCEMENT - BLOCKED] YOU CANNOT EXECUTE ANY TOOL WITHOUT A TASK

This is a HARD REQUIREMENT. RECOMMENDED WORKFLOW:
  1. enter_plan_mode()  — for complex work
  2. TaskManage create operation
  3. TaskManage update operation with status="in_progress"
  4. Execute your tools
  5. TaskManage update operation with status="completed"

Categories: researching | planning | acting | verifying | debugging | documenting`;
  }
}

// ---------------------------------------------------------------------------
// task_maintenance_reminder.go
// ---------------------------------------------------------------------------
export const TASK_MAINTENANCE_HOOK = "task-maintenance-reminder-hook";
export const TOOL_EXECUTION_THRESHOLD = 8;
export const USER_MESSAGE_REMINDER_STRIDE = 5;

const sessionTaskStatuses = (tasks: readonly HookTask[]) => {
  const pending: HookTask[] = [], inProgress: HookTask[] = [];
  for (const t of tasks) {
    if ((t.owner ?? "") !== "") continue; // tm.ByOwner("")
    if (t.status === "pending") pending.push(t);
    else if (t.status === "in_progress") inProgress.push(t);
  }
  return { pending, inProgress };
};

export class TaskMaintenanceReminderHook {
  readonly name = TASK_MAINTENANCE_HOOK;
  private count = 0; private lastTaskID = ""; private remindedAt = 0; private msgCount = 0;
  private reset() { this.count = 0; this.remindedAt = 0; this.msgCount = 0; }
  private setTaskID(id: string) { if (this.lastTaskID !== id) { this.lastTaskID = id; this.count = 0; this.remindedAt = 0; this.msgCount = 0; } }
  private shouldRemindOnMessage() { const should = this.msgCount % USER_MESSAGE_REMINDER_STRIDE === 0; this.msgCount++; return should; }
  private shouldRemind() { if (this.count - this.remindedAt >= TOOL_EXECUTION_THRESHOLD) { this.remindedAt = this.count; return true; } return false; }
  private wrap(body: string, budget: MetaNudgeBudget, session: string): HookResult {
    const [seq, ok] = budget.tryClaim(session, META_NUDGE_MAINTENANCE);
    return ok ? { message: wrapReminder(this.name, "nudge", seq, body) } : CONTINUE;
  }
  onUserMessage(tasks: readonly HookTask[], budget: MetaNudgeBudget, session: string): HookResult {
    const { pending, inProgress } = sessionTaskStatuses(tasks);
    if (inProgress.length > 0) {
      this.setTaskID(inProgress[0].id);
      if (!this.shouldRemindOnMessage()) return CONTINUE;
      return this.wrap(this.inProgressReminderMessage(inProgress, pending), budget, session);
    }
    if (pending.length > 0) {
      if (!this.shouldRemindOnMessage()) return CONTINUE;
      return this.wrap(this.startTaskReminderMessage(pending), budget, session);
    }
    return CONTINUE;
  }
  onToolAfter(event: ToolResultEvent, tasks: readonly HookTask[], budget: MetaNudgeBudget, session: string): HookResult {
    if (!event.toolName) return CONTINUE;
    if (isTaskManagementTool(event.toolName) || isPlanModeTool(event.toolName)) return CONTINUE;
    const { pending, inProgress } = sessionTaskStatuses(tasks);
    if (inProgress.length === 0) { this.reset(); return CONTINUE; }
    this.setTaskID(inProgress[0].id);
    const count = ++this.count;
    if (this.shouldRemind()) return this.wrap(this.expandedWorkReminderMessage(count, inProgress[0], pending), budget, session);
    return CONTINUE;
  }
  private inProgressReminderMessage(inProgress: HookTask[], pending: HookTask[]): string {
    let sb = "[Task Maintenance Reminder]\n\n";
    if (inProgress.length === 1) sb += `You have 1 task in progress: ${inProgress[0].subject}\n`;
    else { sb += `You have ${inProgress.length} tasks in progress.\n`; for (const t of inProgress) sb += `  - ${t.subject}\n`; }
    if (pending.length > 0) sb += `\n${pending.length} task(s) pending after current work.\n`;
    sb += "\nRemember to:\n  - Mark tasks completed when done (TaskManage update)\n  - Add new tasks if work expands\n  - Check tasks periodically with a TaskManage list operation";
    return sb;
  }
  private startTaskReminderMessage(pending: HookTask[]): string {
    let sb = `[Task Reminder]\n\nYou have ${pending.length} pending task(s) but none in progress.\n\nAvailable tasks:\n`;
    pending.forEach((t, i) => {
      if (i >= 3) return;
      const statusByID = new Map(pending.map(p => [p.id, p.status]));
      const blockers = (t.dependsOn ?? []).filter(dep => statusByID.has(dep) && statusByID.get(dep) !== "completed");
      sb += blockers.length > 0 ? `  - ${t.subject} (blocked by: ${blockers.join(", ")})\n` : `  - ${t.subject} [available]\n`;
    });
    if (pending.length > 3) sb += `  ... and ${pending.length - 3} more\n`;
    sb += "\nUse a TaskManage update operation to mark a task as 'in_progress' before working on it.";
    return sb;
  }
  private expandedWorkReminderMessage(toolCount: number, current: HookTask, pending: HookTask[]): string {
    let sb = `[Task Maintenance Reminder]\n\nYou've used ${toolCount} tools while working on: ${current.subject}\n\nHas the work expanded beyond the original task?\n\nConsider:\n  - Creating new tasks for additional work discovered\n  - Updating task descriptions if scope changed\n  - Marking this task complete and starting follow-ups\n`;
    sb += pending.length > 0 ? `\n${pending.length} task(s) already pending. Add more if needed.` : "\nNo pending tasks - use a TaskManage create operation if new work emerged.";
    return sb;
  }
}

// post_acting_hook.go: require verification and documentation after acting work.
export const POST_ACTING_HOOK = "post-acting-hook";
export const DEFAULT_ACTING_THRESHOLD = 2;

export class PostActingHook {
  readonly name = POST_ACTING_HOOK;
  private completedActing = new Set<string>();
  private prompted = false;

  constructor(private readonly threshold = DEFAULT_ACTING_THRESHOLD) {}

  startSession(tasks: readonly HookTask[]): void {
    this.completedActing = new Set(tasks.filter(task => task.status === "completed" && task.category === "acting").map(task => task.id));
    this.prompted = false;
  }

  onToolAfter(event: ToolResultEvent, tasks: readonly HookTask[]): HookResult {
    if (event.failed || !isTaskManagementTool(event.toolName)) return CONTINUE;
    for (const task of tasks) {
      if (task.status === "completed" && task.category === "acting") this.completedActing.add(task.id);
    }
    if (this.completedActing.size < this.threshold) return CONTINUE;
    if (this.prompted || tasks.some(task => task.status !== "deleted" && (task.category === "verifying" || task.category === "documenting"))) return CONTINUE;
    this.prompted = true;
    return { message: `[POST-ACTING WORKFLOW REMINDER]

You have completed ${this.threshold}+ acting (implementation) tasks.

═══════════════════════════════════════════════════════════════════════════════
              THE WORK IS NOT DONE UNTIL IT IS VERIFIED AND DOCUMENTED
═══════════════════════════════════════════════════════════════════════════════

The implementation you just completed needs follow-up work:

┌─────────────────────────────────────────────────────────────────────────────┐
│  REQUIRED NEXT STEPS:                                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│  1. Create VERIFYING tasks for the code you wrote:                          │
│     - Run the test suite                                                    │
│     - Verify edge cases                                                     │
│     - Check integration points                                              │
│                                                                             │
│  2. Create DOCUMENTING tasks for the changes:                              │
│     - Update relevant README sections                                       │
│     - Add/update code comments                                              │
│     - Update changelog if applicable                                        │
│     - Document any new APIs or configuration                                │
└─────────────────────────────────────────────────────────────────────────────┘

═══════════════════════════════════════════════════════════════════════════════
                                WHY THIS MATTERS:
═══════════════════════════════════════════════════════════════════════════════

  Creation ≠ Verification. The agent made something — that's 50% of the job.
  The other 50% is proving it works and documenting it for future developers.

  Unverified code is broken code.
  Undocumented code is unmaintainable code.

If you have already created verifying/documenting tasks, this reminder can be ignored.
Otherwise, CREATE THEM NOW before proceeding with more acting tasks.` };
  }
}

// ---------------------------------------------------------------------------
// autogenskills: TUI trigger config + LifecycleHook + BudgetEnforcementHook
// ---------------------------------------------------------------------------
export interface AutogenTriggerConfig { toolCallBudget: number; workingBudget: number; maxNudgeIgnores: number; nudgeInterval: number; errorResolutionThreshold: number }
/** swarm-tui/internal/chat/sdk_integration.go autogenCfg.Trigger (not the package defaults). */
export const SWARM_TUI_AUTOGEN_TRIGGER: AutogenTriggerConfig = { toolCallBudget: 5, workingBudget: 90, maxNudgeIgnores: 3, nudgeInterval: 5, errorResolutionThreshold: 1 };
export const AUTOGEN_LIFECYCLE_HOOK = "autogenskills";
export const AUTOGEN_BUDGET_HOOK = "autogenskills-budget-enforcement";

/** hook.go LifecycleHook (after-execute, priority 91). Service metrics are nil in the TUI, so only the count-based review nudge is reachable. */
export class AutogenLifecycleHook {
  readonly name = AUTOGEN_LIFECYCLE_HOOK;
  private itersSinceSkill = 0; private hadRecentError = false; private lastNudgeAt = 0;
  constructor(private readonly trigger: AutogenTriggerConfig = SWARM_TUI_AUTOGEN_TRIGGER) {}
  onToolAfter(event: ToolResultEvent, budget: MetaNudgeBudget, session: string): HookResult {
    if (event.failed) { this.hadRecentError = true; return CONTINUE; }
    if (isSkillTool(event.toolName)) { this.itersSinceSkill = 0; this.lastNudgeAt = 0; }
    else this.itersSinceSkill++;
    this.hadRecentError = false;
    const iters = this.itersSinceSkill;
    const parts: string[] = [];
    if (iters > this.trigger.nudgeInterval && iters > this.lastNudgeAt) {
      parts.push(`[SKILL REVIEW] You've made ${iters} tool calls since the last skill review. Preserve useful learning without creating one-session clutter: first patch a loaded skill, then an existing class-level umbrella, then add a support file. Create a new class-level skill only if none fits. If there is genuinely nothing reusable, call SkillManage(action: "review", review_reason: "nothing reusable to save") so work can continue without manufacturing a skill.`);
      this.itersSinceSkill = 0; this.lastNudgeAt = 0;
    }
    if (parts.length === 0) return CONTINUE;
    const [seq, ok] = budget.tryClaim(session, META_NUDGE_SKILL_REVIEW);
    if (!ok) return CONTINUE;
    return { message: wrapReminder(this.name, "review", seq, parts.join("\n\n")) };
  }
}

/** budget_enforcement.go BudgetEnforcementHook (before-execute priority 90; after-execute refills). */
export class AutogenBudgetEnforcementHook {
  readonly name = AUTOGEN_BUDGET_HOOK;
  private toolCalls = 0; private skilled = false; private nudgeIgnores = 0; private taskEverFocused = false;
  constructor(private readonly trigger: AutogenTriggerConfig = SWARM_TUI_AUTOGEN_TRIGGER) {}
  private exempt(event: ToolCallEvent): boolean {
    const n = event.toolName;
    if (isSkillTool(n) || isTaskManagementTool(n) || isPlanModeTool(n) || isUserInteractionTool(n) || isCodeModeTool(n) || isReadOnlyExplorationTool(n)) return true;
    if (isBashTool(n)) { const cmd = event.params?.command; if (typeof cmd === "string" && isBashReadOnly(cmd)) return true; }
    return false;
  }
  onToolBefore(event: ToolCallEvent, tasks: readonly HookTask[], budget: MetaNudgeBudget, session: string): HookResult {
    if (!event.toolName || this.exempt(event)) return CONTINUE;
    if (!this.taskEverFocused) { if (hasFocusedTask(tasks)) this.taskEverFocused = true; else return CONTINUE; }
    const limit = this.skilled ? this.trigger.workingBudget : this.trigger.toolCallBudget;
    if (this.toolCalls >= limit) {
      if (this.skilled) {
        this.nudgeIgnores++;
        if (this.nudgeIgnores > this.trigger.maxNudgeIgnores) return { block: true, message: wrapReminder(this.name, "block", nextReminderSeq(this.name), this.escalationBlockMessage()) };
        const [seq, ok] = budget.tryClaim(session, META_NUDGE_SKILL_REVIEW);
        if (!ok) return CONTINUE;
        return { message: wrapReminder(this.name, "review", seq, this.nudgeMessage()) };
      }
      return { block: true, message: wrapReminder(this.name, "block", nextReminderSeq(this.name), this.blockMessage()) };
    }
    this.toolCalls++;
    return CONTINUE;
  }
  onToolAfter(event: ToolResultEvent): void {
    if (!isSkillTool(event.toolName) || event.failed) return;
    this.toolCalls = 0; this.nudgeIgnores = 0; this.skilled = true;
  }
  private blockMessage() {
    const t = this.trigger;
    return `[SKILL BUDGET ENFORCEMENT — BLOCKED]

You have used ${this.toolCalls} non-exempt tool calls. The onboarding budget is ${t.toolCallBudget}.

═══════════════════════════════════════════════════════════════
                    YOU MUST CREATE OR USE A SKILL
═══════════════════════════════════════════════════════════════

You cannot execute any further tools until you:

  1. Create a skill for the recurring pattern you're working on
     → Call SkillManage(action: "create", name: "...", description: "...", instructions: "...")
  2. Or use an existing skill that covers this workflow
     → Call the Skill tool to invoke a relevant skill

  After creating or using a skill, your budget upgrades to ${t.workingBudget} and ALL tools unlock.

═══════════════════════════════════════════════════════════════

This is a HARD REQUIREMENT. The onboarding budget exists to
ensure you capture patterns early. Once you create your first
skill, your budget expands to ${t.workingBudget}.`;
  }
  private nudgeMessage() {
    const t = this.trigger;
    const remaining = Math.max(t.maxNudgeIgnores - this.nudgeIgnores, 0);
    return `[SKILL BUDGET NUDGE — ${this.toolCalls} non-exempt tool calls]

You've exceeded your working budget of ${t.workingBudget}. This is a soft nudge — your
tool call is still allowed, but you should consider:

  1. Creating a skill for the recurring pattern you're working on
     → Call SkillManage(action: "create", name: "...", description: "...", instructions: "...")
  2. Patching an existing skill that's incomplete
     → Call SkillManage(action: "patch", name: "...", instructions: "...")
  3. Using an existing skill that covers this workflow
     → Call the Skill tool to invoke a relevant skill

If you create, patch, or invoke a skill, your budget refills to ${t.workingBudget}.
You have ${remaining} soft nudge(s) remaining before hard enforcement.`;
  }
  private escalationBlockMessage() {
    const t = this.trigger;
    return `[SKILL BUDGET ENFORCEMENT — ESCALATION BLOCKED]

You have used ${this.toolCalls} non-exempt tool calls and ignored ${this.nudgeIgnores} soft nudges.

═══════════════════════════════════════════════════════════════
                    YOU MUST CREATE OR USE A SKILL
═══════════════════════════════════════════════════════════════

Your working budget of ${t.workingBudget} has been exceeded and you've ignored
${t.maxNudgeIgnores} nudges. ALL non-exempt tools are now HARD-BLOCKED.

Create, patch, or invoke a skill to refill your budget:

  1. SkillManage(action: "create", name: "...", description: "...", instructions: "...")
  2. SkillManage(action: "patch", name: "...", instructions: "...")
  3. Skill tool to invoke an existing skill

═══════════════════════════════════════════════════════════════

After creating, patching, or invoking a skill, your budget refills to ${t.workingBudget}.`;
  }
}

// ---------------------------------------------------------------------------
// Delivery pipeline (HooksManager + agent_tools.go)
// ---------------------------------------------------------------------------
/** A pre-tool hook in Swarm priority order. */
export interface PreToolHook { name: string; run(event: ToolCallEvent, tasks: readonly HookTask[], budget: MetaNudgeBudget, session: string): HookResult }
/** A post-tool hook in Swarm priority order. */
export interface PostToolHook { name: string; run(event: ToolResultEvent, tasks: readonly HookTask[], budget: MetaNudgeBudget, session: string): HookResult }

export const blockedByHookText = (toolName: string, reason: string) => `Tool '${toolName}' blocked by hook: ${reason}`;

/** manager.go dedupeByLeadingLine — first occurrence wins, order preserved. */
export function dedupeByLeadingLine(parts: string[]): string[] {
  const seen = new Set<string>(); const out: string[] = [];
  for (const p of parts) { const head = p.split("\n", 1)[0]; if (seen.has(head)) continue; seen.add(head); out.push(p); }
  return out;
}

export interface PipelineOptions {
  session: string | (() => string);
  tasks: () => readonly HookTask[];
  preHooks: PreToolHook[];
  postHooks: PostToolHook[];
  budget?: MetaNudgeBudget;
  isSubAgent?: () => boolean;
  taskNudge?: TaskNudgeConfig;
}

/**
 * plan_mode_first_tool.go / simulation.go as the interactive TUI runs them
 * (swarm-tui/internal/chat/hooks/manager.go):
 *  - PlanModeFirstToolHook is registered with the SDK hook manager (priority
 *    96, name "plan-mode-first-tool-hook"): enter_plan_mode flips the state
 *    silently; the FIRST later tool gets ProblemBreakdownPrompt as a pre-tool
 *    ContinueWithMessage (embedded in the result AND queued for the next
 *    prompt like any other pre-tool output).
 *  - After every PlanExitDetected tool (enter/exit_plan_mode…) the manager
 *    appends a "simulation" post-tool result carrying the rehearsal text,
 *    which agent_tools.go turns into the turn's RoleUser hook message.
 */
export interface PlanModeHooks {
  /** CheckPlanModeFirstTool: returns the breakdown prompt exactly once per plan session. */
  firstTool(toolName: string): string | undefined;
  planExitDetected(toolName: string): boolean;
  simulationMessage(): string;
}
export const PLAN_MODE_FIRST_TOOL_HOOK = "plan-mode-first-tool-hook";
export const SIMULATION_HOOK = "simulation";

// ---------------------------------------------------------------------------
// task_nudge.go TaskNudgeBudget — the built-in user_prompt_submit nudge the
// TUI HooksManager runs after user hooks. Never on turn one, only after
// multi-step tool activity, on the shared MetaNudgeBudget cadence.
// ---------------------------------------------------------------------------
export interface TaskNudgeConfig { nudgeInterval?: number; toolCallThreshold?: number }
export const TASK_NUDGE_TEXT = "[Task Nudge] Multi-step work detected with no active tasks — consider TaskManage.";
interface TaskNudgeState { turns: number; toolCalls: number; lastNudge: number }

/** task_nudge.go promptSuggestsImmediateExecution. */
export function promptSuggestsImmediateExecution(prompt: string): boolean {
  const trimmed = prompt.trim();
  if (trimmed === "") return false;
  const lower = trimmed.toLowerCase();
  for (const c of ["continue", "keep going", "go ahead", "proceed", "resume", "carry on"]) {
    if (lower === c || lower.startsWith(`${c} `) || lower.startsWith(`${c},`)) return true;
  }
  if (Buffer.byteLength(trimmed) < 100) {
    const imperatives = [
      "run ", "just run ", "please run ", "go run ",
      "show ", "show me ", "print ", "cat ", "ls ", "echo ",
      "check ", "verify ", "confirm ",
      "merge ", "pull ", "push ", "commit ", "rebase ", "fetch ",
      "fix this ", "fix it", "undo ", "revert ",
      "open ", "build ", "test ", "deploy ", "restart ",
    ];
    for (const im of imperatives) if (lower.startsWith(im)) return true;
  }
  return false;
}

export class TaskNudgeBudget {
  private readonly interval: number;
  private readonly threshold: number;
  private states = new Map<string, TaskNudgeState>();
  constructor(config: TaskNudgeConfig = {}) {
    this.interval = config.nudgeInterval || 5;
    this.threshold = config.toolCallThreshold || 2;
  }
  private state(session: string): TaskNudgeState { let s = this.states.get(session); if (!s) { s = { turns: 0, toolCalls: 0, lastNudge: 0 }; this.states.set(session, s); } return s; }
  /** HooksManager.EmitToolAfterExecute: every executed (not hook-blocked, not unknown) tool. */
  recordToolCall(session: string): void { this.state(session).toolCalls++; }
  /** CheckTurn: advances the shared cadence clock, then maybe claims a nudge. */
  checkTurn(session: string, prompt: string, tasks: readonly HookTask[], budget: MetaNudgeBudget): string | undefined {
    budget.recordUserTurn(session);
    const s = this.state(session);
    s.turns++;
    if (s.turns === 1 || s.toolCalls < this.threshold) return undefined;
    if (s.lastNudge !== 0 && s.turns - s.lastNudge < this.interval) return undefined;
    // canTaskNudge: any task at all (ByOwner("")) silences it, as do short imperatives.
    if (tasks.some(t => !t.owner)) return undefined;
    if (promptSuggestsImmediateExecution(prompt)) return undefined;
    const [seq, ok] = budget.tryClaim(session, META_NUDGE_TASK);
    if (!ok) return undefined;
    s.lastNudge = s.turns;
    return wrapReminder("task-nudge", "nudge", seq, TASK_NUDGE_TEXT);
  }
}

/**
 * Runs hooks the way HooksManager + agent_tools.go do and hands back the
 * bytes to put on the wire:
 *  - preTool → { block: "<Tool 'x' blocked by hook: …>" } or the joined
 *    pre-context to prefix onto a SUCCESSFUL tool result (phc + "\n\n---\n\n" + out)
 *  - postTool → the joined post-context for this call; the caller collects
 *    every call of the assistant message and emits ONE user message.
 */
export class SwarmHookPipeline {
  readonly budget: MetaNudgeBudget;
  readonly taskNudge: TaskNudgeBudget;
  private pendingToolMessages: string[] = [];
  private turnParts: string[] = [];
  constructor(private readonly options: PipelineOptions) { this.budget = options.budget ?? new MetaNudgeBudget(); this.taskNudge = new TaskNudgeBudget(options.taskNudge); }
  private get session(): string { const s = this.options.session; return typeof s === "function" ? s() : s; }
  startSession(tasks: readonly HookTask[]): void { (this as any).hooks?.postActing?.startSession(tasks); }

  /**
   * EmitUserPromptSubmit: user hooks, then the built-in task-nudge
   * (CheckTurn advances the cadence clock once per prompt), then the pending
   * tool-time messages (last 3), deduped by leading line and "\n"-joined.
   * Headless `swarm -p` (cmd/swarmos/main.go) additionally emits
   * message.after_receive, whose executor advances the clock a second time;
   * the interactive TUI never emits that event.
   */
  onUserPrompt(options: { messageAfterReceive?: boolean; prompt?: string } = {}): { injected: string } {
    if (options.messageAfterReceive !== false) this.budget.recordUserTurn(this.session);
    const parts: string[] = [];
    const nudge = this.taskNudge.checkTurn(this.session, options.prompt ?? "", this.options.tasks(), this.budget);
    if (nudge) parts.push(nudge);
    // Flush pre-tool messages queued during the previous turn (last 3, deduped).
    let pending = this.pendingToolMessages.splice(0);
    if (pending.length > 3) pending = pending.slice(pending.length - 3);
    const injected = dedupeByLeadingLine([...parts, ...pending]);
    return { injected: injected.join("\n") };
  }

  preTool(event: ToolCallEvent): { block?: string; context: string } {
    const tasks = this.options.tasks();
    const parts: string[] = [];
    for (const hook of this.options.preHooks) {
      const result = hook.run(event, tasks, this.budget, this.session);
      if (result.block) return { block: blockedByHookText(event.toolName, result.message ?? ""), context: "" };
      if (result.message) {
        const wrapped = formatHookContext(hook.name, result.message);
        if (wrapped) { parts.push(wrapped); this.pendingToolMessages.push(result.message); }
      }
    }
    return { context: parts.join("\n\n") };
  }

  /** Returns the post-context for this call and remembers it for the turn message. */
  postTool(event: ToolResultEvent): string {
    const tasks = this.options.tasks();
    this.taskNudge.recordToolCall(this.session);
    const parts: string[] = [];
    for (const hook of this.options.postHooks) {
      const result = hook.run(event, tasks, this.budget, this.session);
      if (result.message) { const wrapped = formatHookContext(hook.name, result.message); if (wrapped) parts.push(wrapped); }
    }
    const context = parts.join("\n\n");
    if (context) this.turnParts.push(context);
    return context;
  }

  /** agent_tools.go: one RoleUser message per assistant tool batch. */
  flushTurn(): string {
    const parts = this.turnParts.splice(0);
    return parts.join("\n\n");
  }

  /** agent_tools.go: prefix pre-context onto a successful result. */
  static applyPreContext(context: string, output: string): string {
    if (!context) return output;
    return output !== "" ? `${context}\n\n---\n\n${output}` : context;
  }
}

/** The headless-registered builtin set, in HooksManager priority order. */
export function createSwarmBuiltinPipeline(options: Omit<PipelineOptions, "preHooks" | "postHooks"> & {
  enforcementMode?: EnforcementMode;
  trigger?: AutogenTriggerConfig;
  extraPre?: PreToolHook[];   // sleep-blocker (85), stdin-conflict (84) — bash-only, supplied by the extension
  extraPost?: PostToolHook[]; // annoyance-nudge (20)
  planMode?: PlanModeHooks;   // interactive TUI only (PlanBroker present)
}): SwarmHookPipeline {
  const enforcement = new TaskEnforcementHook(options.enforcementMode ?? "advise");
  const maintenance = new TaskMaintenanceReminderHook();
  const lifecycle = new AutogenLifecycleHook(options.trigger);
  const postActing = new PostActingHook();
  const budgetHook = new AutogenBudgetEnforcementHook(options.trigger);
  const isSub = options.isSubAgent ?? (() => false);
  const plan = options.planMode;
  const pre: PreToolHook[] = [
    ...(plan ? [{ name: PLAN_MODE_FIRST_TOOL_HOOK, run: (e: ToolCallEvent) => { const m = plan.firstTool(e.toolName); return m ? { message: m } : CONTINUE; } }] : []), // 96
    { name: enforcement.name, run: (e, t, b, s) => enforcement.onToolBefore(e, t, b, s, isSub()) },   // 95
    { name: budgetHook.name, run: (e, t, b, s) => budgetHook.onToolBefore(e, t, b, s) },             // 90
    ...(options.extraPre ?? []),                                                                       // 85, 84
  ];
  const post: PostToolHook[] = [
    { name: lifecycle.name, run: (e, _t, b, s) => lifecycle.onToolAfter(e, b, s) },                    // 91
    { name: maintenance.name, run: (e, t, b, s) => maintenance.onToolAfter(e, t, b, s) },             // 90
    { name: postActing.name, run: (e, t) => postActing.onToolAfter(e, t) },                             // 90
    { name: budgetHook.name, run: (e) => { budgetHook.onToolAfter(e); return CONTINUE; } },           // 90 (refill only)
    ...(options.extraPost ?? []),                                                                      // 20
    // manager.go EmitToolAfterExecute appends the simulation result after every registered hook.
    ...(plan ? [{ name: SIMULATION_HOOK, run: (e: ToolResultEvent) => plan.planExitDetected(e.toolName) ? { message: plan.simulationMessage() } : CONTINUE }] : []),
  ];
  const pipeline = new SwarmHookPipeline({ ...options, preHooks: pre, postHooks: post });
  (pipeline as any).hooks = { enforcement, maintenance, lifecycle, postActing, budgetHook };
  return pipeline;
}
