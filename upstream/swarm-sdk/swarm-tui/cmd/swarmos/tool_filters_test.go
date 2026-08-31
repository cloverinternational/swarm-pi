package main

import (
	"fmt"
	"sort"
	"testing"
)

// fakeRegistry is an in-memory toolRegistryFilter for testing applyToolFilters
// without constructing a real SDK / tools.Registry.
type fakeRegistry struct {
	tools map[string]bool
}

func newFakeRegistry(names ...string) *fakeRegistry {
	r := &fakeRegistry{tools: make(map[string]bool)}
	for _, n := range names {
		r.tools[n] = true
	}
	return r
}

func (r *fakeRegistry) List() []string {
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (r *fakeRegistry) Unregister(name string) error {
	if !r.tools[name] {
		return fmt.Errorf("not registered: %s", name)
	}
	delete(r.tools, name)
	return nil
}

func TestApplyToolFilters_Allowlist(t *testing.T) {
	reg := newFakeRegistry("Read", "Write", "Edit", "grep", "Bash")

	if err := applyToolFilters(reg, "Read,grep", "", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := reg.List()
	want := []string{"Read", "grep"}
	if len(got) != len(want) {
		t.Fatalf("--tools allowlist: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("--tools allowlist: got %v, want %v", got, want)
		}
	}
}

func TestApplyToolFilters_Denylist(t *testing.T) {
	reg := newFakeRegistry("Read", "Write", "Edit", "grep", "Bash")

	if err := applyToolFilters(reg, "", "Bash,Write", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := reg.List()
	want := []string{"Edit", "Read", "grep"}
	if len(got) != len(want) {
		t.Fatalf("--disable-tools: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("--disable-tools: got %v, want %v", got, want)
		}
	}
}

func TestApplyToolFilters_AllowThenDeny(t *testing.T) {
	reg := newFakeRegistry("Read", "Write", "Edit", "grep", "Bash")

	// Allowlist keeps 3, then denylist drops one of them.
	if err := applyToolFilters(reg, "Read,Write,Edit", "Write", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := reg.List()
	want := []string{"Edit", "Read"}
	if len(got) != len(want) {
		t.Fatalf("allow+deny: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("allow+deny: got %v, want %v", got, want)
		}
	}
}

func TestApplyToolFilters_UnknownNameErrors(t *testing.T) {
	reg := newFakeRegistry("Read", "Write")

	err := applyToolFilters(reg, "Read,Nonexistent", "", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool name in --tools, got nil")
	}

	// A typo must not silently mutate the registry.
	if len(reg.List()) != 2 {
		t.Fatalf("registry must be untouched on validation error, got %v", reg.List())
	}
}

func TestApplyToolFilters_NoFlagsIsNoOp(t *testing.T) {
	reg := newFakeRegistry("Read", "Write", "Edit")
	before := reg.List()

	if err := applyToolFilters(reg, "", "", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reg.List()) != len(before) {
		t.Fatalf("no flags should be a no-op: got %v, want %v", reg.List(), before)
	}
}

func TestApplyToolFilters_NilRegistrySafe(t *testing.T) {
	if err := applyToolFilters(nil, "Read", "", nil); err != nil {
		t.Fatalf("nil registry must be safe, got %v", err)
	}
}
