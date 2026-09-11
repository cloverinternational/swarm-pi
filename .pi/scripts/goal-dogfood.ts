/**
 * Dogfood the /goal agentic evaluator against an old session transcript.
 * Usage: npx tsx .pi/scripts/goal-dogfood.ts <session.jsonl> "<goal condition>"
 * Uses the REAL defaultEvaluator (no stubs): real provider, real grep/read tool loop.
 */
import { readFileSync } from "node:fs";
import { execSync } from "node:child_process";
import { registerSwarmGoal } from "../lib/tools/swarm-goal.ts";

const [file, condition] = process.argv.slice(2);
if (!file || !condition) { console.error("usage: goal-dogfood.ts <session.jsonl> '<goal condition>'"); process.exit(1); }

// Load messages from an old Pi session log.
const messages = readFileSync(file, "utf8").split("\n").filter(Boolean).flatMap(line => {
  try { const d = JSON.parse(line); return d.type === "message" ? [d.message] : []; } catch { return []; }
});
console.log(`loaded ${messages.length} messages from ${file}`);

// Build model + auth from the user's real Pi config (same source a live session uses).
const cfg = JSON.parse(readFileSync(`${process.env.HOME}/.pi/agent/models.json`, "utf8"));
const provider = "clover-plexus";
const p = cfg.providers[provider];
const entry = p.models.find((m: any) => m.id === "luna");
const apiKey = p.apiKey.startsWith("!") ? execSync(p.apiKey.slice(1), { encoding: "utf8" }).trim() : p.apiKey;
const model = { ...entry, provider, api: p.api, baseUrl: p.baseUrl };

// Minimal fake-pi harness (same shape as the unit tests) with the REAL evaluator.
const handlers = new Map<string, any[]>();
const pi = {
  registerTool: () => {}, registerCommand: (n: string, c: any) => handlers.set(`cmd:${n}`, [c.handler]),
  on: (n: string, h: any) => handlers.set(n, [...(handlers.get(n) ?? []), h]),
  sendMessage: (m: any) => console.log(`\n[wake] ${String(m.content).slice(0, 200)}`), appendEntry: () => {},
};
const api = registerSwarmGoal(pi as any);
const ctx = {
  model,
  modelRegistry: { getApiKeyAndHeaders: async () => ({ ok: true, apiKey, headers: undefined }) },
  ui: { notify: (t: string, l = "info") => console.log(`[notify:${l}] ${t}`) },
  sessionManager: { getBranch: () => [] },
  goalDebug: (name: string, args: any) => console.log(`  [judge tool] ${name} ${JSON.stringify(args).slice(0, 160)}`),
};
const emit = async (n: string, e: any) => { for (const h of handlers.get(n) ?? []) await h(e, ctx); };

await handlers.get("cmd:goal")![0](condition, ctx);
console.log(`goal set: ${condition}\nrunning agentic evaluation...`);
const t0 = Date.now();
await emit("agent_end", { messages });
console.log(`\ndone in ${((Date.now() - t0) / 1000).toFixed(1)}s — final status: ${api.getGoal()?.status}`);
