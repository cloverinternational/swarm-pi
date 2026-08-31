// Command harborwild packages a real, already-fixed bug from this repo's own
// git history into a self-contained Harbor (github.com/harbor-framework/harbor)
// task directory: a real regression, a real test that proved it RED, and a
// real fix diff, wired into Harbor's instruction.md / environment / tests /
// solution contract.
//
// # Why this exists
//
// PLAN.md ("Measurable Agent Runs") Phase E is "wild mines tasks, benchmark
// scores them: failed/reworked wild episodes -> replayable fixtures with real
// verifiers -> offline pass@k". This tool is the mining half of that pipeline,
// seeded from this branch's own REVIEW.md bugfix queue rather than invented
// fixtures: the regression, the verifier (a Go test that failed before the fix
// and passes after), and the fix are all real commits already on this branch,
// not synthesized for the benchmark.
//
// # The core problem this tool solves
//
// Every fix on this branch added its regression test in the SAME commit as
// the fix (test-driven: reproduce RED, then fix, then commit both together).
// That means the "bug present, test exists, test fails" state this tool needs
// as the task's starting environment is not itself a real git commit — it has
// to be synthesized: take the tree at the fix commit's parent (bug present,
// original behavior), then apply ONLY the test-file half of the fix commit's
// diff on top (test exists now, production code is untouched, so the test
// fails for real). The production-code half of the diff becomes the oracle
// solution, applied by solution/solve.sh.
//
// # Self-containment
//
// Harbor builds task images standalone — they cannot assume this sandbox's
// working copy is mounted. So the packaged task carries a `git archive` of
// the entire mono repo at the parent commit as a tarball inside environment/,
// and the Dockerfile extracts it at build time. No live git remote, no
// network dependency on this exact checkout.
//
// # Usage
//
//	harborwild -manifest <path/to/manifest.json> -repo <mono-repo-root> \
//	    -out swarm-sdk/integrations/harbor/tasks/wild
//
// See manifest.go for the manifest schema.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	manifestPath := flag.String("manifest", "", "path to a task manifest JSON file (see manifest.go)")
	repoRoot := flag.String("repo", "", "path to the mono repo root (git root); defaults to `git rev-parse --show-toplevel` from cwd")
	outDir := flag.String("out", "", "output tasks directory; task will be written to <out>/<slug basename>")
	dryRun := flag.Bool("dry-run", false, "print what would be written without writing any files")
	flag.Parse()

	if *manifestPath == "" {
		fmt.Fprintln(os.Stderr, "harborwild: -manifest is required")
		os.Exit(2)
	}

	m, err := loadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harborwild: %v\n", err)
		os.Exit(1)
	}

	root := *repoRoot
	if root == "" {
		r, err := gitShowToplevel(".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "harborwild: -repo not given and could not detect git root: %v\n", err)
			os.Exit(1)
		}
		root = r
	}

	if *outDir == "" {
		fmt.Fprintln(os.Stderr, "harborwild: -out is required")
		os.Exit(2)
	}

	if err := packageTask(root, *outDir, m, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "harborwild: %v\n", err)
		os.Exit(1)
	}
}
