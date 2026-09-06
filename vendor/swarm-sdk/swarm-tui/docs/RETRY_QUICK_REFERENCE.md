# Retry and Fallback - Quick Reference

## What Is This?

Automatic retry with exponential backoff and provider fallback for handling API rate limits and failures.

## How to Enable

Edit `~/.swarmos/config.json`:

```json
{
  "retryConfig": {
    "enabled": true,
    "maxRetries": 3,
    "showRetryNotifications": true
  }
}
```

## Common Configurations

### 1. Basic Retry (Recommended)

```json
{
  "retryConfig": {
    "enabled": true,
    "maxRetries": 3,
    "initialDelay": 1000,
    "maxDelay": 32000,
    "showRetryNotifications": true
  }
}
```

### 2. With Fallback Providers

```json
{
  "retryConfig": {
    "enabled": true,
    "maxRetries": 3,
    "enableFallback": true,
    "fallbackProviders": [
      {
        "provider": "openrouter",
        "model": "anthropic/claude-3.5-sonnet"
      },
      {
        "provider": "openai",
        "model": "gpt-4"
      }
    ],
    "showRetryNotifications": true
  }
}
```

### 3. Aggressive (High Rate Limit Environments)

```json
{
  "retryConfig": {
    "enabled": true,
    "maxRetries": 5,
    "initialDelay": 2000,
    "maxDelay": 60000,
    "enableFallback": true,
    "fallbackProviders": [
      {
        "provider": "openrouter",
        "model": "anthropic/claude-3.5-sonnet"
      }
    ]
  }
}
```

## Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | boolean | `false` | Enable retry logic |
| `maxRetries` | number | `3` | Maximum retry attempts |
| `initialDelay` | number | `1000` | Initial delay in ms |
| `maxDelay` | number | `32000` | Maximum delay in ms |
| `retryOn` | array | `["rate_limit", "timeout", "network"]` | Error types that trigger retry |
| `enableFallback` | boolean | `false` | Enable automatic fallback providers |
| `fallbackProviders` | array | `[]` | List of fallback provider configurations |
| `showRetryNotifications` | boolean | `false` | Show retry notifications in TUI |

## How It Works

### Retry Flow

```
Request fails → Check if retryable → Wait (exponential backoff) → Retry
                                   ↓
                            Max retries reached
                                   ↓
                            Try fallback provider
                                   ↓
                            Success or final error
```

### Backoff Delays

| Attempt | Delay |
|---------|-------|
| 1st retry | 1 second |
| 2nd retry | 2 seconds |
| 3rd retry | 4 seconds |
| 4th retry | 8 seconds |
| 5th retry | 16 seconds |

## Retryable Errors

Automatically retries on:
- ✅ Rate limits (429, "rate_limit")
- ✅ Timeouts (504, "timeout")
- ✅ Network errors ("connection refused")
- ✅ Service unavailable (503)
- ✅ Temporary failures

**Never retries:**
- ❌ Authentication errors (401, 403)
- ❌ Invalid requests (400)
- ❌ Context cancellation

## Example Scenario

### Without Retry
```
Request → Rate Limit (429) → ERROR ❌
User sees: "Error: rate limit exceeded"
```

### With Retry
```
Request → Rate Limit → Wait 1s → Rate Limit → Wait 2s → Success ✅
User sees: Request completes successfully
```

### With Fallback
```
Primary (Anthropic) → Rate Limit → Retries exhausted
  ↓
Fallback (OpenRouter) → Success ✅
User sees: Request completes (different provider)
```

## Disable Retry

To disable retry logic:

```json
{
  "retryConfig": {
    "enabled": false
  }
}
```

Or remove the `retryConfig` section entirely.

## Tips

1. **For Free Tiers:** Enable retry with low `maxRetries` (2-3)
2. **For Paid Tiers:** Use higher `maxRetries` (4-5) and fallback
3. **For Development:** Enable `showRetryNotifications` to see what's happening
4. **For Production:** Disable notifications, enable fallback

## Troubleshooting

### Retries Not Working?

1. Check `~/.swarmos/config.json` has `"enabled": true`
2. Restart the TUI after config changes
3. Check error logs for non-retryable errors

### Too Many Retries?

Lower `maxRetries`:
```json
{
  "retryConfig": {
    "enabled": true,
    "maxRetries": 2
  }
}
```

### Retries Too Slow?

Lower delays:
```json
{
  "retryConfig": {
    "enabled": true,
    "initialDelay": 500,
    "maxDelay": 8000
  }
}
```

## See Also

- Full documentation: `RETRY_AND_FALLBACK_IMPLEMENTATION.md`
- Example configuration: `config.example.json`
- Configuration reference: `headless/core/config.go`

---

**Quick Start:** Add `retryConfig` to your `~/.swarmos/config.json` and restart the TUI.
