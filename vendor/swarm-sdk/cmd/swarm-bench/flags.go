// flags.go — the effect-selection flags and bounded-memory ledger scan
// shared by `ls` and `survive`. Both subcommands filter the SAME set of
// fields (session/conversation/agent/tool/path/time) so a user narrowing a
// `ls` result to find something suspicious can rerun the identical flags
// under `survive` to ask "did that specific thing reach history?".
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/bench"
)

// commonFlags is the effect-selection surface shared by every subcommand.
type commonFlags struct {
	dir          string
	baseDir      string
	bucket       string
	session      string
	conversation string
	agent        string
	tool         string
	pathSubstr   string
	since        string
	until        string
	scanLimit    int
	jsonOut      bool
}

func addCommonFlags(fs *flag.FlagSet, c *commonFlags) {
	cwd, _ := os.Getwd()
	fs.StringVar(&c.dir, "dir", cwd, "workspace/repo directory used to resolve the ledger bucket (git-common-dir based; defaults to cwd)")
	fs.StringVar(&c.baseDir, "base-dir", "", "override the ledger base dir (default: ~/.swarm/projects); mainly for tests/fixtures")
	fs.StringVar(&c.bucket, "bucket", "", "override the resolved bucket hash directly, skipping -dir resolution")
	fs.StringVar(&c.session, "session", "", "filter: exact SessionID match")
	fs.StringVar(&c.conversation, "conversation", "", "filter: exact ConversationID match")
	fs.StringVar(&c.agent, "agent", "", "filter: exact AgentID match")
	fs.StringVar(&c.tool, "tool", "", "filter: exact Tool match")
	fs.StringVar(&c.pathSubstr, "path", "", "filter: substring match against Path")
	fs.StringVar(&c.since, "since", "", "filter: only rows at/after this time (RFC3339, or a duration like 2h meaning 'now minus 2h')")
	fs.StringVar(&c.until, "until", "", "filter: only rows at/before this time (RFC3339)")
	fs.IntVar(&c.scanLimit, "scan-limit", 100000, "hard cap on rows scanned from the ledger before stopping early (bounded-memory safety valve; reported if hit)")
	fs.BoolVar(&c.jsonOut, "json", false, "machine-readable JSON output")
}

// resolveBucketDir turns commonFlags into the directory ReadEffects should
// walk, plus the effective bucket/base actually used — printed by callers
// so "nothing recorded" is diagnosable ("which bucket did we look in?")
// rather than mysterious.
func (c *commonFlags) resolveBucketDir() (dir, bucket, base string) {
	base = c.baseDir
	if base == "" {
		base = bench.DefaultBaseDir()
	}
	bucket = c.bucket
	if bucket == "" {
		bucket = bench.BucketFor(c.dir)
	}
	return bench.BucketDir(base, bucket), bucket, base
}

// parseTimeFlag accepts either an RFC3339 timestamp or a Go duration
// string (e.g. "2h", "45m"), the latter interpreted as "now minus this
// duration" — the common case of "show me the last N hours".
func parseTimeFlag(raw string) (t time.Time, ok bool, err error) {
	if raw == "" {
		return time.Time{}, false, nil
	}
	if parsed, perr := time.Parse(time.RFC3339, raw); perr == nil {
		return parsed, true, nil
	}
	if d, derr := time.ParseDuration(raw); derr == nil {
		return time.Now().UTC().Add(-d), true, nil
	}
	return time.Time{}, false, fmt.Errorf("cannot parse %q as an RFC3339 timestamp or a duration like \"2h\"", raw)
}

// matches reports whether eff passes every configured filter.
func (c *commonFlags) matches(eff bench.Effect, since, until time.Time, hasSince, hasUntil bool) bool {
	if c.session != "" && eff.SessionID != c.session {
		return false
	}
	if c.conversation != "" && eff.ConversationID != c.conversation {
		return false
	}
	if c.agent != "" && eff.AgentID != c.agent {
		return false
	}
	if c.tool != "" && eff.Tool != c.tool {
		return false
	}
	if c.pathSubstr != "" && !strings.Contains(eff.Path, c.pathSubstr) {
		return false
	}
	if hasSince && eff.TS.Before(since) {
		return false
	}
	if hasUntil && eff.TS.After(until) {
		return false
	}
	return true
}

// scanResult is the bounded outcome of walking the ledger under a filter.
// "matched" is capped by scanLimit — see scanLedger — so this never grows
// with the size of the on-disk ledger, only with the number of rows a
// caller actually asked to see.
type scanResult struct {
	matched    []bench.Effect
	readStats  bench.ReadStats
	scanned    int
	scanCapped bool
	bucketDir  string
	bucket     string
	baseDir    string
}

// errScanLimitReached stops ReadEffects' walk early once scanLimit rows
// have been examined — the bounded-memory guarantee: this tool never holds
// more than scanLimit Effect structs in memory regardless of how large the
// on-disk ledger has grown.
var errScanLimitReached = errors.New("scan limit reached")

func scanLedger(c *commonFlags) (*scanResult, error) {
	dir, bucket, base := c.resolveBucketDir()
	files, err := bench.ListEffectFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("listing ledger files in %s: %w", dir, err)
	}

	since, hasSince, err := parseTimeFlag(c.since)
	if err != nil {
		return nil, fmt.Errorf("-since: %w", err)
	}
	until, hasUntil, err := parseTimeFlag(c.until)
	if err != nil {
		return nil, fmt.Errorf("-until: %w", err)
	}

	res := &scanResult{bucketDir: dir, bucket: bucket, baseDir: base}
	stats, err := bench.ReadEffects(files, func(eff bench.Effect) error {
		res.scanned++
		if c.scanLimit > 0 && res.scanned > c.scanLimit {
			res.scanCapped = true
			return errScanLimitReached
		}
		if c.matches(eff, since, until, hasSince, hasUntil) {
			res.matched = append(res.matched, eff)
		}
		return nil
	})
	if err != nil && !errors.Is(err, errScanLimitReached) {
		return nil, fmt.Errorf("reading ledger: %w", err)
	}
	res.readStats = stats
	return res, nil
}
