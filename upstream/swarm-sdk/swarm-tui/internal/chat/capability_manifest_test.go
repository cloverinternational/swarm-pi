package chat

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestBuildCapabilityManifestDeterministicAndBounded(t *testing.T) {
	tools := []provider.Tool{
		{Name: "Write", Description: "Writes a file. Extra sentence omitted."},
		{Name: "Read", Description: "Reads a file. More details."},
		{Name: "Bash", Description: "Runs shell commands."},
	}

	got := buildCapabilityManifest("plan", tools, 2)
	wantOrder := []string{"Bash", "Read"}
	last := -1
	for _, name := range wantOrder {
		idx := strings.Index(got, "`"+name+"`")
		if idx < 0 || idx <= last {
			t.Fatalf("manifest order/content invalid: %q", got)
		}
		last = idx
	}
	if strings.Contains(got, "`Write`") {
		t.Fatalf("manifest exceeded max tools: %q", got)
	}
	if !strings.Contains(got, "1 additional tool omitted") {
		t.Fatalf("missing truncation marker: %q", got)
	}
	if !strings.Contains(got, "mode: plan") {
		t.Fatalf("missing mode: %q", got)
	}
}

func TestBuildCapabilityManifestNoTools(t *testing.T) {
	got := buildCapabilityManifest("plan", nil, 10)
	if !strings.Contains(got, "No tools are available") {
		t.Fatalf("unexpected empty manifest: %q", got)
	}
}

func TestCapabilityManifestSignatureTracksMeaningfulChangesOnly(t *testing.T) {
	base := []provider.Tool{
		{Name: "Bash", Description: "Runs shell commands."},
		{Name: "Read", Description: "Reads a file."},
	}
	reordered := []provider.Tool{
		{Name: "Read", Description: "Reads a file."},
		{Name: "Bash", Description: "Runs shell commands."},
	}

	baseSig := capabilityManifestSignature("act", base)

	if got := capabilityManifestSignature("act", reordered); got != baseSig {
		t.Fatalf("signature changed on pure reordering: %q vs %q", got, baseSig)
	}

	// Descriptions are static per build and excluded from the fingerprint.
	rewritten := []provider.Tool{
		{Name: "Bash", Description: "Totally different prose."},
		{Name: "Read", Description: "Also different."},
	}
	if got := capabilityManifestSignature("act", rewritten); got != baseSig {
		t.Fatalf("signature changed on description-only edit: %q vs %q", got, baseSig)
	}

	if got := capabilityManifestSignature("plan", base); got == baseSig {
		t.Fatal("signature ignored the operating mode")
	}

	added := append(append([]provider.Tool(nil), base...), provider.Tool{Name: "Write", Description: "Writes a file."})
	if got := capabilityManifestSignature("act", added); got == baseSig {
		t.Fatal("signature ignored an added tool")
	}

	if got := capabilityManifestSignature("act", base[:1]); got == baseSig {
		t.Fatal("signature ignored a removed tool")
	}
}

func TestShouldEmitCapabilityManifestOnlyWhenItSaysSomethingNew(t *testing.T) {
	tools := []provider.Tool{
		{Name: "Bash", Description: "Runs shell commands."},
		{Name: "Read", Description: "Reads a file."},
	}
	sdk := &SDKIntegration{}

	actSig := capabilityManifestSignature("act", tools)

	// First turn of a conversation always emits.
	if !sdk.shouldEmitCapabilityManifest("conv-1", actSig) {
		t.Fatal("first turn did not emit the manifest")
	}

	// A stable turn is a byte-for-byte repeat and must be skipped.
	if sdk.shouldEmitCapabilityManifest("conv-1", actSig) {
		t.Fatal("unchanged turn re-emitted the manifest")
	}
	if sdk.shouldEmitCapabilityManifest("conv-1", actSig) {
		t.Fatal("unchanged turn re-emitted the manifest on the third call")
	}

	// A mode switch changes the effective tool exposure and must re-emit.
	planSig := capabilityManifestSignature("plan", tools)
	if !sdk.shouldEmitCapabilityManifest("conv-1", planSig) {
		t.Fatal("mode change did not re-emit the manifest")
	}
	if sdk.shouldEmitCapabilityManifest("conv-1", planSig) {
		t.Fatal("manifest re-emitted after the mode change settled")
	}

	// A different conversation has never seen the manifest.
	if !sdk.shouldEmitCapabilityManifest("conv-2", planSig) {
		t.Fatal("conversation switch did not re-emit the manifest")
	}

	// Switching back is also a change relative to the last recorded turn.
	if !sdk.shouldEmitCapabilityManifest("conv-1", planSig) {
		t.Fatal("switching back did not re-emit the manifest")
	}
}

func TestShouldEmitCapabilityManifestNilReceiverEmits(t *testing.T) {
	var sdk *SDKIntegration
	if !sdk.shouldEmitCapabilityManifest("conv-1", "sig") {
		t.Fatal("nil integration must fail open and emit")
	}
}
