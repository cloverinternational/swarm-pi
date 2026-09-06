package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/analytics/backfill"
)

func runAnalyticsCLI(args []string) error {
	return runAnalyticsCLIWithWriters(args, os.Stdout, os.Stderr)
}

func runAnalyticsCLIWithWriters(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printAnalyticsUsage(stderr)
		return nil
	}
	switch args[0] {
	case "status":
		return runAnalyticsStatusCLI(args[1:], stdout, stderr)
	case "flush":
		return runAnalyticsFlushCLI(args[1:], stdout, stderr)
	case "backfill":
		return runAnalyticsBackfillCLI(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printAnalyticsUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown analytics subcommand: %s", args[0])
	}
}

func printAnalyticsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: swarmos analytics <subcommand> [options]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  status     Show analytics queue and backfill ledger status")
	fmt.Fprintln(w, "  flush      Flush locally spooled analytics events and artifacts")
	fmt.Fprintln(w, "  backfill   Backfill local conversations and hook artifacts to remote analytics")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  swarmos analytics status")
	fmt.Fprintln(w, "  swarmos analytics flush --timeout 2m")
	fmt.Fprintln(w, "  swarmos analytics backfill --dry-run --until 2026-04-20")
	fmt.Fprintln(w, "  swarmos analytics backfill --include conversations --workspace /path/to/project --limit 100")
}

func runAnalyticsStatusCLI(args []string, stdout, stderr io.Writer) error {
	opts, jsonOutput, err := parseAnalyticsCommonFlags("analytics status", args, stderr)
	if err != nil {
		return err
	}
	result, err := backfill.Status(context.Background(), opts)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "Analytics enabled: %t\n", result.AnalyticsEnabled)
	fmt.Fprintf(stdout, "Collector URL: %s\n", result.CollectorURL)
	fmt.Fprintf(stdout, "Event spool: %d files, %d bytes (%s)\n", result.Spool.Events.Files, result.Spool.Events.Bytes, result.Spool.Events.Dir)
	fmt.Fprintf(stdout, "Artifact spool: %d files, %d bytes (%s)\n", result.Spool.Artifacts.Files, result.Spool.Artifacts.Bytes, result.Spool.Artifacts.Dir)
	fmt.Fprintf(stdout, "Backfill ledger: %d entries (%d events, %d artifacts) at %s\n", result.Ledger.Entries, result.Ledger.Events, result.Ledger.Artifacts, result.LedgerPath)
	return nil
}

func runAnalyticsFlushCLI(args []string, stdout, stderr io.Writer) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	opts := backfill.DefaultOptions(homeDir)
	fs := flag.NewFlagSet("analytics flush", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", opts.FlushTimeout, "Maximum time to wait for collector flush")
	jsonFlag := fs.Bool("json", false, "Output JSON")
	ledgerPath := fs.String("ledger", opts.LedgerPath, "Backfill ledger JSONL path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts.FlushTimeout = *timeout
	opts.LedgerPath = *ledgerPath
	result, err := backfill.Flush(context.Background(), opts)
	if err != nil {
		return err
	}
	if *jsonFlag {
		return writeJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "Flushed analytics spool to %s\n", result.CollectorURL)
	fmt.Fprintf(stdout, "Events: %d -> %d files\n", result.SpoolBefore.Events.Files, result.SpoolAfter.Events.Files)
	fmt.Fprintf(stdout, "Artifacts: %d -> %d files\n", result.SpoolBefore.Artifacts.Files, result.SpoolAfter.Artifacts.Files)
	return nil
}

func runAnalyticsBackfillCLI(args []string, stdout, stderr io.Writer) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	defaults := backfill.DefaultOptions(homeDir)
	fs := flag.NewFlagSet("analytics backfill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	since := fs.String("since", "", "Backfill items at or after date/time (YYYY-MM-DD or RFC3339)")
	until := fs.String("until", "", "Backfill items at or before date/time (YYYY-MM-DD or RFC3339)")
	workspace := fs.String("workspace", "", "Workspace path to scope the backfill")
	scope := fs.String("scope", defaults.Scope, "Backfill scope: all or workspace")
	include := fs.String("include", defaults.Include, "Data to include: all, conversations, or artifacts")
	limit := fs.Int("limit", 0, "Maximum events/artifacts to enqueue")
	dryRun := fs.Bool("dry-run", false, "Scan and report without enqueueing")
	noFlush := fs.Bool("no-flush", false, "Leave backfilled data in the local spool instead of flushing")
	requeue := fs.Bool("requeue", false, "Requeue IDs already present in the backfill ledger")
	timeout := fs.Duration("timeout", defaults.FlushTimeout, "Maximum time to wait for final collector flush")
	jsonOutput := fs.Bool("json", false, "Output JSON")
	conversationsDir := fs.String("conversations-dir", defaults.ConversationsDir, "Local conversation storage directory")
	swarmDir := fs.String("swarm-dir", defaults.SwarmDir, "Local ~/.swarm directory")
	legacyFindingsDir := fs.String("legacy-findings-dir", defaults.LegacyFindingsDir, "Legacy ~/.swarmos/findings directory")
	ledgerPath := fs.String("ledger", defaults.LedgerPath, "Backfill ledger JSONL path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := defaults
	opts.ConversationsDir = *conversationsDir
	opts.SwarmDir = *swarmDir
	opts.LegacyFindingsDir = *legacyFindingsDir
	opts.LedgerPath = *ledgerPath
	opts.Workspace = *workspace
	opts.Scope = *scope
	opts.Include = *include
	opts.Limit = *limit
	opts.DryRun = *dryRun
	opts.NoFlush = *noFlush
	opts.Requeue = *requeue
	opts.FlushTimeout = *timeout
	if opts.Workspace != "" {
		if abs, err := filepath.Abs(opts.Workspace); err == nil {
			opts.Workspace = abs
		}
	}
	if *since != "" {
		parsed, err := backfillParseTime(*since, false)
		if err != nil {
			return err
		}
		opts.Since = parsed
	}
	if *until != "" {
		parsed, err := backfillParseTime(*until, true)
		if err != nil {
			return err
		}
		opts.Until = parsed
	}
	result, runErr := backfill.Run(context.Background(), opts)
	if *jsonOutput {
		if err := writeJSON(stdout, result); err != nil {
			return err
		}
		return runErr
	}
	fmt.Fprintf(stdout, "Backfill scan complete (dry-run: %t)\n", result.DryRun)
	fmt.Fprintf(stdout, "Conversations scanned: %d\n", result.ConversationsScanned)
	fmt.Fprintf(stdout, "Events: %d considered, %d enqueued\n", result.EventsConsidered, result.EventsEnqueued)
	fmt.Fprintf(stdout, "Artifacts: %d considered, %d enqueued\n", result.ArtifactsConsidered, result.ArtifactsEnqueued)
	fmt.Fprintf(stdout, "Skipped: %d ledger, %d filter\n", result.SkippedLedger, result.SkippedFilter)
	if !result.DryRun {
		fmt.Fprintf(stdout, "Event spool: %d -> %d files\n", result.SpoolBefore.Events.Files, result.SpoolAfter.Events.Files)
		fmt.Fprintf(stdout, "Artifact spool: %d -> %d files\n", result.SpoolBefore.Artifacts.Files, result.SpoolAfter.Artifacts.Files)
	}
	if len(result.Errors) > 0 {
		fmt.Fprintf(stdout, "Errors: %d\n", len(result.Errors))
		for _, item := range result.Errors {
			fmt.Fprintf(stdout, "  %s\n", item)
		}
	}
	return runErr
}

func parseAnalyticsCommonFlags(name string, args []string, stderr io.Writer) (backfill.Options, bool, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return backfill.Options{}, false, err
	}
	defaults := backfill.DefaultOptions(homeDir)
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := fs.Bool("json", false, "Output JSON")
	ledgerPath := fs.String("ledger", defaults.LedgerPath, "Backfill ledger JSONL path")
	if err := fs.Parse(args); err != nil {
		return backfill.Options{}, false, err
	}
	defaults.LedgerPath = *ledgerPath
	return defaults, *jsonOutput, nil
}

func backfillParseTime(value string, endOfDay bool) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"}
	var lastErr error
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			if layout == "2006-01-02" && endOfDay {
				return parsed.Add(24*time.Hour - time.Nanosecond).UTC(), nil
			}
			return parsed.UTC(), nil
		}
		lastErr = err
	}
	return time.Time{}, fmt.Errorf("invalid time %q: %w", value, lastErr)
}

func writeJSON(w io.Writer, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(body))
	return err
}
