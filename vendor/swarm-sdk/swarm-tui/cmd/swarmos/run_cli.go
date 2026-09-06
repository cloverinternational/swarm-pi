package main

import "strings"

// run_cli.go implements the argument-shape adapter for the canonical
// `swarm run <prompt> [run options]` verb (ADR-002 DOMAIN: "run owns an
// ephemeral invocation"). It intentionally contains NO execution logic of
// its own: the `case "run":` dispatch entry in main.go's subcommand switch
// uses normalizeRunArgs (below) to turn the positional prompt into the
// exact same -p flag value the historical `swarm -p <prompt>` path has
// always used, parses flags through the SAME flag.CommandLine used
// everywhere else in this binary, and then calls dispatchHeadlessExecution
// (main.go) — the identical helper `swarm -p` calls — which in turn calls
// the EXISTING, unmodified runHeadless(outputFormat) (main.go:1165).
//
// This guarantees `swarm run <prompt> [options]` and `swarm -p <prompt>
// [options]` are byte-identical in behavior beyond invocation syntax,
// matching ADR-002's compatibility-matrix row 1 exactly ("swarm -p
// <prompt>" -> "swarm run <prompt>", "None" warning, "Preserve exit
// status, stdout, and every documented machine schema").
//
// `swarm run` must NEVER start or select the persistent daemon implicitly
// (ADR-002 Decision section: "swarm run always means one explicit
// ephemeral execution as defined by ADR-001. It never starts or selects
// the persistent daemon implicitly."). Because this file only ever calls
// runHeadless via dispatchHeadlessExecution, and runHeadless's body
// (main.go) never references ensureGlobalDaemon or globalDaemonHandle,
// this invariant holds by construction. cli_contract_test.go asserts it
// statically against runHeadless's source so a future edit cannot silently
// violate it.

// normalizeRunArgs splits `swarm run <prompt> [options]` CLI arguments into
// the bare prompt text and the remaining option tokens, following ADR-002's
// INTERFACE grammar verbatim: `swarm run <prompt> [run options]` — the
// prompt is always the first positional argument, and everything else is a
// flag belonging to the existing headless flag set already declared in
// main.go's var block (-m, -P, --workspace, --output-format, ...).
//
// If args is empty, or the first token itself looks like a flag (has a
// leading "-" — e.g. a caller already spells it out explicitly as
// `swarm run -p "..."` for a scripted/back-compat invocation), no
// positional prompt is extracted: every token is returned unchanged in
// rest, and hasPositionalPrompt is false. The caller (main.go's `case
// "run":`) is responsible for detecting a still-empty -p flag value after
// parsing rest and failing with a clear "requires a prompt" error — this
// function never guesses a prompt out of the middle of an options list,
// which would risk swallowing a flag's own value (e.g. the "/foo" in
// `--workspace /foo`) as if it were the prompt.
func normalizeRunArgs(args []string) (prompt string, rest []string, hasPositionalPrompt bool) {
	if len(args) == 0 {
		return "", nil, false
	}
	first := args[0]
	if first == "" || strings.HasPrefix(first, "-") {
		return "", args, false
	}
	return first, args[1:], true
}
