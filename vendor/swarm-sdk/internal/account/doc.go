/*
Package account provides comprehensive abstractions for multi-account OAuth support in the Swarm TUI.

It introduces a clean architecture for managing multiple OAuth accounts across different providers,
with support for automatic fallback, token refresh, and profile-based account organization.

Core Concepts:

1. AccountIdentity - A unique account identifier (provider + account_id)
  - Immutable representation of an account
  - Used as the key for lookups and caching
  - Examples: "anthropic::org-123", "openai::user-456"

2. AccountProfile - An account bound to a profile context
  - Associates AccountIdentity with profile name and metadata
  - Tracks last used model and custom attributes
  - Example: "Production Claude" profile using "anthropic::org-123"

3. CredentialBinding - OAuth token management for an account
  - Holds the actual OAuth token
  - Handles token expiry checks and refresh
  - Provides access to token data for API calls

4. AccountRegistry - Central account storage and lookup
  - Manages all registered accounts
  - Handles token persistence
  - Provides default account selection per provider
  - Error codes: account_not_found, token_refresh_failed, etc.

5. ProfileStore - Profile-to-account mappings
  - Creates and manages named profiles
  - Associates profiles with accounts and models
  - Handles default profile selection
  - Separate from agent_profiles.json (which handles multi-role routing)

6. AccountManager - High-level orchestrator
  - Coordinates registry and profile store
  - Manages active profile switching
  - Triggers hooks on account/token changes
  - Handles OAuth registration flow

7. AccountFallbackChain - Automatic fallback support
  - Maintains a primary + fallback account list
  - Tries fallbacks when primary fails
  - Tracks exhaustion state
  - Integrates with provider error handling

Architecture Layers:

	┌─────────────────────────────────┐
	│   Application Layer (TUI)       │
	│  - /auth command                │
	│  - Settings UI                  │
	│  - Provider switching           │
	└──────────────┬──────────────────┘
	               │
	┌──────────────▼──────────────────┐
	│  AccountManager (Orchestrator)  │
	│  - Active profile tracking      │
	│  - Hook coordination            │
	│  - OAuth registration           │
	└──────┬──────────────────┬───────┘
	       │                  │
	┌──────▼────────┐   ┌─────▼──────────┐
	│ AccountRegistry│   │ ProfileStore   │
	│ - Persistence │   │ - Profile CRUD │
	│ - Token refresh│   │ - Default sel. │
	└──────┬────────┘   └─────┬──────────┘
	       │                  │
	┌──────▼────────────────────▼──┐
	│  Identity + Profile + Token   │
	│  (Stored in ~/.swarm/)        │
	└───────────────────────────────┘

Usage Examples:

	Creating and registering an account:

	    token := &account.OAuthToken{...}
	    profile, err := manager.RegisterOAuthAccount(ctx, "openai", token)
	    if err != nil {
	        // Handle error
	    }
	    // Profile is now active and can be used for API calls

	Switching between accounts:

	    err := manager.SwitchAccount(ctx, "openai", "user-456")
	    if err != nil {
	        // Handle error
	    }
	    // Now "user-456" is active

	Setting up auto-fallback:

	    chain := account.NewStandardAccountFallbackChain()
	    chain.AddPrimary(primary)      // "openai::user-123"
	    chain.AddFallback(fallback)    // "anthropic::org-456"
	    // Configure in SDK to automatically try fallback on 429 error

	Handling token refresh:

	    binding, err := manager.GetBinding("openai", "user-456")
	    if err != nil {
	        // Handle error
	    }
	    if binding.IsExpired() {
	        token, err := binding.RefreshToken(ctx)
	        if err != nil {
	            // Handle refresh failure
	        }
	    }

Error Handling:

Registry errors use structured error codes:
  - account_not_found: Account doesn't exist
  - token_not_found: Token for account missing
  - token_refresh_failed: Token refresh failed
  - max_accounts_exceeded: Too many accounts for provider
  - corrupted_data: Storage format is invalid

Manager errors:
  - no_active_profile: No profile selected
  - profile_not_found: Profile doesn't exist
  - fallback_exhausted: All fallback accounts failed
  - refresh_failed: Token refresh failed

Storage:

Accounts are stored in ~/.swarm/accounts/:

	accounts.json - Registry (account IDs, defaults, fallback chain)
	openai/account-{id}.json - OpenAI account credentials
	anthropic/account-{id}.json - Anthropic account credentials
	etc.

Backward Compatibility:

The system maintains compatibility with existing storage:
  - Old ~/.swarm/openai_oauth.json is migrated to new format
  - Old ~/.swarm/anthropic/oauth.json is migrated
  - Migration is automatic on first load

Extension Points:

Hook system allows customization:
  - OnAccountSwitch: Invoked when active account changes
  - OnTokenRefresh: Invoked when token is refreshed
  - OnAccountRegistered: Invoked when new account is registered

Custom handlers can implement:
  - Telemetry tracking (which accounts are used)
  - Quota monitoring (fail-fast when quota approached)
  - Audit logging (compliance tracking)
  - Budget alerts (cost tracking)

Design Principles:

1. Interface-based: All major components are interfaces
2. Separation of Concerns: Identity, credentials, profiles are separate
3. Account-First: Credentials belong to accounts, not just providers
4. Type-Safe: Compile-time safety with Go interfaces
5. Backward Compatible: Supports migration from single-account format
6. Extensible: Hook system allows future features
7. Context-Aware: Respects context cancellation and timeouts
*/
package account
