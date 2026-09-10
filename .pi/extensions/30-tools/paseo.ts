import { withDefaultToolRenderer } from "../../../packages/runtime/core/src/tool-renderer.ts";
import { defaultListen, paseo, paseoBuild, paseoPair, paseoSetup, paseoStart, paseoStatus, paseoStop, paseoUpdate } from "../../lib/tools/paseo-setup.ts";
type UI = { notify?: (message: string, type?: string) => void };
type SessionContext = { ui?: UI };
type CommandSpec = { description: string; handler: (args: string, context: SessionContext) => Promise<void> };
type Pi = { on?: (e: string, h: (event: unknown, context: SessionContext) => unknown) => void; registerTool?: (t: unknown) => void; registerCommand?: (n: string, s: CommandSpec) => void };
type Action = "status" | "update" | "build" | "start" | "stop" | "pair" | "setup" | "health" | "serve" | "logs";
type PaseoInput = { action: Action; listen?: string; apply?: boolean; inspect?: boolean; lines?: number };
const actions: readonly Action[] = ["status", "update", "build", "start", "stop", "pair", "setup", "health", "serve", "logs"];
const isAction = (value: unknown): value is Action => typeof value === "string" && actions.includes(value as Action);
const validListen = (value: unknown): value is string => typeof value === "string" && value.length > 0 && value.length <= 256 && !/[\u0000-\u001f\u007f\s]/.test(value);
function parseInput(value: unknown): PaseoInput {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Paseo arguments must be an object");
  const input = value as Record<string, unknown>;
  if (!isAction(input.action)) throw new Error(`action must be ${actions.join(", ")}`);
  if (input.listen !== undefined && !validListen(input.listen)) throw new Error("listen must be a bounded non-empty string without whitespace");
  if (input.apply !== undefined && typeof input.apply !== "boolean") throw new Error("apply must be boolean");
  if (input.inspect !== undefined && typeof input.inspect !== "boolean") throw new Error("inspect must be boolean");
  if (input.lines !== undefined && (typeof input.lines !== "number" || !Number.isInteger(input.lines) || input.lines < 1 || input.lines > 500)) throw new Error("lines must be an integer from 1 to 500");
  return input as PaseoInput;
}
const text = (v: unknown) => ({ content: [{ type: "text", text: typeof v === "string" ? v : JSON.stringify(v, undefined, 1) }], details: v });
const parameters = { type: "object", properties: { action: { type: "string", enum: ["status", "update", "build", "start", "stop", "pair", "setup", "health", "serve", "logs"] }, listen: { type: "string" }, apply: { type: "boolean", description: "Explicitly authorize hostname and Tailscale Serve changes. Default: inspect only." }, inspect: { type: "boolean" }, lines: { type: "integer", minimum: 1, maximum: 500 } }, required: ["action"] };
export { defaultListen, paseoSetup, paseoStatus, paseoStart, paseoStop, paseoUpdate, paseoBuild, paseoPair };
export default function paseoExtension(pi: Pi) {
  pi.on?.("session_start", async (_e, ctx: SessionContext) => {
    // Startup never changes network exposure; setup requires an explicit action.
    void paseoStart(pi).then(async (r) => {
      if (!r.success) { ctx?.ui?.notify?.(`Paseo auto-start skipped: ${r.error}`, "warning"); return; }
      process.env.PASEO_HOST ||= `http://${r.listen}`;
      ctx?.ui?.notify?.(r.alreadyRunning ? `Paseo connected on ${r.listen}.` : `Paseo started on ${r.listen}.`, "info");
    }).catch(() => ctx?.ui?.notify?.("Paseo startup failed; inspect daemon status.", "warning"));
  });
  pi.registerTool?.(withDefaultToolRenderer({
    name: "paseo", label: "Paseo daemon", description: "Manage the vendored Paseo install and Tailscale setup.", parameters,
    async execute(_id: string, raw: unknown) {
      let i: PaseoInput;
      try { i = parseInput(raw); } catch (error) { return text({ success: false, error: error instanceof Error ? error.message : String(error) }); }
      switch (i?.action) {
        case "status": { const s = await paseoStatus(pi); return text({ ...s, healthy: Boolean(s.pid) && await paseo.health(s.listen) }); }
        case "update": return text(await paseoUpdate(pi));
        case "build": return text(await paseoBuild(pi));
        case "start": return text(await paseoStart(pi, i.listen));
        case "stop": return text(await paseoStop(pi));
        case "pair": return text(await paseoPair(pi));
        case "setup": case "serve": return text(await paseoSetup(pi, i.listen, i.inspect !== true && i.apply === true));
        case "health": return text({ success: await paseo.health(i.listen ?? defaultListen()) });
        case "logs": return text(paseo.logs(i.lines ?? 40));
        default: return text({ success: false, error: "action must be status, update, build, start, stop, pair, setup, health, serve, or logs" });
      }
    },
  }));
  pi.registerCommand?.("paseo", {
    description: "Paseo daemon controls: /paseo status|update|build|start|stop|pair|setup|serve|health|logs (setup/serve inspect by default; use apply to mutate)",
    handler: async (args: string, ctx: { ui?: UI }) => {
      const action = args.trim().split(/\s+/, 1)[0] || "status";
      try {
        if (action === "status") { const s = await paseoStatus(pi); ctx.ui?.notify?.(`paseo ${s.revision || "(not cloned)"} · built=${s.built} · daemon=${s.pid ? `pid ${s.pid} on ${s.listen}` : "stopped"}`, "info"); }
        else if (action === "update") { const r = await paseoUpdate(pi); ctx.ui?.notify?.(r.success ? "Updated vendor/paseo to latest upstream main. Run /paseo build next." : `Update failed: ${r.steps?.find(s => !s.ok)?.output}`, r.success ? "info" : "error"); }
        else if (action === "build") { ctx.ui?.notify?.("Building paseo (this can take several minutes)…", "info"); const r = await paseoBuild(pi); ctx.ui?.notify?.(r.success ? "Paseo built." : `Build failed: ${r.output?.slice(-500)}`, r.success ? "info" : "error"); }
        else if (action === "start") { const r = await paseoStart(pi); ctx.ui?.notify?.(r.success ? (r.alreadyRunning ? `Already running (pid ${r.pid}).` : `Daemon started (pid ${r.pid}) on ${r.listen}.`) : r.error!, r.success ? "info" : "error"); }
        else if (action === "stop") { const r = await paseoStop(pi); ctx.ui?.notify?.(!r.success ? String(r.error ?? "Stop failed; daemon record retained.") : r.wasRunning ? `Stopped daemon (pid ${r.pid}).` : "Daemon was not running.", r.success ? "info" : "error"); }
        else if (action === "pair") { const r = await paseoPair(pi); ctx.ui?.notify?.(r.ok ? r.output : `Pairing failed: ${r.output}`, r.ok ? "info" : "error"); }
        else if (action === "setup" || action === "serve") { const words = args.trim().split(/\s+/).filter(Boolean); const mode = words[1] ?? "inspect"; if (words.length > 2 || (mode !== "inspect" && mode !== "apply")) throw new Error(`usage: /paseo ${action} [inspect|apply]`); const apply = mode === "apply"; const r = await paseoSetup(pi, undefined, apply); ctx.ui?.notify?.(r.success ? `Paseo ${action} ${apply ? "applied" : "inspection"} for ${r.dnsName ?? "machine"}.` : (r.error ?? `Paseo ${action} failed`), r.success ? "info" : "error"); }
        else if (action === "health") { if (args.trim() !== "health") throw new Error("usage: /paseo health"); const healthy = await paseo.health(defaultListen()); ctx.ui?.notify?.(healthy ? "Paseo is healthy." : "Paseo is not healthy.", healthy ? "info" : "error"); }
        else if (action === "logs") ctx.ui?.notify?.(paseo.logs(40), "info");
        else throw new Error("usage: /paseo status|update|build|start|stop|pair|setup|serve|health|logs");
      } catch (error) { ctx.ui?.notify?.(error instanceof Error ? error.message : String(error), "error"); }
    },
  });
}
