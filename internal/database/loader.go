package database

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"market-terminal/internal/config"
)

// DataLoader handles loading data from SQLite databases
type DataLoader struct {
	pool       *ConnectionPool
	settings   *config.Settings
	debugPrint func(string, string)
	debugOn    func(string) bool

	colMu       sync.RWMutex
	columnCache map[string]map[string]bool
	migrated    map[string]struct{}

	settingsMu sync.RWMutex
}

// SetDebugGate skips formatting log lines when the category is disabled.
func (dl *DataLoader) SetDebugGate(fn func(string) bool) {
	dl.debugOn = fn
}

func (dl *DataLoader) logf(category, format string, args ...interface{}) {
	if dl.debugOn != nil && !dl.debugOn(category) {
		return
	}
	if dl.debugPrint == nil {
		return
	}
	dl.debugPrint(fmt.Sprintf(format, args...), category)
}

// NewDataLoader creates a new data loader
func NewDataLoader(settings *config.Settings, debugPrint func(string, string)) *DataLoader {
	pool := NewConnectionPool(
		config.DBConnectionPoolMaxSize,
		time.Duration(config.DBConnectionIdleTimeoutSec)*time.Second,
		time.Duration(config.SQLiteConnectionCleanupIntervalSeconds)*time.Second,
	)

	return &DataLoader{
		pool:       pool,
		settings:   settings,
		debugPrint: debugPrint,
	}
}

// SetSettings replaces the settings used to resolve the data directory.
// LoadSettings returns a new object, so charts keep the startup folder until this runs.
func (dl *DataLoader) SetSettings(settings *config.Settings) {
	dl.settingsMu.Lock()
	dl.settings = settings
	dl.settingsMu.Unlock()
}

// LoadChartData loads only the columns needed for chart display.
// It is READ-ONLY: opens the database with read-only connection and never writes.
// Chart data requests do not modify the database file.
// CRITICAL: Skips profiles_blob to prevent massive memory usage (28GB+ issue)
// Loads: timestamp, spot, zero_gamma, major_pos_vol, major_neg_vol, major_long_gamma, major_short_gamma,
//
//	major_positive, major_negative, major_pos_oi, major_neg_oi
//
// Does NOT use query cache (chart data changes frequently)
// If since is non-nil, rows with timestamp >= since are returned so an update of the last point is visible.
func (dl *DataLoader) LoadChartData(ticker string, date time.Time, maxRows int, since *float64) (map[string]ChartSeries, error) {
	dateStr := date.Format("2006-01-02")
	incStr := "full"
	if since != nil {
		incStr = fmt.Sprintf("incremental since=%v", *since)
	}
	dl.logf("loader", "LoadChartData: [START] Loading chart data for %s on %s (maxRows=%d, %s)", ticker, dateStr, maxRows, incStr)
	dbPath := dl.getDBPath(ticker, date)
	dl.logf("loader", "LoadChartData: Checking database path for %s on %s: %s", ticker, dateStr, dbPath)

	// Check if file exists - return empty data if it doesn't
	fileInfo, err := os.Stat(dbPath)
	if os.IsNotExist(err) {
		dl.logf("loader", "LoadChartData: Database file does not exist for %s: %s", ticker, dbPath)
		return emptyChartSeries(), nil
	}
	if err != nil {
		dl.logf("error", "LoadChartData: Error checking file existence for %s: %v", ticker, err)
		return nil, fmt.Errorf("failed to check file existence: %w", err)
	}
	dl.logf("loader", "LoadChartData: Database file exists for %s: %s (size: %d bytes)", ticker, dbPath, fileInfo.Size())
	dl.migrateFile(dbPath)

	// Get connection
	db, err := dl.pool.GetConnection(dbPath, true) // Read-only
	if err != nil {
		dl.logf("error", "LoadChartData: Failed to get connection for %s: %v", ticker, err)
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	dl.logf("loader", "LoadChartData: Got database connection for %s", ticker)

	// Only load columns needed for charts (explicitly exclude profiles_blob)
	requiredColumns := []string{
		"timestamp",
		"spot",
		"zero_gamma",
		"major_pos_vol",     // Positive gamma
		"major_neg_vol",     // Negative gamma
		"major_long_gamma",  // Long gamma
		"major_short_gamma", // Short gamma
		"major_positive",    // Major positive strike
		"major_negative",    // Major negative strike
		"major_pos_oi",      // Major positive OI
		"major_neg_oi",      // Major negative OI
	}

	// Check which columns actually exist in the table.
	// Once every chart column is present the list is cached for this file.
	existingColumns, err := dl.tableColumnsCached(db, dbPath, requiredColumns)
	if err != nil {
		dl.logf("error", "LoadChartData: Failed to get existing columns for %s: %v", ticker, err)
		dl.pool.Discard(dbPath)
		return nil, fmt.Errorf("failed to get existing columns: %w", err)
	}

	// Filter to only include columns that exist
	existingRequiredColumns := make([]string, 0)
	for _, col := range requiredColumns {
		if existingColumns[col] {
			existingRequiredColumns = append(existingRequiredColumns, col)
		} else {
			dl.logf("loader", "LoadChartData: Column %s does not exist in table for %s, will return empty array", col, ticker)
		}
	}

	// If no columns exist (or only timestamp), return empty data
	if len(existingRequiredColumns) == 0 || (len(existingRequiredColumns) == 1 && existingRequiredColumns[0] == "timestamp") {
		dl.logf("loader", "LoadChartData: No required columns exist in table for %s, returning empty data", ticker)
		return emptyChartSeries(), nil
	}

	// Build SELECT statement with only existing required columns
	// NOTE: Embed limit directly in query string (modernc.org/sqlite may not handle LIMIT ? correctly)
	selectCols := strings.Join(existingRequiredColumns, ", ")
	var rows *sql.Rows
	if since != nil {
		query := fmt.Sprintf("SELECT %s FROM ticker_data WHERE timestamp >= ? ORDER BY timestamp ASC LIMIT %d", selectCols, maxRows)
		dl.logf("loader", "LoadChartData: Executing incremental query for %s: timestamp >= %v", ticker, *since)
		rows, err = db.Query(query, *since)
	} else {
		// The cap keeps the newest rows, then the chart still receives them oldest-first.
		// A plain ASC limit charts the start of a long session.
		query := fmt.Sprintf(
			"SELECT %s FROM (SELECT %s FROM ticker_data ORDER BY timestamp DESC LIMIT %d) ORDER BY timestamp ASC",
			selectCols, selectCols, maxRows,
		)
		dl.logf("loader", "LoadChartData: Executing query for %s: %s", ticker, query)
		rows, err = db.Query(query)
	}
	if err != nil {
		dl.pool.Discard(dbPath)
		dl.logf("error", "LoadChartData: Query failed for %s: %v", ticker, err)
		// Check if table exists
		tableCheckQuery := "SELECT name FROM sqlite_master WHERE type='table' AND name='ticker_data'"
		tableRows, tableErr := db.Query(tableCheckQuery)
		if tableErr == nil {
			hasTable := tableRows.Next()
			tableRows.Close()
			if !hasTable {
				dl.logf("error", "LoadChartData: Table 'ticker_data' does not exist in database for %s", ticker)
				return nil, fmt.Errorf("table 'ticker_data' does not exist: %w", err)
			}
		}
		return nil, fmt.Errorf("failed to query: %w", err)
	}
	defer rows.Close()
	dl.logf("loader", "LoadChartData: Query executed successfully for %s, scanning rows...", ticker)

	// One float slice per column. SQL NULL is stored as NaN and later encoded as JSON null.
	capHint := maxRows
	if capHint <= 0 {
		capHint = 256
	}
	result := make(map[string]ChartSeries, len(requiredColumns))
	for _, col := range requiredColumns {
		if existingColumns[col] {
			result[col] = make(ChartSeries, 0, capHint)
		} else {
			result[col] = ChartSeries{}
		}
	}

	nCols := len(existingRequiredColumns)
	holders := make([]sql.NullFloat64, nCols)
	valuePtrs := make([]interface{}, nCols)
	for i := range holders {
		valuePtrs[i] = &holders[i]
	}

	rowCount := 0
	for rows.Next() {
		if err := rows.Scan(valuePtrs...); err != nil {
			rows.Close()
			dl.pool.Discard(dbPath)
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		for i, col := range existingRequiredColumns {
			if holders[i].Valid && !math.IsNaN(holders[i].Float64) && !math.IsInf(holders[i].Float64, 0) {
				result[col] = append(result[col], holders[i].Float64)
			} else {
				result[col] = append(result[col], math.NaN())
			}
		}
		rowCount++
	}

	if err := rows.Err(); err != nil {
		dl.logf("error", "LoadChartData: Error iterating rows for %s: %v", ticker, err)
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	if rowCount == 0 {
		dl.logf("loader", "LoadChartData: WARNING - Query returned 0 rows for %s on %s (table exists but is empty)", ticker, dateStr)
	} else {
		dl.logf("loader", "LoadChartData: Successfully loaded %d rows for %s on %s (skipped profiles_blob)", rowCount, ticker, dateStr)
		// Log timestamp count for debugging
		if timestamps, ok := result["timestamp"]; ok {
			dl.logf("loader", "LoadChartData: Result contains %d timestamps for %s", len(timestamps), ticker)
		} else {
			dl.logf("error", "LoadChartData: WARNING - Result does not contain 'timestamp' key for %s", ticker)
		}
	}

	dl.logf("loader", "LoadChartData: [END] Returning data for %s with %d timestamps", ticker, len(result["timestamp"]))
	return result, nil
}

// LoadTickerData loads only the columns needed for main window ticker table display
// CRITICAL: Skips profiles_blob to prevent massive memory usage
// Loads: timestamp, spot, zero_gamma, major_pos_vol, major_neg_vol
// Does NOT use query cache (ticker data changes frequently)
// Returns only the latest values (last row) for efficient main window display
func (dl *DataLoader) LoadTickerData(ticker string, date time.Time) (map[string]interface{}, error) {
	dateStr := date.Format("2006-01-02")

	dbPath := dl.getDBPath(ticker, date)
	dl.logf("loader", "LoadTickerData: Checking database path for %s on %s: %s", ticker, dateStr, dbPath)

	// Check if file exists - return empty data if it doesn't
	fileInfo, err := os.Stat(dbPath)
	if os.IsNotExist(err) {
		dl.logf("loader", "LoadTickerData: Database file does not exist for %s: %s", ticker, dbPath)
		emptyData := make(map[string]interface{})
		emptyData["timestamp"] = []interface{}{}
		emptyData["spot"] = []interface{}{}
		emptyData["zero_gamma"] = []interface{}{}
		emptyData["major_pos_vol"] = []interface{}{}
		emptyData["major_neg_vol"] = []interface{}{}
		return emptyData, nil
	}
	if err != nil {
		dl.logf("error", "LoadTickerData: Error checking file existence for %s: %v", ticker, err)
		return nil, fmt.Errorf("failed to check file existence: %w", err)
	}
	dl.logf("loader", "LoadTickerData: Database file exists for %s: %s (size: %d bytes)", ticker, dbPath, fileInfo.Size())
	dl.migrateFile(dbPath)

	// Get connection
	db, err := dl.pool.GetConnection(dbPath, true) // Read-only
	if err != nil {
		dl.logf("error", "LoadTickerData: Failed to get connection for %s: %v", ticker, err)
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	dl.logf("loader", "LoadTickerData: Got database connection for %s", ticker)

	// No checkpoint needed here: WAL readers always see committed data
	// (each read starts a fresh snapshot of the latest committed state)

	// Only load columns needed for main window (explicitly exclude profiles_blob)
	requiredColumns := []string{
		"timestamp",
		"spot",
		"zero_gamma",
		"major_pos_vol", // Positive gamma
		"major_neg_vol", // Negative gamma
	}

	existingColumns, err := dl.tableColumnsCached(db, dbPath, requiredColumns)
	if err != nil {
		dl.logf("error", "LoadTickerData: Failed to get existing columns for %s: %v", ticker, err)
		dl.pool.Discard(dbPath)
		return nil, fmt.Errorf("failed to get existing columns: %w", err)
	}

	// Filter to only include columns that exist
	existingRequiredColumns := make([]string, 0)
	for _, col := range requiredColumns {
		if existingColumns[col] {
			existingRequiredColumns = append(existingRequiredColumns, col)
		} else {
			dl.logf("loader", "LoadTickerData: Column %s does not exist in table for %s, will return empty array", col, ticker)
		}
	}

	// If no columns exist (or only timestamp), return empty data
	if len(existingRequiredColumns) == 0 || (len(existingRequiredColumns) == 1 && existingRequiredColumns[0] == "timestamp") {
		dl.logf("loader", "LoadTickerData: No required columns exist in table for %s, returning empty data", ticker)
		emptyData := make(map[string]interface{})
		for _, col := range requiredColumns {
			emptyData[col] = []interface{}{}
		}
		return emptyData, nil
	}

	// Build SELECT statement with only existing required columns, ordered by timestamp DESC, limit 1
	// This gets the latest row efficiently
	selectCols := strings.Join(existingRequiredColumns, ", ")
	query := fmt.Sprintf("SELECT %s FROM ticker_data ORDER BY timestamp DESC LIMIT 1", selectCols)
	dl.logf("loader", "LoadTickerData: Executing query for %s: %s", ticker, query)

	// Query only the latest row
	rows, err := db.Query(query)
	if err != nil {
		dl.pool.Discard(dbPath)
		dl.logf("error", "LoadTickerData: Query failed for %s: %v", ticker, err)
		// Check if table exists
		tableCheckQuery := "SELECT name FROM sqlite_master WHERE type='table' AND name='ticker_data'"
		tableRows, tableErr := db.Query(tableCheckQuery)
		if tableErr == nil {
			hasTable := tableRows.Next()
			tableRows.Close()
			if !hasTable {
				dl.logf("error", "LoadTickerData: Table 'ticker_data' does not exist in database for %s", ticker)
				return nil, fmt.Errorf("table 'ticker_data' does not exist: %w", err)
			}
		}
		return nil, fmt.Errorf("failed to query: %w", err)
	}

	// Initialize result map with only required columns
	result := make(map[string]interface{})
	for _, col := range requiredColumns {
		result[col] = []interface{}{}
	}

	// Scan the latest row, then CLOSE rows before any follow-up queries.
	// The connection pool allows one connection per file, so a second query
	// while rows is still open would deadlock waiting for the connection.
	var values []interface{}
	haveRow := rows.Next()
	if haveRow {
		values = make([]interface{}, len(existingRequiredColumns))
		valuePtrs := make([]interface{}, len(existingRequiredColumns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			rows.Close()
			dl.pool.Discard(dbPath)
			dl.logf("error", "LoadTickerData: Failed to scan row for %s: %v", ticker, err)
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
	}
	iterErr := rows.Err()
	rows.Close()
	if iterErr != nil {
		dl.logf("error", "LoadTickerData: Error iterating rows for %s: %v", ticker, iterErr)
		return nil, fmt.Errorf("error iterating rows: %w", iterErr)
	}

	if !haveRow {
		dl.logf("loader", "LoadTickerData: No rows found for %s on %s", ticker, dateStr)
		return result, nil
	}

	// Add to result - only for columns that exist and were queried
	for i, col := range existingRequiredColumns {
		result[col] = []interface{}{values[i]}
	}
	// Missing columns already have empty arrays from initialization above

	// For any field that is NULL or missing, query for last known non-null value.
	// This prevents showing "-" or "No data" when we have historical data available.
	fieldsToCheck := []string{"spot", "zero_gamma", "major_pos_vol", "major_neg_vol"}

	for _, fieldName := range fieldsToCheck {
		// Find the index of this field in the queried columns
		fieldIdx := -1
		for i, col := range existingRequiredColumns {
			if col == fieldName {
				fieldIdx = i
				break
			}
		}
		if fieldIdx < 0 {
			continue
		}

		fieldVal := values[fieldIdx]
		// NULL falls back to the last stored value. A stored 0 is real, including
		// zero_gamma, and the main window shows it the same way the chart does.
		if fieldVal != nil {
			continue
		}

		// Query for last non-null value for this field
		fieldCol := sanitizeFieldName(fieldName)
		fallbackQuery := fmt.Sprintf("SELECT %s FROM ticker_data WHERE %s IS NOT NULL ORDER BY timestamp DESC LIMIT 1",
			fieldCol, fieldCol)

		var lastKnownValue float64
		err := db.QueryRow(fallbackQuery).Scan(&lastKnownValue)
		switch {
		case err == nil:
			result[fieldName] = []interface{}{lastKnownValue}
			dl.logf("app", "LoadTickerData: Found last known %s for %s: %.2f", fieldName, ticker, lastKnownValue)
		case err == sql.ErrNoRows:
			dl.logf("app", "LoadTickerData: No known %s found in database for %s", fieldName, ticker)
		default:
			dl.logf("error", "LoadTickerData: Error querying for last known %s for %s: %v", fieldName, ticker, err)
		}
	}

	dl.logf("loader", "LoadTickerData: Successfully loaded latest row for %s on %s (skipped profiles_blob)", ticker, dateStr)
	return result, nil
}

func chartColumns() []string {
	return []string{
		"timestamp",
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
}

func emptyChartSeries() map[string]ChartSeries {
	cols := chartColumns()
	out := make(map[string]ChartSeries, len(cols))
	for _, col := range cols {
		out[col] = ChartSeries{}
	}
	return out
}

// tableColumnsCached returns column names for a database file.
// The list is remembered once it covers every column the caller needs.
func (dl *DataLoader) tableColumnsCached(db *sql.DB, path string, needed []string) (map[string]bool, error) {
	dl.colMu.RLock()
	cols, ok := dl.columnCache[path]
	dl.colMu.RUnlock()
	if ok && columnsCover(cols, needed) {
		return cols, nil
	}

	cols, err := tableColumns(db)
	if err != nil {
		return nil, err
	}
	if columnsCover(cols, needed) {
		dl.colMu.Lock()
		if dl.columnCache == nil {
			dl.columnCache = make(map[string]map[string]bool)
		}
		dl.columnCache[path] = cols
		dl.colMu.Unlock()
	}
	return cols, nil
}

func columnsCover(cols map[string]bool, needed []string) bool {
	for _, name := range needed {
		if !cols[name] {
			return false
		}
	}
	return true
}

// migrateFile drops the redundant timestamp indexes the first time a file is opened.
func (dl *DataLoader) migrateFile(path string) {
	dl.colMu.Lock()
	if dl.migrated == nil {
		dl.migrated = make(map[string]struct{})
	}
	if _, done := dl.migrated[path]; done {
		dl.colMu.Unlock()
		return
	}
	dl.colMu.Unlock()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.ExecContext(ctx, "PRAGMA busy_timeout=10000")
	if _, err = conn.ExecContext(ctx, `DROP INDEX IF EXISTS idx_timestamp_desc`); err != nil {
		return
	}
	if _, err = conn.ExecContext(ctx, `DROP INDEX IF EXISTS idx_timestamp_asc`); err != nil {
		return
	}

	dl.colMu.Lock()
	dl.migrated[path] = struct{}{}
	dl.colMu.Unlock()
}

// ProfileSnapshot is one stored sample. Profiles maps an endpoint name to that
// endpoint's raw JSON. Chart loads do not use this.
type ProfileSnapshot struct {
	Timestamp float64                    `json:"timestamp"`
	Spot      *float64                   `json:"spot,omitempty"`
	ZeroGamma *float64                   `json:"zero_gamma,omitempty"`
	Profiles  map[string]json.RawMessage `json:"profiles"`
	Legacy    bool                       `json:"legacy,omitempty"`
	Missing   bool                       `json:"missing,omitempty"`
}

// LoadProfile reads one snapshot. The sibling profile file wins. A namespaced
// profiles_blob is the fallback for rows written before that file existed.
// A ladder stored as deltas is rebuilt from the last full copy, at most
// profileKeyframeEvery rows back.
func (dl *DataLoader) LoadProfile(ticker string, date time.Time, at *float64, endpoints []string) (*ProfileSnapshot, error) {
	empty := &ProfileSnapshot{Profiles: map[string]json.RawMessage{}, Missing: true}
	endpoints = cleanEndpointNames(endpoints)
	dbPath := dl.getDBPath(ticker, date)
	profilePath := ProfileDBPath(dbPath)
	if info, err := os.Stat(profilePath); err == nil && info.Size() > 0 {
		snap, err := dl.loadProfileFile(profilePath, at, endpoints)
		if err != nil {
			return nil, err
		}
		if snap != nil {
			dl.attachChartLevels(snap, dbPath)
			return snap, nil
		}
		if len(endpoints) > 0 {
			return &ProfileSnapshot{Profiles: map[string]json.RawMessage{}}, nil
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to check profile file: %w", err)
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return empty, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to check file existence: %w", err)
	}

	db, err := dl.pool.GetConnection(dbPath, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}

	cols, err := tableColumns(db)
	if err != nil {
		dl.pool.Discard(dbPath)
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}
	if !cols["profiles_blob"] {
		return empty, nil
	}

	selectCols := []string{"timestamp", "profiles_blob"}
	if cols["spot"] {
		selectCols = append(selectCols, "spot")
	}
	if cols["zero_gamma"] {
		selectCols = append(selectCols, "zero_gamma")
	}
	query := "SELECT " + strings.Join(selectCols, ", ") +
		" FROM ticker_data WHERE profiles_blob IS NOT NULL AND length(profiles_blob) > 0"
	args := []interface{}{}
	if at != nil {
		query += " AND timestamp <= ?"
		args = append(args, *at)
	}
	query += " ORDER BY timestamp DESC LIMIT 1"

	row := db.QueryRow(query, args...)
	var ts sql.NullFloat64
	var blob []byte
	var spot, zeroGamma sql.NullFloat64
	dest := []interface{}{&ts, &blob}
	if cols["spot"] {
		dest = append(dest, &spot)
	}
	if cols["zero_gamma"] {
		dest = append(dest, &zeroGamma)
	}
	if err := row.Scan(dest...); err != nil {
		if err == sql.ErrNoRows {
			return empty, nil
		}
		dl.pool.Discard(dbPath)
		return nil, fmt.Errorf("failed to read profile: %w", err)
	}

	zr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("failed to decompress profile: %w", err)
	}
	defer zr.Close()
	var profiles map[string]json.RawMessage
	if err := json.NewDecoder(zr).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("failed to decode profile: %w", err)
	}
	if profiles == nil {
		profiles = map[string]json.RawMessage{}
	}
	legacy := profilesAreLegacy(profiles)
	if len(endpoints) > 0 && !legacy {
		profiles = pickProfiles(profiles, endpoints)
	}

	snap := &ProfileSnapshot{
		Profiles: profiles,
		Legacy:   legacy,
	}
	if ts.Valid {
		snap.Timestamp = ts.Float64
	}
	if spot.Valid {
		v := spot.Float64
		snap.Spot = &v
	}
	if zeroGamma.Valid {
		v := zeroGamma.Float64
		snap.ZeroGamma = &v
	}
	return snap, nil
}

// loadProfileFile reads each selected endpoint as of at.
// An empty list reads every endpoint in the file.
// Each endpoint keeps its own latest row, then deltas are applied back to the
// last full ladder. A nil snapshot means the file has no rows for the request.
func (dl *DataLoader) loadProfileFile(path string, at *float64, endpoints []string) (*ProfileSnapshot, error) {
	if err := prepareProfileStore(path); err != nil {
		return nil, fmt.Errorf("failed to prepare profile file: %w", err)
	}
	db, err := dl.pool.GetConnection(path, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get profile connection: %w", err)
	}
	names := endpoints
	if len(names) == 0 {
		var listErr error
		names, listErr = listProfileEndpoints(db, at)
		if listErr != nil {
			dl.pool.Discard(path)
			return nil, fmt.Errorf("failed to list profile endpoints: %w", listErr)
		}
	}
	profiles := map[string]json.RawMessage{}
	var ts float64
	seen := false
	for _, name := range names {
		if !endpointNameOK(name) {
			continue
		}
		body, rowTS, _, err := profileAt(db, name, at)
		if err != nil {
			dl.pool.Discard(path)
			return nil, err
		}
		if body == nil {
			continue
		}
		profiles[name] = json.RawMessage(body)
		if !seen || rowTS > ts {
			ts = rowTS
			seen = true
		}
	}
	if len(profiles) == 0 {
		return nil, nil
	}
	return &ProfileSnapshot{
		Timestamp: ts,
		Profiles:  profiles,
		Legacy:    profilesAreLegacy(profiles),
	}, nil
}

func (dl *DataLoader) attachChartLevels(snap *ProfileSnapshot, chartPath string) {
	if _, err := os.Stat(chartPath); err != nil {
		return
	}
	db, err := dl.pool.GetConnection(chartPath, true)
	if err != nil {
		return
	}
	cols, err := tableColumns(db)
	if err != nil || !cols["timestamp"] {
		return
	}
	selectCols := []string{}
	if cols["spot"] {
		selectCols = append(selectCols, "spot")
	}
	if cols["zero_gamma"] {
		selectCols = append(selectCols, "zero_gamma")
	}
	if len(selectCols) == 0 {
		return
	}
	var spot, zeroGamma sql.NullFloat64
	dest := []interface{}{}
	if cols["spot"] {
		dest = append(dest, &spot)
	}
	if cols["zero_gamma"] {
		dest = append(dest, &zeroGamma)
	}
	err = db.QueryRow(
		"SELECT "+strings.Join(selectCols, ", ")+" FROM ticker_data WHERE timestamp = ?",
		snap.Timestamp,
	).Scan(dest...)
	if err != nil {
		return
	}
	if spot.Valid {
		v := spot.Float64
		snap.Spot = &v
	}
	if zeroGamma.Valid {
		v := zeroGamma.Float64
		snap.ZeroGamma = &v
	}
}

func gunzipBytes(blob []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func cleanEndpointNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] || !endpointNameOK(name) {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func endpointNameOK(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func pickProfiles(profiles map[string]json.RawMessage, endpoints []string) map[string]json.RawMessage {
	keep := map[string]bool{}
	for _, name := range endpoints {
		keep[name] = true
	}
	out := map[string]json.RawMessage{}
	for name, payload := range profiles {
		if keep[name] {
			out[name] = payload
		}
	}
	return out
}

// profilesAreLegacy reports a blob saved before ladders were stored per endpoint.
// Those rows keep one strikes array at the top instead of an object per endpoint.
func profilesAreLegacy(profiles map[string]json.RawMessage) bool {
	if len(profiles) == 0 {
		return false
	}
	for _, raw := range profiles {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 && trimmed[0] == '{' {
			return false
		}
	}
	return true
}

func (dl *DataLoader) getDBPath(ticker string, date time.Time) string {
	dl.settingsMu.RLock()
	dataDir := ""
	if dl.settings != nil {
		dataDir = dl.settings.DataDirectory
	}
	dl.settingsMu.RUnlock()
	return DBPath(dataDir, ticker, date, dl.debugPrint, "loader")
}

// Close closes all connections
// Ensures WAL files are checkpointed and cleaned up
func (dl *DataLoader) Close() error {
	dl.debugPrint("DataLoader: Closing connection pool", "loader")

	// Close connection pool (this will checkpoint WAL and close all connections)
	if err := dl.pool.Close(); err != nil {
		return fmt.Errorf("failed to close connection pool: %w", err)
	}

	dl.debugPrint("DataLoader: Closed successfully", "loader")
	return nil
}
