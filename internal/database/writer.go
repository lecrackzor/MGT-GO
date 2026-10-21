package database

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

// DataWriter handles writing market data to SQLite databases
type DataWriter struct {
	pool             *ConnectionPool
	mu               sync.RWMutex
	pendingWrites    map[string][]*PendingWrite // ticker -> []PendingWrite
	firstPendingTime map[string]time.Time       // When first pending write was added (for flush timing)
	lastFlushTime    map[string]time.Time       // When last flush occurred
	settings         *config.Settings
	debugPrint       func(string, string)
	debugOn          func(string) bool

	schemaMu     sync.Mutex
	knownColumns map[string]map[string]bool

	// One flush at a time per ticker. The value is *sync.Mutex.
	tickerFlush sync.Map

	// failNextProfileFlush makes the next profile commit return an error.
	// Chart rows for that flush stay committed. Tests use it to prove the
	// ladder is retried on the following flush.
	failNextProfileFlush atomic.Bool

	// Background flusher
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// SetSettings replaces the settings used when deciding which ladders to keep.
func (dw *DataWriter) SetSettings(settings *config.Settings) {
	dw.mu.Lock()
	dw.settings = settings
	dw.mu.Unlock()
}

func (dw *DataWriter) currentSettings() *config.Settings {
	dw.mu.RLock()
	defer dw.mu.RUnlock()
	return dw.settings
}

// SetDebugGate skips formatting log lines when the category is disabled.
func (dw *DataWriter) SetDebugGate(fn func(string) bool) {
	dw.debugOn = fn
}

func (dw *DataWriter) logf(category, format string, args ...interface{}) {
	if dw.debugOn != nil && !dw.debugOn(category) {
		return
	}
	if dw.debugPrint == nil {
		return
	}
	dw.debugPrint(fmt.Sprintf(format, args...), category)
}

// PendingWrite represents a pending database write
type PendingWrite struct {
	Ticker    string
	Timestamp float64
	Scalars   map[string]interface{}
	Profiles  map[string]interface{}
	Date      time.Time
}

// NewDataWriter creates a new data writer
func NewDataWriter(settings *config.Settings, debugPrint func(string, string)) *DataWriter {
	pool := NewConnectionPool(
		config.DBConnectionPoolMaxSize,
		time.Duration(config.DBConnectionIdleTimeoutSec)*time.Second,
		time.Duration(config.SQLiteConnectionCleanupIntervalSeconds)*time.Second,
	)

	dw := &DataWriter{
		pool:             pool,
		pendingWrites:    make(map[string][]*PendingWrite),
		firstPendingTime: make(map[string]time.Time),
		lastFlushTime:    make(map[string]time.Time),
		settings:         settings,
		debugPrint:       debugPrint,
		stopChan:         make(chan struct{}),
	}

	// Start background flusher
	dw.startBackgroundFlusher()

	return dw
}

// startBackgroundFlusher starts a goroutine that periodically flushes pending writes
// and checkpoints WAL files once a minute (instead of after every flush, which
// caused lock contention with readers)
func (dw *DataWriter) startBackgroundFlusher() {
	dw.wg.Add(1)
	go func() {
		defer dw.wg.Done()

		// Check every second for pending writes that need flushing
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		const checkpointEverySec = 60
		secondsSinceCheckpoint := 0

		for {
			select {
			case <-dw.stopChan:
				dw.debugPrint("Background flusher stopping", "writer")
				return
			case <-ticker.C:
				dw.checkAndFlushPending()

				secondsSinceCheckpoint++
				if secondsSinceCheckpoint >= checkpointEverySec {
					secondsSinceCheckpoint = 0
					dw.pool.CheckpointAll()
				}
			}
		}
	}()
	dw.debugPrint("Background flusher started", "writer")
}

// checkAndFlushPending checks all tickers for pending writes that should be flushed
func (dw *DataWriter) checkAndFlushPending() {
	dw.mu.RLock()
	// Get list of tickers with pending writes
	tickersToCheck := make([]string, 0)
	for ticker, pending := range dw.pendingWrites {
		if len(pending) > 0 {
			tickersToCheck = append(tickersToCheck, ticker)
		}
	}
	dw.mu.RUnlock()

	// Check each ticker
	for _, ticker := range tickersToCheck {
		// Use shouldFlush with isActive=false (background flusher treats all as collection)
		if dw.shouldFlush(ticker, false) {
			dw.logf("writer", "Background flusher: triggering flush for %s", ticker)
			if err := dw.FlushTicker(ticker); err != nil {
				dw.logf("error", "Background flusher: flush failed for %s: %v", ticker, err)
			}
		}
	}
}

// WriteDataEntry writes a single data entry (queues for batch write)
func (dw *DataWriter) WriteDataEntry(ticker string, timestamp float64, data map[string]interface{}, isActive bool) error {
	dw.logf("writer", "WriteDataEntry: Called for %s (timestamp: %.0f, fields: %d, active: %v)",
		ticker, timestamp, len(data), isActive)

	dw.mu.Lock()
	// Note: We unlock before calling shouldFlush() to avoid deadlock
	// shouldFlush() needs its own read lock, and we can't hold a write lock while acquiring a read lock

	scalars, profiles := splitCollectedFields(data)
	scalarCount := len(scalars)
	profileCount := len(profiles)

	dw.logf("writer", "WriteDataEntry: Extracted %d scalars, %d profiles for %s",
		scalarCount, profileCount, ticker)

	// Determine date from API timestamp
	// Convert to Eastern Time first, then use market date logic to handle weekends and rollover
	timestampTime := time.Unix(int64(timestamp), 0).UTC()
	timestampET := timestampTime.In(utils.GetMarketTimezone())

	// Use the API timestamp's date with market date logic (handles weekends and 8:30 AM rollover)
	// This ensures data is written to the directory corresponding to when the data was actually collected
	entryDate := utils.GetMarketDateForDate(timestampET)

	// Extract just the date part (set to midnight) to avoid time component issues
	dateOnly := time.Date(entryDate.Year(), entryDate.Month(), entryDate.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())
	entryDate = dateOnly

	// Debug logging for date calculation
	dw.logf("writer", "WriteDataEntry: Timestamp %d (UTC: %s, ET: %s) -> GetMarketDateForDate() returned: %s -> Final entryDate: %s",
		int64(timestamp),
		timestampTime.Format("2006-01-02 15:04:05 MST"),
		timestampET.Format("2006-01-02 15:04:05 MST"),
		entryDate.Format("2006-01-02 15:04:05 MST"),
		entryDate.Format("2006-01-02 15:04:05 MST"))

	// Add to pending writes
	if dw.pendingWrites[ticker] == nil {
		dw.pendingWrites[ticker] = make([]*PendingWrite, 0)
		dw.logf("writer", "WriteDataEntry: First write for %s, initializing pending writes", ticker)
	}

	queued := &PendingWrite{
		Ticker:    ticker,
		Timestamp: timestamp,
		Scalars:   scalars,
		Profiles:  profiles,
		Date:      entryDate,
	}
	dw.pendingWrites[ticker] = append(dw.pendingWrites[ticker], queued)

	pendingCount := len(dw.pendingWrites[ticker])

	// CRITICAL: Check if this is the first write ever for this ticker
	// If it's the first write, we need to force flush to create the database file
	_, hasFlushHistory := dw.lastFlushTime[ticker]

	// Track when first pending write was added (for flush interval calculation)
	// Only set if this is the first pending write in the current batch
	if pendingCount == 1 {
		dw.firstPendingTime[ticker] = time.Now()
	}

	dw.logf("writer", "WriteDataEntry: Added write to pending queue for %s (total pending: %d, first ever: %v)",
		ticker, pendingCount, !hasFlushHistory)

	// CRITICAL: Unlock before calling shouldFlush() to avoid deadlock
	// shouldFlush() needs to acquire a read lock, and we're currently holding a write lock
	dw.mu.Unlock()

	// Check if we should flush
	// For first write ever, force flush immediately to create database file
	// For subsequent writes, use normal shouldFlush() logic
	var shouldFlush bool
	if !hasFlushHistory {
		// First write ever - force flush to create database file
		shouldFlush = true
		dw.logf("writer", "WriteDataEntry: First write ever for %s - forcing flush to create database file", ticker)
	} else {
		// Not first write - use normal flush logic
		shouldFlush = dw.shouldFlush(ticker, isActive)
		dw.logf("writer", "WriteDataEntry: shouldFlush check for %s: %v (pending: %d, active: %v)",
			ticker, shouldFlush, pendingCount, isActive)
	}

	if shouldFlush {
		dw.logf("writer", "WriteDataEntry: Triggering flush for %s (pending: %d, active: %v, first ever: %v)",
			ticker, pendingCount, isActive, !hasFlushHistory)
		go func() {
			if err := dw.FlushTicker(ticker); err != nil {
				dw.logf("error", "WriteDataEntry: ❌ Flush failed for %s: %v", ticker, err)
			} else {
				dw.logf("writer", "WriteDataEntry: ✅ Flush completed for %s", ticker)
			}
		}()
	} else if err := dw.commitChartScalars(queued); err != nil {
		// The sample stays pending and the collection flush will retry it.
		dw.logf("error", "WriteDataEntry: chart row for %s was not visible yet: %v", ticker, err)
		return err
	} else {
		dw.logf("writer", "WriteDataEntry: chart row for %s is visible; ladders stay batched (pending: %d)",
			ticker, pendingCount)
	}

	return nil
}

// shouldFlush determines if we should flush based on thresholds
func (dw *DataWriter) shouldFlush(ticker string, isActive bool) bool {
	dw.mu.RLock()
	defer dw.mu.RUnlock()

	pending := dw.pendingWrites[ticker]
	pendingCount := len(pending)

	if pendingCount == 0 {
		dw.logf("writer", "shouldFlush: %s - false (no pending writes)", ticker)
		return false
	}

	// Active tickers flush immediately (every write)
	if isActive {
		dw.logf("writer", "shouldFlush: %s - true (active ticker, pending: %d)", ticker, pendingCount)
		return true
	}

	// Collection tickers flush after threshold
	var countThreshold int
	var intervalThreshold time.Duration

	countThreshold = config.FileWriteCountThresholdCollection
	intervalThreshold = time.Duration(config.FileWriteIntervalCollectionSec) * time.Second

	if pendingCount >= countThreshold {
		dw.logf("writer", "shouldFlush: %s - true (pending count %d >= threshold %d)",
			ticker, pendingCount, countThreshold)
		return true
	}

	// Check interval since first pending write was added
	firstPending, exists := dw.firstPendingTime[ticker]
	if !exists {
		// No pending time tracked - shouldn't happen if we have pending writes
		// Flush anyway to be safe
		dw.logf("writer", "shouldFlush: %s - true (no firstPendingTime but has pending: %d) - flushing to be safe",
			ticker, pendingCount)
		return true
	}

	timeSinceFirstPending := time.Since(firstPending)
	if timeSinceFirstPending >= intervalThreshold {
		dw.logf("writer", "shouldFlush: %s - true (time since first pending %.1fs >= threshold %.1fs, pending: %d)",
			ticker, timeSinceFirstPending.Seconds(), intervalThreshold.Seconds(), pendingCount)
		return true
	}

	dw.logf("writer", "shouldFlush: %s - false (pending: %d, time since first pending: %.1fs, threshold: %.1fs)",
		ticker, pendingCount, timeSinceFirstPending.Seconds(), intervalThreshold.Seconds())
	return false
}

// FlushAllTickers flushes all pending writes for all tickers
func (dw *DataWriter) FlushAllTickers() error {
	dw.mu.RLock()
	tickers := make([]string, 0, len(dw.pendingWrites))
	for ticker := range dw.pendingWrites {
		tickers = append(tickers, ticker)
	}
	dw.mu.RUnlock()

	dw.logf("writer", "FlushAllTickers: Flushing %d tickers", len(tickers))

	var lastErr error
	for _, ticker := range tickers {
		if err := dw.FlushTicker(ticker); err != nil {
			dw.logf("error", "FlushAllTickers: Failed to flush %s: %v", ticker, err)
			lastErr = err
		}
	}

	if lastErr != nil {
		return fmt.Errorf("one or more tickers failed to flush: %w", lastErr)
	}

	dw.logf("writer", "FlushAllTickers: Successfully flushed all %d tickers", len(tickers))
	return nil
}

// tickerFlushLock serializes flushes for one ticker. A second flush waits,
// then writes whatever arrived while the first was encoding. Two flushes
// that both delta from the same head can leave a strike stuck on the
// intermediate value when it changes and returns.
func (dw *DataWriter) tickerFlushLock(ticker string) *sync.Mutex {
	if v, ok := dw.tickerFlush.Load(ticker); ok {
		return v.(*sync.Mutex)
	}
	mu := &sync.Mutex{}
	actual, _ := dw.tickerFlush.LoadOrStore(ticker, mu)
	return actual.(*sync.Mutex)
}

// FlushTicker flushes all pending writes for a ticker
func (dw *DataWriter) FlushTicker(ticker string) (err error) {
	lock := dw.tickerFlushLock(ticker)
	lock.Lock()
	defer lock.Unlock()

	dw.logf("writer", "FlushTicker: Starting flush for %s", ticker)

	dw.mu.Lock()
	pending := dw.pendingWrites[ticker]
	pendingCount := len(pending)
	if pendingCount == 0 {
		dw.mu.Unlock()
		dw.logf("writer", "FlushTicker: No pending writes for %s, skipping flush", ticker)
		return nil
	}

	dw.logf("writer", "FlushTicker: Flushing %d pending writes for %s", pendingCount, ticker)

	// Clear pending writes and reset timing
	dw.pendingWrites[ticker] = make([]*PendingWrite, 0)
	delete(dw.firstPendingTime, ticker)   // Clear first pending time after flush
	dw.lastFlushTime[ticker] = time.Now() // Record flush time
	dw.mu.Unlock()

	// A panic used to drop this slice. Put it back so the next flush can retry.
	defer func() {
		if r := recover(); r != nil {
			dw.logf("error", "FlushTicker panic for %s: %v", ticker, r)
			dw.requeuePending(ticker, pending)
			err = fmt.Errorf("flush panic: %v", r)
		}
	}()

	// Group by date
	// CRITICAL: Preserve timezone when grouping - write.Date is in ET, keep it in ET
	byDate := make(map[time.Time][]*PendingWrite)
	for _, write := range pending {
		// Extract date components and recreate in the same timezone (ET) to preserve the date
		// This prevents timezone conversion issues that can shift the date
		date := time.Date(write.Date.Year(), write.Date.Month(), write.Date.Day(), 0, 0, 0, 0, write.Date.Location())
		byDate[date] = append(byDate[date], write)
	}

	// Flush each date. A failure must put back every date in this batch that
	// was not committed, not only the one that returned the error.
	committed := make(map[time.Time]bool, len(byDate))
	var flushErr error
	for date, writes := range byDate {
		if err := dw.flushDate(ticker, date, writes); err != nil {
			dw.logf("error", "Failed to flush %s for date %s: %v", ticker, date.Format("2006-01-02"), err)
			flushErr = err
			break
		}
		committed[date] = true
	}
	if flushErr != nil {
		dw.mu.Lock()
		for date, writes := range byDate {
			if committed[date] {
				continue
			}
			dw.pendingWrites[ticker] = append(dw.pendingWrites[ticker], writes...)
		}
		dw.mu.Unlock()
		return flushErr
	}

	return nil
}

func (dw *DataWriter) requeuePending(ticker string, writes []*PendingWrite) {
	if len(writes) == 0 {
		return
	}
	dw.mu.Lock()
	dw.pendingWrites[ticker] = append(writes, dw.pendingWrites[ticker]...)
	if _, ok := dw.firstPendingTime[ticker]; !ok {
		dw.firstPendingTime[ticker] = time.Now()
	}
	dw.mu.Unlock()
}

// flushDate flushes writes for a specific date
func (dw *DataWriter) flushDate(ticker string, date time.Time, writes []*PendingWrite) error {
	// Deduplicate timestamps (100ms tolerance - matches Python TIMESTAMP_DEDUP_TOLERANCE_DATA_LOADING)
	// This prevents duplicate data points in the database
	const tolerance = 0.1 // 100ms in seconds
	deduplicatedWrites := dw.deduplicateWrites(writes, tolerance)
	if len(deduplicatedWrites) < len(writes) {
		dw.logf("writer", "Deduplicated %d writes to %d for %s (tolerance: %.3fs)",
			len(writes), len(deduplicatedWrites), ticker, tolerance)
	}
	writes = deduplicatedWrites

	if err := dw.writeChartRows(ticker, date, writes); err != nil {
		return err
	}
	if err := dw.flushProfiles(ticker, dw.getDBPath(ticker, date), writes); err != nil {
		return err
	}

	dw.logf("writer", "flushDate: ✅ Successfully flushed %d writes for %s", len(writes), ticker)
	return nil
}

// commitChartScalars writes one sample's chart columns now.
// The pending entry is left queued so its ladders still flush with the batch.
// The main window reads these columns, so a 1s poll stays visible during the
// collection batch instead of jumping forward when that batch commits.
func (dw *DataWriter) commitChartScalars(write *PendingWrite) error {
	if write == nil || len(write.Scalars) == 0 {
		return nil
	}
	lock := dw.tickerFlushLock(write.Ticker)
	lock.Lock()
	defer lock.Unlock()
	return dw.writeChartRows(write.Ticker, write.Date, []*PendingWrite{write})
}

func (dw *DataWriter) writeChartRows(ticker string, date time.Time, writes []*PendingWrite) error {
	if len(writes) == 0 {
		return nil
	}
	dbPath := dw.getDBPath(ticker, date)
	dw.logf("writer", "writeChartRows: %d rows for %s to %s", len(writes), ticker, dbPath)

	db, err := dw.pool.GetConnection(dbPath, false)
	if err != nil {
		dw.pool.Discard(dbPath)
		return fmt.Errorf("failed to get connection: %w", err)
	}

	// Ensure schema exists
	scalarFields := make([]string, 0)
	scalarFieldsSet := make(map[string]bool)

	// Collect scalar fields from writes
	for _, write := range writes {
		for field := range write.Scalars {
			if !scalarFieldsSet[field] {
				scalarFields = append(scalarFields, field)
				scalarFieldsSet[field] = true
			}
		}
	}

	// Pre-create expected chart columns even if not in current batch
	// This prevents "no such column" errors when reading data before all fields are written
	expectedChartColumns := []string{
		"spot",
		"zero_gamma",
		"major_pos_vol",
		"major_neg_vol",
		"major_long_gamma",
		"major_short_gamma",
		"major_positive",
		"major_negative",
		"major_pos_oi",
		"major_neg_oi",
	}

	// Add expected columns that aren't already in scalarFields
	for _, expectedCol := range expectedChartColumns {
		if !scalarFieldsSet[expectedCol] {
			scalarFields = append(scalarFields, expectedCol)
			scalarFieldsSet[expectedCol] = true
		}
	}

	if err := dw.ensureSchema(db, dbPath, scalarFields); err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}

	// Begin transaction
	tx, err := db.Begin()
	if err != nil {
		dw.pool.Discard(dbPath)
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// One statement per sample, naming only the columns that sample has.
	// A repeated timestamp updates those columns and leaves the rest of the row.
	// Profile ladders go to the sibling file, not profiles_blob.
	for _, write := range writes {
		keys := sortedKeys(write.Scalars)
		insertSQL, cleanKeys := upsertSQL(keys)
		stmt, err := tx.Prepare(insertSQL)
		if err != nil {
			return fmt.Errorf("failed to prepare statement: %w", err)
		}

		args := []interface{}{write.Timestamp}
		for _, field := range cleanKeys {
			args = append(args, write.Scalars[field])
		}

		_, err = stmt.Exec(args...)
		stmt.Close()
		if err != nil {
			tx.Rollback()
			dw.pool.Discard(dbPath)
			return fmt.Errorf("failed to insert: %w", err)
		}
	}

	// Commit transaction. WAL checkpointing stays on the background flusher.
	// A per-flush TRUNCATE checkpoint contended with readers.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	return nil
}

// flushProfiles writes each endpoint's JSON into the sibling profile database.
// A row is replaced only when this sample includes that endpoint.
func (dw *DataWriter) flushProfiles(ticker, chartPath string, writes []*PendingWrite) error {
	if dw.failNextProfileFlush.CompareAndSwap(true, false) {
		return fmt.Errorf("profile flush failed")
	}
	pending := make([]*PendingWrite, 0, len(writes))
	for _, write := range writes {
		if len(write.Profiles) > 0 {
			pending = append(pending, write)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	settings := dw.currentSettings()
	kept := false
	for _, write := range pending {
		for _, endpoint := range sortedKeys(write.Profiles) {
			if config.KeepProfileEndpoint(settings, endpoint) {
				kept = true
				break
			}
		}
		if kept {
			break
		}
	}
	if !kept {
		return nil
	}

	path := ProfileDBPath(chartPath)
	db, err := dw.pool.GetConnection(path, false)
	if err != nil {
		dw.pool.Discard(path)
		return fmt.Errorf("failed to open profile database: %w", err)
	}
	if err := ensureProfileTable(db); err != nil {
		dw.pool.Discard(path)
		return fmt.Errorf("failed to ensure profile table: %w", err)
	}
	markProfileStoreReady(path)

	sort.Slice(pending, func(i, j int) bool { return pending[i].Timestamp < pending[j].Timestamp })

	tx, err := db.Begin()
	if err != nil {
		dw.pool.Discard(path)
		return fmt.Errorf("failed to begin profile transaction: %w", err)
	}
	defer tx.Rollback()

	// The delta head has to be read in this transaction. A read before BEGIN
	// can still see the chain a flush that has not committed yet, or miss one
	// that just did.
	type profileHead struct {
		body  []byte
		chain int
	}
	heads := map[string]*profileHead{}
	for _, write := range pending {
		for _, endpoint := range sortedKeys(write.Profiles) {
			if !config.KeepProfileEndpoint(settings, endpoint) || heads[endpoint] != nil {
				continue
			}
			body, _, chain, err := profileAt(tx, endpoint, nil)
			if err != nil {
				dw.logf("writer", "flushProfiles: %s %s will be stored in full (%v)", ticker, endpoint, err)
				body, chain = nil, 0
			}
			heads[endpoint] = &profileHead{body: body, chain: chain}
		}
	}

	stmt, err := tx.Prepare(`
		INSERT INTO endpoint_profile (timestamp, endpoint, payload) VALUES (?, ?, ?)
		ON CONFLICT(timestamp, endpoint) DO UPDATE SET payload = excluded.payload
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare profile insert: %w", err)
	}
	defer stmt.Close()

	for _, write := range pending {
		for _, endpoint := range sortedKeys(write.Profiles) {
			if !config.KeepProfileEndpoint(settings, endpoint) {
				continue
			}
			head := heads[endpoint]
			next, err := json.Marshal(write.Profiles[endpoint])
			if err != nil {
				return err
			}
			stored, chain, skip, err := encodeProfile(head.body, head.chain, next)
			if err != nil {
				return err
			}
			head.body = next
			head.chain = chain
			if skip {
				continue
			}
			payload, err := gzipJSON(json.RawMessage(stored))
			if err != nil {
				return err
			}
			if _, err := stmt.Exec(write.Timestamp, endpoint, payload); err != nil {
				tx.Rollback()
				dw.pool.Discard(path)
				return fmt.Errorf("failed to insert profile %s for %s: %w", endpoint, ticker, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit profiles: %w", err)
	}
	dw.logf("writer", "flushProfiles: wrote profiles for %s to %s", ticker, path)
	return nil
}

// splitCollectedFields separates numeric columns from profile payloads.
// Numeric zeros are kept. Nulls, empty strings, and false are not columns.
// Array and object values stay as raw JSON when the fetch captured them that way.
func splitCollectedFields(data map[string]interface{}) (map[string]interface{}, map[string]interface{}) {
	scalars := make(map[string]interface{})
	profiles := make(map[string]interface{})

	if prof, ok := data["profiles"].(map[string]interface{}); ok {
		for key, value := range prof {
			profiles[key] = value
		}
	} else if raw, ok := data["profiles"].(json.RawMessage); ok {
		var inner map[string]json.RawMessage
		if json.Unmarshal(raw, &inner) == nil {
			for key, value := range inner {
				profiles[key] = value
			}
		}
	}

	for key, value := range data {
		if key == "profiles" || key == "timestamp" || key == "ticker" || key == "_response_headers" || key == "_response_time" {
			continue
		}
		switch v := value.(type) {
		case []interface{}, map[string]interface{}, json.RawMessage:
			profiles[key] = v
		default:
			if v == nil || v == "" || v == false {
				continue
			}
			scalars[key] = v
		}
	}
	return scalars, profiles
}

func sortedKeys(fields map[string]interface{}) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// upsertSQL inserts the columns this sample has and, on a repeated timestamp,
// updates only those columns.
func upsertSQL(fields []string) (string, []string) {
	columns := []string{"timestamp"}
	placeholders := []string{"?"}
	updates := make([]string, 0, len(fields)+1)
	seen := map[string]bool{"timestamp": true, "profiles_blob": true}
	clean := make([]string, 0, len(fields))

	for _, field := range fields {
		sanitized := sanitizeFieldName(field)
		if sanitized == "" || seen[sanitized] {
			continue
		}
		seen[sanitized] = true
		columns = append(columns, sanitized)
		placeholders = append(placeholders, "?")
		updates = append(updates, sanitized+" = excluded."+sanitized)
		clean = append(clean, field)
	}

	if len(updates) == 0 {
		return fmt.Sprintf(
			"INSERT INTO ticker_data (%s) VALUES (%s) ON CONFLICT(timestamp) DO NOTHING",
			strings.Join(columns, ", "),
			strings.Join(placeholders, ", "),
		), clean
	}
	return fmt.Sprintf(
		"INSERT INTO ticker_data (%s) VALUES (%s) ON CONFLICT(timestamp) DO UPDATE SET %s",
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(updates, ", "),
	), clean
}

// mergeProfiles keeps keys from earlier and lets later replace the ones it has.
func mergeProfiles(earlier, later map[string]interface{}) map[string]interface{} {
	if len(earlier) == 0 {
		return later
	}
	out := make(map[string]interface{}, len(earlier)+len(later))
	for key, value := range earlier {
		out[key] = value
	}
	for key, value := range later {
		out[key] = value
	}
	return out
}

func gzipJSON(value interface{}) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal profiles: %w", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(payload); err != nil {
		return nil, fmt.Errorf("failed to compress profiles: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}
	return buf.Bytes(), nil
}

// ensureSchema creates the table and any new columns, then remembers that set
// so later flushes of the same file do not repeat the catalog queries.
func (dw *DataWriter) ensureSchema(db *sql.DB, path string, fields []string) error {
	needed := make([]string, 0, len(fields))
	for _, field := range fields {
		sanitized := sanitizeFieldName(field)
		if sanitized != "" && sanitized != "timestamp" && sanitized != "profiles_blob" {
			needed = append(needed, sanitized)
		}
	}

	dw.schemaMu.Lock()
	known := dw.knownColumns[path]
	if known != nil {
		ready := true
		for _, name := range needed {
			if !known[name] {
				ready = false
				break
			}
		}
		if ready {
			dw.schemaMu.Unlock()
			return nil
		}
	}
	dw.schemaMu.Unlock()

	if err := NewSchemaManager(db).EnsureTable(fields); err != nil {
		return err
	}

	dw.schemaMu.Lock()
	if dw.knownColumns == nil {
		dw.knownColumns = make(map[string]map[string]bool)
	}
	set := dw.knownColumns[path]
	if set == nil {
		set = make(map[string]bool)
		dw.knownColumns[path] = set
	}
	set["timestamp"] = true
	set["profiles_blob"] = true
	for _, name := range needed {
		set[name] = true
	}
	dw.schemaMu.Unlock()
	return nil
}

func (dw *DataWriter) getDBPath(ticker string, date time.Time) string {
	dataDir := ""
	if dw.settings != nil {
		dataDir = dw.settings.DataDirectory
	}
	return DBPath(dataDir, ticker, date, dw.debugPrint, "writer")
}

// deduplicateWrites removes duplicate timestamps within tolerance
// Uses 100ms tolerance (matches Python TIMESTAMP_DEDUP_TOLERANCE_DATA_LOADING)
// Keeps the last write for each timestamp group
func (dw *DataWriter) deduplicateWrites(writes []*PendingWrite, tolerance float64) []*PendingWrite {
	if len(writes) == 0 {
		return writes
	}

	// Sort by timestamp (ascending)
	sorted := make([]*PendingWrite, len(writes))
	copy(sorted, writes)

	// Simple bubble sort (small arrays, performance not critical)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i].Timestamp > sorted[j].Timestamp {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Deduplicate: keep last write within tolerance window
	result := make([]*PendingWrite, 0)
	for i := 0; i < len(sorted); i++ {
		// Check if this timestamp is within tolerance of next write
		if i < len(sorted)-1 {
			timeDiff := sorted[i+1].Timestamp - sorted[i].Timestamp
			if timeDiff <= tolerance {
				// Within tolerance - keep the later sample, but do not drop
				// profile endpoints the later sample did not include.
				sorted[i+1].Profiles = mergeProfiles(sorted[i].Profiles, sorted[i+1].Profiles)
				continue
			}
		}
		// Keep this write (either last in group or unique)
		result = append(result, sorted[i])
	}

	return result
}

// Close stops the background flusher, flushes all pending writes, and closes
// all connections. Ensures data is on disk and WAL files are cleaned up.
func (dw *DataWriter) Close() error {
	dw.debugPrint("DataWriter: Closing - flushing all pending writes", "writer")

	// Stop the background flusher first so it doesn't race with final flushes
	close(dw.stopChan)
	dw.wg.Wait()

	// Flush all pending writes before closing
	dw.mu.Lock()
	tickersToFlush := make([]string, 0, len(dw.pendingWrites))
	for ticker := range dw.pendingWrites {
		if len(dw.pendingWrites[ticker]) > 0 {
			tickersToFlush = append(tickersToFlush, ticker)
		}
	}
	dw.mu.Unlock()

	// Flush each ticker synchronously (we're shutting down, so async doesn't matter)
	for _, ticker := range tickersToFlush {
		if err := dw.FlushTicker(ticker); err != nil {
			dw.logf("error", "DataWriter: Warning - failed to flush %s on close: %v", ticker, err)
		} else {
			dw.logf("writer", "DataWriter: Flushed %s on close", ticker)
		}
	}

	// Close connection pool (this will checkpoint WAL and close all connections)
	if err := dw.pool.Close(); err != nil {
		return fmt.Errorf("failed to close connection pool: %w", err)
	}

	dw.debugPrint("DataWriter: Closed successfully", "writer")
	return nil
}
