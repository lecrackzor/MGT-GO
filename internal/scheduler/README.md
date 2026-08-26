# Scheduler Layer

This package provides scheduling and rate limiting for Market Terminal Gexbot.

## Components

### RateLimitTracker (`rate_limiter.go`)
- Tracks API request history and rate limit info from `X-RateLimit-*` response headers
- Handles 429 errors: activates a global backoff honoring `Retry-After` (defaults to 60s)
- `GetMinimumInterval` enforces a rate-limit-aware floor on polling intervals
- Thread-safe

### UnifiedAdaptiveScheduler (`scheduler.go`)
- Priority-based polling intervals:
  - High priority (in chart): 1 second
  - Medium priority (enabled): 6-15 seconds
  - Low priority: 16-30 seconds
- Intervals scale with ticker count
- Per-ticker refresh rate override support (`ticker_configs.{ticker}.refresh_rate_ms`)

### PerTickerScheduler (`per_ticker_scheduler.go`)
- One goroutine per enabled ticker
- Anchored (drift-free) cadence: each fire time is computed from the previous
  scheduled fire time, so fetch duration does not stretch the polling interval
- Fetches are dispatched asynchronously; overlapping fetches for the same
  ticker are skipped rather than stacked
- Skips fetches while rate limited or when the market is closed
  (60s re-check interval when closed)

## Usage

```go
// Create scheduler
adaptive := scheduler.NewUnifiedAdaptiveScheduler(settings)
adaptive.SetEnabledTickers([]string{"SPX", "ES_SPX"})

// Create per-ticker scheduler
pts := scheduler.NewPerTickerScheduler(
    adaptive,
    getOpenCharts,
    onTickerReady, // called asynchronously when a ticker is due
    debugPrint,
    false, // allowAfterHours
)
pts.UpdateTickers([]string{"SPX", "ES_SPX"})
pts.Start()
```

## Memory Visibility

All scheduling operations use pure Go:
- **No C dependencies** - all memory allocations visible in Go profiler
- **Efficient data structures** - slices and maps with automatic cleanup
