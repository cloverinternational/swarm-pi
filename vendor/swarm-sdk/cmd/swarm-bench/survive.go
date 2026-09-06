// survive.go — `swarm-bench survive`: the point of the whole ledger
// (PLAN.md Phase B). For each recorded blob, ask git the deterministic
// question "did this content ever reach history?" via
// `git log --all --find-object=<blob>`.
//
// # The one subprocess in this tool
//
// gitFindObject below is the ONLY call to exec.Command anywhere in
// internal/bench + cmd/swarm-bench. It is read-only: `git log` never
// writes to refs, the index, or the working tree, and every argument this
// function passes is either a fixed flag or a 40/64-hex blob hash already
// validated by looksLikeGitHash — there is no path through this function
// that lets ledger content reach a shell.
//
// # No verdicts, no pass rate
//
// This file computes exactly three counts over ESTABLISHED blobs (non-empty
// PostBlob): in history, not in history, and query-error — plus a fourth,
// disjoint count of NOT-ESTABLISHED effects (empty PostBlob) that is never
// folded into the other three. The one ratio this file prints is labelled
// "survival ratio" and is always printed adjacent to the not-established
// count, per the task's explicit requirement ("so silence is visible").
// Nothing here is named or shaped like Pass/Fail/Score/Grade — see
// effect.go's doc comment on why Effect itself cannot carry one.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/bench"
)

const maxSampleEffectsPerBlob = 3

// blobGroup aggregates every matched, established (non-empty PostBlob)
// effect that shares one content hash. SampleEffects is bounded regardless
// of how many rows share the blob, so this stays O(distinct blobs) in
// memory, not O(rows).
type blobGroup struct {
	Blob          string         `json:"blob"`
	Count         int            `json:"count"`
	SampleEffects []bench.Effect `json:"sample_effects"`
	InHistory     bool           `json:"in_history"`
	Commits       []string       `json:"commits,omitempty"`
	QueryError    string         `json:"query_error,omitempty"`
}

type surviveReport struct {
	Bucket            string         `json:"bucket"`
	LedgerDir         string         `json:"ledger_dir"`
	Repo              string         `json:"repo"`
	RowsScanned       int            `json:"rows_scanned"`
	FilesRead         int            `json:"files_read"`
	LinesSkipped      int            `json:"lines_skipped"`
	ScanCapped        bool           `json:"scan_capped"`
	NotEstablished    []bench.Effect `json:"not_established"`
	Established       []*blobGroup   `json:"established_blobs"`
	InHistoryCount    int            `json:"in_history_count"`
	NotInHistoryCount int            `json:"not_in_history_count"`
	QueryErrorCount   int            `json:"query_error_count"`
	// SurvivalRatioNote documents, in the JSON itself, what the ratio
	// derived from InHistoryCount/(InHistoryCount+NotInHistoryCount) means
	// and does not mean — so a consumer of -json output cannot reasonably
	// mistake it for a pass rate either.
	SurvivalRatioNote string `json:"survival_ratio_note"`
}

func runSurvive(args []string) {
	fs := flag.NewFlagSet("survive", flag.ExitOnError)
	var c commonFlags
	addCommonFlags(fs, &c)
	repoFlag := fs.String("repo", "", "git repository to query for survival (default: -dir)")
	gitTimeout := fs.Duration("git-timeout", 10*time.Second, "timeout for each 'git log --find-object' invocation")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: swarm-bench survive [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	repoDir := *repoFlag
	if repoDir == "" {
		repoDir = c.dir
	}

	res, err := scanLedger(&c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "swarm-bench survive:", err)
		os.Exit(1)
	}

	if len(res.matched) == 0 {
		if c.jsonOut {
			fmt.Println("{}")
			return
		}
		fmt.Printf("nothing recorded (bucket %s under %s; scanned %d row(s) across %d file(s))\n",
			res.bucket, res.baseDir, res.readStats.RowsRead, res.readStats.FilesRead)
		return
	}

	if err := preflightGitRepo(repoDir); err != nil {
		fmt.Fprintf(os.Stderr, "swarm-bench survive: %s is not a usable git repository: %v\n", repoDir, err)
		os.Exit(1)
	}

	notEstablished, groups, order := groupByBlob(res.matched)

	report := &surviveReport{
		Bucket:         res.bucket,
		LedgerDir:      res.bucketDir,
		Repo:           repoDir,
		RowsScanned:    res.readStats.RowsRead,
		FilesRead:      res.readStats.FilesRead,
		LinesSkipped:   res.readStats.LinesSkipped,
		ScanCapped:     res.scanCapped,
		NotEstablished: notEstablished,
		SurvivalRatioNote: "in_history_count / (in_history_count + not_in_history_count) over ESTABLISHED " +
			"blobs only (non-empty PostBlob). This is a fact about git history, not a correctness " +
			"verdict: it never includes not_established rows, and a low ratio means content was " +
			"discarded/reverted/never-committed, not that the agent's work was wrong. See PLAN.md §2/§3/§6.",
	}

	for _, blob := range order {
		g := groups[blob]
		commits, gerr := gitFindObject(repoDir, blob, *gitTimeout)
		if gerr != nil {
			g.QueryError = gerr.Error()
			report.QueryErrorCount++
		} else if len(commits) > 0 {
			g.InHistory = true
			g.Commits = commits
			report.InHistoryCount++
		} else {
			report.NotInHistoryCount++
		}
		report.Established = append(report.Established, g)
	}

	if c.jsonOut {
		printJSON(report)
		return
	}
	printSurviveReport(report)
}

// groupByBlob splits matched effects into the "not established" bucket
// (empty PostBlob — PLAN.md §2: missing evidence, never folded into a
// success/failure count) and one blobGroup per distinct non-empty PostBlob.
func groupByBlob(matched []bench.Effect) (notEstablished []bench.Effect, groups map[string]*blobGroup, order []string) {
	groups = map[string]*blobGroup{}
	for _, eff := range matched {
		if eff.PostBlob == "" {
			notEstablished = append(notEstablished, eff)
			continue
		}
		g, ok := groups[eff.PostBlob]
		if !ok {
			g = &blobGroup{Blob: eff.PostBlob}
			groups[eff.PostBlob] = g
			order = append(order, eff.PostBlob)
		}
		g.Count++
		if len(g.SampleEffects) < maxSampleEffectsPerBlob {
			g.SampleEffects = append(g.SampleEffects, eff)
		}
	}
	sort.Strings(order)
	return notEstablished, groups, order
}

func printSurviveReport(r *surviveReport) {
	fmt.Printf("=== swarm-bench survive ===\n")
	fmt.Printf("bucket:       %s\n", r.Bucket)
	fmt.Printf("ledger dir:   %s\n", r.LedgerDir)
	fmt.Printf("repo:         %s\n", r.Repo)
	fmt.Printf("rows scanned: %d (%d file(s)", r.RowsScanned, r.FilesRead)
	if r.LinesSkipped > 0 {
		fmt.Printf(", %d unparsable line(s) skipped", r.LinesSkipped)
	}
	if r.ScanCapped {
		fmt.Printf(", scan stopped early — results may be incomplete")
	}
	fmt.Printf(")\n\n")

	fmt.Printf("NOT ESTABLISHED (empty PostBlob — e.g. delete ops, or evidence never captured): %d\n", len(r.NotEstablished))
	fmt.Printf("  These are excluded from the survival ratio below (PLAN.md §2: missing evidence -> Undetermined, never Fail).\n")
	if len(r.NotEstablished) > 0 {
		w := newTabWriter()
		fmt.Fprintln(w, "  TS\tTOOL\tOP\tPATH")
		limit := len(r.NotEstablished)
		if limit > 10 {
			limit = 10
		}
		for _, eff := range r.NotEstablished[:limit] {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", eff.TS.UTC().Format(time.RFC3339), orDash(eff.Tool), orDash(eff.Op), orDash(eff.Path))
		}
		_ = w.Flush()
		if len(r.NotEstablished) > limit {
			fmt.Printf("  ... and %d more\n", len(r.NotEstablished)-limit)
		}
	}
	fmt.Println()

	established := len(r.Established)
	fmt.Printf("ESTABLISHED (distinct non-empty PostBlob values): %d\n", established)
	fmt.Printf("  in history (git log --all --find-object found >=1 commit): %d\n", r.InHistoryCount)
	fmt.Printf("  NOT in history (blob absent from every reachable ref):      %d\n", r.NotInHistoryCount)
	if r.QueryErrorCount > 0 {
		fmt.Printf("  git query errors (could not determine either way):        %d\n", r.QueryErrorCount)
	}
	fmt.Println()

	denom := r.InHistoryCount + r.NotInHistoryCount
	fmt.Println("SURVIVAL RATIO (established blobs only — NOT a pass rate, NOT a correctness score):")
	if denom == 0 {
		fmt.Println("  n/a (no established blob could be queried)")
	} else {
		pct := float64(r.InHistoryCount) / float64(denom) * 100
		fmt.Printf("  %d/%d established blobs (%.1f%%) reached git history\n", r.InHistoryCount, denom, pct)
	}
	fmt.Printf("  %d effect(s) are NOT ESTABLISHED and are excluded from this ratio (see above) — silence stays visible.\n\n", len(r.NotEstablished))

	if r.NotInHistoryCount > 0 {
		fmt.Println("--- blobs NOT found in git history (discarded / reverted / never committed) ---")
		w := newTabWriter()
		fmt.Fprintln(w, "BLOB\tCOUNT\tSAMPLE_PATH\tSAMPLE_TOOL")
		for _, g := range r.Established {
			if g.InHistory || g.QueryError != "" {
				continue
			}
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", shortHash(g.Blob), g.Count, samplePath(g), sampleTool(g))
		}
		_ = w.Flush()
		fmt.Println()
	}

	if r.InHistoryCount > 0 {
		fmt.Println("--- blobs found in git history ---")
		w := newTabWriter()
		fmt.Fprintln(w, "BLOB\tCOUNT\tSAMPLE_PATH\tCOMMIT(S)")
		for _, g := range r.Established {
			if !g.InHistory {
				continue
			}
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", shortHash(g.Blob), g.Count, samplePath(g), strings.Join(shortenAll(g.Commits), ","))
		}
		_ = w.Flush()
	}

	if r.QueryErrorCount > 0 {
		fmt.Println("\n--- blobs the git query could not resolve (error, not a verdict) ---")
		for _, g := range r.Established {
			if g.QueryError == "" {
				continue
			}
			fmt.Printf("  %s: %s\n", shortHash(g.Blob), g.QueryError)
		}
	}
}

func samplePath(g *blobGroup) string {
	if len(g.SampleEffects) == 0 {
		return "-"
	}
	return g.SampleEffects[0].Path
}

func sampleTool(g *blobGroup) string {
	if len(g.SampleEffects) == 0 {
		return "-"
	}
	return orDash(g.SampleEffects[0].Tool)
}

func shortenAll(hashes []string) []string {
	out := make([]string, len(hashes))
	for i, h := range hashes {
		out[i] = shortHash(h)
	}
	return out
}

// preflightGitRepo checks once, up front, that repoDir is inside a usable
// git repository — so a bad -repo/-dir produces one clear error instead of
// N identical per-blob failures.
func preflightGitRepo(repoDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--git-dir")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// looksLikeGitHash is a defensive check before a hash-shaped value ever
// reaches exec.Command's argv: git blob hashes are 40 (sha1) or 64 (sha256
// object format) lowercase hex characters, and nothing this tool has ever
// read from the ledger should be anything else. This is belt-and-suspenders
// (Effect.PostBlob is only ever produced by blobHash/BlobHash or a
// caller-supplied hash of the same shape) — it exists so a corrupted or
// hand-edited ledger line can never inject an extra flag or path into the
// git invocation below.
func looksLikeGitHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// gitFindObject runs `git -C repoDir log --all --find-object=<blob>
// --pretty=format:%H` — the ground-truth query PLAN.md §3 specifies
// verbatim ("git log --all --find-object") — and returns the distinct
// commit SHAs it reports. This is the ONLY call to exec.Command in this
// whole tool. It is read-only: `git log` cannot mutate refs, the index, or
// the working tree, and every path through this function is bounded by a
// context timeout so a pathological repository cannot hang the CLI.
func gitFindObject(repoDir, blob string, timeout time.Duration) ([]string, error) {
	if !looksLikeGitHash(blob) {
		return nil, fmt.Errorf("refusing to query a value that is not a git object hash: %q", blob)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "log", "--all",
		"--find-object="+blob, "--pretty=format:%H")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("git log timed out after %s", timeout)
		}
		return nil, fmt.Errorf("git log --find-object=%s: %w: %s", blob, err, strings.TrimSpace(stderr.String()))
	}

	var commits []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(&stdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		commits = append(commits, line)
	}
	return commits, nil
}
