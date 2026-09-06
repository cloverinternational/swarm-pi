import { createHash } from "node:crypto";

export type PolicyClass = "KEEP" | "OPT-IN" | "DEFER-DISCOVER" | "NEVER-DEFAULT";
export type CapabilityKind = "tool" | "skill" | "hook" | "mcp";

/** Immutable, redacted runtime posture. Arrays are copied and frozen. */
export interface RuntimeProfileInput {
  tools?: readonly string[];
  skills?: readonly string[];
  hooks?: readonly string[];
  mcp?: readonly string[];
  capabilities?: readonly string[];
  context?: { workspace?: string; files?: readonly string[] };
  provider?: { id: string; model: string };
  promptHash?: string;
  packages?: readonly PackageProvenance[];
}

export interface PackageProvenance {
  name: string;
  version: string;
  source: "npm" | "git" | "path" | "builtin" | "unknown";
  integrity?: string;
}

export interface ImmutableRuntimeProfile {
  readonly tools: readonly string[];
  readonly skills: readonly string[];
  readonly hooks: readonly string[];
  readonly mcp: readonly string[];
  readonly capabilities: readonly string[];
  readonly context: Readonly<{ workspace?: string; files: readonly string[] }>;
  readonly provider?: Readonly<{ id: string; model: string }>;
  readonly promptHash?: string;
  readonly packages: readonly PackageProvenance[];
  readonly digest: string;
}

const clean = (items: readonly string[] | undefined) =>
  Object.freeze([...new Set(items ?? [])].filter((x) => x.length > 0).sort());
const cleanPackages = (items: readonly PackageProvenance[] | undefined) =>
  Object.freeze((items ?? []).map((p) => Object.freeze({ ...p })).sort((a, b) => `${a.name}@${a.version}`.localeCompare(`${b.name}@${b.version}`)));

/** SHA-256 in a stable, JSON-canonical-enough representation (sorted fields/lists). */
export function createRuntimeProfile(input: RuntimeProfileInput = {}): ImmutableRuntimeProfile {
  const profile = {
    tools: clean(input.tools), skills: clean(input.skills), hooks: clean(input.hooks), mcp: clean(input.mcp),
    capabilities: clean(input.capabilities),
    context: Object.freeze({ workspace: input.context?.workspace, files: clean(input.context?.files) }),
    provider: input.provider ? Object.freeze({ id: input.provider.id, model: input.provider.model }) : undefined,
    promptHash: input.promptHash,
    packages: cleanPackages(input.packages),
  };
  const digest = createHash("sha256").update(JSON.stringify(profile)).digest("hex");
  return Object.freeze({ ...profile, digest });
}

export function hasCapability(profile: ImmutableRuntimeProfile, kind: CapabilityKind, id: string): boolean {
  const list = kind === "tool" ? profile.tools : kind === "skill" ? profile.skills : kind === "hook" ? profile.hooks : profile.mcp;
  return list.includes(id) || profile.capabilities.includes(id);
}

export function assertAllowed(profile: ImmutableRuntimeProfile, kind: CapabilityKind, id: string): void {
  if (!hasCapability(profile, kind, id)) throw new Error(`profile denied ${kind}:${id}`);
}

/** Hash prompt material without retaining or returning the prompt itself. */
export function hashPrompt(prompt: string): string {
  return `sha256:${createHash("sha256").update(prompt, "utf8").digest("hex")}`;
}

export function provenance(name: string, version: string, source: PackageProvenance["source"], integrity?: string): PackageProvenance {
  return integrity ? { name, version, source, integrity } : { name, version, source };
}
