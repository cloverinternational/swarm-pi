package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// ExportOptions provides additional options for export operations.
type ExportOptions struct {
	// IncludeMetadata includes conversation metadata.
	IncludeMetadata bool

	// IncludeEdits includes message edit history.
	IncludeEdits bool

	// IncludeTimestamps includes timestamp information.
	IncludeTimestamps bool

	// PrettyPrint formats output with indentation (JSON/HTML only).
	PrettyPrint bool
}

// Exporter handles exporting conversations in various formats.
type Exporter struct {
	options ExportOptions
}

// NewExporter creates a new exporter.
func NewExporter(opts ExportOptions) *Exporter {
	return &Exporter{
		options: opts,
	}
}

// ExportToJSON exports a conversation as JSON.
func ExportToJSON(conv *conversation.Conversation, pretty bool) ([]byte, error) {
	if conv == nil {
		return nil, sdkerr.Permanent("export.invalid_conversation", "conversation is nil")
	}

	var data []byte
	var err error

	if pretty {
		data, err = json.MarshalIndent(conv, "", "  ")
	} else {
		data, err = json.Marshal(conv)
	}

	if err != nil {
		return nil, sdkerr.Permanent("export.json_failed", err.Error())
	}

	return data, nil
}

// ExportToMarkdown exports a conversation as Markdown.
func ExportToMarkdown(conv *conversation.Conversation) ([]byte, error) {
	if conv == nil {
		return nil, sdkerr.Permanent("export.invalid_conversation", "conversation is nil")
	}

	var buf bytes.Buffer

	// Header
	buf.WriteString(fmt.Sprintf("# Conversation %s\n\n", conv.ID))

	// Metadata section
	buf.WriteString("## Metadata\n\n")
	buf.WriteString(fmt.Sprintf("- **ID**: %s\n", conv.ID))
	buf.WriteString(fmt.Sprintf("- **Mode**: %s\n", conv.Mode))
	buf.WriteString(fmt.Sprintf("- **Status**: %s\n", conv.Status))
	buf.WriteString(fmt.Sprintf("- **Created**: %s\n", conv.CreatedAt.Format(time.RFC3339)))
	buf.WriteString(fmt.Sprintf("- **Updated**: %s\n", conv.UpdatedAt.Format(time.RFC3339)))
	buf.WriteString(fmt.Sprintf("- **Total Tokens**: %d\n", conv.TotalTokens))
	buf.WriteString(fmt.Sprintf("- **Total Cost**: $%.4f\n", conv.TotalCostUSD))

	if conv.Metadata.UserID != "" {
		buf.WriteString(fmt.Sprintf("- **User ID**: %s\n", conv.Metadata.UserID))
	}

	if conv.Metadata.ProjectID != "" {
		buf.WriteString(fmt.Sprintf("- **Project ID**: %s\n", conv.Metadata.ProjectID))
	}

	if len(conv.Metadata.Tags) > 0 {
		buf.WriteString(fmt.Sprintf("- **Tags**: %s\n", strings.Join(conv.Metadata.Tags, ", ")))
	}

	buf.WriteString("\n")

	// Messages section
	if len(conv.Messages) > 0 {
		buf.WriteString("## Messages\n\n")

		for i, msg := range conv.Messages {
			// Message header
			buf.WriteString(fmt.Sprintf("### Message %d: %s\n\n", i+1, strings.ToTitle(string(msg.Role))))

			// Timestamp
			if !msg.Timestamp.IsZero() {
				buf.WriteString(fmt.Sprintf("**Time**: %s\n\n", msg.Timestamp.Format(time.RFC3339)))
			}

			// Content
			buf.WriteString("**Content**:\n\n")
			buf.WriteString(msg.Content)
			buf.WriteString("\n\n")

			// Token usage
			if msg.Tokens != nil {
				buf.WriteString(fmt.Sprintf("**Tokens**: Input: %d, Output: %d, Total: %d\n\n",
					msg.Tokens.Input, msg.Tokens.Output, msg.Tokens.Total))
			}

			// Tool calls
			if len(msg.ToolCalls) > 0 {
				buf.WriteString("**Tool Calls**:\n\n")
				for _, tc := range msg.ToolCalls {
					buf.WriteString(fmt.Sprintf("- **Name**: %s\n", tc.Name))
					if len(tc.Parameters) > 0 {
						params, _ := json.MarshalIndent(tc.Parameters, "  ", "  ")
						buf.WriteString(fmt.Sprintf("  **Parameters**:\n  ```json\n  %s\n  ```\n", string(params)))
					}
				}
				buf.WriteString("\n")
			}

			// Tool results
			if len(msg.ToolResults) > 0 {
				buf.WriteString("**Tool Results**:\n\n")
				for _, tr := range msg.ToolResults {
					buf.WriteString(fmt.Sprintf("- **Call ID**: %s\n", tr.CallID))
					buf.WriteString(fmt.Sprintf("  **Output**: %s\n", tr.Output))
					if tr.Error != nil {
						buf.WriteString(fmt.Sprintf("  **Error**: %s (%s)\n", tr.Error.Message, tr.Error.Type))
					}
				}
				buf.WriteString("\n")
			}

			buf.WriteString("---\n\n")
		}
	}

	return buf.Bytes(), nil
}

// ExportToHTML exports a conversation as HTML with syntax highlighting.
func ExportToHTML(conv *conversation.Conversation) ([]byte, error) {
	if conv == nil {
		return nil, sdkerr.Permanent("export.invalid_conversation", "conversation is nil")
	}

	var buf bytes.Buffer

	// HTML header
	buf.WriteString(`<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Conversation Export</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
            max-width: 900px;
            margin: 0 auto;
            padding: 20px;
            line-height: 1.6;
            color: #333;
        }
        .header {
            background: #f5f5f5;
            padding: 20px;
            border-radius: 5px;
            margin-bottom: 20px;
        }
        .metadata {
            display: grid;
            grid-template-columns: 1fr 1fr;
            gap: 10px;
            margin: 10px 0;
        }
        .metadata-item {
            padding: 5px;
        }
        .message {
            border-left: 4px solid #007bff;
            padding: 15px;
            margin: 15px 0;
            background: #f9f9f9;
            border-radius: 5px;
        }
        .message.user {
            border-left-color: #28a745;
        }
        .message.assistant {
            border-left-color: #007bff;
        }
        .message.system {
            border-left-color: #6c757d;
        }
        .message.tool {
            border-left-color: #ffc107;
        }
        .role {
            font-weight: bold;
            margin-bottom: 10px;
            text-transform: uppercase;
            font-size: 0.9em;
        }
        .content {
            margin: 10px 0;
            white-space: pre-wrap;
            word-wrap: break-word;
        }
        .tokens {
            font-size: 0.85em;
            color: #666;
            margin-top: 10px;
        }
        .tool-calls {
            margin-top: 10px;
            padding: 10px;
            background: #f0f0f0;
            border-radius: 3px;
        }
        code {
            background: #f4f4f4;
            padding: 2px 5px;
            border-radius: 3px;
            font-family: monospace;
        }
        pre {
            background: #f4f4f4;
            padding: 10px;
            border-radius: 5px;
            overflow-x: auto;
        }
    </style>
</head>
<body>
`)

	// Header section
	buf.WriteString(`    <div class="header">`)
	buf.WriteString(fmt.Sprintf(`        <h1>Conversation %s</h1>`, conv.ID))
	buf.WriteString(`        <div class="metadata">`)
	buf.WriteString(fmt.Sprintf(`            <div class="metadata-item"><strong>Mode:</strong> %s</div>`, conv.Mode))
	buf.WriteString(fmt.Sprintf(`            <div class="metadata-item"><strong>Status:</strong> %s</div>`, conv.Status))
	buf.WriteString(fmt.Sprintf(`            <div class="metadata-item"><strong>Created:</strong> %s</div>`, conv.CreatedAt.Format("2006-01-02 15:04:05")))
	buf.WriteString(fmt.Sprintf(`            <div class="metadata-item"><strong>Tokens:</strong> %d</div>`, conv.TotalTokens))
	if conv.Metadata.UserID != "" {
		buf.WriteString(fmt.Sprintf(`            <div class="metadata-item"><strong>User:</strong> %s</div>`, conv.Metadata.UserID))
	}
	if conv.Metadata.ProjectID != "" {
		buf.WriteString(fmt.Sprintf(`            <div class="metadata-item"><strong>Project:</strong> %s</div>`, conv.Metadata.ProjectID))
	}
	buf.WriteString(`        </div>`)
	buf.WriteString(`    </div>`)

	// Messages
	for _, msg := range conv.Messages {
		roleClass := strings.ToLower(string(msg.Role))
		buf.WriteString(fmt.Sprintf(`    <div class="message %s">`, roleClass))
		buf.WriteString(fmt.Sprintf(`        <div class="role">%s</div>`, msg.Role))
		buf.WriteString(fmt.Sprintf(`        <div class="content">%s</div>`, escapeHTML(msg.Content)))

		if msg.Tokens != nil {
			buf.WriteString(fmt.Sprintf(`        <div class="tokens">Tokens: %d input, %d output, %d total</div>`,
				msg.Tokens.Input, msg.Tokens.Output, msg.Tokens.Total))
		}

		if len(msg.ToolCalls) > 0 {
			buf.WriteString(`        <div class="tool-calls"><strong>Tool Calls:</strong><ul>`)
			for _, tc := range msg.ToolCalls {
				buf.WriteString(fmt.Sprintf(`            <li><code>%s</code></li>`, tc.Name))
			}
			buf.WriteString(`        </ul></div>`)
		}

		buf.WriteString(`    </div>`)
	}

	// Close HTML
	buf.WriteString(`</body>
</html>`)

	return buf.Bytes(), nil
}

// ExportToJSONL exports a conversation as line-delimited JSON (one message per line).
func ExportToJSONL(conv *conversation.Conversation) ([]byte, error) {
	if conv == nil {
		return nil, sdkerr.Permanent("export.invalid_conversation", "conversation is nil")
	}

	var buf bytes.Buffer

	for _, msg := range conv.Messages {
		data, err := json.Marshal(msg)
		if err != nil {
			return nil, sdkerr.Permanent("export.jsonl_failed", err.Error())
		}
		buf.Write(data)
		buf.WriteString("\n")
	}

	return buf.Bytes(), nil
}

// Helper function to escape HTML special characters.
func escapeHTML(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(s)
}

// Importer handles importing conversations from various formats.
type Importer struct{}

// NewImporter creates a new importer.
func NewImporter() *Importer {
	return &Importer{}
}

// ImportFromJSON imports a conversation from JSON.
func ImportFromJSON(data []byte) (*conversation.Conversation, error) {
	var conv conversation.Conversation
	err := json.Unmarshal(data, &conv)
	if err != nil {
		return nil, sdkerr.Permanent("import.json_failed", err.Error())
	}

	// Ensure conversation has basic fields
	if conv.ID == "" {
		conv.ID = fmt.Sprintf("imported_%d", time.Now().UnixNano())
	}
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = time.Now()
	}
	if conv.UpdatedAt.IsZero() {
		conv.UpdatedAt = time.Now()
	}

	return &conv, nil
}

// ImportFromJSONL imports a conversation from line-delimited JSON.
func ImportFromJSONL(data []byte) (*conversation.Conversation, error) {
	conv := &conversation.Conversation{
		ID:        fmt.Sprintf("imported_%d", time.Now().UnixNano()),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Messages:  make([]*conversation.Message, 0),
	}

	lines := bytes.SplitSeq(data, []byte("\n"))
	for line := range lines {
		if len(line) == 0 {
			continue
		}

		var msg conversation.Message
		err := json.Unmarshal(line, &msg)
		if err != nil {
			return nil, sdkerr.Permanent("import.jsonl_failed", fmt.Sprintf("failed to parse line: %v", err))
		}

		conv.Messages = append(conv.Messages, &msg)
	}

	// Recalculate token count
	for _, msg := range conv.Messages {
		if msg.Tokens != nil {
			conv.TotalTokens += msg.Tokens.Total
		}
	}

	return conv, nil
}
