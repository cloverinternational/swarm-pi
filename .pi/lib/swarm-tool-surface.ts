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

export interface CanonicalTool { name: string; description: string; parameters: unknown }

const FIXTURE = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..", "tools", "parity", "fixtures", "swarm-tools.json");
let cache: Map<string, CanonicalTool> | undefined;

export function loadSwarmToolSurface(path = FIXTURE): Map<string, CanonicalTool> {
  if (cache && path === FIXTURE) return cache;
  const raw = JSON.parse(readFileSync(path, "utf8")) as Array<{ function: CanonicalTool }>;
  const map = new Map(raw.map((entry) => [entry.function.name, entry.function]));
  if (path === FIXTURE) cache = map;
  return map;
}

export function swarmToolNames(): string[] { return [...loadSwarmToolSurface().keys()]; }

/** Overlay Swarm's canonical description/parameters onto a Pi tool definition by name. */
export function applySwarmSurface<T extends { name: string; description?: string; parameters?: unknown }>(tool: T): T {
  const canonical = loadSwarmToolSurface().get(tool.name);
  if (!canonical) return tool;
  return { ...tool, description: canonical.description, parameters: structuredClone(canonical.parameters) };
}

/** Return a `pi` facade whose registerTool applies the Swarm surface overlay. */
export function withSwarmToolSurface<P extends { registerTool?: (tool: any) => void }>(pi: P): P {
  if (!pi.registerTool) return pi;
  const original = pi.registerTool.bind(pi);
  return new Proxy(pi, {
    get(target, prop, receiver) {
      if (prop === "registerTool") return (tool: any) => original(applySwarmSurface(tool));
      const value = Reflect.get(target, prop, receiver);
      return typeof value === "function" ? value.bind(target) : value;
    },
  });
}
