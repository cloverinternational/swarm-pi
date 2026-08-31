package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// DefaultResultSpillThreshold is the text size that triggers spilling.
// Larger successful results are persisted and replaced with a bounded head/tail excerpt.
const DefaultResultSpillThreshold = 16 * 1024

// ResultSpillConfig controls the post-execution result spill layer.
//
// A zero-value config uses the defaults. Set Disabled to bypass spilling.
// ToolThresholds keys are case-insensitive; a negative threshold opts that tool
// out, while a positive value replaces ThresholdBytes for that tool.
// Directory is primarily useful to embedders and tests. When empty, spills are
// stored under the owning conversation directory and share its lifecycle.
type ResultSpillConfig struct {
	Disabled       bool
	ThresholdBytes int
	ToolThresholds map[string]int
	Directory      string
}

type resultSpillConfigKey struct{}

// WithResultSpillConfig overrides result spilling for executions using ctx.
func WithResultSpillConfig(ctx context.Context, cfg ResultSpillConfig) context.Context {
	return context.WithValue(ctx, resultSpillConfigKey{}, cfg)
}

// SpillToolResult applies the shared post-execution spill policy. It is
// exported for execution paths such as StreamingTool that cannot use
// measuredExecute.
func SpillToolResult(ctx context.Context, toolName string, result *ToolResult, execErr error) *ToolResult {
	if result == nil || execErr != nil || result.IsError || result.Error != nil {
		return result
	}

	cfg, _ := ctx.Value(resultSpillConfigKey{}).(ResultSpillConfig)
	if cfg.Disabled {
		return result
	}
	threshold := spillThreshold(cfg, toolName)
	if threshold < 0 {
		return result
	}

	payload := result.Output
	for _, block := range result.Content {
		if block.Type != ContentTypeText && block.Type != ContentTypeHTML {
			continue
		}
		// NewToolResult mirrors Output into Content; do not persist that twice.
		if block.Text == "" || block.Text == result.Output {
			continue
		}
		if payload != "" {
			payload += "\n\n--- additional content block ---\n\n"
		}
		payload += block.Text
	}
	if len(payload) <= threshold {
		return result
	}

	spillDir := cfg.Directory
	if spillDir == "" {
		conversationID := safeSpillName(OwnerConversationID(ctx), "unscoped")
		spillDir = filepath.Join(paths.ConversationsDir(), conversationID, "spill")
	}
	if err := os.MkdirAll(spillDir, 0o700); err != nil {
		return result // Never discard output unless its durable copy exists.
	}

	name := safeSpillName(toolName, "tool")
	callID := safeSpillName(ToolCallID(ctx), fmt.Sprintf("%d", time.Now().UnixNano()))
	path := filepath.Join(spillDir, name+"-"+callID+".txt")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		return result
	}

	pointer := fmt.Sprintf(
		"\n\n[output truncated: %d bytes total; full output: %s; read with Read/Bash offset tools]",
		len(payload), path,
	)
	excerptBudget := threshold - len(pointer)
	if excerptBudget < 256 {
		excerptBudget = 256
	}
	excerpt := headTailBytes(payload, excerptBudget) + pointer
	result.Output = excerpt

	// NewToolResult mirrors Output into a text content block. Rewrite textual
	// blocks too, otherwise providers that prefer rich Content would still send
	// and persist the full result. Preserve non-text blocks (notably images).
	textWritten := false
	for i := range result.Content {
		if result.Content[i].Type != ContentTypeText && result.Content[i].Type != ContentTypeHTML {
			continue
		}
		if !textWritten {
			result.Content[i].Text = excerpt
			textWritten = true
		} else {
			result.Content[i].Text = ""
		}
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	result.Metadata["truncated"] = true
	result.Metadata["original_output_bytes"] = len(payload)
	result.Metadata["full_output_path"] = path
	return result
}

func spillThreshold(cfg ResultSpillConfig, toolName string) int {
	key := strings.ToLower(toolName)
	for name, threshold := range cfg.ToolThresholds {
		if strings.ToLower(name) == key {
			return threshold
		}
	}
	switch key {
	case "apply_patch":
		return -1
	case "bash", "read", "file_read":
		// Bash already owns a 50 KiB/2,000-line spill contract that preserves
		// raw stdout/stderr. Keeping this above that cap avoids replacing its
		// pointer with a spill of the already-truncated excerpt.
		return 64 * 1024
	}
	if cfg.ThresholdBytes > 0 {
		return cfg.ThresholdBytes
	}
	return DefaultResultSpillThreshold
}

func safeSpillName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}

func headTailBytes(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	headLen := budget / 2
	tailStart := len(s) - (budget - headLen)
	for headLen > 0 && !utf8.RuneStart(s[headLen]) {
		headLen--
	}
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	return s[:headLen] + "\n\n... [excerpt elided] ...\n\n" + s[tailStart:]
}
