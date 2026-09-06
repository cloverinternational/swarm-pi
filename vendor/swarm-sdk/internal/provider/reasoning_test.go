package provider

import "testing"

func TestNormalizeReasoningEffortSetting(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults auto", in: "", want: ReasoningEffortAuto},
		{name: "med alias", in: "med", want: ReasoningEffortMedium},
		{name: "x-high alias", in: "x-high", want: ReasoningEffortXHigh},
		{name: "off alias", in: "off", want: ReasoningEffortNone},
		{name: "unknown defaults auto", in: "banana", want: ReasoningEffortAuto},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeReasoningEffortSetting(tt.in)
			if got != tt.want {
				t.Fatalf("NormalizeReasoningEffortSetting(%q)=%q want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeReasoningEffort(t *testing.T) {
	if got := NormalizeReasoningEffort("auto"); got != "" {
		t.Fatalf("expected auto to map to empty request value, got %q", got)
	}
	if got := NormalizeReasoningEffort("med"); got != ReasoningEffortMedium {
		t.Fatalf("expected med alias to map to medium, got %q", got)
	}
}
