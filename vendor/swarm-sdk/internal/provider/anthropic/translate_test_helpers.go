package anthropic

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TranslateRequest exposes translateRequest for testing.
// This is a public wrapper around the internal translateRequest function.
// Uses isOAuth=false, no cache for backward compatibility.
func TranslateRequest(req provider.ChatRequest) (*MessageRequest, error) {
	result, _, err := translateRequest(context.Background(), req, false, "", nil, nil)
	return result, err
}

// TranslateRequestForTest exposes translateRequest for testing.
// Deprecated: Use TranslateRequest instead.
func TranslateRequestForTest(req provider.ChatRequest) (*MessageRequest, error) {
	result, _, err := translateRequest(context.Background(), req, false, "", nil, nil)
	return result, err
}

// TranslateRequestWithJSON exposes translateRequest for testing with provider JSON.
// Uses isOAuth=false, no cache for backward compatibility.
func TranslateRequestWithJSON(req provider.ChatRequest) (*MessageRequest, map[string]any, error) {
	return translateRequest(context.Background(), req, false, "", nil, nil)
}

// TranslateRequestOAuth exposes translateRequest for OAuth testing.
// Sets isOAuth=true to test OAuth tool prefixing behavior.
func TranslateRequestOAuth(req provider.ChatRequest) (*MessageRequest, map[string]any, error) {
	return translateRequest(context.Background(), req, true, "", nil, nil)
}

// TranslateRequestWithCache exposes translateRequest for testing with a translation cache.
// This allows tests to verify incremental translation and cache behavior.
func TranslateRequestWithCache(req provider.ChatRequest, cache *TranslationCache) (*MessageRequest, error) {
	result, _, err := translateRequest(context.Background(), req, false, "", nil, cache)
	return result, err
}

// TranslateResponse exposes translateResponse for testing.
// This is a public wrapper around the internal translateResponse function.
// Uses isOAuth=false for backward compatibility.
func TranslateResponse(resp *MessageResponse) (*provider.ChatResponse, error) {
	return translateResponse(resp, false)
}

// TranslateResponseForTest exposes translateResponse for testing.
// Deprecated: Use TranslateResponse instead.
func TranslateResponseForTest(resp *MessageResponse) (*provider.ChatResponse, error) {
	return translateResponse(resp, false)
}

// TranslateResponseOAuth exposes translateResponse for OAuth testing.
// Sets isOAuth=true to test OAuth tool unprefixing behavior.
func TranslateResponseOAuth(resp *MessageResponse) (*provider.ChatResponse, error) {
	return translateResponse(resp, true)
}

// ProcessDocumentMetadataForTest exposes ProcessDocumentMetadata for testing.
func ProcessDocumentMetadataForTest(metadata map[string]any) ([]*ContentBlock, error) {
	return ProcessDocumentMetadata(metadata)
}
