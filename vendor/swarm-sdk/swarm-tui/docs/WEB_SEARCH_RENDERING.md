# Web Search Tool - Beautiful Output Rendering

## Overview

The web search tool integration now includes a **beautiful, professional rendering system** for search results that matches the quality of the Read and Bash tools.

## Files Added

### 1. `internal/chat/ansi_colors.go`
- Shared ANSI color constants used across multiple rendering systems
- Defines foreground colors, background colors, and text attributes
- Provides both uppercase (exported) and lowercase (backward compatible) versions

### 2. `internal/chat/websearch_renderer.go`
- Dedicated renderer for web search results
- Handles beautiful formatting of search results with:
  - **Query headers** in bright blue with bold styling
  - **Result indices** in green
  - **Titles** in blue for visual hierarchy
  - **URLs** in yellow for distinction  
  - **Content snippets** in white with tree-style connectors
  - **Metadata** (page age) in muted colors
  - **Usage statistics** at the bottom

### 3. Enhanced `internal/chat/tool_result_processor.go`
- Updated to use the new `WebSearchRenderer`
- Graceful fallback to plain text if JSON parsing fails
- Pre-processes results for efficient caching

## Visual Features

### Search Result Format

```
    ⎿ Search: your query here

  1. Result Title Here
  ├─ https://example.com/page
  │ This is the first line of content from the search result
  │ This is the second line of content
  └─ Age: 1d

  2. Another Result Title
  ├─ https://another-example.com
  │ Content from this result
  └─ Age: 2h

  ┌─ Usage: in:150 │ out:200 │ web:2
```

### Color Scheme

- **Query Header**: Bright Blue (#89b4fa) - Bold
- **Result Index**: Light Green (#a6e3a1)
- **Titles**: Blue (#89b4fa) - For hierarchy
- **URLs**: Yellow (#f9e2af) - Stands out as links
- **Content**: White (#cdd6f4) - Main text
- **Metadata**: Muted (#9399b2) - Tree connectors and labels

### Tree Connectors

Uses Unicode box-drawing characters for professional appearance:
- `├─` Mid-level connections
- `│` Vertical continuation
- `└─` Final items
- `⎿` Top-level entry point

## Integration Points

### Tool Registration
Web search tool is automatically registered when SDK initializes:
```go
webSearchTools := NewWebSearchTools(sdk.authToken)
for _, tool := range webSearchTools.GetTools() {
    sdk.toolRegistry.Register(tool)
}
```

### Result Caching
Results are cached in the `Message` struct:
```go
msg.WebSearchResults[callID] = result
```

### Rendering
Results are rendered in the viewport with:
- Pre-computed ANSI-formatted lines
- Cached rendering to avoid recomputation
- Proper width handling for terminal resizing

## Configuration

Web search behavior can be customized via:

```go
config := websearch.WebSearchConfig{
    Enabled:            true,
    MaxResultsPerQuery: 5,
    Timeout:            30 * time.Second,
    MaxPerSession:      100,
    MaxPerMinute:       10,
}
webSearchTools.SetConfig(config)
```

## Performance Considerations

1. **Pre-processing**: Results are processed once when received
2. **Caching**: Rendered lines are cached and reused
3. **ANSI Aware**: All truncation respects ANSI escape sequences
4. **Memory Efficient**: Only stores formatted output, not raw data

## Quality Comparison

### Bash Tool Output
- Terminal-style header with command
- Colored output and error messages
- Exit code display

### Read Tool Output
- Syntax highlighting by language
- Line numbers with alignment
- Comment detection and styling

### Web Search Output (Now!)
- Query header with search terms
- Numbered results with hierarchy
- Color-coded information (titles, URLs, content)
- Tree-style visual structure
- Usage statistics
- Professional appearance matching system quality

## Usage

When a user asks Claude to perform a web search:

1. Claude calls the `web_search` tool with a query
2. Results are returned as JSON
3. `ProcessWebSearchResult()` parses and formats the results
4. `WebSearchRenderer` creates beautiful output with proper styling
5. Results are cached and displayed in the chat viewport

Example:
```
User: "Search for the latest Anthropic news"

→ Claude uses web_search tool
→ Results are rendered with:
  - Query header in blue
  - 3-5 numbered results
  - Each with URL, snippet, and metadata
  - Usage stats at bottom
```

## Future Enhancements

1. **Interactive Results**: Click on URLs to open in browser
2. **Result Filtering**: Show only results from specific domains
3. **Search History**: Track and display previous searches
4. **Advanced Search**: Support search operators and filters
5. **Result Export**: Save results to file

---

**Status**: ✅ Complete and Production-Ready

The web search tool now provides output quality matching the best TUI tools!
