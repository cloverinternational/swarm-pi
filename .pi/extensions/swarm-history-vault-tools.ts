import { boundedHistoryJSON, historyGet, historyRootFromContext, historySearch } from "../lib/swarm-history-tools.ts";
import { vaultAdd, vaultApprove, vaultExec, vaultJSONXML, vaultList, vaultTwoPersonStatus, type VaultRuntime } from "../lib/swarm-vault-tools.ts";
import { newErrorID } from "../lib/swarm-bash.ts";
import { applySwarmSurface } from "../lib/swarm-tool-surface.ts";
import { sortKeysDeep } from "../lib/swarm-transport-parity.ts";

type Pi = any;
const registrations = new WeakSet<object>();
const names = ["HistorySearch", "HistoryGet", "vault_add", "vault_approve", "vault_exec", "vault_list", "vault_two_person_status"] as const;
const labels: Record<string, string> = Object.fromEntries(names.map((name) => [name, name]));

function okay(value: unknown, xml = false) {
  // history.go jsonResult marshals a map[string]any → keys sorted at every level.
  const text = xml ? vaultJSONXML(value) : JSON.stringify(sortKeysDeep(value));
  return { content: [{ type: "text", text }], details: value };
}
function failed(name: string, error: unknown) {
  let message = error instanceof Error ? error.message : String(error);
  if ((name === "HistorySearch" || name === "HistoryGet") && !message.startsWith(`${name}:`)) message = `${name}: ${message}`;
  // Pi flags a tool result as failed only when execute() throws; the message
  // becomes the result content verbatim (docs/extensions.md "Signaling errors").
  throw new Error(`Error executing ${name}: ${message} (error_id=${newErrorID()})`);
}

export function registerSwarmHistoryVaultTools(pi: Pi, options: { historyRoot?: string; cwd?: string; vault?: VaultRuntime } = {}): void {
  if (registrations.has(pi as object)) return;
  registrations.add(pi as object);
  let sessionContext: any;
  pi.on?.("session_start", (_event: any, ctx: any) => { sessionContext = ctx; });
  const register = (name: typeof names[number], run: (p: any, ctx: any) => Promise<any> | any, xml = false) => pi.registerTool?.(applySwarmSurface({
    name, label: labels[name], description: "", parameters: {},
    async execute(_id: string, params: any, _signal: AbortSignal | undefined, _update: unknown, ctx: any) {
      try {
        const value = await run(params ?? {}, ctx ?? sessionContext);
        if (name === "HistoryGet") {
          const bounded = boundedHistoryJSON(value, params?.max_chars ?? 12000);
          return { content: [{ type: "text", text: bounded.text }], details: bounded.value };
        }
        return okay(value, xml);
      } catch (error) { return failed(name, error); }
    },
  }));
  const historyRuntime = (ctx: any) => ({ root: options.historyRoot ?? historyRootFromContext(ctx ?? sessionContext), cwd: options.cwd ?? ctx?.cwd ?? sessionContext?.cwd ?? pi.getCwd?.() ?? process.cwd() });
  register("HistorySearch", (p, ctx) => historySearch(p, historyRuntime(ctx)));
  register("HistoryGet", (p, ctx) => historyGet(p, historyRuntime(ctx)));
  register("vault_add", (p) => vaultAdd(p, options.vault), true);
  register("vault_approve", (p) => vaultApprove(p, options.vault), true);
  register("vault_exec", (p) => vaultExec(p, options.vault), true);
  register("vault_list", (p) => vaultList(p, options.vault), true);
  register("vault_two_person_status", (p) => vaultTwoPersonStatus(p, options.vault), true);
}

export default function swarmHistoryVaultToolsExtension(pi: Pi): void { registerSwarmHistoryVaultTools(pi); }
