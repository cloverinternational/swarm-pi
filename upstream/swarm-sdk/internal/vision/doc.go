// Package vision provides unified image handling utilities for all LLM providers.
//
// This package standardizes image encoding, validation, and metadata management
// across different providers (Anthropic, OpenAI, Google, etc.).
//
// # Image Data Format
//
// Images are represented using the ImageData struct:
//
//	type ImageData struct {
//	    Type      string // "base64" or "url"
//	    MediaType string // MIME type (e.g., "image/jpeg")
//	    Data      string // Base64-encoded data (for Type="base64")
//	    URL       string // Image URL (for Type="url")
//	}
//
// # Metadata Storage
//
// Images are stored in conversation.Message.Metadata["images"] as []map[string]string:
//
//	metadata["images"] = []map[string]string{
//	    {
//	        "type": "base64",
//	        "media_type": "image/png",
//	        "data": "iVBORw0KGgoAAAANS...",
//	    },
//	    {
//	        "type": "url",
//	        "url": "https://example.com/image.jpg",
//	    },
//	}
//
// # Supported Formats
//
// - JPEG (image/jpeg)
// - PNG (image/png)
// - GIF (image/gif)
// - WebP (image/webp)
//
// Maximum image size: 5MB (following Anthropic's limit)
//
// # Usage Example
//
//	// Create image from file
//	img, err := vision.NewImageFromFile("path/to/image.jpg")
//	if err != nil {
//	    return err
//	}
//
//	// Add to message metadata
//	if msg.Metadata == nil {
//	    msg.Metadata = make(map[string]any)
//	}
//	err = vision.AddImagesToMetadata(msg.Metadata, []*vision.ImageData{img})
//
//	// Extract from message metadata
//	images, err := vision.ExtractImagesFromMetadata(msg.Metadata)
package vision
