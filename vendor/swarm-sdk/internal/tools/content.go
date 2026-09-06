// Package tools defines the interface for tools that agents can execute.
// This is Ring 0 - pure interface definitions with no implementations.
package tools

// ContentType represents supported content types for tool results
type ContentType string

const (
	// ContentTypeText represents plain text content
	ContentTypeText ContentType = "text"

	// ContentTypeImage represents image content (JPEG, PNG, WebP, GIF)
	ContentTypeImage ContentType = "image"

	// ContentTypeAudio represents audio content (MP3, WAV, etc.)
	ContentTypeAudio ContentType = "audio"

	// ContentTypePDF represents PDF document content
	ContentTypePDF ContentType = "pdf"

	// ContentTypeVideo represents video content (MP4, WebM, etc.)
	ContentTypeVideo ContentType = "video"

	// ContentTypeResource represents a linked resource (URI-based)
	ContentTypeResource ContentType = "resource"

	// ContentTypeEmbedded represents embedded resource content
	ContentTypeEmbedded ContentType = "embedded"

	// ContentTypeHTML represents HTML content for client-side rendering.
	// Follows the MCP spec EmbeddedResource pattern with mimeType "text/html".
	ContentTypeHTML ContentType = "html"
)

// ContentBlock represents rich content in tool results
type ContentBlock struct {
	// Type indicates the content type
	Type ContentType

	// For text content
	Text string

	// For binary content (images, audio, PDF, video)
	Data     []byte
	MimeType string

	// For resource links
	URI         string
	Name        string
	Description string
	Size        *int64

	// Annotations for provider-specific metadata
	Annotations map[string]any
}

// TextContent creates a text content block
func TextContent(text string) ContentBlock {
	return ContentBlock{
		Type: ContentTypeText,
		Text: text,
	}
}

// ImageContent creates an image content block
func ImageContent(data []byte, mimeType string) ContentBlock {
	return ContentBlock{
		Type:     ContentTypeImage,
		Data:     data,
		MimeType: mimeType,
	}
}

// ImageContentBase64 creates an image content block from base64-encoded data.
// This is useful when the image data is already base64 encoded (e.g., from screenshots).
func ImageContentBase64(base64Data string, mimeType string) ContentBlock {
	return ContentBlock{
		Type:     ContentTypeImage,
		Text:     base64Data, // Store base64 in Text field for serialization
		MimeType: mimeType,
		Annotations: map[string]any{
			"encoding": "base64",
		},
	}
}

// AudioContent creates an audio content block
func AudioContent(data []byte, mimeType string) ContentBlock {
	return ContentBlock{
		Type:     ContentTypeAudio,
		Data:     data,
		MimeType: mimeType,
	}
}

// PDFContent creates a PDF content block
func PDFContent(data []byte) ContentBlock {
	return ContentBlock{
		Type:     ContentTypePDF,
		Data:     data,
		MimeType: "application/pdf",
	}
}

// ResourceContent creates a resource link content block
func ResourceContent(uri string, name string, description string) ContentBlock {
	return ContentBlock{
		Type:        ContentTypeResource,
		URI:         uri,
		Name:        name,
		Description: description,
	}
}

// HTMLContent creates an HTML content block for client-side rendering.
// The client should render the HTML in a sandboxed iframe or similar safe container.
// Follows the MCP spec: EmbeddedResource with mimeType "text/html" and audience ["user"].
func HTMLContent(html string) ContentBlock {
	return ContentBlock{
		Type:     ContentTypeHTML,
		Text:     html,
		MimeType: "text/html",
		Annotations: map[string]any{
			"audience": []string{"user"},
		},
	}
}

// WithAnnotation adds an annotation to a content block
func (c ContentBlock) WithAnnotation(key string, value any) ContentBlock {
	if c.Annotations == nil {
		c.Annotations = make(map[string]any)
	}
	c.Annotations[key] = value
	return c
}
