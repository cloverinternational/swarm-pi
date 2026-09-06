package lifecycle

// CanAcceptWork reports whether a daemon observed in state s may accept new
// work. Per ADR-005, only `ready` means the daemon identity is proved,
// required listeners and dependencies pass self-probes, and the daemon has
// no active execution: exactly the predicate `accept_work` requires.
func CanAcceptWork(s State) bool {
	return s == StateReady
}

// IsLive reports whether s represents a candidate or live daemon instance —
// every closed state except `absent` (no authoritative process, instance-
// lock owner, or owned discovery artifacts known) and `stopped` (the
// identified process has already exited). This matches ADR-005's
// "Unexpected process exit" list of candidate/live states plus `ready` and
// `working` and `upgrading`.
func IsLive(s State) bool {
	return s != StateAbsent && s != StateStopped
}

// IsReady reports whether s is exactly `ready`: the daemon can accept work
// and has no active execution. `working` is deliberately excluded — a
// working daemon is live but not idle/ready.
func IsReady(s State) bool {
	return s == StateReady
}
