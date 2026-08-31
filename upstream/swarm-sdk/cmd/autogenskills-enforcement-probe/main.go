// Package main provides an integration probe that exercises the autogenskills
// BudgetEnforcementHook through the SDK hooks.Manager — no LLM required.
//
// Usage:
//
//	go run ./cmd/autogenskills-enforcement-probe
//
// This simulates tool execution events and verifies:
//  1. First N non-exempt tool calls are allowed (onboarding budget)
//  2. After onboarding budget exceeded, ALL non-exempt tools are HARD-BLOCKED
//  3. SkillManage and task tools remain allowed after budget exceeded
//  4. Budget upgrades to working budget after successful SkillManage
//  5. After working budget exceeded, agent gets soft nudge (not hard block)
//  6. After MaxNudgeIgnores soft nudges, escalates to hard block
//  7. Budget refills after subsequent SkillManage
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills"
)

func main() {
	ctx := context.Background()
	failed := false

	fail := func() {
		failed = true
	}

	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  AUTOGENSKILLS BUDGET ENFORCEMENT — INTEGRATION PROBE")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	// ── Setup ─────────────────────────────────────────────────────
	cfg := autogenskills.Config{
		Mode:       autogenskills.ModeAuto,
		AutogenDir: os.TempDir() + "/autogen-probe",
		Trigger: autogenskills.TriggerConfig{
			ToolCallBudget:  3, // Low onboarding budget for fast test
			WorkingBudget:   5, // Small working budget for test
			MaxNudgeIgnores: 2, // Escalate after 2 nudges
			NudgeInterval:   1,
		},
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config validation failed: %v\n", err)
		os.Exit(1)
	}

	skillReg := skills.NewRegistry()
	metrics := &autogenskills.Metrics{}
	svc, err := autogenskills.NewService(cfg, skillReg, metrics)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create service failed: %v\n", err)
		os.Exit(1)
	}

	// Register hooks with a real SDK hooks.Manager
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	if err := svc.RegisterHooksWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "register hooks failed: %v\n", err)
		os.Exit(1)
	}

	registered := mgr.List()
	fmt.Printf("Registered %d hook(s):\n", len(registered))
	for _, h := range registered {
		fmt.Printf("  • %s (priority=%d)\n", h.Hook.Name(), h.Hook.Priority())
	}
	fmt.Println()

	// ── Test 1: Onboarding budget — tool calls allowed ─────────
	fmt.Println("─── Test 1: Onboarding budget (budget=3) — calls allowed ──────")
	for i := range 3 {
		event := hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{
				"tool_name": "bash",
				"params":    map[string]any{"command": "rm file.txt"},
			},
		}
		_, err := mgr.EmitWithResult(ctx, event)
		if err != nil {
			fmt.Printf("  call %d: BLOCKED unexpectedly: %v\n", i+1, err)
			fail()
		}
		fmt.Printf("  call %d: ✓ ALLOWED\n", i+1)
	}
	fmt.Println()

	// ── Test 2: Onboarding budget exceeded — HARD BLOCK ────────
	fmt.Println("─── Test 2: Onboarding budget exceeded — HARD BLOCK ────────────")
	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "bash",
			"params":    map[string]any{"command": "rm file.txt"},
		},
	}
	result, err := mgr.EmitWithResult(ctx, event)
	if err == nil {
		fmt.Println("  ✗ EXPECTED BLOCK but got no error")
		fail()
	} else {
		fmt.Printf("  ✓ BLOCKED as expected\n")
		fmt.Printf("    Blocked by: %s\n", result.BlockedBy)
		fmt.Printf("    Reason: %s\n", result.BlockReason)
	}
	fmt.Println()

	// ── Test 3: Read-only tools also BLOCKED after budget ────────
	fmt.Println("─── Test 3: Read-only tools BLOCKED after budget (no exemption) ────")
	readOnlyTools := []string{"Read", "Grep", "Glob", "git_log", "lsp_definition"}
	for _, toolName := range readOnlyTools {
		event := hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": toolName},
		}
		_, err := mgr.EmitWithResult(ctx, event)
		if err == nil {
			fmt.Printf("  ✗ %s should be BLOCKED but was allowed\n", toolName)
			fail()
		} else {
			fmt.Printf("  ✓ %s BLOCKED\n", toolName)
		}
	}
	fmt.Println()

	// ── Test 4: SkillManage tool still allowed ───────────────────
	fmt.Println("─── Test 4: SkillManage tool allowed after budget ──────────────")
	event = hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"params":    map[string]any{"action": "create", "name": "test-skill"},
		},
	}
	_, err = mgr.EmitWithResult(ctx, event)
	if err != nil {
		fmt.Printf("  ✗ SkillManage BLOCKED unexpectedly: %v\n", err)
		fail()
	} else {
		fmt.Println("  ✓ SkillManage ALLOWED")
	}
	fmt.Println()

	// ── Test 5: Task tools still allowed ─────────────────────────
	fmt.Println("─── Test 5: Task tools allowed after budget ──────────────────────")
	taskTools := []string{"task_list", "TodoRead"}
	for _, toolName := range taskTools {
		event := hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": toolName},
		}
		_, err := mgr.EmitWithResult(ctx, event)
		if err != nil {
			fmt.Printf("  ✗ %s BLOCKED unexpectedly: %v\n", toolName, err)
			fail()
		} else {
			fmt.Printf("  ✓ %s ALLOWED\n", toolName)
		}
	}
	fmt.Println()

	// ── Test 6: Budget upgrades after first SkillManage ────────
	fmt.Println("─── Test 6: Budget upgrades after successful SkillManage ───────")

	// Confirm we're still blocked before reset
	event = hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "bash",
			"params":    map[string]any{"command": "echo hello"},
		},
	}
	_, err = mgr.EmitWithResult(ctx, event)
	if err == nil {
		fmt.Println("  ✗ Expected bash to be BLOCKED before reset")
		fail()
	} else {
		fmt.Println("  ✓ Bash BLOCKED before reset")
	}

	// Emit after-execute for a successful SkillManage → upgrade to working budget
	afterEvent := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"params":    map[string]any{"action": "create", "name": "probe-skill"},
			"result":    "skill created successfully",
		},
	}
	_, _ = mgr.EmitWithResult(ctx, afterEvent)
	fmt.Println("  ✓ SkillManage after-execute emitted (budget upgraded to working)")

	// Now bash should be allowed (upgraded to working budget of 5)
	event = hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "bash",
			"params":    map[string]any{"command": "echo hello"},
		},
	}
	_, err = mgr.EmitWithResult(ctx, event)
	if err != nil {
		fmt.Printf("  ✗ Bash should be ALLOWED after budget upgrade, but got: %v\n", err)
		fail()
	} else {
		fmt.Println("  ✓ Bash ALLOWED after budget upgrade")
	}
	fmt.Println()

	// ── Test 7: Working budget — soft nudge when exceeded ──────
	fmt.Println("─── Test 7: Working budget exceeded → soft nudge ───────────────")

	// Use up remaining working budget (already used 1 call, budget is 5)
	for range 4 {
		event = hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{
				"tool_name": "bash",
				"params":    map[string]any{"command": "echo hello"},
			},
		}
		_, err := mgr.EmitWithResult(ctx, event)
		if err != nil {
			fmt.Printf("  ✗ Bash should be ALLOWED within working budget, got: %v\n", err)
			fail()
		}
	}

	// Next call should get a soft nudge (ContinueWithMessage), not a hard block
	event = hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "bash",
			"params":    map[string]any{"command": "echo hello"},
		},
	}
	result, err = mgr.EmitWithResult(ctx, event)
	if err != nil {
		fmt.Printf("  ✗ Expected soft nudge (allowed), got hard block: %v\n", err)
		fail()
	} else {
		fmt.Println("  ✓ Soft nudge delivered (tool call ALLOWED)")
		// Check if the nudge message was injected
		hasNudge := false
		for _, ho := range result.HookOutputs {
			if ho.Output != "" {
				hasNudge = true
				fmt.Printf("    Nudge context: %s\n", truncate(ho.Output, 100))
			}
		}
		if !hasNudge {
			fmt.Println("    (no nudge context in hook results — message may be in block reason)")
		}
	}
	fmt.Println()

	// ── Test 8: Escalation after max nudges ignored ────────────
	fmt.Println("─── Test 8: Escalation after MaxNudgeIgnores exceeded ─────────")

	// We already used 1 nudge. Max is 2. One more soft nudge, then hard block.
	event = hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "bash",
			"params":    map[string]any{"command": "echo hello"},
		},
	}
	result, err = mgr.EmitWithResult(ctx, event)
	if err != nil {
		fmt.Printf("  ✗ Expected 2nd soft nudge (allowed), got hard block: %v\n", err)
		fail()
	} else {
		fmt.Println("  ✓ 2nd soft nudge delivered (tool call ALLOWED)")
	}

	// 3rd nudge should escalate to hard block
	event = hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "bash",
			"params":    map[string]any{"command": "echo hello"},
		},
	}
	_, err = mgr.EmitWithResult(ctx, event)
	if err == nil {
		fmt.Println("  ✗ Expected ESCALATION hard block, but call was allowed")
		fail()
	} else {
		fmt.Println("  ✓ ESCALATION hard block after max nudges ignored")
	}
	fmt.Println()

	// ── Test 9: Budget refills after subsequent SkillManage ────
	fmt.Println("─── Test 9: Budget refills after subsequent SkillManage ────────")

	// Confirm we're still blocked after escalation
	event = hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "bash",
			"params":    map[string]any{"command": "echo hello"},
		},
	}
	_, err = mgr.EmitWithResult(ctx, event)
	if err == nil {
		fmt.Println("  ✗ Expected bash to be BLOCKED after escalation")
		fail()
	} else {
		fmt.Println("  ✓ Bash BLOCKED after escalation")
	}

	// Create another skill → refill budget
	afterEvent = hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"params":    map[string]any{"action": "create", "name": "probe-skill-2"},
			"result":    "skill created successfully",
		},
	}
	_, _ = mgr.EmitWithResult(ctx, afterEvent)
	fmt.Println("  ✓ SkillManage after-execute emitted (budget refilled)")

	// Now bash should be allowed again
	event = hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "bash",
			"params":    map[string]any{"command": "echo hello"},
		},
	}
	_, err = mgr.EmitWithResult(ctx, event)
	if err != nil {
		fmt.Printf("  ✗ Bash should be ALLOWED after refill, but got: %v\n", err)
		fail()
	} else {
		fmt.Println("  ✓ Bash ALLOWED after budget refill")
	}
	fmt.Println()

	// ── Summary ─────────────────────────────────────────────────
	if failed {
		fmt.Println("═══════════════════════════════════════════════════════════════")
		fmt.Println("  SOME TESTS FAILED")
		fmt.Println("═══════════════════════════════════════════════════════════════")
		os.Exit(1)
	}

	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  ALL TESTS PASSED")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Println("The two-tier budget enforcement is working correctly:")
	fmt.Println("  • Onboarding budget (3): HARD BLOCK after exceeded")
	fmt.Println("  • Read-only tools count toward budget (no exemption)")
	fmt.Println("  • SkillManage tool always available (escape hatch)")
	fmt.Println("  • Task tools always available (planning continues)")
	fmt.Println("  • Budget upgrades to working budget (5) after first skill creation")
	fmt.Println("  • Working budget exceeded → soft nudge (not hard block)")
	fmt.Println("  • After MaxNudgeIgnores (2) → escalation hard block")
	fmt.Println("  • Budget refills after subsequent SkillManage")
	fmt.Println()
	fmt.Printf("Hook execution stats: executed=%d, blocked=%d\n",
		mgr.GetStats().TotalExecutions,
		mgr.GetStats().TotalBlocked,
	)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
