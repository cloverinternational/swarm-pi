import { isHookEnabled, registerHook } from "../hook-state.ts";
import { AnnoyanceNudgeState, formatHookContext, resultText } from "./swarm-annoyance-nudge.ts";
import {
  SwarmHookPipeline, createSwarmBuiltinPipeline,
  type EnforcementMode, type HookTask, type PostToolHook, type PreToolHook, type ToolCallEvent,
} from "./swarm-builtin-hooks.ts";
import { sleepBlockReason } from "./swarm-sleep-blocker.ts";
import { STDIN_CONFLICT_HOOK_NAME, StdinConflictHook } from "./swarm-stdin-conflict.ts";
import { rawPi } from "./swarm-tool-surface.ts";

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
  const sessionOf = (ctx: any) => { const id = ctx?.sessionManager?.getSessionId?.() ?? ctx?.sessionId; if (typeof id === "string" && id) session = id; return session || "session"; };
  const bashPre = (name: string, decide: (command: string | undefined) => { block?: string; message?: string } | undefined): PreToolHook => ({
    name,
    run: (event: ToolCallEvent) => {
      if (event.toolName !== "bash") return {};
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
  });
  registrations.set(owner, pipeline);
  const hooks = (pipeline as any).hooks;
  const preContext = new Map<string, string>();

  registerHook(pi, "taskmanage", "before_agent_start", (event: any, ctx: any) => {
    sessionOf(ctx);
    const prompt = typeof event?.prompt === "string" ? event.prompt : "";
    // EmitMessageAfterReceive is fire-and-forget in Swarm: only state effects survive.
    hooks.enforcement.onUserMessage(prompt);
    hooks.maintenance.onUserMessage(hookTasks(), pipeline.budget, session || "session");
    const { injected } = pipeline.onUserPrompt();
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
    const { text, index } = firstText(event?.content);
    pipeline.postTool({ toolName: event?.toolName ?? "", params: event?.input ?? {}, toolCallId: event?.toolCallId, failed, output: resultText(event?.content), ...( { details: event?.details } as any) });
    const phc = event?.toolCallId ? preContext.get(event.toolCallId) ?? "" : "";
    if (event?.toolCallId) preContext.delete(event.toolCallId);
    if (!phc || failed) return undefined; // agent_tools.go drops pre-context on error
    const merged = SwarmHookPipeline.applyPreContext(phc, text);
    if (Array.isArray(event.content) && index >= 0) { const content = [...event.content]; content[index] = { ...content[index], text: merged }; return { content }; }
    return { content: [{ type: "text", text: merged }, ...(Array.isArray(event.content) ? event.content : [])] };
  });
  registerHook(pi, "taskmanage", "turn_end", () => {
    const content = pipeline.flushTurn();
    if (!content) return undefined;
    // Steered custom messages drain right after turn_end, before the next
    // model call — the slot Swarm's standalone RoleUser hook message occupies.
    pi.sendMessage?.({ customType: "swarm-hook-context", content, display: false }, { deliverAs: "steer", triggerTurn: true });
    return undefined;
  });
  return pipeline;
}
