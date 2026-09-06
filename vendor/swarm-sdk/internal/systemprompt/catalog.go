package systemprompt

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// Entry is one named system prompt in the shared catalog. The JSON shape is
// backwards-compatible with swarm-tui/internal/chat/settings/SystemPromptEntry
// so the TUI and any SDK consumer (sac, headless CLIs) read and write the
// same file. The Role field is additive — existing TUI entries that omit it
// default to RoleAgent (generic free-form) and keep working.
type Entry struct {
	Name             string `json:"name"`
	Content          string `json:"content"`
	Builtin          bool   `json:"builtin"`
	WorkspaceContext bool   `json:"workspace_context,omitempty"`

	// Role declares which consumer slot this prompt fills. Empty or "agent"
	// means generic free-form chat/coding. Structured-output consumers
	// (planner, evaluator, merger) use dedicated roles so users can supply
	// custom prompts without replacing free-form agent prompts by accident.
	// See the Role* constants below for the known catalog of roles.
	Role string `json:"role,omitempty"`
}

// Role is a well-known slot name for prompt consumers. Empty Role means
// generic free-form agent. Consumers that expect a specific JSON output
// shape (planner/evaluator/merger) use non-empty roles so users can author
// role-tagged entries in ~/.swarm/config/system_prompts.yaml without risking a
// free-form prompt replacing a JSON-contract one.
type Role string

const (
	RoleAgent                  Role = "" // generic free-form agent
	RoleShotgunAgent           Role = "shotgun-agent"
	RoleShotgunPlanner         Role = "shotgun-planner"
	RoleShotgunEvaluator       Role = "shotgun-evaluator"
	RoleShotgunMerger          Role = "shotgun-merger"
	RoleReview                 Role = "review"
	RoleReviewFix              Role = "review-fix"
	RoleNightwatchAnalysis     Role = "nightwatch-analysis"
	RoleNightwatchVerification Role = "nightwatch-verification"
	RoleNightwatchRegression   Role = "nightwatch-regression"
	RoleConversation           Role = "conversation"
	RoleWorktreeAgent          Role = "worktree-agent"
	RoleGenerateCommit         Role = "generate-commit"
	RoleSteeringEvaluator      Role = "steering-evaluator"
)

// Catalog is the full on-disk document at ~/.swarm/config/system_prompts.yaml.
type Catalog struct {
	ActivePrompt string  `json:"active_prompt"`
	Prompts      []Entry `json:"prompts"`
}

// ErrCatalogMissing is returned by Load when the catalog file does not exist.
// Callers that want to treat "no catalog" as "no override" can check for this
// error and fall back to their built-in default prompt.
var ErrCatalogMissing = errors.New("systemprompt: catalog file not found")

// DefaultCatalogPath is the canonical ~/.swarm/config/system_prompts.yaml.
func DefaultCatalogPath() string {
	return paths.SystemPromptsFile()
}

// Load reads the catalog from DefaultCatalogPath. Returns ErrCatalogMissing
// when the file is absent so callers can distinguish "user hasn't configured
// any prompts" from "catalog file is malformed".
func Load() (*Catalog, error) {
	return LoadFrom(DefaultCatalogPath())
}

// LoadFrom reads the catalog from an explicit path.
func LoadFrom(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCatalogMissing
		}
		return nil, fmt.Errorf("systemprompt: read %s: %w", path, err)
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("systemprompt: parse %s: %w", path, err)
	}
	return &c, nil
}

// ByName returns the entry whose Name matches exactly. Lookup is O(n) — the
// catalog is tiny so a map isn't worth the allocation.
func (c *Catalog) ByName(name string) (Entry, bool) {
	for _, e := range c.Prompts {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Names returns every prompt name in catalog order.
func (c *Catalog) Names() []string {
	out := make([]string, 0, len(c.Prompts))
	for _, e := range c.Prompts {
		out = append(out, e.Name)
	}
	return out
}

// Active returns the currently-selected prompt (ActivePrompt field). Returns
// false when ActivePrompt is unset or refers to a missing entry.
func (c *Catalog) Active() (Entry, bool) {
	if c.ActivePrompt == "" {
		return Entry{}, false
	}
	return c.ByName(c.ActivePrompt)
}

// ByRole returns every entry whose Role matches. Order preserved.
func (c *Catalog) ByRole(role Role) []Entry {
	out := make([]Entry, 0, len(c.Prompts))
	for _, e := range c.Prompts {
		if Role(e.Role) == role {
			out = append(out, e)
		}
	}
	return out
}

// FirstByRole returns the first entry matching Role. Used by Resolve when no
// ExplicitName is provided — multiple entries per role is allowed but only
// the first wins by default. If a user wants deterministic selection they
// should set ActivePrompt to the entry's name, or pass ExplicitName.
func (c *Catalog) FirstByRole(role Role) (Entry, bool) {
	for _, e := range c.Prompts {
		if Role(e.Role) == role {
			return e, true
		}
	}
	return Entry{}, false
}

// Request describes how to look up a prompt for a given consumer.
type Request struct {
	// Role is the slot this consumer fills (e.g. RoleShotgunPlanner).
	Role Role

	// ExplicitName, if non-empty, forces selection of the named entry.
	// Typically sourced from a --prompt CLI flag.
	ExplicitName string

	// Fallback is the hardcoded prompt returned when no catalog match is
	// found. For structured-JSON consumers this MUST contain the JSON
	// schema contract so omitting a catalog entry behaves identically to
	// the pre-catalog world.
	Fallback string
}

// Resolve looks up the prompt content for a Request. Precedence:
//
//  1. ExplicitName (looked up by name in the catalog).
//  2. ActivePrompt if its role matches req.Role.
//  3. First entry whose Role matches req.Role.
//  4. Fallback.
//
// When the catalog file is missing or malformed, Resolve returns Fallback —
// the caller never sees an error. This keeps every consumer call-site to a
// single line and makes the zero-config path safe by default.
func Resolve(req Request) string {
	cat, err := Load()
	if err != nil {
		return req.Fallback
	}
	return cat.Resolve(req)
}

// Resolve is the method form of the package-level Resolve, for callers that
// already have a catalog in hand (e.g. loaded once at startup).
func (c *Catalog) Resolve(req Request) string {
	if c == nil {
		return req.Fallback
	}
	if req.ExplicitName != "" {
		if e, ok := c.ByName(req.ExplicitName); ok {
			return e.Content
		}
	}
	if active, ok := c.Active(); ok && Role(active.Role) == req.Role {
		return active.Content
	}
	if e, ok := c.FirstByRole(req.Role); ok {
		return e.Content
	}
	return req.Fallback
}
