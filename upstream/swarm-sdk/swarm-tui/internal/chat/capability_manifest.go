package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

const defaultCapabilityManifestToolLimit = 40

// capabilityManifestSignature returns a stable fingerprint of the inputs that
// can change the rendered manifest: the operating mode and the set of exposed
// tool names. Descriptions are deliberately excluded — they are static per
// build, so including them would only add hashing cost without ever changing
// the verdict within a session.
//
// Tool availability is effectively static inside a conversation (it moves only
// on an operating-mode switch, a deferred-tool load, an MCP connect/disconnect,
// or a skill-activated tool), so this signature lets the caller re-send the
// manifest exactly when it carries new information instead of every turn.
func capabilityManifestSignature(modeID string, providerTools []provider.Tool) string {
	names := make([]string, 0, len(providerTools))
	for _, tool := range providerTools {
		names = append(names, tool.Name)
	}
	// Match buildCapabilityManifest's case-insensitive ordering so a pure
	// reordering of the same tool set does not read as a change.
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})

	digest := sha256.New()
	digest.Write([]byte(strings.TrimSpace(modeID)))
	for _, name := range names {
		digest.Write([]byte{0})
		digest.Write([]byte(name))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// shouldEmitCapabilityManifest reports whether the capability manifest must be
// injected for this turn, recording the signature when the answer is yes.
//
// It returns true when the manifest carries information the model has not
// already been shown in this conversation: the first turn of a conversation,
// a switch to a different conversation, or any change to the effective tool
// set or operating mode. It returns false on a stable turn, where the block
// would be a byte-for-byte repeat of the previous turn's.
func (sdk *SDKIntegration) shouldEmitCapabilityManifest(convID, signature string) bool {
	if sdk == nil {
		return true
	}

	sdk.capabilityManifestMu.Lock()
	defer sdk.capabilityManifestMu.Unlock()

	if sdk.lastCapabilityManifestConvID == convID &&
		sdk.lastCapabilityManifestSignature == signature {
		return false
	}

	sdk.lastCapabilityManifestConvID = convID
	sdk.lastCapabilityManifestSignature = signature
	return true
}

func buildCapabilityManifest(modeID string, providerTools []provider.Tool, maxTools int) string {
	tools := append([]provider.Tool(nil), providerTools...)
	sort.Slice(tools, func(i, j int) bool {
		return strings.ToLower(tools[i].Name) < strings.ToLower(tools[j].Name)
	})
	if maxTools <= 0 {
		maxTools = defaultCapabilityManifestToolLimit
	}

	var builder strings.Builder
	builder.WriteString("<effective_capabilities>\n")
	builder.WriteString("This is a request-scoped summary of tools actually exposed to the model")
	if strings.TrimSpace(modeID) != "" {
		fmt.Fprintf(&builder, " (mode: %s)", strings.TrimSpace(modeID))
	}
	builder.WriteString(". Tool schemas remain authoritative; this summary grants no permissions.\n")
	if len(tools) == 0 {
		builder.WriteString("No tools are available for this turn.\n</effective_capabilities>")
		return builder.String()
	}

	visible := tools
	if len(visible) > maxTools {
		visible = visible[:maxTools]
	}
	for _, tool := range visible {
		fmt.Fprintf(&builder, "- `%s`: %s\n", tool.Name, firstSentence(tool.Description))
	}
	if omitted := len(tools) - len(visible); omitted > 0 {
		fmt.Fprintf(&builder, "- %d additional tool omitted; use the tool search/discovery capability when available.\n", omitted)
	}
	builder.WriteString("</effective_capabilities>")
	return builder.String()
}

func firstSentence(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return "Available for this turn."
	}
	for index, char := range description {
		if char != '.' && char != '!' && char != '?' {
			continue
		}
		next := index + 1
		if next >= len(description) || unicode.IsSpace(rune(description[next])) {
			return strings.TrimSpace(description[:next])
		}
	}
	const maxDescriptionRunes = 160
	runes := []rune(description)
	if len(runes) > maxDescriptionRunes {
		return strings.TrimSpace(string(runes[:maxDescriptionRunes])) + "…"
	}
	return description
}
