package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

const (
	modelsCacheFileName = "codex_models_cache.json"

	// DefaultModelsCacheTTL mirrors codex-rs DEFAULT_MODEL_CACHE_TTL.
	DefaultModelsCacheTTL = 5 * time.Minute
)

// modelsCache is the on-disk catalog snapshot. It is deliberately not keyed
// by provider/base URL (same acknowledged trade-off as codex-rs
// models-manager) because swarm only fetches against one codex base URL.
type modelsCache struct {
	FetchedAt     time.Time      `json:"fetched_at"`
	ETag          string         `json:"etag,omitempty"`
	ClientVersion string         `json:"client_version"`
	Models        []CatalogModel `json:"models"`
}

// ModelsCachePath returns the catalog cache location under the swarm home.
func ModelsCachePath() (string, error) {
	// Root-level cache under the canonical swarm home (~/.swarm/<file>).
	return paths.In(modelsCacheFileName), nil
}

// LoadFreshModels returns cached catalog models when the cache exists, was
// written by the same pinned client version, and is younger than ttl.
// A missing, stale, or corrupt cache returns (nil, false).
func LoadFreshModels(ttl time.Duration) ([]CatalogModel, bool) {
	path, err := ModelsCachePath()
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var cache modelsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, false
	}
	if cache.ClientVersion != codexClientVersion {
		return nil, false
	}
	if time.Since(cache.FetchedAt) > ttl {
		return nil, false
	}
	if len(cache.Models) == 0 {
		return nil, false
	}
	return cache.Models, true
}

// loadStaleModels returns whatever the cache holds regardless of TTL, for
// network-failure fallback. Client-version mismatches still invalidate.
func loadStaleModels() ([]CatalogModel, bool) {
	models, ok := LoadFreshModels(1<<62 - 1)
	return models, ok
}

// PersistModels writes the catalog snapshot atomically (temp file + rename +
// dir fsync) via atomicfile, serialized across processes with an advisory lock.
func PersistModels(models []CatalogModel, etag string) error {
	path, err := ModelsCachePath()
	if err != nil {
		return err
	}
	cache := modelsCache{
		FetchedAt:     time.Now(),
		ETag:          etag,
		ClientVersion: codexClientVersion,
		Models:        models,
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal models cache: %w", err)
	}
	if err := atomicfile.WithLock(path, func() error {
		return atomicfile.Write(path, data, atomicfile.WithPerm(0o600))
	}); err != nil {
		return fmt.Errorf("failed to persist models cache: %w", err)
	}
	return nil
}

// ModelWireOptionsFromCache returns per-model request conventions derived
// from the cached catalog regardless of TTL (wire conventions change only
// with the catalog itself). Returns nil when no usable cache exists, in
// which case the provider falls back to its prefix heuristics.
func ModelWireOptionsFromCache() map[string]ModelWireOptions {
	models, ok := loadStaleModels()
	if !ok {
		return nil
	}
	out := make(map[string]ModelWireOptions, len(models))
	for _, m := range models {
		out[m.Slug] = ModelWireOptions{
			ResponsesLite:     m.UseResponsesLite,
			ParallelToolCalls: m.SupportsParallelToolCalls,
			SupportsMaxEffort: m.SupportsMaxEffort(),
		}
	}
	return out
}

// FetchModelsCached returns the codex model catalog with
// online-if-uncached semantics: a fresh cache short-circuits the network;
// otherwise the catalog is fetched, persisted, and returned. On network
// failure a stale cache (any age, same client version) is returned instead,
// with the fetch error joined only when no fallback exists.
func FetchModelsCached(ctx context.Context, opts ListModelsOptions) ([]CatalogModel, error) {
	if models, ok := LoadFreshModels(DefaultModelsCacheTTL); ok {
		return models, nil
	}

	models, etag, err := ListModels(ctx, opts)
	if err != nil {
		if stale, ok := loadStaleModels(); ok {
			return stale, nil
		}
		return nil, err
	}
	if len(models) == 0 {
		if stale, ok := loadStaleModels(); ok {
			return stale, nil
		}
		return nil, errors.New("codex models response contained no models")
	}
	if err := PersistModels(models, etag); err != nil {
		// A cache write failure must not hide a successful fetch.
		return models, nil
	}
	return models, nil
}
