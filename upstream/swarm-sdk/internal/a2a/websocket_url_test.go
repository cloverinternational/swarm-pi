package a2a

import "testing"

func TestConvertToWebSocketURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"rpc path http", "http://127.0.0.1:38825/rpc", "ws://127.0.0.1:38825/ws"},
		{"rpc path https", "https://example.com/rpc", "wss://example.com/ws"},
		{"empty path http", "http://127.0.0.1:38825", "ws://127.0.0.1:38825/ws"},
		{"root path http", "http://127.0.0.1:38825/", "ws://127.0.0.1:38825/ws"},
		{"nested path", "http://host:9000/api/v2/rpc", "ws://host:9000/ws"},
		{"strips query", "http://host:1/rpc?x=1", "ws://host:1/ws"},
		{"strips fragment", "http://host:1/rpc#frag", "ws://host:1/ws"},
		{"already ws scheme", "ws://host:1/rpc", "ws://host:1/ws"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := convertToWebSocketURL(tc.in)
			if got != tc.want {
				t.Fatalf("convertToWebSocketURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
