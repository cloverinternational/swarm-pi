// Harness Phase 7b — client-side MCP EXECUTION wiring on the closed path.
//
// buildHarnessMCPManager connects to the MCP servers a compiled *harness.Plan
// declared (harness/mcp.go, Phase 7a: harness only declares/resolves,
// NEVER connects) and registers their tools into the SAME closed tool
// registry the harness path already builds for its static catalog tools
// (harness_plan.go) and Skill tool (harness_skills.go, Phase 5b). It is the
// closed, execution counterpart to Phase 7a, mirroring how harness_hooks.go
// (Phase 6b) is the execution counterpart to harness/hooks.go (Phase 6a):
//
//   - CLOSED CONFIG SOURCE: an internal/mcp.ConfigManager is built over an
//     ISOLATED, freshly-created scratch directory (os.MkdirTemp) with an
//     EMPTY workspaceRoot ("") — so ConfigManager.ProjectPath() returns ""
//     and ConfigManager.GlobalPath() points at a directory guaranteed to
//     contain zero pre-existing files. The real `~/.swarmos` config
//     directory and the real project workspace's `.swarmos/mcp_servers.json`
//     are NEVER read on this path — the ONLY servers the resulting
//     RuntimeManager ever sees are injected in-memory via
//     ConfigManager.SetPluginServers, one PluginServerEntry per plan-declared,
//     enabled McpServerSpec.
//   - ENV/HEADER RESOLUTION AT INVOCATION TIME: only environment variable
//     NAMES present in a spec's own Environment allowlist (Phase 7a,
//     compile-time validated) are ever read from the ambient process
//     environment, exactly once, here — via os.LookupEnv — and only their
//     resolved VALUES flow into the ephemeral ServerConfig handed to
//     internal/mcp. An allowlisted name that is unset in the ambient
//     environment is OMITTED (not an error: env absence is common and
//     shouldn't block an otherwise-valid config, matching the "best effort
//     connectivity" spirit of this whole layer) with a debug log naming the
//     var (never its value — there is no value to log). A header value is a
//     REFERENCE to an allowlisted name (Phase 7a already rejected any header
//     value that is not a declared allowlist name); the resolved header
//     carries the referenced name's resolved value, or is omitted the same
//     way when that name is unset.
//   - WORKDIR FAIL-CLOSED: a stdio server's WorkDir is re-resolved against
//     plan.RevealSkillBaseDir() (the SAME manifest-dir base skills/hooks use)
//     and containment is RE-VERIFIED independently of harness's own
//     compile-time check (Phase 7a) — mirrors resolveHarnessHookScript
//     (harness_hooks.go, Phase 6b) exactly, except the target must be a
//     DIRECTORY, not a file. Escape/missing/non-directory fails the ENTIRE
//     construction atomically, before any RuntimeManager is built.
//   - LIVE CONNECTIVITY IS BEST-EFFORT: internal/mcp.RuntimeManager.Start
//     only resolves configuration (ConfigManager.Resolve, a local,
//     file-existence-only operation against the isolated scratch dir — see
//     above) and then launches one background goroutine PER enabled server
//     that dials and retries with backoff; Start itself never blocks on or
//     fails because of a live connection attempt (internal/mcp/
//     runtime_manager.go, RuntimeManager.runServer). A server that can never
//     be reached (bad URL, missing binary, network down) therefore degrades
//     gracefully — exactly like the standard client's initMCPManager
//     (client.go) — and never aborts harness agent construction. Only the
//     CONFIG/RESOLUTION problems above (workDir escape/missing) are
//     fail-closed, and they are checked BEFORE any RuntimeManager exists.
//   - enabled: false servers are present on plan.MCPServers() (Phase 7a
//     preserves them for Explain) but are simply never turned into a
//     PluginServerEntry here, so they can never be registered or started —
//     mirrors the disabled-hook skip in buildHarnessHooksManager.
//
// Phase 7c CLOSES the exposure gap 7b documented: initHarnessAgent still
// computes an EXACT, static agent.Definition.ToolHints list (the plan's
// selected catalog tools plus the Skill tool name) BEFORE the agent is
// constructed, but harnessMCPExposure (below) now RECOMPUTES that list
// transactionally as plan-declared servers finish registering their tools, so
// a declared MCP tool becomes genuinely model-usable instead of merely
// registered. See the "PHASE 7c" banner below for the recompute contract and
// the stated tool-name collision policy.
package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// buildHarnessMCPManager loads the plan's declared, enabled MCP servers into
// a fresh, closed-posture internal/mcp.RuntimeManager and starts it. It
// returns:
//
//   - (nil, false, nil) when the plan declares zero mcp entries, OR every
//     declared entry is explicitly enabled: false (present on
//     plan.MCPServers(), but nothing ever registered/started — observably
//     identical to an mcp-less plan);
//   - (mgr, true, nil) when at least one server was handed to the runtime
//     manager (regardless of whether it ever successfully CONNECTS — see the
//     file doc's best-effort-connectivity note);
//   - (nil, false, err) fail-closed on a CONFIG/RESOLUTION problem: a
//     nil plan, or a stdio server's workDir failing containment/existence
//     re-verification. This error class is checked entirely BEFORE any
//     RuntimeManager is constructed, so a rejection here leaves nothing
//     running that would need to be torn down.
//
// statusHandler is OPTIONAL (variadic purely so every existing call site keeps
// compiling unchanged — this parameter is additive, never required). When a
// non-nil handler is supplied it is installed via SetStatusHandler STRICTLY
// BEFORE Start, which matters for Phase 7c exposure: Start immediately spawns
// the per-server dial goroutines, and a server that connects and registers its
// tools quickly would otherwise emit its post-registration status BEFORE the
// handler existed and never be exposed (the following emit could be many
// seconds later, or — for a connection that simply stays healthy — never).
// Installing first makes "registration completed" strictly imply "recompute
// ran".
func buildHarnessMCPManager(
	ctx context.Context,
	plan *harness.Plan,
	toolReg tools.Registry,
	logger observability.Logger,
	tracer observability.Tracer,
	statusHandler ...func(mcp.ServerStatusRecord),
) (*mcp.RuntimeManager, bool, error) {
	if plan == nil {
		return nil, false, fmt.Errorf("harness: buildHarnessMCPManager: nil plan")
	}
	specs := plan.MCPServers()
	if len(specs) == 0 {
		return nil, false, nil
	}

	base := plan.RevealSkillBaseDir()
	var realBase string
	if base != "" {
		if rb, err := filepath.EvalSymlinks(base); err == nil {
			realBase = rb
		} else {
			realBase = filepath.Clean(base)
		}
	}

	var entries []mcp.PluginServerEntry
	for _, spec := range specs {
		if !spec.Enabled {
			// Explicitly disabled: a normal, valid state (mirrors
			// McpServerSpec.Enabled semantics elsewhere). Present on
			// plan.MCPServers() but never turned into a PluginServerEntry, so
			// it can never be registered or started.
			continue
		}

		var workDir string
		if spec.WorkDir != "" {
			resolved, werr := resolveHarnessMCPWorkDir(base, realBase, spec.WorkDir, spec.ID)
			if werr != nil {
				return nil, false, werr
			}
			workDir = resolved
		}

		env := harnessMCPAllowlistedEnv(ctx, logger, spec.ID, spec.Environment)
		headers := harnessMCPResolvedHeaders(spec.Headers, env)

		sc := mcp.ServerConfig{
			Name:       spec.ID,
			Type:       spec.Type,
			Enabled:    true,
			Command:    spec.Command,
			Args:       append([]string(nil), spec.Args...),
			WorkingDir: workDir,
			URL:        spec.URL,
			TimeoutSec: spec.TimeoutSeconds,
		}
		if len(env) > 0 {
			sc.Env = env
		}
		if len(headers) > 0 {
			sc.Headers = headers
		}
		if tc := harnessMCPToolsConfig(spec); tc != nil {
			sc.Tools = tc
		}

		entries = append(entries, mcp.PluginServerEntry{
			Plugin: mcp.PluginInfo{Name: "harness:" + plan.Name(), Enabled: true},
			Server: sc,
		})
	}

	if len(entries) == 0 {
		// Every declared server was explicitly disabled: nothing to start,
		// behavior stays byte-identical to an mcp-less plan.
		return nil, false, nil
	}

	// ISOLATED scratch config dir: guaranteed to contain zero pre-existing
	// files (os.MkdirTemp always creates a brand-new, empty directory), and
	// workspaceRoot is deliberately "" so ConfigManager.ProjectPath() returns
	// "" too — the real `~/.swarmos` and the real project workspace's
	// `.swarmos/mcp_servers.json` are structurally unreachable from this
	// ConfigManager. Removed immediately after Start (which is the only
	// call that ever reads it, via Resolve/LoadLayered — see file doc);
	// nothing else in this closed path calls a method that writes to it.
	scratchDir, derr := os.MkdirTemp("", "harness-mcp-")
	if derr != nil {
		return nil, false, fmt.Errorf("harness: mcp scratch config dir: %w", derr)
	}
	defer os.RemoveAll(scratchDir)

	cfgMgr := mcp.NewConfigManager(scratchDir, "", nil, nil)
	cfgMgr.SetPluginServers(entries)

	mgr := mcp.NewRuntimeManager(cfgMgr, toolReg, nil, logger, tracer)
	for _, h := range statusHandler {
		if h != nil {
			mgr.SetStatusHandler(h)
		}
	}
	if err := mgr.Start(ctx); err != nil {
		// Start only fails on a ConfigManager.Resolve error (local file I/O
		// against the isolated scratch dir) — a config-layer problem, not a
		// live-connectivity one. Individual server connection attempts run in
		// background goroutines Start launches and never propagate here.
		return nil, false, fmt.Errorf("harness: mcp start: %w", err)
	}

	return mgr, true, nil
}

// harnessMCPToolsConfig translates a spec's Tools/ExcludeTools allowlist
// (Phase 7a already rejected declaring both) into the internal/mcp
// ToolsConfig shape, or nil when neither was declared (meaning: all of this
// server's tools, the internal/mcp default).
func harnessMCPToolsConfig(spec harness.McpServerSpec) *mcp.ToolsConfig {
	switch {
	case len(spec.Tools) > 0:
		return &mcp.ToolsConfig{Mode: "allowlist", Enabled: append([]string(nil), spec.Tools...)}
	case len(spec.ExcludeTools) > 0:
		return &mcp.ToolsConfig{Mode: "blocklist", Disabled: append([]string(nil), spec.ExcludeTools...)}
	default:
		return nil
	}
}

// harnessMCPAllowlistedEnv resolves ONLY the names in allowlist against the
// ambient process environment, AT INVOCATION TIME (never at plan
// compile/load time, never persisted anywhere but the ephemeral map
// returned here). A name that is allowlisted but unset in the ambient
// environment is OMITTED (not an error) with a debug log naming ONLY the
// var name — never a value, because there is no value to log for an unset
// var, and a set var's resolved value is never logged either.
func harnessMCPAllowlistedEnv(ctx context.Context, logger observability.Logger, serverID string, allowlist []string) map[string]string {
	if len(allowlist) == 0 {
		return nil
	}
	out := make(map[string]string, len(allowlist))
	for _, name := range allowlist {
		if v, ok := os.LookupEnv(name); ok {
			out[name] = v
			continue
		}
		if logger != nil {
			logger.Debug(ctx, "harness.mcp.env_unset",
				observability.F("server", serverID), observability.F("name", name))
		}
	}
	return out
}

// harnessMCPResolvedHeaders resolves a spec's header-name -> allowlisted-env-
// name reference map (Phase 7a validated every value is a declared allowlist
// name) against the already-resolved env values map, omitting a header
// whose referenced name was omitted (unset) above.
func harnessMCPResolvedHeaders(headerRefs map[string]string, resolvedEnv map[string]string) map[string]string {
	if len(headerRefs) == 0 {
		return nil
	}
	out := make(map[string]string, len(headerRefs))
	for headerName, envRef := range headerRefs {
		if v, ok := resolvedEnv[envRef]; ok {
			out[headerName] = v
		}
	}
	return out
}

// resolveHarnessMCPWorkDir resolves a stdio McpServerSpec.WorkDir against
// plan.RevealSkillBaseDir() (the SAME manifest base directory skills/hooks
// use; hooks/skills/mcp share one trusted root) and RE-VERIFIES containment
// independently of harness's own compile-time check (harness/mcp.go, Phase
// 7a), rather than trusting it — mirrors resolveHarnessHookScript
// (harness_hooks.go, Phase 6b) exactly, except the target here must be a
// DIRECTORY (a spawned process's working directory), not a file.
func resolveHarnessMCPWorkDir(base, realBase, rel, id string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("harness: mcp server %q declares workDir but plan has no manifest base directory", id)
	}

	var joined string
	if filepath.IsAbs(rel) {
		joined = filepath.Clean(rel)
	} else {
		joined = filepath.Clean(filepath.Join(base, rel))
	}

	// Pre-symlink containment: reject "../" escapes early.
	if skillEscapesDir(realBase, joined) {
		return "", fmt.Errorf("harness: mcp server %q workDir escapes the manifest directory", id)
	}

	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("harness: mcp server %q workDir does not exist: %s", id, rel)
		}
		return "", fmt.Errorf("harness: mcp server %q workDir could not be resolved: %w", id, err)
	}

	// Post-symlink containment: the real target must also stay within the subtree.
	if skillEscapesDir(realBase, real) {
		return "", fmt.Errorf("harness: mcp server %q workDir resolves (via symlink) outside the manifest directory", id)
	}

	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("harness: mcp server %q workDir could not be read: %w", id, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("harness: mcp server %q workDir must reference a directory", id)
	}

	return real, nil
}

// ===========================================================================
// PHASE 7c — MCP TOOL EXPOSURE: TRANSACTIONAL RECOMPUTE + COLLISION POLICY
// ===========================================================================
//
// WHY: provider visibility on the closed harness path is gated ENTIRELY by
// agent.Definition.ToolHints. buildProviderTools (internal/agent/
// agent_execute_filter.go) never enumerates the registry while hints are
// non-empty; it iterates the hint list and calls toolReg.Get(name) per hint.
// Phase 7b registered plan-declared MCP tools into that registry but left the
// static hint list alone, so the model could never call them. 7c recomputes
// the hint list as servers come online.
//
// THE EXPOSED SET IS DEFINED AS:
//
//	staticHints (the plan's selected catalog tools + the Skill tool, exactly as
//	Phase 7b computed them, never re-derived and never dropped)
//	  UNION
//	every registry entry that ALL of the following hold for:
//	  1. its name carries the `mcp_` namespace prefix, AND
//	  2. its registration metadata declares Source == tools.ToolSourceMCP, AND
//	  3. metadata.ServerName is a server THIS PLAN declared with enabled: true, AND
//	  4. its registration is ENABLED (which is exactly where the per-server
//	     tools/excludeTools filter lands — see ALLOW/BLOCK below).
//
// (2)+(3) are why exposure is PROVENANCE-checked rather than name-pattern
// checked: a tool is exposed as an MCP tool only because the registry itself
// records that an MCP server for a plan-declared ID registered it. No ambient
// or undeclared server's tools can be attributed to this plan, and a
// coincidental `mcp_`-shaped catalog tool name cannot be reclassified.
//
// ALLOW/BLOCK (roadmap #6) — ENFORCED AT REGISTRATION, ASSERTED AT EXPOSURE.
// The per-spec tools/excludeTools lists (Phase 7a) are translated by
// harnessMCPToolsConfig into mcp.ToolsConfig and handed to internal/mcp, whose
// registerTools registers every discovered tool and then applyToolConfig
// Enable/Disables each one per shouldEnableTool. A DISABLED registration is
// excluded from SimpleRegistry.List() and makes Get() return
// registry.tool_disabled — so a filtered-out tool is ALREADY neither exposable
// nor executable, and condition (4) above simply refuses to re-widen it. We do
// NOT duplicate the filter predicate here (duplication could drift from
// internal/mcp's semantics); the exposure tests assert the end-to-end result.
//
// ORDERING (roadmap #6, "before the first provider request") — the recompute
// trigger is RuntimeManager.SetStatusHandler, installed BEFORE Start (see
// buildHarnessMCPManager). That specific hook is chosen deliberately: within
// runServer, tool registration is followed by applyToolConfig (which emits
// status only AFTER the allow/block Enable/Disable pass) and then by
// setStatus("connected") (which emits again). BOTH emissions therefore happen
// strictly after filtering, so a recompute can never observe the window inside
// registerTools where a to-be-blocked tool is momentarily enabled. An async
// tools.ToolChangeListener on the registry would fire INSIDE that window and is
// deliberately NOT used.
//
// BIDIRECTIONAL — the same handler covers removal, but only on the paths where a
// status emit FOLLOWS the unregistration. unregisterTools emits BEFORE it
// unregisters, so the hint list shrinks on the *next* emit: monitorConnection
// (setStatus "error", connection lost) and Disconnect both emit after
// unregisterTools returns, so hints shrink promptly there. Stop() does NOT emit
// afterwards, so a stopped manager can leave the last hint list naming tools
// that are already gone.
//
// That residue is harmless because removal is SELF-CORRECTING regardless of hint
// staleness: because buildProviderTools resolves every hint through
// toolReg.Get, a hint naming an unregistered or disabled tool contributes
// NOTHING to the provider payload. The effective provider-visible set is always
// hints ∩ registry(enabled), so exposure can never outlive the tool itself.
//
// TRANSACTIONALITY (roadmap #7) — recompute builds a COMPLETE new hint slice
// off to the side and installs it with ONE agent.SetToolHints call, which swaps
// the whole slice under the agent's write lock while readers snapshot it under
// the read lock. Readers see the complete old list or the complete new one,
// never a partially-updated set; the live slice is never mutated in place. A
// package-level mutex serialises concurrent recomputes (the status handler runs
// on RuntimeManager's background goroutines, one per server) so the snapshot →
// build → swap sequence is atomic with respect to another recompute.
//
// LOCK ORDERING — recompute takes (1) its own mutex, then (2) registry read
// locks (via List/Registration, each released before returning), then (3) the
// agent write lock (inside SetToolHints). It NEVER calls back into the MCP
// RuntimeManager, and the agent never calls into this code, so there is NO
// CYCLE and no deadlock.
//
// Be precise about what that does and does not guarantee: the manager's write
// lock IS held across a handler invocation in one direction — RuntimeManager.
// Stop() holds m.mu for its whole loop and calls unregisterTools → emitStatus →
// this handler → SetToolHints, so m.mu and the agent write lock are held
// together there. That is safe ONLY because no path acquires the agent lock and
// then reaches for a manager lock. Anyone adding a RuntimeManager call from an
// agent-locked path (or from this recompute) would close the cycle and create a
// real deadlock — don't.
//
// SAFETY INVARIANT PRESERVED — the resulting list is NEVER empty. Empty hints
// mean "all registry tools" in the agent filter, which would let the closed
// path enumerate the entire registry; harnessNoToolsSentinel exists precisely
// to prevent that, and computeHints re-establishes it on every path.
//
// ---------------------------------------------------------------------------
// TOOL-NAME COLLISION POLICY (roadmap #4) — STATED, NOT ACCIDENTAL
// ---------------------------------------------------------------------------
//
//	P1. NAMESPACING. Every MCP tool is registered under
//	    `mcp_<sanitizedServer>_<sanitizedTool>` (internal/mcp uniqueToolName).
//	    Catalog tools and the Skill tool are registered under their own runtime
//	    names, which never carry that prefix. An MCP tool therefore CANNOT
//	    occupy a trusted tool's registry key, and exposure additionally
//	    requires the prefix (harnessMCPToolNamePrefix), so a name-shadowing
//	    attempt cannot even be classified as an MCP tool here.
//
//	P2. PER-SERVER DISAMBIGUATION. Two different servers exposing the SAME
//	    upstream tool name yield DIFFERENT registry keys, because the server ID
//	    is part of the key. Both coexist and both are exposed; neither clobbers
//	    the other. Server IDs are unique per plan (Phase 7a rejects duplicates),
//	    so the key is unique per (server, tool).
//
//	P3. INCUMBENT WINS. If a namespaced name still collides with an existing
//	    registry entry (same server ID + tool name twice, a sanitizer
//	    collision, or a catalog tool contrived to be named `mcp_*`), the
//	    registry REFUSES the second Register with registry.duplicate_tool and
//	    leaves the incumbent's tool object untouched; internal/mcp logs
//	    mcp.tool_register_failed and continues. The trusted incumbent is never
//	    replaced, and because exposure reads the SURVIVING registration's
//	    provenance metadata, a rejected MCP tool cannot borrow the incumbent's
//	    identity: if the incumbent is a catalog tool its metadata is not
//	    ToolSourceMCP, so condition (2) fails and nothing new is exposed.
//
//	P4. NO SILENT SUBSTITUTION. Because exposure only ever ADDS `mcp_*` names
//	    to a baseline that always contains the full static catalog+Skill hint
//	    list, no recompute can remove or displace a trusted tool from the
//	    provider payload.

// harnessMCPToolNamePrefix is the namespace every MCP-registered tool key
// carries (internal/mcp uniqueToolName). Exposure REQUIRES it — see policy P1.
const harnessMCPToolNamePrefix = "mcp_"

// harnessMCPHintSetter is the narrow slice of *agent.Agent the recompute needs:
// an atomic whole-list swap of the provider-visible hints. Kept as an interface
// so the exposure logic is testable without a live provider.
type harnessMCPHintSetter interface {
	SetToolHints(hints []string)
}

// harnessMCPRegistrationInspector is the registry capability used to read a
// registered tool's provenance metadata and enabled state.
// *tools.SimpleRegistry (the only registry the closed path builds) implements
// it. A registry that does NOT implement it exposes ZERO MCP tools —
// fail-closed: without provenance we cannot prove a tool belongs to a declared
// server, and guessing from the name alone is exactly what policy P1 forbids.
type harnessMCPRegistrationInspector interface {
	Registration(name string) (*tools.ToolRegistration, bool)
}

// harnessMCPExposure recomputes the agent's provider-visible hint list as
// plan-declared MCP servers register and unregister tools. See the PHASE 7c
// banner above for the full contract.
type harnessMCPExposure struct {
	// staticHints is the IMMUTABLE Phase 7b baseline (selected catalog runtime
	// names + the Skill tool name, or the no-tools sentinel). Copied at
	// construction so a later mutation of the caller's slice cannot leak in,
	// and never mutated afterwards.
	staticHints []string

	// sentinelOnly records that the baseline is EXACTLY the no-tools sentinel,
	// i.e. the plan selected zero catalog tools and zero skills. In that case
	// the sentinel is dropped once at least one real MCP tool is exposed: it
	// exists only to keep the list non-empty, and keeping it would make the
	// agent log a spurious tool_not_found every turn.
	sentinelOnly bool

	// declared is the set of plan-declared server IDs with enabled: true. A
	// registry entry attributed to any other server name is NEVER exposed.
	declared map[string]struct{}

	reg    tools.Registry
	setter harnessMCPHintSetter
	logger observability.Logger

	// mu serialises snapshot → build → swap so two concurrent recomputes (one
	// per server goroutine) cannot interleave, and guards lastSet.
	mu sync.Mutex

	// lastSet is the most recently INSTALLED hint list, seeded with the static
	// baseline so that a recompute finding no MCP tools performs NO swap at
	// all — keeping a zero-MCP-tool run byte-identical to Phase 7b.
	lastSet []string
}

// newHarnessMCPExposure builds the recompute closure state. specs is the plan's
// FULL declared server list (harness.Plan.MCPServers()); disabled entries are
// skipped here exactly as buildHarnessMCPManager skips them, so an
// enabled: false server can contribute nothing even if something else somehow
// registered a tool under its name.
func newHarnessMCPExposure(
	staticHints []string,
	specs []harness.McpServerSpec,
	reg tools.Registry,
	setter harnessMCPHintSetter,
	logger observability.Logger,
) *harnessMCPExposure {
	base := append([]string(nil), staticHints...)
	declared := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if !spec.Enabled {
			continue
		}
		declared[spec.ID] = struct{}{}
	}
	return &harnessMCPExposure{
		staticHints:  base,
		sentinelOnly: len(base) == 1 && base[0] == harnessNoToolsSentinel,
		declared:     declared,
		reg:          reg,
		setter:       setter,
		logger:       logger,
		lastSet:      append([]string(nil), base...),
	}
}

// onServerStatus is the RuntimeManager.SetStatusHandler callback. It runs
// SYNCHRONOUSLY on the reporting server's background goroutine, which is what
// makes "this server finished registering (and filtering) its tools" imply
// "exposure has been recomputed" before that goroutine proceeds.
func (e *harnessMCPExposure) onServerStatus(ctx context.Context, rec mcp.ServerStatusRecord) {
	if e == nil {
		return
	}
	if _, ok := e.declared[rec.Name]; !ok {
		// Structurally impossible on the closed path (the RuntimeManager only
		// ever sees plan-declared, enabled servers), so this is a pure
		// belt-and-braces gate: an undeclared server can never even trigger a
		// recompute, let alone contribute a tool.
		return
	}
	e.recompute(ctx, rec.Name, rec.Status.State)
}

// recompute installs a freshly computed hint list, atomically. It is idempotent
// and cheap, and performs NO swap when the computed list is unchanged.
func (e *harnessMCPExposure) recompute(ctx context.Context, cause, state string) {
	if e == nil || e.setter == nil || e.reg == nil {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	next := e.computeHints()
	if slices.Equal(next, e.lastSet) {
		return
	}

	// ONE swap of a COMPLETE list. Never a partial update, never an in-place
	// mutation of a list a reader may be holding.
	e.setter.SetToolHints(next)
	e.lastSet = next

	if e.logger != nil {
		e.logger.Info(ctx, "harness.mcp.exposure_recomputed",
			observability.F("server", cause),
			observability.F("server_state", state),
			observability.F("exposed_count", len(next)),
			observability.F("exposed", next))
	}
}

// computeHints is the pure exposure function: static baseline UNION the
// eligible `mcp_*` names, de-duplicated, order-stable, and NEVER empty.
func (e *harnessMCPExposure) computeHints() []string {
	mcpNames := e.eligibleMCPToolNames()

	base := e.staticHints
	if len(mcpNames) > 0 && e.sentinelOnly {
		// The sentinel's only job is keeping the list non-empty; a real MCP
		// tool now does that job. Dropping it avoids a per-turn
		// agent.tool_not_found warning for a name that matches nothing.
		base = nil
	}

	next := make([]string, 0, len(base)+len(mcpNames))
	seen := make(map[string]struct{}, len(base)+len(mcpNames))
	for _, name := range base {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		next = append(next, name)
	}
	// MCP names are appended AFTER the static baseline and only when new, so a
	// trusted tool can never be displaced or duplicated (policy P4).
	for _, name := range mcpNames {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		next = append(next, name)
	}

	if len(next) == 0 {
		// Load-bearing SAFETY: empty hints mean "all registry tools" in the
		// agent filter. Unreachable given a non-empty baseline, kept as a hard
		// floor so no future edit can silently open the registry.
		return []string{harnessNoToolsSentinel}
	}
	return next
}

// eligibleMCPToolNames snapshots the registry and returns the sorted unique
// names of tools that pass ALL FOUR exposure conditions in the banner above.
// Registry.List() already excludes disabled and hidden tools; the explicit
// Enabled re-check is a deliberate belt-and-braces read of the same
// registration we are inspecting anyway.
func (e *harnessMCPExposure) eligibleMCPToolNames() []string {
	inspector, ok := e.reg.(harnessMCPRegistrationInspector)
	if !ok {
		// Fail-closed: no provenance => expose nothing (see the interface doc).
		return nil
	}

	names := e.reg.List()
	out := make([]string, 0, len(names))
	for _, name := range names {
		if !strings.HasPrefix(name, harnessMCPToolNamePrefix) {
			continue // policy P1
		}
		reg, found := inspector.Registration(name)
		if !found || reg == nil || !reg.Enabled || reg.Metadata == nil {
			continue
		}
		if reg.Metadata.Source != tools.ToolSourceMCP {
			continue // policy P3: an incumbent non-MCP tool keeps its identity
		}
		if _, declared := e.declared[reg.Metadata.ServerName]; !declared {
			continue // only plan-declared, enabled servers
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
