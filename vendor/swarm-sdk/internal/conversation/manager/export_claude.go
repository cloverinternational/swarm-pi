package manager

import (
	"encoding/json"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/converters/claude"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// Add Claude Code format to existing export formats in manager.go:
// const (
//     ExportFormatJSON       ExportFormat = "json"
//     ExportFormatMarkdown   ExportFormat = "markdown"
//     ExportFormatHTML       ExportFormat = "html"
//     ExportFormatJSONL      ExportFormat = "jsonl"
//     ExportFormatClaudeCode ExportFormat = "claude"  // Add this
// )

// ExportToClaudeCode exports a conversation to Claude Code format
func ExportToClaudeCode(conv *conversation.Conversation) ([]byte, error) {
	if conv == nil {
		return nil, sdkerr.Permanent("export.invalid_conversation", "conversation is nil")
	}

	converter := claude.NewClaudeCodeConverter()
	claudeConv, err := converter.ExportConversation(conv)
	if err != nil {
		return nil, err
	}

	// Marshal to JSON with pretty printing
	data, err := json.MarshalIndent(claudeConv, "", "  ")
	if err != nil {
		return nil, sdkerr.Permanent("export.claude_json_failed", err.Error())
	}

	return data, nil
}

// ImportFromClaudeCode imports a conversation from Claude Code format
func ImportFromClaudeCode(data []byte) (*conversation.Conversation, error) {
	var claudeConv claude.ClaudeConversation
	err := json.Unmarshal(data, &claudeConv)
	if err != nil {
		return nil, sdkerr.Permanent("import.claude_json_failed", err.Error())
	}

	converter := claude.NewClaudeCodeConverter()
	return converter.ImportConversation(&claudeConv)
}

// Update the Export function in manager.go to handle Claude Code format:
// func (m *Manager) Export(ctx context.Context, convID string, format ExportFormat) ([]byte, error) {
//     // ... existing code ...
//     switch format {
//     case ExportFormatJSON:
//         return ExportToJSON(conv, true)
//     case ExportFormatMarkdown:
//         return ExportToMarkdown(conv)
//     case ExportFormatHTML:
//         return ExportToHTML(conv)
//     case ExportFormatJSONL:
//         return ExportToJSONL(conv)
//     case ExportFormatClaudeCode:  // Add this case
//         return ExportToClaudeCode(conv)
//     default:
//         return nil, sdkerr.Permanent("manager.invalid_format",
//             fmt.Sprintf("unsupported export format: %s", format))
//     }
// }

// Update the Import function in manager.go to handle Claude Code format:
// func (m *Manager) Import(ctx context.Context, data []byte, format ExportFormat) (*conversation.Conversation, error) {
//     // ... existing code ...
//     switch format {
//     case ExportFormatJSON:
//         conv, err = ImportFromJSON(data)
//     case ExportFormatJSONL:
//         conv, err = ImportFromJSONL(data)
//     case ExportFormatClaudeCode:  // Add this case
//         conv, err = ImportFromClaudeCode(data)
//     default:
//         return nil, sdkerr.Permanent("manager.invalid_format",
//             fmt.Sprintf("unsupported import format: %s", format))
//     }
//     // ... rest of the function ...
// }
