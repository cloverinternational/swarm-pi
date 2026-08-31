package chat

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
)

func validateCompactionOutcome(result *compaction.CompactionResult, err error) error {
	if err != nil {
		return fmt.Errorf("auto-compaction failed before provider call: %w", err)
	}
	if result == nil {
		return fmt.Errorf("auto-compaction returned no result")
	}
	if result.Error != nil {
		return fmt.Errorf("auto-compaction failed before provider call: %w", result.Error)
	}
	if !result.Compacted {
		return fmt.Errorf("auto-compaction did not reduce the active context")
	}
	if result.Summary == "" {
		return fmt.Errorf("auto-compaction returned an empty summary")
	}
	if result.NewConvID == "" {
		return fmt.Errorf("auto-compaction was not committed")
	}
	return nil
}
