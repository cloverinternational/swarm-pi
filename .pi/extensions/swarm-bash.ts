import {
  SWARM_BASH_DESCRIPTION,
  SWARM_BASH_PARAMETERS,
  buildResultXML,
  commandFailedMessage,
  runSwarmBash,
  timedOutMessage,
  type BashParams,
} from "../lib/swarm-bash.ts";
import { PERMISSIVE_PARAMETERS } from "../lib/swarm-tool-surface.ts";

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
 *
 * Swarm's bash-only pre-tool hooks (sleep-blocker, stdin-conflict) live in
 * .pi/extensions/swarm-builtin-hooks.ts so they run in HooksManager priority
 * order with the task/skill gates and embed their context the same way.
 */
export function registerSwarmBash(pi: Pi): void {
  if (registrations.has(pi as object)) return;
  registrations.add(pi as object);
  const text = (value: string, details: Record<string, unknown> = {}) => ({ content: [{ type: "text", text: value }], details });
  // Pi marks a tool result as failed only when execute() throws; the thrown
  // message becomes the result content verbatim (docs/extensions.md,
  // "Signaling errors"). Returning { isError: true } is silently ignored.
  const fail = (value: string): never => { throw new Error(value); };
  pi.registerTool?.({
    name: "bash",
    label: "bash",
    description: SWARM_BASH_DESCRIPTION,
    // Swarm decodes BashParams with encoding/json: a missing command runs
    // `bash -c ""`. The canonical schema (SWARM_BASH_PARAMETERS) is put on
    // the wire by the transport-parity overlay; Pi must not pre-validate.
    parameters: { ...PERMISSIVE_PARAMETERS },
    async execute(_toolCallId: string, params: BashParams, signal: AbortSignal | undefined, _onUpdate: unknown, ctx: any) {
      params = { ...(params ?? {}), command: typeof params?.command === "string" ? params.command : "" };
      const outcome = await runSwarmBash(params, { defaultCwd: ctx?.cwd ?? pi.getCwd?.() ?? process.cwd(), signal });
      if ("error" in outcome) return fail(outcome.error);
      const details = { exit_code: outcome.exitCode, duration_ms: outcome.durationMs, timed_out: outcome.timedOut, command: params.command, ...(params.description ? { description: params.description } : {}) };
      if (outcome.timedOut) return fail(timedOutMessage(outcome.effectiveSecs, outcome.exitCode));
      if (outcome.exitCode !== 0) return fail(commandFailedMessage(outcome.exitCode, outcome.stdout, outcome.stderr));
      return text(buildResultXML(outcome), details);
    },
  });
}

export default function swarmBashExtension(pi: Pi): void { registerSwarmBash(pi); }
