# Database Layer

This package provides database operations for Market Terminal Gexbot using the
pure Go SQLite driver.

## Where ladder data lives

Each ticker/date pair has two files:

- `<TICKER>.db` — the chart table `ticker_data`, one row per poll, holding
  scalar columns (spot, zero_gamma, major levels, and so on). Timestamp is the
  primary key.
- `<TICKER>.profiles.db` — the sibling profile file. Its `endpoint_profile`
  table holds one gzip-compressed, delta-chained ladder per endpoint per sample
  (`timestamp`, `endpoint`, `payload`). This is the current home for the
  option-profile ladders (classic/state/greek chains).

A `profiles_blob` column used to sit on `ticker_data` and held the ladders
before the sibling file existed. New databases do not create it. Databases
written by older builds still carry it, and `DataLoader.LoadProfile` falls back
to decoding it when no sibling file is present — so that history keeps loading.
The column holds real data for those legacy days, not dead bytes.

## Historical major levels

`major_call_gamma` and `major_put_gamma` are not part of a chain snapshot. They
come from the Ninja historical majors feed and are a different calculation from
`major_pos_vol` / `major_neg_vol` (largest gamma snapshot) and from
`major_pos_oi` / `major_neg_oi` (largest OI). The feed serves a whole session
per request and then prunes it, so a level that is not written while the
session is still served is lost.

Three paths write them, all through `DataWriter.FillMajorLevels`:

- a chart load stores the levels it fetched for that day,
- a live ticker is topped up once a minute whether a chart is open or not,
- the intraday and archive passes fill the bars they recover.

`FillMajorLevels` only fills `NULL` or `0` columns on timestamps that already
exist: it never inserts a row and never replaces a known level. That makes it
idempotent and the gap fill all at once. `LoadBarsMissingMajorLevels` returns
the bars that still have no level, so a re-run after a recovery sweep picks up
the rows the sweep created and does nothing else. A day that has no such column
at all — an archive-recovered day, where the recovery path only creates columns
the archive carries — reports every row as missing, and the fill adds the
columns before writing.

`cmd/majors-backfill` runs the same fill over past days, which is the only way
to recover a session collected before the live fill existed. The feed keeps
roughly a 96 hour rolling window and prunes from the front, so older sessions
come back partial or empty; there is no archive route for these levels.

## Settled days

The recovery ledger (`eod-state.json`, next to the day folders) also records the
outcome of each ticker-day's level fill, under the key `date|ticker`:

| Reason | Meaning |
|---|---|
| `complete` | every existing timestamp has a level |
| `partial` | the feed served part of the session; the rest is pre-market rows it never covers |
| `unavailable` | the feed no longer serves the session at all |

Any of the three settles the day. The feed only shrinks, so a second request can
return the same coverage or less — never more. Only a failed request leaves the
day unsettled, so it is retried on the next sweep. Empty rows that remain after
a `partial` settlement are pre-market (08:30–09:30), which the feed does not
carry.

`Manager.SweepMajors` walks the past few sessions, skips weekends, never touches
the current session, and skips any ticker-day already settled. It runs at
startup, from the nightly archive roundup, and as `cmd/majors-backfill`. A dry
run passes `SetMajorsDryRun(true)` so it reports without settling anything.

## Components

### ConnectionPool (`connection.go`)
- Manages database connections with idle timeout
- Automatic cleanup of idle connections
- Thread-safe connection access
- Uses `modernc.org/sqlite` (pure Go) for full memory visibility

### SchemaManager (`schema.go`)
- Creates `ticker_data` with `timestamp` as the primary key
- Adds scalar columns on demand as new fields appear
- Drops the redundant duplicate timestamp indexes from older files

### DataWriter (`writer.go`)
- Writes chart rows to `ticker_data` and ladders to the sibling `.profiles.db`
- Batched writes for performance
- Priority-based flushing (active vs collection tickers)
- Gzip-compresses and delta-chains each endpoint's ladder

### DataLoader (`loader.go`)
- Loads chart series from `ticker_data`
- Loads ladders from the sibling `.profiles.db`, with a `profiles_blob`
  fallback for legacy databases
- Read-only connections for chart queries

## Memory Visibility

All database operations use `modernc.org/sqlite` (pure Go driver):
- **No C dependencies** - all memory allocations visible in Go profiler
- **No hidden memory** - every byte is accounted for

## Connection Management

- **Idle Timeout**: Connections idle for 3 minutes are closed
- **Cleanup Interval**: Cleanup runs every 5 seconds
- **Pool Size Limit**: Maximum 20 connections (configurable)
- **Thread-Safe**: All pool operations are protected by locks

## Database Schema

- **Chart table**: `ticker_data`
- **Primary Key**: `timestamp` (REAL)
- **Columns**: Dynamic columns for scalar fields (spot, zero_gamma, etc.)
- **Profile table**: `endpoint_profile` in the sibling `.profiles.db`
  (`timestamp`, `endpoint`, `payload`), with the ladder body gzip-compressed.
- **Legacy**: `profiles_blob` on `ticker_data`, present only in databases
  written before the sibling file; read-only via the loader fallback.

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

// Load the latest row for the ticker table
data, err := loader.LoadTickerData("SPX", time.Now())

// Load chart series
chart, err := loader.LoadChartData("SPX", time.Now(), 0, nil)
```
