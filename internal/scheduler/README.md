# Scheduler Layer

This package provides scheduling and rate limiting for Market Terminal Gexbot.

## Components

### RateLimitTracker (`rate_limiter.go`)
- Tracks API rate limits from response headers
- Monitors 429 error frequency
- Adaptive light throttling (200ms minimum between same endpoint calls)
- Thread-safe rate limit tracking

### UnifiedAdaptiveScheduler (`scheduler.go`)
- Priority-based polling intervals:
  - High priority (in chart): 1-5 seconds
  - Medium priority (enabled): 6-15 seconds
  - Low priority: 16-30 seconds
- Intervals scale with ticker count
- Per-ticker refresh rate override support
- Per-endpoint throttling (1 second minimum)

### MasterTimerScheduler (`master_timer.go`)
- Single master timer checks all tickers every 100ms
- Batches ready tickers together
- Eliminates timer conflicts and drift
- More predictable polling timing

## Features

- **Priority-Based Intervals**: Faster polling for visible charts, slower for background collection
- **Rate Limit Awareness**: Respects API rate limits while maintaining consistent polling
- **Per-Endpoint Throttling**: Minimum 1 second between calls to same endpoint
- **Adaptive Throttling**: Automatically enables light throttling if 429 errors are frequent
- **Thread-Safe**: All operations are protected by locks

## Usage

```go
// Create scheduler
scheduler := scheduler.NewUnifiedAdaptiveScheduler(settings, false)

// Set enabled tickers
scheduler.SetEnabledTickers([]string{"SPX", "ES_SPX"})

// Check if ticker should be fetched
if scheduler.ShouldFetchTicker("SPX", openCharts) {
    // Fetch ticker
    scheduler.RecordFetch("SPX")
}

// Create master timer
masterTimer := scheduler.NewMasterTimerScheduler(
    scheduler,
    getOpenCharts,
    onTickersReady,
    debugPrint,
)

// Start master timer
masterTimer.Start()
```

## Memory Visibility

All scheduling operations use pure Go:
- **No C dependencies** - all memory allocations visible in Go profiler
- **Full visibility** - use `go tool pprof` to see exact allocations
- **Efficient data structures** - slices and maps with automatic cleanup
