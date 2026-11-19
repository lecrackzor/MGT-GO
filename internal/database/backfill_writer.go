package database

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// BackfillRow is one recovered chart sample. Only the scalar columns are here;
// ladders travel separately because they are stored outside the delta chain.
type BackfillRow struct {
	Timestamp float64
	Scalars   map[string]interface{}
}

// SplitBackfillSample separates one decoded history or archive snapshot the
// same way the live collection pipeline does, so a recovered row lands on the
// same columns a live poll would have written.
func SplitBackfillSample(sample map[string]interface{}) (map[string]interface{}, map[string]interface{}) {
	return splitCollectedFields(sample)
}

// BackfillLadderPayload keeps only the ladder arrays of one recovered snapshot.
// A nil result means the snapshot has no ladder for its endpoint.
func BackfillLadderPayload(profiles map[string]interface{}) []byte {
	obj := make(map[string]json.RawMessage, len(profileLadderKeys))
	for _, key := range profileLadderKeys {
		value, ok := profiles[key]
		if !ok {
			continue
		}
		switch raw := value.(type) {
		case json.RawMessage:
			if len(bytes.TrimSpace(raw)) > 0 {
				obj[key] = raw
			}
		default:
			encoded, err := json.Marshal(value)
			if err == nil && len(encoded) > 0 {
				obj[key] = json.RawMessage(encoded)
			}
		}
	}
	if len(obj) == 0 {
		return nil
	}
	payload := marshalObject(obj)
	if !hasStrikeLadder(payload) {
		return nil
	}
	return payload
}

// WriteBackfillScalars inserts only the timestamps a day does not already have.
// An existing row is never updated: ON CONFLICT DO NOTHING, so live data always
// wins over recovered data.
func (dw *DataWriter) WriteBackfillScalars(ticker string, date time.Time, rows []BackfillRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	path := dw.getDBPath(ticker, date)
	db, err := dw.pool.GetConnection(path, false)
	if err != nil {
		dw.pool.Discard(path)
		return 0, fmt.Errorf("failed to open database: %w", err)
	}

	columns := make([]string, 0)
	seen := map[string]bool{}
	for _, row := range rows {
		for field := range row.Scalars {
			if !seen[field] {
				columns = append(columns, field)
				seen[field] = true
			}
		}
	}
	if err := dw.ensureSchema(db, path, columns); err != nil {
		return 0, fmt.Errorf("failed to ensure schema: %w", err)
	}

	// Short transactions so a long recovery pass never holds the file for the
	// whole download.
	const chunkSize = 5000
	inserted := 0
	for start := 0; start < len(rows); start += chunkSize {
		end := start + chunkSize
		if end > len(rows) {
			end = len(rows)
		}
		written, err := dw.insertBackfillChunk(db, rows[start:end])
		inserted += written
		if err != nil {
			return inserted, err
		}
	}
	return inserted, nil
}

func (dw *DataWriter) insertBackfillChunk(db *sql.DB, rows []BackfillRow) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	groups := make(map[string][]BackfillRow)
	for _, row := range rows {
		if len(row.Scalars) == 0 {
			continue
		}
		signature := strings.Join(sortedKeys(row.Scalars), "\x00")
		groups[signature] = append(groups[signature], row)
	}
	signatures := make([]string, 0, len(groups))
	for signature := range groups {
		signatures = append(signatures, signature)
	}
	sort.Strings(signatures)

	inserted := 0
	for _, signature := range signatures {
		keys := strings.Split(signature, "\x00")
		insertSQL, cleanKeys := insertMissingSQL(keys)
		stmt, err := tx.Prepare(insertSQL)
		if err != nil {
			return inserted, fmt.Errorf("failed to prepare backfill statement: %w", err)
		}
		for _, row := range groups[signature] {
			args := make([]interface{}, 0, len(cleanKeys)+1)
			args = append(args, row.Timestamp)
			for _, field := range cleanKeys {
				args = append(args, row.Scalars[field])
			}
			result, err := stmt.Exec(args...)
			if err != nil {
				stmt.Close()
				return inserted, fmt.Errorf("failed to insert recovered row at %v: %w", row.Timestamp, err)
			}
			if count, err := result.RowsAffected(); err == nil && count > 0 {
				inserted++
			}
		}
		stmt.Close()
	}
	if err := tx.Commit(); err != nil {
		return inserted, fmt.Errorf("failed to commit recovered rows: %w", err)
	}
	return inserted, nil
}

// insertMissingSQL inserts only the named columns and leaves an existing
// timestamp untouched.
func insertMissingSQL(fields []string) (string, []string) {
	columns := []string{"timestamp"}
	placeholders := []string{"?"}
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
		clean = append(clean, field)
	}
	return fmt.Sprintf(
		"INSERT INTO ticker_data (%s) VALUES (%s) ON CONFLICT(timestamp) DO NOTHING",
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
	), clean
}
