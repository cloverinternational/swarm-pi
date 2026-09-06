// Package anthropic provides citation parsing and translation utilities for the Anthropic provider.
package anthropic

import (
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// ParseCitations extracts citations from the API response and stores them in metadata.
// This function processes citation data returned by the Anthropic API and structures it
// for use by the application.
func ParseCitations(contentBlocks []ContentBlock) ([]Citation, error) {
	if len(contentBlocks) == 0 {
		return []Citation{}, nil
	}

	var allCitations []Citation

	// Iterate through content blocks to extract citations
	for _, block := range contentBlocks {
		if block.Citations != nil && len(block.Citations) > 0 {
			allCitations = append(allCitations, block.Citations...)
		}
	}

	return allCitations, nil
}

// ValidateCitationType validates that a citation type is supported.
func ValidateCitationType(citationType string) error {
	validTypes := map[string]bool{
		"char_location":              true,
		"page_location":              true,
		"content_block_location":     true,
		"web_search_result_location": true,
		"search_result_location":     true,
	}

	if !validTypes[citationType] {
		return sdkerr.Permanent(
			"anthropic.invalid_citation_type",
			fmt.Sprintf("unsupported citation type: %s", citationType),
		)
	}

	return nil
}

// ValidateCitationType validates multiple citation types at once.
func ValidateCitationTypes(types []string) error {
	if len(types) == 0 {
		return nil
	}

	// Validate each citation type
	for _, citationType := range types {
		if err := ValidateCitationType(citationType); err != nil {
			return err
		}
	}

	return nil
}

// ExtractCharacterLocationCitation extracts character-level citation details.
// This is useful for precise citation tracking in text documents.
func ExtractCharacterLocationCitation(citation *Citation) (map[string]any, error) {
	if citation == nil {
		return nil, sdkerr.Permanent(
			"anthropic.nil_citation",
			"citation object cannot be nil",
		)
	}

	if citation.Type != "char_location" {
		return nil, sdkerr.Permanent(
			"anthropic.wrong_citation_type",
			fmt.Sprintf("expected char_location, got %s", citation.Type),
		)
	}

	result := make(map[string]any)

	result["type"] = citation.Type
	result["cited_text"] = citation.CitedText

	if citation.StartCharIndex != nil {
		result["start_char_index"] = *citation.StartCharIndex
	}

	if citation.EndCharIndex != nil {
		result["end_char_index"] = *citation.EndCharIndex
	}

	if citation.DocumentIndex != nil {
		result["document_index"] = *citation.DocumentIndex
	}

	if citation.DocumentTitle != "" {
		result["document_title"] = citation.DocumentTitle
	}

	if citation.FileID != "" {
		result["file_id"] = citation.FileID
	}

	return result, nil
}

// ExtractPageLocationCitation extracts page-level citation details.
// This is useful for citing specific pages in PDF documents.
func ExtractPageLocationCitation(citation *Citation) (map[string]any, error) {
	if citation == nil {
		return nil, sdkerr.Permanent(
			"anthropic.nil_citation",
			"citation object cannot be nil",
		)
	}

	if citation.Type != "page_location" {
		return nil, sdkerr.Permanent(
			"anthropic.wrong_citation_type",
			fmt.Sprintf("expected page_location, got %s", citation.Type),
		)
	}

	result := make(map[string]any)

	result["type"] = citation.Type
	result["cited_text"] = citation.CitedText

	if citation.StartPageNumber != nil {
		result["start_page_number"] = *citation.StartPageNumber
	}

	if citation.EndPageNumber != nil {
		result["end_page_number"] = *citation.EndPageNumber
	}

	if citation.DocumentIndex != nil {
		result["document_index"] = *citation.DocumentIndex
	}

	if citation.DocumentTitle != "" {
		result["document_title"] = citation.DocumentTitle
	}

	if citation.FileID != "" {
		result["file_id"] = citation.FileID
	}

	return result, nil
}

// ExtractContentBlockLocationCitation extracts content block citation details.
// This is useful for citing specific blocks within structured documents.
func ExtractContentBlockLocationCitation(citation *Citation) (map[string]any, error) {
	if citation == nil {
		return nil, sdkerr.Permanent(
			"anthropic.nil_citation",
			"citation object cannot be nil",
		)
	}

	if citation.Type != "content_block_location" {
		return nil, sdkerr.Permanent(
			"anthropic.wrong_citation_type",
			fmt.Sprintf("expected content_block_location, got %s", citation.Type),
		)
	}

	result := make(map[string]any)

	result["type"] = citation.Type
	result["cited_text"] = citation.CitedText

	if citation.StartBlockIndex != nil {
		result["start_block_index"] = *citation.StartBlockIndex
	}

	if citation.EndBlockIndex != nil {
		result["end_block_index"] = *citation.EndBlockIndex
	}

	if citation.DocumentIndex != nil {
		result["document_index"] = *citation.DocumentIndex
	}

	if citation.DocumentTitle != "" {
		result["document_title"] = citation.DocumentTitle
	}

	if citation.FileID != "" {
		result["file_id"] = citation.FileID
	}

	return result, nil
}

// ExtractWebSearchResultLocationCitation extracts web search result citation details.
// This is useful for tracking citations from web search sources.
func ExtractWebSearchResultLocationCitation(citation *Citation) (map[string]any, error) {
	if citation == nil {
		return nil, sdkerr.Permanent(
			"anthropic.nil_citation",
			"citation object cannot be nil",
		)
	}

	if citation.Type != "web_search_result_location" {
		return nil, sdkerr.Permanent(
			"anthropic.wrong_citation_type",
			fmt.Sprintf("expected web_search_result_location, got %s", citation.Type),
		)
	}

	result := make(map[string]any)

	result["type"] = citation.Type
	result["cited_text"] = citation.CitedText
	result["url"] = citation.URL

	if citation.Title != "" {
		result["title"] = citation.Title
	}

	if citation.EncryptedIndex != "" {
		result["encrypted_index"] = citation.EncryptedIndex
	}

	return result, nil
}

// ExtractSearchResultLocationCitation extracts general search result citation details.
func ExtractSearchResultLocationCitation(citation *Citation) (map[string]any, error) {
	if citation == nil {
		return nil, sdkerr.Permanent(
			"anthropic.nil_citation",
			"citation object cannot be nil",
		)
	}

	if citation.Type != "search_result_location" {
		return nil, sdkerr.Permanent(
			"anthropic.wrong_citation_type",
			fmt.Sprintf("expected search_result_location, got %s", citation.Type),
		)
	}

	result := make(map[string]any)

	result["type"] = citation.Type
	result["cited_text"] = citation.CitedText

	if citation.SearchResultIndex != nil {
		result["search_result_index"] = *citation.SearchResultIndex
	}

	if citation.StartBlockIndex != nil {
		result["start_block_index"] = *citation.StartBlockIndex
	}

	if citation.EndBlockIndex != nil {
		result["end_block_index"] = *citation.EndBlockIndex
	}

	if citation.Title != "" {
		result["title"] = citation.Title
	}

	if citation.Source != "" {
		result["source"] = citation.Source
	}

	return result, nil
}

// ExtractCitationDetails extracts detailed information from a citation based on its type.
// This function uses explicit type checking to route to the appropriate extraction function.
func ExtractCitationDetails(citation *Citation) (map[string]any, error) {
	if citation == nil {
		return nil, sdkerr.Permanent(
			"anthropic.nil_citation",
			"citation object cannot be nil",
		)
	}

	// Route to appropriate extraction function based on citation type
	switch citation.Type {
	case "char_location":
		return ExtractCharacterLocationCitation(citation)
	case "page_location":
		return ExtractPageLocationCitation(citation)
	case "content_block_location":
		return ExtractContentBlockLocationCitation(citation)
	case "web_search_result_location":
		return ExtractWebSearchResultLocationCitation(citation)
	case "search_result_location":
		return ExtractSearchResultLocationCitation(citation)
	default:
		return nil, sdkerr.Permanent(
			"anthropic.unknown_citation_type",
			fmt.Sprintf("unknown citation type: %s", citation.Type),
		)
	}
}

// FormatCitationForDisplay formats a citation for user display.
// This creates a human-readable string representation of the citation.
func FormatCitationForDisplay(citation *Citation) (string, error) {
	if citation == nil {
		return "", sdkerr.Permanent(
			"anthropic.nil_citation",
			"citation object cannot be nil",
		)
	}

	switch citation.Type {
	case "char_location":
		if citation.StartCharIndex != nil && citation.EndCharIndex != nil {
			return fmt.Sprintf(
				"Document: %s (chars %d-%d)",
				citation.DocumentTitle,
				*citation.StartCharIndex,
				*citation.EndCharIndex,
			), nil
		}
		return fmt.Sprintf("Document: %s", citation.DocumentTitle), nil

	case "page_location":
		if citation.StartPageNumber != nil && citation.EndPageNumber != nil {
			if *citation.StartPageNumber == *citation.EndPageNumber {
				return fmt.Sprintf(
					"Document: %s (page %d)",
					citation.DocumentTitle,
					*citation.StartPageNumber,
				), nil
			}
			return fmt.Sprintf(
				"Document: %s (pages %d-%d)",
				citation.DocumentTitle,
				*citation.StartPageNumber,
				*citation.EndPageNumber,
			), nil
		}
		return fmt.Sprintf("Document: %s", citation.DocumentTitle), nil

	case "content_block_location":
		if citation.StartBlockIndex != nil && citation.EndBlockIndex != nil {
			if *citation.StartBlockIndex == *citation.EndBlockIndex {
				return fmt.Sprintf(
					"Document: %s (block %d)",
					citation.DocumentTitle,
					*citation.StartBlockIndex,
				), nil
			}
			return fmt.Sprintf(
				"Document: %s (blocks %d-%d)",
				citation.DocumentTitle,
				*citation.StartBlockIndex,
				*citation.EndBlockIndex,
			), nil
		}
		return fmt.Sprintf("Document: %s", citation.DocumentTitle), nil

	case "web_search_result_location":
		if citation.Title != "" {
			return fmt.Sprintf("Web: %s (%s)", citation.Title, citation.URL), nil
		}
		return fmt.Sprintf("Web: %s", citation.URL), nil

	case "search_result_location":
		if citation.Title != "" {
			return fmt.Sprintf("Search: %s", citation.Title), nil
		}
		if citation.Source != "" {
			return fmt.Sprintf("Search: %s", citation.Source), nil
		}
		return "Search result", nil

	default:
		return fmt.Sprintf("Citation (%s)", citation.Type), nil
	}
}
