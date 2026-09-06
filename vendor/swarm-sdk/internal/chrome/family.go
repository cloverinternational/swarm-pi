// Package chrome contains trusted runtime identity for the built-in Chrome
// subsystem. It deliberately has no dependency on tools or providers.
package chrome

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

var (
	// ErrBrowserFamilyAuthority is a hard authorization failure. Browser
	// operations must not continue when errors.Is reports this value.
	ErrBrowserFamilyAuthority = errors.New("chrome: browser family authority unavailable")
	errContradictoryFamily    = fmt.Errorf("%w: contradictory browser family context", ErrBrowserFamilyAuthority)
)

// FamilyID is an opaque, comparable manager key. Its representation and root
// identity are intentionally not serializable or stringable.
type FamilyID struct {
	digest [sha256.Size]byte
}

// FamilyContext is immutable authority shared by a root and verified children.
type FamilyContext struct {
	id FamilyID
}

func (f FamilyContext) Valid() bool  { return f.id != (FamilyID{}) }
func (f FamilyContext) ID() FamilyID { return f.id }
func (f FamilyContext) SameFamily(other FamilyContext) bool {
	return f.Valid() && other.Valid() && f.id == other.id
}

// PersistenceHash returns a domain-separated one-way identifier suitable for
// non-secret claim metadata. It is not browser authority and cannot be used to
// reconstruct a FamilyContext.
func (f FamilyContext) PersistenceHash() [sha256.Size]byte {
	if !f.Valid() {
		return [sha256.Size]byte{}
	}
	input := make([]byte, 0, len("swarm.chrome.persistence.v1\x00")+len(f.id.digest))
	input = append(input, "swarm.chrome.persistence.v1\x00"...)
	input = append(input, f.id.digest[:]...)
	return sha256.Sum256(input)
}

// ConversationLookup must load exactly the requested persisted conversation.
// It must never substitute a latest or workspace-selected record.
type ConversationLookup interface {
	LoadConversation(context.Context, string) (*conversation.Conversation, error)
}

// Resolver verifies parent join keys and resolves a conversation to its root.
type Resolver struct {
	lookup ConversationLookup
}

func NewFamilyResolver(lookup ConversationLookup) (*Resolver, error) {
	if lookup == nil {
		return nil, fmt.Errorf("%w: nil conversation lookup", ErrBrowserFamilyAuthority)
	}
	return &Resolver{lookup: lookup}, nil
}

// Resolve follows exact persisted parent IDs. Missing parents, malformed
// metadata, cycles, and partial join keys fail closed.
func (r *Resolver) Resolve(ctx context.Context, conversationID string) (FamilyContext, error) {
	if r == nil || r.lookup == nil {
		return FamilyContext{}, fmt.Errorf("%w: resolver is not configured", ErrBrowserFamilyAuthority)
	}
	if !validConversationID(conversationID) {
		return FamilyContext{}, fmt.Errorf("%w: invalid conversation identity", ErrBrowserFamilyAuthority)
	}

	seen := make(map[string]struct{})
	currentID := conversationID
	expectedAgentID := ""
	for {
		if _, exists := seen[currentID]; exists {
			return FamilyContext{}, fmt.Errorf("%w: parent lineage cycle", ErrBrowserFamilyAuthority)
		}
		seen[currentID] = struct{}{}

		conv, err := r.lookup.LoadConversation(ctx, currentID)
		if err != nil {
			return FamilyContext{}, fmt.Errorf("%w: load conversation %q: %v", ErrBrowserFamilyAuthority, currentID, err)
		}
		if conv == nil || conv.ID != currentID || !validConversationID(conv.ID) {
			return FamilyContext{}, fmt.Errorf("%w: conversation lookup identity mismatch", ErrBrowserFamilyAuthority)
		}
		if expectedAgentID != "" {
			actualAgentID, present, err := strictCustomString(conv.Metadata.Custom, conversation.CustomKeyAgentID)
			if err != nil {
				return FamilyContext{}, err
			}
			if !present || actualAgentID != expectedAgentID {
				return FamilyContext{}, fmt.Errorf("%w: parent agent identity mismatch", ErrBrowserFamilyAuthority)
			}
		}

		parentID, parentAgentID, err := persistedParent(conv.Metadata)
		if err != nil {
			return FamilyContext{}, err
		}
		if parentID == "" {
			return familyForRoot(conv.ID), nil
		}
		if parentID == currentID || parentAgentID == "" {
			return FamilyContext{}, fmt.Errorf("%w: contradictory parent lineage", ErrBrowserFamilyAuthority)
		}
		currentID = parentID
		expectedAgentID = parentAgentID
	}
}

func persistedParent(md conversation.ConversationMetadata) (string, string, error) {
	parentID, parentPresent, err := strictCustomString(md.Custom, conversation.CustomKeyParentConversationID)
	if err != nil {
		return "", "", err
	}
	parentAgentID, agentPresent, err := strictCustomString(md.Custom, conversation.CustomKeyParentAgentID)
	if err != nil {
		return "", "", err
	}
	if parentPresent != agentPresent {
		return "", "", fmt.Errorf("%w: incomplete parent join key", ErrBrowserFamilyAuthority)
	}
	if parentPresent && (!validConversationID(parentID) || strings.TrimSpace(parentAgentID) == "") {
		return "", "", fmt.Errorf("%w: invalid parent join key", ErrBrowserFamilyAuthority)
	}
	return parentID, parentAgentID, nil
}

func strictCustomString(custom map[string]any, key string) (string, bool, error) {
	if custom == nil {
		return "", false, nil
	}
	raw, present := custom[key]
	if !present {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok || strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return "", false, fmt.Errorf("%w: malformed %s", ErrBrowserFamilyAuthority, key)
	}
	return value, true, nil
}

func validConversationID(id string) bool { return id != "" && id == strings.TrimSpace(id) }

func familyForRoot(rootConversationID string) FamilyContext {
	return FamilyContext{
		id: FamilyID{digest: sha256.Sum256([]byte("swarm.chrome.family.v1\x00" + rootConversationID))},
	}
}

type familyContextKey struct{}

// Bind attaches trusted authority. Rebinding to another family fails instead
// of silently replacing authority.
func Bind(ctx context.Context, family FamilyContext) (context.Context, error) {
	if ctx == nil || !family.Valid() {
		return nil, fmt.Errorf("%w: missing family context", ErrBrowserFamilyAuthority)
	}
	if existing, ok := FromContext(ctx); ok {
		if !existing.SameFamily(family) {
			return nil, errContradictoryFamily
		}
		return ctx, nil
	}
	return context.WithValue(ctx, familyContextKey{}, family), nil
}

// FromContext has no string/map fallback, so model text and arguments cannot
// forge family authority.
func FromContext(ctx context.Context) (FamilyContext, bool) {
	if ctx == nil {
		return FamilyContext{}, false
	}
	family, ok := ctx.Value(familyContextKey{}).(FamilyContext)
	return family, ok && family.Valid()
}

func Require(ctx context.Context) (FamilyContext, error) {
	family, ok := FromContext(ctx)
	if !ok {
		return FamilyContext{}, ErrBrowserFamilyAuthority
	}
	return family, nil
}
