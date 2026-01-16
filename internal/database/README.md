# Database Layer

This package provides database operations for Market Terminal Gexbot using pure Go SQLite driver.

## Components

### ConnectionPool (`connection.go`)
- Manages database connections with idle timeout
- Automatic cleanup of idle connections
- Thread-safe connection access
- Uses `modernc.org/sqlite` (pure Go) for full memory visibility

### SchemaManager (`schema.go`)
- Creates and manages database schema
- Handles dynamic column addition
- Creates indexes for performance

### DataWriter (`writer.go`)
- Writes market data to SQLite databases
- Batched writes for performance
- Priority-based flushing (active vs collection tickers)
- Compresses profile data (arrays) to BLOB

### DataLoader (`loader.go`)
- Loads data from SQLite databases
- Time range queries
- Decompresses profile data from BLOB
- Read-only connections for chart queries

## Memory Visibility

All database operations use `modernc.org/sqlite` (pure Go driver):
- **No C dependencies** - all memory allocations visible in Go profiler
- **Full visibility** - use `go tool pprof` to see exact allocations
- **No hidden memory** - every byte is accounted for

## Connection Management

- **Idle Timeout**: Connections idle for >10 seconds are closed
- **Cleanup Interval**: Cleanup runs every 5 seconds
- **Pool Size Limit**: Maximum 20 connections (configurable)
- **Thread-Safe**: All pool operations are protected by locks

## Database Schema

- **Table**: `ticker_data`
- **Primary Key**: `timestamp` (REAL)
- **Columns**: Dynamic columns for scalar fields (spot, zero_gamma, etc.)
- **BLOB**: `profiles_blob` stores compressed JSON of profile arrays
- **Indexes**: 
  - `idx_timestamp_desc` - For recent-entry queries
  - `idx_timestamp_asc` - For chronological queries

## Usage

```go
// Create writer
writer := database.NewDataWriter(settings, debugPrint)

// Write data
writer.WriteDataEntry("SPX", 1234567890.0, data, true)

// Flush pending writes
writer.FlushTicker("SPX")

// Create loader
loader := database.NewDataLoader(settings, debugPrint)

// Load data
data, err := loader.LoadFromFile("SPX", time.Now())

// Load time range
data, err := loader.LoadTimeRange("SPX", time.Now(), startTime, endTime)
```
