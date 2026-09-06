import { createHash } from "node:crypto";
import { hookTelemetry } from "../../lib/runtime/hook-state.ts";
const hash = (x: unknown) => createHash("sha256").update(JSON.stringify(x)).digest("hex");
export default function cacheTelemetry(pi: any) {
  let previous = "", calls = 0;
  pi.on("before_provider_request", (e: any) => { const current = hash(e.payload); const changed = !!previous && previous !== current; calls++; pi.appendEntry("pi-swarm-cache-telemetry", { at: new Date().toISOString(), calls, requestHash: current, previousHash: previous || undefined, requestChanged: changed, hook: hookTelemetry().counts }); previous = current; });
  pi.on("after_provider_response", (e: any) => pi.appendEntry("pi-swarm-cache-response", { at: new Date().toISOString(), status: e.status, headers: Object.keys(e.headers ?? {}) }));
  pi.registerCommand?.("cache", { description: "Show provider cache telemetry", handler: async (_args: string, ctx: any) => { const entries = ctx.sessionManager?.getEntries?.() ?? []; const rows = entries.filter((x: any) => x.type === "pi-swarm-cache-telemetry"); const hits = rows.filter((x: any) => x.data?.requestChanged === false).length; ctx.ui?.notify?.(`Cache telemetry: ${rows.length} requests, ${hits} stable request payloads, ${rows.length - hits} changed payloads. Check provider usage cacheRead/cacheWrite for actual cache hits.`, "info"); } });
}
