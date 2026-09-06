package backfill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdkanalytics "github.com/Swarm-Code/mono/swarm-sdk/internal/analytics"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

type sender struct {
	cfg           sdkanalytics.Config
	dispatcher    *sdkanalytics.Dispatcher
	ledger        *ledger
	result        *Result
	dryRun        bool
	requeue       bool
	machineIDHash string
	deviceIDHash  string
}

func Status(ctx context.Context, opts Options) (StatusResult, error) {
	opts = normalizeOptions(opts)
	cfg := analyticsConfig()
	dispatcher, err := sdkanalytics.NewManualDispatcher(cfg)
	if err != nil {
		return StatusResult{}, err
	}
	spoolStats, err := dispatcher.Stats()
	if err != nil {
		return StatusResult{}, err
	}
	l, err := loadLedger(opts.LedgerPath)
	if err != nil {
		return StatusResult{}, err
	}
	select {
	case <-ctx.Done():
		return StatusResult{}, ctx.Err()
	default:
	}
	return StatusResult{
		AnalyticsEnabled: cfg.Enabled(),
		CollectorURL:     cfg.CollectorURL,
		LedgerPath:       opts.LedgerPath,
		Ledger:           l.Stats(),
		Spool:            spoolStats,
	}, nil
}

func Flush(ctx context.Context, opts Options) (FlushResult, error) {
	opts = normalizeOptions(opts)
	cfg := analyticsConfig()
	if !cfg.Enabled() {
		return FlushResult{AnalyticsEnabled: false, CollectorURL: cfg.CollectorURL}, fmt.Errorf("analytics is disabled")
	}
	dispatcher, err := sdkanalytics.NewManualDispatcher(cfg)
	if err != nil {
		return FlushResult{}, err
	}
	before, err := dispatcher.Stats()
	if err != nil {
		return FlushResult{}, err
	}
	timeout := opts.FlushTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	flushCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := dispatcher.Flush(flushCtx); err != nil {
		return FlushResult{}, err
	}
	after, err := dispatcher.Stats()
	if err != nil {
		return FlushResult{}, err
	}
	return FlushResult{
		AnalyticsEnabled: true,
		CollectorURL:     cfg.CollectorURL,
		SpoolBefore:      before,
		SpoolAfter:       after,
	}, nil
}

func Run(ctx context.Context, opts Options) (Result, error) {
	opts = normalizeOptions(opts)
	cfg := analyticsConfig()
	result := Result{
		StartedAt:        time.Now().UTC(),
		DryRun:           opts.DryRun,
		LedgerPath:       opts.LedgerPath,
		AnalyticsEnabled: cfg.Enabled(),
		CollectorURL:     cfg.CollectorURL,
	}
	if err := validateOptions(opts); err != nil {
		return result, err
	}
	l, err := loadLedger(opts.LedgerPath)
	if err != nil {
		return result, err
	}
	s, err := newSender(opts, l, &result)
	if err != nil {
		return result, err
	}
	if s.dispatcher != nil {
		before, err := s.dispatcher.Stats()
		if err != nil {
			return result, err
		}
		result.SpoolBefore = before
	}

	workspaceIndex, err := buildWorkspaceIndex(ctx, opts)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
	}

	if includeConversations(opts.Include) {
		if err := backfillConversations(ctx, opts, s, workspaceIndex); err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
	}
	if includeArtifacts(opts.Include) && !result.limitReached(opts.Limit) {
		if err := backfillArtifacts(ctx, opts, s, workspaceIndex); err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
	}
	if s.dispatcher != nil && !opts.NoFlush {
		timeout := opts.FlushTimeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		flushCtx, cancel := context.WithTimeout(ctx, timeout)
		err := s.dispatcher.Flush(flushCtx)
		cancel()
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
	}
	if s.dispatcher != nil {
		after, err := s.dispatcher.Stats()
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.SpoolAfter = after
		}
	}
	result.FinishedAt = time.Now().UTC()
	if len(result.Errors) > 0 {
		return result, errors.New(strings.Join(result.Errors, "; "))
	}
	return result, nil
}

func normalizeOptions(opts Options) Options {
	if opts.ConversationsDir == "" || opts.SwarmDir == "" || opts.LegacyFindingsDir == "" || opts.LedgerPath == "" || opts.FlushTimeout <= 0 {
		if homeDir, err := os.UserHomeDir(); err == nil {
			defaults := DefaultOptions(homeDir)
			if opts.ConversationsDir == "" {
				opts.ConversationsDir = defaults.ConversationsDir
			}
			if opts.SwarmDir == "" {
				opts.SwarmDir = defaults.SwarmDir
			}
			if opts.LegacyFindingsDir == "" {
				opts.LegacyFindingsDir = defaults.LegacyFindingsDir
			}
			if opts.LedgerPath == "" {
				opts.LedgerPath = defaults.LedgerPath
			}
			if opts.FlushTimeout <= 0 {
				opts.FlushTimeout = defaults.FlushTimeout
			}
		}
	}
	if opts.Scope == "" {
		opts.Scope = ScopeAll
	}
	if opts.Include == "" {
		opts.Include = IncludeAll
	}
	if opts.Scope == ScopeWorkspace && opts.Workspace == "" {
		opts.Workspace = "."
	}
	if opts.Workspace != "" {
		if abs, err := filepath.Abs(opts.Workspace); err == nil {
			opts.Workspace = abs
		}
	}
	return opts
}

func validateOptions(opts Options) error {
	switch opts.Scope {
	case ScopeAll, ScopeWorkspace:
	default:
		return fmt.Errorf("invalid scope %q: want all or workspace", opts.Scope)
	}
	switch opts.Include {
	case IncludeAll, IncludeConversations, IncludeArtifacts:
	default:
		return fmt.Errorf("invalid include %q: want all, conversations, or artifacts", opts.Include)
	}
	if !opts.Since.IsZero() && !opts.Until.IsZero() && opts.Since.After(opts.Until) {
		return fmt.Errorf("since must be before until")
	}
	return nil
}

func analyticsConfig() sdkanalytics.Config {
	cfg := sdkanalytics.ConfigFromEnv()
	if cfg.Source == "" {
		cfg.Source = sdkanalytics.DefaultSource
	}
	if cfg.AppVersion == "" {
		cfg.AppVersion = version.DisplayVersion()
	}
	return cfg
}

func newSender(opts Options, l *ledger, result *Result) (*sender, error) {
	cfg := analyticsConfig()
	s := &sender{
		cfg:     cfg,
		ledger:  l,
		result:  result,
		dryRun:  opts.DryRun,
		requeue: opts.Requeue,
	}
	if opts.DryRun {
		return s, nil
	}
	if !cfg.Enabled() {
		return nil, fmt.Errorf("analytics is disabled")
	}
	machineIDHash, err := sdkanalytics.MachineIDHash(cfg.MachineIDNamespace)
	if err != nil {
		return nil, err
	}
	deviceIDHash, err := sdkanalytics.DeviceIDHash(cfg.DeviceIDNamespace)
	if err != nil {
		deviceIDHash = machineIDHash
	}
	dispatcher, err := sdkanalytics.NewManualDispatcher(cfg)
	if err != nil {
		return nil, err
	}
	s.dispatcher = dispatcher
	s.machineIDHash = machineIDHash
	s.deviceIDHash = deviceIDHash
	return s, nil
}

func (s *sender) enqueueEvent(event sdkanalytics.EventEnvelope, source string) error {
	s.result.EventsConsidered++
	if s.ledger.has(event.EventID) && !s.requeue {
		s.result.SkippedLedger++
		return nil
	}
	if s.dryRun {
		s.result.EventsEnqueued++
		return nil
	}
	event.SchemaVersion = sdkanalytics.SchemaVersion
	event.Source = s.cfg.Source
	event.AppVersion = s.cfg.AppVersion
	event.MachineIDHash = s.machineIDHash
	event.DeviceIDHash = s.deviceIDHash
	if event.IngestedAt.IsZero() {
		event.IngestedAt = time.Now().UTC()
	}
	if err := s.dispatcher.Enqueue(event); err != nil {
		return err
	}
	s.result.EventsEnqueued++
	return s.ledger.append(ledgerEntry{
		ID:          event.EventID,
		Kind:        "event",
		Source:      source,
		ContentHash: stableContentHash(event),
	})
}

func (s *sender) enqueueArtifact(artifact sdkanalytics.ArtifactEnvelope, source string) error {
	s.result.ArtifactsConsidered++
	if s.ledger.has(artifact.ArtifactID) && !s.requeue {
		s.result.SkippedLedger++
		return nil
	}
	if s.dryRun {
		s.result.ArtifactsEnqueued++
		return nil
	}
	artifact.SchemaVersion = sdkanalytics.SchemaVersion
	artifact.Source = s.cfg.Source
	artifact.AppVersion = s.cfg.AppVersion
	artifact.MachineIDHash = s.machineIDHash
	artifact.DeviceIDHash = s.deviceIDHash
	if artifact.IngestedAt.IsZero() {
		artifact.IngestedAt = time.Now().UTC()
	}
	if err := s.dispatcher.EnqueueArtifact(artifact); err != nil {
		return err
	}
	s.result.ArtifactsEnqueued++
	return s.ledger.append(ledgerEntry{
		ID:          artifact.ArtifactID,
		Kind:        "artifact",
		Source:      source,
		ContentHash: stableContentHash(artifact),
	})
}

func includeConversations(include string) bool {
	return include == "" || include == IncludeAll || include == IncludeConversations
}

func includeArtifacts(include string) bool {
	return include == "" || include == IncludeAll || include == IncludeArtifacts
}
