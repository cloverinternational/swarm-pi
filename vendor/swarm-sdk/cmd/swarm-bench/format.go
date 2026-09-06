// format.go — small, dependency-free output helpers shared by ls and
// survive: a compact tabwriter table for humans, and a plain JSON encoder
// for machines. Neither function computes anything; they only render what
// the caller already decided to show.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/bench"
)

// shortHash truncates a git blob/commit hash to a stable, still-useful
// prefix for table display; -json always carries the full value.
func shortHash(s string) string {
	if s == "" {
		return "-"
	}
	if len(s) <= 10 {
		return s
	}
	return s[:10]
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func printEffectTable(w *tabwriter.Writer, rows []bench.Effect) {
	fmt.Fprintln(w, "TS\tTOOL\tOP\tPATH\tPRE_BLOB\tPOST_BLOB\tSESSION\tAGENT\tCONVERSATION")
	for _, eff := range rows {
		ts := eff.TS.UTC().Format(time.RFC3339)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ts,
			orDash(eff.Tool),
			orDash(eff.Op),
			orDash(eff.Path),
			shortHash(eff.PreBlob),
			shortHash(eff.PostBlob),
			shortHash(eff.SessionID),
			orDash(eff.AgentID),
			shortHash(eff.ConversationID),
		)
	}
}

func newTabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "swarm-bench: encoding JSON output:", err)
		os.Exit(1)
	}
}
