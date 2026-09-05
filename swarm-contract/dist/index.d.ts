export type PolicyClass = "KEEP" | "OPT-IN" | "DEFER-DISCOVER" | "NEVER-DEFAULT";
export type CapabilityKind = "tool" | "skill" | "hook" | "mcp";
/** Immutable, redacted runtime posture. Arrays are copied and frozen. */
export interface RuntimeProfileInput {
    tools?: readonly string[];
    skills?: readonly string[];
    hooks?: readonly string[];
    mcp?: readonly string[];
    capabilities?: readonly string[];
    context?: {
        workspace?: string;
        files?: readonly string[];
    };
    provider?: {
        id: string;
        model: string;
    };
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
    readonly context: Readonly<{
        workspace?: string;
        files: readonly string[];
    }>;
    readonly provider?: Readonly<{
        id: string;
        model: string;
    }>;
    readonly promptHash?: string;
    readonly packages: readonly PackageProvenance[];
    readonly digest: string;
}
/** SHA-256 in a stable, JSON-canonical-enough representation (sorted fields/lists). */
export declare function createRuntimeProfile(input?: RuntimeProfileInput): ImmutableRuntimeProfile;
export declare function hasCapability(profile: ImmutableRuntimeProfile, kind: CapabilityKind, id: string): boolean;
export declare function assertAllowed(profile: ImmutableRuntimeProfile, kind: CapabilityKind, id: string): void;
/** Hash prompt material without retaining or returning the prompt itself. */
export declare function hashPrompt(prompt: string): string;
export declare function provenance(name: string, version: string, source: PackageProvenance["source"], integrity?: string): PackageProvenance;
