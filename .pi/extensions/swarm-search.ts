/**
 * Port of Swarm's xAI tools (internal/tools/xai/{responses,x_search,web_search}.go).
 * Both are advertised only when xaiHasCredentials() (swarm-tool-gating.ts);
 * the description/schema on the wire come from the Swarm capture
 * (fixtures/swarm-conditional-tools.json). Results are XMLBuilder envelopes:
 *   <error>\n  <message><![CDATA[error: …]]></message>\n</error>
 *   <result tool="x_search"[ query="…"]>\n  <content><![CDATA[{json}]]></content>\n</result>
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { withSwarmToolSurface } from "../lib/swarm-tool-surface.ts";
import { goQuote } from "../lib/swarm-bash.ts";

const DEFAULT_BASE_URL = "https://api.x.ai/v1";
const DEFAULT_MODEL = "grok-4.3";
const DEFAULT_TIMEOUT_MS = 120_000;
const MAX_RESULT_SIZE = 100_000;

/** oauth_config.go GetStoredOAuthToken + IsExpired. */
function storedToken(): { access_token?: string; refresh_token?: string; expires_at?: number } | undefined {
  const file = join(process.env.SWARM_HOME || join(process.env.HOME ?? process.cwd(), ".swarm"), "config", "oauth", "xai.json");
  try { const token = JSON.parse(readFileSync(file, "utf8"))?.token; return token && typeof token === "object" ? token : undefined; } catch { return undefined; }
}
const isExpired = (t: { expires_at?: number }) => { const at = Number(t.expires_at ?? 0); return at !== 0 && Math.floor(Date.now() / 1000) > at - 60; };
/** responses.go HasCredentials. */
export function hasCredentials(): boolean {
  const token = storedToken();
  if (token && (!isExpired(token) || (token.refresh_token ?? "") !== "")) return true;
  return (process.env.XAI_API_KEY ?? "").trim() !== "";
}
/** responses.go resolveBearer (an expired token cannot be refreshed here → env fallback). */
function resolveBearer(): string {
  const token = storedToken();
  if (token && !isExpired(token) && token.access_token) return token.access_token;
  const key = (process.env.XAI_API_KEY ?? "").trim();
  if (key) return key;
  throw new Error("no xAI credentials found — run /auth xai or set XAI_API_KEY");
}

const xmlError = (message: string) => `<error>\n  <message><![CDATA[${message}]]></message>\n</error>`;
const text = (value: string) => ({ content: [{ type: "text", text: value }], details: {} });

/** x_search.go extractHandles: strip a leading "@", drop blanks; ok only when non-empty. */
const extractHandles = (params: any, key: string): string[] | undefined => {
  const raw = params?.[key];
  if (!Array.isArray(raw)) return undefined;
  const out = raw.filter((h: unknown) => typeof h === "string").map((h: string) => h.replace(/^@/, "").trim()).filter(Boolean);
  return out.length ? out : undefined;
};
/** web_search.go extractDomains. */
const extractDomains = (params: any, key: string): string[] => {
  const raw = params?.[key];
  if (!Array.isArray(raw)) return [];
  return raw.filter((d: unknown) => typeof d === "string").map((d: string) => d.trim()).filter(Boolean);
};

interface ResponsesResult { answer: string; citations?: string[]; inline_citations?: Array<{ url: string; title?: string; start_index?: number; end_index?: number }> }
/** responses.go callResponses. */
async function callResponses(prompt: string, toolDef: Record<string, unknown>, signal?: AbortSignal): Promise<ResponsesResult> {
  const apiKey = resolveBearer();
  const body = JSON.stringify({ model: DEFAULT_MODEL, input: [{ role: "user", content: prompt }], tools: [toolDef], store: false });
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), DEFAULT_TIMEOUT_MS);
  signal?.addEventListener("abort", () => controller.abort(), { once: true });
  let response: Response;
  try {
    response = await fetch(`${DEFAULT_BASE_URL}/responses`, { method: "POST", headers: { Authorization: `Bearer ${apiKey}`, "Content-Type": "application/json", "User-Agent": "SwarmCode-TUI/1.0 (+https://swarmcode.ai)" }, body, signal: controller.signal });
  } catch (e) { clearTimeout(timer); throw new Error(`xAI responses: request failed: ${e instanceof Error ? e.message : String(e)}`); }
  clearTimeout(timer);
  const raw = Buffer.from(await response.arrayBuffer()).subarray(0, 4 * 1024 * 1024).toString("utf8");
  if (response.status >= 400) {
    try { const parsed = JSON.parse(raw); if (parsed?.error?.message) throw new Error(`xAI responses HTTP ${response.status}: ${parsed.error.message}`); } catch (e) { if (e instanceof Error && e.message.startsWith("xAI responses HTTP")) throw e; }
    throw new Error(`xAI responses HTTP ${response.status}: ${Buffer.from(raw.trim()).subarray(0, 300).toString("utf8")}`);
  }
  let reply: any;
  try { reply = JSON.parse(raw); } catch (e) { throw new Error(`xAI responses: parse reply: ${e instanceof Error ? e.message : String(e)}`); }
  if (reply?.error?.message) throw new Error(`xAI returned error: ${reply.error.message}`);
  const parts: string[] = [], inline: ResponsesResult["inline_citations"] = [];
  for (const item of reply?.output ?? []) {
    if (item?.type !== "message") continue;
    for (const content of item.content ?? []) {
      if (content?.type === "output_text" && String(content.text ?? "").trim() !== "") parts.push(content.text);
      for (const ann of content?.annotations ?? []) if (ann?.type === "url_citation" && ann.url) inline.push({ url: ann.url, ...(ann.title ? { title: ann.title } : {}), ...(ann.start_index != null ? { start_index: ann.start_index } : {}), ...(ann.end_index != null ? { end_index: ann.end_index } : {}) });
    }
  }
  let answer = parts.join("\n\n");
  if (Buffer.byteLength(answer) > MAX_RESULT_SIZE) answer = Buffer.from(answer).subarray(0, MAX_RESULT_SIZE).toString("utf8") + "\n\n[truncated]";
  return { answer, ...(Array.isArray(reply?.citations) && reply.citations.length ? { citations: reply.citations } : {}), ...(inline.length ? { inline_citations: inline } : {}) };
}

/** Go map[string]any → MarshalIndent: keys sorted. */
const marshalIndent = (out: Record<string, unknown>) => JSON.stringify(Object.fromEntries(Object.keys(out).sort().map(k => [k, out[k]])), null, 2);
const resultJSON = (tool: string, query: string, r: ResponsesResult) => marshalIndent({ success: true, tool, query, answer: r.answer, ...(r.citations ? { citations: r.citations } : {}), ...(r.inline_citations ? { inline_citations: r.inline_citations } : {}) });

export async function executeXSearch(params: any, signal?: AbortSignal) {
  const query = String(params?.query ?? "").trim();
  if (query === "") return text(xmlError("error: query is required"));
  if (!hasCredentials()) return text(xmlError("error: no xAI credentials found — run /auth xai to sign in with SuperGrok OAuth"));
  const toolDef: Record<string, unknown> = { type: "x_search" };
  const allowed = extractHandles(params, "allowed_x_handles");
  if (allowed) {
    if (allowed.length > 10) return text(xmlError("error: allowed_x_handles supports at most 10 handles"));
    if (extractHandles(params, "excluded_x_handles")) return text(xmlError("error: allowed_x_handles and excluded_x_handles cannot both be set"));
    toolDef.allowed_x_handles = allowed;
  } else {
    const excluded = extractHandles(params, "excluded_x_handles");
    if (excluded) {
      if (excluded.length > 10) return text(xmlError("error: excluded_x_handles supports at most 10 handles"));
      toolDef.excluded_x_handles = excluded;
    }
  }
  const from = String(params?.from_date ?? "").trim(); if (from) toolDef.from_date = from;
  const to = String(params?.to_date ?? "").trim(); if (to) toolDef.to_date = to;
  if (params?.enable_image_understanding === true) toolDef.enable_image_understanding = true;
  if (params?.enable_video_understanding === true) toolDef.enable_video_understanding = true;
  let result: ResponsesResult;
  try { result = await callResponses(query, toolDef, signal); } catch (e) { return text(xmlError(`x_search error: ${e instanceof Error ? e.message : String(e)}`)); }
  return text(`<result tool="x_search">\n  <content><![CDATA[${resultJSON("x_search", query, result)}]]></content>\n</result>`);
}

export async function executeWebSearch(params: any, signal?: AbortSignal) {
  const query = String(params?.query ?? "").trim();
  if (query === "") return text(xmlError("error: query is required"));
  if (!hasCredentials()) return text(xmlError("error: no xAI credentials found — run /auth xai to sign in with SuperGrok OAuth"));
  const toolDef: Record<string, unknown> = { type: "web_search" };
  const allowed = extractDomains(params, "allowed_domains"), excluded = extractDomains(params, "excluded_domains");
  if (allowed.length > 0 && excluded.length > 0) return text(xmlError("error: allowed_domains and excluded_domains cannot both be set"));
  if (allowed.length > 5) return text(xmlError("error: allowed_domains supports at most 5 domains"));
  if (excluded.length > 5) return text(xmlError("error: excluded_domains supports at most 5 domains"));
  if (allowed.length > 0) toolDef.filters = { allowed_domains: allowed }; else if (excluded.length > 0) toolDef.filters = { excluded_domains: excluded };
  let result: ResponsesResult;
  try { result = await callResponses(query, toolDef, signal); } catch (e) { return text(xmlError(`xai_web_search error: ${e instanceof Error ? e.message : String(e)}`)); }
  return text(`<result tool="xai_web_search" query=${goQuote(query)}>\n  <content><![CDATA[${resultJSON("xai_web_search", query, result)}]]></content>\n</result>`);
}

export default function swarmSearchExtension(rawPi: any) {
  // Description + JSON Schema on the wire come from the Swarm capture
  // (fixtures/swarm-conditional-tools.json) and the permissive validator.
  const pi = withSwarmToolSurface(rawPi);
  pi.registerTool({ name: "xai_web_search", label: "xAI Web Search", description: "", parameters: { type: "object" }, execute: (_id: string, p: any, signal: AbortSignal) => executeWebSearch(p, signal) });
  pi.registerTool({ name: "x_search", label: "X Search", description: "", parameters: { type: "object" }, execute: (_id: string, p: any, signal: AbortSignal) => executeXSearch(p, signal) });
}
