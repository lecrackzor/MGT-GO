package database

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

// DataWriter handles writing market data to SQLite databases
type DataWriter struct {
	pool              *ConnectionPool
	schemaManager     *SchemaManager
	mu                sync.RWMutex
	pendingWrites     map[string][]*PendingWrite // ticker -> []PendingWrite
	firstPendingTime  map[string]time.Time       // When first pending write was added (for flush timing)
	lastFlushTime     map[string]time.Time       // When last flush occurred
	settings          *config.Settings
	debugPrint        func(string, string)
	
	// Background flusher
	stopChan          chan struct{}
	wg                sync.WaitGroup
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
		time.Duration(config.SQLiteConnectionIdleTimeoutSeconds)*time.Second,
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
func (dw *DataWriter) startBackgroundFlusher() {
	dw.wg.Add(1)
	go func() {
		defer dw.wg.Done()
		
		// Check every second for pending writes that need flushing
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		
		for {
			select {
			case <-dw.stopChan:
				dw.debugPrint("Background flusher stopping", "writer")
				return
			case <-ticker.C:
				dw.checkAndFlushPending()
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
			dw.debugPrint(fmt.Sprintf("Background flusher: triggering flush for %s", ticker), "writer")
			if err := dw.FlushTicker(ticker); err != nil {
				dw.debugPrint(fmt.Sprintf("Background flusher: flush failed for %s: %v", ticker, err), "error")
			}
		}
	}
}

// Stop stops the background flusher and flushes any remaining pending writes
func (dw *DataWriter) Stop() {
	dw.debugPrint("Stopping DataWriter...", "writer")
	
	// Signal background flusher to stop
	close(dw.stopChan)
	dw.wg.Wait()
	
	// Flush any remaining pending writes
	dw.mu.RLock()
	tickers := make([]string, 0)
	for ticker := range dw.pendingWrites {
		tickers = append(tickers, ticker)
	}
	dw.mu.RUnlock()
	
	for _, ticker := range tickers {
		if err := dw.FlushTicker(ticker); err != nil {
			dw.debugPrint(fmt.Sprintf("Stop: failed to flush %s: %v", ticker, err), "error")
		}
	}
	
	dw.debugPrint("DataWriter stopped", "writer")
}

// WriteDataEntry writes a single data entry (queues for batch write)
func (dw *DataWriter) WriteDataEntry(ticker string, timestamp float64, data map[string]interface{}, isActive bool) error {
	dw.debugPrint(fmt.Sprintf("WriteDataEntry: Called for %s (timestamp: %.0f, fields: %d, active: %v)", 
		ticker, timestamp, len(data), isActive), "writer")
	
	dw.mu.Lock()
	// Note: We unlock before calling shouldFlush() to avoid deadlock
	// shouldFlush() needs its own read lock, and we can't hold a write lock while acquiring a read lock

	// Extract scalars and profiles
	scalars := make(map[string]interface{})
	profiles := make(map[string]interface{})

	if prof, ok := data["profiles"].(map[string]interface{}); ok {
		profiles = prof
	}

	scalarCount := 0
	profileCount := 0
	
	for key, value := range data {
		if key == "profiles" || key == "timestamp" || key == "ticker" || key == "_response_headers" || key == "_response_time" {
			continue // Skip metadata fields
		}

		// Check if value is array/dict (store in profiles)
		switch v := value.(type) {
		case []interface{}, map[string]interface{}:
			profiles[key] = v
			profileCount++
		default:
			// Skip zero values for scalar fields (optimization - matches Python version)
			// This reduces database size and improves performance
			if v == nil || v == 0 || v == 0.0 || v == "" || v == false {
				continue // Skip zero/null values
			}
			scalars[key] = v
			scalarCount++
		}
	}
	
	dw.debugPrint(fmt.Sprintf("WriteDataEntry: Extracted %d scalars, %d profiles for %s", 
		scalarCount, profileCount, ticker), "writer")

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
	dw.debugPrint(fmt.Sprintf("WriteDataEntry: Timestamp %d (UTC: %s, ET: %s) -> GetMarketDateForDate() returned: %s -> Final entryDate: %s", 
		int64(timestamp), 
		timestampTime.Format("2006-01-02 15:04:05 MST"),
		timestampET.Format("2006-01-02 15:04:05 MST"),
		entryDate.Format("2006-01-02 15:04:05 MST"),
		entryDate.Format("2006-01-02 15:04:05 MST")), "writer")

	// Add to pending writes
	if dw.pendingWrites[ticker] == nil {
		dw.pendingWrites[ticker] = make([]*PendingWrite, 0)
		dw.debugPrint(fmt.Sprintf("WriteDataEntry: First write for %s, initializing pending writes", ticker), "writer")
	}

	dw.pendingWrites[ticker] = append(dw.pendingWrites[ticker], &PendingWrite{
		Ticker:    ticker,
		Timestamp: timestamp,
		Scalars:   scalars,
		Profiles:  profiles,
		Date:      entryDate,
	})
	
	pendingCount := len(dw.pendingWrites[ticker])
	
	// CRITICAL: Check if this is the first write ever for this ticker
	// If it's the first write, we need to force flush to create the database file
	_, hasFlushHistory := dw.lastFlushTime[ticker]
	
	// Track when first pending write was added (for flush interval calculation)
	// Only set if this is the first pending write in the current batch
	if pendingCount == 1 {
		dw.firstPendingTime[ticker] = time.Now()
	}
	
	dw.debugPrint(fmt.Sprintf("WriteDataEntry: Added write to pending queue for %s (total pending: %d, first ever: %v)", 
		ticker, pendingCount, !hasFlushHistory), "writer")

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
		dw.debugPrint(fmt.Sprintf("WriteDataEntry: First write ever for %s - forcing flush to create database file", ticker), "writer")
	} else {
		// Not first write - use normal flush logic
		shouldFlush = dw.shouldFlush(ticker, isActive)
		dw.debugPrint(fmt.Sprintf("WriteDataEntry: shouldFlush check for %s: %v (pending: %d, active: %v)", 
			ticker, shouldFlush, pendingCount, isActive), "writer")
	}
	
	if shouldFlush {
		dw.debugPrint(fmt.Sprintf("WriteDataEntry: Triggering flush for %s (pending: %d, active: %v, first ever: %v)", 
			ticker, pendingCount, isActive, !hasFlushHistory), "writer")
		go func() {
			if err := dw.FlushTicker(ticker); err != nil {
				dw.debugPrint(fmt.Sprintf("WriteDataEntry: ❌ Flush failed for %s: %v", ticker, err), "error")
			} else {
				dw.debugPrint(fmt.Sprintf("WriteDataEntry: ✅ Flush completed for %s", ticker), "writer")
			}
		}()
	} else {
		dw.debugPrint(fmt.Sprintf("WriteDataEntry: Not flushing %s yet (pending: %d, will flush when threshold met)", 
			ticker, pendingCount), "writer")
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
		dw.debugPrint(fmt.Sprintf("shouldFlush: %s - false (no pending writes)", ticker), "writer")
		return false
	}

	// Active tickers flush immediately (every write)
	if isActive {
		dw.debugPrint(fmt.Sprintf("shouldFlush: %s - true (active ticker, pending: %d)", ticker, pendingCount), "writer")
		return true
	}

	// Collection tickers flush after threshold
	var countThreshold int
	var intervalThreshold time.Duration

	countThreshold = config.FileWriteCountThresholdCollection
	intervalThreshold = time.Duration(config.FileWriteIntervalCollectionSec) * time.Second

	if pendingCount >= countThreshold {
		dw.debugPrint(fmt.Sprintf("shouldFlush: %s - true (pending count %d >= threshold %d)", 
			ticker, pendingCount, countThreshold), "writer")
		return true
	}

	// Check interval since first pending write was added
	firstPending, exists := dw.firstPendingTime[ticker]
	if !exists {
		// No pending time tracked - shouldn't happen if we have pending writes
		// Flush anyway to be safe
		dw.debugPrint(fmt.Sprintf("shouldFlush: %s - true (no firstPendingTime but has pending: %d) - flushing to be safe", 
			ticker, pendingCount), "writer")
		return true
	}

	timeSinceFirstPending := time.Since(firstPending)
	if timeSinceFirstPending >= intervalThreshold {
		dw.debugPrint(fmt.Sprintf("shouldFlush: %s - true (time since first pending %.1fs >= threshold %.1fs, pending: %d)", 
			ticker, timeSinceFirstPending.Seconds(), intervalThreshold.Seconds(), pendingCount), "writer")
		return true
	}

	dw.debugPrint(fmt.Sprintf("shouldFlush: %s - false (pending: %d, time since first pending: %.1fs, threshold: %.1fs)", 
		ticker, pendingCount, timeSinceFirstPending.Seconds(), intervalThreshold.Seconds()), "writer")
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
	
	dw.debugPrint(fmt.Sprintf("FlushAllTickers: Flushing %d tickers", len(tickers)), "writer")
	
	var lastErr error
	for _, ticker := range tickers {
		if err := dw.FlushTicker(ticker); err != nil {
			dw.debugPrint(fmt.Sprintf("FlushAllTickers: Failed to flush %s: %v", ticker, err), "error")
			lastErr = err
		}
	}
	
	if lastErr != nil {
		return fmt.Errorf("one or more tickers failed to flush: %w", lastErr)
	}
	
	dw.debugPrint(fmt.Sprintf("FlushAllTickers: Successfully flushed all %d tickers", len(tickers)), "writer")
	return nil
}

// FlushTicker flushes all pending writes for a ticker
func (dw *DataWriter) FlushTicker(ticker string) error {
	dw.debugPrint(fmt.Sprintf("FlushTicker: Starting flush for %s", ticker), "writer")
	
	dw.mu.Lock()
	pending := dw.pendingWrites[ticker]
	pendingCount := len(pending)
	if pendingCount == 0 {
		dw.mu.Unlock()
		dw.debugPrint(fmt.Sprintf("FlushTicker: No pending writes for %s, skipping flush", ticker), "writer")
		return nil
	}

	dw.debugPrint(fmt.Sprintf("FlushTicker: Flushing %d pending writes for %s", pendingCount, ticker), "writer")

	// Clear pending writes and reset timing
	dw.pendingWrites[ticker] = make([]*PendingWrite, 0)
	delete(dw.firstPendingTime, ticker) // Clear first pending time after flush
	dw.lastFlushTime[ticker] = time.Now() // Record flush time
	dw.mu.Unlock()

	// Group by date
	// CRITICAL: Preserve timezone when grouping - write.Date is in ET, keep it in ET
	byDate := make(map[time.Time][]*PendingWrite)
	for _, write := range pending {
		// Extract date components and recreate in the same timezone (ET) to preserve the date
		// This prevents timezone conversion issues that can shift the date
		date := time.Date(write.Date.Year(), write.Date.Month(), write.Date.Day(), 0, 0, 0, 0, write.Date.Location())
		byDate[date] = append(byDate[date], write)
	}

	// Flush each date
	for date, writes := range byDate {
		if err := dw.flushDate(ticker, date, writes); err != nil {
			dw.debugPrint(fmt.Sprintf("Failed to flush %s for date %s: %v", ticker, date.Format("2006-01-02"), err), "error")
			// Re-add failed writes
			dw.mu.Lock()
			dw.pendingWrites[ticker] = append(dw.pendingWrites[ticker], writes...)
			dw.mu.Unlock()
			return err
		}
	}

	return nil
}

// flushDate flushes writes for a specific date
func (dw *DataWriter) flushDate(ticker string, date time.Time, writes []*PendingWrite) error {
	// Deduplicate timestamps (100ms tolerance - matches Python TIMESTAMP_DEDUP_TOLERANCE_DATA_LOADING)
	// This prevents duplicate data points in the database
	const tolerance = 0.1 // 100ms in seconds
	deduplicatedWrites := dw.deduplicateWrites(writes, tolerance)
	if len(deduplicatedWrites) < len(writes) {
		dw.debugPrint(fmt.Sprintf("Deduplicated %d writes to %d for %s (tolerance: %.3fs)", 
			len(writes), len(deduplicatedWrites), ticker, tolerance), "writer")
	}
	writes = deduplicatedWrites
	
	// Get database path
	dbPath := dw.getDBPath(ticker, date)
	dw.debugPrint(fmt.Sprintf("flushDate: Flushing %d writes for %s to %s", len(writes), ticker, dbPath), "writer")

	// Get connection
	db, err := dw.pool.GetConnection(dbPath, false)
	if err != nil {
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

	schemaManager := NewSchemaManager(db)
	if err := schemaManager.EnsureTable(scalarFields); err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}

	// Begin transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Collect all unique scalar fields from all writes
	allScalarFields := make(map[string]bool)
	for _, write := range writes {
		for field := range write.Scalars {
			allScalarFields[field] = true
		}
	}

	// Convert to sorted slice for consistent column order
	scalarFieldsList := make([]string, 0, len(allScalarFields))
	for field := range allScalarFields {
		scalarFieldsList = append(scalarFieldsList, field)
	}

	// Prepare insert statement
	insertSQL := dw.buildInsertStatement(scalarFieldsList)
	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	// Insert each write
	for _, write := range writes {
		// Compress profiles to BLOB
		var profilesBlob []byte
		if len(write.Profiles) > 0 {
			profilesJSON, err := json.Marshal(write.Profiles)
			if err != nil {
				return fmt.Errorf("failed to marshal profiles: %w", err)
			}

			// Compress with gzip
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			if _, err := gz.Write(profilesJSON); err != nil {
				return fmt.Errorf("failed to compress profiles: %w", err)
			}
			if err := gz.Close(); err != nil {
				return fmt.Errorf("failed to close gzip writer: %w", err)
			}
			profilesBlob = buf.Bytes()
		}

		// Build values for insert
		args := []interface{}{write.Timestamp, profilesBlob}
		for _, field := range scalarFieldsList {
			if value, ok := write.Scalars[field]; ok {
				args = append(args, value)
			} else {
				args = append(args, nil)
			}
		}

		if _, err := stmt.Exec(args...); err != nil {
			return fmt.Errorf("failed to insert: %w", err)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	dw.debugPrint(fmt.Sprintf("flushDate: Transaction committed for %s to %s", ticker, dbPath), "writer")

	// WAL checkpointing: Checkpoint WAL file after every flush (prevents WAL file growth)
	// This matches Python version which checkpoints every flush
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err == nil {
		// Execute WAL checkpoint (TRUNCATE mode moves WAL data to main DB and truncates WAL file)
		_, err = conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
		if err != nil {
			// Log but don't fail - checkpoint is optional
			dw.debugPrint(fmt.Sprintf("WAL checkpoint warning for %s: %v", ticker, err), "writer")
		} else {
			dw.debugPrint(fmt.Sprintf("WAL checkpoint completed for %s", ticker), "writer")
		}
		conn.Close()
	}

	// Verify database file exists after commit and checkpoint
	if fileInfo, err := os.Stat(dbPath); err != nil {
		dw.debugPrint(fmt.Sprintf("flushDate: ⚠️ WARNING - Database file does not exist after commit: %s (error: %v)", dbPath, err), "error")
	} else {
		dw.debugPrint(fmt.Sprintf("flushDate: ✅ Database file verified: %s (size: %d bytes)", dbPath, fileInfo.Size()), "writer")
	}

	// Also check for WAL file (should be empty or small after checkpoint)
	walPath := dbPath + "-wal"
	if walInfo, err := os.Stat(walPath); err == nil {
		if walInfo.Size() > 0 {
			dw.debugPrint(fmt.Sprintf("flushDate: WAL file exists: %s (size: %d bytes) - checkpoint may not have completed", walPath, walInfo.Size()), "writer")
		} else {
			dw.debugPrint(fmt.Sprintf("flushDate: WAL file is empty (checkpoint successful): %s", walPath), "writer")
		}
	}

	dw.debugPrint(fmt.Sprintf("flushDate: ✅ Successfully flushed %d writes for %s to %s", len(writes), ticker, dbPath), "writer")
	return nil
}

// buildInsertStatement builds an INSERT statement with all scalar fields
// Note: This is a simplified version - in production, we'd need to handle dynamic columns better
func (dw *DataWriter) buildInsertStatement(scalarFields []string) string {
	// Build column list
	columns := []string{"timestamp", "profiles_blob"}
	placeholders := []string{"?", "?"}

	// Use a set to track unique sanitized field names
	seen := make(map[string]bool)
	seen["timestamp"] = true
	seen["profiles_blob"] = true

	for _, field := range scalarFields {
		sanitized := sanitizeFieldName(field)
		if !seen[sanitized] {
			columns = append(columns, sanitized)
			placeholders = append(placeholders, "?")
			seen[sanitized] = true
		}
	}

	return fmt.Sprintf(
		"INSERT OR REPLACE INTO ticker_data (%s) VALUES (%s)",
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
	)
}

// getDBPath returns the database file path for a ticker and date
// The date passed here is already the correct market date (from WriteDataEntry)
// We only need to handle weekend adjustments if the date is a weekend
func (dw *DataWriter) getDBPath(ticker string, date time.Time) string {
	dataDir := dw.settings.DataDirectory
	if dataDir == "" {
		dataDir = "Tickers"
	}

	// The date passed to this function is already the correct market date
	// (it was calculated in WriteDataEntry using GetMarketDate() which handles rollover)
	// DO NOT call GetMarketDateForDate() here - it would apply rollover logic again
	// Since the date is at midnight, GetMarketDateForDate() would think it's before 8:30 AM
	// and subtract a day, causing the wrong directory to be created
	
	// CRITICAL: Ensure date is in ET timezone before processing
	// The date might be in UTC from FlushTicker grouping, so convert to ET first
	dateET := date.In(utils.GetMarketTimezone())
	
	// Only handle weekend adjustment if needed
	var marketDate time.Time
	if utils.IsWeekend(dateET) {
		marketDate = utils.GetLastTradingDay(dateET)
		dw.debugPrint(fmt.Sprintf("getDBPath: Weekend detected for %s, using last Friday: %s", 
			dateET.Format("2006-01-02"), marketDate.Format("2006-01-02")), "writer")
	} else {
		marketDate = dateET
	}

	// Format date as MM.DD.YYYY (marketDate is now guaranteed to be in ET)
	dateStr := marketDate.Format("01.02.2006")
	// Directory format: "Tickers 01.14.2026" (not "Tickers\Tickers 01.14.2026")
	dir := fmt.Sprintf("%s %s", dataDir, dateStr)
	
	// Log directory construction
	dw.debugPrint(fmt.Sprintf("getDBPath: Constructing path for %s on %s (input date: %s, market date: %s): dataDir=%s, dateStr=%s, dir=%s", 
		ticker, date.Format("2006-01-02"), dateET.Format("2006-01-02"), marketDate.Format("2006-01-02"), dataDir, dateStr, dir), "writer")
	
	if err := os.MkdirAll(dir, 0755); err != nil {
		dw.debugPrint(fmt.Sprintf("getDBPath: WARNING - Failed to create directory %s: %v", dir, err), "error")
	}

	return filepath.Join(dir, fmt.Sprintf("%s.db", ticker))
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
				// Within tolerance - skip this one, keep the next (last in group)
				continue
			}
		}
		// Keep this write (either last in group or unique)
		result = append(result, sorted[i])
	}
	
	return result
}

// Close closes all connections and flushes any pending writes
// Ensures all data is written to disk and WAL files are cleaned up
func (dw *DataWriter) Close() error {
	dw.debugPrint("DataWriter: Closing - flushing all pending writes", "writer")
	
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
			dw.debugPrint(fmt.Sprintf("DataWriter: Warning - failed to flush %s on close: %v", ticker, err), "error")
		} else {
			dw.debugPrint(fmt.Sprintf("DataWriter: Flushed %s on close", ticker), "writer")
		}
	}
	
	// Close connection pool (this will checkpoint WAL and close all connections)
	if err := dw.pool.Close(); err != nil {
		return fmt.Errorf("failed to close connection pool: %w", err)
	}
	
	dw.debugPrint("DataWriter: Closed successfully", "writer")
	return nil
}
