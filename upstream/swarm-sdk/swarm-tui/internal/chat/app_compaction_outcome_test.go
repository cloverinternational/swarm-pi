package chat

import (
	"errors"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
)

func TestValidateCompactionOutcomeRejectsEveryUnsafeOutcome(t *testing.T) {
	cause := errors.New("summary failed")
	tests := []struct {
		name   string
		result *compaction.CompactionResult
		err    error
		want   string
	}{
		{name: "go error", err: cause, want: "summary failed"},
		{name: "nil result", want: "no result"},
		{name: "result error", result: &compaction.CompactionResult{Error: cause}, want: "summary failed"},
		{name: "not compacted", result: &compaction.CompactionResult{}, want: "did not reduce"},
		{name: "empty summary", result: &compaction.CompactionResult{Compacted: true}, want: "empty summary"},
		{name: "not committed", result: &compaction.CompactionResult{Compacted: true, Summary: "ok"}, want: "not committed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCompactionOutcome(tt.result, tt.err)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateCompactionOutcomeAcceptsCommittedReduction(t *testing.T) {
	result := &compaction.CompactionResult{
		Compacted: true,
		Summary:   "safe handoff",
		NewConvID: "same-conversation",
	}
	if err := validateCompactionOutcome(result, nil); err != nil {
		t.Fatalf("valid outcome rejected: %v", err)
	}
}
