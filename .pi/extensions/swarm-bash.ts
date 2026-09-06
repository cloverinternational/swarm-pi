import {
  SWARM_BASH_DESCRIPTION,
  SWARM_BASH_PARAMETERS,
  buildResultXML,
  commandFailedMessage,
  runSwarmBash,
  timedOutMessage,
  type BashParams,
} from "../lib/swarm-bash.ts";

type Pi = any;
const registrations = new WeakSet<object>();

/**
 * Replace Pi's builtin `bash` with Swarm's `bash` contract (same name, so the
 * extension definition overrides the builtin in Pi's tool registry): identical
 * description and JSON Schema, 60-second minimum timeout, non-interactive env,
 * ANSI stripping, 2000-line / 12.5K-token truncation with disk spill, and the
 * `<result exit_code=… duration_ms=… timed_out=…>` XML envelope the model sees
 * from `swarm -p`. Non-zero exits surface exactly like Swarm's
 * `Error executing bash: Command exited with code N …` tool error.
 */
export function registerSwarmBash(pi: Pi): void {
  if (registrations.has(pi as object)) return;
  registrations.add(pi as object);
  const text = (value: string, details: Record<string, unknown> = {}, isError = false) => ({ content: [{ type: "text", text: value }], details, ...(isError ? { isError: true } : {}) });
  pi.registerTool?.({
    name: "bash",
    label: "bash",
    description: SWARM_BASH_DESCRIPTION,
    parameters: SWARM_BASH_PARAMETERS,
    async execute(_toolCallId: string, params: BashParams, signal: AbortSignal | undefined, _onUpdate: unknown, ctx: any) {
      const outcome = await runSwarmBash(params, { defaultCwd: ctx?.cwd ?? pi.getCwd?.() ?? process.cwd(), signal });
      if ("error" in outcome) return text(outcome.error, { error: outcome.error }, true);
      const details = { exit_code: outcome.exitCode, duration_ms: outcome.durationMs, timed_out: outcome.timedOut, command: params.command, ...(params.description ? { description: params.description } : {}) };
      if (outcome.timedOut) return text(timedOutMessage(outcome.effectiveSecs, outcome.exitCode), details, true);
      if (outcome.exitCode !== 0) return text(commandFailedMessage(outcome.exitCode, outcome.stdout, outcome.stderr), details, true);
      return text(buildResultXML(outcome), details);
    },
  });
}

export default function swarmBashExtension(pi: Pi): void { registerSwarmBash(pi); }
