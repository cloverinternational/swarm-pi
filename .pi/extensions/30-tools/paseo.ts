import { withDefaultToolRenderer } from "../../../packages/runtime/core/src/tool-renderer.ts";
import { defaultListen, paseo, paseoBuild, paseoPair, paseoSetup, paseoStart, paseoStatus, paseoStop, paseoUpdate } from "../../lib/tools/paseo-setup.ts";
type UI = { notify?: (message: string, type?: string) => void };
type Pi = { on?: (e: string, h: (...a: any[]) => unknown) => void; registerTool?: (t: unknown) => void; registerCommand?: (n: string, s: any) => void };
const text = (v: unknown) => ({ content: [{ type: "text", text: typeof v === "string" ? v : JSON.stringify(v, undefined, 1) }], details: v });
const parameters = { type: "object", properties: { action: { type: "string", enum: ["status", "update", "build", "start", "stop", "pair", "setup", "health", "serve", "logs"] }, listen: { type: "string" }, apply: { type: "boolean", description: "Explicitly authorize hostname and Tailscale Serve changes. Default: inspect only." }, inspect: { type: "boolean" }, lines: { type: "integer", minimum: 1, maximum: 500 } }, required: ["action"] };
export { defaultListen, paseoSetup, paseoStatus, paseoStart, paseoStop, paseoUpdate, paseoBuild, paseoPair };
export default function paseoExtension(pi: Pi) {
  pi.on?.("session_start", async (_e, ctx: any) => {
    // Startup never changes network exposure; setup requires an explicit action.
    void paseoStart(pi).then(async (r) => {
      if (!r.success) { ctx?.ui?.notify?.(`Paseo auto-start skipped: ${r.error}`, "warning"); return; }
      process.env.PASEO_HOST ||= `http://${r.listen}`;
      ctx?.ui?.notify?.(r.alreadyRunning ? `Paseo connected on ${r.listen}.` : `Paseo started on ${r.listen}.`, "info");
    }).catch(() => ctx?.ui?.notify?.("Paseo startup failed; inspect daemon status.", "warning"));
  });
  pi.registerTool?.(withDefaultToolRenderer({
    name: "paseo", label: "Paseo daemon", description: "Manage the vendored Paseo install and Tailscale setup.", parameters,
    async execute(_id: string, i: any) {
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
    description: "Paseo daemon controls: /paseo status|update|build|start|stop|pair|setup|logs",
    handler: async (args: string, ctx: { ui?: UI }) => {
      const action = args.trim().split(/\s+/, 1)[0] || "status";
      try {
        if (action === "status") { const s = await paseoStatus(pi); ctx.ui?.notify?.(`paseo ${s.revision || "(not cloned)"} · built=${s.built} · daemon=${s.pid ? `pid ${s.pid} on ${s.listen}` : "stopped"}`, "info"); }
        else if (action === "update") { const r = await paseoUpdate(pi); ctx.ui?.notify?.(r.success ? "Updated vendor/paseo to latest upstream main. Run /paseo build next." : `Update failed: ${r.steps?.find(s => !s.ok)?.output}`, r.success ? "info" : "error"); }
        else if (action === "build") { ctx.ui?.notify?.("Building paseo (this can take several minutes)…", "info"); const r = await paseoBuild(pi); ctx.ui?.notify?.(r.success ? "Paseo built." : `Build failed: ${r.output?.slice(-500)}`, r.success ? "info" : "error"); }
        else if (action === "start") { const r = await paseoStart(pi); ctx.ui?.notify?.(r.success ? (r.alreadyRunning ? `Already running (pid ${r.pid}).` : `Daemon started (pid ${r.pid}) on ${r.listen}.`) : r.error!, r.success ? "info" : "error"); }
        else if (action === "stop") { const r = await paseoStop(pi); ctx.ui?.notify?.(!r.success ? String(r.error ?? "Stop failed; daemon record retained.") : r.wasRunning ? `Stopped daemon (pid ${r.pid}).` : "Daemon was not running.", r.success ? "info" : "error"); }
        else if (action === "pair") { const r = await paseoPair(pi); ctx.ui?.notify?.(r.ok ? r.output : `Pairing failed: ${r.output}`, r.ok ? "info" : "error"); }
        else if (action === "setup") { const inspect = args.split(/\s+/).includes("inspect"); const r = inspect ? await paseoSetup(pi, defaultListen(), false) : await paseo.applySetup(); ctx.ui?.notify?.(r.success ? `Paseo setup ${inspect ? "inspection" : "complete"} for ${r.dnsName ?? "machine"}.` : (r.error ?? "Paseo setup failed"), r.success ? "info" : "error"); }
        else if (action === "logs") ctx.ui?.notify?.(paseo.logs(40), "info");
        else throw new Error("usage: /paseo status|update|build|start|stop|pair|setup|logs");
      } catch (error) { ctx.ui?.notify?.(error instanceof Error ? error.message : String(error), "error"); }
    },
  });
}
