package usageindex

import (
	"sort"
	"time"
)

// Detection thresholds. They live together so tuning is a single edit.
const (
	// spikeWindow is how many prior turns form the baseline. A rolling median
	// is used rather than a mean because conversation input grows steadily and
	// a mean would be dragged along by the very turns being measured.
	spikeWindow = 20
	// spikeMultiple is how far above that baseline counts as a spike.
	spikeMultiple = 3.0
	// spikeMinTokens keeps short conversations from reporting a "spike" when a
	// few hundred tokens tripled.
	spikeMinTokens = 20_000

	// A cache break is a turn that re-pays a prefix it should have read from
	// cache: the previous turn read a substantial cache, this turn barely reads
	// any, and writes a substantial one instead.
	cacheBreakMinRead      = 10_000
	cacheBreakMinWrite     = 10_000
	cacheBreakReadFraction = 0.25

	// maxFindingsPerKind bounds what is stored; the view only shows the worst.
	maxFindingsPerKind = 100
)

const (
	cacheTTLStandard = 5 * time.Minute
	cacheTTLExtended = time.Hour
)

// detector walks responses in per-conversation order and records anomalies.
// It is fed by the same single pass that builds the roll-up, so detection adds
// no extra read of the conversation store.
type detector struct {
	conversationID string
	window         []int64
	previous       aggregateRow
	havePrevious   bool

	spikes []Finding
	breaks []Finding
}

func newDetector() *detector {
	return &detector{window: make([]int64, 0, spikeWindow)}
}

func (d *detector) observe(row aggregateRow) {
	if row.ConversationID != d.conversationID {
		d.conversationID = row.ConversationID
		d.window = d.window[:0]
		d.havePrevious = false
	}

	input := row.FreshInput + row.CacheWrite + row.CacheRead
	if input >= spikeMinTokens && len(d.window) == spikeWindow {
		if baseline := median(d.window); baseline > 0 {
			if ratio := float64(input) / float64(baseline); ratio >= spikeMultiple {
				d.spikes = append(d.spikes, Finding{
					Kind:           FindingSpike,
					ConversationID: row.ConversationID,
					Timestamp:      timeOrZero(row.TS),
					Tokens:         input,
					Ratio:          ratio,
				})
			}
		}
	}

	if d.havePrevious {
		if finding, ok := detectCacheBreak(d.previous, row); ok {
			d.breaks = append(d.breaks, finding)
		}
	}

	d.window = append(d.window, input)
	if len(d.window) > spikeWindow {
		d.window = d.window[1:]
	}
	d.previous = row
	d.havePrevious = true
}

// detectCacheBreak reports a turn that paid again for a cached prefix, and
// classifies why. The cause matters more than the count: a gap longer than the
// cache TTL means the cache simply expired while idle, whereas a break moments
// after the previous turn means something rewrote the prompt prefix — an
// actionable bug rather than a fact of life.
func detectCacheBreak(previous, current aggregateRow) (Finding, bool) {
	if previous.CacheRead < cacheBreakMinRead {
		return Finding{}, false
	}
	if current.CacheWrite < cacheBreakMinWrite {
		return Finding{}, false
	}
	if float64(current.CacheRead) >= float64(previous.CacheRead)*cacheBreakReadFraction {
		return Finding{}, false
	}
	finding := Finding{
		Kind:           FindingCacheBreak,
		ConversationID: current.ConversationID,
		Timestamp:      timeOrZero(current.TS),
		Tokens:         current.CacheWrite,
		Cause:          CauseUnknown,
	}
	if previous.TS > 0 && current.TS > 0 {
		gap := current.TS - previous.TS
		if gap < 0 {
			gap = 0
		}
		finding.GapSeconds = gap
		// Classify honestly, including when we cannot tell.
		//
		// The per-TTL split is only present when the provider recorded it. If it
		// is missing we do NOT get to assume the 5m default: the client may have
		// requested 1h, in which case a 12-minute gap is a prefix mutation (a real
		// bug) rather than an expiry (unavoidable). Assuming 5m here previously
		// misfiled 59 of 62 breaks as ttl_expiry and hid the actionable class.
		//
		// Known TTL  -> compare directly.
		// Unknown TTL -> only the unambiguous ends are safe to call:
		//   gap <= 5m : no cache could have expired  -> prefix mutation
		//   gap  > 1h : even the longest TTL expired -> expiry
		//   between   : genuinely ambiguous          -> unknown
		switch {
		case previous.CacheWrite1h > 0 || current.CacheWrite1h > 0:
			finding.Cause = causeForGap(gap, cacheTTLExtended)
		case previous.CacheWrite5m > 0 || current.CacheWrite5m > 0:
			finding.Cause = causeForGap(gap, cacheTTLStandard)
		case float64(gap) <= cacheTTLStandard.Seconds():
			finding.Cause = CausePrefixMutation
		case float64(gap) > cacheTTLExtended.Seconds():
			finding.Cause = CauseTTLExpiry
		default:
			finding.Cause = CauseUnknown
		}
	}
	return finding, true
}

func (d *detector) findings() []Finding {
	return append(topFindings(d.spikes), topFindings(d.breaks)...)
}

func topFindings(findings []Finding) []Finding {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Tokens != findings[j].Tokens {
			return findings[i].Tokens > findings[j].Tokens
		}
		return findings[i].Timestamp.After(findings[j].Timestamp)
	})
	if len(findings) > maxFindingsPerKind {
		findings = findings[:maxFindingsPerKind]
	}
	return findings
}

func median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]int64, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func timeOrZero(ts int64) time.Time {
	if ts <= 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

// causeForGap classifies a cache break when the cache TTL is known.
func causeForGap(gap int64, ttl time.Duration) FindingCause {
	if float64(gap) > ttl.Seconds() {
		return CauseTTLExpiry
	}
	return CausePrefixMutation
}
