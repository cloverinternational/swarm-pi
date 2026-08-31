import {
  registerTaskHooks,
  registerTaskManage,
  type HookConfig,
} from "../../taskmanage/src/index.ts";

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

export interface TaskManageExtensionOptions extends HookConfig {}

/**
 * Register the canonical TaskManage tool and its lifecycle coordinator.
 *
 * The manager is intentionally created once per Pi extension instance and is
 * shared by both registrations. Pi reloads create a new instance; the
 * session_start handler rehydrates it from the replacement session manager.
 */
export function registerTaskManageExtension(
  pi: TaskManageExtensionAPI,
  options?: TaskManageExtensionOptions,
) {
  const manager = registerTaskManage(pi);
  const hooks = registerTaskHooks(pi, manager, options);
  return { manager, hooks };
}

export default function taskManageExtension(pi: TaskManageExtensionAPI): void {
  registerTaskManageExtension(pi);
}
