# Fix: Empty Tool Result API Validation Error

## Problem

When the TUI's `swarm_Read` tool encountered an empty file, it would cause the entire agent execution to fail with:

```
ERROR Request failed: agent execution failed: all providers exhausted: [permanent]
anthropic.invalid_request: messages.10.content.2.tool_result.content.0.text.text:
Field required (type: invalid_request_error)
```

## Root Cause

The SDK was creating a `ToolResult` with:
- `Output = ""` (empty string)
- `Content = []` (empty array)

This was being translated to the Anthropic API format as:

```json
{
  "type": "tool_result",
  "tool_use_id": "toolu_xyz",
  "content": ""
}
```

However, the Anthropic Messages API schema requires `tool_result.content` to be either:
1. A **non-empty string**, OR
2. An **array of content blocks** with at least one text block containing a `text` field

An empty string `""` violates this schema and triggers validation errors.

## Solution

**File:** `sdk/provider/anthropic/translate.go` (lines 654-669)

Added validation to detect when `toolResult.Output` is an empty string and no rich content blocks exist. In this case, wrap the empty string in a proper text content block:

```go
// CRITICAL FIX: Anthropic API requires tool_result.content to be either:
// 1. A non-empty string, OR
// 2. An array of content blocks with at least one text block
// Empty string "" violates the schema and causes:
// "messages.X.content.Y.tool_result.content.0.text.text: Field required"
//
// When toolResult.Output is empty AND no rich content blocks exist,
// wrap it in a text block to satisfy the API schema.
if outputStr, ok := content.(string); ok && outputStr == "" {
    content = []ContentBlock{
        {
            Type: "text",
            Text: "(empty file)",
        },
    }
}
```

## Result

Empty files now generate valid Anthropic API format:

```json
{
  "type": "tool_result",
  "tool_use_id": "toolu_xyz",
  "content": [
    {
      "type": "text",
      "text": "(empty file)"
    }
  ]
}
```

## Testing

Added comprehensive test suite in `sdk/provider/anthropic/translate_empty_tool_result_test.go`:

- ✅ `TestTranslateMessages_EmptyToolResult` - Verifies handling of:
  - Empty output strings → wrapped in text block
  - Non-empty output strings → passed as-is
  - Empty output with rich content → uses rich content

- ✅ `TestTranslateMessages_EmptyToolResultJSONSchema` - Validates exact JSON structure sent to API

All tests pass:
```
=== RUN   TestTranslateMessages_EmptyToolResult
--- PASS: TestTranslateMessages_EmptyToolResult (0.00s)
=== RUN   TestTranslateMessages_EmptyToolResultJSONSchema
    ✓ Empty tool result properly formatted with text: "(empty file)"
--- PASS: TestTranslateMessages_EmptyToolResultJSONSchema (0.00s)
PASS
```

## Commits

**SDK:**
- `9d0ecca` - fix: handle empty tool results to prevent Anthropic API validation errors

**TUI:**
- `c53500e` - feat: refactor conversations view with improved layout and SDK update

## Impact

This fix resolves a critical issue where the TUI would completely fail when:
- Reading empty files
- Any tool returned an empty string output
- The agent was in the middle of a conversation

Without this fix, the entire conversation would be interrupted and the user would see a cryptic API validation error.

## Future Considerations

Other tools that might return empty outputs should be reviewed to ensure they handle this case properly. The fix is implemented at the SDK translation layer, so it protects all tools automatically.
