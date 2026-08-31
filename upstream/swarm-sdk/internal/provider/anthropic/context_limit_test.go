package anthropic

import (
	"testing"
)

func TestParseContextLimitExceeded(t *testing.T) {
	tests := []struct {
		name    string
		msg     string
		want    *contextLimitExceeded
		wantNil bool
	}{
		{
			name: "standard_error_message",
			msg:  "input length and `max_tokens` exceed context limit: 182530 + 31999 > 200000, decrease input length or `max_tokens` and try again",
			want: &contextLimitExceeded{
				InputLength: 182530,
				MaxTokens:   31999,
				ContextSize: 200000,
			},
		},
		{
			name: "1M_context_window",
			msg:  "input length and `max_tokens` exceed context limit: 950000 + 60000 > 1000000, decrease input length or `max_tokens` and try again",
			want: &contextLimitExceeded{
				InputLength: 950000,
				MaxTokens:   60000,
				ContextSize: 1000000,
			},
		},
		{
			name:    "unrelated_error",
			msg:     "invalid model specified",
			wantNil: true,
		},
		{
			name:    "partial_match_no_numbers",
			msg:     "exceed context limit: some text without numbers",
			wantNil: true,
		},
		{
			name:    "empty_message",
			msg:     "",
			wantNil: true,
		},
		{
			name:    "similar_but_wrong_format",
			msg:     "input length and max_tokens exceed context limit (no colons or pluses)",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseContextLimitExceeded(tt.msg)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected non-nil result, got nil")
			}
			if got.InputLength != tt.want.InputLength {
				t.Errorf("InputLength = %d, want %d", got.InputLength, tt.want.InputLength)
			}
			if got.MaxTokens != tt.want.MaxTokens {
				t.Errorf("MaxTokens = %d, want %d", got.MaxTokens, tt.want.MaxTokens)
			}
			if got.ContextSize != tt.want.ContextSize {
				t.Errorf("ContextSize = %d, want %d", got.ContextSize, tt.want.ContextSize)
			}
		})
	}
}

func TestClampedMaxTokens(t *testing.T) {
	tests := []struct {
		name string
		e    *contextLimitExceeded
		want int
	}{
		{
			name: "room_available",
			e: &contextLimitExceeded{
				InputLength: 182530,
				MaxTokens:   31999,
				ContextSize: 200000,
			},
			want: 17470, // 200000 - 182530
		},
		{
			name: "input_fills_window",
			e: &contextLimitExceeded{
				InputLength: 200000,
				MaxTokens:   1,
				ContextSize: 200000,
			},
			want: 0, // no room
		},
		{
			name: "input_exceeds_window",
			e: &contextLimitExceeded{
				InputLength: 200001,
				MaxTokens:   1,
				ContextSize: 200000,
			},
			want: 0, // negative room
		},
		{
			name: "1M_window_small_input",
			e: &contextLimitExceeded{
				InputLength: 950000,
				MaxTokens:   60000,
				ContextSize: 1000000,
			},
			want: 50000, // 1000000 - 950000
		},
		{
			name: "exactly_one_token_available",
			e: &contextLimitExceeded{
				InputLength: 199999,
				MaxTokens:   8192,
				ContextSize: 200000,
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.e.clampedMaxTokens()
			if got != tt.want {
				t.Errorf("clampedMaxTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}
