// Harness Phase 6b — client-side hook EXECUTION wiring on the closed path.
//
// buildHarnessHooksManager constructs real executable internal/hooks.Hook
// instances from a compiled *harness.Plan's plan.Hooks() (Phase 6a: harness
// only declares/resolves, never executes) and wires them into a purpose-built,
// minimal agent.HooksManager. It is the closed, execution counterpart to
// harness_skills.go's static skill loading:
//
//   - NO internal/hooks/builtin (task-enforcement, sleep-blocker, auto-mode,
//     git-user-enforcement, protected-branch, recap, autogenskills lifecycle,
//     ...): those are the standard client's OPT-IN hooks and are structurally
//     unreachable from this file — harnessHooksManager has no field that could
//     ever hold one.
//   - Command text is revealed ONLY via plan.RevealHookCommand (never trusted
//     from a hash); script targets are re-resolved and re-verified for
//     containment within plan.RevealSkillBaseDir() (the SAME manifest-dir base
//     skills use) independently of harness's own compile-time check.
//   - A hook's child-process environment contains ONLY variables whose NAME is
//     in that hook's declared allowlist (HookSpec.Environment), intersected
//     against the ambient environment AT INVOCATION TIME — never at plan
//     compile/load time, never widened, never persisted.
//   - type == http has no safe executor in internal/hooks yet: fail closed
//     with a clear error rather than silently registering fewer hooks than
//     declared.
//
// When the plan declares zero hooks (or every declared hook is explicitly
// disabled, or every declared hook's interface/agent scope excludes THIS
// construction), buildHarnessHooksManager returns (nil, false, nil) and the
// caller (initHarnessAgent) does not call a.SetHooksManager at all — behavior
// is byte-for-byte unchanged, mirroring harness_skills.go's zero-skills
// invariant.
//
// NOTE on internal/hooks.Manager: an earlier version of this file routed
// through hooks.NewManager/Register/EmitWithResult (the SDK's general hook
// registry). That was abandoned: Manager.getApplicableHooks caches its
// filtered hook list per event-type+ConversationID+ModeID (manager.go,
// buildCacheKey) — a cache key that excludes event.Data entirely. A
// scope == "tool" hook's Filter depends on event.Data["tool_name"], which
// varies per call for the SAME event type under the SAME (constant, harness
// closed-path) conversation/mode — so Manager would silently reuse a stale
// filtered result computed from the FIRST tool name seen, incorrectly
// admitting or excluding hooks for every later, different tool. This file
// instead does its own cache-free dispatch (harnessHooksManager.emit) over a
// small, statically-resolved slice of harness-declared hooks — simpler, and
// correct for a per-event, data-dependent Filter.
package client

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// hooksManagerIface is the exact surface initHarnessAgent needs: the same
// interface a.SetHooksManager accepts. Named locally purely for readability at
// the call site; it is a plain alias, not a new type.
type hooksManagerIface = agent.HooksManager

// harnessDefaultHookTimeout mirrors harness.defaultHookTimeoutSeconds (30s);
// resolveHooks always resolves a positive TimeoutSeconds on a valid compiled
// plan, so this is a defensive fallback only, never the normal path.
const harnessDefaultHookTimeout = 30 * time.Second

// buildHarnessHooksManager loads the plan's declared hooks into a fresh,
// minimal agent.HooksManager. It returns:
//
//   - (nil, false, nil) when the plan declares zero hooks, OR every declared
//     hook is explicitly enabled: false, OR every declared hook's
//     interface/agent scope excludes this specific construction (present in
//     plan.Hooks(), but nothing ever registered — observably identical to a
//     hookless plan);
//   - (mgr, true, nil) when at least one hook is active;
//   - (nil, false, err) fail-closed on any resolution/integrity/scope error,
//     including an unsupported type == http hook (no executor exists yet).
//
// interfaceID identifies the CURRENT client interface (e.g. the resolved
// client.ClientType — "tui"/"headless"/"managed"/"sdk") and is compared
// against a scope == "interface" hook's Matcher. A scope == "agent" hook's
// Matcher is compared against the compiled plan's OWN declared identity,
// plan.Name() (metadata.name) — the only agent identity a manifest author
// controls at write time; the runtime agent.Definition.ID is a freshly
// generated UUID unknown until construction and would make scope: agent
// unwritable in practice, so it is deliberately NOT used here. Both
// comparisons are STATIC / build-time: interfaceID and the plan's agentID are
// each constant for the entire lifetime of one harnessHooksManager (one
// manager == one compiled plan == one client construction), so whether an
// interface/agent-scoped hook is reachable at all is decided once, here.
func buildHarnessHooksManager(plan *harness.Plan, logger observability.Logger, interfaceID string) (hooksManagerIface, bool, error) {
	if plan == nil {
		return nil, false, fmt.Errorf("harness: buildHarnessHooksManager: nil plan")
	}
	specs := plan.Hooks()
	if len(specs) == 0 {
		return nil, false, nil
	}

	agentID := plan.Name()
	base := plan.RevealSkillBaseDir()
	var realBase string
	if base != "" {
		if rb, err := filepath.EvalSymlinks(base); err == nil {
			realBase = rb
		} else {
			realBase = filepath.Clean(base)
		}
	}

	var active []*harnessCommandHook
	for _, spec := range specs {
		if !spec.Enabled {
			// Explicitly disabled: a normal, valid state (mirrors
			// HookDefinition.Enabled semantics elsewhere in the SDK). Present
			// on plan.Hooks() but never registered, so it can never fire.
			continue
		}

		applies, aerr := harnessHookApplies(spec, interfaceID, agentID)
		if aerr != nil {
			return nil, false, aerr
		}
		if !applies {
			// scope == interface/agent but the matcher does not identify THIS
			// construction: skip resolution entirely (not an error — mirrors
			// the disabled-hook skip above) so an unrelated hook's bad
			// script/command can never abort a build it does not even apply to.
			continue
		}

		command, cerr := resolveHarnessHookCommand(plan, base, realBase, spec)
		if cerr != nil {
			return nil, false, cerr
		}

		var toolMatcher *regexp.Regexp
		if spec.Scope == harness.HookScopeTool && spec.Matcher != "" && spec.Matcher != "*" {
			re, rerr := regexp.Compile("^(" + spec.Matcher + ")$")
			if rerr != nil {
				return nil, false, fmt.Errorf("harness: hook %q: invalid tool matcher %q: %w", spec.ID, spec.Matcher, rerr)
			}
			toolMatcher = re
		}

		timeout := time.Duration(spec.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = harnessDefaultHookTimeout
		}

		active = append(active, &harnessCommandHook{
			id:           spec.ID,
			command:      command,
			eventPattern: spec.Event,
			toolMatcher:  toolMatcher,
			priority:     spec.Priority,
			timeout:      timeout,
			env:          append([]string(nil), spec.Environment...),
		})
	}

	if len(active) == 0 {
		return nil, false, nil
	}

	// Stable priority-descending order, computed once: the active set never
	// changes over this manager's lifetime, so emit() need not re-sort per call.
	sort.SliceStable(active, func(i, j int) bool { return active[i].priority > active[j].priority })

	return &harnessHooksManager{hooks: active, logger: logger}, true, nil
}

// harnessHookApplies performs STATIC, build-time inclusion filtering for
// scope == "interface" / "agent" hooks (see buildHarnessHooksManager doc for
// why this is safe to decide once). scope == "global" and scope == "tool"
// always apply here; "tool" is narrowed dynamically per-event instead, by
// harnessCommandHook.Filter's ToolMatcher — a tool name is a PER-EVENT
// property (it varies within a single agent run), unlike an interface/agent
// identity which is fixed for this manager's whole lifetime.
func harnessHookApplies(spec harness.HookSpec, interfaceID, agentID string) (bool, error) {
	switch spec.Scope {
	case harness.HookScopeGlobal, harness.HookScopeTool:
		return true, nil
	case harness.HookScopeInterface:
		return spec.Matcher == interfaceID, nil
	case harness.HookScopeAgent:
		return spec.Matcher == agentID, nil
	default:
		return false, fmt.Errorf("harness: hook %q: unknown scope %q", spec.ID, spec.Scope)
	}
}

// resolveHarnessHookCommand resolves the executable command text for one
// active HookSpec, fail-closed. type == command reveals the tainted inline
// text via plan.RevealHookCommand (never trusts a hash); type == script
// re-resolves + re-verifies containment and content hash within the manifest
// base, independently of harness's own compile-time check (Phase 6a); type ==
// http is not yet supported on the closed client path (no safe executor
// exists in internal/hooks) and fails closed rather than being silently
// dropped.
func resolveHarnessHookCommand(plan *harness.Plan, base, realBase string, spec harness.HookSpec) (string, error) {
	switch spec.Type {
	case harness.HookTypeCommand:
		cmd, ok := plan.RevealHookCommand(spec.ID)
		if !ok {
			return "", fmt.Errorf("harness: hook %q: type=command has no resolved command text", spec.ID)
		}
		return cmd, nil

	case harness.HookTypeScript:
		if base == "" {
			return "", fmt.Errorf("harness: hook %q: type=script but plan has no manifest base directory", spec.ID)
		}
		abs, rerr := resolveHarnessHookScript(base, realBase, spec.Path, spec.ID)
		if rerr != nil {
			return "", rerr
		}
		if spec.ContentHash != "" {
			got, herr := hashHarnessSkillFile(abs)
			if herr != nil {
				return "", fmt.Errorf("harness: hook %q: %w", spec.ID, herr)
			}
			if got != spec.ContentHash {
				return "", fmt.Errorf(
					"harness: hook %q content hash mismatch (compiled %s, on-disk %s); refusing to load",
					spec.ID, spec.ContentHash, got)
			}
		}
		return abs, nil

	case harness.HookTypeHTTP:
		// No net/http-backed hooks.Hook executor exists in internal/hooks
		// (verified: shell_hook.go is the sole concrete Hook implementation
		// available to client code; there is no HTTPHook/WebhookHook). Rather
		// than silently registering fewer hooks than the plan declares, fail
		// the ENTIRE construction atomically — mirrors the Phase 6a
		// instruction-vs-executable deferral discipline: honest, not silent.
		return "", fmt.Errorf("harness: hook %q: type=http is not yet supported on the closed client path", spec.ID)

	default:
		return "", fmt.Errorf("harness: hook %q: unknown type %q", spec.ID, spec.Type)
	}
}

// resolveHarnessHookScript resolves a type==script HookSpec.Path against
// plan.RevealSkillBaseDir() — the SAME manifest base directory skills use
// (hooks and skills share one trusted root; Phase 6a) — and RE-VERIFIES
// containment independently of harness's own resolveContainedFile check
// (harness/hooks.go), rather than trusting it. Mirrors
// resolveContainedSkillPath (harness_skills.go, Phase 5b) but a hook script
// target must be a regular, readable FILE (not a SKILL.md-bearing directory).
func resolveHarnessHookScript(base, realBase, rel, id string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("harness: hook %q: type=script but path is empty", id)
	}

	var joined string
	if filepath.IsAbs(rel) {
		joined = filepath.Clean(rel)
	} else {
		joined = filepath.Clean(filepath.Join(base, rel))
	}

	// Pre-symlink containment: reject "../" escapes early.
	if skillEscapesDir(realBase, joined) {
		return "", fmt.Errorf("harness: hook %q script path escapes the manifest directory", id)
	}

	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("harness: hook %q script does not exist: %s", id, rel)
		}
		return "", fmt.Errorf("harness: hook %q script could not be resolved: %w", id, err)
	}

	// Post-symlink containment: the real target must also stay within the subtree.
	if skillEscapesDir(realBase, real) {
		return "", fmt.Errorf("harness: hook %q script resolves (via symlink) outside the manifest directory", id)
	}

	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("harness: hook %q script could not be read: %w", id, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("harness: hook %q script path must reference a file, not a directory", id)
	}

	// Confirm actual readability (permission bits alone can lie under ACLs).
	f, ferr := os.Open(real)
	if ferr != nil {
		return "", fmt.Errorf("harness: hook %q script could not be read: %w", id, ferr)
	}
	_ = f.Close()

	return real, nil
}

// ─── harnessCommandHook ─────────────────────────────────────────────────────

// harnessCommandHook is a minimal hooks.Hook for the CLOSED harness path. It
// deliberately does NOT reuse internal/hooks.ShellHook: ShellHook.OnEvent
// (shell_hook.go, buildEnvironment) unconditionally seeds the child process
// environment from the FULL os.Environ(), plus its own baseline additions,
// with no public override point — reusing it would defeat the ENV ALLOWLIST
// DISCIPLINE hard invariant for every harness-declared command/script hook.
// harnessCommandHook instead builds the child environment from EXACTLY the
// hook's declared allowlist (names only), intersected against the ambient
// environment at invocation time — nothing more, no baseline additions.
type harnessCommandHook struct {
	id           string
	command      string         // resolved inline text OR resolved absolute script path.
	eventPattern string         // exact event string this hook fires on (harness Event is free-form; no wildcard support here).
	toolMatcher  *regexp.Regexp // non-nil only for scope == "tool".
	priority     int
	timeout      time.Duration
	env          []string // ALLOWLIST of env var NAMES only — never values, never persisted.
}

func (h *harnessCommandHook) Name() string  { return h.id }
func (h *harnessCommandHook) Priority() int { return h.priority }

// Filter matches the exact declared event string and, for scope == "tool",
// the tool name carried on the event's Data["tool_name"].
func (h *harnessCommandHook) Filter(event hooks.Event) bool {
	if event.Type != h.eventPattern {
		return false
	}
	if h.toolMatcher != nil {
		toolName, _ := event.Data["tool_name"].(string)
		if !h.toolMatcher.MatchString(toolName) {
			return false
		}
	}
	return true
}

// OnEvent runs the resolved command via "sh -c" with a child environment
// limited to EXACTLY the declared allowlist, intersected against os.Environ()
// AT INVOCATION TIME (never at plan compile/load time, never persisted).
// Exit code 2 blocks (mirrors the standard client's ShellHook "block_exit2"
// default for consistency); any other exit continues, surfacing stdout as a
// non-blocking message.
func (h *harnessCommandHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	timeout := h.timeout
	if timeout <= 0 {
		timeout = harnessDefaultHookTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", h.command)
	cmd.Env = h.buildAllowlistedEnv()

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	errOut := strings.TrimSpace(stderr.String())

	if execCtx.Err() == context.DeadlineExceeded {
		return hooks.ContinueWithMessage(fmt.Sprintf("harness hook %s timed out after %s", h.id, timeout)), nil
	}

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return hooks.Continue(), fmt.Errorf("harness hook %s: %w", h.id, runErr)
		}
	}

	if exitCode == 2 {
		reason := errOut
		if reason == "" {
			reason = out
		}
		if reason == "" {
			reason = fmt.Sprintf("harness hook %s blocked execution", h.id)
		}
		return hooks.Block(reason), nil
	}
	if out != "" {
		return hooks.ContinueWithMessage(out), nil
	}
	return hooks.Continue(), nil
}

// buildAllowlistedEnv intersects the ambient process environment against this
// hook's declared allowlist (names only) AT INVOCATION TIME. No value is ever
// read at plan-compile time or stored persistently — only h.env (names,
// sourced from HookSpec.Environment, harness/hooks.go) is carried on the hook.
// Always returns a non-nil slice (even when empty): exec.Cmd treats a nil Env
// as "inherit the current process's FULL environment", which would silently
// defeat the allowlist for a hook declaring no environment names at all.
func (h *harnessCommandHook) buildAllowlistedEnv() []string {
	out := make([]string, 0, len(h.env))
	for _, name := range h.env {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// ─── harnessHooksManager ────────────────────────────────────────────────────

// harnessHooksManager is a minimal, PURPOSE-BUILT agent.HooksManager for the
// CLOSED harness path. Unlike clientHooksManager (client.go), it has NO
// fields for task-enforcement/sleep-blocker/auto-mode/recap/
// git-user-enforcement/protected-branch/autogenskills-lifecycle — those
// opt-in standard-client hooks are structurally unreachable here because this
// type simply has no slot to ever hold one. It only ever wraps a slice of
// harnessCommandHook built exclusively from plan.Hooks() by
// buildHarnessHooksManager. This is the strictest available choice: reusing
// clientHooksManager (even with only .extra populated) would still carry the
// dead-but-present taskEnforcement/sleepBlocker/autoMode/recap/
// gitUserEnforcement/protectedBranch fields on the same type used by the
// standard opt-in client path, one accidental future edit away from being
// wired on the closed path too.
type harnessHooksManager struct {
	hooks  []*harnessCommandHook // priority-descending, fixed at construction.
	logger observability.Logger
}

func (m *harnessHooksManager) EmitToolBeforeExecute(ctx context.Context, toolName string, params map[string]any) ([]agent.HookResult, error) {
	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		// Identity from ctx (tools.WithOwnerInfo in internal/agent/agent_tools.go);
		// previously dropped, which made harness command hooks un-attributable.
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data: map[string]any{
			"tool_name":  toolName,
			"params":     params,
			"tool_input": params,
		},
	}
	return m.emit(ctx, event)
}

func (m *harnessHooksManager) EmitToolAfterExecute(ctx context.Context, toolName string, params map[string]any, result any, execErr error) []agent.HookResult {
	data := map[string]any{
		"tool_name":  toolName,
		"params":     params,
		"tool_input": params,
		"result":     result,
	}
	if execErr != nil {
		data["error"] = execErr
	}
	event := hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data:           data,
		// Typed structured outcome (G4) — see internal/toolout. Nil when the
		// tool does not report one.
		ToolOutcome: tools.NamedOutcomeOf(toolName, result),
	}
	results, _ := m.emit(ctx, event)
	return results
}

// EmitProviderResponse is a no-op: the closed harness path does not surface a
// provider.after_response event through plan.Hooks() in Phase 6b — only
// tool.before_execute / tool.after_execute route through this manager. A hook
// declaring event: "provider.after_response" is registered (harness does not
// validate the event enum; see harness/hooks.go) but simply never fires until
// a future phase adds this Emit call site — honest, not silent, mirroring the
// type == http deferral above.
func (m *harnessHooksManager) EmitProviderResponse(context.Context, string, string, int, int, int64) {
}

// emit runs every registered hook whose Filter(event) matches, in the fixed
// priority-descending order computed at construction, stopping at the first
// ActionBlock — mirroring internal/hooks.Executor.Execute's semantics. See
// the file doc for why this does NOT route through internal/hooks.Manager.
func (m *harnessHooksManager) emit(ctx context.Context, event hooks.Event) ([]agent.HookResult, error) {
	var out []agent.HookResult
	for _, h := range m.hooks {
		if !h.Filter(event) {
			continue
		}
		res, err := h.OnEvent(ctx, event)
		if err != nil {
			if m.logger != nil {
				m.logger.Warn(ctx, "harness.hook_error",
					observability.F("hook", h.Name()), observability.F("error", err.Error()))
			}
			out = append(out, agent.HookResult{HookName: h.Name(), Error: err.Error()})
			return out, err
		}
		switch res.Action {
		case hooks.ActionBlock:
			out = append(out, agent.HookResult{HookName: h.Name(), Output: res.Message, Blocked: true})
			return out, fmt.Errorf("blocked by %s: %s", h.Name(), res.Message)
		default:
			if res.Message != "" {
				out = append(out, agent.HookResult{HookName: h.Name(), Output: res.Message, Success: true})
			}
		}
	}
	return out, nil
}
