import { createHash } from "node:crypto";
const clean = (items) => Object.freeze([...new Set(items ?? [])].filter((x) => x.length > 0).sort());
const cleanPackages = (items) => Object.freeze((items ?? []).map((p) => Object.freeze({ ...p })).sort((a, b) => `${a.name}@${a.version}`.localeCompare(`${b.name}@${b.version}`)));
/** SHA-256 in a stable, JSON-canonical-enough representation (sorted fields/lists). */
export function createRuntimeProfile(input = {}) {
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
export function hasCapability(profile, kind, id) {
    const list = kind === "tool" ? profile.tools : kind === "skill" ? profile.skills : kind === "hook" ? profile.hooks : profile.mcp;
    return list.includes(id) || profile.capabilities.includes(id);
}
export function assertAllowed(profile, kind, id) {
    if (!hasCapability(profile, kind, id))
        throw new Error(`profile denied ${kind}:${id}`);
}
/** Hash prompt material without retaining or returning the prompt itself. */
export function hashPrompt(prompt) {
    return `sha256:${createHash("sha256").update(prompt, "utf8").digest("hex")}`;
}
export function provenance(name, version, source, integrity) {
    return integrity ? { name, version, source, integrity } : { name, version, source };
}
