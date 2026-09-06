// Package toolmetrics provides per-tool execution accounting.
//
// Design constraints (these are load-bearing, do not relax them casually):
//
//  1. The always-on path must be cheap enough to be invisible against the
//     FASTEST tool in the system. TailOutputByID runs in ~4.5us, so the
//     recording path is a handful of atomic adds on an already-resolved
//     *Stat and allocates nothing steady-state.
//
//  2. Nothing is written to disk, ever, and nothing grows without bound.
//     Cardinality is bounded by the number of registered tool names (~70),
//     not by the number of calls. This is deliberate: the predecessor
//     telemetry in this codebase (the token anomaly detector) was removed
//     precisely because it wrote ~6MB per turn unbounded.
//
//  3. Deep attribution (pprof CPU labels, allocation deltas) is OFF unless
//     SWARM_TOOL_PROFILE=1, and allocates nothing when off.
//
// This package intentionally imports only the standard library so it can be
// used from both internal/tools and internal/agent without an import cycle.
package toolmetrics

import (
	"context"
	"encoding/json"
	"expvar"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// deepProfile gates the expensive attribution paths. Read once at init so the
// hot path is a plain bool load rather than a getenv.
var deepProfile = os.Getenv("SWARM_TOOL_PROFILE") == "1"

// DeepProfileEnabled reports whether deep attribution is active.
func DeepProfileEnabled() bool { return deepProfile }

// Latency bucket upper bounds. One bucket is incremented per call, so the
// distribution costs exactly one extra atomic add.
var bucketBounds = [...]time.Duration{
	100 * time.Microsecond,
	time.Millisecond,
	10 * time.Millisecond,
	100 * time.Millisecond,
	time.Second,
	10 * time.Second,
}

// BucketLabels names the buckets, including the implicit overflow bucket.
var BucketLabels = [...]string{
	"<100us", "<1ms", "<10ms", "<100ms", "<1s", "<10s", ">=10s",
}

const numBuckets = len(bucketBounds) + 1

// Stat is the per-tool accumulator. All fields are atomic; a *Stat is created
// once per tool name and then only mutated via atomic adds.
type Stat struct {
	Calls       atomic.Int64
	Errors      atomic.Int64
	TotalNanos  atomic.Int64
	MaxNanos    atomic.Int64
	OutputBytes atomic.Int64

	// AllocBytes is only populated in deep-profile mode. See RecordAlloc for
	// the accuracy caveat under concurrent execution.
	AllocBytes   atomic.Int64
	AllocSamples atomic.Int64

	Buckets [numBuckets]atomic.Int64
}

// stats maps tool name -> *Stat. sync.Map is the right structure here: the
// key set is small and effectively write-once (one store per tool name for
// the process lifetime), while reads happen on every single tool call.
var stats sync.Map

// statFor returns the accumulator for name, creating it on first use.
func statFor(name string) *Stat {
	if v, ok := stats.Load(name); ok {
		return v.(*Stat)
	}
	actual, _ := stats.LoadOrStore(name, &Stat{})
	return actual.(*Stat)
}

// Record accounts for one completed tool execution.
//
// outputBytes is the size of the payload the tool handed back. It is tracked
// because payload size is what downstream rendering and history persistence
// actually pay for, and it is measurable with zero extra work.
func Record(name string, d time.Duration, outputBytes int, isErr bool) {
	s := statFor(name)
	s.Calls.Add(1)
	if isErr {
		s.Errors.Add(1)
	}

	ns := d.Nanoseconds()
	s.TotalNanos.Add(ns)
	if outputBytes > 0 {
		s.OutputBytes.Add(int64(outputBytes))
	}

	// Lock-free running max.
	for {
		cur := s.MaxNanos.Load()
		if ns <= cur || s.MaxNanos.CompareAndSwap(cur, ns) {
			break
		}
	}

	s.Buckets[bucketIndex(d)].Add(1)
}

// RecordAlloc records an allocation delta for a tool call.
//
// ACCURACY CAVEAT: Go exposes no per-goroutine allocation counter, so this is
// a process-wide runtime.MemStats.TotalAlloc delta. It is accurate only when
// the measured call is the dominant allocator during its own window — i.e.
// under the SEQUENTIAL test harness. Under the parallel batch path
// (internal/tools/optimized_runtime.go) concurrently-running tools contaminate
// each other's deltas. Treat live parallel numbers as indicative, not exact.
// This is also why it is gated: ReadMemStats stops the world.
func RecordAlloc(name string, allocBytes uint64) {
	s := statFor(name)
	s.AllocBytes.Add(int64(allocBytes))
	s.AllocSamples.Add(1)
}

func bucketIndex(d time.Duration) int {
	for i, b := range bucketBounds {
		if d < b {
			return i
		}
	}
	return numBuckets - 1
}

// Begin starts attribution for a tool call. The returned function MUST be
// called when the call completes (defer it).
//
// When deep profiling is off this is a no-op returning a nil-safe closure, so
// the always-on cost stays at the atomic adds in Record.
//
// When on, it attaches a pprof goroutine label so CPU profiles break down by
// tool. NOTE: pprof labels are recorded by the CPU profiler ONLY -- the heap
// and allocs profilers do not carry them. That asymmetry is why allocation is
// measured separately via RecordAlloc instead of via labels.
func Begin(ctx context.Context, tool string) (context.Context, func()) {
	if !deepProfile {
		return ctx, func() {}
	}
	labeled := pprof.WithLabels(ctx, pprof.Labels("tool", tool))
	pprof.SetGoroutineLabels(labeled)
	return labeled, func() {
		// Restore whatever labels the goroutine carried before this call so
		// nested tool calls (subagents invoking tools) unwind correctly.
		pprof.SetGoroutineLabels(ctx)
	}
}

// ReadAlloc returns the process cumulative allocation counter, or 0 when deep
// profiling is disabled. Callers pair two reads around a tool call.
//
// This is stop-the-world; it is never called unless deep profiling is on.
func ReadAlloc() uint64 {
	if !deepProfile {
		return 0
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.TotalAlloc
}

// Snapshot is an exported, plain-data view of one tool's accounting.
type Snapshot struct {
	Tool         string  `json:"tool"`
	Calls        int64   `json:"calls"`
	Errors       int64   `json:"errors"`
	TotalMS      float64 `json:"total_ms"`
	MeanMS       float64 `json:"mean_ms"`
	MaxMS        float64 `json:"max_ms"`
	OutputBytes  int64   `json:"output_bytes"`
	MeanOutBytes int64   `json:"mean_output_bytes"`
	AllocBytes   int64   `json:"alloc_bytes,omitempty"`
	AllocSamples int64   `json:"alloc_samples,omitempty"`
	MeanAlloc    int64   `json:"mean_alloc_bytes,omitempty"`

	Buckets map[string]int64 `json:"latency_buckets"`
}

// SnapshotAll returns every tool's accounting, sorted by total time descending
// -- i.e. already ranked as a heatmap.
func SnapshotAll() []Snapshot {
	var out []Snapshot
	stats.Range(func(k, v any) bool {
		name := k.(string)
		s := v.(*Stat)

		calls := s.Calls.Load()
		totalNs := s.TotalNanos.Load()
		outBytes := s.OutputBytes.Load()
		allocBytes := s.AllocBytes.Load()
		allocSamples := s.AllocSamples.Load()

		snap := Snapshot{
			Tool:         name,
			Calls:        calls,
			Errors:       s.Errors.Load(),
			TotalMS:      float64(totalNs) / 1e6,
			MaxMS:        float64(s.MaxNanos.Load()) / 1e6,
			OutputBytes:  outBytes,
			AllocBytes:   allocBytes,
			AllocSamples: allocSamples,
			Buckets:      make(map[string]int64, numBuckets),
		}
		if calls > 0 {
			snap.MeanMS = float64(totalNs) / float64(calls) / 1e6
			snap.MeanOutBytes = outBytes / calls
		}
		if allocSamples > 0 {
			snap.MeanAlloc = allocBytes / allocSamples
		}
		for i := range s.Buckets {
			if c := s.Buckets[i].Load(); c > 0 {
				snap.Buckets[BucketLabels[i]] = c
			}
		}
		out = append(out, snap)
		return true
	})

	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalMS != out[j].TotalMS {
			return out[i].TotalMS > out[j].TotalMS
		}
		// Stable tiebreaker so repeated scrapes do not shuffle equal rows.
		return out[i].Tool < out[j].Tool
	})
	return out
}

// Reset clears all accounting. Used by the test harness to establish a clean
// baseline between measured workloads.
func Reset() {
	stats.Range(func(k, _ any) bool {
		stats.Delete(k)
		return true
	})
}

func init() {
	// Published lazily: the snapshot is only built when /debug/vars is
	// actually scraped, so an unscraped process pays nothing.
	expvar.Publish("swarm_tool_stats", expvar.Func(func() any {
		return SnapshotAll()
	}))
	expvar.Publish("swarm_tool_profile_enabled", expvar.Func(func() any {
		return deepProfile
	}))
}

// MarshalSnapshot renders the current heatmap as indented JSON. Used by the
// offline harness, which has no HTTP server to scrape.
func MarshalSnapshot() ([]byte, error) {
	return json.MarshalIndent(SnapshotAll(), "", "  ")
}
