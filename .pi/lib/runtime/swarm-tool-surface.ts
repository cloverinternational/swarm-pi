/**
 * Canonical model-facing tool surface, captured from `swarm -p` on the wire
 * (tools/parity/fixtures/swarm-tools.json). For any Pi tool that shares a name
 * with a Swarm tool, the description and JSON Schema the model sees must be
 * byte-identical to Swarm's. Runtime input validation stays inside each tool
 * implementation; only the advertised contract is overlaid here.
 *
 * Wrap the extension API once at registration time:
 *   registerFoo(withSwarmToolSurface(pi))
 */
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { withDefaultToolRenderer } from "../../../packages/runtime/core/src/tool-renderer.ts";

export interface CanonicalTool { name: string; description: string; parameters: unknown }

/** Repository root: this file lives at .pi/lib/runtime/. */
const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..", "..");
const FIXTURES = resolve(REPO_ROOT, "tools", "parity", "fixtures");
const FIXTURE = resolve(FIXTURES, "swarm-tools.json");
/** Tools Swarm registers only in some environments (x_search / xai_web_search behind xAI credentials); same wire capture, gated separately. */
const CONDITIONAL_FIXTURE = resolve(FIXTURES, "swarm-conditional-tools.json");
/** Interactive-TUI-only definitions (Bash/ReadBackgroundCommand, ask_user_question, enter/exit_plan_mode), captured from the `swarm` TUI on the wire. */
const INTERACTIVE_FIXTURE = resolve(FIXTURES, "swarm-interactive-tools.json");
let cache: Map<string, CanonicalTool> | undefined;
let conditionalCache: Map<string, CanonicalTool> | undefined;

const readFixture = (path: string) => new Map((JSON.parse(readFileSync(path, "utf8")) as Array<{ function: CanonicalTool }>).map((entry) => [entry.function.name, entry.function]));

/** The always-on Swarm tool surface (29 tools, including the unified vault adapter). */
export function loadSwarmToolSurface(path = FIXTURE): Map<string, CanonicalTool> {
  if (cache && path === FIXTURE) return cache;
  const map = readFixture(path);
  if (path === FIXTURE) cache = map;
  return map;
}
/** Always-on plus environment-gated definitions, for description/schema overlay by name. */
export function loadSwarmCanonicalTools(): Map<string, CanonicalTool> {
  if (!conditionalCache) conditionalCache = new Map([...loadSwarmToolSurface(), ...readFixture(CONDITIONAL_FIXTURE), ...readFixture(INTERACTIVE_FIXTURE)]);
  return conditionalCache;
}

export function swarmToolNames(): string[] { return [...loadSwarmToolSurface().keys()]; }

/**
 * Swarm never validates tool arguments against the advertised JSON Schema:
 * each Go tool decodes/validates its own input and reports its own prose
 * (registry_impl.go Validate → "validation failed for X: …", or a plain
 * result such as bash running an empty command). Pi validates against
 * `tool.parameters` before any hook runs (pi-agent-core prepareToolCall) and
 * reports `Validation failed for tool "X": …`. To keep the model-visible
 * error paths identical, canonical tools register with this permissive
 * validator schema and the canonical schema is overlaid on the wire by
 * `overlaySwarmToolSchemas` (before_provider_request).
 */
export const PERMISSIVE_PARAMETERS = { type: "object" } as const;

/** Overlay Swarm's canonical description onto a Pi tool definition by name and relax its validator. */
export function applySwarmSurface<T extends { name: string; description?: string; parameters?: unknown }>(tool: T): T {
  const canonical = loadSwarmCanonicalTools().get(tool.name);
  const surfaced = canonical ? { ...tool, description: canonical.description, parameters: { ...PERMISSIVE_PARAMETERS } } : tool;
  return withDefaultToolRenderer(surfaced) as T;
}

/**
 * Replace `tools[].function.parameters` (and description) with the canonical
 * Swarm schema for every tool Swarm knows. Returns undefined when nothing
 * changed. Anthropic-shaped payloads (`input_schema`) are handled too.
 */
export function overlaySwarmToolSchemas<T extends { tools?: unknown }>(payload: T): T | undefined {
  const tools = payload?.tools;
  if (!Array.isArray(tools) || tools.length === 0) return undefined;
  const surface = loadSwarmCanonicalTools();
  let changed = false;
  const next = tools.map((tool: any) => {
    const fn = tool?.function;
    if (fn && typeof fn === "object") {
      const canonical = surface.get(fn.name);
      if (!canonical) return tool;
      if (fn.description === canonical.description && JSON.stringify(fn.parameters) === JSON.stringify(canonical.parameters)) return tool;
      changed = true;
      return { ...tool, function: { ...fn, description: canonical.description, parameters: structuredClone(canonical.parameters) } };
    }
    if (typeof tool?.name === "string" && "input_schema" in tool) {
      const canonical = surface.get(tool.name);
      if (!canonical) return tool;
      if (tool.description === canonical.description && JSON.stringify(tool.input_schema) === JSON.stringify(canonical.parameters)) return tool;
      changed = true;
      return { ...tool, description: canonical.description, input_schema: structuredClone(canonical.parameters) };
    }
    return tool;
  });
  return changed ? { ...payload, tools: next } : undefined;
}

/** Unwraps a `withSwarmToolSurface` proxy so registries keyed by the Pi instance stay stable. */
export const RAW_PI = Symbol.for("pi-swarm-raw-pi");
export const rawPi = <P extends object>(pi: P): P => ((pi as any)[RAW_PI] as P | undefined) ?? pi;
/** Return a `pi` facade whose registerTool applies the Swarm surface overlay. */
export function withSwarmToolSurface<P extends { registerTool?: (tool: any) => void }>(pi: P): P {
  if (!pi.registerTool) return pi;
  const original = pi.registerTool.bind(pi);
  return new Proxy(pi, {
    get(target, prop, receiver) {
      if (prop === RAW_PI) return rawPi(target);
      if (prop === "registerTool") return (tool: any) => {
        const surfaced = applySwarmSurface(tool);
        const wrap = (globalThis as any)[Symbol.for("pi-swarm-wrap-tool-for-hook-rows")];
        original(typeof wrap === "function" ? wrap(surfaced) : surfaced);
      };
      const value = Reflect.get(target, prop, receiver);
      return typeof value === "function" ? value.bind(target) : value;
    },
  });
}
