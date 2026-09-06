// Command swarm-bench is a small, READ-ONLY inspector over the
// internal/bench effect ledger (PLAN.md Phase B, "the 20% version").
//
// # Read-only, for real
//
// This binary never writes to the ledger (~/.swarm/projects/<bucket>/bench/
// *.jsonl), never mutates git refs/index/worktree, and never prunes or
// rewrites anything. The only external process it ever spawns is
// `git log --all --find-object=<blob>` (survive.go), which is itself
// read-only — it walks history, it does not change it.
//
// # No verdicts here
//
// PLAN.md §6 refuses a pass-rate headline: "It rises when the agent does
// easier, more verifiable work. If the Undetermined share cannot be
// displayed beside it, the number does not ship." This tool prints a
// SURVIVAL ratio (did recorded content reach git history — a fact about
// git, not about correctness), always next to the count of rows for which
// survival could not even be evaluated ("not established", PLAN.md §2's
// "missing evidence -> Undetermined, never Fail" made concrete for a blob
// that was never captured — e.g. a delete). Nothing in this package
// computes or prints a score, a grade, or a percentage framed as
// "correct"/"passed".
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "ls":
		runLS(os.Args[2:])
	case "survive":
		runSurvive(os.Args[2:])
	case "-h", "--help", "help":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "swarm-bench: unknown subcommand %q\n\n", os.Args[1])
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

const usageText = `swarm-bench -- read-only inspector for the internal/bench effect ledger.

This tool NEVER writes to the ledger, and NEVER touches git refs, the
worktree, or the index. 'survive' runs a single read-only
'git log --all --find-object' query per distinct recorded blob.

Usage:
  swarm-bench ls       [flags]   List recorded file-effect rows.
  swarm-bench survive  [flags]   Report whether recorded content reached git history.

Run 'swarm-bench ls -h' or 'swarm-bench survive -h' for flag help.
`

func printUsage(w *os.File) {
	fmt.Fprint(w, usageText)
}
