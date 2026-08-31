package harness

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

// defaultHookTimeoutSeconds is the timeout applied when a hook entry omits
// timeoutSeconds. Documented default: 30 seconds.
const defaultHookTimeoutSeconds = 30

// Valid hook scope values. matcher is required iff scope != HookScopeGlobal.
const (
	HookScopeGlobal    = "global"
	HookScopeInterface = "interface"
	HookScopeAgent     = "agent"
	HookScopeTool      = "tool"
)

// Valid hook type values. Exactly one of command/path/url must be set,
// matching the declared type.
const (
	HookTypeCommand = "command"
	HookTypeScript  = "script"
	HookTypeHTTP    = "http"
)

// HookEntry is one declared entry under the v1alpha1 `hooks:` section. It is
// DECLARATION + RESOLUTION only: harness parses, validates, and hashes/resolves
// the entry but never executes it, spawns a process, or opens a network
// connection. Execution is a later, client-side concern (Phase 6b), mirroring
// how SkillEntry (Phase 5a) preceded client-side skill loading (Phase 5b).
type HookEntry struct {
	ID      string `json:"id"`
	Event   string `json:"event"`
	Scope   string `json:"scope"`
	Matcher string `json:"matcher,omitempty"`
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Path    string `json:"path,omitempty"`
	URL     string `json:"url,omitempty"`
	// Priority orders hook execution among hooks sharing an event; default 0.
	Priority int `json:"priority,omitempty"`
	// TimeoutSeconds bounds a single hook invocation; 0 (unset) resolves to
	// defaultHookTimeoutSeconds. A negative value is a diagnostic.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// Enabled defaults to true when unset (a pointer distinguishes absent from
	// explicit false).
	Enabled *bool `json:"enabled,omitempty"`
	// Environment is an ALLOWLIST of environment variable NAMES to pass through
	// from the ambient process environment at execution time (Phase 6b). It
	// NEVER carries literal values.
	Environment []string `json:"environment,omitempty"`
}

// HookSpec is one resolved hook carried on the immutable Plan. It is fully
// redacted for display: an inline command is described by content hash + byte
// length ONLY (mirroring promptText/RevealSystemPrompt taint); a script's
// content is hashed but never retained; a url is shown as-is (it is a
// destination, not a secret).
type HookSpec struct {
	// ID is the stable hook id.
	ID string `json:"id"`
	// Event is the free-form dot-notation matcher/event name. harness does not
	// validate it against a closed enum; that coupling belongs to the
	// client/internal-hooks side (Phase 6b).
	Event string `json:"event"`
	// Scope is one of global|interface|agent|tool.
	Scope string `json:"scope"`
	// Matcher narrows a non-global scope (interface/agent-id/tool-name); empty
	// for scope == global.
	Matcher string `json:"matcher,omitempty"`
	// Type is one of command|script|http.
	Type string `json:"type"`
	// Path is the manifest-relative, normalized display label for a
	// type == script entry (empty otherwise). It intentionally mirrors the
	// provenance ref rather than an absolute host path, so a compiled plan
	// stays portable.
	Path string `json:"path,omitempty"`
	// ContentHash is "sha256:<hex>" of the backing script file for
	// type == script (empty otherwise). The file content is never retained.
	ContentHash string `json:"contentHash,omitempty"`
	// URL is the plain destination for type == http (not tainted; it is a
	// destination, not a credential).
	URL string `json:"url,omitempty"`
	// CommandHash is "sha256:<hex>" of the inline command for type == command
	// (empty otherwise). The command text itself is NEVER exposed here; only
	// RevealHookCommand returns it.
	CommandHash string `json:"commandHash,omitempty"`
	// CommandBytes is the byte length of the inline command for type == command
	// (0 otherwise).
	CommandBytes int `json:"commandBytes,omitempty"`
	// Priority orders hook execution among hooks sharing an event.
	Priority int `json:"priority"`
	// TimeoutSeconds is the resolved timeout (defaulted when unset).
	TimeoutSeconds int `json:"timeoutSeconds"`
	// Enabled is the resolved enabled flag (defaulted to true when unset).
	Enabled bool `json:"enabled"`
	// Environment is a copy of the resolved allowlist of env var NAMES (never
	// values).
	Environment []string `json:"environment,omitempty"`
}

// resolveHooks validates and resolves the declarative hooks section against
// the manifest directory. Path-backed (type: script) entries fail closed when
// the referenced file does not exist / is unreadable / escapes the manifest
// subtree. Script contents are hashed but NEVER stored; inline commands are
// retained in full on the Plan (tainted) but never exposed outside a hash +
// byte count on the redacted surface.
func resolveHooks(p *Plan, sourcePath, mdir string, entries []HookEntry) Diagnostics {
	var ds Diagnostics

	seen := make(map[string]struct{}, len(entries))
	specs := make([]HookSpec, 0, len(entries))
	commands := make(map[string]string, len(entries))

	for i, e := range entries {
		field := "hooks[" + strconv.Itoa(i) + "]"

		if e.ID == "" {
			ds = append(ds, newDiag("harness.hooks.id.missing", field+".id",
				"hook entry requires a non-empty id", sourcePath))
			continue
		}
		if _, dup := seen[e.ID]; dup {
			ds = append(ds, newDiag("harness.hooks.id.duplicate", field+".id",
				"duplicate hook id "+quote(e.ID), sourcePath))
			continue
		}
		seen[e.ID] = struct{}{}

		entryOK := true

		if e.Event == "" {
			ds = append(ds, newDiag("harness.hooks.event.missing", field+".event",
				"hook event is required", sourcePath))
			entryOK = false
		}

		switch e.Scope {
		case HookScopeGlobal, HookScopeInterface, HookScopeAgent, HookScopeTool:
			// valid
		case "":
			ds = append(ds, newDiag("harness.hooks.scope.missing", field+".scope",
				"hook scope is required", sourcePath))
			entryOK = false
		default:
			ds = append(ds, newDiag("harness.hooks.scope.invalid", field+".scope",
				"hook scope must be one of global, interface, agent, tool; got "+quote(e.Scope), sourcePath))
			entryOK = false
		}

		if e.Scope == HookScopeGlobal {
			if e.Matcher != "" {
				ds = append(ds, newDiag("harness.hooks.matcher.forbidden", field+".matcher",
					"hook matcher must not be set when scope is global", sourcePath))
				entryOK = false
			}
		} else if e.Scope != "" {
			if e.Matcher == "" {
				ds = append(ds, newDiag("harness.hooks.matcher.missing", field+".matcher",
					"hook matcher is required when scope is not global", sourcePath))
				entryOK = false
			}
		}

		hasCommand := e.Command != ""
		hasPath := e.Path != ""
		hasURL := e.URL != ""
		setCount := 0
		if hasCommand {
			setCount++
		}
		if hasPath {
			setCount++
		}
		if hasURL {
			setCount++
		}

		switch e.Type {
		case HookTypeCommand, HookTypeScript, HookTypeHTTP:
			// valid; oneOf checked below.
		case "":
			ds = append(ds, newDiag("harness.hooks.type.missing", field+".type",
				"hook type is required", sourcePath))
			entryOK = false
		default:
			ds = append(ds, newDiag("harness.hooks.type.invalid", field+".type",
				"hook type must be one of command, script, http; got "+quote(e.Type), sourcePath))
			entryOK = false
		}

		if setCount != 1 {
			ds = append(ds, newDiag("harness.hooks.source.oneOf", field,
				"hook entry must set exactly one of command, path, url", sourcePath))
			entryOK = false
		} else if e.Type != "" {
			mismatched := (e.Type == HookTypeCommand && !hasCommand) ||
				(e.Type == HookTypeScript && !hasPath) ||
				(e.Type == HookTypeHTTP && !hasURL)
			if mismatched {
				ds = append(ds, newDiag("harness.hooks.source.typeMismatch", field,
					"hook type "+quote(e.Type)+" does not match the set source field (command/path/url)", sourcePath))
				entryOK = false
			}
		}

		timeout := e.TimeoutSeconds
		if timeout == 0 {
			timeout = defaultHookTimeoutSeconds
		} else if timeout < 0 {
			ds = append(ds, newDiag("harness.hooks.timeoutSeconds.invalid", field+".timeoutSeconds",
				"hook timeoutSeconds must not be negative", sourcePath))
			entryOK = false
		}

		enabled := true
		if e.Enabled != nil {
			enabled = *e.Enabled
		}

		envSeen := make(map[string]struct{}, len(e.Environment))
		env := make([]string, 0, len(e.Environment))
		for j, name := range e.Environment {
			envField := field + ".environment[" + strconv.Itoa(j) + "]"
			if name == "" {
				ds = append(ds, newDiag("harness.hooks.environment.empty", envField,
					"hook environment entry must not be empty", sourcePath))
				entryOK = false
				continue
			}
			if _, dup := envSeen[name]; dup {
				ds = append(ds, newDiag("harness.hooks.environment.duplicate", envField,
					"duplicate environment name "+quote(name), sourcePath))
				entryOK = false
				continue
			}
			envSeen[name] = struct{}{}
			env = append(env, name)
		}

		if !entryOK {
			continue
		}

		spec := HookSpec{
			ID:             e.ID,
			Event:          e.Event,
			Scope:          e.Scope,
			Matcher:        e.Matcher,
			Type:           e.Type,
			Priority:       e.Priority,
			TimeoutSeconds: timeout,
			Enabled:        enabled,
			Environment:    env,
		}

		switch e.Type {
		case HookTypeCommand:
			spec.CommandHash = hashString(e.Command)
			spec.CommandBytes = len(e.Command)
			commands[e.ID] = e.Command
			p.addProvenance("hooks."+e.ID, "inline", "")
		case HookTypeScript:
			abs, d := resolveContainedFile(sourcePath, field+".path", mdir, e.Path)
			if d != nil {
				ds = append(ds, *d)
				continue
			}
			hash, hd := hashHookScript(sourcePath, field+".path", abs)
			if hd != nil {
				ds = append(ds, *hd)
				continue
			}
			spec.Path = filepath.Clean(e.Path)
			spec.ContentHash = hash
			p.addProvenance("hooks."+e.ID, "file", filepath.Clean(e.Path))
		case HookTypeHTTP:
			u, err := url.Parse(e.URL)
			if err != nil || !u.IsAbs() || u.Host == "" {
				ds = append(ds, newDiag("harness.hooks.url.invalid", field+".url",
					"hook url must be a well-formed absolute URL", sourcePath))
				continue
			}
			spec.URL = e.URL
			p.addProvenance("hooks."+e.ID, "manifest", "")
		}

		specs = append(specs, spec)
	}

	if ds.HasErrors() {
		return ds
	}
	p.hooks = specs
	p.hookCommands = commands
	return ds
}

// hashHookScript computes the content hash of a path-backed hook script
// WITHOUT retaining its contents. Unlike skills' SKILL.md convention, a hook
// path must reference a file directly, not a directory.
func hashHookScript(sourcePath, fieldPath, abs string) (string, *Diagnostic) {
	info, err := os.Stat(abs)
	if err != nil {
		d := newDiag("harness.hooks.unreadable", fieldPath,
			"referenced hook script could not be read", sourcePath)
		return "", &d
	}
	if info.IsDir() {
		d := newDiag("harness.hooks.pathIsDir", fieldPath,
			"hook path must reference a file, not a directory", sourcePath)
		return "", &d
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		d := newDiag("harness.hooks.unreadable", fieldPath,
			"referenced hook script could not be read", sourcePath)
		return "", &d
	}
	return hashBytes(data), nil
}

// Hooks returns a copy of the resolved hook specs carried on the plan.
func (p *Plan) Hooks() []HookSpec {
	out := make([]HookSpec, len(p.hooks))
	copy(out, p.hooks)
	for i := range out {
		if out[i].Environment != nil {
			env := make([]string, len(out[i].Environment))
			copy(env, out[i].Environment)
			out[i].Environment = env
		}
	}
	return out
}

// RevealHookCommand returns the resolved inline command text for the hook
// with the given id, for a trusted later-phase consumer (Phase 6b client-side
// execution). It is deliberately explicit and never used by Explain/Digest,
// mirroring RevealSystemPrompt. ok is false when no type == command hook with
// that id was resolved onto the plan.
func (p *Plan) RevealHookCommand(id string) (command string, ok bool) {
	command, ok = p.hookCommands[id]
	return command, ok
}
