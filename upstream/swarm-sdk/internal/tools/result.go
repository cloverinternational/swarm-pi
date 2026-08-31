package tools

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
)

// ToolResult represents the result of a tool execution with rich content support.
type ToolResult struct {
	// Output is the primary text result (legacy/convenience field)
	Output string

	// Content contains rich content blocks (images, audio, PDFs, resources)
	Content []ContentBlock

	// Error contains error information if execution failed
	Error error

	// IsError flags this as an error result
	IsError bool

	// DurationMS is how long execution took in milliseconds
	DurationMS int64

	// CacheHit indicates if this result came from cache
	CacheHit bool

	// Metadata contains additional execution details
	Metadata map[string]any

	// Annotations for provider-specific metadata
	Annotations map[string]any

	// Hosted carries additive hosted result metadata such as risk and redaction hints.
	Hosted *hosted.ResultMetadata

	// Outcome carries the tool's typed, structured terminal facts: the real
	// exit code, a success/failure verdict distinct from "the output text
	// mentions an error", and the paths a mutating tool actually touched.
	//
	// It is optional and additive. Tools that do not populate it keep working
	// unchanged, and consumers must treat nil as "no structured outcome
	// reported" — never as success. Populate it at the site that already
	// holds the information (see builtin.buildResult for exit codes and
	// forge.ApplyPatchTool.commit callers for file effects); never re-derive
	// it by parsing Output downstream.
	Outcome *toolout.Outcome
}

// NewXMLResult creates a ToolResult whose text content is XML produced by b.Build().
// It sets render_type="xml" in Metadata so the TUI renderer can syntax-highlight it.
// All other ToolResult invariants (non-empty Output, Content slice) are handled by
// the underlying NewToolResult call.
func NewXMLResult(b *XMLBuilder) *ToolResult {
	xmlStr := b.Build()
	r := NewToolResult(xmlStr)
	r.Metadata["render_type"] = "xml"
	return r
}

// NewToolResult creates a new tool result with text output.
// Empty output is replaced with "(empty)" to prevent Anthropic API
// validation errors (text content blocks require non-empty text).
func NewToolResult(output string) *ToolResult {
	if output == "" {
		output = "(empty)"
	}
	return &ToolResult{
		Output:      output,
		Content:     []ContentBlock{TextContent(output)},
		Metadata:    make(map[string]any),
		Annotations: make(map[string]any),
	}
}

// NewErrorResult creates a new error result
func NewErrorResult(err error) *ToolResult {
	return &ToolResult{
		Output:      err.Error(),
		Content:     []ContentBlock{TextContent(err.Error())},
		Error:       err,
		IsError:     true,
		Metadata:    make(map[string]any),
		Annotations: make(map[string]any),
	}
}

// NewContentResult creates a result with rich content blocks
func NewContentResult(blocks ...ContentBlock) *ToolResult {
	result := &ToolResult{
		Content:     blocks,
		Metadata:    make(map[string]any),
		Annotations: make(map[string]any),
	}

	// Set Output from first text block
	for _, block := range blocks {
		if block.Type == ContentTypeText {
			result.Output = block.Text
			break
		}
	}

	return result
}

// AddContent adds a content block to the result
func (r *ToolResult) AddContent(block ContentBlock) {
	r.Content = append(r.Content, block)

	// Update Output if it's a text block and Output is empty
	if r.Output == "" && block.Type == ContentTypeText {
		r.Output = block.Text
	}
}

// AddText adds a text content block
func (r *ToolResult) AddText(text string) {
	r.AddContent(TextContent(text))
}

// AddImage adds an image content block
func (r *ToolResult) AddImage(data []byte, mimeType string) {
	r.AddContent(ImageContent(data, mimeType))
}

// AddAudio adds an audio content block
func (r *ToolResult) AddAudio(data []byte, mimeType string) {
	r.AddContent(AudioContent(data, mimeType))
}

// AddPDF adds a PDF content block
func (r *ToolResult) AddPDF(data []byte) {
	r.AddContent(PDFContent(data))
}

// AddResource adds a resource link content block
func (r *ToolResult) AddResource(uri string, name string, description string) {
	r.AddContent(ResourceContent(uri, name, description))
}

// AddHTML adds an HTML content block for client-side rendering.
func (r *ToolResult) AddHTML(html string) {
	r.AddContent(HTMLContent(html))
}

// WithMetadata adds metadata to the result
func (r *ToolResult) WithMetadata(key string, value any) *ToolResult {
	if r.Metadata == nil {
		r.Metadata = make(map[string]any)
	}
	r.Metadata[key] = value
	return r
}

// WithAnnotation adds an annotation to the result
func (r *ToolResult) WithAnnotation(key string, value any) *ToolResult {
	if r.Annotations == nil {
		r.Annotations = make(map[string]any)
	}
	r.Annotations[key] = value
	return r
}

// WithDuration sets the execution duration
func (r *ToolResult) WithDuration(durationMS int64) *ToolResult {
	r.DurationMS = durationMS
	return r
}

// WithCacheHit marks the result as coming from cache
func (r *ToolResult) WithCacheHit(hit bool) *ToolResult {
	r.CacheHit = hit
	return r
}

// WithHostedMetadata attaches additive hosted metadata to the result.
func (r *ToolResult) WithHostedMetadata(metadata *hosted.ResultMetadata) *ToolResult {
	r.Hosted = metadata
	return r
}

// WithOutcome attaches the typed structured outcome to the result.
func (r *ToolResult) WithOutcome(outcome *toolout.Outcome) *ToolResult {
	r.Outcome = outcome
	return r
}

// HasContent checks if the result has any content blocks
func (r *ToolResult) HasContent() bool {
	return len(r.Content) > 0
}

// HasError checks if the result represents an error
func (r *ToolResult) HasError() bool {
	return r.IsError || r.Error != nil
}

// TextBlocks returns all text content blocks
func (r *ToolResult) TextBlocks() []ContentBlock {
	var blocks []ContentBlock
	for _, block := range r.Content {
		if block.Type == ContentTypeText {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// ImageBlocks returns all image content blocks
func (r *ToolResult) ImageBlocks() []ContentBlock {
	var blocks []ContentBlock
	for _, block := range r.Content {
		if block.Type == ContentTypeImage {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// AudioBlocks returns all audio content blocks
func (r *ToolResult) AudioBlocks() []ContentBlock {
	var blocks []ContentBlock
	for _, block := range r.Content {
		if block.Type == ContentTypeAudio {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// PDFBlocks returns all PDF content blocks
func (r *ToolResult) PDFBlocks() []ContentBlock {
	var blocks []ContentBlock
	for _, block := range r.Content {
		if block.Type == ContentTypePDF {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// ResourceBlocks returns all resource link blocks
func (r *ToolResult) ResourceBlocks() []ContentBlock {
	var blocks []ContentBlock
	for _, block := range r.Content {
		if block.Type == ContentTypeResource {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// HTMLBlocks returns all HTML content blocks.
func (r *ToolResult) HTMLBlocks() []ContentBlock {
	var blocks []ContentBlock
	for _, block := range r.Content {
		if block.Type == ContentTypeHTML {
			blocks = append(blocks, block)
		}
	}
	return blocks
}
