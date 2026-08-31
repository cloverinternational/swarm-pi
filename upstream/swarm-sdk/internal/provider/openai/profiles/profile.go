// Package profiles provides provider profile management for OpenAI-compatible APIs.
package profiles

// Profile defines a provider configuration template.
type Profile struct {
	Name           string          `toml:"name"`
	DisplayName    string          `toml:"display_name"`
	BaseURL        string          `toml:"base_url"`
	CompatibleWith string          `toml:"compatible_with"`
	QuirkAdapter   string          `toml:"quirk_adapter"`
	Auth           AuthConfig      `toml:"auth"`
	Features       FeatureFlags    `toml:"features"`
	Models         ModelConfig     `toml:"models"`
	Transforms     TransformConfig `toml:"transforms"`
	Notes          NotesConfig     `toml:"notes"`
}

// AuthConfig defines authentication configuration.
type AuthConfig struct {
	Type         string        `toml:"type"`          // "bearer", "api-key", "custom"
	HeaderName   string        `toml:"header_name"`   // e.g., "Authorization", "api-key"
	HeaderFormat string        `toml:"header_format"` // e.g., "Bearer {api_key}"
	ExtraHeaders []ExtraHeader `toml:"extra_headers"` // Additional headers
}

// ExtraHeader defines an additional HTTP header.
type ExtraHeader struct {
	Name     string
	Value    string
	Required bool
}

// FeatureFlags defines which features a provider supports.
type FeatureFlags struct {
	ChatCompletion    bool   `toml:"chat_completion"`
	Streaming         bool   `toml:"streaming"`
	FunctionCalling   bool   `toml:"function_calling"`
	Vision            bool   `toml:"vision"`
	Audio             bool   `toml:"audio"`
	ReasoningEffort   bool   `toml:"reasoning_effort"`
	Verbosity         bool   `toml:"verbosity"`
	CustomTools       bool   `toml:"custom_tools"`
	MaxTokensParam    string `toml:"max_tokens_param"`    // "max_tokens", "max_completion_tokens", or "auto"
	SystemPromptStyle string `toml:"system_prompt_style"` // "message" or "parameter"
}

// ModelConfig defines model naming and discovery.
type ModelConfig struct {
	Prefix            string
	Format            string // Template: {model}, {provider}/{model}, etc.
	SupportsDiscovery bool
}

// TransformConfig defines request/response transformations.
type TransformConfig struct {
	ParamMapping []ParamMapping
	RemoveParams []string
	AddParams    []AddParam
}

// ParamMapping maps canonical parameter names to provider-specific names.
type ParamMapping struct {
	From string
	To   string
}

// AddParam adds a provider-specific parameter.
type AddParam struct {
	Name  string
	Value any
}

// NotesConfig provides additional metadata about the provider.
type NotesConfig struct {
	Description     string
	RequiresUpgrade string
	APIEndpoint     string
}

// Merge combines this profile with user overrides.
func (p Profile) Merge(overrides UserConfig) Profile {
	merged := p

	if overrides.BaseURL != "" {
		merged.BaseURL = overrides.BaseURL
	}

	if overrides.Auth != nil {
		if overrides.Auth.HeaderName != "" {
			merged.Auth.HeaderName = overrides.Auth.HeaderName
		}
		if overrides.Auth.HeaderFormat != "" {
			merged.Auth.HeaderFormat = overrides.Auth.HeaderFormat
		}
		// Merge extra headers
		if len(overrides.Auth.ExtraHeaders) > 0 {
			merged.Auth.ExtraHeaders = append(merged.Auth.ExtraHeaders, overrides.Auth.ExtraHeaders...)
		}
	}

	if overrides.Models != nil {
		if overrides.Models.Format != "" {
			merged.Models.Format = overrides.Models.Format
		}
		if overrides.Models.Prefix != "" {
			merged.Models.Prefix = overrides.Models.Prefix
		}
	}

	// Merge feature flags (user can override)
	if overrides.Features != nil {
		// Only override if explicitly set
		merged.Features.MergeWith(overrides.Features)
	}

	return merged
}

// MergeWith merges feature flags with user overrides.
func (f *FeatureFlags) MergeWith(overrides *FeatureFlags) {
	if overrides == nil {
		return
	}

	// Only override non-zero values (allowing explicit false)
	// This is a simple merge; could be more sophisticated
	if overrides.MaxTokensParam != "" {
		f.MaxTokensParam = overrides.MaxTokensParam
	}
	if overrides.SystemPromptStyle != "" {
		f.SystemPromptStyle = overrides.SystemPromptStyle
	}
}

// UserConfig represents user-provided configuration overrides.
type UserConfig struct {
	ProfileName string
	BaseURL     string
	APIKey      string
	Auth        *AuthConfig
	Models      *ModelConfig
	Features    *FeatureFlags
	// Add provider-specific fields as needed
	AppName      string            // For OpenRouter
	AppURL       string            // For OpenRouter
	ResourceName string            // For Azure
	APIVersion   string            // For Azure
	Deployments  map[string]string // For Azure
}

// FromUserConfig creates a profile from user configuration (no built-in profile).
func FromUserConfig(userConfig UserConfig) Profile {
	profile := Profile{
		Name:           userConfig.ProfileName,
		DisplayName:    userConfig.ProfileName,
		BaseURL:        userConfig.BaseURL,
		CompatibleWith: "openai",
	}

	if userConfig.Auth != nil {
		profile.Auth = *userConfig.Auth
	} else {
		// Default auth
		profile.Auth = AuthConfig{
			Type:         "bearer",
			HeaderName:   "Authorization",
			HeaderFormat: "Bearer {api_key}",
		}
	}

	if userConfig.Features != nil {
		profile.Features = *userConfig.Features
	} else {
		// Default features (assume standard OpenAI)
		profile.Features = FeatureFlags{
			ChatCompletion:    true,
			Streaming:         true,
			FunctionCalling:   true,
			Vision:            false,
			Audio:             false,
			ReasoningEffort:   false,
			Verbosity:         false,
			CustomTools:       false,
			MaxTokensParam:    "max_tokens",
			SystemPromptStyle: "message",
		}
	}

	if userConfig.Models != nil {
		profile.Models = *userConfig.Models
	} else {
		// Default model config
		profile.Models = ModelConfig{
			Format:            "{model}",
			SupportsDiscovery: false,
		}
	}

	return profile
}
