package mcp

// ConfigFile represents the MCP configuration file schema.
type ConfigFile struct {
	SchemaVersion   int                               `json:"schema_version"`
	Servers         []ServerConfig                    `json:"servers"`
	PluginOverrides map[string]map[string]ServerPatch `json:"plugin_overrides,omitempty"`
}

// ServerConfig represents a single MCP server configuration.
type ServerConfig struct {
	Name          string            `json:"name"`
	Type          string            `json:"type,omitempty"`
	Enabled       bool              `json:"enabled,omitempty"`
	CredentialRef string            `json:"credential_ref,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	SecretEnv     []string          `json:"secret_env,omitempty"`
	WorkingDir    string            `json:"working_dir,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	SecretHeaders []string          `json:"secret_headers,omitempty"`
	Auth          *AuthConfig       `json:"auth,omitempty"`
	TimeoutSec    int               `json:"timeout_sec,omitempty"`
	Retries       int               `json:"retries,omitempty"`
	OAuth         *OAuthConfig      `json:"oauth,omitempty"`
	Tools         *ToolsConfig      `json:"tools,omitempty"`
}

// ServerPatch represents a partial configuration used for overrides.
type ServerPatch struct {
	Type          *string            `json:"type,omitempty"`
	Enabled       *bool              `json:"enabled,omitempty"`
	CredentialRef *string            `json:"credential_ref,omitempty"`
	Command       *string            `json:"command,omitempty"`
	Args          *[]string          `json:"args,omitempty"`
	Env           *map[string]string `json:"env,omitempty"`
	SecretEnv     *[]string          `json:"secret_env,omitempty"`
	WorkingDir    *string            `json:"working_dir,omitempty"`
	URL           *string            `json:"url,omitempty"`
	Headers       *map[string]string `json:"headers,omitempty"`
	SecretHeaders *[]string          `json:"secret_headers,omitempty"`
	Auth          *AuthConfig        `json:"auth,omitempty"`
	TimeoutSec    *int               `json:"timeout_sec,omitempty"`
	Retries       *int               `json:"retries,omitempty"`
	OAuth         *OAuthConfig       `json:"oauth,omitempty"`
	Tools         *ToolsConfig       `json:"tools,omitempty"`
}

// AuthConfig controls how tokens are applied to requests.
type AuthConfig struct {
	Mode   string `json:"mode,omitempty"`
	Header string `json:"header,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	Env    string `json:"env,omitempty"`
	Query  string `json:"query,omitempty"`
}

// OAuthConfig defines OAuth options for a server.
type OAuthConfig struct {
	ClientID        string   `json:"client_id,omitempty"`
	ClientSecretRef string   `json:"client_secret_ref,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
	AuthURL         string   `json:"auth_url,omitempty"`
	TokenURL        string   `json:"token_url,omitempty"`
}

// ToolsConfig configures tool allow/deny lists.
type ToolsConfig struct {
	Mode     string   `json:"mode,omitempty"`
	Enabled  []string `json:"enabled,omitempty"`
	Disabled []string `json:"disabled,omitempty"`
}

// ConfigScope identifies which layer a config belongs to.
type ConfigScope string

const (
	ScopeGlobal  ConfigScope = "global"
	ScopeProject ConfigScope = "project"
	ScopePlugin  ConfigScope = "plugin"
)

// OriginType labels the source of a server.
type OriginType string

const (
	OriginGlobal      OriginType = "global"
	OriginProject     OriginType = "project"
	OriginPlugin      OriginType = "plugin"
	OriginMarketplace OriginType = "marketplace"
	OriginImport      OriginType = "import"
	OriginBuiltin     OriginType = "builtin"
)

// Editability describes whether a server can be edited.
type Editability string

const (
	EditabilityReadonly            Editability = "readonly"
	EditabilityEditableWithWarning Editability = "editable_with_warning"
	EditabilityEditable            Editability = "editable"
)

// ShadowInfo describes a higher-precedence server shadowing another.
type ShadowInfo struct {
	Name   string     `json:"name"`
	Origin OriginType `json:"origin"`
}

// ResolvedServer represents a server with origin metadata.
type ResolvedServer struct {
	Config         ServerConfig `json:"config"`
	Scope          ConfigScope  `json:"scope"`
	Origin         OriginType   `json:"origin"`
	OriginDetail   string       `json:"origin_detail"`
	ShadowedBy     []ShadowInfo `json:"shadowed_by,omitempty"`
	Editability    Editability  `json:"editability"`
	EditWarning    string       `json:"edit_warning,omitempty"`
	HasCredentials bool         `json:"has_credentials"`
	HasOverride    bool         `json:"has_override,omitempty"`
}

// RuntimeServer contains runtime-only data for a server.
type RuntimeServer struct {
	Config      ServerConfig
	Headers     map[string]string
	Env         map[string]string
	OAuthSecret string
	AuthToken   string
	ResolvedURL string
	Tools       *ToolsConfig
}

// LayeredConfig holds raw config files for global and project scopes.
type LayeredConfig struct {
	Global  *ConfigFile
	Project *ConfigFile
}

// ResolvedConfig is the merged view used at runtime.
type ResolvedConfig struct {
	Servers []ResolvedServer
}
