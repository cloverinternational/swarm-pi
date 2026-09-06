// ls.go — `swarm-bench ls`: list recorded effect rows, most-recent first.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
)

func runLS(args []string) {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	var c commonFlags
	addCommonFlags(fs, &c)
	limit := fs.Int("n", 50, "show at most N most-recent matching rows (0 = no limit, still bounded by -scan-limit)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: swarm-bench ls [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	res, err := scanLedger(&c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "swarm-bench ls:", err)
		os.Exit(1)
	}

	if len(res.matched) == 0 {
		if c.jsonOut {
			fmt.Println("[]")
			return
		}
		fmt.Printf("nothing recorded (bucket %s under %s; scanned %d row(s) across %d file(s))\n",
			res.bucket, res.baseDir, res.readStats.RowsRead, res.readStats.FilesRead)
		return
	}

	sort.SliceStable(res.matched, func(i, j int) bool { return res.matched[i].TS.After(res.matched[j].TS) })
	rows := res.matched
	if *limit > 0 && len(rows) > *limit {
		rows = rows[:*limit]
	}

	if c.jsonOut {
		printJSON(rows)
		return
	}

	w := newTabWriter()
	printEffectTable(w, rows)
	_ = w.Flush()

	fmt.Printf("\n%d row(s) shown of %d matched (%d row(s) scanned across %d file(s)",
		len(rows), len(res.matched), res.readStats.RowsRead, res.readStats.FilesRead)
	if res.readStats.LinesSkipped > 0 {
		fmt.Printf("; %d unparsable line(s) skipped", res.readStats.LinesSkipped)
	}
	if res.scanCapped {
		fmt.Printf("; scan stopped early at -scan-limit=%d, results may be incomplete", c.scanLimit)
	}
	fmt.Println(")")
}
