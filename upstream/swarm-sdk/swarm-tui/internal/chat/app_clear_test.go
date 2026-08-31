package chat

import "testing"

func TestIsClearSlashCommand(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"/clear", true},
		{" /clear ", true},
		{"/clear now", true},
		{"/clearance", false},
		{"/clear-all", false},
		{"/goal clear", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := isClearSlashCommand(tt.input); got != tt.want {
			t.Fatalf("isClearSlashCommand(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
