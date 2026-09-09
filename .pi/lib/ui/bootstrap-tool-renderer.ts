/** Presentation-only renderer for the streaming bootstrap tool.
 *
 * The bootstrap tool owns execution and emits `BootstrapToolDetails` through
 * its normal onUpdate/result payload. This module deliberately keeps no
 * timers, does not infer completion, and never sends a message to the model.
 */

export type BootstrapStage =
  | "scope"
  | "selectors"
  | "task-draft"
  | "skills"
  | "tasks"
  | "complete";
export type BootstrapStatus = "running" | "complete" | "degraded" | "cancelled" | "failed";

export interface BootstrapCount { done: number; total?: number; }
export interface BootstrapFailure { summary: string; stage?: BootstrapStage; }
export interface BootstrapCitation { label: string; source?: string; }

/** The shape recommended for both execute `onUpdate` and the final details. */
export interface BootstrapToolDetails {
  stage: BootstrapStage;
  status: BootstrapStatus;
  mode?: "parallel" | "combined";
  model?: string;
  elapsedMs?: number;
  scope?: string;
  memory?: BootstrapCount;
  selectors?: BootstrapCount;
  skillsSelected?: number;
  skillsLoaded?: number;
  tasksDrafted?: number;
  tasksCommitted?: number;
  failures?: BootstrapFailure[];
  citations?: BootstrapCitation[];
  /** Set by the tool when the user requested expanded provenance. */
  showCitations?: boolean;
}

export interface BootstrapRenderOptions {
  expanded?: boolean;
  isPartial?: boolean;
  isError?: boolean;
  width?: number;
}

const stageLabel: Record<BootstrapStage, string> = {
  scope: "scope resolution",
  selectors: "memory/skill selection",
  "task-draft": "optional task draft",
  skills: "skill loading",
  tasks: "authoritative task commit",
  complete: "complete",
};

function count(value: BootstrapCount | undefined): string {
  if (!value) return "";
  return `${value.done}/${value.total === undefined ? "?" : value.total}`;
}

function statusMark(status: BootstrapStatus, running: boolean): string {
  if (running) return "⋯";
  if (status === "complete") return "✓";
  if (status === "degraded") return "!";
  if (status === "cancelled") return "−";
  if (status === "failed") return "✗";
  return "⋯";
}

/** Pure, test-friendly formatting. No line contains a synthetic footer/header. */
export function formatBootstrapTool(details: BootstrapToolDetails | undefined, options: BootstrapRenderOptions = {}): string {
  if (options.isError) details = { ...(details ?? { stage: "scope" }), status: "failed" };
  if (!details) return options.isPartial ? "⋯ bootstrap: starting" : "bootstrap: no result";
  const running = details.status === "running";
  const frames = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];
  const mark = running && details.elapsedMs !== undefined ? frames[Math.floor(details.elapsedMs / 250) % frames.length] : statusMark(details.status, running);
  const stage = stageLabel[details.stage] ?? details.stage;
  const bits = [`${mark} bootstrap · ${stage}`];
  if (details.mode) bits.push(details.mode);
  if (details.model) bits.push(details.model);
  if (details.scope) bits.push(`scope=${details.scope}`);
  if (details.memory || details.selectors) {
    const selected = [details.memory && `memory ${count(details.memory)}`, details.selectors && `selectors ${count(details.selectors)}`].filter(Boolean);
    if (selected.length) bits.push(selected.join(", "));
  }
  if (details.skillsSelected !== undefined || details.skillsLoaded !== undefined) {
    bits.push(`skills selected ${details.skillsSelected ?? 0}, loaded ${details.skillsLoaded ?? 0}`);
  }
  if (details.tasksDrafted !== undefined || details.tasksCommitted !== undefined) {
    bits.push(`tasks drafted ${details.tasksDrafted ?? 0}, committed ${details.tasksCommitted ?? 0}`);
  }
  if (details.elapsedMs !== undefined) bits.push(`${Math.max(0, details.elapsedMs)}ms`);
  const lines = [bits.join(" · ")];
  if (details.failures?.length) {
    for (const failure of details.failures.slice(0, 3)) lines.push(`  ${failure.stage ? `${failure.stage}: ` : ""}${failure.summary}`);
    if (details.failures.length > 3) lines.push(`  … ${details.failures.length - 3} more failures`);
  }
  if ((options.expanded || details.showCitations) && details.citations?.length) {
    for (const citation of details.citations.slice(0, 8)) lines.push(`  ↳ ${citation.label}${citation.source ? `: ${citation.source}` : ""}`);
  }
  return lines.join("\n");
}

class BootstrapComponent {
  private value: string;
  constructor(value: string) { this.value = value; }
  setText(value: string): void { this.value = value; }
  invalidate(): void {}
  render(width: number): string[] {
    const limit = Math.max(1, Number.isFinite(width) ? width : 80);
    return this.value.split("\n").map((line) => line.length <= limit ? line : `${line.slice(0, Math.max(1, limit - 1))}…`);
  }
}

export interface BootstrapToolRenderer {
  renderCall(args: { scope?: string } | undefined, theme?: unknown, context?: unknown): BootstrapComponent;
  renderResult(result: { details?: BootstrapToolDetails; isError?: boolean }, options?: BootstrapRenderOptions, theme?: unknown, context?: unknown): BootstrapComponent;
}

/** Factory for a tool definition. State is event-derived; it has no clock or side effects. */
export function createBootstrapToolRenderer(): BootstrapToolRenderer {
  return {
    renderCall(args) {
      return new BootstrapComponent(`⋯ bootstrap · scope resolution${args?.scope ? ` · scope=${args.scope}` : ""}`);
    },
    renderResult(result, options = {}) {
      // Each result belongs to one execution. Never reuse another call's
      // details: the renderer factory is shared across all tool rows.
      const details = result?.details;
      return new BootstrapComponent(formatBootstrapTool(details, { ...options, isError: options.isError || result?.isError }));
    },
  };
}
