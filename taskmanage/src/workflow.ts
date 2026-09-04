/** A small, host-agnostic workflow runtime. Hosts provide the agent runner; this
 * module owns orchestration, limits, persistence hooks, and lifecycle events. */
export type WorkflowStrategy = "parallel" | "sequential" | "first";
export type FailurePolicy = "fail_fast" | "continue";
export type WorkflowStatus = "completed" | "failed" | "cancelled";

export interface WorkflowAgent { id: string; input?: unknown; maxRetries?: number; route?: string }
export interface WorkflowGroup {
  id: string; agents: WorkflowAgent[]; dependsOn?: string[];
  strategy?: WorkflowStrategy; maxFailures?: number; timeoutMs?: number;
}
export interface WorkflowBudget { maxAttempts?: number; maxTokens?: number; maxCost?: number }
export interface WorkflowDefinition {
  id: string; groups: WorkflowGroup[]; budget?: WorkflowBudget;
  failurePolicy?: FailurePolicy; maxRetries?: number;
}
export interface AgentUsage { tokens?: number; cost?: number }
export interface AgentResult { output?: unknown; usage?: AgentUsage }
export interface WorkflowRunContext { workflowId: string; groupId: string; agent: WorkflowAgent; attempt: number; signal: AbortSignal; previous?: unknown }
export type AgentRunner = (context: WorkflowRunContext) => Promise<AgentResult>;
export interface WorkflowCheckpoint { workflowId: string; status: WorkflowStatus | "running"; completedGroups: string[]; results: Record<string, AgentResult>; attempts: number; tokens: number; cost: number }
export interface CheckpointStore { load(workflowId: string): Promise<WorkflowCheckpoint | undefined> | WorkflowCheckpoint | undefined; save(checkpoint: WorkflowCheckpoint): Promise<void> | void }
export type WorkflowEventType = "workflow_started" | "workflow_completed" | "workflow_failed" | "workflow_cancelled" | "group_started" | "group_completed" | "group_failed" | "agent_started" | "agent_completed" | "agent_failed" | "checkpoint_saved";
export interface WorkflowEvent { type: WorkflowEventType; workflowId: string; groupId?: string; agentId?: string; attempt?: number; message?: string; result?: AgentResult; checkpoint?: WorkflowCheckpoint }
export interface WorkflowRun { status: WorkflowStatus; results: Record<string, AgentResult>; attempts: number; tokens: number; cost: number; completedGroups: string[]; error?: string }

const clone = <T>(v: T): T => structuredClone(v);
const emit = (cb: ((event: WorkflowEvent) => void) | undefined, event: WorkflowEvent) => cb?.(event);

export class WorkflowEngine {
  constructor(private readonly onEvent?: (event: WorkflowEvent) => void, private readonly checkpoints?: CheckpointStore) {}

  validate(def: WorkflowDefinition): string[] {
    const errors: string[] = [], ids = new Set<string>();
    if (!def?.id) errors.push("workflow id is required");
    if (!Array.isArray(def?.groups) || !def.groups.length) errors.push("workflow requires at least one group");
    for (const group of def.groups ?? []) {
      if (!group.id || ids.has(group.id)) errors.push(`duplicate or empty group id: ${group.id}`); ids.add(group.id);
      if (!Array.isArray(group.agents) || !group.agents.length) errors.push(`group ${group.id} requires agents`);
      if (group.strategy && !["parallel", "sequential", "first"].includes(group.strategy)) errors.push(`invalid strategy for group ${group.id}`);
      for (const agent of group.agents ?? []) if (!agent.id) errors.push(`group ${group.id} contains an agent without an id`);
    }
    for (const group of def.groups ?? []) for (const dep of group.dependsOn ?? []) if (!ids.has(dep)) errors.push(`group ${group.id} depends on missing group ${dep}`);
    const visiting = new Set<string>(), visited = new Set<string>();
    const walk = (id: string) => { if (visiting.has(id)) { errors.push("workflow group dependency cycle"); return; } if (visited.has(id)) return; visiting.add(id); const g = def.groups.find(x => x.id === id); for (const d of g?.dependsOn ?? []) walk(d); visiting.delete(id); visited.add(id); };
    for (const id of ids) walk(id);
    return [...new Set(errors)];
  }

  async run(def: WorkflowDefinition, runner: AgentRunner, signal: AbortSignal = new AbortController().signal): Promise<WorkflowRun> {
    const validation = this.validate(def);
    if (validation.length) throw new Error(validation.join("; "));
    const budget = def.budget ?? {}, policy = def.failurePolicy ?? "fail_fast";
    const saved = await this.checkpoints?.load(def.id);
    const completed = new Set(saved?.completedGroups ?? []), results: Record<string, AgentResult> = clone(saved?.results ?? {});
    let attempts = saved?.attempts ?? 0, tokens = saved?.tokens ?? 0, cost = saved?.cost ?? 0;
    emit(this.onEvent, { type: "workflow_started", workflowId: def.id });
    const checkpoint = async (status: WorkflowCheckpoint["status"] = "running") => {
      const value: WorkflowCheckpoint = { workflowId: def.id, status, completedGroups: [...completed], results: clone(results), attempts, tokens, cost };
      await this.checkpoints?.save(value); emit(this.onEvent, { type: "checkpoint_saved", workflowId: def.id, checkpoint: value });
    };
    const fail = async (status: WorkflowStatus, error?: string): Promise<WorkflowRun> => { await checkpoint(status); emit(this.onEvent, { type: status === "cancelled" ? "workflow_cancelled" : "workflow_failed", workflowId: def.id, message: error }); return { status, results, attempts, tokens, cost, completedGroups: [...completed], error }; };
    try {
      while (completed.size < def.groups.length) {
        if (signal.aborted) return fail("cancelled", "workflow cancelled");
        const ready = def.groups.filter(g => !completed.has(g.id) && (g.dependsOn ?? []).every(d => completed.has(d)));
        if (!ready.length) return fail("failed", "no runnable groups remain");
        // Independent groups are safe to run concurrently, while each group
        // retains its declared agent strategy.
        const wave = await Promise.all(ready.map(group => this.runGroup(def, group, runner, signal, policy, results, () => ({ attempts, tokens, cost }), v => { attempts = v.attempts; tokens = v.tokens; cost = v.cost; })));
        for (const item of wave) {
          if (!item.ok && policy === "fail_fast") return fail(signal.aborted ? "cancelled" : "failed", item.error);
          if (item.ok) { completed.add(item.group.id); emit(this.onEvent, { type: "group_completed", workflowId: def.id, groupId: item.group.id }); await checkpoint(); }
        }
        if (wave.some(x => !x.ok) && policy === "continue") for (const item of wave.filter(x => !x.ok)) completed.add(item.group.id);
      }
      await checkpoint("completed"); emit(this.onEvent, { type: "workflow_completed", workflowId: def.id });
      return { status: "completed", results, attempts, tokens, cost, completedGroups: [...completed] };
    } catch (error) { return fail(signal.aborted ? "cancelled" : "failed", error instanceof Error ? error.message : String(error)); }
  }

  private async runGroup(def: WorkflowDefinition, group: WorkflowGroup, runner: AgentRunner, signal: AbortSignal, policy: FailurePolicy, results: Record<string, AgentResult>, usage: () => { attempts: number; tokens: number; cost: number }, update: (v: { attempts: number; tokens: number; cost: number }) => void) {
    emit(this.onEvent, { type: "group_started", workflowId: def.id, groupId: group.id });
    const agents = group.strategy === "first" ? group.agents.slice() : group.agents;
    const runOne = async (agent: WorkflowAgent): Promise<boolean> => {
      const max = agent.maxRetries ?? def.maxRetries ?? 0;
      for (let attempt = 1; attempt <= max + 1; attempt++) {
        if (signal.aborted) throw new Error("workflow cancelled");
        const current = usage(); if (def.budget?.maxAttempts !== undefined && current.attempts >= def.budget.maxAttempts) throw new Error("workflow attempt budget exhausted");
        update({ ...current, attempts: current.attempts + 1 }); emit(this.onEvent, { type: "agent_started", workflowId: def.id, groupId: group.id, agentId: agent.id, attempt });
        try {
          const controller = new AbortController(), timer = group.timeoutMs ? setTimeout(() => controller.abort(), group.timeoutMs) : undefined;
          const abort = () => controller.abort(); signal.addEventListener("abort", abort, { once: true });
          const execution = runner({ workflowId: def.id, groupId: group.id, agent, attempt, signal: controller.signal, previous: results[agent.id]?.output });
          const result = timer
            ? await Promise.race([execution, new Promise<never>((_, reject) => setTimeout(() => reject(new Error(`group ${group.id} timed out`)), group.timeoutMs))])
            : await execution;
          if (timer) clearTimeout(timer); signal.removeEventListener("abort", abort);
          const next = usage(), t = result.usage?.tokens ?? 0, c = result.usage?.cost ?? 0;
          if (def.budget?.maxTokens !== undefined && next.tokens + t > def.budget.maxTokens) throw new Error("workflow token budget exhausted");
          if (def.budget?.maxCost !== undefined && next.cost + c > def.budget.maxCost) throw new Error("workflow cost budget exhausted");
          update({ ...next, tokens: next.tokens + t, cost: next.cost + c }); results[agent.id] = result; emit(this.onEvent, { type: "agent_completed", workflowId: def.id, groupId: group.id, agentId: agent.id, attempt, result }); return true;
        } catch (error) {
          emit(this.onEvent, { type: "agent_failed", workflowId: def.id, groupId: group.id, agentId: agent.id, attempt, message: error instanceof Error ? error.message : String(error) });
          if (signal.aborted) throw new Error("workflow cancelled");
          if (attempt > max) return false;
        }
      }
      return false;
    };
    const ok: boolean[] = group.strategy === "sequential" ? [] : await Promise.all(agents.map(runOne));
    if (group.strategy === "sequential") for (const agent of agents) ok.push(await runOne(agent));
    const successes = ok.filter(Boolean).length, allowed = group.maxFailures ?? 0;
    if (group.strategy === "first" && successes > 0) return { ok: true, group };
    if (successes === agents.length || (policy === "continue" && agents.length - successes <= allowed)) return { ok: true, group };
    emit(this.onEvent, { type: "group_failed", workflowId: def.id, groupId: group.id, message: `${agents.length - successes} agent(s) failed` });
    return { ok: false, group, error: `group ${group.id} failed` };
  }
}
