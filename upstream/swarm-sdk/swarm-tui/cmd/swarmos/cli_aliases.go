package main

import (
	"fmt"
	"os"
)

// legacyAliasWarningFor looks up the ADR-002 Compatibility-matrix row for an
// exact legacy spelling key (one of the Legacy values in
// legacyAliasWarnings). ok is false when no such row exists.
func legacyAliasWarningFor(legacy string) (row legacyAliasWarning, ok bool) {
	for _, w := range legacyAliasWarnings {
		if w.Legacy == legacy {
			return w, true
		}
	}
	return legacyAliasWarning{}, false
}

// warnLegacyAlias prints the bounded ADR-002 deprecation warning for a
// legacy CLI spelling to stderr, exactly once per invocation (call sites
// each fire it at most once per process, matching "once on stderr" in every
// Compatibility-matrix row), and ONLY when machineMode is false.
//
// ADR-002's Compatibility section is explicit: "In any machine-output mode,
// including JSON and stream JSON, it emits no warning text and no
// additional record: the legacy exit status, stdout framing, field names,
// and documented schema remain compatible until removal." Every call site
// MUST pass the correct machine-mode detection for its own command surface
// (see isMachineOutputArgs for the shared detector used by the headless/
// peer/swarm command families).
//
// legacy must be one of the exact keys in legacyAliasWarnings; an unknown
// key or a row with an empty Warning (ADR-002 row 1, "swarm -p <prompt>",
// which is explicitly NOT a deprecation) is a silent no-op by design.
func warnLegacyAlias(legacy string, machineMode bool) {
	if machineMode {
		return
	}
	// "swarm swarm attach"'s ADR-002 canonical replacement is `swarm peer
	// control`, but that command is explicitly DEFERRED (peer_cli.go's
	// peerControlImplemented) and currently returns a "not yet supported"
	// error that itself points back at `swarm swarm attach`. Printing the
	// ADR-002 row's normal recommendation here would create exactly the
	// contradictory command loop reported in issue #231: attach warns
	// "use swarm peer control", swarm peer control replies "not yet
	// supported, use swarm swarm attach". Substitute a warning that states
	// the future plan without recommending a command that cannot succeed
	// yet. This reverts to the ADR-002 row's normal text automatically the
	// moment peerControlImplemented flips to true — no second edit needed
	// here, only there.
	if legacy == "swarm swarm attach" && !peerControlImplemented {
		fmt.Fprintln(os.Stderr, "warning: 'swarm swarm attach' will be replaced by 'swarm peer control' once that command is implemented; "+
			"'swarm peer control' is not yet supported, so continue using 'swarm swarm attach' for now")
		return
	}
	row, ok := legacyAliasWarningFor(legacy)
	if !ok || row.Warning == "" {
		return
	}
	fmt.Fprintln(os.Stderr, row.Warning)
}

// nearestCanonicalCommand returns the canonicalTopLevelCommands entry with
// the smallest Levenshtein edit distance to got, plus true, when a
// reasonably close match exists. It returns ok=false when got is farther
// from every known command than half its own length (rounded up) plus one,
// which keeps wildly unrelated input (e.g. a stray filename) from
// producing a misleading suggestion.
func nearestCanonicalCommand(got string) (best string, ok bool) {
	if got == "" {
		return "", false
	}
	bestDist := -1
	for _, cand := range canonicalTopLevelCommands {
		d := levenshteinDistance(got, cand)
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = cand
		}
	}
	if bestDist == -1 {
		return "", false
	}
	threshold := len(got)/2 + 1
	if bestDist > threshold {
		return "", false
	}
	return best, true
}

// levenshteinDistance computes the classic single-character
// insert/delete/substitute edit distance between a and b using the
// standard O(len(a)*len(b)) dynamic-programming table. Implemented locally
// (no third-party dependency) since it is only ever used for the small,
// bounded canonicalTopLevelCommands suggestion list above.
func levenshteinDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
