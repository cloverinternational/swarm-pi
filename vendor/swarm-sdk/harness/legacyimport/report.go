// Package legacyimport — Phase 11a report types (plan.md Phase 11 action #2:
// "produce a proposed harness and migration report; never silently rewrite
// existing stores").
//
// This file defines the OUTPUT SHAPE only: Report, ClassificationEntry,
// RollbackPlan. Nothing in this file reads a file, writes a file, or
// constructs a harness Plan/client/provider — see importer.go for that.
//
// # D2 — total classification, one of exactly four buckets
//
// Bucket is a CLOSED four-value set (MIGRATED / LOSSY / UNSUPPORTED /
// IGNORED). Every legacy ConfigBundle field the importer recognizes is
// placed in at least one ClassificationEntry naming that field's exact
// dotted JSON path (see legacyFieldInventory in importer_test.go for the
// independently authored completeness check). A legacy key this package does
// not recognize at all is never silently absorbed into any bucket — it fails
// the read itself (strict decode, importer.go) before classification runs.
//
// # D4 — a rollback PLAN, never a performed backup
//
// RollbackPlan is deliberately named *Plan* (not *Backup*, not *Snapshot*):
// D1 forbids this package from writing anything, so it can only describe
// what an operator would need to restore their previous state (paths +
// content hashes + ordering) — it never performs that restore and never
// copies a single byte of the legacy store anywhere. RollbackFileEntry.
// ContentHash is computed by HASHING the legacy file this package already
// read for classification; no separate copy is made.
//
// # D5 — secrets never appear here
//
// Every string field on Report and its nested types is either a structural
// label (a dotted field path, a bucket name, a target manifest path) or a
// human-authored justification string written by THIS package's own code
// (never copied from a legacy VALUE). Report.Classification's Reason/Target
// fields describe *shape*, not *content*. See importer.go's
// assertNoSecretLeak for the runtime guarantee backing this claim.
package legacyimport

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Bucket is the closed D2 classification set. There are exactly four values;
// a fifth is never introduced without also updating every consumer that
// switches on it exhaustively (see Report.BucketCounts).
type Bucket string

const (
	// BucketMigrated: expressible in the harness manifest as-is (or via a
	// lossless structural translation); the proposal carries the full value.
	BucketMigrated Bucket = "MIGRATED"
	// BucketLossy: partially expressible; ClassificationEntry.Reason states
	// precisely what is preserved and what is dropped.
	BucketLossy Bucket = "LOSSY"
	// BucketUnsupported: the harness has no way to express this setting at
	// all. ClassificationEntry.Reason cites the Phase 10a audit finding or
	// the Phase 10c PresetShortfallEntry that explains why, when one exists.
	BucketUnsupported Bucket = "UNSUPPORTED"
	// BucketIgnored: deliberately not migrated (host-only/presentation, or a
	// legacy-layering artifact meaningless in a single flat document).
	// ClassificationEntry.Reason justifies the omission.
	BucketIgnored Bucket = "IGNORED"
)

// AllBuckets is the closed, ordered bucket list used for deterministic
// report rendering (never derived from map iteration).
func AllBuckets() []Bucket {
	return []Bucket{BucketMigrated, BucketLossy, BucketUnsupported, BucketIgnored}
}

// ClassificationEntry places one legacy ConfigBundle setting — or, for a
// list-shaped legacy section (for example tools.enabled, hooks.definitions),
// one ITEM within that section — into exactly one bucket (D2).
//
// Source is the exact dotted JSON field path(s) from the ConfigBundle root
// this entry classifies (for example "system.defaultProvider" or
// "tools.enabled"). Multiple ClassificationEntry values MAY share the same
// Source when a single legacy list field contains items that land in
// different buckets (for example tools.enabled containing both a
// catalog-known and an unrecognized tool name) — Source names the LEGACY
// FIELD, not a claim of one-entry-per-field.
//
// Item, when non-empty, names the specific element within Source this entry
// describes (for example a tool name or a hook id), so a reader can tell
// which of several same-Source entries they are looking at without parsing
// Reason text.
type ClassificationEntry struct {
	Source []string `json:"source"`
	Item   string   `json:"item,omitempty"`
	Bucket Bucket   `json:"bucket"`
	// Target is the harness manifest path/expression the setting was mapped
	// to (MIGRATED/LOSSY only; empty for UNSUPPORTED/IGNORED).
	Target string `json:"target,omitempty"`
	// Reason is a human-authored justification. It NEVER contains a legacy
	// VALUE (see the package doc's D5 note) — only field/shape descriptions.
	Reason string `json:"reason"`
}

// sourceKey returns a stable, comparable string for Source, used only for
// deterministic sorting within a report render.
func (c ClassificationEntry) sourceKey() string {
	return strings.Join(c.Source, ",")
}

// RollbackFileEntry is ONE file an operator would need to preserve/restore to
// undo an adoption of the proposed harness manifest. It is produced by
// HASHING a file this package already read (D1); no copy is made.
type RollbackFileEntry struct {
	// Order is the 0-based restore ordering (lower first); stable and
	// meaningful only when more than one file participates.
	Order int `json:"order"`
	// Path is the legacy file's path exactly as supplied to Import (D1: never
	// a path this package invented or normalized against the working
	// directory).
	Path string `json:"path"`
	// ContentHash is "sha256:<hex>" of the file's content AT IMPORT TIME, so
	// later drift between this recorded hash and the file's live content is
	// detectable without this package retaining the content itself.
	ContentHash string `json:"contentHash"`
	// SizeBytes is the file's size at import time (a cheap corroborating
	// signal alongside ContentHash).
	SizeBytes int64 `json:"sizeBytes"`
}

// RollbackPlan is deliberately NOT a performed backup (D4 — see the package
// doc). Note is always the fixed sentence below so a caller cannot mistake a
// serialized RollbackPlan for a completed action by omitting it.
type RollbackPlan struct {
	// Note is a fixed, non-empty sentence stating this is a plan, not a
	// performed backup. See rollbackPlanNote.
	Note string `json:"note"`
	// Files lists, in restore order, every legacy file an operator would need
	// to preserve to roll back. This package never copies, moves, or deletes
	// any of them.
	Files []RollbackFileEntry `json:"files"`
}

// rollbackPlanNote is the fixed D4 sentence: it names this a PLAN and states
// plainly that no backup was performed.
const rollbackPlanNote = "This is a ROLLBACK PLAN, not a performed backup: legacyimport is read-only " +
	"(D1) and has not copied, moved, or modified any file listed below. An operator who wants an actual " +
	"backup must copy these paths themselves before adopting the proposed manifest; ContentHash/SizeBytes " +
	"let that copy (or the untouched original) be verified against this plan later."

// CompileOutcome carries the truthful result of attempting to compile the
// proposed manifest through the real harness compiler (D3). It is never
// fabricated: Report.Compile is only ever populated by actually calling
// harness.CompileBytes on the built proposal.
type CompileOutcome struct {
	// Compiled reports whether harness.CompileBytes succeeded.
	Compiled bool `json:"compiled"`
	// Digest is the compiled Plan's digest (Plan.Digest()) when Compiled is
	// true; empty otherwise. It is non-secret (see harness/plan.go).
	Digest string `json:"digest,omitempty"`
	// Diagnostics lists every harness.Diagnostic.Error() string returned by
	// CompileBytes when Compiled is false. Diagnostics are safe to render
	// verbatim: harness.Diagnostic never carries a resolved secret or a
	// resolved prompt body (see harness/diagnostics.go).
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// Report is the complete, deterministic Phase 11a migration report (D2/D3/D4
// combined). Two Report values built from byte-identical input (including an
// identical, explicitly supplied GeneratedAt) render byte-identical output —
// see Render/RenderJSON.
type Report struct {
	// GeneratedAt is caller-supplied (see ImportInput.Timestamp) and is
	// empty when the caller supplies none, which keeps rendering
	// reproducible by construction rather than by accident (no time.Now()
	// anywhere in this package).
	GeneratedAt string `json:"generatedAt,omitempty"`
	// LegacySourcePath is the exact path Import was given for the legacy
	// ConfigBundle (D1: never resolved against a default/home-dir location
	// by this package).
	LegacySourcePath string `json:"legacySourcePath"`
	// BundleName is configbundle.ConfigBundle.Name, when non-empty (a
	// non-secret display label read from the legacy store).
	BundleName string `json:"bundleName,omitempty"`
	// Classification is the full D2 total-classification table, in a fixed
	// deterministic order (see buildReport).
	Classification []ClassificationEntry `json:"classification"`
	// Synthesized documents importer-GENERATED content that has no legacy
	// source at all (for example a placeholder agent.systemPrompt when no
	// legacy default prompt exists) — kept separate from Classification
	// because it is not a translation of any legacy field.
	Synthesized []string `json:"synthesized,omitempty"`
	// Compile is the real, attempted CompileBytes outcome for the proposal
	// this report describes (D3).
	Compile CompileOutcome `json:"compile"`
	// Rollback is the D4 rollback plan (never a performed backup).
	Rollback RollbackPlan `json:"rollback"`
}

// BucketCount pairs one bucket with its entry count (used by
// Report.BucketCounts for deterministic, non-map rendering).
type BucketCount struct {
	Bucket Bucket `json:"bucket"`
	Count  int    `json:"count"`
}

// BucketCounts returns the number of ClassificationEntry values in each
// bucket, in AllBuckets order (never map iteration).
func (r Report) BucketCounts() []BucketCount {
	counts := map[Bucket]int{}
	for _, e := range r.Classification {
		counts[e.Bucket]++
	}
	out := make([]BucketCount, 0, len(AllBuckets()))
	for _, b := range AllBuckets() {
		out = append(out, BucketCount{Bucket: b, Count: counts[b]})
	}
	return out
}

// sortedClassification returns a defensive, deterministically ordered copy:
// by Source key, then Item, then Bucket. The build side already appends in a
// fixed section order, but sorting here makes Render/RenderJSON's
// determinism independent of that build-side discipline too.
func (r Report) sortedClassification() []ClassificationEntry {
	out := make([]ClassificationEntry, len(r.Classification))
	copy(out, r.Classification)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].sourceKey() != out[j].sourceKey() {
			return out[i].sourceKey() < out[j].sourceKey()
		}
		if out[i].Item != out[j].Item {
			return out[i].Item < out[j].Item
		}
		return out[i].Bucket < out[j].Bucket
	})
	return out
}

// Render produces a deterministic, human-readable report. Identical Report
// values (including GeneratedAt) always render identical bytes.
func (r Report) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Legacy import report\n")
	fmt.Fprintf(&b, "  source:       %s\n", r.LegacySourcePath)
	if r.BundleName != "" {
		fmt.Fprintf(&b, "  bundle name:  %s\n", r.BundleName)
	}
	if r.GeneratedAt != "" {
		fmt.Fprintf(&b, "  generated at: %s\n", r.GeneratedAt)
	}
	b.WriteString("\nClassification counts:\n")
	for _, c := range r.BucketCounts() {
		fmt.Fprintf(&b, "  %-12s %d\n", c.Bucket, c.Count)
	}
	b.WriteString("\nClassification:\n")
	for _, e := range r.sortedClassification() {
		item := e.Item
		if item == "" {
			item = "-"
		}
		target := e.Target
		if target == "" {
			target = "-"
		}
		fmt.Fprintf(&b, "  [%s] %s / %s -> %s\n      %s\n",
			e.Bucket, strings.Join(e.Source, "+"), item, target, e.Reason)
	}
	if len(r.Synthesized) > 0 {
		b.WriteString("\nSynthesized (no legacy source):\n")
		for _, s := range r.Synthesized {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
	}
	b.WriteString("\nProposal compile result:\n")
	if r.Compile.Compiled {
		fmt.Fprintf(&b, "  COMPILED (digest=%s)\n", r.Compile.Digest)
	} else {
		b.WriteString("  DID NOT COMPILE:\n")
		for _, d := range r.Compile.Diagnostics {
			fmt.Fprintf(&b, "    - %s\n", d)
		}
	}
	b.WriteString("\n" + r.Rollback.Note + "\n")
	for _, f := range r.Rollback.Files {
		fmt.Fprintf(&b, "  [%d] %s  %s  %d bytes\n", f.Order, f.Path, f.ContentHash, f.SizeBytes)
	}
	return b.String()
}

// RenderJSON produces a deterministic JSON rendering (sorted classification,
// two-space indent). Every nested type is a slice/struct — never a bare
// map[string]any — so encoding/json's key ordering can never introduce
// nondeterminism.
func (r Report) RenderJSON() ([]byte, error) {
	ordered := r
	ordered.Classification = r.sortedClassification()
	return json.MarshalIndent(ordered, "", "  ")
}
