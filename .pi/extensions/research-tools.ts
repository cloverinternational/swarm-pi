import { createHash } from "node:crypto";
import { withDefaultToolRenderer } from "../lib/swarm-tool-renderer.ts";
import { readFileSync } from "node:fs";
import { join } from "node:path";

/** Network guard shared by fetch-like research tools. Defaults are deliberately conservative. */
export type NetworkPolicy = { allowedHosts?: string[]; blockedHosts?: string[]; maxBytes?: number; timeoutMs?: number };
export type Provenance = { url: string; retrievedAt: string; status: number; contentType: string; sha256: string; bytes: number };

const DEFAULT_POLICY: Required<NetworkPolicy> = { allowedHosts: [], blockedHosts: [], maxBytes: 2_000_000, timeoutMs: 15_000 };
const privateHost = (host: string) => host === "localhost" || host.endsWith(".localhost") || host === "::1" || /^127\./.test(host) || /^10\./.test(host) || /^192\.168\./.test(host) || /^172\.(1[6-9]|2\d|3[01])\./.test(host);

export function validateURL(raw: string, policy: NetworkPolicy = {}): URL {
  let url: URL;
  try { url = new URL(raw); } catch { throw new Error("url must be absolute"); }
  if (url.protocol !== "https:") throw new Error("only https URLs are allowed");
  const p = { ...DEFAULT_POLICY, ...policy };
  const host = url.hostname.toLowerCase();
  if (privateHost(host)) throw new Error("private and loopback hosts are blocked");
  if (p.blockedHosts.some(h => host === h || host.endsWith(`.${h}`))) throw new Error(`host is blocked by network policy: ${host}`);
  if (p.allowedHosts.length && !p.allowedHosts.some(h => host === h || host.endsWith(`.${h}`))) throw new Error(`host is not in the network allowlist: ${host}`);
  return url;
}

function credential(name: string): string | undefined {
  const direct = process.env[name]?.trim(); if (direct) return direct;
  const file = process.env.PI_SWARM_API_KEYS ?? join(process.env.HOME ?? process.cwd(), ".swarmos", "credentials.json");
  try { const value = JSON.parse(readFileSync(file, "utf8")); const candidate = value?.[name] ?? value?.providers?.[name]?.api_key; return typeof candidate === "string" && candidate.trim() ? candidate.trim() : undefined; } catch { return undefined; }
}

export async function fetchEvidence(rawURL: string, init: RequestInit = {}, policy: NetworkPolicy = {}, signal?: AbortSignal): Promise<{ text: string; provenance: Provenance }> {
  const p = { ...DEFAULT_POLICY, ...policy };
  const url = validateURL(rawURL, p);
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), p.timeoutMs);
  const abort = () => controller.abort();
  signal?.addEventListener("abort", abort, { once: true });
  try {
    const response = await fetch(url, { ...init, signal: controller.signal, headers: { "user-agent": "pi-swarm-research/1", ...(init.headers ?? {}) } });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const length = Number(response.headers.get("content-length") ?? 0);
    if (length > p.maxBytes) throw new Error(`response exceeds ${p.maxBytes} byte limit`);
    const bytes = new Uint8Array(await response.arrayBuffer());
    if (bytes.byteLength > p.maxBytes) throw new Error(`response exceeds ${p.maxBytes} byte limit`);
    const text = new TextDecoder().decode(bytes);
    return { text, provenance: { url: url.toString(), retrievedAt: new Date().toISOString(), status: response.status, contentType: response.headers.get("content-type") ?? "", sha256: createHash("sha256").update(bytes).digest("hex"), bytes: bytes.byteLength } };
  } finally { clearTimeout(timer); signal?.removeEventListener("abort", abort); }
}

const schema = (properties: Record<string, unknown>) => ({ type: "object", required: ["url"], additionalProperties: false, properties });
const result = (value: unknown) => ({ content: [{ type: "text", text: JSON.stringify(value, null, 2) }], details: value });
const error = (e: unknown) => ({ content: [{ type: "text", text: e instanceof Error ? e.message : String(e) }], isError: true, details: {} });

export default function researchToolsExtension(pi: any) {
  pi.registerTool(withDefaultToolRenderer({ name: "web_fetch", label: "Web Fetch", description: "Fetch one HTTPS source with bounded bytes, timeout, network policy, and content provenance.", parameters: schema({ url: { type: "string" }, allowed_hosts: { type: "array", items: { type: "string" }, maxItems: 20 }, blocked_hosts: { type: "array", items: { type: "string" }, maxItems: 20 }, max_bytes: { type: "integer", minimum: 1024, maximum: 10000000 }, timeout_ms: { type: "integer", minimum: 1000, maximum: 60000 } }), async execute(_id: string, p: any, signal: AbortSignal) { try { return result(await fetchEvidence(p.url, {}, { allowedHosts: p.allowed_hosts, blockedHosts: p.blocked_hosts, maxBytes: p.max_bytes, timeoutMs: p.timeout_ms }, signal)); } catch (e) { return error(e); } } }));

  pi.registerTool(withDefaultToolRenderer({ name: "deepwiki", label: "DeepWiki", description: "Fetch a DeepWiki source through its configured HTTPS endpoint; credentials are loaded from the environment only.", parameters: schema({ url: { type: "string" }, question: { type: "string" } }), async execute(_id: string, p: any, signal: AbortSignal) { try { const endpoint = p.url || process.env.DEEPWIKI_BASE_URL; if (!endpoint) throw new Error("DeepWiki URL is not configured"); const token = credential("DEEPWIKI_API_KEY"); const body = p.question ? { question: p.question } : undefined; return result(await fetchEvidence(endpoint, { method: body ? "POST" : "GET", headers: token ? { authorization: `Bearer ${token}`, "content-type": "application/json" } : undefined, body: body ? JSON.stringify(body) : undefined }, {}, signal)); } catch (e) { return error(e); } } }));

  pi.registerTool(withDefaultToolRenderer({ name: "browser_get_page", label: "Browser Page Read", description: "Read a page from an explicitly configured browser automation bridge. Never performs navigation or mutation.", parameters: schema({ url: { type: "string" } }), async execute(_id: string, p: any, signal: AbortSignal) { try { const bridge = process.env.PI_BROWSER_READ_URL; if (!bridge) throw new Error("browser read bridge is not configured"); const token = credential("PI_BROWSER_TOKEN"); return result(await fetchEvidence(bridge, { method: "POST", headers: { "content-type": "application/json", ...(token ? { authorization: `Bearer ${token}` } : {}) }, body: JSON.stringify({ url: validateURL(p.url).toString(), action: "read" }) }, {}, signal)); } catch (e) { return error(e); } } }));
}
