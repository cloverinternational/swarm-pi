import { createHash } from "node:crypto";

const webSchema = { type: "object", required: ["query"], additionalProperties: false, properties: { query: { type: "string" }, allowed_domains: { type: "array", items: { type: "string" }, maxItems: 5 }, excluded_domains: { type: "array", items: { type: "string" }, maxItems: 5 } } } as const;
const xSchema = { type: "object", required: ["query"], additionalProperties: false, properties: { query: { type: "string" }, allowed_x_handles: { type: "array", items: { type: "string" }, maxItems: 10 }, excluded_x_handles: { type: "array", items: { type: "string" }, maxItems: 10 }, from_date: { type: "string" }, to_date: { type: "string" } } } as const;
import { readFileSync } from "node:fs";
import { join } from "node:path";
const key = (name: "XAI_API_KEY") => {
  const env = process.env[name]?.trim(); if (env) return env;
  try { const d = JSON.parse(readFileSync(process.env.PI_SWARM_API_KEYS ?? join(process.env.HOME ?? process.cwd(), ".swarmos", "xai_oauth.json"), "utf8")); return d?.token?.access_token?.trim(); } catch { return undefined; }
};
async function search(tool: "web_search" | "x_search", p: any, signal?: AbortSignal) {
  const apiKey = key("XAI_API_KEY"); if (!apiKey) throw new Error("XAI_API_KEY is not configured");
  const body: any = { model: process.env.XAI_SEARCH_MODEL ?? "grok-4.3", input: [{ role: "user", content: p.query.trim() }], tools: [{ type: tool }], store: false };
  if (tool === "x_search") { for (const n of ["allowed_x_handles", "excluded_x_handles", "from_date", "to_date"]) if (p[n] !== undefined) body.tools[0][n] = p[n]; } else if (p.allowed_domains?.length) body.tools[0].filters = { allowed_domains: p.allowed_domains }; else if (p.excluded_domains?.length) body.tools[0].filters = { excluded_domains: p.excluded_domains };
  const r = await fetch(`${process.env.XAI_BASE_URL ?? "https://api.x.ai/v1"}/responses`, { method: "POST", headers: { Authorization: `Bearer ${apiKey}`, "content-type": "application/json" }, body: JSON.stringify(body), signal });
  const data: any = await r.json(); if (!r.ok) throw new Error(`xAI HTTP ${r.status}: ${data?.error?.message ?? "request failed"}`);
  const text = (data.output ?? []).flatMap((o: any) => o.content ?? []).filter((c: any) => c.type === "output_text").map((c: any) => c.text).join("\n\n");
  return { tool, query: p.query, answer: text, citations: data.citations ?? [], requestHash: createHash("sha256").update(JSON.stringify(body)).digest("hex") };
}
export default function swarmSearchExtension(pi: any) {
  pi.registerTool({ name: "xai_web_search", label: "xAI Web Search", description: "Search the live web through xAI with citations. Requires XAI_API_KEY.", parameters: webSchema, async execute(_id: string, p: any, signal: AbortSignal) { try { return { content: [{ type: "text", text: JSON.stringify(await search("web_search", p, signal)) }], details: {} }; } catch (e) { return { content: [{ type: "text", text: e instanceof Error ? e.message : String(e) }], isError: true, details: {} }; } } });
  pi.registerTool({ name: "x_search", label: "X Search", description: "Search X posts and profiles through xAI. Requires XAI_API_KEY.", parameters: xSchema, async execute(_id: string, p: any, signal: AbortSignal) { try { return { content: [{ type: "text", text: JSON.stringify(await search("x_search", p, signal)) }], details: {} }; } catch (e) { return { content: [{ type: "text", text: e instanceof Error ? e.message : String(e) }], isError: true, details: {} }; } } });
}
