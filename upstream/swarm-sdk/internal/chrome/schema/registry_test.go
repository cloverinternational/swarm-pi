package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRegistryCoversEveryV1ToolAndFailsClosed(t *testing.T) {
	want := []string{
		"chrome_close_session", "chrome_computer", "chrome_console", "chrome_find",
		"chrome_form_input", "chrome_get_page_text", "chrome_javascript", "chrome_navigate",
		"chrome_network", "chrome_new_tab", "chrome_read_page", "chrome_resize_window",
		"chrome_session_status", "chrome_tabs", "chrome_upload",
	}
	if got := ToolNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tool coverage = %v, want %v", got, want)
	}
	if _, err := Lookup("chrome_gif"); err == nil {
		t.Fatal("unknown/unimplemented tool did not fail closed")
	}
	for name, contract := range Registry() {
		for kind, document := range map[string]map[string]any{"input": contract.Input, "result": contract.Result} {
			if document["$schema"] != draft202012 {
				t.Errorf("%s %s has schema %v", name, kind, document["$schema"])
			}
			id, _ := document["$id"].(string)
			if !strings.Contains(id, name+"."+kind+".schema.json") {
				t.Errorf("%s %s id = %q", name, kind, id)
			}
			encoded, err := json.Marshal(document)
			if err != nil || !json.Valid(encoded) {
				t.Errorf("%s %s is not machine-readable: %v", name, kind, err)
			}
		}
	}
}

func TestEveryInputRejectsAdditionalProperties(t *testing.T) {
	valid := map[string]map[string]any{
		"chrome_session_status": {},
		"chrome_tabs":           {},
		"chrome_new_tab":        {},
		"chrome_navigate":       {"tab": "tab_AAAAAAAAAAAAAAAA", "target": map[string]any{"url": "https://example.test"}},
		"chrome_read_page":      {"tab": "tab_AAAAAAAAAAAAAAAA"},
		"chrome_get_page_text":  {"tab": "tab_AAAAAAAAAAAAAAAA"},
		"chrome_find":           {"tab": "tab_AAAAAAAAAAAAAAAA", "query": "button"},
		"chrome_form_input":     {"tab": "tab_AAAAAAAAAAAAAAAA", "ref": "ref_AAAAAAAAAAAAAAAA", "value": "hello"},
		"chrome_computer":       {"tab": "tab_AAAAAAAAAAAAAAAA", "action": "screenshot"},
		"chrome_javascript":     {"tab": "tab_AAAAAAAAAAAAAAAA", "expression": "document.title"},
		"chrome_console":        {"tab": "tab_AAAAAAAAAAAAAAAA"},
		"chrome_network":        {"tab": "tab_AAAAAAAAAAAAAAAA"},
		"chrome_resize_window":  {"width": 1280, "height": 800},
		"chrome_upload": {
			"tab":    "tab_AAAAAAAAAAAAAAAA",
			"source": map[string]any{"file_path": "/workspace/image.png"},
			"target": map[string]any{"ref": "ref_AAAAAAAAAAAAAAAA"},
		},
		"chrome_close_session": {},
	}
	for tool, input := range valid {
		raw, _ := json.Marshal(input)
		if err := ValidateInput(tool, raw); err != nil {
			t.Errorf("%s valid input: %v", tool, err)
		}
		input["unexpected"] = true
		raw, _ = json.Marshal(input)
		if err := ValidateInput(tool, raw); err == nil {
			t.Errorf("%s accepted additional property", tool)
		}
	}
}

func TestComputerDiscriminatedActionMatrix(t *testing.T) {
	tab := "tab_AAAAAAAAAAAAAAAA"
	valid := []map[string]any{
		{"tab": tab, "action": "left_click", "coordinate": []any{1, 2}},
		{"tab": tab, "action": "left_click", "ref": "ref_AAAAAAAAAAAAAAAA", "modifiers": []any{"ctrl"}},
		{"tab": tab, "action": "hover", "ref": "ref_AAAAAAAAAAAAAAAA"},
		{"tab": tab, "action": "type", "text": "hello"},
		{"tab": tab, "action": "key", "text": "Enter", "repeat": 2},
		{"tab": tab, "action": "scroll", "direction": "down", "amount": 3},
		{"tab": tab, "action": "drag", "start_coordinate": []any{1, 2}, "coordinate": []any{3, 4}},
		{"tab": tab, "action": "screenshot"},
		{"tab": tab, "action": "zoom", "region": []any{1, 2, 3, 4}},
		{"tab": tab, "action": "scroll_to", "ref": "ref_AAAAAAAAAAAAAAAA"},
		{"tab": tab, "action": "wait", "duration_ms": 10},
	}
	for _, input := range valid {
		raw, _ := json.Marshal(input)
		if err := ValidateInput("chrome_computer", raw); err != nil {
			t.Errorf("valid %v: %v", input, err)
		}
	}
	invalid := []map[string]any{
		{"tab": tab, "action": "left_click"},
		{"tab": tab, "action": "left_click", "coordinate": []any{1, 2}, "ref": "ref_AAAAAAAAAAAAAAAA"},
		{"tab": tab, "action": "screenshot", "text": "contradiction"},
		{"tab": tab, "action": "wait"},
		{"tab": tab, "action": "hover", "coordinate": []any{1, 2}, "modifiers": []any{"ctrl"}},
	}
	for _, input := range invalid {
		raw, _ := json.Marshal(input)
		if err := ValidateInput("chrome_computer", raw); err == nil {
			t.Errorf("accepted invalid %v", input)
		}
	}
}

func TestNestedOneOfAndResultContracts(t *testing.T) {
	badNavigate := []byte(`{"tab":"tab_AAAAAAAAAAAAAAAA","target":{"url":"https://example.test","history":"back"}}`)
	if err := ValidateInput("chrome_navigate", badNavigate); err == nil {
		t.Fatal("navigate target accepted contradictory fields")
	}
	badUpload := []byte(`{"tab":"tab_AAAAAAAAAAAAAAAA","source":{"file_path":"x","extra":true},"target":{"ref":"ref_AAAAAAAAAAAAAAAA"}}`)
	if err := ValidateInput("chrome_upload", badUpload); err == nil {
		t.Fatal("upload source accepted nested additional property")
	}
	browser := `"browser":{"generation":3,"request_id":"req-1","started_at":"2026-08-11T12:00:00Z","duration_ms":5,"activity_deadline":"2026-08-11T12:30:00Z"}`
	status := []byte(`{"state":"ready","generation":3,"tab_count":0,"idle_for_ms":5,"in_flight":0,` + browser + `}`)
	if err := ValidateResult("chrome_session_status", status); err != nil {
		t.Fatal(err)
	}
	statusWithError := []byte(`{"state":"failed","generation":3,"tab_count":0,"idle_for_ms":5,"in_flight":0,"last_error":` +
		`{"code":"protocol_mismatch","message":"response mismatch","retryable":false,"execution":"not_started",` +
		`"details":{"reason":"completed requires a result","missing_capability":"dom"}},` + browser + `}`)
	if err := ValidateResult("chrome_session_status", statusWithError); err != nil {
		t.Fatalf("status with structured error details: %v", err)
	}
	statusWithUnknownDetail := []byte(`{"state":"failed","generation":3,"tab_count":0,"idle_for_ms":5,"in_flight":0,"last_error":` +
		`{"code":"protocol_mismatch","message":"response mismatch","retryable":false,"execution":"not_started",` +
		`"details":{"unbounded_extension":"not allowed"}},` + browser + `}`)
	if err := ValidateResult("chrome_session_status", statusWithUnknownDetail); err == nil {
		t.Fatal("structured error details accepted an undefined member")
	}
	screenshot := []byte(`{"action":"screenshot","image_handle":"image_AAAAAAAAAAAAAAAA","width":10,"height":10,"mime_type":"image/png",` + browser + `}`)
	if err := ValidateResult("chrome_computer", screenshot); err != nil {
		t.Fatal(err)
	}
	contradictory := []byte(`{"action":"wait","waited_ms":2,"state":"ready","performed":true,` + browser + `}`)
	if err := ValidateResult("chrome_computer", contradictory); err == nil {
		t.Fatal("computer result accepted fields from another discriminator")
	}
}

func TestRegistryReturnsDeepCopy(t *testing.T) {
	copy := Registry()
	delete(copy, "chrome_tabs")
	copy["chrome_find"].Input["mutated"] = true
	if len(ToolNames()) != 15 {
		t.Fatal("registry map was mutated")
	}
	if _, exists := ChromeContractSchemaRegistryV1["chrome_find"].Input["mutated"]; exists {
		t.Fatal("registry nested map was mutated")
	}
}
