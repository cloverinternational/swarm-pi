import type { ControlTaskInterface, ID } from "../../runtime-contracts/src/control-task.ts";
import { withDefaultToolRenderer } from "../lib/swarm-tool-renderer.ts";

type Pi = { registerTool(tool: unknown): void };
const schema = (required: string[], properties: Record<string, unknown>) => ({ type: "object", required, properties });
const text = (value: unknown) => ({ content: [{ type: "text", text: JSON.stringify(value) }], details: value });

/** Typed Pi adapter; the daemon/control client is the sole source of truth. */
export function registerControlTaskTools(pi: Pi, control: ControlTaskInterface): void {
  const add = (name: string, description: string, required: string[], properties: Record<string, unknown>, call: (p: any) => Promise<unknown>) => pi.registerTool(withDefaultToolRenderer({ name, label: name, description, parameters: schema(required, properties), async execute(_id: string, p: unknown) { try { return text(await call(p)); } catch (error) { return { content: [{ type: "text", text: error instanceof Error ? error.message : String(error) }], isError: true, details: {} }; } } }));
  const id = { type: "string", minLength: 1 };
  add("goal_get", "Get a goal from the authoritative control plane.", ["id"], { id }, p => control.goalGet(p.id as ID));
  add("task_create", "Create a task through the authoritative control plane.", ["prompt"], { prompt: { type: "string" }, goalId: id, idempotencyKey: { type: "string" } }, p => control.taskCreate(p));
  add("task_get", "Get a task from the authoritative control plane.", ["id"], { id }, p => control.taskGet(p.id as ID));
  add("task_status", "Get task status from the authoritative control plane.", ["id"], { id }, p => control.taskStatus(p.id as ID));
  add("task_cancel", "Cancel a task through the authoritative control plane.", ["id"], { id }, p => control.taskCancel(p.id as ID));
  add("run_create", "Create a task run through the authoritative control plane.", ["taskId"], { taskId: id, idempotencyKey: { type: "string" } }, p => control.runCreate(p));
  add("run_get", "Get a run from the authoritative control plane.", ["id"], { id }, p => control.runGet(p.id as ID));
  add("run_status", "Get run status from the authoritative control plane.", ["id"], { id }, p => control.runStatus(p.id as ID));
  add("run_cancel", "Cancel a run through the authoritative control plane.", ["id"], { id }, p => control.runCancel(p.id as ID));
}
export default function controlTaskToolsExtension(pi: Pi, control?: ControlTaskInterface) {
  // Pi auto-loads every file in .pi/extensions. This file is also an adapter
  // helper imported by swarm-runtime, so standalone discovery must be inert;
  // only the runtime integration may provide the authoritative control plane.
  if (control) registerControlTaskTools(pi, control);
}
