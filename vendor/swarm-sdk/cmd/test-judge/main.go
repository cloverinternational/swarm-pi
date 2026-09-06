// cmd/test-judge audits the honesty of a Go test suite.
//
// It runs the three-layer judge (deterministic AST checks, optional LLM
// judgement, optional mutation testing) over a directory tree and classifies
// every test as real | weak | fake | helper. A "fake" test is one that passes
// even when the code under test is broken — exactly the failure mode where a
// green suite hides a real regression.
//
// Usage:
//
//	# Report-only over the whole SDK (no LLM, with mutation on suspicious tests):
//	go run ./cmd/test-judge --root . --mutate --report-only --out judge_records.jsonl
//
//	# Generate the grandfather baseline of currently-known fakes:
//	go run ./cmd/test-judge --root . --mutate --write-baseline judge/baseline.json
//
//	# Hard gate: exit non-zero if any NEW fake (not in baseline) appears.
//	go run ./cmd/test-judge --root . --mutate --baseline judge/baseline.json --gate
//
// Exit codes: 0 = clean (or report-only); 1 = new/regressed fake tests found;
// 2 = usage/setup error.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/judge"
)

func main() {
	var (
		root          = flag.String("root", ".", "root directory to scan for *_test.go")
		out           = flag.String("out", "", "write JSONL judge records to this path")
		mutate        = flag.Bool("mutate", false, "enable Layer 3 mutation testing on suspicious tests")
		mutTimeout    = flag.Int("mutant-timeout", 60, "per-mutant `go test` timeout in seconds")
		maxMutants    = flag.Int("max-mutants", 6, "max mutants per suspicious test (0 = all)")
		baselinePath  = flag.String("baseline", "", "baseline JSON of grandfathered fake tests")
		writeBaseline = flag.String("write-baseline", "", "write current fakes as a new baseline file and exit clean")
		gate          = flag.Bool("gate", false, "exit non-zero if new fakes (not in baseline) are found")
		reportOnly    = flag.Bool("report-only", false, "never fail; just report (overrides --gate)")
		timeoutMin    = flag.Int("timeout", 30, "overall wall-clock timeout in minutes (0 = none)")

		useLLM      = flag.Bool("llm", false, "enable Layer 2 LLM judge on suspicious tests")
		llmProvider = flag.String("llm-provider", "anthropic", "LLM provider: anthropic|openai|gemini|minimax")
		llmModel    = flag.String("llm-model", "", "LLM model id (provider default if empty)")
		llmAPIKey   = flag.String("llm-api-key", "", "API key (falls back to the provider's standard env var)")
	)
	flag.Parse()

	ctx := context.Background()
	if *timeoutMin > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*timeoutMin)*time.Minute)
		defer cancel()
	}

	// Layer 2 (LLM): off by default so the tool runs in CI without provider
	// credentials. When --llm is set, build an SDKJudge; on any setup error we
	// warn and fall back to NoopJudge rather than aborting the run.
	var theJudge judge.Judge = judge.NoopJudge{}
	if *useLLM {
		j, err := buildSDKJudge(*llmProvider, *llmModel, *llmAPIKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "test-judge: --llm disabled: %v\n", err)
		} else {
			theJudge = j
			fmt.Fprintf(os.Stderr, "test-judge: LLM judge enabled (%s)\n", *llmProvider)
		}
	}

	opts := judge.Options{
		Judge:                   theJudge,
		Mutate:                  *mutate,
		PerMutantTimeoutSeconds: *mutTimeout,
		MaxMutantsPerTest:       *maxMutants,
	}

	fmt.Fprintf(os.Stderr, "test-judge: scanning %s (mutate=%v)\n", *root, *mutate)
	records, err := judge.RunTree(ctx, *root, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test-judge: scan error: %v\n", err)
		os.Exit(2)
	}

	if *out != "" {
		if err := writeJSONL(*out, records); err != nil {
			fmt.Fprintf(os.Stderr, "test-judge: write %s: %v\n", *out, err)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "test-judge: wrote %d records to %s\n", len(records), *out)
	}

	summary := judge.Summarize(records)
	printSummary(summary)

	if *writeBaseline != "" {
		if err := judge.SaveBaseline(*writeBaseline, records); err != nil {
			fmt.Fprintf(os.Stderr, "test-judge: write baseline: %v\n", err)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "test-judge: wrote baseline (%d fakes) to %s\n", summary.Fake, *writeBaseline)
		os.Exit(0)
	}

	// Determine gate failures.
	base := &judge.Baseline{}
	if *baselinePath != "" {
		b, err := judge.LoadBaseline(*baselinePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "test-judge: load baseline: %v\n", err)
			os.Exit(2)
		}
		base = b
	}
	newFakes := base.NewFakes(records)

	if len(newFakes) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d NEW fake test(s) (not in baseline):\n", len(newFakes))
		for _, r := range newFakes {
			fmt.Fprintf(os.Stderr, "  ✗ %s:%d %s.%s — %s\n", r.File, r.Line, r.Package, r.TestName, r.Reason)
		}
	}

	if *reportOnly {
		os.Exit(0)
	}
	if *gate && len(newFakes) > 0 {
		fmt.Fprintf(os.Stderr, "\ntest-judge: GATE FAILED — %d new fake test(s)\n", len(newFakes))
		os.Exit(1)
	}
	os.Exit(0)
}

func writeJSONL(path string, records []judge.JudgeRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i := range records {
		if err := enc.Encode(records[i]); err != nil {
			return err
		}
	}
	return nil
}

func printSummary(s judge.Summary) {
	fmt.Fprintf(os.Stderr, "\n=== test-judge summary ===\n")
	fmt.Fprintf(os.Stderr, "  total tests : %d\n", s.Total)
	fmt.Fprintf(os.Stderr, "  real        : %d\n", s.Real)
	fmt.Fprintf(os.Stderr, "  weak        : %d\n", s.Weak)
	fmt.Fprintf(os.Stderr, "  fake        : %d\n", s.Fake)
	fmt.Fprintf(os.Stderr, "  helpers     : %d (excluded)\n", s.Helpers)
	if len(s.Fakes) > 0 {
		fmt.Fprintf(os.Stderr, "\n  fake tests:\n")
		for _, r := range s.Fakes {
			fmt.Fprintf(os.Stderr, "    ✗ %s.%s — %s\n", r.Package, r.TestName, r.Reason)
		}
	}
}
