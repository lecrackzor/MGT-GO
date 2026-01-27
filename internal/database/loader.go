package database

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

// DataLoader handles loading data from SQLite databases
type DataLoader struct {
	pool       *ConnectionPool
	settings   *config.Settings
	debugPrint func(string, string)
	queryCache *QueryCache // Query result cache (5-second TTL, 50 query limit)
}

// getExistingColumns returns a map of existing column names in the ticker_data table
func (dl *DataLoader) getExistingColumns(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query("SELECT name FROM pragma_table_info('ticker_data')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns[name] = true
	}

	return columns, rows.Err()
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
		queryCache: NewQueryCache(50, 5.0), // 50 query limit, 5-second TTL (matches Python)
	}
}

// LoadChartData loads only the columns needed for chart display
// CRITICAL: Skips profiles_blob to prevent massive memory usage (28GB+ issue)
// Loads: timestamp, spot, zero_gamma, major_pos_vol, major_neg_vol, major_long_gamma, major_short_gamma,
//        major_positive, major_negative, major_pos_oi, major_neg_oi
// Does NOT use query cache (chart data changes frequently)
func (dl *DataLoader) LoadChartData(ticker string, date time.Time, maxRows int) (map[string][]interface{}, error) {
	dateStr := date.Format("2006-01-02")
	
	dbPath := dl.getDBPath(ticker, date)
	dl.debugPrint(fmt.Sprintf("LoadChartData: [START] Loading chart data for %s on %s (maxRows=%d)", ticker, dateStr, maxRows), "loader")
	dl.debugPrint(fmt.Sprintf("LoadChartData: Checking database path for %s on %s: %s", ticker, dateStr, dbPath), "loader")

	// Check if file exists - return empty data if it doesn't
	fileInfo, err := os.Stat(dbPath)
	if os.IsNotExist(err) {
		dl.debugPrint(fmt.Sprintf("LoadChartData: Database file does not exist for %s: %s", ticker, dbPath), "loader")
		emptyData := make(map[string][]interface{})
		emptyData["timestamp"] = []interface{}{}
		emptyData["spot"] = []interface{}{}
		emptyData["zero_gamma"] = []interface{}{}
		emptyData["major_pos_vol"] = []interface{}{}
		emptyData["major_neg_vol"] = []interface{}{}
		emptyData["major_long_gamma"] = []interface{}{}
		emptyData["major_short_gamma"] = []interface{}{}
		emptyData["major_positive"] = []interface{}{}
		emptyData["major_negative"] = []interface{}{}
		emptyData["major_pos_oi"] = []interface{}{}
		emptyData["major_neg_oi"] = []interface{}{}
		return emptyData, nil
	}
	if err != nil {
		dl.debugPrint(fmt.Sprintf("LoadChartData: Error checking file existence for %s: %v", ticker, err), "error")
		return nil, fmt.Errorf("failed to check file existence: %w", err)
	}
	dl.debugPrint(fmt.Sprintf("LoadChartData: Database file exists for %s: %s (size: %d bytes)", ticker, dbPath, fileInfo.Size()), "loader")

	// Get connection
	db, err := dl.pool.GetConnection(dbPath, true) // Read-only
	if err != nil {
		dl.debugPrint(fmt.Sprintf("LoadChartData: Failed to get connection for %s: %v", ticker, err), "error")
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	dl.debugPrint(fmt.Sprintf("LoadChartData: Got database connection for %s", ticker), "loader")

	// Only load columns needed for charts (explicitly exclude profiles_blob)
	requiredColumns := []string{
		"timestamp",
		"spot",
		"zero_gamma",
		"major_pos_vol",    // Positive gamma
		"major_neg_vol",    // Negative gamma
		"major_long_gamma", // Long gamma
		"major_short_gamma", // Short gamma
		"major_positive",   // Major positive strike
		"major_negative",   // Major negative strike
		"major_pos_oi",     // Major positive OI
		"major_neg_oi",     // Major negative OI
	}
	
	// Check which columns actually exist in the table
	existingColumns, err := dl.getExistingColumns(db)
	if err != nil {
		dl.debugPrint(fmt.Sprintf("LoadChartData: Failed to get existing columns for %s: %v", ticker, err), "error")
		return nil, fmt.Errorf("failed to get existing columns: %w", err)
	}
	
	// Filter to only include columns that exist
	existingRequiredColumns := make([]string, 0)
	for _, col := range requiredColumns {
		if existingColumns[col] {
			existingRequiredColumns = append(existingRequiredColumns, col)
		} else {
			dl.debugPrint(fmt.Sprintf("LoadChartData: Column %s does not exist in table for %s, will return empty array", col, ticker), "loader")
		}
	}
	
	// If no columns exist (or only timestamp), return empty data
	if len(existingRequiredColumns) == 0 || (len(existingRequiredColumns) == 1 && existingRequiredColumns[0] == "timestamp") {
		dl.debugPrint(fmt.Sprintf("LoadChartData: No required columns exist in table for %s, returning empty data", ticker), "loader")
		emptyData := make(map[string][]interface{})
		for _, col := range requiredColumns {
			emptyData[col] = []interface{}{}
		}
		return emptyData, nil
	}
	
	// Build SELECT statement with only existing required columns
	// NOTE: Embed limit directly in query string (modernc.org/sqlite may not handle LIMIT ? correctly)
	selectCols := strings.Join(existingRequiredColumns, ", ")
	query := fmt.Sprintf("SELECT %s FROM ticker_data ORDER BY timestamp ASC LIMIT %d", selectCols, maxRows)
	dl.debugPrint(fmt.Sprintf("LoadChartData: Executing query for %s: %s", ticker, query), "loader")

	// Query data with row limit (embedded in query string)
	rows, err := db.Query(query)
	if err != nil {
		dl.debugPrint(fmt.Sprintf("LoadChartData: Query failed for %s: %v", ticker, err), "error")
		// Check if table exists
		tableCheckQuery := "SELECT name FROM sqlite_master WHERE type='table' AND name='ticker_data'"
		tableRows, tableErr := db.Query(tableCheckQuery)
		if tableErr == nil {
			hasTable := tableRows.Next()
			tableRows.Close()
			if !hasTable {
				dl.debugPrint(fmt.Sprintf("LoadChartData: Table 'ticker_data' does not exist in database for %s", ticker), "error")
				return nil, fmt.Errorf("table 'ticker_data' does not exist: %w", err)
			}
		}
		return nil, fmt.Errorf("failed to query: %w", err)
	}
	defer rows.Close()
	dl.debugPrint(fmt.Sprintf("LoadChartData: Query executed successfully for %s, scanning rows...", ticker), "loader")

	// Initialize result map with all required columns (including missing ones as empty arrays)
	result := make(map[string][]interface{})
	for _, col := range requiredColumns {
		result[col] = make([]interface{}, 0)
	}

	// Scan rows
	rowCount := 0
	for rows.Next() {
		// Create slice for row values (only existing columns that we're querying)
		values := make([]interface{}, len(existingRequiredColumns))
		valuePtrs := make([]interface{}, len(existingRequiredColumns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Add to result - only for columns that exist and were queried
		for i, col := range existingRequiredColumns {
			result[col] = append(result[col], values[i])
		}
		// Missing columns already have empty arrays from initialization above
		rowCount++
	}

	if err := rows.Err(); err != nil {
		dl.debugPrint(fmt.Sprintf("LoadChartData: Error iterating rows for %s: %v", ticker, err), "error")
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	if rowCount == 0 {
		dl.debugPrint(fmt.Sprintf("LoadChartData: WARNING - Query returned 0 rows for %s on %s (table exists but is empty)", ticker, dateStr), "loader")
	} else {
		dl.debugPrint(fmt.Sprintf("LoadChartData: Successfully loaded %d rows for %s on %s (skipped profiles_blob)", rowCount, ticker, dateStr), "loader")
		// Log timestamp count for debugging
		if timestamps, ok := result["timestamp"]; ok {
			dl.debugPrint(fmt.Sprintf("LoadChartData: Result contains %d timestamps for %s", len(timestamps), ticker), "loader")
		} else {
			dl.debugPrint(fmt.Sprintf("LoadChartData: WARNING - Result does not contain 'timestamp' key for %s", ticker), "error")
		}
	}
	
	dl.debugPrint(fmt.Sprintf("LoadChartData: [END] Returning data for %s with %d timestamps", ticker, len(result["timestamp"])), "loader")
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
	dl.debugPrint(fmt.Sprintf("LoadTickerData: Checking database path for %s on %s: %s", ticker, dateStr, dbPath), "loader")

	// Check if file exists - return empty data if it doesn't
	fileInfo, err := os.Stat(dbPath)
	if os.IsNotExist(err) {
		dl.debugPrint(fmt.Sprintf("LoadTickerData: Database file does not exist for %s: %s", ticker, dbPath), "loader")
		emptyData := make(map[string]interface{})
		emptyData["timestamp"] = []interface{}{}
		emptyData["spot"] = []interface{}{}
		emptyData["zero_gamma"] = []interface{}{}
		emptyData["major_pos_vol"] = []interface{}{}
		emptyData["major_neg_vol"] = []interface{}{}
		return emptyData, nil
	}
	if err != nil {
		dl.debugPrint(fmt.Sprintf("LoadTickerData: Error checking file existence for %s: %v", ticker, err), "error")
		return nil, fmt.Errorf("failed to check file existence: %w", err)
	}
	dl.debugPrint(fmt.Sprintf("LoadTickerData: Database file exists for %s: %s (size: %d bytes)", ticker, dbPath, fileInfo.Size()), "loader")

	// Get connection
	db, err := dl.pool.GetConnection(dbPath, true) // Read-only
	if err != nil {
		dl.debugPrint(fmt.Sprintf("LoadTickerData: Failed to get connection for %s: %v", ticker, err), "error")
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	dl.debugPrint(fmt.Sprintf("LoadTickerData: Got database connection for %s", ticker), "loader")

	// Force WAL checkpoint to ensure we see latest committed data
	// This ensures read connections see data that was just written and checkpointed
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	conn, err := db.Conn(ctx)
	if err == nil {
		// Passive checkpoint - safe for read-only, ensures we see latest WAL data
		// PASSIVE mode doesn't block writers and makes WAL data visible to this connection
		_, _ = conn.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
		conn.Close()
	}
	cancel()

	// Only load columns needed for main window (explicitly exclude profiles_blob)
	requiredColumns := []string{
		"timestamp",
		"spot",
		"zero_gamma",
		"major_pos_vol",    // Positive gamma
		"major_neg_vol",    // Negative gamma
	}
	
	// Check which columns actually exist in the table
	existingColumns, err := dl.getExistingColumns(db)
	if err != nil {
		dl.debugPrint(fmt.Sprintf("LoadTickerData: Failed to get existing columns for %s: %v", ticker, err), "error")
		return nil, fmt.Errorf("failed to get existing columns: %w", err)
	}
	
	// Filter to only include columns that exist
	existingRequiredColumns := make([]string, 0)
	for _, col := range requiredColumns {
		if existingColumns[col] {
			existingRequiredColumns = append(existingRequiredColumns, col)
		} else {
			dl.debugPrint(fmt.Sprintf("LoadTickerData: Column %s does not exist in table for %s, will return empty array", col, ticker), "loader")
		}
	}
	
	// If no columns exist (or only timestamp), return empty data
	if len(existingRequiredColumns) == 0 || (len(existingRequiredColumns) == 1 && existingRequiredColumns[0] == "timestamp") {
		dl.debugPrint(fmt.Sprintf("LoadTickerData: No required columns exist in table for %s, returning empty data", ticker), "loader")
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
	dl.debugPrint(fmt.Sprintf("LoadTickerData: Executing query for %s: %s", ticker, query), "loader")

	// Query only the latest row
	rows, err := db.Query(query)
	if err != nil {
		dl.debugPrint(fmt.Sprintf("LoadTickerData: Query failed for %s: %v", ticker, err), "error")
		// Check if table exists
		tableCheckQuery := "SELECT name FROM sqlite_master WHERE type='table' AND name='ticker_data'"
		tableRows, tableErr := db.Query(tableCheckQuery)
		if tableErr == nil {
			hasTable := tableRows.Next()
			tableRows.Close()
			if !hasTable {
				dl.debugPrint(fmt.Sprintf("LoadTickerData: Table 'ticker_data' does not exist in database for %s", ticker), "error")
				return nil, fmt.Errorf("table 'ticker_data' does not exist: %w", err)
			}
		}
		return nil, fmt.Errorf("failed to query: %w", err)
	}
	defer rows.Close()
	dl.debugPrint(fmt.Sprintf("LoadTickerData: Query executed successfully for %s, scanning rows...", ticker), "loader")

	// Initialize result map with only required columns
	result := make(map[string]interface{})
	for _, col := range requiredColumns {
		result[col] = []interface{}{}
	}

	// Scan the latest row
	if rows.Next() {
		// Create slice for row values (only existing columns that we're querying)
		values := make([]interface{}, len(existingRequiredColumns))
		valuePtrs := make([]interface{}, len(existingRequiredColumns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			dl.debugPrint(fmt.Sprintf("LoadTickerData: Failed to scan row for %s: %v", ticker, err), "error")
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Add to result - only for columns that exist and were queried
		for i, col := range existingRequiredColumns {
			result[col] = []interface{}{values[i]}
		}
		// Missing columns already have empty arrays from initialization above
		
		// CRITICAL FIX: For any field that is NULL or missing, query for last known non-null value
		// This prevents showing "-" or "No data" when we have historical data available
		// Applies to: spot, zero_gamma, major_pos_vol, major_neg_vol
		fieldsToCheck := []string{"spot", "zero_gamma", "major_pos_vol", "major_neg_vol"}
		
		for _, fieldName := range fieldsToCheck {
			// Find the index of this field in requiredColumns
			fieldIdx := -1
			for i, col := range requiredColumns {
				if col == fieldName {
					fieldIdx = i
					break
				}
			}
			
			if fieldIdx >= 0 {
				fieldVal := values[fieldIdx]
				// Check if field is NULL or missing
				isNullOrMissing := false
				if fieldVal == nil {
					isNullOrMissing = true
				} else if val, ok := fieldVal.(float64); ok && val == 0.0 && fieldName == "zero_gamma" {
					// For zero_gamma specifically, also treat 0.0 as missing (needs fallback)
					isNullOrMissing = true
				}
				
				if isNullOrMissing {
					dl.debugPrint(fmt.Sprintf("LoadTickerData: Latest %s is NULL/missing for %s, querying for last known value", fieldName, ticker), "app")
					// Query for last non-null value for this field
					fieldCol := sanitizeFieldName(fieldName)
					
					// Build query - for zero_gamma, exclude 0.0; for others, just check IS NOT NULL
					var fallbackQuery string
					if fieldName == "zero_gamma" {
						fallbackQuery = fmt.Sprintf("SELECT %s FROM ticker_data WHERE %s IS NOT NULL AND %s != 0.0 ORDER BY timestamp DESC LIMIT 1", 
							fieldCol, fieldCol, fieldCol)
					} else {
						fallbackQuery = fmt.Sprintf("SELECT %s FROM ticker_data WHERE %s IS NOT NULL ORDER BY timestamp DESC LIMIT 1", 
							fieldCol, fieldCol)
					}
					
					fallbackRows, err := db.Query(fallbackQuery)
					if err == nil {
						defer fallbackRows.Close()
						if fallbackRows.Next() {
							var lastKnownValue float64
							if err := fallbackRows.Scan(&lastKnownValue); err == nil {
								result[fieldName] = []interface{}{lastKnownValue}
								dl.debugPrint(fmt.Sprintf("LoadTickerData: Found last known %s for %s: %.2f", fieldName, ticker, lastKnownValue), "app")
							}
						} else {
							dl.debugPrint(fmt.Sprintf("LoadTickerData: No known %s found in database for %s", fieldName, ticker), "app")
						}
					} else {
						dl.debugPrint(fmt.Sprintf("LoadTickerData: Error querying for last known %s for %s: %v", fieldName, ticker, err), "error")
					}
				}
			}
		}
		
		dl.debugPrint(fmt.Sprintf("LoadTickerData: Successfully loaded latest row for %s on %s (skipped profiles_blob)", ticker, dateStr), "loader")
	} else {
		dl.debugPrint(fmt.Sprintf("LoadTickerData: No rows found for %s on %s", ticker, dateStr), "loader")
	}

	if err := rows.Err(); err != nil {
		dl.debugPrint(fmt.Sprintf("LoadTickerData: Error iterating rows for %s: %v", ticker, err), "error")
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}
	
	return result, nil
}

// LoadFromFile loads data from a database file for a ticker and date
// Returns empty data if file doesn't exist (data hasn't been collected yet)
func (dl *DataLoader) LoadFromFile(ticker string, date time.Time) (map[string][]interface{}, error) {
	// Generate cache key
	dateStr := date.Format("2006-01-02")
	cacheKey := GenerateCacheKey(ticker, dateStr, 0, 0)
	
	// Check cache first
	if cached, found := dl.queryCache.Get(cacheKey); found {
		dl.debugPrint(fmt.Sprintf("Cache hit for %s on %s", ticker, dateStr), "loader")
		return cached, nil
	}
	
	dbPath := dl.getDBPath(ticker, date)

	// Check if file exists - return empty data if it doesn't (data not collected yet)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// Return empty data structure instead of error
		// This allows the UI to display "No data" instead of crashing
		dl.debugPrint(fmt.Sprintf("Database file does not exist yet for %s: %s (data collection may not have started)", ticker, dbPath), "loader")
		emptyData := make(map[string][]interface{})
		// Cache empty result (prevents repeated file checks)
		dl.queryCache.Set(cacheKey, emptyData)
		return emptyData, nil
	}

	// Get connection
	db, err := dl.pool.GetConnection(dbPath, true) // Read-only
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}

	// First, get column names
	columnRows, err := db.Query("PRAGMA table_info(ticker_data)")
	if err != nil {
		return nil, fmt.Errorf("failed to get table info: %w", err)
	}

	columns := []string{"timestamp", "profiles_blob"}
	for columnRows.Next() {
		var cid int
		var name, colType string
		var notnull, dfltValue, pk interface{}
		if err := columnRows.Scan(&cid, &name, &colType, &notnull, &dfltValue, &pk); err != nil {
			columnRows.Close()
			return nil, fmt.Errorf("failed to scan column info: %w", err)
		}
		if name != "timestamp" && name != "profiles_blob" {
			columns = append(columns, name)
		}
	}
	columnRows.Close()

	// Build SELECT statement with explicit columns
	selectCols := strings.Join(columns, ", ")
	query := fmt.Sprintf("SELECT %s FROM ticker_data ORDER BY timestamp ASC", selectCols)

	// Query all data
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query: %w", err)
	}
	defer rows.Close()

	// Initialize result map
	result := make(map[string][]interface{})
	for _, col := range columns {
		result[col] = make([]interface{}, 0)
	}

	// Scan rows
	for rows.Next() {
		// Create slice for row values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Extract values
		for i, col := range columns {
			val := values[i]

			// Handle profiles_blob decompression
			if col == "profiles_blob" && val != nil {
				if blob, ok := val.([]byte); ok && len(blob) > 0 {
					// Decompress
					reader, err := gzip.NewReader(bytes.NewReader(blob))
					if err == nil {
						decompressed, err := io.ReadAll(reader)
						reader.Close()
						if err == nil {
							var profiles map[string]interface{}
							if err := json.Unmarshal(decompressed, &profiles); err == nil {
								// Merge profiles into result
								for key, value := range profiles {
									if result[key] == nil {
										result[key] = make([]interface{}, 0)
									}
									result[key] = append(result[key], value)
								}
							}
						}
					}
				}
				continue // Skip adding profiles_blob itself
			}

			// Add to result
			result[col] = append(result[col], val)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	// Cache result
	dl.queryCache.Set(cacheKey, result)
	
	return result, nil
}

// LoadTimeRange loads data within a time range
// Returns empty data if file doesn't exist (data hasn't been collected yet)
func (dl *DataLoader) LoadTimeRange(ticker string, date time.Time, startTime, endTime float64) (map[string][]interface{}, error) {
	// Generate cache key
	dateStr := date.Format("2006-01-02")
	cacheKey := GenerateCacheKey(ticker, dateStr, startTime, endTime)
	
	// Check cache first
	if cached, found := dl.queryCache.Get(cacheKey); found {
		dl.debugPrint(fmt.Sprintf("Cache hit for %s on %s (range: %.3f-%.3f)", ticker, dateStr, startTime, endTime), "loader")
		return cached, nil
	}
	
	dbPath := dl.getDBPath(ticker, date)

	// Check if file exists - return empty data if it doesn't
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// Return empty data structure instead of error
		dl.debugPrint(fmt.Sprintf("Database file does not exist yet for %s: %s", ticker, dbPath), "loader")
		emptyData := make(map[string][]interface{})
		// Cache empty result
		dl.queryCache.Set(cacheKey, emptyData)
		return emptyData, nil
	}

	// Get connection
	db, err := dl.pool.GetConnection(dbPath, true) // Read-only
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}

	// Get column names first
	columnRows, err := db.Query("PRAGMA table_info(ticker_data)")
	if err != nil {
		return nil, fmt.Errorf("failed to get table info: %w", err)
	}

	columns := []string{"timestamp", "profiles_blob"}
	for columnRows.Next() {
		var cid int
		var name, colType string
		var notnull, dfltValue, pk interface{}
		if err := columnRows.Scan(&cid, &name, &colType, &notnull, &dfltValue, &pk); err != nil {
			columnRows.Close()
			return nil, fmt.Errorf("failed to scan column info: %w", err)
		}
		if name != "timestamp" && name != "profiles_blob" {
			columns = append(columns, name)
		}
	}
	columnRows.Close()

	// Build SELECT statement with explicit columns
	selectCols := strings.Join(columns, ", ")
	query := fmt.Sprintf("SELECT %s FROM ticker_data WHERE timestamp >= ? AND timestamp <= ? ORDER BY timestamp ASC", selectCols)

	// Query data in time range
	rows, err := db.Query(query, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to query: %w", err)
	}
	defer rows.Close()

	// Initialize result map
	result := make(map[string][]interface{})
	for _, col := range columns {
		result[col] = make([]interface{}, 0)
	}

	// Scan rows (same as LoadFromFile)
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		for i, col := range columns {
			val := values[i]

			if col == "profiles_blob" && val != nil {
				if blob, ok := val.([]byte); ok && len(blob) > 0 {
					reader, err := gzip.NewReader(bytes.NewReader(blob))
					if err == nil {
						decompressed, err := io.ReadAll(reader)
						reader.Close()
						if err == nil {
							var profiles map[string]interface{}
							if err := json.Unmarshal(decompressed, &profiles); err == nil {
								for key, value := range profiles {
									if result[key] == nil {
										result[key] = make([]interface{}, 0)
									}
									result[key] = append(result[key], value)
								}
							}
						}
					}
				}
				continue
			}

			result[col] = append(result[col], val)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	// Cache result
	dl.queryCache.Set(cacheKey, result)
	
	return result, nil
}

// getDBPath returns the database file path for a ticker and date
// Creates directory if it doesn't exist
// The date passed here is already in ET at midnight (from ParseDateInET or GetMarketDate)
// We only need to handle weekend adjustments if the date is a weekend
func (dl *DataLoader) getDBPath(ticker string, date time.Time) string {
	dataDir := dl.settings.DataDirectory
	if dataDir == "" {
		dataDir = "Tickers"
	}

	// The date passed to this function is already in ET at midnight
	// (from ParseDateInET() which ensures dates are parsed as ET, not UTC)
	// No timezone conversion needed - just handle weekend adjustment if needed
	
	// Only handle weekend adjustment if needed
	var marketDate time.Time
	if utils.IsWeekend(date) {
		marketDate = utils.GetLastTradingDay(date)
		dl.debugPrint(fmt.Sprintf("getDBPath: Weekend detected for %s, using last Friday: %s", 
			date.Format("2006-01-02"), marketDate.Format("2006-01-02")), "loader")
	} else {
		marketDate = date
	}

	// Format date as MM.DD.YYYY (date is already in ET)
	dateStr := marketDate.Format("01.02.2006")
	// Directory format: "Tickers 01.14.2026" (not "Tickers\Tickers 01.14.2026")
	dir := fmt.Sprintf("%s %s", dataDir, dateStr)
	
	// Log directory construction
	dl.debugPrint(fmt.Sprintf("getDBPath: Constructing path for %s on %s (market date: %s): dataDir=%s, dateStr=%s, dir=%s", 
		ticker, date.Format("2006-01-02"), marketDate.Format("2006-01-02"), dataDir, dateStr, dir), "loader")
	
	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		dl.debugPrint(fmt.Sprintf("getDBPath: WARNING - Failed to create directory %s: %v", dir, err), "error")
	}

	dbPath := filepath.Join(dir, fmt.Sprintf("%s.db", ticker))
	dl.debugPrint(fmt.Sprintf("getDBPath: Final database path for %s: %s", ticker, dbPath), "loader")
	
	return dbPath
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
