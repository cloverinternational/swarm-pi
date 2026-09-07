import { loadSwarmCanonicalTools, PERMISSIVE_PARAMETERS, rawPi, withSwarmToolSurface } from "../../lib/runtime/swarm-tool-surface.ts";
import { afterTurnFlushListeners } from "../../lib/runtime/swarm-builtin-hooks-runtime.ts";
import { newErrorID } from "../../lib/tools/swarm-bash.ts";
import {
  SwarmBackgroundProcessManager,
  BACKGROUND_BASH_DETACH,
  formatBackgroundDone,
  type BackgroundBashParams,
  type ReadBackgroundParams,
} from "../../lib/tools/swarm-bgprocess.ts";
import { bashCallComponent, formatBashCall } from "../../lib/tools/swarm-bash.ts";
import { withDefaultToolRenderer } from "../../../packages/runtime/core/src/tool-renderer.ts";

type Pi = any;
const registrations = new WeakSet<object>();
const managers = new WeakMap<object, SwarmBackgroundProcessManager>();
/**
 * Texts injected mid-run as conversation.RoleSystem (app_messaging.go
 * RichMessageInjector). Pi custom messages reach the wire as role "user";
 * the transport-parity layer promotes exact matches from this set.
 */
export const BACKGROUND_SYSTEM_TEXTS = Symbol.for("pi-swarm-background-system-texts");
const systemTexts = (): Set<string> => ((globalThis as any)[BACKGROUND_SYSTEM_TEXTS] ??= new Set<string>());

export function registerSwarmBackgroundBash(inputPi: Pi): void {
  const identity = rawPi(inputPi as object);
  if (registrations.has(identity)) return;
  registrations.add(identity);
  const pi = withSwarmToolSurface(inputPi);
  const manager = new SwarmBackgroundProcessManager();
  (globalThis as any)[BACKGROUND_BASH_DETACH] = () => manager.requestBackground();
  managers.set(identity, manager);
  const fixtures = loadSwarmCanonicalTools();
  const text = (value: string, details: Record<string, unknown> = {}) => ({ content: [{ type: "text", text: value }], details });
  const fail = (value: string): never => { throw new Error(value); };

  // app_update.go bgProcessDoneMsg: while the agent runs the notification is
  // queued and flushed by the RichMessageInjector as RoleSystem messages in
  // the slot after the turn's hook-context user message, before the next
  // model call; when idle the queued texts are joined with "\n\n" and wake
  // the agent as a (bubble-suppressed) user message.
  let running = false;
  let sessionContext: any;
  const pending: string[] = [];
  const midRun: string[] = [];
  const wake = () => {
    if (running || !pending.length) return;
    const combined = pending.splice(0).join("\n\n");
    (sessionContext?.sendUserMessage ?? pi.sendUserMessage)?.(combined, { deliverAs: "followUp", triggerTurn: true });
  };
  manager.onDone((done) => {
    const body = formatBackgroundDone(done);
    if (running) { midRun.push(body); return; }
    pending.push(body);
    wake();
  });
  // Contribute the queued notifications to the post-tool hook slot as
  // RoleSystem parts (the transport layer promotes exact texts to "system").
  afterTurnFlushListeners().push(({ runContinues }) => {
    if (!running || !runContinues || !midRun.length) return undefined;
    return midRun.splice(0).map((text) => { systemTexts().add(text); return { role: "system" as const, text }; });
  });
  pi.on?.("session_start", (_e: unknown, ctx: any) => { sessionContext = ctx; });
  pi.on?.("agent_start", () => { running = true; });
  pi.on?.("agent_end", () => { running = false; pending.push(...midRun.splice(0)); wake(); });

  pi.registerTool?.(withDefaultToolRenderer({
    name: "Bash",
    label: "Bash",
    description: fixtures.get("Bash")!.description,
    // Keep the canonical permissive schema for parity; `background` is an
    // accepted execution parameter handled by SwarmBackgroundProcessManager.
    parameters: { ...PERMISSIVE_PARAMETERS },
    renderCall(args: BackgroundBashParams, theme: any) {
      return bashCallComponent(formatBashCall(args, theme));
    },
    async execute(_id: string, params: BackgroundBashParams, signal: AbortSignal | undefined, _update: unknown, ctx: any) {
      try {
        const result = await manager.executeBash(params ?? ({} as BackgroundBashParams), ctx?.cwd ?? pi.getCwd?.() ?? process.cwd(), signal);
        return text(result.text, result.details);
      } catch (e) { return fail(String((e as Error).message ?? e)); }
    },
  }));
  pi.registerTool?.(withDefaultToolRenderer({
    name: "ReadBackgroundCommand",
    label: "ReadBackgroundCommand",
    description: fixtures.get("ReadBackgroundCommand")!.description,
    parameters: { ...PERMISSIVE_PARAMETERS },
    async execute(_id: string, params: ReadBackgroundParams) {
      try {
        const result = await manager.read(params ?? {});
        return text(result.text, result.details);
      } catch (e) {
        const message = String((e as Error).message ?? e);
        // registry_impl.go: Validate errors surface as sdkerr tool errors.
        if (message.startsWith("validation failed for ReadBackgroundCommand: ")) return fail(`Error executing ReadBackgroundCommand: ${message} (error_id=${newErrorID()})`);
        return fail(message);
      }
    },
  }));
}

export default function swarmBackgroundBashExtension(pi: Pi): void {
  registerSwarmBackgroundBash(pi);
}
