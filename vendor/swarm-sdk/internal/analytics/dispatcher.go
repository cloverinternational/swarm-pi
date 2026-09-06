package analytics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"
)

type Dispatcher struct {
	cfg           Config
	client        *Client
	spool         *Spool
	artifactSpool *ArtifactSpool
	redactor      *Redactor
	autoFlush     bool
	flushMu       sync.Mutex
	flushCh       chan struct{}
	stopCh        chan struct{}
	doneCh        chan struct{}
	closeOnce     sync.Once
	closeErr      error
}

func NewDispatcher(cfg Config) (*Dispatcher, error) {
	return newDispatcher(cfg, true)
}

// NewManualDispatcher creates a dispatcher that spools events and artifacts but
// does not run the background flush loop. Call Flush explicitly when ready.
func NewManualDispatcher(cfg Config) (*Dispatcher, error) {
	return newDispatcher(cfg, false)
}

func newDispatcher(cfg Config, autoFlush bool) (*Dispatcher, error) {
	cfg = cfg.withDefaults()
	spool, err := NewSpool(cfg.SpoolDir, cfg.MaxSpoolBytes, cfg.MaxSpoolFiles)
	if err != nil {
		return nil, err
	}
	artifactSpool, err := NewArtifactSpool(cfg.SpoolDir, cfg.MaxSpoolBytes, cfg.MaxSpoolFiles)
	if err != nil {
		return nil, err
	}
	dispatcher := &Dispatcher{
		cfg:           cfg,
		client:        NewClient(cfg),
		spool:         spool,
		artifactSpool: artifactSpool,
		redactor:      NewRedactor(),
		autoFlush:     autoFlush,
		flushCh:       make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	if autoFlush {
		go dispatcher.loop()
	} else {
		close(dispatcher.doneCh)
	}
	return dispatcher, nil
}

func (d *Dispatcher) loop() {
	ticker := time.NewTicker(d.cfg.FlushInterval)
	defer ticker.Stop()
	defer close(d.doneCh)

	// Run age-prune once at startup to clear any accumulated stale events from
	// previous sessions where the backend was unreachable, then again hourly.
	d.pruneOldEvents()
	pruneTicker := time.NewTicker(time.Hour)
	defer pruneTicker.Stop()

	for {
		select {
		case <-d.flushCh:
			_ = d.Flush(context.Background())
		case <-ticker.C:
			_ = d.Flush(context.Background())
		case <-pruneTicker.C:
			d.pruneOldEvents()
		case <-d.stopCh:
			ctx, cancel := context.WithTimeout(context.Background(), d.cfg.ShutdownFlushTimeout)
			d.closeErr = d.Flush(ctx)
			cancel()
			return
		}
	}
}

func (d *Dispatcher) pruneOldEvents() {
	_ = d.spool.PruneOlderThan(d.cfg.MaxSpoolAge)
	_ = d.spool.PrunToLimits()
	_ = d.artifactSpool.PruneOlderThan(d.cfg.MaxSpoolAge)
	_ = d.artifactSpool.PruneToLimits()
}

func (d *Dispatcher) signalFlush() {
	if !d.autoFlush {
		return
	}
	select {
	case d.flushCh <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) Enqueue(event EventEnvelope) error {
	if strings.TrimSpace(event.EventID) == "" {
		event.EventID = NewEventID()
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = SchemaVersion
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.IngestedAt.IsZero() {
		event.IngestedAt = time.Now().UTC()
	}
	if event.Source == "" {
		event.Source = d.cfg.Source
	}
	if event.AppVersion == "" {
		event.AppVersion = d.cfg.AppVersion
	}
	if event.ArchiveStatus == "" {
		event.ArchiveStatus = "pending"
	}

	if event.Payload != nil {
		redacted := d.redactor.Redact(event.Payload)
		event.Payload = redacted.Value.(map[string]any)
		event.RedactionFlags = mergeFlags(event.RedactionFlags, redacted.Flags)
	}
	event.ContentHash = eventHash(event)

	if err := d.spool.Enqueue(event); err != nil {
		return err
	}
	d.signalFlush()
	return nil
}

func (d *Dispatcher) EnqueueArtifact(artifact ArtifactEnvelope) error {
	if strings.TrimSpace(artifact.ArtifactID) == "" {
		artifact.ArtifactID = NewEventID()
	}
	if artifact.SchemaVersion == 0 {
		artifact.SchemaVersion = SchemaVersion
	}
	if artifact.OccurredAt.IsZero() {
		artifact.OccurredAt = time.Now().UTC()
	}
	if artifact.IngestedAt.IsZero() {
		artifact.IngestedAt = time.Now().UTC()
	}
	if artifact.Source == "" {
		artifact.Source = d.cfg.Source
	}
	if artifact.AppVersion == "" {
		artifact.AppVersion = d.cfg.AppVersion
	}
	if artifact.Payload != nil {
		redacted := d.redactor.Redact(artifact.Payload)
		artifact.Payload = redacted.Value.(map[string]any)
		artifact.RedactionFlags = mergeFlags(artifact.RedactionFlags, redacted.Flags)
	}
	if artifact.ArtifactPath != "" {
		redacted := d.redactor.Redact(artifact.ArtifactPath)
		if path, ok := redacted.Value.(string); ok {
			artifact.ArtifactPath = path
			artifact.RedactionFlags = mergeFlags(artifact.RedactionFlags, redacted.Flags)
		}
	}
	artifact.ContentHash = artifactHash(artifact)

	if err := d.artifactSpool.Enqueue(artifact); err != nil {
		return err
	}
	d.signalFlush()
	return nil
}

func (d *Dispatcher) Flush(ctx context.Context) error {
	d.flushMu.Lock()
	defer d.flushMu.Unlock()

	for {
		batch, err := d.spool.LoadBatch(d.cfg.BatchSize)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}

		events := make([]EventEnvelope, 0, len(batch))
		paths := make([]string, 0, len(batch))
		for _, item := range batch {
			events = append(events, item.Event)
			paths = append(paths, item.Path)
		}
		if err := d.client.Send(ctx, BatchRequest{SentAt: time.Now().UTC(), Events: events}); err != nil {
			return err
		}
		if err := d.spool.Delete(paths); err != nil {
			return err
		}
	}

	for {
		batch, err := d.artifactSpool.LoadBatch(d.cfg.BatchSize)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}

		artifacts := make([]ArtifactEnvelope, 0, len(batch))
		paths := make([]string, 0, len(batch))
		for _, item := range batch {
			artifacts = append(artifacts, item.Artifact)
			paths = append(paths, item.Path)
		}
		if err := d.client.SendArtifacts(ctx, ArtifactBatchRequest{SentAt: time.Now().UTC(), Artifacts: artifacts}); err != nil {
			return err
		}
		if err := d.artifactSpool.Delete(paths); err != nil {
			return err
		}
	}
}

func (d *Dispatcher) Close() error {
	if !d.autoFlush {
		return nil
	}
	d.closeOnce.Do(func() {
		close(d.stopCh)
	})
	<-d.doneCh
	return d.closeErr
}

func (d *Dispatcher) Stats() (DispatcherStats, error) {
	if d == nil {
		return DispatcherStats{}, nil
	}
	eventStats, err := d.spool.Stats()
	if err != nil {
		return DispatcherStats{}, err
	}
	artifactStats, err := d.artifactSpool.Stats()
	if err != nil {
		return DispatcherStats{}, err
	}
	return DispatcherStats{
		Events:    eventStats,
		Artifacts: artifactStats,
	}, nil
}

func eventHash(event EventEnvelope) string {
	copyEvent := event
	copyEvent.ContentHash = ""
	body, _ := json.Marshal(copyEvent)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func artifactHash(artifact ArtifactEnvelope) string {
	copyArtifact := artifact
	copyArtifact.ContentHash = ""
	body, _ := json.Marshal(copyArtifact)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func mergeFlags(left, right []string) []string {
	flags := make(map[string]struct{}, len(left)+len(right))
	for _, flag := range left {
		if strings.TrimSpace(flag) != "" {
			flags[flag] = struct{}{}
		}
	}
	for _, flag := range right {
		if strings.TrimSpace(flag) != "" {
			flags[flag] = struct{}{}
		}
	}
	if len(flags) == 0 {
		return nil
	}
	merged := make([]string, 0, len(flags))
	for flag := range flags {
		merged = append(merged, flag)
	}
	slices.Sort(merged)
	return merged
}
