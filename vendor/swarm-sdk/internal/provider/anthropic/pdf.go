package anthropic

import (
	"encoding/base64"
	"fmt"
	"os"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// PDFConstants define limits for PDF document processing
const (
	// MaxPDFSizeBytes is the maximum PDF file size (32 MB based on Anthropic API limits)
	MaxPDFSizeBytes int64 = 32 * 1024 * 1024

	// PDFMediaType is the standard media type for PDF documents
	PDFMediaType string = "application/pdf"

	// PDFSourceType is the content source type for base64 PDFs
	PDFSourceType string = "base64"
)

// EncodePDFToBase64 reads a PDF file from the filesystem and returns the base64 encoded data.
// The file is read entirely into memory, so ensure the file size is within reasonable limits.
func EncodePDFToBase64(filePath string) (string, error) {
	// Validate file path is not empty
	if filePath == "" {
		return "", sdkerr.Permanent(
			"anthropic.pdf.empty_path",
			"PDF file path cannot be empty",
		)
	}

	// Read file from filesystem
	pdfData, err := os.ReadFile(filePath)
	if err != nil {
		return "", sdkerr.Permanent(
			"anthropic.pdf.read_failed",
			fmt.Sprintf("failed to read PDF file: %v", err),
		)
	}

	// Validate PDF size
	if err := ValidatePDFSize(pdfData); err != nil {
		return "", err
	}

	// Encode to base64
	encodedData := base64.StdEncoding.EncodeToString(pdfData)

	return encodedData, nil
}

// EncodePDFDataToBase64 encodes raw PDF bytes to base64 string.
// This is useful when PDF data is already in memory (e.g., from network or generated).
func EncodePDFDataToBase64(pdfData []byte) (string, error) {
	// Validate PDF data is not empty
	if len(pdfData) == 0 {
		return "", sdkerr.Permanent(
			"anthropic.pdf.empty_data",
			"PDF data cannot be empty",
		)
	}

	// Validate PDF size
	if err := ValidatePDFSize(pdfData); err != nil {
		return "", err
	}

	// Encode to base64
	encodedData := base64.StdEncoding.EncodeToString(pdfData)

	return encodedData, nil
}

// ValidatePDFSize checks if the PDF data is within acceptable size limits.
// Returns a Permanent error if the PDF is too large.
func ValidatePDFSize(pdfData []byte) error {
	size := int64(len(pdfData))

	if size > MaxPDFSizeBytes {
		return sdkerr.Permanent(
			"anthropic.pdf.size_exceeded",
			fmt.Sprintf("PDF size (%d bytes) exceeds maximum allowed size (%d bytes)",
				size, MaxPDFSizeBytes),
		)
	}

	return nil
}

// IsPDFValid validates that the data looks like a PDF by checking the magic bytes.
// PDF files always start with "%PDF-" sequence.
func IsPDFValid(pdfData []byte) bool {
	if len(pdfData) < 5 {
		return false
	}

	// Check for PDF magic bytes: "%PDF-"
	return pdfData[0] == '%' &&
		pdfData[1] == 'P' &&
		pdfData[2] == 'D' &&
		pdfData[3] == 'F' &&
		pdfData[4] == '-'
}

// CreateDocumentContentBlock creates a ContentBlock for a PDF document.
// The pdfData parameter should be base64 encoded PDF data.
// This is used internally by the translate functions to create document blocks.
func CreateDocumentContentBlock(base64PDFData string) (*ContentBlock, error) {
	// Validate base64 data is not empty
	if base64PDFData == "" {
		return nil, sdkerr.Permanent(
			"anthropic.pdf.empty_base64",
			"base64 encoded PDF data cannot be empty",
		)
	}

	// Create content block with PDF document source
	mediaType := PDFMediaType
	contentBlock := &ContentBlock{
		Type: "document",
		Source: &ContentSource{
			Type:      PDFSourceType,
			MediaType: &mediaType,
			Data:      base64PDFData,
		},
	}

	return contentBlock, nil
}

// DocumentMetadata represents metadata for a PDF document in a message.
// This structure is used when passing documents through message metadata.
type DocumentMetadata struct {
	// Type is the document source type ("base64" or "url")
	Type string `json:"type"`

	// MediaType is the MIME type of the document ("application/pdf")
	MediaType string `json:"media_type"`

	// Data is the base64 encoded document data (for base64 type)
	Data string `json:"data,omitempty"`

	// URL is the document URL (for url type)
	URL string `json:"url,omitempty"`
}

// CreateDocumentFromMetadata creates a ContentBlock from document metadata.
// This helper function validates and converts metadata into a proper ContentBlock.
func CreateDocumentFromMetadata(docMetadata DocumentMetadata) (*ContentBlock, error) {
	// Validate document type
	if docMetadata.Type == "" {
		return nil, sdkerr.Permanent(
			"anthropic.pdf.missing_type",
			"document type is required (must be 'base64' or 'url')",
		)
	}

	// Validate media type
	if docMetadata.MediaType != PDFMediaType {
		return nil, sdkerr.Permanent(
			"anthropic.pdf.unsupported_media_type",
			fmt.Sprintf("unsupported media type: %s (only application/pdf is currently supported)",
				docMetadata.MediaType),
		)
	}

	// Handle base64 documents
	if docMetadata.Type == PDFSourceType {
		if docMetadata.Data == "" {
			return nil, sdkerr.Permanent(
				"anthropic.pdf.missing_data",
				"base64 document data is required",
			)
		}

		return CreateDocumentContentBlock(docMetadata.Data)
	}

	// Handle URL documents (if supported in future)
	if docMetadata.Type == "url" {
		if docMetadata.URL == "" {
			return nil, sdkerr.Permanent(
				"anthropic.pdf.missing_url",
				"URL is required for url-type documents",
			)
		}

		mediaType := PDFMediaType
		contentBlock := &ContentBlock{
			Type: "document",
			Source: &ContentSource{
				Type:      "url",
				MediaType: &mediaType,
				URL:       docMetadata.URL,
			},
		}

		return contentBlock, nil
	}

	// Unsupported type
	return nil, sdkerr.Permanent(
		"anthropic.pdf.unsupported_type",
		fmt.Sprintf("unsupported document type: %s (must be 'base64' or 'url')",
			docMetadata.Type),
	)
}
