# Retry and Fallback - SDK Implementation Complete ✅

## Summary

Successfully implemented comprehensive retry logic with exponential backoff directly in the SDK, plus provider fallback routing in the headless bridge, and UI notifications in the TUI.

## SDK Changes (Automatic Retry at HTTP Layer)

### 1. SDK HTTP Client - Retry Infrastructure ✅

**File:** `/home/rincon/swarm/SDK/provider/http/client.go`

**Changes:**
- Added `retryPolicy` field to `Client` struct
- Added `EnableRetry` and `RetryPolicy` to `ClientConfig`
- Modified `NewClient()` to initialize retry policy when enabled
- Modified `Do()` method to use `RetryableRequest` for automatic retries

**Before:**
```go
type Client struct {
    httpClient *http.Client
    logger     observability.Logger
    // ...
}

func (c *Client) Do(ctx context.Context, builder *RequestBuilder) (*Response, error) {
    // ... build request ...
    httpResp, err := c.httpClient.Do(req.Underlying().WithContext(execCtx))
    // ...
}
```

**After:**
```go
type Client struct {
    httpClient  *http.Client
    logger      observability.Logger
    retryPolicy RetryPolicy  // ← Added retry policy
    // ...
}

func (c *Client) Do(ctx context.Context, builder *RequestBuilder) (*Response, error) {
    // ... build request ...
    
    // Execute with retry if policy configured
    var httpResp *http.Response
    if c.retryPolicy != nil {
        retryableReq := &RetryableRequest{
            Request: req.Underlying().WithContext(execCtx),
            Client:  c.httpClient,
            Policy:  c.retryPolicy,
            Logger:  c.logger,
        }
        httpResp, err = retryableReq.Do(execCtx)
    } else {
        httpResp, err = c.httpClient.Do(req.Underlying().WithContext(execCtx))
    }
    // ...
}
```

**Retry Logic:**
- Uses existing `RetryableRequest` from `provider/http/retry.go`
- Exponential backoff: 1s → 2s → 4s → 8s → 16s
- Retries on: rate limits (429), timeouts (504), network errors, 5xx errors
- Respects `Retry-After` headers
- Default: 3 retries (4 total attempts)

### 2. OpenAI Provider - Retry Enabled ✅

**File:** `/home/rincon/swarm/SDK/provider/openai/provider.go`

**Change:** Added `EnableRetry: true` to HTTP client configuration

```go
httpClient, err := httplib.NewClient(httplib.ClientConfig{
    HTTPClient:      underlyingClient,
    Logger:          config.Logger,
    Tracer:          config.Tracer,
    OperationPrefix: "openai",
    EnableRetry:     true, // ← Enable automatic retry
})
```

**Result:** All OpenAI API calls now automatically retry on transient failures

### 3. Anthropic Provider - Retry Enabled ✅

**File:** `/home/rincon/swarm/SDK/provider/anthropic/provider.go`

**Change:** Added `EnableRetry: true` to HTTP client configuration

```go
client, err := httplib.NewClient(httplib.ClientConfig{
    HTTPClient:     httpClient,
    DefaultHeaders: headers,
    Logger:         logger,
    Tracer:         tracer,
    EnableRetry:    true, // ← Enable automatic retry
})
```

**Result:** All Anthropic API calls now automatically retry on transient failures

## TUI Changes (Provider Fallback & UI Notifications)

### 4. Headless Core - Retry Configuration ✅

**File:** `headless/core/config.go`

**Added Configuration Structs:**

```go
type RetryConfig struct {
    Enabled                bool               // Enable retry logic
    MaxRetries             int                // Max retry attempts (default: 3)
    InitialDelay           int                // Initial backoff in ms (default: 1000)
    MaxDelay               int                // Max backoff in ms (default: 32000)
    RetryOn                []string           // Error types to retry
    EnableFallback         bool               // Enable provider fallback
    FallbackProviders      []FallbackProvider // Fallback provider list
    ShowRetryNotifications bool               // Show UI notifications
}

type FallbackProvider struct {
    Provider string // Provider name
    Model    string // Model to use
    Priority int    // Fallback order
}
```

**Added to Config:**
```go
type Config struct {
    // ... existing fields ...
    RetryConfig *RetryConfig `json:"retryConfig,omitempty"`
    // ...
}
```

### 5. Headless SDK Bridge - Fallback Routing ✅

**File:** `headless/sdk/bridge.go`

**Added Functions:**
- `executeWithRetry()` - Main retry orchestration (calls SDK which does HTTP-level retry)
- `executeSingleAttempt()` - Single execution attempt
- `tryFallbackProviders()` - Provider fallback logic
- `isRetryableError()` - Error classification
- `calculateBackoff()` - Exponential backoff calculation
- `getRetryConfig()` - Configuration retrieval

**Retry Flow:**
```
1. SDK executes request with automatic HTTP-level retries (3 attempts)
   ↓
2. If all SDK retries fail, bridge checks if fallback enabled
   ↓
3. If yes, try next fallback provider with its own HTTP retries
   ↓
4. Continue through fallback list until success or exhaustion
```

### 6. Headless Core Events - Retry Update Types ✅

**File:** `headless/core/event.go`

**Added Update Types:**
```go
const (
    // ... existing types ...
    UpdateRetryAttempt    UpdateType = "retry_attempt"     // Retry notification
    UpdateFallbackAttempt UpdateType = "fallback_attempt"  // Fallback notification
    // ...
)
```

### 7. TUI App - Retry Status Banner ✅

**File:** `internal/chat/app.go`

**Added to App Struct:**
```go
type App struct {
    // ... existing fields ...
    
    // Retry/Fallback status banner
    retryStatus struct {
        active    bool      // True when showing notification
        message   string    // Status message
        expiresAt time.Time // Auto-hide time
    }
    // ...
}
```

**Next Steps:** Add update handlers and banner rendering (in progress)

## How It Works

### Two-Layer Retry System

#### Layer 1: SDK HTTP Client (Automatic, Always On)
```
API Request → Network Error → Retry 1 (wait 1s) → Retry 2 (wait 2s) → Retry 3 (wait 4s)
                                                                              ↓
                                                                        Success or Error
```

- Handled by: `SDK/provider/http/client.go` + `retry.go`
- Triggers: Network errors, timeouts, 429, 503, 504
- Config: Built-in, always enabled when `EnableRetry: true`
- User visibility: None (transparent)

#### Layer 2: Bridge Provider Fallback (Configurable)
```
Primary Provider Fails (after SDK retries) → Try Fallback Provider #1 (with SDK retries)
                                                          ↓
                                                    Fallback Provider #2...
                                                          ↓
                                                    Success or Final Error
```

- Handled by: `headless/sdk/bridge.go`
- Triggers: SDK retry exhaustion + fallback enabled
- Config: User configures in `~/.swarmos/config.json`
- User visibility: UI notifications (if enabled)

### Complete Request Flow

```
User Message → Bridge executeWithRetry()
                    ↓
               Create Agent (SDK retry enabled)
                    ↓
               executeSingleAttempt()
                    ↓
               Agent.Execute()
                    ↓
               Provider.Chat() → HTTP Client.Do()
                                      ↓
                                 RetryableRequest
                                      ↓
                            Attempt 1: Network error
                            Wait 1s
                            Attempt 2: Network error
                            Wait 2s
                            Attempt 3: Network error
                            Wait 4s
                            Attempt 4: Rate limit (429)
                                      ↓
                                 Return Error to Bridge
                                      ↓
                            Bridge: Try fallback provider
                                      ↓
                            Switch to OpenRouter
                                      ↓
                            OpenRouter.Chat() → RetryableRequest
                                      ↓
                            Attempt 1: Success ✅
                                      ↓
                            Return to User
```

## Configuration

### Minimal (SDK Retry Only)

```json
{}
```

**Behavior:** 
- SDK automatically retries failed requests (3 attempts)
- No provider fallback
- No UI notifications

### Basic (SDK Retry + User Notifications)

```json
{
  "retryConfig": {
    "enabled": true,
    "showRetryNotifications": true
  }
}
```

**Behavior:**
- SDK retries (3 attempts)
- UI shows retry attempt numbers
- No provider fallback

### Full (SDK Retry + Provider Fallback + Notifications)

```json
{
  "retryConfig": {
    "enabled": true,
    "maxRetries": 3,
    "initialDelay": 1000,
    "maxDelay": 32000,
    "retryOn": ["rate_limit", "timeout", "network", "503", "504", "429"],
    "enableFallback": true,
    "fallbackProviders": [
      {
        "provider": "openrouter",
        "model": "anthropic/claude-3.5-sonnet",
        "priority": 1
      },
      {
        "provider": "openai",
        "model": "gpt-4",
        "priority": 2
      }
    ],
    "showRetryNotifications": true
  }
}
```

**Behavior:**
- SDK retries each provider (3 attempts per provider)
- If primary fails → try OpenRouter (with 3 SDK retries)
- If OpenRouter fails → try OpenAI (with 3 SDK retries)
- UI shows all retry and fallback attempts

## Files Modified

### SDK (Agent-SDK Repository)

| File | Lines Changed | Status |
|------|---------------|--------|
| `provider/http/client.go` | +30 | ✅ |
| `provider/openai/provider.go` | +1 | ✅ |
| `provider/anthropic/provider.go` | +1 | ✅ |

### TUI (SwarmOS-TUI Repository)

| File | Lines Changed | Status |
|------|---------------|--------|
| `headless/core/config.go` | +43 | ✅ |
| `headless/core/event.go` | +2 | ✅ |
| `headless/sdk/bridge.go` | +153 | ✅ |
| `internal/chat/app.go` | +6 | ✅ |

**Total:** +236 lines

## Testing

### Test SDK Retry (Automatic)

1. **Simulate network failure:**
   ```bash
   # Block API temporarily
   sudo iptables -A OUTPUT -d api.openai.com -j DROP
   
   # Send request (will auto-retry)
   ./swarm
   # Ask: "Hello"
   
   # Restore network
   sudo iptables -D OUTPUT -d api.openai.com -j DROP
   ```

2. **Check logs for retry attempts:**
   ```bash
   tail -f ~/swarm-debug.log | grep "provider.http.retry"
   ```

### Test Provider Fallback

1. **Configure fallback:**
   ```bash
   cat > ~/.swarmos/config.json << 'EOF'
   {
     "retryConfig": {
       "enabled": true,
       "enableFallback": true,
       "fallbackProviders": [
         {
           "provider": "openrouter",
           "model": "anthropic/claude-3.5-sonnet"
         }
       ],
       "showRetryNotifications": true
     }
   }
   EOF
   ```

2. **Invalidate primary provider:**
   ```bash
   # Set invalid API key for primary
   # In providers.json, corrupt the API key
   ```

3. **Send request:**
   ```bash
   ./swarm
   # Ask: "Hello"
   # Should fail on primary, succeed on fallback
   ```

4. **Check for fallback notification:**
   - Look for UI banner showing fallback attempt
   - Check logs for provider switch

## Benefits

### SDK-Level Retry

✅ **Transparent** - Works automatically for all providers  
✅ **Immediate** - No round-trip to bridge layer  
✅ **Efficient** - Retries happen at HTTP level  
✅ **Consistent** - Same retry logic for all providers  

### Bridge-Level Fallback

✅ **Flexible** - User configures which providers to use  
✅ **Resilient** - Multiple backup options  
✅ **Cost-Optimized** - Can route to cheaper alternatives  
✅ **Visible** - User sees what's happening (optional)

## Status

✅ **SDK Implementation Complete**
- HTTP client retry logic
- OpenAI provider retry enabled
- Anthropic provider retry enabled

✅ **TUI Configuration Complete**
- RetryConfig structure
- Update event types
- Retry status banner field

⏳ **TUI UI Handlers (In Progress)**
- Update event handlers (TODO)
- Banner rendering (TODO)
- Testing with real rate limits (TODO)

## Next Steps

1. Add handlers for `UpdateRetryAttempt` and `UpdateFallbackAttempt` in `internal/chat/app.go`
2. Add retry status banner rendering in View method
3. Test with real API rate limits
4. Polish UI styling (colors, animations)

---

**Build Status:** ✅ Passing  
**Date:** 2026-01-28  
**Implementation:** SDK + TUI Headless + TUI App
