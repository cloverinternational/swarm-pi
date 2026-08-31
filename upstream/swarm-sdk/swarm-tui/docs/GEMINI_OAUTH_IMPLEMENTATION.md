# Gemini OAuth Provider Implementation Plan

## Overview

Based on analysis of the Gemini CLI source code, this document outlines how to implement a Gemini OAuth provider for SwarmOS.

## Key Components from Gemini CLI

### 1. OAuth Configuration

**OAuth Client Credentials** (from `oauth2.ts`):
```
Client ID: 681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com
Client Secret: set at runtime via `GEMINI_OAUTH_CLIENT_SECRET` (do not commit secrets)
```

**Scopes**:
```
- https://www.googleapis.com/auth/cloud-platform
- https://www.googleapis.com/auth/userinfo.email
- https://www.googleapis.com/auth/userinfo.profile
```

### 2. Authentication Flow

The Gemini CLI supports two OAuth flows:

#### A. Web-Based OAuth (Browser)
1. Start local HTTP server on random port
2. Generate auth URL with PKCE (code verifier/challenge)
3. Open browser to Google OAuth consent page
4. User authorizes, redirected to `http://localhost:{port}/oauth2callback`
5. Exchange authorization code for tokens
6. Cache tokens for future use

#### B. Device Code Flow (NO_BROWSER=true)
1. Generate auth URL with PKCE
2. Display URL to user
3. User enters authorization code manually
4. Exchange code for tokens

### 3. API Endpoint

**Base URL**: `https://cloudcode-pa.googleapis.com`
**API Version**: `v1internal`

**Key Endpoints**:
- `streamGenerateContent` - Streaming generation
- `generateContent` - Non-streaming generation
- `countTokens` - Token counting
- `loadCodeAssist` - Load user tier info
- `onboardUser` - User onboarding

### 4. Request Format

**Generate Content Request** (from `converter.ts`):
```typescript
interface CAGenerateContentRequest {
  model: string;               // e.g., "models/gemini-2.5-pro"
  project?: string;            // GCP project ID
  user_prompt_id?: string;     // Unique prompt ID
  request: {
    contents: Content[];       // Conversation history
    systemInstruction?: Content;
    tools?: Tool[];
    toolConfig?: ToolConfig;
    safetySettings?: SafetySetting[];
    generationConfig?: {
      temperature?: number;
      topP?: number;
      topK?: number;
      maxOutputTokens?: number;
      stopSequences?: string[];
      // ... more fields
    };
    session_id?: string;
  };
}
```

### 5. Response Format

**Generate Content Response**:
```typescript
interface CaGenerateContentResponse {
  response: {
    candidates: Candidate[];
    usageMetadata?: {
      promptTokenCount?: number;
      candidatesTokenCount?: number;
      totalTokenCount?: number;
    };
    modelVersion?: string;
  };
  traceId?: string;
}
```

### 6. Streaming (SSE)

- Uses Server-Sent Events (SSE) with `?alt=sse` query parameter
- Response type: `stream`
- Events are prefixed with `data: ` and separated by empty lines
- Each chunk is a JSON object matching the response format

### 7. Authentication Headers

When authenticated, requests include:
```
Authorization: Bearer {access_token}
Content-Type: application/json
```

## Implementation for SwarmOS

### New Files to Create

```
sdk/provider/gemini/
├── provider.go       # Main provider implementation
├── config.go         # Configuration struct
├── oauth.go          # OAuth flow implementation
├── translate.go      # Request/response translation
├── stream.go         # SSE streaming handler
├── token_storage.go  # Token caching
└── register.go       # Provider registration
```

### Provider Config

```go
type Config struct {
    // OAuth credentials (client secret must be provided at runtime)
    ClientID     string
    ClientSecret string
    
    // Optional: Use API key instead of OAuth
    APIKey string
    
    // Optional: GCP Project ID (required for some tiers)
    ProjectID string
    
    // Token storage path
    TokenPath string
    
    // Endpoint override (for testing)
    BaseURL string
    
    // HTTP client settings
    Timeout int
    Proxy   string
    
    // Observability
    Logger observability.Logger
    Tracer observability.Tracer
}
```

### OAuth Flow (Go Implementation)

```go
type OAuthManager struct {
    clientID     string
    clientSecret string
    tokenPath    string
    httpClient   *http.Client
}

func (m *OAuthManager) GetAccessToken() (string, error) {
    // 1. Check for cached token
    // 2. If expired, refresh using refresh_token
    // 3. If no token, start OAuth flow
    // 4. Return valid access_token
}

func (m *OAuthManager) StartWebFlow() error {
    // 1. Find available port
    // 2. Generate PKCE verifier/challenge
    // 3. Build auth URL
    // 4. Start callback server
    // 5. Open browser
    // 6. Wait for callback
    // 7. Exchange code for tokens
    // 8. Cache tokens
}
```

### Request Translation

```go
func translateRequest(req provider.ChatRequest) *GeminiRequest {
    return &GeminiRequest{
        Model:   mapModel(req.Model),
        Project: config.ProjectID,
        Request: &VertexRequest{
            Contents:          translateMessages(req.Messages),
            SystemInstruction: translateSystemPrompt(req.SystemPrompt),
            Tools:             translateTools(req.Tools),
            GenerationConfig: &GenerationConfig{
                Temperature:     req.Temperature,
                MaxOutputTokens: req.MaxTokens,
                StopSequences:   req.StopSequences,
            },
        },
    }
}
```

### Response Translation

```go
func translateResponse(resp *GeminiResponse) *provider.ChatResponse {
    candidate := resp.Response.Candidates[0]
    return &provider.ChatResponse{
        Message: &conversation.Message{
            Role:    conversation.RoleAssistant,
            Content: extractContent(candidate.Content.Parts),
        },
        FinishReason: mapFinishReason(candidate.FinishReason),
        Usage: &conversation.TokenUsage{
            InputTokens:  resp.Response.UsageMetadata.PromptTokenCount,
            OutputTokens: resp.Response.UsageMetadata.CandidatesTokenCount,
        },
    }
}
```

## Key Differences from OpenAI Provider

1. **Authentication**: OAuth 2.0 vs API Key
2. **Endpoint**: Google Cloud endpoint vs OpenAI API
3. **Request Format**: Nested structure with `model`, `project`, `request`
4. **Streaming**: SSE vs OpenAI's streaming format
5. **User Tiers**: Free tier onboarding flow

## Testing

1. **Unit Tests**: Mock HTTP responses
2. **Integration Tests**: Use test OAuth credentials
3. **E2E Tests**: Real OAuth flow with user interaction

## Security Considerations

1. Store tokens with restricted permissions (0600)
2. Use system keychain when available
3. Refresh tokens before they expire
4. Handle token revocation gracefully

## References

- Gemini CLI Source: `gemini-cli/packages/core/src/code_assist/`
- OAuth Flow: `oauth2.ts`
- API Server: `server.ts`
- Request/Response: `converter.ts`
- Types: `types.ts`
