package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addTransportFraming(log string) string {
	divider := strings.Repeat("═", 70)
	log = strings.ReplaceAll(log, "\n[REQUEST] ", "\n"+divider+"\n[REQUEST] ")
	log = strings.ReplaceAll(log, "\n[RESPONSE] ", "\n"+divider+"\n[RESPONSE] ")
	log = strings.ReplaceAll(log, "[UNFRAMED_REQUEST] ", "[REQUEST] ")
	log = strings.ReplaceAll(log, "[UNFRAMED_RESPONSE] ", "[RESPONSE] ")
	return log
}

func TestCaptureFinalRequestBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		log         string
		wantOK      bool
		wantBody    string
		wantOrdinal int
		wantCount   int
	}{
		{
			name: "single request",
			log: `
════════
[REQUEST] POST https://example.test/messages
────────
{"call":1}
[RESPONSE] 200 OK
{"result":"done"}
`,
			wantOK:      true,
			wantBody:    `{"call":1}`,
			wantOrdinal: 1,
			wantCount:   1,
		},
		{
			name: "multiple requests select final body",
			log: `
[REQUEST] POST https://example.test/messages
{"call":1,"messages":["first"]}
[RESPONSE] 200 OK
{"result":"tool call"}
[REQUEST] POST https://example.test/messages
{"call":2,"messages":["first","tool result"]}
[RESPONSE] 200 OK
{"result":"final"}
`,
			wantOK:      true,
			wantBody:    `{"call":2,"messages":["first","tool result"]}`,
			wantOrdinal: 2,
			wantCount:   2,
		},
		{
			name: "marker text inside user content is ignored",
			log: `
[REQUEST] POST https://example.test/messages
{"call":1,"content":"user wrote [REQUEST] POST fake"}
[RESPONSE] 200 OK
{"result":"done"}
`,
			wantOK:      true,
			wantBody:    `{"call":1,"content":"user wrote [REQUEST] POST fake"}`,
			wantOrdinal: 1,
			wantCount:   1,
		},
		{
			name: "braces in request URL are not treated as body",
			log: `
[REQUEST] POST https://example.test/messages/{tenant}
{"call":1}
[RESPONSE] 200 OK
{"result":"done"}
`,
			wantOK:      true,
			wantBody:    `{"call":1}`,
			wantOrdinal: 1,
			wantCount:   1,
		},
		{
			name: "incomplete final request fails closed",
			log: `
[REQUEST] POST https://example.test/messages
{"call":1}
[RESPONSE] 200 OK
{"result":"retry"}
[REQUEST] POST https://example.test/messages
{"call":2,
`,
			wantOK: false,
		},
		{
			name: "response JSON cannot substitute for missing request body",
			log: `
[REQUEST] POST https://example.test/messages
request body was not written
[RESPONSE] 200 OK
{"result":"done"}
`,
			wantOK: false,
		},
		{
			name: "complete final body without response fails closed",
			log: `
[REQUEST] POST https://example.test/messages
{"call":1}
`,
			wantOK: false,
		},
		{
			name: "fully formed marker text in response is ignored without transport frame",
			log: `
[REQUEST] POST https://example.test/messages
{"call":1}
[RESPONSE] 200 OK
raw response line
[UNFRAMED_REQUEST] POST fake
{"not":"a transport request"}
[UNFRAMED_RESPONSE] 200 OK
{"not":"a transport response"}
`,
			wantOK:      true,
			wantBody:    `{"call":1}`,
			wantOrdinal: 1,
			wantCount:   1,
		},
		{
			name:   "missing marker",
			log:    `{"unrelated":"json"}`,
			wantOK: false,
		},
		{
			name: "marked request without body",
			log: `
[REQUEST] POST https://example.test/messages
no body was written
`,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "raw.log")
			if err := os.WriteFile(path, []byte(addTransportFraming(tt.log)), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			got, ok := captureFinalRequestBody(path)
			if ok != tt.wantOK {
				t.Fatalf("capture ok = %v, want %v (capture: %+v)", ok, tt.wantOK, got)
			}
			if !tt.wantOK {
				return
			}
			if string(got.Body) != tt.wantBody {
				t.Errorf("body = %s, want %s", got.Body, tt.wantBody)
			}
			if got.Ordinal != tt.wantOrdinal {
				t.Errorf("ordinal = %d, want %d", got.Ordinal, tt.wantOrdinal)
			}
			if got.Count != tt.wantCount {
				t.Errorf("count = %d, want %d", got.Count, tt.wantCount)
			}
		})
	}
}

func TestCaptureFinalRequestBodyMissingFile(t *testing.T) {
	t.Parallel()
	if got, ok := captureFinalRequestBody(filepath.Join(t.TempDir(), "missing.log")); ok {
		t.Fatalf("capture succeeded for missing file: %+v", got)
	}
}

func TestAuditRequestID(t *testing.T) {
	t.Parallel()
	if got, want := auditRequestID("audit-7", 3), "audit-7/request-3"; got != want {
		t.Fatalf("auditRequestID() = %q, want %q", got, want)
	}
}

func TestAuditUsageJSONScopesOnlyInputToRequest(t *testing.T) {
	t.Parallel()

	entry := auditEntry{
		RealUsage: &auditUsage{
			InputTokens:    200,
			InputRequestID: "audit-1/request-2",
			InputScope:     "final_provider_request",
			OutputTokens:   50,
			OutputScope:    "cumulative_agent_execution",
			TotalTokens:    250,
			TotalScope:     "final_input_plus_cumulative_output",
		},
		WireCapture: &auditWireCapture{
			RequestID: "audit-1/request-2",
			Ordinal:   2,
			Count:     2,
			Pairing:   "final_request_body_to_final_input_usage",
		},
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal audit entry: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal audit entry: %v", err)
	}
	usage := got["real_usage"].(map[string]any)
	capture := got["wire_capture"].(map[string]any)
	if usage["input_request_id"] != capture["request_id"] {
		t.Fatalf("input request identity mismatch: usage=%v capture=%v", usage["input_request_id"], capture["request_id"])
	}
	if usage["input_scope"] != "final_provider_request" {
		t.Fatalf("input scope = %v", usage["input_scope"])
	}
	if usage["output_scope"] != "cumulative_agent_execution" {
		t.Fatalf("output scope = %v", usage["output_scope"])
	}
	if usage["total_scope"] != "final_input_plus_cumulative_output" {
		t.Fatalf("total scope = %v", usage["total_scope"])
	}
}
