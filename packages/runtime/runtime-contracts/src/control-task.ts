import type { ID, JsonObject } from "./contracts.js";

export type EntityState = "queued" | "running" | "completed" | "failed" | "cancelled";
export interface Goal { id: ID; description: string; state: EntityState; createdAt: string; updatedAt: string; }
export interface Task { id: ID; goalId?: ID; prompt: string; state: EntityState; createdAt: string; updatedAt: string; }
export interface Run { id: ID; taskId: ID; state: EntityState; attempt: number; createdAt: string; updatedAt: string; result?: JsonObject; }
export interface CreateGoalRequest { description: string; idempotencyKey?: string; }
export interface CreateTaskRequest { goalId?: ID; prompt: string; idempotencyKey?: string; }
export interface CreateRunRequest { taskId: ID; idempotencyKey?: string; }

/** Authoritative control/task boundary. Transports and Pi tools only adapt this interface. */
export interface ControlTaskInterface {
  goalCreate(request: CreateGoalRequest): Promise<Goal>;
  goalGet(id: ID): Promise<Goal>;
  taskCreate(request: CreateTaskRequest): Promise<Task>;
  taskGet(id: ID): Promise<Task>;
  taskStatus(id: ID): Promise<Task>;
  taskCancel(id: ID): Promise<Task>;
  runCreate(request: CreateRunRequest): Promise<Run>;
  runGet(id: ID): Promise<Run>;
  runStatus(id: ID): Promise<Run>;
  runCancel(id: ID): Promise<Run>;
}
