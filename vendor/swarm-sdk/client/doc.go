// Package client provides an ergonomic top-level interface to the Swarm SDK.
//
// It auto-loads configuration from ~/.swarm/ (the same config the TUI uses),
// resolves credentials via OAuth tokens, env vars, and credentials.json,
// and wires up the agent, provider, conversation storage, and tool registry
// so callers don't have to.
//
// Minimal usage:
//
//	c, err := client.New()          // reads ~/.swarm/config/config.yaml automatically
//	reply, err := c.Chat("Hello!")
//
// With overrides:
//
//	c, err := client.New(
//	    client.WithProvider("anthropic", "claude-sonnet-4-5"),
//	    client.WithWorkspace("/path/to/project"),
//	    client.WithSystemPrompt("You are a Go expert."),
//	)
//
// Downstream applications can compose capabilities explicitly:
//
//	c, err := client.New(
//	    client.WithAllTools(),
//	    client.WithNestedAgents(),
//	    client.WithSkills("/path/to/project/.claude/skills"),
//	    client.WithTaskEnforcement(),
//	)
//
// The client package re-exports (via type aliases in aliases.go) every
// cross-package type that appears in its exported signatures — Tool, Hook,
// Registry, IntermediateUpdate, Conversation, Message, OperatingMode, and so
// on — so a consumer can use the full facade importing only this package.
//
// # Configuration discovery
//
// client.New() resolves its configuration from caller options, environment
// variables, and on-disk files, in this precedence order (highest wins):
//
//  1. Explicit caller options (WithProvider, WithAPIKey, …)
//  2. Environment variables
//  3. On-disk config / credential files under ~/.swarm/
//  4. Built-in defaults
//
// Caller options always win: the env-var and config-file steps only fill a
// field that the caller left empty. Defaults apply last (provider "anthropic",
// the provider's default model, and ~/.swarm/conversations for storage).
//
// Auto-loaded paths (consulted by client.New() with no extra options):
//
//	~/.swarm/config/config.yaml
//	    Provider + model selection (current_provider / current_model).
//	    Loaded unless WithoutAutoConfig() is set. Caller options and the
//	    SWARM_PROVIDER / SWARM_MODEL env vars take precedence over it.
//
//	~/.swarm/config/credentials.json
//	    Per-provider API key + base URL. Consulted during credential
//	    resolution (see resolveCredentials) after env vars.
//
//	~/.swarm/config/providers.json
//	    Custom / OpenAI-compatible provider definitions (api_type, base_url)
//	    and keys stored by the TUI / settings UI. Consulted after
//	    credentials.json. Also used to validate custom provider names.
//
//	~/.swarm/config/oauth/anthropic.json
//	    Anthropic OAuth access token. Last resort in the Anthropic credential
//	    chain. (xAI/SuperGrok use ~/.swarm/config/oauth/xai.json analogously.)
//
//	<workspace>/**/INDEX.md
//	    Codebase structure maps walked from the workspace directory and
//	    injected into the system prompt. Enabled by default; disable with
//	    WithoutIndexMd().
//
// Opt-in paths (consulted only when the corresponding option is set):
//
//	~/.swarm/config/tui_accounts.json
//	    Multi-account credentials for rotation. Loaded only with
//	    WithCredentialRotation().
//
//	~/.swarm/config/hooks.json, ~/.claude/settings.json, <workspace>/.claude/settings.json
//	    Hook definitions (SwarmOS-native and Claude Code formats). Loaded only
//	    with WithHooksFromConfig() (search order: workspace .claude/settings.json,
//	    then ~/.claude/settings.json, then ~/.swarm/config/hooks.json). A single
//	    explicit file may be given with WithHooksFromPath().
//
//	Regular skills are opt-in with WithSkills(). The SDK discovers canonical
//	user-level skills, workspace .claude/skills, and any paths passed to
//	WithSkills. This registers the model-facing Skill tool only; skill creation,
//	budget enforcement, and curator lifecycle remain opt-in via
//	WithAutogenSkills().
//
//	~/.swarm/config/mcp_servers.json
//	    MCP server definitions. Loaded only with WithMCP("") (which also adds
//	    project-level discovery of mcp.json / .mcp.json / .swarm/mcp.json under
//	    the workspace).
//
// Environment variables:
//
//	SWARM_PROVIDER      Provider name (e.g. "anthropic", "openai", "wafer").
//	                    Overrides config.json; overridden by WithProvider.
//	SWARM_MODEL         Model ID. Overrides config.json; overridden by the model
//	                    argument to WithProvider.
//	{PROVIDER}_API_KEY  Per-provider API key. Recognised names include
//	                    ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY,
//	                    GROQ_API_KEY, OPENROUTER_API_KEY, XAI_API_KEY, … For a
//	                    provider not in the built-in map, a convention key is
//	                    tried: the uppercased provider name (dots → underscores)
//	                    plus _API_KEY (e.g. WAFER_API_KEY). Highest-precedence
//	                    credential source, ahead of credentials.json /
//	                    providers.json. An explicit WithAPIKey() still wins.
package client
