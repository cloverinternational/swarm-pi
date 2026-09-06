package serve

import (
	"encoding/json"
	"testing"
)

// TestSendMessageParams_AcceptsBothConvIDCasings locks in the hardening that
// serve's sendMessage tolerates BOTH conversation-id spellings. This method's
// wire key is "convId" while getMessages/setConversationTitle use "convID"; a
// client that reused the getMessages casing here would otherwise have its
// conversation id silently dropped and the turn sent to whatever the active
// conversation happens to be — a subtle "sent to the wrong conversation" bug
// that reads to the user as flaky RPC behaviour from the mobile app.
func TestSendMessageParams_AcceptsBothConvIDCasings(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"canonical_convId", `{"convId":"c-123","message":"hi"}`, "c-123"},
		{"alt_convID", `{"convID":"c-456","message":"hi"}`, "c-456"},
		{"canonical_wins_when_both", `{"convId":"c-canon","convID":"c-alt","message":"hi"}`, "c-canon"},
		{"empty_means_active", `{"message":"hi"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p sendMessageParams
			if err := json.Unmarshal([]byte(tc.body), &p); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := p.resolveConvID(); got != tc.want {
				t.Fatalf("resolveConvID() = %q, want %q", got, tc.want)
			}
		})
	}
}
