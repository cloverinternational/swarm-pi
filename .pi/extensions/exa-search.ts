import { readFileSync } from "node:fs";
import { join } from "node:path";
import { withSwarmToolSurface } from "../lib/swarm-tool-surface.ts";

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

export default function exaSearchExtension(rawPi: any) {
  const pi = withSwarmToolSurface(rawPi);
  pi.registerTool({
    name: "websearch", label: "Web Search (Exa)",
    description: "Search the web using Exa AI, matching Swarm's websearch tool. Requires EXA_API_KEY or ~/.swarmos/credentials.json providers.Exa.api_key. Returns concise highlights and source URLs.",
    parameters: schema,
    async execute(_id: string, params: any, signal: AbortSignal) {
      const key = exaKey();
      if (!key) return { content: [{ type: "text", text: "EXA_API_KEY is not configured" }], isError: true, details: {} };
      const query = String(params.query ?? "").trim();
      if (!query) return { content: [{ type: "text", text: "query is required" }], isError: true, details: {} };
      // Prefer the allow-list if both are supplied. This keeps permissive model
      // tool calls from becoming provider validation failures.
      const allowedDomains = Array.isArray(params.allowed_domains) ? params.allowed_domains.filter((v: any) => typeof v === "string" && v.trim()) : [];
      const excludedDomains = allowedDomains.length ? [] : (Array.isArray(params.excluded_domains) ? params.excluded_domains.filter((v: any) => typeof v === "string" && v.trim()) : []);
      // The tool caller may supply optional fields as empty strings. Exa rejects
      // empty date strings ("Invalid date format"), so normalize those away and
      // validate dates before sending the request.
      const optionalDate = (value: unknown) => {
        const date = String(value ?? "").trim();
        // Invalid optional filters are ignored: the query remains useful and
        // the agent never gets stuck on Exa's strict date parser.
        return /^\d{4}-\d{2}-\d{2}(?:T.*)?$/.test(date) && !Number.isNaN(Date.parse(date)) ? date : undefined;
      };
      const startPublishedDate = optionalDate(params.start_published_date);
      const endPublishedDate = optionalDate(params.end_published_date);
      const body: any = {
        query, type: params.search_type ?? "auto", numResults: params.num_results ?? 10,
        category: params.category,
        includeDomains: allowedDomains.length ? allowedDomains : undefined,
        excludeDomains: excludedDomains.length ? excludedDomains : undefined,
        startPublishedDate, endPublishedDate,
        contents: { highlights: { numSentences: 5, highlightsPerUrl: 3, query } },
      };
      const clean = (value: any): any => {
        if (Array.isArray(value)) return value.length ? value : undefined;
        if (value && typeof value === "object") {
          for (const k of Object.keys(value)) value[k] = clean(value[k]);
          return value;
        }
        return value === "" || value == null ? undefined : value;
      };
      clean(body);
      const endpoint = `${process.env.EXA_BASE_URL ?? "https://api.exa.ai"}/search`;
      const request = (payload: any) => fetch(endpoint, {
        method: "POST",
        headers: { "x-api-key": key, "content-type": "application/json" },
        body: JSON.stringify(payload), signal,
      });
      let response: Response;
      let raw = "";
      try {
        response = await request(body);
        raw = await response.text();
      } catch (error) {
        return { content: [{ type: "text", text: `Exa request failed: ${(error as Error).message}` }], isError: true, details: {} };
      }
      let data: any;
      try { data = raw ? JSON.parse(raw) : {}; } catch { data = { error: raw }; }
      // Defense in depth: older callers or a stale loaded extension may still
      // pass date fields that Exa rejects. Retry once with dates removed rather
      // than exposing a recoverable provider validation error to the agent.
      if (!response.ok && response.status === 400 && /date|ISO 8601|published/i.test(raw)) {
        const fallback = { ...body };
        delete fallback.startPublishedDate;
        delete fallback.endPublishedDate;
        try {
          response = await request(fallback);
          raw = await response.text();
          try { data = raw ? JSON.parse(raw) : {}; } catch { data = { error: raw }; }
        } catch (error) {
          return { content: [{ type: "text", text: `Exa request failed: ${(error as Error).message}` }], isError: true, details: {} };
        }
      }
      if (!response.ok) return { content: [{ type: "text", text: `Exa HTTP ${response.status}: ${data?.message ?? data?.error ?? "request failed"}` }], isError: true, details: {} };
      const results = (data.results ?? []).map((r: any) => ({ title: r.title, url: r.url, author: r.author, publishedDate: r.publishedDate, highlights: r.highlights ?? [], summary: r.summary }));
      return { content: [{ type: "text", text: JSON.stringify({ query, backend: "exa", searchType: data.searchType, results, costDollars: data.costDollars }, null, 2) }], details: { requestId: data.requestId, resultCount: results.length } };
    },
  });
}
