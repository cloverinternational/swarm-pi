/** Verbatim body of `forgeSwarmSystemPrompt` (system_prompt.go:408). */
export declare const forgeSwarmSystemPrompt: string;
/** Verbatim body of `swarmForgeDelegationAddendum` (system_prompt.go:561). */
export declare const swarmForgeDelegationAddendum: string;
/** `swarmForgeSystemPrompt = forgeSwarmSystemPrompt + swarmForgeDelegationAddendum` (system_prompt.go:628). */
export declare const swarmForgeSystemPrompt: string;
/** Provenance reference recorded by prompt-assembling extensions. */
export declare const UPSTREAM_SOURCE = "upstream/swarm-sdk/swarm-tui/internal/chat/settings/system_prompt.go";
export interface SwarmPromptPreset {
    /** Display name, matching the TUI's builtin prompt entry. */
    name: string;
    content: string;
    /** Whether the TUI prepends the runtime <system_information> block. */
    workspaceContext: boolean;
    /** `name` of the originating Go constant. */
    constant: string;
}
/**
 * The two workspace-context builtins registered by the TUI
 * (system_prompt.go:72-84), in upstream declaration order.
 */
export declare const swarmPromptPresets: readonly SwarmPromptPreset[];
