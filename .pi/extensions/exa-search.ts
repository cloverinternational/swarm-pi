import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

function exaKey() {
  const env = process.env.EXA_API_KEY?.trim(); if (env) return env;
  const configured = process.env.PI_SWARM_API_KEYS ?? join(process.env.HOME ?? process.cwd(), ".swarmos", "credentials.json");
  try { const value = JSON.parse(readFileSync(configured, "utf8"))?.providers?.Exa?.api_key; if (typeof value === "string" && value.trim()) return value.trim(); } catch {}
  return undefined;
}

const schema = {
  type: "object", required: ["query"], additionalProperties: false,
  properties: {
    query: { type: "string", description: "Search query" },
    num_results: { type: "integer", minimum: 1, maximum: 20 },
    search_type: { type: "string", enum: ["auto", "neural", "fast", "instant", "deep-lite", "deep"] },
    category: { type: "string" },
    allowed_domains: { type: "array", items: { type: "string" }, maxItems: 10 },
    excluded_domains: { type: "array", items: { type: "string" }, maxItems: 10 },
    start_published_date: { type: "string" }, end_published_date: { type: "string" },
  },
} as const;

export default function exaSearchExtension(pi: any) {
  pi.registerTool({
    name: "exa_search", label: "Exa Search",
    description: "Search the web using Exa AI. Requires EXA_API_KEY. Returns concise highlights and source URLs.",
    parameters: schema,
    async execute(_id: string, params: any, signal: AbortSignal) {
      const key = exaKey();
      if (!key) return { content: [{ type: "text", text: "EXA_API_KEY is not configured" }], isError: true, details: {} };
      const query = String(params.query ?? "").trim();
      if (!query) return { content: [{ type: "text", text: "query is required" }], isError: true, details: {} };
      if (params.allowed_domains?.length && params.excluded_domains?.length) return { content: [{ type: "text", text: "allowed_domains and excluded_domains are mutually exclusive" }], isError: true, details: {} };
      const body: any = {
        query, type: params.search_type ?? "auto", numResults: params.num_results ?? 10,
        category: params.category, includeDomains: params.allowed_domains, excludeDomains: params.excluded_domains,
        startPublishedDate: params.start_published_date, endPublishedDate: params.end_published_date,
        contents: { highlights: { numSentences: 5, highlightsPerUrl: 3, query } },
      };
      Object.keys(body).forEach(k => body[k] === undefined && delete body[k]);
      const response = await fetch(`${process.env.EXA_BASE_URL ?? "https://api.exa.ai"}/search`, { method: "POST", headers: { "x-api-key": key, "content-type": "application/json" }, body: JSON.stringify(body), signal });
      const data: any = await response.json();
      if (!response.ok) return { content: [{ type: "text", text: `Exa HTTP ${response.status}: ${data?.message ?? data?.error ?? "request failed"}` }], isError: true, details: {} };
      const results = (data.results ?? []).map((r: any) => ({ title: r.title, url: r.url, author: r.author, publishedDate: r.publishedDate, highlights: r.highlights ?? [], summary: r.summary }));
      return { content: [{ type: "text", text: JSON.stringify({ query, backend: "exa", searchType: data.searchType, results, costDollars: data.costDollars }, null, 2) }], details: { requestId: data.requestId, resultCount: results.length } };
    },
  });
}
