package chrome

import "testing"

func TestConditionalDeliveryClassification(t *testing.T) {
	tests := []struct {
		tool  string
		input map[string]any
		want  DeliveryClass
	}{
		{"chrome_session_status", map[string]any{}, DeliveryReadSafe},
		{"chrome_session_status", map[string]any{"ensure_open": true}, DeliveryStateChange},
		{"chrome_console", map[string]any{"clear": true}, DeliveryStateChange},
		{"chrome_network", map[string]any{}, DeliveryReadSafe},
		{"chrome_computer", map[string]any{"action": "screenshot"}, DeliveryReadSafe},
		{"chrome_computer", map[string]any{"action": "scroll"}, DeliveryStateChange},
		{"chrome_computer", map[string]any{"action": "left_click"}, DeliveryExternalEffect},
	}
	for _, test := range tests {
		got, err := ClassifyDelivery(test.tool, test.input)
		if err != nil || got != test.want {
			t.Errorf("%s %v = %s, %v; want %s", test.tool, test.input, got, err, test.want)
		}
	}
}
