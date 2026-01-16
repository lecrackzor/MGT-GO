# Data Collection Coordinator

This package coordinates data collection operations, tying together API calls, database writes, and scheduling.

## Components

### SmartQueryPlanner (`query_planner.go`)
- Builds optimized query plans for tickers
- Filters by subscription tiers
- Data hoarding mode (all tickers get all endpoints)

### PriorityWriteQueue (`write_queue.go`)
- Priority-based write queue (high/medium/low)
- Non-blocking writes
- Automatic batching

### DataCollectionCoordinator (`data_collection.go`)
- Coordinates API calls, database writes, and scheduling
- Aggregates API results by ticker
- Processes completed ticker data
- Updates scheduler state

## Features

- **Priority-Based Writes**: Visible charts get high priority writes
- **Non-Blocking**: All operations are non-blocking
- **Automatic Aggregation**: Combines multiple endpoint results per ticker
- **Thread-Safe**: All operations protected by locks

## Usage

```go
// Create coordinator
coordinator := coordinator.NewDataCollectionCoordinator(
    querySystem,
    dataWriter,
    scheduler,
    queryPlanner,
    writeQueue,
    getShuttingDown,
    getOpenCharts,
    debugPrint,
)

// Process a batch of tickers
coordinator.ProcessTickerBatch([]string{"SPX", "ES_SPX"})
```

## Memory Visibility

All coordination operations use pure Go:
- **No C dependencies** - all memory allocations visible in Go profiler
- **Full visibility** - use `go tool pprof` to see exact allocations
- **Efficient data structures** - maps and slices with automatic cleanup
