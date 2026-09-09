import { randomUUID } from "node:crypto";
import { dispatchRegisteredHook } from "./hook-state.ts";

// Explicit registration bridge, limited to the two bootstrap handoff tools.
// Adapters replace their registration on reload; this does not create managers.
// Pi loads extensions in separate jiti module graphs. Share only registrations,
// never domain state; owning adapters refresh these definitions on each load.
const key = Symbol.for("pi-swarm-bootstrap-handoff-tools");
const tools: Map<string, any> = (globalThis as any)[key] ??= new Map();
export function registerBootstrapHandoff(tool: any) {
  if (tool.name === "Skill" || tool.name === "TaskManage") tools.set(tool.name, tool);
}
export async function dispatchBootstrapHandoff(name: "Skill" | "TaskManage", input: any, signal: AbortSignal, ctx: any, active: string[]) {
  if (signal.aborted) throw new Error("Bootstrap cancelled");
  if (!active.includes(name)) throw new Error(`${name} is not active; bootstrap cannot widen tool permissions`);
  const tool = tools.get(name);
  if (!tool) throw new Error(`${name} is unavailable for bootstrap handoff`);
  const event = { toolName: name, toolCallId: `bootstrap-${randomUUID()}`, input };
  const before = await dispatchRegisteredHook("tool_call", event, ctx);
  if (before?.block) throw new Error(before.reason || `${name} blocked`);
  let result: any;
  try { result = await tool.execute(event.toolCallId, input, signal, undefined, ctx); }
  catch (error) { result = { isError: true, content: [{ type: "text", text: error instanceof Error ? error.message : String(error) }] }; }
  const after = await dispatchRegisteredHook("tool_result", { ...event, ...result }, ctx);
  result = { ...result, ...(after ?? {}) };
  if (result.isError) throw new Error(result.content?.[0]?.text || `${name} failed`);
  return result;
}
