package builtin

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/version"
)

// provenanceBlock renders just the build-provenance lines.
func provenanceBlock(t *testing.T) string {
	t.Helper()
	var body strings.Builder
	writeAnnoyedBuildProvenance(&body)
	return body.String()
}

// stubBuildIdentity installs a known commit/build time for the duration of a
// test.
//
// This is required, not cosmetic: a `go test` binary carries NO vcs.revision or
// vcs.time build settings, so EffectiveGitCommit/EffectiveBuildTime both report
// "unknown" under test even though a real `go build` binary of this same tree
// reports "a8177bc262...-dirty" and "2026-08-10T16:06:48Z" (verified by
// building and running a probe). Without stubbing, these tests would assert
// against an environment that never ships.
func stubBuildIdentity(t *testing.T, commit, built string) {
	t.Helper()
	originalCommit, originalBuilt := version.GitCommit, version.BuildTime
	t.Cleanup(func() {
		version.GitCommit = originalCommit
		version.BuildTime = originalBuilt
	})
	version.GitCommit = commit
	version.BuildTime = built
}

func metadataValue(block, label string) string {
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "- "+label+": ") {
			return strings.TrimPrefix(line, "- "+label+": ")
		}
	}
	return ""
}

// Acceptance test 1: the published body carries Commit and Built lines inside
// the Invocation block.
func TestAnnoyedProvenanceEmitsCommitAndBuilt(t *testing.T) {
	stubBuildIdentity(t, "a8177bc262ca712c3b0a35babd8c46ab465020ef", "2026-08-10T16:06:48Z")
	block := provenanceBlock(t)
	if metadataValue(block, "Commit") == "" {
		t.Fatalf("no Commit line in provenance block:\n%s", block)
	}
	if metadataValue(block, "Built") == "" {
		t.Fatalf("no Built line in provenance block:\n%s", block)
	}
	if metadataValue(block, "Go/Platform") == "" {
		t.Fatalf("no Go/Platform line in provenance block:\n%s", block)
	}
}

// Acceptance test 2: with no resolver installed the SDK/commit/built lines
// still emit and ONLY the product Version line is omitted.
func TestAnnoyedProvenanceOmitsOnlyProductVersionWithoutResolver(t *testing.T) {
	stubBuildIdentity(t, "a8177bc262ca712c3b0a35babd8c46ab465020ef", "2026-08-10T16:06:48Z")
	version.SetProductVersionResolver(nil)
	block := provenanceBlock(t)

	if got := metadataValue(block, "Version"); got != "" {
		t.Fatalf("product Version emitted with no resolver installed: %q", got)
	}
	if got := metadataValue(block, "SDK"); got != version.Version {
		t.Fatalf("SDK line = %q, want %q", got, version.Version)
	}
	if metadataValue(block, "Commit") == "" || metadataValue(block, "Built") == "" {
		t.Fatalf("commit/built suppressed along with product version:\n%s", block)
	}

	version.SetProductVersionResolver(func() string { return "v9.9.9" })
	defer version.SetProductVersionResolver(nil)
	if got := metadataValue(provenanceBlock(t), "Version"); got != "v9.9.9" {
		t.Fatalf("resolver-supplied version = %q, want v9.9.9", got)
	}
}

// Acceptance test 3: a dirty build is marked as such. EffectiveGitCommit
// appends "-dirty"; the block must surface that, re-expressed as a separate
// marker so the hex run stays under the redactor's token floor.
func TestAnnoyedProvenanceMarksDirtyBuild(t *testing.T) {
	stubBuildIdentity(t, "a8177bc262ca712c3b0a35babd8c46ab465020ef-dirty", "2026-08-10T16:06:48Z")
	commit := metadataValue(provenanceBlock(t), "Commit")
	if !strings.HasSuffix(commit, " (dirty)") {
		t.Fatalf("dirty build not marked: %q", commit)
	}
	if strings.Contains(commit, "-dirty") {
		t.Fatalf("raw -dirty suffix retained, which the redactor would eat: %q", commit)
	}
}

// The reason the commit is abbreviated at all: the finished body is passed
// through vault.OutputRedactor.RedactAll, which replaces high-entropy tokens.
// A full 40-char SHA measures ~3.73 bits/char and IS redacted, which would
// publish "Commit: [REDACTED-ENTROPY]" and silently defeat this feature. This
// test fails if anyone widens annoyedCommitDisplayLen back past that cliff.
func TestAnnoyedProvenanceSurvivesEntropyRedaction(t *testing.T) {
	stubBuildIdentity(t, "a8177bc262ca712c3b0a35babd8c46ab465020ef", "2026-08-10T16:06:48Z")

	block := provenanceBlock(t)
	redacted, _ := vault.NewOutputRedactor().RedactAll(block, nil)
	if strings.Contains(redacted, "[REDACTED-ENTROPY]") {
		t.Fatalf("provenance was entropy-redacted; the stamp is useless:\n%s", redacted)
	}

	commit := metadataValue(redacted, "Commit")
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(commit) {
		t.Fatalf("commit did not survive redaction as 12 hex chars: %q", commit)
	}
	if !strings.HasPrefix("a8177bc262ca712c3b0a35babd8c46ab465020ef", commit) {
		t.Fatalf("commit %q is not a prefix of the real SHA", commit)
	}

	// Guard the near-miss directly: the untruncated forms really are redacted,
	// so the abbreviation is load-bearing rather than cosmetic.
	for _, hazard := range []string{
		"- Commit: a8177bc262ca712c3b0a35babd8c46ab465020ef",
		"- Commit: a8177bc26262-dirty",
	} {
		out, _ := vault.NewOutputRedactor().RedactAll(hazard, nil)
		if !strings.Contains(out, "[REDACTED-ENTROPY]") {
			t.Fatalf("expected %q to be redacted; if this stopped being true the "+
				"abbreviation rationale needs revisiting, got %q", hazard, out)
		}
	}
}

// Acceptance test 4: provenance is emitted before the transcript and survives
// truncateAnnoyedBody, which keeps a head slice plus a tail slice.
func TestAnnoyedProvenanceSurvivesTruncation(t *testing.T) {
	stubBuildIdentity(t, "a8177bc262ca712c3b0a35babd8c46ab465020ef", "2026-08-10T16:06:48Z")
	var body strings.Builder
	body.WriteString("## Agent complaint\n\nsomething broke\n\n## Invocation\n\n")
	writeAnnoyedBuildProvenance(&body)
	body.WriteString("\n## Conversation transcript\n\n")
	body.WriteString(strings.Repeat("transcript filler line\n", 20_000))

	full := body.String()
	truncated, didTruncate := truncateAnnoyedBody(full, defaultAnnoyedBodyChars)
	if !didTruncate {
		t.Fatalf("fixture did not exceed the limit; test proves nothing")
	}
	if metadataValue(truncated, "Commit") == "" {
		t.Fatalf("Commit lost to truncation")
	}
	if metadataValue(truncated, "Built") == "" {
		t.Fatalf("Built lost to truncation")
	}
	if !strings.Contains(truncated, "## Invocation") {
		t.Fatalf("Invocation heading lost to truncation")
	}
}
