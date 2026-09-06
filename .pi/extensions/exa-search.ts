import { readFileSync } from "node:fs";
import { join } from "node:path";
import { withSwarmToolSurface } from "../lib/swarm-tool-surface.ts";
import { withDefaultToolRenderer } from "../lib/swarm-tool-renderer.ts";
import { newErrorID } from "../lib/swarm-bash.ts";

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
    max_results: { type: "integer", minimum: 1, maximum: 20 },
    type: { type: "string", enum: ["auto", "fast", "instant", "deep-lite", "deep", "deep-reasoning"] },
    category: { type: "string" },
    allowed_domains: { type: "array", items: { type: "string" }, maxItems: 10 },
    blocked_domains: { type: "array", items: { type: "string" }, maxItems: 10 },
    excluded_domains: { type: "array", items: { type: "string" }, maxItems: 10 },
    start_published_date: { type: "string" }, end_published_date: { type: "string" },
    user_location: { type: "string", minLength: 2, maxLength: 2 },
    moderation: { type: "boolean" },
    additional_queries: { type: "array", items: { type: "string" }, maxItems: 10 },
    system_prompt: { type: "string", maxLength: 4000 },
  },
} as const;

export default function exaSearchExtension(rawPi: any) {
  const pi = withSwarmToolSurface(rawPi);
  pi.registerTool(withDefaultToolRenderer({
    name: "websearch", label: "Web Search (Exa)",
    description: "Search the web using Exa AI, matching Swarm's websearch tool. Requires EXA_API_KEY or ~/.swarmos/credentials.json providers.Exa.api_key. Returns concise highlights and source URLs.",
    parameters: schema,
    async execute(_id: string, params: any, signal: AbortSignal) {
      // websearch/tool.go Validate: plain fmt.Errorf, so registry_impl.go's
      // "validation failed for websearch: …" carries a single error_id.
      const invalid = (message: string): never => { throw new Error(`Error executing websearch: validation failed for websearch: ${message} (error_id=${newErrorID()})`); };
      const failure = (message: string): never => { throw new Error(`Error executing websearch: ${message} (error_id=${newErrorID()})`); };
      if (typeof params?.query !== "string") invalid("query parameter is required and must be a string");
      if (params.query === "") invalid("query cannot be empty");
      if (Buffer.byteLength(params.query) > 1000) invalid("query exceeds maximum length of 1000 characters");
      if (typeof params.max_results === "number" && (params.max_results < 1 || params.max_results > 20)) invalid("max_results must be between 1 and 20");
      const hasDomains = (key: string) => Array.isArray(params?.[key]) && params[key].some((v: any) => typeof v === "string" && v.trim());
      if (hasDomains("allowed_domains") && (hasDomains("blocked_domains") || hasDomains("excluded_domains"))) invalid("allowed_domains and blocked_domains cannot both be specified in the same request");
      if (hasDomains("blocked_domains") && hasDomains("excluded_domains")) invalid("blocked_domains and excluded_domains cannot both be specified in the same request");
      if (params.category && ["company", "people"].includes(params.category) && ((hasDomains("blocked_domains") || hasDomains("excluded_domains")) || params.start_published_date || params.end_published_date)) invalid("category company/people does not support excluded domains or publication date filters");
      if (typeof params.type === "string" && params.type !== "" && !["auto", "fast", "instant", "deep-lite", "deep", "deep-reasoning"].includes(params.type)) invalid(`invalid search type ${JSON.stringify(params.type)}; must be one of: auto, neural, fast, instant, deep-lite, deep, deep-reasoning, deep-max`);
      // websearch.New: EXA_API_KEY selects the Exa backend, otherwise the
      // Anthropic OAuth backend reads ~/.swarm/config/oauth/anthropic.json
      // (provider/anthropic/oauth_config.go: a missing file is an empty config).
      const key = process.env.EXA_API_KEY?.trim() || undefined;
      if (!key) {
        const oauthPath = join(process.env.SWARM_HOME || join(process.env.HOME ?? process.cwd(), ".swarm"), "config", "oauth", "anthropic.json");
        let token: any;
        try { token = JSON.parse(readFileSync(oauthPath, "utf8"))?.token; }
        catch (error: any) {
          if (error?.code !== "ENOENT") failure(`failed to get OAuth token: failed to ${error instanceof SyntaxError ? "parse" : "read"} OAuth config: ${error?.message ?? error} (run 'claude login')`);
        }
        if (!token) failure("failed to get OAuth token: no OAuth token stored (run 'claude login')");
        if (!token.access_token) failure("OAuth token is empty — run 'claude login' to authenticate");
        // The Anthropic server-side search itself is not ported; Pi falls back
        // to Exa when a key is configured outside the environment.
        const fallback = exaKey();
        if (!fallback) failure("Anthropic web search backend is not available in Pi; set EXA_API_KEY");
        return runExa(fallback, params, signal);
      }
      return runExa(key, params, signal);
    },
  }));
}

async function runExa(key: string, params: any, signal: AbortSignal) {
      const query = String(params.query ?? "").trim();
      // Prefer the allow-list if both are supplied. This keeps permissive model
      // tool calls from becoming provider validation failures.
      const allowedDomains = Array.isArray(params.allowed_domains) ? params.allowed_domains.filter((v: any) => typeof v === "string" && v.trim()) : [];
      const blockedDomains = Array.isArray(params.blocked_domains) ? params.blocked_domains.filter((v: any) => typeof v === "string" && v.trim()) : [];
      const excludedDomains = Array.isArray(params.excluded_domains) ? params.excluded_domains.filter((v: any) => typeof v === "string" && v.trim()) : [];
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
        query, type: params.type ?? "auto", numResults: params.max_results ?? 10,
        category: params.category,
        includeDomains: allowedDomains.length ? allowedDomains : undefined,
        excludeDomains: blockedDomains.length ? blockedDomains : (excludedDomains.length ? excludedDomains : undefined),
        userLocation: params.user_location,
        moderation: params.moderation,
        additionalQueries: Array.isArray(params.additional_queries) && params.additional_queries.length ? params.additional_queries : undefined,
        systemPrompt: params.system_prompt,
        startPublishedDate, endPublishedDate,
        contents: { highlights: { query, maxCharacters: 4000 } },
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
        headers: { authorization: `Bearer ${key}`, "content-type": "application/json" },
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
}
