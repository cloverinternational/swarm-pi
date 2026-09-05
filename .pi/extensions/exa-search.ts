import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

function exaKey() {
  const env = process.env.EXA_API_KEY?.trim(); if (env) return env;
  const configured = process.env.PI_SWARM_API_KEYS ?? join(process.env.HOME ?? process.cwd(), ".swarmos", "credentials.json");
  const local = join(process.cwd(), ".pi", "config", "api-keys.json");
  for (const path of [configured, local]) {
    try {
      const parsed = JSON.parse(readFileSync(path, "utf8"));
      const value = parsed?.providers?.Exa?.api_key ?? parsed?.exa?.apiKey;
      if (typeof value === "string" && value.trim()) return value.trim();
    } catch {}
  }
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
    name: "websearch", label: "Web Search (Exa)",
    description: "Search the web using Exa AI, matching Swarm's websearch tool. Requires EXA_API_KEY or ~/.swarmos/credentials.json providers.Exa.api_key. Returns concise highlights and source URLs.",
    parameters: schema,
    async execute(_id: string, params: any, signal: AbortSignal) {
      const key = exaKey();
      if (!key) return { content: [{ type: "text", text: "EXA_API_KEY is not configured" }], isError: true, details: {} };
      const query = String(params.query ?? "").trim();
      if (!query) return { content: [{ type: "text", text: "query is required" }], isError: true, details: {} };
      if (params.allowed_domains?.length && params.excluded_domains?.length) return { content: [{ type: "text", text: "allowed_domains and excluded_domains are mutually exclusive" }], isError: true, details: {} };
      // The tool caller may supply optional fields as empty strings. Exa rejects
      // empty date strings ("Invalid date format"), so normalize those away and
      // validate dates before sending the request.
      const optionalDate = (value: unknown, name: string) => {
        if (value == null || String(value).trim() === "") return undefined;
        const date = String(value).trim();
        if (!/^\d{4}-\d{2}-\d{2}(?:T.*)?$/.test(date) || Number.isNaN(Date.parse(date))) {
          throw new Error(`${name} must be a valid ISO 8601 date`);
        }
        return date;
      };
      let startPublishedDate: string | undefined;
      let endPublishedDate: string | undefined;
      try {
        startPublishedDate = optionalDate(params.start_published_date, "start_published_date");
        endPublishedDate = optionalDate(params.end_published_date, "end_published_date");
      } catch (error) {
        return { content: [{ type: "text", text: (error as Error).message }], isError: true, details: {} };
      }
      const body: any = {
        query, type: params.search_type ?? "auto", numResults: params.num_results ?? 10,
        category: params.category, includeDomains: params.allowed_domains, excludeDomains: params.excluded_domains,
        startPublishedDate, endPublishedDate,
        contents: { highlights: { numSentences: 5, highlightsPerUrl: 3, query } },
      };
      Object.keys(body).forEach(k => body[k] === undefined || body[k] === "" ? delete body[k] : undefined);
      const response = await fetch(`${process.env.EXA_BASE_URL ?? "https://api.exa.ai"}/search`, { method: "POST", headers: { "x-api-key": key, "content-type": "application/json" }, body: JSON.stringify(body), signal });
      const data: any = await response.json();
      if (!response.ok) return { content: [{ type: "text", text: `Exa HTTP ${response.status}: ${data?.message ?? data?.error ?? "request failed"}` }], isError: true, details: {} };
      const results = (data.results ?? []).map((r: any) => ({ title: r.title, url: r.url, author: r.author, publishedDate: r.publishedDate, highlights: r.highlights ?? [], summary: r.summary }));
      return { content: [{ type: "text", text: JSON.stringify({ query, backend: "exa", searchType: data.searchType, results, costDollars: data.costDollars }, null, 2) }], details: { requestId: data.requestId, resultCount: results.length } };
    },
  });
}
