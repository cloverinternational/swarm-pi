package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

func runCuratorCLI(args []string) error {
	fs := flag.NewFlagSet("curator", flag.ExitOnError)
	providerFlag := fs.String("P", "", "Provider name (anthropic, openai, fireworks, etc.)")
	modelFlag := fs.String("m", "", "Model ID")
	executeFlag := fs.Bool("execute", false, "Execute consolidation via LLM agent (default: dry-run)")
	previewFlag := fs.Bool("preview", false, "Run a full non-mutating LLM consolidation preview")
	consolidateFlag := fs.Bool("consolidate", false, "Force the consolidation pass for this run (overrides curator.consolidate config)")
	listBackupsFlag := fs.Bool("list-backups", false, "List restorable curator snapshots")
	rollbackFlag := fs.String("rollback", "", "Restore a curator snapshot by ID (takes a safety snapshot first)")
	verboseFlag := fs.Bool("verbose", false, "Verbose output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Resolve provider / model from saved config if not explicitly given
	providerName := *providerFlag
	modelName := *modelFlag
	configMgr, err := commands.NewConfigManager()
	if err == nil {
		if savedConfig, err := configMgr.LoadConfig(); err == nil && savedConfig != nil {
			if providerName == "" {
				providerName = savedConfig.CurrentProvider
			}
			if modelName == "" {
				modelName = savedConfig.CurrentModel
			}
		}
	}
	if providerName == "" {
		providerName = "claudecode"
	}
	if modelName == "" {
		modelName = "claude-sonnet-4-20250514"
	}

	explicitProviderModel := strings.TrimSpace(*providerFlag) != "" && strings.TrimSpace(*modelFlag) != ""

	permConfig, _ := chat.LoadPermissionConfig()
	if permConfig == nil {
		permConfig = chat.NewPermissionConfigWithDefaults()
	}
	headlessBroker := chat.NewHeadlessApprovalBroker()

	sdk, err := chat.NewSDKIntegrationWithOptions(providerName, modelName, chat.SDKIntegrationOptions{
		DebugMode:             *verboseFlag,
		VerboseDebug:          *verboseFlag,
		PermissionConfig:      permConfig,
		ApprovalBroker:        headlessBroker,
		HeadlessMode:          true,
		ExplicitProviderModel: explicitProviderModel,
	})
	if err != nil {
		return fmt.Errorf("SDK init failed: %w", err)
	}

	hooksMgr := sdk.GetHooksManager()
	if hooksMgr == nil {
		return fmt.Errorf("hooks manager not initialized")
	}

	svc := hooksMgr.GetAutogenSkillsService()
	if svc == nil {
		return fmt.Errorf("autogenskills service not enabled")
	}

	curator := svc.GetCurator()
	if curator == nil {
		return fmt.Errorf("curator not configured")
	}

	autogenDir := svc.GetConfig().AutogenDir
	if autogenDir == "" {
		return fmt.Errorf("autogen directory not configured")
	}
	backupStore, err := autogenskills.NewCuratorBackupStore(autogenDir, "", autogenskills.DefaultCuratorBackupRetention)
	if err != nil {
		return fmt.Errorf("initialize curator backups: %w", err)
	}
	if *listBackupsFlag {
		backups, err := backupStore.List()
		if err != nil {
			return fmt.Errorf("list curator backups: %w", err)
		}
		if len(backups) == 0 {
			fmt.Println("No curator snapshots.")
			return nil
		}
		for _, backup := range backups {
			fmt.Printf("%s  %s  files=%d  bytes=%d  %s\n", backup.ID, backup.CreatedAt.Format(time.RFC3339), backup.FileCount, backup.SizeBytes, backup.Reason)
		}
		return nil
	}
	if strings.TrimSpace(*rollbackFlag) != "" {
		result, err := backupStore.Rollback(strings.TrimSpace(*rollbackFlag))
		if err != nil {
			return fmt.Errorf("rollback curator snapshot: %w", err)
		}
		fmt.Printf("Restored snapshot %s; pre-rollback safety snapshot: %s\n", result.Restored.ID, result.Safety.ID)
		return nil
	}

	consolidateOn := svc.GetConfig().Curator.Consolidate
	if *consolidateFlag {
		curator.SetConsolidate(true)
		consolidateOn = true
	}

	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║  SWARM SKILL CURATOR PROBE                                   ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Provider: %-49s ║\n", providerName)
	fmt.Printf("║  Model:    %-49s ║\n", modelName)
	fmt.Printf("║  Skills:   %-49s ║\n", autogenDir)
	fmt.Printf("║  Execute:  %-49v ║\n", *executeFlag)
	fmt.Printf("║  Preview:  %-49v ║\n", *previewFlag)
	fmt.Printf("║  Consolidate: %-46v ║\n", consolidateOn)
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Run the rule-based curator analysis
	fmt.Println("[1] Running rule-based analysis...")
	results, err := curator.Run(autogenDir)
	if err != nil {
		return fmt.Errorf("curator analysis failed: %w", err)
	}

	archiveCount := 0
	staleCount := 0
	consolidateCount := 0
	patchCount := 0
	noneCount := 0

	for _, r := range results {
		switch r.Action {
		case autogenskills.ActionArchive:
			archiveCount++
		case autogenskills.ActionMarkStale:
			staleCount++
		case autogenskills.ActionConsolidate:
			consolidateCount++
		case autogenskills.ActionPatch:
			patchCount++
		case autogenskills.ActionNone:
			noneCount++
		}
	}

	fmt.Printf("  Total skills:     %d\n", len(results))
	fmt.Printf("  Archive recs:     %d\n", archiveCount)
	fmt.Printf("  Stale (candidates): %d\n", staleCount)
	fmt.Printf("  Consolidate recs: %d\n", consolidateCount)
	fmt.Printf("  Patch recs:       %d\n", patchCount)
	fmt.Printf("  No action:        %d\n", noneCount)
	fmt.Println()

	if consolidateCount > 0 {
		fmt.Println("[CONSOLIDATION CANDIDATES]")
		for _, r := range results {
			if r.Action == autogenskills.ActionConsolidate {
				fmt.Printf("  %s → %v\n", r.SkillName, r.RelatedSkills)
			}
		}
		fmt.Println()
	}

	if archiveCount > 0 {
		fmt.Println("[ARCHIVE CANDIDATES]")
		for _, r := range results {
			if r.Action == autogenskills.ActionArchive {
				fmt.Printf("  %s (%s)\n", r.SkillName, r.Reason)
			}
		}
		fmt.Println()
	}

	if staleCount > 0 {
		fmt.Println("[STALE — ARCHIVAL CANDIDATES]")
		for _, r := range results {
			if r.Action == autogenskills.ActionMarkStale {
				fmt.Printf("  %s (%s)\n", r.SkillName, r.Reason)
			}
		}
		fmt.Println()
	}

	if patchCount > 0 {
		fmt.Println("[PATCH CANDIDATES]")
		for _, r := range results {
			if r.Action == autogenskills.ActionPatch {
				fmt.Printf("  %s (%s)\n", r.SkillName, r.Reason)
			}
		}
		fmt.Println()
	}

	// Check if we should execute or preview with the LLM.
	if !*executeFlag && !*previewFlag {
		fmt.Println("[DRY RUN — use --execute to run the LLM agent]")
		return nil
	}

	curatorAgent := hooksMgr.GetCuratorAgent()
	if curatorAgent == nil {
		fmt.Println("[ERROR] CuratorAgent not available — no LLM agent factory configured")
		fmt.Println("        The curator recommendations are ready but require an LLM agent to execute.")
		return nil
	}

	mode := "execute"
	if *previewFlag {
		mode = "preview"
	}
	fmt.Printf("[2] Spawning LLM CuratorAgent to %s recommendations...\n", mode)
	curatorTimeout, err := time.ParseDuration(svc.GetConfig().Curator.WithDefaults().Timeout)
	if err != nil || curatorTimeout <= 0 {
		return fmt.Errorf("invalid curator timeout %q", svc.GetConfig().Curator.Timeout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), curatorTimeout)
	defer cancel()

	before, err := autogenskills.SnapshotCuratorInventory(autogenDir)
	if err != nil {
		return fmt.Errorf("capture pre-run curator inventory: %w", err)
	}
	var snapshotID string
	if *executeFlag {
		snapshot, err := backupStore.Snapshot("pre-curator-run")
		if err != nil {
			return fmt.Errorf("create pre-run curator snapshot: %w", err)
		}
		snapshotID = snapshot.ID
		fmt.Printf("  Safety snapshot: %s\n", snapshotID)
	}

	var agentResult *autogenskills.CuratorAgentResult
	if *previewFlag {
		agentResult, err = curatorAgent.RunPreview(ctx, results, consolidateOn)
	} else {
		agentResult, err = curatorAgent.RunWithReview(ctx, results, consolidateOn)
	}
	if err != nil {
		fmt.Println()
		fmt.Println("[ERROR] CuratorAgent execution failed:")
		fmt.Printf("  %v\n", err)
		fmt.Println()
		fmt.Println("This usually means the provider/model combination is invalid for sub-agents.")
		fmt.Printf("Current: provider=%s model=%s\n", providerName, modelName)
		fmt.Println("Try: swarm curator -P anthropic -m claude-sonnet-4-20250514 --execute")
		return err
	}

	if *executeFlag {
		if reconcileErr := curator.ReconcileAndSave(autogenDir); reconcileErr != nil {
			return fmt.Errorf("post-agent curator reconciliation: %w", reconcileErr)
		}
		curator.SetLastRunAt(time.Now())
		if saveErr := curator.SaveState(); saveErr != nil {
			return fmt.Errorf("save curator state: %w", saveErr)
		}
	}

	after, err := autogenskills.SnapshotCuratorInventory(autogenDir)
	if err != nil {
		return fmt.Errorf("capture post-run curator inventory: %w", err)
	}
	if *previewFlag && (len(before.Active) != len(after.Active) || len(before.Archived) != len(after.Archived)) {
		return fmt.Errorf("curator preview changed corpus inventory")
	}
	if _, parseErr := autogenskills.ParseCuratorStructuredSummary(agentResult.Summary); parseErr != nil {
		fmt.Printf("[WARN] curator omitted or malformed structured summary: %v\n", parseErr)
	}
	finishedAt := time.Now()
	reportDir := filepath.Join(filepath.Dir(autogenDir), ".curator_logs", finishedAt.UTC().Format("20060102-150405.000000000"))
	report, reportErr := autogenskills.WriteCuratorRunReport(autogenskills.CuratorRunReportOptions{
		AutogenDir: autogenDir, OutputDir: reportDir,
		Before: before, After: after, State: curator.GetState(),
		ModelOutput: agentResult.Summary, Model: modelName, Provider: providerName,
		TurnCount: agentResult.TurnCount, TokenCount: agentResult.TokensUsed,
		StartedAt: finishedAt.Add(-agentResult.Duration), FinishedAt: finishedAt,
	})
	if reportErr != nil {
		return fmt.Errorf("write curator report (snapshot %s): %w", snapshotID, reportErr)
	}

	fmt.Println()
	fmt.Println("[3] CuratorAgent completed")
	fmt.Printf("  Duration: %s\n", agentResult.Duration.String())
	fmt.Printf("  Turns:    %d\n", agentResult.TurnCount)
	fmt.Printf("  Tokens:   %d\n", agentResult.TokensUsed)
	fmt.Println()
	fmt.Println("[AGENT SUMMARY]")
	fmt.Println(agentResult.Summary)
	fmt.Printf("\nReport: %s (removed=%d added=%d)\n", reportDir, len(report.Removed), len(report.Added))

	return nil
}
