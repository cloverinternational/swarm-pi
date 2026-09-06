package harness

import (
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// defaultMcpTimeoutSeconds is the timeout applied when an mcp entry omits
// timeoutSeconds. Documented default: 30 seconds (mirrors defaultHookTimeoutSeconds).
const defaultMcpTimeoutSeconds = 30

// Valid mcp type values. "oauth" is deliberately NOT accepted here: declaring
// an OAuth-authenticated server without a working auth flow would be a schema
// section that silently does nothing. It is explicitly rejected with a
// diagnostic pointing at a future phase rather than accepted and ignored.
const (
	McpTypeStdio = "stdio"
	McpTypeHTTP  = "http"
	McpTypeSSE   = "sse"
)

// mcpTypeOAuth is recognized ONLY so it can be rejected with a clear,
// actionable diagnostic instead of falling through to the generic
// "unknown type" message.
const mcpTypeOAuth = "oauth"

// McpEntry is one declared entry under the v1alpha1 `mcp:` section. It is
// DECLARATION + RESOLUTION only: harness parses, validates, and resolves the
// entry (manifest-relative workDir, url well-formedness, env-name allowlists)
// but NEVER connects to a server, spawns a process, or performs network
// discovery. Client-side execution is a later phase (Phase 7b), mirroring how
// HookEntry (Phase 6a) preceded client-side hook execution (Phase 6b).
type McpEntry struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	// Command/Args are required for type == stdio. Per the PINNING rule, the
	// resolved (command, args) pair must reference a pinned artifact unless
	// UnsafeDevMode is explicitly set — see isPinnedStdioCommand.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// WorkDir is optional, manifest-relative, and only meaningful for
	// type == stdio (it is the working directory of the spawned process).
	WorkDir string `json:"workDir,omitempty"`

	// URL is required for type == http|sse.
	URL string `json:"url,omitempty"`

	// TimeoutSeconds bounds a single request/connection; 0 (unset) resolves to
	// defaultMcpTimeoutSeconds. A negative value is a diagnostic.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// Enabled defaults to true when unset (a pointer distinguishes absent from
	// explicit false).
	Enabled *bool `json:"enabled,omitempty"`

	// Environment is an ALLOWLIST of environment variable NAMES to pass
	// through from the ambient process environment at execution time
	// (Phase 7b). It NEVER carries literal values.
	Environment []string `json:"environment,omitempty"`

	// Headers is only valid for type == http|sse. Each value must be a NAME
	// present in this entry's Environment allowlist — it is a reference to an
	// allowlisted env var, NEVER an inline literal secret.
	Headers map[string]string `json:"headers,omitempty"`

	// Tools is an optional allowlist of exposed tool names (empty/omitted means
	// all). ExcludeTools is an optional blocklist. Setting both is a
	// diagnostic — choose one.
	Tools        []string `json:"tools,omitempty"`
	ExcludeTools []string `json:"excludeTools,omitempty"`

	// UnsafeDevMode is a documented, explicit escape hatch that bypasses the
	// stdio pin check for this entry. It defaults to false and is never a
	// silent default.
	UnsafeDevMode bool `json:"unsafeDevMode,omitempty"`
}

// McpServerSpec is one resolved mcp server carried on the immutable Plan. Its
// fields are all NON-secret identifiers and are shown PLAINLY in Explain: an
// mcp command+args pair is an operational identifier (like agent.tools), not
// attacker-controllable free text the way an inline hook shell command is, so
// it is not tainted/hashed the way HookSpec's inline command is. Only actual
// environment VALUES are excluded from the harness surface entirely — this
// spec carries only env var NAMES (an allowlist) and header NAME references,
// never a value.
type McpServerSpec struct {
	// ID is the stable server id.
	ID string `json:"id"`
	// Type is one of stdio|http|sse.
	Type string `json:"type"`

	// Command/Args are shown plainly for type == stdio (empty otherwise).
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// WorkDir is the manifest-relative, normalized display label (empty when
	// unset). It intentionally mirrors the provenance ref rather than an
	// absolute host path, so a compiled plan stays portable.
	WorkDir string `json:"workDir,omitempty"`

	// URL is the plain destination for type == http|sse (empty otherwise). It
	// is not tainted; it is a destination, not a credential.
	URL string `json:"url,omitempty"`

	// TimeoutSeconds is the resolved timeout (defaulted when unset).
	TimeoutSeconds int `json:"timeoutSeconds"`
	// Enabled is the resolved enabled flag (defaulted to true when unset).
	Enabled bool `json:"enabled"`

	// Environment is a copy of the resolved allowlist of env var NAMES (never
	// values).
	Environment []string `json:"environment,omitempty"`
	// Headers is a copy of the resolved header-name -> env-var-name reference
	// map (never a literal value); only set for type == http|sse.
	Headers map[string]string `json:"headers,omitempty"`

	Tools        []string `json:"tools,omitempty"`
	ExcludeTools []string `json:"excludeTools,omitempty"`

	// Pinned reports whether the stdio command/args satisfied the pin
	// detection heuristic (always false for type == http|sse).
	Pinned bool `json:"pinned"`
	// UnsafeDevMode is the resolved escape-hatch flag as declared (it is
	// non-secret and shown plainly so an explained plan is self-describing
	// about its posture).
	UnsafeDevMode bool `json:"unsafeDevMode,omitempty"`
}

// resolveMcp validates and resolves the declarative mcp section against the
// manifest directory. It performs NO connection, process spawn, or network
// discovery: workDir is resolved/stat'd on the local filesystem exactly like a
// skill search root, and a url is only parsed (net/url), never dialed.
func resolveMcp(p *Plan, sourcePath, mdir string, entries []McpEntry) Diagnostics {
	var ds Diagnostics

	seen := make(map[string]struct{}, len(entries))
	specs := make([]McpServerSpec, 0, len(entries))

	for i, e := range entries {
		field := "mcp[" + strconv.Itoa(i) + "]"

		if e.ID == "" {
			ds = append(ds, newDiag("harness.mcp.id.missing", field+".id",
				"mcp entry requires a non-empty id", sourcePath))
			continue
		}
		if _, dup := seen[e.ID]; dup {
			ds = append(ds, newDiag("harness.mcp.id.duplicate", field+".id",
				"duplicate mcp id "+quote(e.ID), sourcePath))
			continue
		}
		seen[e.ID] = struct{}{}

		entryOK := true

		switch e.Type {
		case McpTypeStdio, McpTypeHTTP, McpTypeSSE:
			// valid
		case mcpTypeOAuth:
			ds = append(ds, newDiag("harness.mcp.type.oauthUnsupported", field+".type",
				"mcp type \"oauth\" is not supported yet; a declared OAuth-authenticated server "+
					"cannot be resolved without a working auth flow (planned for a future phase); "+
					"remove this entry or use stdio/http/sse", sourcePath))
			entryOK = false
		case "":
			ds = append(ds, newDiag("harness.mcp.type.missing", field+".type",
				"mcp type is required", sourcePath))
			entryOK = false
		default:
			ds = append(ds, newDiag("harness.mcp.type.invalid", field+".type",
				"mcp type must be one of stdio, http, sse; got "+quote(e.Type), sourcePath))
			entryOK = false
		}

		isStdio := e.Type == McpTypeStdio
		isRemote := e.Type == McpTypeHTTP || e.Type == McpTypeSSE

		var pinned bool
		if isStdio {
			if e.Command == "" {
				ds = append(ds, newDiag("harness.mcp.stdio.command.missing", field+".command",
					"mcp command is required for type stdio", sourcePath))
				entryOK = false
			} else {
				pinned = isPinnedStdioCommand(e.Command, e.Args)
				if !pinned && !e.UnsafeDevMode {
					ds = append(ds, newDiag("harness.mcp.stdio.unpinned", field,
						"mcp stdio entry must reference a pinned command (an absolute path, or a package "+
							"argument with an explicit version pin like pkg@1.2.3) or explicitly set "+
							"unsafeDevMode: true to bypass this check", sourcePath))
					entryOK = false
				}
			}
			if e.URL != "" {
				ds = append(ds, newDiag("harness.mcp.stdio.url.notAllowed", field+".url",
					"mcp url must not be set when type is stdio", sourcePath))
				entryOK = false
			}
			if len(e.Headers) > 0 {
				ds = append(ds, newDiag("harness.mcp.headers.notAllowedForStdio", field+".headers",
					"mcp headers is only valid for type http or sse", sourcePath))
				entryOK = false
			}
		} else if isRemote {
			if e.Command != "" || len(e.Args) > 0 {
				ds = append(ds, newDiag("harness.mcp.remote.command.notAllowed", field+".command",
					"mcp command/args must not be set when type is http or sse", sourcePath))
				entryOK = false
			}
			if e.WorkDir != "" {
				ds = append(ds, newDiag("harness.mcp.workDir.notAllowedForNonStdio", field+".workDir",
					"mcp workDir is only valid for type stdio", sourcePath))
				entryOK = false
			}
			if e.URL == "" {
				ds = append(ds, newDiag("harness.mcp.url.missing", field+".url",
					"mcp url is required for type http or sse", sourcePath))
				entryOK = false
			} else if u, err := url.Parse(e.URL); err != nil || !u.IsAbs() || u.Host == "" {
				ds = append(ds, newDiag("harness.mcp.url.invalid", field+".url",
					"mcp url must be a well-formed absolute URL", sourcePath))
				entryOK = false
			}
		}

		timeout := e.TimeoutSeconds
		if timeout == 0 {
			timeout = defaultMcpTimeoutSeconds
		} else if timeout < 0 {
			ds = append(ds, newDiag("harness.mcp.timeoutSeconds.invalid", field+".timeoutSeconds",
				"mcp timeoutSeconds must not be negative", sourcePath))
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
				ds = append(ds, newDiag("harness.mcp.environment.empty", envField,
					"mcp environment entry must not be empty", sourcePath))
				entryOK = false
				continue
			}
			if _, dup := envSeen[name]; dup {
				ds = append(ds, newDiag("harness.mcp.environment.duplicate", envField,
					"duplicate environment name "+quote(name), sourcePath))
				entryOK = false
				continue
			}
			envSeen[name] = struct{}{}
			env = append(env, name)
		}

		// Headers (http/sse only): each value must reference a NAME present in
		// this entry's own environment allowlist. A value that is not a
		// declared name is rejected — there is no way to carry an inline
		// literal secret here.
		var headerKeys []string
		for k := range e.Headers {
			headerKeys = append(headerKeys, k)
		}
		sort.Strings(headerKeys)
		headers := make(map[string]string, len(e.Headers))
		if isRemote {
			for _, k := range headerKeys {
				v := e.Headers[k]
				hf := field + ".headers." + k
				if k == "" {
					ds = append(ds, newDiag("harness.mcp.headers.keyEmpty", field+".headers",
						"mcp header name must not be empty", sourcePath))
					entryOK = false
					continue
				}
				if _, ok := envSeen[v]; !ok {
					ds = append(ds, newDiag("harness.mcp.headers.unknownEnvRef", hf,
						"mcp header value must reference a name declared in this entry's environment "+
							"allowlist; got "+quote(v), sourcePath))
					entryOK = false
					continue
				}
				headers[k] = v
			}
		}

		if len(e.Tools) > 0 && len(e.ExcludeTools) > 0 {
			ds = append(ds, newDiag("harness.mcp.tools.bothSet", field,
				"mcp entry must set at most one of tools, excludeTools", sourcePath))
			entryOK = false
		}

		if !entryOK {
			continue
		}

		spec := McpServerSpec{
			ID:             e.ID,
			Type:           e.Type,
			TimeoutSeconds: timeout,
			Enabled:        enabled,
			Environment:    env,
			Tools:          copyStringSlice(e.Tools),
			ExcludeTools:   copyStringSlice(e.ExcludeTools),
			UnsafeDevMode:  e.UnsafeDevMode,
		}
		if len(headers) > 0 {
			spec.Headers = headers
		}

		switch e.Type {
		case McpTypeStdio:
			spec.Command = e.Command
			spec.Args = copyStringSlice(e.Args)
			spec.Pinned = pinned
			if e.WorkDir != "" {
				abs, d := resolveContainedFile(sourcePath, field+".workDir", mdir, e.WorkDir)
				if d != nil {
					ds = append(ds, *d)
					continue
				}
				info, err := os.Stat(abs)
				if err != nil || !info.IsDir() {
					ds = append(ds, newDiag("harness.mcp.workDir.notDir", field+".workDir",
						"mcp workDir must be an existing directory", sourcePath))
					continue
				}
				spec.WorkDir = filepath.Clean(e.WorkDir)
			}
			p.addProvenance("mcp."+e.ID, "manifest", "")
		case McpTypeHTTP, McpTypeSSE:
			spec.URL = e.URL
			p.addProvenance("mcp."+e.ID, "manifest", "")
		}

		specs = append(specs, spec)
	}

	if ds.HasErrors() {
		return ds
	}
	p.mcpServers = specs
	return ds
}

// isPinnedStdioCommand implements the PINNING RULE detection heuristic for
// type: stdio entries: a command/args pair is considered pinned when EITHER
//
//   - command is an absolute path (implicitly pinned to a specific binary on
//     the host, rather than resolved by $PATH lookup at run time), OR
//   - command itself, or any element of args, carries an explicit version
//     pin of the form "<name>@<version>" where <version> is non-empty and is
//     not one of the well-known FLOATING tags ("latest", "next", "canary",
//     "*"). This also correctly handles npm-style scoped packages such as
//     "@modelcontextprotocol/server-filesystem@1.2.3": the LAST "@" in the
//     string is treated as the version separator, not the leading scope "@".
//
// This is a heuristic, not a package-manager-aware parser: it cannot verify
// that a version actually resolves to an immutable artifact (e.g. it cannot
// detect a git-branch ref smuggled in as a "version"). It only rejects the
// unambiguous cases (a bare/floating package name, or "pkg@latest") that this
// phase's PINNING RULE targets — the property that matters is "unpinned and
// not explicitly unsafe = rejected", not exhaustive artifact provenance
// verification (that would require the client-side resolver in a later
// phase). unsafeDevMode remains the documented, explicit escape hatch for
// anything this heuristic does not recognize as pinned.
func isPinnedStdioCommand(command string, args []string) bool {
	if filepath.IsAbs(command) {
		return true
	}
	if hasVersionPin(command) {
		return true
	}
	for _, a := range args {
		if hasVersionPin(a) {
			return true
		}
	}
	return false
}

// hasVersionPin reports whether s carries an explicit, non-floating version
// pin after its LAST "@" (so a leading npm scope marker like
// "@modelcontextprotocol/..." is not mistaken for a version separator).
func hasVersionPin(s string) bool {
	idx := lastIndexByte(s, '@')
	if idx <= 0 {
		return false
	}
	version := s[idx+1:]
	switch version {
	case "", "latest", "next", "canary", "*":
		return false
	}
	return true
}

// lastIndexByte is a tiny stdlib-only helper (avoids importing strings just
// for LastIndexByte in this file, keeping the import list minimal/obvious).
func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// copyStringSlice returns a defensive copy, preserving nil-vs-empty.
func copyStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// MCPServers returns a copy of the resolved mcp server specs carried on the
// plan.
func (p *Plan) MCPServers() []McpServerSpec {
	out := make([]McpServerSpec, len(p.mcpServers))
	copy(out, p.mcpServers)
	for i := range out {
		out[i].Args = copyStringSlice(out[i].Args)
		out[i].Environment = copyStringSlice(out[i].Environment)
		out[i].Tools = copyStringSlice(out[i].Tools)
		out[i].ExcludeTools = copyStringSlice(out[i].ExcludeTools)
		if out[i].Headers != nil {
			h := make(map[string]string, len(out[i].Headers))
			for k, v := range out[i].Headers {
				h[k] = v
			}
			out[i].Headers = h
		}
	}
	return out
}
