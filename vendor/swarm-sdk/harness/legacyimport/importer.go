// Package legacyimport implements Phase 11a: read-only import/explain for a
// legacy ConfigBundle (plan.md Phase 11 actions #1-#3). It reads a legacy
// swarm-sdk/internal/configbundle.ConfigBundle file, classifies every
// recognized legacy setting into exactly one of four buckets (MIGRATED /
// LOSSY / UNSUPPORTED / IGNORED — see report.go's Bucket), builds a proposed
// harness.Document from what IS migratable, attempts to compile it through
// the real harness compiler, and returns both the proposal and a
// deterministic migration Report. It never writes anything.
//
// # D1 — absolutely read-only
//
// The only filesystem call in this package's entire Import path is a single
// os.ReadFile of the exact path the caller supplies (see readLegacyBundle).
// There is no os.WriteFile, os.Create, os.Remove, os.Rename, os.MkdirAll, or
// any other mutating call anywhere in this package — grep it yourself:
//
//	grep -n "os\.\(Write\|Create\|Remove\|Rename\|Mkdir\|Truncate\|Symlink\)" *.go
//
// returns nothing. Import returns an in-memory Proposal (a harness.Document
// plus its marshaled JSON) and a Report; if a caller wants either persisted,
// writing those bytes to a NEW path is the caller's own explicit act — this
// package performs no such write itself, by design (see the package's own
// doc comment on Result).
//
// # D2 — total classification
//
// translate (translate.go — same package, split for size) walks every
// section of configbundle.ConfigBundle and emits at least one
// ClassificationEntry naming each field's exact dotted JSON path. A legacy
// key this package's Go types do not even know about (schema drift) is
// rejected before classification ever runs: readLegacyBundle strict-decodes
// with json.Decoder.DisallowUnknownFields, so an unrecognized field fails
// the read loudly (an error naming the file) rather than silently vanishing.
// importer_test.go's TestLegacyFieldInventoryIsFullyClassified independently
// re-enumerates every ConfigBundle JSON field by hand and asserts the union
// of every ClassificationEntry.Source in a maximal fixture's report exactly
// equals that independently authored list.
//
// # D3 — the proposal is always actually compiled
//
// Import ALWAYS calls harness.CompileBytes on the proposal's marshaled JSON
// (see buildAndCompile) and reports the truthful outcome — Report.Compile —
// including the real diagnostics when it fails to compile. No YAML/JSON is
// ever handed to a caller without having gone through that exact call.
//
// # D4 — rollback is a plan, not a backup
//
// Report.Rollback (report.go's RollbackPlan) is built by hashing the exact
// bytes this package already read for classification (see readLegacyBundle)
// — no copy of the legacy file is made or retained beyond the current
// process's memory.
//
// # D5 — secrets never leave this package
//
// Every legacy string value that could plausibly be a secret (provider API
// keys, OAuth tokens, MCP auth tokens/header values, hook/mcp environment
// VALUES) is collected into translation.sensitive as it is read and is used
// ONLY for the defense-in-depth check in assertNoSecretLeak, which scans the
// final marshaled proposal JSON and the final rendered report (both text and
// JSON) for every collected value and returns a hard error — refusing to
// return a Result at all — if any is found. The proposal and report
// themselves are built to carry only REFERENCES (harness.Ref{Env: ...}) and
// EXISTENCE/PROVENANCE facts, never literal legacy secret values; see
// translate.go's credential-handling code for where those references are
// synthesized.
package legacyimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// ImportInput names the single legacy file to read and the small amount of
// caller-controlled, non-secret context that affects the proposal/report.
type ImportInput struct {
	// ConfigBundlePath is the exact path to a legacy ConfigBundle JSON file.
	// Required. Never resolved against a default/home-dir location, and
	// never walked for (D1) — this package reads exactly this one file.
	ConfigBundlePath string
	// Name, when non-empty, overrides the proposal's metadata.name (which
	// otherwise falls back to the legacy bundle's own Name, and then to a
	// fixed literal — see translateMetadata).
	Name string
	// Timestamp, when non-empty, is carried verbatim into Report.GeneratedAt.
	// This package never calls time.Now() itself (no clock-dependent
	// output); a caller that wants a timestamped report supplies one.
	Timestamp string
}

// Proposal is the in-memory harness manifest this package proposes. JSON is
// the exact bytes handed to harness.CompileBytes (D3) — never a
// re-serialization that could drift from what was actually compiled.
type Proposal struct {
	Document harness.Document
	JSON     []byte
}

// Result is Import's full, in-memory output. Plan is nil when
// Report.Compile.Compiled is false (D3: a Plan is only ever populated from a
// real, successful CompileBytes call — never fabricated).
//
// Result is never written to disk by this package (D1). A caller who wants
// to persist Proposal.JSON or Report must do so explicitly, and must write
// it to a NEW path — never over an existing legacy store file.
type Result struct {
	Proposal Proposal
	Report   Report
	Plan     *harness.Plan
}

// Import reads, classifies, and translates exactly one legacy ConfigBundle
// file, then attempts to compile the resulting proposal (D3). It returns an
// error only for a fatal, fail-closed condition (missing/unreadable/
// malformed input, or the D5 leak guard tripping); a legacy config that
// cannot itself compile into a valid harness manifest is NOT an error — it
// is a fully truthful Result whose Report.Compile.Compiled is false.
func Import(input ImportInput) (*Result, error) {
	if input.ConfigBundlePath == "" {
		return nil, errors.New("legacyimport: ImportInput.ConfigBundlePath must be set")
	}

	raw, bundle, err := readLegacyBundle(input.ConfigBundlePath)
	if err != nil {
		return nil, err
	}

	tr := newTranslation()
	tr.translate(bundle, input)

	docJSON, err := json.Marshal(&tr.doc)
	if err != nil {
		return nil, fmt.Errorf("legacyimport: internal error marshaling proposal: %w", err)
	}

	outcome, plan := compileProposal(docJSON)

	hash, size := hashBytes(raw)
	rollback := RollbackPlan{
		Note: rollbackPlanNote,
		Files: []RollbackFileEntry{
			{Order: 0, Path: input.ConfigBundlePath, ContentHash: hash, SizeBytes: size},
		},
	}

	report := Report{
		GeneratedAt:      input.Timestamp,
		LegacySourcePath: input.ConfigBundlePath,
		BundleName:       bundle.Name,
		Classification:   tr.classification,
		Synthesized:      tr.synthesized,
		Compile:          outcome,
		Rollback:         rollback,
	}

	reportJSON, jerr := report.RenderJSON()
	if jerr != nil {
		return nil, fmt.Errorf("legacyimport: internal error rendering report: %w", jerr)
	}
	reportText := report.Render()
	if err := assertNoSecretLeak(tr.sensitive, docJSON, reportJSON, []byte(reportText)); err != nil {
		return nil, err
	}

	result := &Result{
		Proposal: Proposal{Document: tr.doc, JSON: docJSON},
		Report:   report,
	}
	if outcome.Compiled {
		result.Plan = plan
	}
	return result, nil
}

// readLegacyBundle performs the ONLY filesystem access in this package's
// Import path: a single os.ReadFile of the exact caller-supplied path,
// followed by a strict decode. It never writes, and it never reads any
// other file (D1).
func readLegacyBundle(path string) ([]byte, *configbundle.ConfigBundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("legacyimport: legacy config bundle %q does not exist", path)
		}
		return nil, nil, fmt.Errorf("legacyimport: legacy config bundle %q could not be read: %w", path, err)
	}
	bundle, err := strictDecodeConfigBundle(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("legacyimport: legacy config bundle %q is malformed: %w", path, err)
	}
	return raw, bundle, nil
}

// strictDecodeConfigBundle decodes raw JSON into a configbundle.ConfigBundle
// with DisallowUnknownFields, so a legacy key this package's classifier does
// not know about fails the read loudly instead of being silently dropped
// (D2's "a legacy key that matches no bucket must FAIL the import loudly").
// Trailing data after the one JSON value is likewise rejected, mirroring
// harness's own strictDecode discipline (harness/strict_decode.go).
func strictDecodeConfigBundle(raw []byte) (*configbundle.ConfigBundle, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var bundle configbundle.ConfigBundle
	if err := dec.Decode(&bundle); err != nil {
		return nil, err
	}
	var trailing json.RawMessage
	if derr := dec.Decode(&trailing); !errors.Is(derr, io.EOF) {
		return nil, errors.New("unexpected trailing data after the config bundle JSON document")
	}
	return &bundle, nil
}

// compileProposal ALWAYS attempts the real harness compiler (D3) and returns
// a truthful outcome either way; it never fabricates a digest or a
// diagnostic list.
func compileProposal(docJSON []byte) (CompileOutcome, *harness.Plan) {
	plan, err := harness.CompileBytes(docJSON, "legacyimport-proposal.json")
	if err == nil {
		return CompileOutcome{Compiled: true, Digest: plan.Digest()}, plan
	}
	outcome := CompileOutcome{Compiled: false}
	if ds, ok := harness.AsDiagnostics(err); ok {
		for _, d := range ds {
			outcome.Diagnostics = append(outcome.Diagnostics, d.Error())
		}
	} else {
		outcome.Diagnostics = []string{err.Error()}
	}
	return outcome, nil
}

// hashBytes returns the D4 rollback content hash ("sha256:<hex>") and size
// of raw, without retaining raw itself beyond the caller's own scope.
func hashBytes(raw []byte) (string, int64) {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), int64(len(raw))
}

// assertNoSecretLeak is the D5 defense-in-depth guarantee: it scans every
// haystack (the proposal JSON, the rendered report JSON, the rendered report
// text) for every collected legacy secret value and fails closed — returning
// a hard error instead of a Result — if any is found. This makes a D5
// regression a build-breaking test failure rather than a silent leak: see
// importer_test.go's TestNoSecretLeak, which plants sentinel values and
// exercises exactly this path.
func assertNoSecretLeak(sensitive []string, haystacks ...[]byte) error {
	for _, s := range sensitive {
		if s == "" {
			continue
		}
		needle := []byte(s)
		for _, h := range haystacks {
			if bytes.Contains(h, needle) {
				return errors.New("legacyimport: internal error: a legacy secret value leaked into generated output (D5 violation); refusing to return a result")
			}
		}
	}
	return nil
}
