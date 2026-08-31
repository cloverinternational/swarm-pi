package configformat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// sample mirrors the shape that matters: mixed snake/camel json tags, a
// no-omitempty bool (must always serialize), an omitempty bool, and a nested
// struct — exactly the SwarmOSConfig/ConfigBundle pattern.
type sample struct {
	CurrentProvider string  `json:"current_provider"`
	CurrentModel    string  `json:"current_model"`
	SchemaVersion   int     `json:"schemaVersion"`
	ShowThinking    bool    `json:"showThinking"`           // no omitempty: explicit false must persist
	CompactMode     bool    `json:"compact_mode,omitempty"` // omitempty
	Threshold       float64 `json:"autoCompactionThresholdPercent,omitempty"`
	Nested          *inner  `json:"nested,omitempty"`
}

type inner struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func newSample() sample {
	return sample{
		CurrentProvider: "OpenAI",
		CurrentModel:    "gpt-5",
		SchemaVersion:   2,
		ShowThinking:    false,
		Threshold:       0.8,
		Nested:          &inner{Provider: "Gemini", Model: "gemini-3-pro-preview"},
	}
}

func TestYAMLRoundTripHonorsJSONTags(t *testing.T) {
	in := newSample()
	data, err := Marshal(in, FormatYAML)
	if err != nil {
		t.Fatalf("Marshal YAML: %v", err)
	}
	// json-tag names must appear verbatim (camelCase preserved, not lowercased).
	s := string(data)
	for _, want := range []string{"current_provider:", "schemaVersion:", "showThinking:", "autoCompactionThresholdPercent:"} {
		if !contains(s, want) {
			t.Errorf("YAML missing json-tag key %q\n---\n%s", want, s)
		}
	}
	var out sample
	if err := Unmarshal(data, FormatYAML, &out); err != nil {
		t.Fatalf("Unmarshal YAML: %v", err)
	}
	assertEqual(t, in, out)
}

func TestJSONStillLoadsViaUnmarshal(t *testing.T) {
	in := newSample()
	jsonBytes, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	// A .json file read through the JSON branch.
	var out sample
	if err := Unmarshal(jsonBytes, FormatJSON, &out); err != nil {
		t.Fatalf("Unmarshal JSON: %v", err)
	}
	assertEqual(t, in, out)

	// And YAML branch must also parse JSON content (superset) — proves a
	// mislabeled .yaml holding JSON still loads.
	var out2 sample
	if err := Unmarshal(jsonBytes, FormatYAML, &out2); err != nil {
		t.Fatalf("Unmarshal JSON-as-YAML: %v", err)
	}
	assertEqual(t, in, out2)
}

func TestResolvePathPrecedence(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "config.json"), "{}")

	// Only .json exists -> resolve to it.
	if p, found := ResolvePath(dir, "config"); !found || filepath.Base(p) != "config.json" {
		t.Fatalf("expected config.json, got %q found=%v", p, found)
	}

	// Add .yaml -> it must now win.
	mustWrite(t, filepath.Join(dir, "config.yaml"), "{}")
	if p, found := ResolvePath(dir, "config"); !found || filepath.Base(p) != "config.yaml" {
		t.Fatalf("expected config.yaml to win, got %q found=%v", p, found)
	}

	// Nothing for a different base -> write target .yaml, found=false.
	if p, found := ResolvePath(dir, "missing"); found || filepath.Base(p) != "missing.yaml" {
		t.Fatalf("expected missing.yaml write target, got %q found=%v", p, found)
	}
}

func TestSaveDefaultsToYAMLAndLeavesJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	mustWrite(t, jsonPath, `{"current_provider":"legacy"}`)

	written, err := Save(dir, "config", newSample(), 0o644)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if filepath.Base(written) != "config.yaml" {
		t.Fatalf("Save should write config.yaml, wrote %q", written)
	}
	// Legacy .json must still be present (left in place, not deleted).
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("legacy config.json must remain on disk: %v", err)
	}
	// And a subsequent Load must prefer the new .yaml.
	var out sample
	resolved, err := Load(dir, "config", &out)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if filepath.Base(resolved) != "config.yaml" {
		t.Fatalf("Load should resolve config.yaml, got %q", resolved)
	}
	if out.CurrentProvider != "OpenAI" {
		t.Fatalf("Load read stale data: %q", out.CurrentProvider)
	}
}

func TestSavePreserveFormatKeepsJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "providers.json")
	mustWrite(t, jsonPath, `[]`)

	written, err := Save(dir, "providers", newSample(), 0o644, PreserveFormat())
	if err != nil {
		t.Fatalf("Save preserve: %v", err)
	}
	if filepath.Base(written) != "providers.json" {
		t.Fatalf("PreserveFormat should keep providers.json, wrote %q", written)
	}
	if _, err := os.Stat(filepath.Join(dir, "providers.yaml")); !os.IsNotExist(err) {
		t.Fatalf("PreserveFormat must not create providers.yaml")
	}
}

func TestLoadMissingReturnsNotExist(t *testing.T) {
	dir := t.TempDir()
	var out sample
	_, err := Load(dir, "nope", &out)
	if !os.IsNotExist(err) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestToJSONLeavesJSONUntouched(t *testing.T) {
	raw := []byte(`{"a":1,"unknown":true}`)
	out, err := ToJSON(raw, FormatJSON)
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if string(out) != string(raw) {
		t.Fatalf("ToJSON(JSON) must be identity; got %s", out)
	}
}

// helpers

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func assertEqual(t *testing.T, want, got sample) {
	t.Helper()
	if want.CurrentProvider != got.CurrentProvider || want.CurrentModel != got.CurrentModel ||
		want.SchemaVersion != got.SchemaVersion || want.ShowThinking != got.ShowThinking ||
		want.Threshold != got.Threshold {
		t.Fatalf("round-trip mismatch:\n want %+v\n got  %+v", want, got)
	}
	if (want.Nested == nil) != (got.Nested == nil) {
		t.Fatalf("nested presence mismatch")
	}
	if want.Nested != nil && (want.Nested.Provider != got.Nested.Provider || want.Nested.Model != got.Nested.Model) {
		t.Fatalf("nested mismatch: want %+v got %+v", want.Nested, got.Nested)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
