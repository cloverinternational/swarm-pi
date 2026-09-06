import {
  registerTaskHooks,
  registerTaskManage,
  type HookConfig,
} from "../../taskmanage/src/index.ts";
import "../hook-state.ts";
import { withSwarmToolSurface } from "../lib/swarm-tool-surface.ts";
import { registerSwarmBuiltinHooks } from "../lib/swarm-builtin-hooks-runtime.ts";

/**
 * Minimal structural subset of Pi's ExtensionAPI used by TaskManage.
 *
 * Keeping this adapter structural means the standalone package does not need
 * to depend on a particular Pi release just to remain buildable and testable.
 */
export interface TaskManageExtensionAPI {
  registerTool(tool: unknown): void;
  appendEntry(type: string, data: unknown): void;
  on(
    event: string,
    handler: (
      event: unknown,
      ctx: { sessionManager?: { getEntries(): readonly unknown[] } },
    ) => unknown,
  ): void;
}

export interface TaskManageExtensionOptions extends HookConfig { headless?: boolean; interactionTimeoutMs?: number; }

/**
 * Register the canonical TaskManage tool and its lifecycle coordinator.
 *
 * The manager is intentionally created once per Pi extension instance and is
 * shared by both registrations. Pi reloads create a new instance; the
 * session_start handler rehydrates it from the replacement session manager.
 */
const taskRuntimeByPi = new WeakMap<object, { manager: any; hooks: any; interactions: any }>();
export function registerTaskManageExtension(
  pi: TaskManageExtensionAPI,
  options?: TaskManageExtensionOptions,
) {
  const owner = pi as object;
  const existing = taskRuntimeByPi.get(owner);
  if (existing) return existing;
  const bridged: any[] = [];
  const registrationPi = { ...pi, registerTool: (tool: unknown) => { bridged.push(tool); pi.registerTool(tool); } };
  const manager = registerTaskManage(registrationPi);
  // Swarm's builtin task hooks (.pi/extensions/swarm-builtin-hooks.ts) read
  // task state through this handle; the coordinator below keeps only the
  // task-audit bookkeeping so no second copy of the nudges reaches the model.
  (globalThis as any)[Symbol.for("pi-swarm-task-manager")] = manager;
  (pi as any).codemodeTools = [...((pi as any).codemodeTools ?? []), ...bridged];
  // Swarm's builtin hooks (task-enforcement, task-maintenance, skill budget,
  // sleep/stdin, annoyance) in HooksManager order; idempotent per Pi instance.
  registerSwarmBuiltinHooks(pi, { enforcementMode: options?.enforcementMode });
  const hooks = registerTaskHooks(pi, manager, { ...options, enforcementMode: "off", silent: true });
  // ask_user_question is provided by the dedicated pi-ask-user extension.
  // Keep interaction registration here out of the root extension: Pi rejects
  // duplicate tool names when both extensions are auto-loaded.
  const result = { manager, hooks };
  taskRuntimeByPi.set(owner, result);
  return result;
}

export default function taskManageExtension(pi: TaskManageExtensionAPI): void {
  registerTaskManageExtension(withSwarmToolSurface(pi));
}
