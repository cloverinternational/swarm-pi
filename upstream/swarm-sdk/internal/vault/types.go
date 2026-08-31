// Package vault provides secure credential storage with execution proxy pattern.
// Agents never see actual credential values - commands are executed on their behalf
// with credentials, and output is redacted before returning.
package vault

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

// CredentialKind represents the type of credential.
type CredentialKind string

const (
	CredentialKindAPIKey        CredentialKind = "api_key"
	CredentialKindBearerToken   CredentialKind = "bearer_token"
	CredentialKindAWSAccessKey  CredentialKind = "aws_access_key"
	CredentialKindAWSSecretKey  CredentialKind = "aws_secret_key"
	CredentialKindAWSSession    CredentialKind = "aws_session_token"
	CredentialKindSSHKey        CredentialKind = "ssh_key"
	CredentialKindSSHPassphrase CredentialKind = "ssh_passphrase"
	CredentialKindPassword      CredentialKind = "password"
	CredentialKindUsername      CredentialKind = "username"
	CredentialKindTLSCert       CredentialKind = "tls_cert"
	CredentialKindTLSKey        CredentialKind = "tls_key"
	CredentialKindEnvVar        CredentialKind = "env_var"
)

// CredentialScope defines where a credential is accessible.
type CredentialScope string

const (
	ScopeGlobal  CredentialScope = "global"  // Available to all projects
	ScopeProject CredentialScope = "project" // Project-specific
)

// VaultMode defines the permission mode for credential access.
type VaultMode string

const (
	// ModeYOLO auto-approves all credential access with audit logging.
	ModeYOLO VaultMode = "yolo"
	// ModeDelegated skips the vault's ordinary approval prompt because a trusted
	// interactive host owns that prompt. Credential constraints, expiry,
	// two-person integrity, empty-secret guards, redaction, and auditing remain
	// enforced by the executor.
	ModeDelegated VaultMode = "delegated"
	// ModeBalanced requires approval for sensitive credentials.
	ModeBalanced VaultMode = "balanced"
	// ModeRestrictive requires approval for all credential access.
	ModeRestrictive VaultMode = "restrictive"
)

// EncryptionMode defines how the vault is encrypted.
type EncryptionMode string

const (
	// EncryptionPassphrase uses a passphrase-derived key (scrypt).
	// Single-user, personal vaults.
	EncryptionPassphrase EncryptionMode = "passphrase"
	// EncryptionMultiRecipient uses X25519 keypairs.
	// Team vaults - encrypt to multiple recipients, any can decrypt.
	EncryptionMultiRecipient EncryptionMode = "multi-recipient"
)

// InjectMethod defines how a credential is injected into execution.
type InjectMethod string

const (
	InjectEnv      InjectMethod = "env"       // Environment variable
	InjectFile     InjectMethod = "file"      // Temporary file
	InjectSSHAgent InjectMethod = "ssh-agent" // SSH agent
)

// InjectConfig specifies how to inject a credential.
type InjectConfig struct {
	Method      InjectMethod `json:"method" yaml:"method"`
	Target      string       `json:"target" yaml:"target"` // Env var name or file path
	Permissions int          `json:"permissions,omitempty" yaml:"permissions,omitempty"`
}

// Credential represents a stored credential with constraints.
type Credential struct {
	// Identity
	ID          string         `json:"id" yaml:"id"`
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Kind        CredentialKind `json:"kind" yaml:"kind"`

	// The secret value - NEVER exposed to agents
	Secret string `json:"secret" yaml:"secret"`

	// Scope
	Scope     CredentialScope `json:"scope" yaml:"scope"`
	ProjectID string          `json:"projectId,omitempty" yaml:"projectId,omitempty"`

	// Execution constraints
	AllowedTools    []string `json:"allowedTools,omitempty" yaml:"allowedTools,omitempty"`
	AllowedCommands []string `json:"allowedCommands,omitempty" yaml:"allowedCommands,omitempty"`
	AllowedHosts    []string `json:"allowedHosts,omitempty" yaml:"allowedHosts,omitempty"` // Host restrictions

	// Injection configuration
	Inject InjectConfig `json:"inject" yaml:"inject"`

	// Tags for filtering and sensitivity
	Tags []string `json:"tags,omitempty" yaml:"tags,omitempty"`

	// Metadata
	CreatedAt  time.Time  `json:"createdAt" yaml:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt" yaml:"updatedAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty" yaml:"expiresAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty" yaml:"lastUsedAt,omitempty"`

	// Audit level
	AuditLevel AuditLevel `json:"auditLevel,omitempty" yaml:"auditLevel,omitempty"`
}

// AuditLevel defines how much to log.
type AuditLevel string

const (
	AuditNone     AuditLevel = "none"     // No logging
	AuditMetadata AuditLevel = "metadata" // Log usage without values
	AuditFull     AuditLevel = "full"     // Log with redacted values
)

// IsExpired returns true if the credential has expired.
func (c *Credential) IsExpired() bool {
	if c.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*c.ExpiresAt)
}

// IsSensitive returns true if tagged as sensitive.
func (c *Credential) IsSensitive() bool {
	return slices.Contains(c.Tags, "sensitive")
}

// highRiskKinds are credential kinds that are treated as sensitive by default,
// regardless of whether the "sensitive" tag was set. These grant broad or
// long-lived access and should require approval in balanced mode even if the
// operator forgot to tag them.
var highRiskKinds = map[CredentialKind]bool{
	CredentialKindAWSAccessKey:  true,
	CredentialKindAWSSecretKey:  true,
	CredentialKindAWSSession:    true,
	CredentialKindSSHKey:        true,
	CredentialKindSSHPassphrase: true,
	CredentialKindPassword:      true,
	CredentialKindTLSKey:        true,
}

// RequiresApprovalByDefault returns true when the credential should require
// explicit approval in balanced mode. This is true if it is tagged sensitive
// OR its kind is inherently high risk (AWS keys, SSH keys, passwords, TLS keys).
func (c *Credential) RequiresApprovalByDefault() bool {
	return c.IsSensitive() || highRiskKinds[c.Kind]
}

// CanUseTool checks if the credential can be used by the specified tool.
func (c *Credential) CanUseTool(tool string) bool {
	if len(c.AllowedTools) == 0 {
		return true
	}
	toolLower := strings.ToLower(tool)
	for _, t := range c.AllowedTools {
		if strings.ToLower(t) == toolLower || t == "*" {
			return true
		}
	}
	return false
}

// CanUseCommand checks if the credential can be used for the specified command.
func (c *Credential) CanUseCommand(command string) bool {
	if len(c.AllowedCommands) == 0 {
		return true
	}
	for _, pattern := range c.AllowedCommands {
		if matchCommand(pattern, command) {
			return true
		}
	}
	return false
}

// CanUseHost checks if the credential can be used for the specified host.
// Supports exact match, wildcard (*.domain.com), and regex (~pattern).
func (c *Credential) CanUseHost(host string) bool {
	if len(c.AllowedHosts) == 0 {
		return true // No host restriction
	}

	hostLower := strings.ToLower(host)
	for _, allowed := range c.AllowedHosts {
		allowedLower := strings.ToLower(allowed)

		// Regex pattern (starts with ~)
		if strings.HasPrefix(allowedLower, "~") {
			pattern := allowedLower[1:]
			matched, err := regexp.MatchString(pattern, hostLower)
			if err == nil && matched {
				return true
			}
			continue
		}

		// Wildcard match (*.domain.com)
		if strings.HasPrefix(allowedLower, "*.") {
			suffix := allowedLower[1:] // .domain.com
			if strings.HasSuffix(hostLower, suffix) {
				return true
			}
			continue
		}

		// Exact match
		if allowedLower == hostLower {
			return true
		}
	}
	return false
}

// CredentialFilter is used to filter credentials when listing.
type CredentialFilter struct {
	Kind           CredentialKind  `json:"kind,omitempty" yaml:"kind,omitempty"`
	Scope          CredentialScope `json:"scope,omitempty" yaml:"scope,omitempty"`
	ProjectID      string          `json:"projectId,omitempty" yaml:"projectId,omitempty"`
	Tags           []string        `json:"tags,omitempty" yaml:"tags,omitempty"`
	IncludeExpired bool            `json:"includeExpired,omitempty" yaml:"includeExpired,omitempty"`
	IncludeSecrets bool            `json:"includeSecrets,omitempty" yaml:"includeSecrets,omitempty"` // Only for CLI, never for agents
}

// VaultConfig is the configuration for the vault.
type VaultConfig struct {
	Enabled         bool      `json:"enabled" yaml:"enabled"`
	DefaultMode     VaultMode `json:"defaultMode" yaml:"defaultMode"`
	AuditEnabled    bool      `json:"auditEnabled" yaml:"auditEnabled"`
	AuditLogPath    string    `json:"auditLogPath,omitempty" yaml:"auditLogPath,omitempty"`
	TrustedUnlocked bool      `json:"trustedUnlocked,omitempty" yaml:"trustedUnlocked,omitempty"`
}

// ExecutionRequest represents a request to execute a command with a credential.
type ExecutionRequest struct {
	CredentialID string        `json:"credentialId"`
	Command      string        `json:"command"`
	Args         []string      `json:"args,omitempty"`
	WorkingDir   string        `json:"workingDir,omitempty"`
	Timeout      time.Duration `json:"timeout,omitempty"`

	// Context for permission checks
	Tool      string `json:"tool,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	Host      string `json:"host,omitempty"` // Extracted from command or explicit
	Reason    string `json:"reason,omitempty"`
}

// EffectiveCommandLine reconstructs the full command line (command plus
// arguments, space-joined) that was actually authorized/executed. Command
// allow-list patterns like "agent-browser *" are written against the FULL
// invocation an operator expects to see, but vault_exec's command-string
// convenience parsing (splitShellCommand) splits a single command string
// into Command="agent-browser" + Args=["--session-name",...] before
// building this request — so matching an allow-list pattern against the
// bare Command alone silently rejects (or, for a differently-shaped
// pattern, could silently over-match) every invocation that carries
// arguments. CanUseCommand callers MUST match against this reconstruction,
// never against req.Command in isolation. See issue #247.
func (r ExecutionRequest) EffectiveCommandLine() string {
	if len(r.Args) == 0 {
		return r.Command
	}
	return r.Command + " " + strings.Join(r.Args, " ")
}

// ExecutionResult contains the result of a credential execution.
// IMPORTANT: Never includes the actual credential value!
type ExecutionResult struct {
	Stdout         string        `json:"stdout"`                   // Redacted stdout
	Stderr         string        `json:"stderr"`                   // Redacted stderr
	ExitCode       int           `json:"exitCode"`                 // Process exit code
	Duration       time.Duration `json:"duration"`                 // Execution duration
	RedactedCount  int           `json:"redactedCount"`            // Number of redactions
	RedactionHints []string      `json:"redactionHints,omitempty"` // What was redacted (without values)
	// SafeToParse is true when zero redactions occurred, meaning the output is
	// byte-for-byte the command's real output and is safe to parse
	// programmatically. It is false when any redaction happened, since redaction
	// may have altered structurally-significant bytes.
	SafeToParse bool `json:"safeToParse"`
}

// AuditEntry represents an audit log entry.
type AuditEntry struct {
	Timestamp      time.Time `json:"timestamp"`
	CredentialID   string    `json:"credentialId"`
	CredentialName string    `json:"credentialName"`
	Action         string    `json:"action"` // "execute", "access", "add", "remove"
	Tool           string    `json:"tool,omitempty"`
	Command        string    `json:"command,omitempty"`
	Host           string    `json:"host,omitempty"`
	Approved       bool      `json:"approved"`
	ApprovalScope  string    `json:"approvalScope,omitempty"`
	DeniedReason   string    `json:"deniedReason,omitempty"`
	SessionID      string    `json:"sessionId,omitempty"`
	ProjectID      string    `json:"projectId,omitempty"`
}

// expandPath expands ~ to the home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return strings.Replace(path, "~", home, 1)
	}
	return path
}
