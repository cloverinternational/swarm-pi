import { isHookEnabled, registerHook } from "./hook-state.ts";
import { AnnoyanceNudgeState, formatHookContext, resultText } from "../policy/swarm-annoyance-nudge.ts";
import {
  SwarmHookPipeline, createSwarmBuiltinPipeline,
  type EnforcementMode, type HookTask, type PlanModeHooks, type PostToolHook, type PreToolHook, type ToolCallEvent,
} from "./swarm-builtin-hooks.ts";
import { planExitDetected, simulationReminderMessage } from "../context/swarm-plan-mode.ts";
import { sleepBlockReason } from "../policy/swarm-sleep-blocker.ts";
import { STDIN_CONFLICT_HOOK_NAME, StdinConflictHook } from "./swarm-stdin-conflict.ts";
import { rawPi } from "./swarm-tool-surface.ts";
import { elideOversizedToolOutput } from "./swarm-toolout.ts";

type Pi = any;
// Pi hands every extension module its own ExtensionAPI object, so a per-object
// registry would install the pipeline once per importing extension. Key it by
// the underlying Pi instance AND keep it process-wide (Symbol.for survives
// module re-evaluation on /reload; the WeakMap forgets stale instances).
const REGISTRY = Symbol.for("pi-swarm-builtin-hooks-registry");
const registrations: WeakMap<object, SwarmHookPipeline> = ((globalThis as any)[REGISTRY] ??= new WeakMap<object, SwarmHookPipeline>());
export const TASK_MANAGER_SYMBOL = Symbol.for("pi-swarm-task-manager");
export interface SwarmBuiltinHookOptions { enforcementMode?: EnforcementMode }

/** Tasks as the Swarm hooks see them (ii.TodoManager), read live from the TaskManage extension. */
function hookTasks(): HookTask[] {
  const manager: any = (globalThis as any)[TASK_MANAGER_SYMBOL];
  const tasks: any[] = manager?.snapshot?.()?.tasks ?? [];
  return tasks.filter(t => t?.status !== "deleted").map(t => ({
    id: String(t.id), subject: String(t.subject ?? ""), status: String(t.status ?? ""), active: t.active === true,
    category: t.category, owner: t.owner_id, dependsOn: Array.isArray(t.dependsOn) ? t.dependsOn.map(String) : [],
  }));
}

const firstText = (content: unknown): { text: string; index: number } => {
  if (typeof content === "string") return { text: content, index: -1 };
  if (Array.isArray(content)) { const i = content.findIndex((b: any) => b?.type === "text" && typeof b.text === "string"); return { text: i >= 0 ? content[i].text : "", index: i }; }
  return { text: "", index: -1 };
};

/**
 * The plan-mode state the SDK-registered PlanModeFirstToolHook and the
 * TUI's simulation post-hook read (plan_mode_first_tool.go planModeState).
 * Resolved lazily through the process-wide handle the plan-mode extension
 * publishes so this lib never imports an extension module. Headless `-p`
 * never registers the plan tools, so the hooks are inert there — exactly
 * like Swarm, where the hook is registered but the state never flips.
 */
const PLAN_CONTROLLER_SYMBOL = Symbol.for("pi-swarm-plan-mode-controller");
function planModeHooks(): PlanModeHooks {
  const controller = (): any => (globalThis as any)[PLAN_CONTROLLER_SYMBOL];
  return {
    firstTool: (toolName) => controller()?.beforeTool?.(toolName)?.inject,
    planExitDetected,
    simulationMessage: simulationReminderMessage,
  };
}

/**
 * Swarm's builtin hook pipeline for Pi (registered once, from the TaskManage
 * extension, since the task state it gates on lives there): task-enforcement (95), autogenskills
 * budget (90), sleep-blocker (85), stdin-conflict (84) before a tool;
 * autogenskills lifecycle (91), task-maintenance (90), annoyance-nudge (20)
 * after it. Pre-context is prefixed onto the successful tool result
 * (agent_tools.go), blocks become "Tool 'x' blocked by hook: …", and all
 * post-context of one assistant turn is delivered as ONE user message right
 * after the tool results — exactly where `swarm -p` puts it.
 */
export function registerSwarmBuiltinHooks(pi: Pi, options: SwarmBuiltinHookOptions = {}): SwarmHookPipeline {
  const owner = rawPi(pi as object);
  const existing = registrations.get(owner);
  if (existing) return existing;
  const stdinConflict = new StdinConflictHook();
  const annoyance = new AnnoyanceNudgeState();
  let session = "";
  let startedPostActingSession = "";
  const sessionOf = (ctx: any) => { const id = ctx?.sessionManager?.getSessionId?.() ?? ctx?.sessionId; if (typeof id === "string" && id) session = id; return session || "session"; };
  const bashPre = (name: string, decide: (command: string | undefined) => { block?: string; message?: string } | undefined): PreToolHook => ({
    name,
    run: (event: ToolCallEvent) => {
      // sleep_blocker_hook.go / stdin_conflict_hook.go accept "Bash" (TUI bgprocess) and "bash" (-p).
      if (event.toolName !== "bash" && event.toolName !== "Bash") return {};
      const r = decide(event.params?.command);
      if (!r) return {};
      if (r.block) return { block: true, message: r.block };
      return { message: r.message };
    },
  });
  const extraPre: PreToolHook[] = [
    bashPre("sleep-blocker", command => { const reason = sleepBlockReason(command); return reason ? { block: reason } : undefined; }),
    bashPre(STDIN_CONFLICT_HOOK_NAME, command => { const c = stdinConflict.onBashCommand(command); if (!c) return undefined; return "block" in c ? { block: c.block } : { message: c.advise }; }),
  ];
  const extraPost: PostToolHook[] = [{
    name: "annoyance-nudge",
    run: (event) => {
      if (!isHookEnabled("annoyance")) return {};
      const reminder = annoyance.onToolResult(session || "session", { toolName: event.toolName, isError: event.failed, content: event.output, details: (event as any).details });
      return reminder ? { message: reminder } : {};
    },
  }];
  const pipeline = createSwarmBuiltinPipeline({
    session: () => session || "session",
    tasks: hookTasks,
    isSubAgent: () => process.env.PI_SWARM_SUBAGENT === "1",
    enforcementMode: options.enforcementMode,
    extraPre, extraPost,
    planMode: planModeHooks(),
  });
  registrations.set(owner, pipeline);
  const hooks = (pipeline as any).hooks;
  const postActing = hooks.postActing;
  const preContext = new Map<string, string>();

  registerHook(pi, "taskmanage", "before_agent_start", (event: any, ctx: any) => {
    sessionOf(ctx);
    if (startedPostActingSession !== (session || "session")) {
      startedPostActingSession = session || "session";
      postActing?.startSession(hookTasks());
    }
    const prompt = typeof event?.prompt === "string" ? event.prompt : "";
    // Only headless `swarm -p` emits message.after_receive (main.go:1552); it
    // is fire-and-forget there, so only its state effects survive. The TUI
    // never emits it, so interactive sessions skip those effects and the
    // second cadence tick.
    const messageAfterReceive = ctx?.hasUI !== true;
    if (messageAfterReceive) {
      hooks.enforcement.onUserMessage(prompt);
      hooks.maintenance.onUserMessage(hookTasks(), pipeline.budget, session || "session");
    }
    const { injected } = pipeline.onUserPrompt({ messageAfterReceive, prompt });
    if (!injected) return undefined;
    return { message: { customType: "swarm-hook-context", content: formatHookContext("user_prompt_submit", injected), display: false } };
  });
  registerHook(pi, "taskmanage", "tool_call", (event: any, ctx: any) => {
    sessionOf(ctx);
    const { block, context } = pipeline.preTool({ toolName: event?.toolName ?? "", params: event?.input ?? {}, toolCallId: event?.toolCallId });
    if (block) return { block: true, reason: block };
    if (context && event?.toolCallId) preContext.set(event.toolCallId, context);
    return undefined;
  });
  registerHook(pi, "taskmanage", "tool_result", (event: any, ctx: any) => {
    sessionOf(ctx);
    const failed = event?.isError === true;
    let { text, index } = firstText(event?.content);
    // agent_tools.go: oversized results (100k bytes / 1000 lines) are
    // middle-elided BEFORE after-hooks see them, unless they carry an image.
    let content: any[] | undefined = Array.isArray(event?.content) ? event.content : undefined;
    const hasImage = !!content?.some((b: any) => b?.type === "image");
    const elided = !hasImage && index >= 0 ? elideOversizedToolOutput(text) : undefined;
    if (elided !== undefined && content) { content = [...content]; content[index] = { ...content[index], text: elided }; text = elided; }
    pipeline.postTool({ toolName: event?.toolName ?? "", params: event?.input ?? {}, toolCallId: event?.toolCallId, failed, output: resultText(content ?? event?.content), ...( { details: event?.details } as any) });
    const phc = event?.toolCallId ? preContext.get(event.toolCallId) ?? "" : "";
    if (event?.toolCallId) preContext.delete(event.toolCallId);
    // agent_tools.go drops pre-context only when the tool returned a Go
    // error (sdkerr "Error executing …", registry "Error: Tool '…' not
    // found"); a NewErrorResult (IsError) keeps it.
    const goError = failed && /^Error executing |^Error: Tool '/.test(text);
    if (!phc || goError) return elided !== undefined && content ? { content } : undefined;
    const merged = SwarmHookPipeline.applyPreContext(phc, text);
    if (content && index >= 0) { const next = [...content]; next[index] = { ...next[index], text: merged }; return { content: next }; }
    return { content: [{ type: "text", text: merged }, ...(Array.isArray(event.content) ? event.content : [])] };
  });
  registerHook(pi, "taskmanage", "turn_end", (event: any) => {
    const content = pipeline.flushTurn();
    // agent_tools.go appends the standalone RoleUser hook message right after
    // the turn's tool results, and the TUI's RichMessageInjector flushes its
    // RoleSystem notifications in the same slot. Pi's steering queue drains
    // one message per turn, so every part rides ONE steered custom message
    // that the transport-parity context hook expands back into separate
    // wire messages (user hook context, then system notifications).
    const runContinues = Array.isArray(event?.message?.content) && event.message.content.some((b: any) => b?.type === "toolCall");
    const parts: HookSlotPart[] = content ? [{ role: "user", text: content }] : [];
    for (const listener of afterTurnFlushListeners()) { try { parts.push(...(listener({ runContinues }) ?? [])); } catch { /* best effort */ } }
    if (!parts.length) return undefined;
    if (runContinues) pi.sendMessage?.({ customType: HOOK_SLOT_TYPE, content: parts[0].text, display: false, details: { parts } }, { deliverAs: "steer", triggerTurn: true });
    // Final turn: nothing Swarm would send before the next prompt; appended
    // (not steered) so no extra model call is triggered.
    else pi.sendMessage?.({ customType: HOOK_SLOT_TYPE, content: parts[0].text, display: false, details: { parts } }, { triggerTurn: false });
    return undefined;
  });
  return pipeline;
}

export const HOOK_SLOT_TYPE = "swarm-hook-context";
export interface HookSlotPart { role: "user" | "system"; text: string }
/** Listeners invoked at turn_end; they contribute extra parts to the post-tool hook slot. */
export const AFTER_TURN_HOOK_FLUSH = Symbol.for("pi-swarm-after-turn-hook-flush");
export const afterTurnFlushListeners = (): Array<(turn: { runContinues: boolean }) => HookSlotPart[] | undefined> => ((globalThis as any)[AFTER_TURN_HOOK_FLUSH] ??= []);
