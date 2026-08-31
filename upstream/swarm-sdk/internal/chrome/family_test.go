package chrome

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

type mapLookup map[string]*conversation.Conversation

func (m mapLookup) LoadConversation(_ context.Context, id string) (*conversation.Conversation, error) {
	conv, ok := m[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return conv, nil
}

func root(id string) *conversation.Conversation {
	return &conversation.Conversation{
		ID: id,
		Metadata: conversation.ConversationMetadata{Custom: map[string]any{
			conversation.CustomKeyAgentID: "parent-agent",
		}},
	}
}

func child(id, parentID string) *conversation.Conversation {
	return &conversation.Conversation{
		ID: id,
		Metadata: conversation.ConversationMetadata{Custom: map[string]any{
			conversation.CustomKeyAgentID:              "parent-agent",
			conversation.CustomKeyParentConversationID: parentID,
			conversation.CustomKeyParentAgentID:        "parent-agent",
		}},
	}
}

func mustResolver(t *testing.T, lookup ConversationLookup) *Resolver {
	t.Helper()
	resolver, err := NewFamilyResolver(lookup)
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func TestResolveSameRootAndDifferentRootIsolation(t *testing.T) {
	lookup := mapLookup{
		"root-a": root("root-a"),
		"child":  child("child", "root-a"),
		"grand":  child("grand", "child"),
		"root-b": root("root-b"),
	}
	resolver := mustResolver(t, lookup)

	rootFamily, err := resolver.Resolve(t.Context(), "root-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"child", "grand"} {
		got, err := resolver.Resolve(t.Context(), id)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", id, err)
		}
		if !got.SameFamily(rootFamily) {
			t.Fatalf("%q did not inherit root family", id)
		}
	}
	other, err := resolver.Resolve(t.Context(), "root-b")
	if err != nil {
		t.Fatal(err)
	}
	if other.SameFamily(rootFamily) || other.ID() == rootFamily.ID() {
		t.Fatal("unrelated roots resolved to the same family")
	}
	if rootFamily.PersistenceHash() == other.PersistenceHash() {
		t.Fatal("unrelated roots received the same persistence hash")
	}
	if rootFamily.PersistenceHash() == rootFamily.ID().digest {
		t.Fatal("persistence hash was not domain-separated from manager authority")
	}
}

func TestResolveFailsClosedForInvalidLineage(t *testing.T) {
	tests := map[string]mapLookup{
		"missing parent": {
			"child": child("child", "absent"),
		},
		"cycle": {
			"a": child("a", "b"),
			"b": child("b", "a"),
		},
		"partial join key": {
			"child": {
				ID: "child",
				Metadata: conversation.ConversationMetadata{Custom: map[string]any{
					conversation.CustomKeyParentConversationID: "root",
				}},
			},
		},
		"malformed join key": {
			"child": {
				ID: "child",
				Metadata: conversation.ConversationMetadata{Custom: map[string]any{
					conversation.CustomKeyParentConversationID: 42,
					conversation.CustomKeyParentAgentID:        "parent-agent",
				}},
			},
		},
		"self parent": {
			"child": child("child", "child"),
		},
		"parent agent mismatch": {
			"child": child("child", "root"),
			"root": {
				ID: "root",
				Metadata: conversation.ConversationMetadata{Custom: map[string]any{
					conversation.CustomKeyAgentID: "different-agent",
				}},
			},
		},
		"missing parent agent identity": {
			"child": child("child", "root"),
			"root":  {ID: "root"},
		},
		"lookup identity mismatch": {
			"child": {ID: "different-conversation"},
		},
	}
	for name, lookup := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := mustResolver(t, lookup).Resolve(t.Context(), "child")
			if !errors.Is(err, ErrBrowserFamilyAuthority) {
				t.Fatalf("error = %v, want browser authority failure", err)
			}
		})
	}
}

func TestResolveLegacyRootWithoutAgentIdentity(t *testing.T) {
	family, err := mustResolver(t, mapLookup{
		"legacy-root": {ID: "legacy-root"},
	}).Resolve(t.Context(), "legacy-root")
	if err != nil {
		t.Fatal(err)
	}
	if !family.Valid() {
		t.Fatal("legacy root resolved to an invalid family")
	}
}

func TestContextAuthorityCannotBeForgedOrContradicted(t *testing.T) {
	resolver := mustResolver(t, mapLookup{
		"a": root("a"),
		"b": root("b"),
	})
	a, _ := resolver.Resolve(t.Context(), "a")
	b, _ := resolver.Resolve(t.Context(), "b")

	forged := context.WithValue(t.Context(), "family_id", a.ID())
	if _, err := Require(forged); !errors.Is(err, ErrBrowserFamilyAuthority) {
		t.Fatalf("forged value was accepted: %v", err)
	}

	bound, err := Bind(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(bound, b); !errors.Is(err, ErrBrowserFamilyAuthority) {
		t.Fatalf("contradictory authority was accepted: %v", err)
	}
	if got, err := Require(bound); err != nil || !got.SameFamily(a) {
		t.Fatalf("Require() = (%v, %v), want family a", got.Valid(), err)
	}
}

func TestMissingAuthorityFailsClosed(t *testing.T) {
	if _, err := Require(t.Context()); !errors.Is(err, ErrBrowserFamilyAuthority) {
		t.Fatalf("Require() error = %v", err)
	}
	if _, err := Bind(t.Context(), FamilyContext{}); !errors.Is(err, ErrBrowserFamilyAuthority) {
		t.Fatalf("Bind(zero) error = %v", err)
	}
	if got := (FamilyContext{}).PersistenceHash(); got != ([32]byte{}) {
		t.Fatalf("zero family persistence hash = %x", got)
	}
}
