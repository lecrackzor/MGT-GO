package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// endpoint_profile_fill holds full ladder snapshots recovered from the intraday
// history route and the end-of-day archive. It is strictly additive: no live
// row is rewritten and no recovered sample is inserted into a delta chain, so a
// recovered timestamp can never change how an existing delta resolves.
func ensureProfileFillTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS endpoint_profile_fill (
			timestamp REAL NOT NULL,
			endpoint TEXT NOT NULL,
			payload BLOB NOT NULL,
			PRIMARY KEY (timestamp, endpoint)
		) WITHOUT ROWID
	`)
	return err
}

// profileFillAt returns the newest recovered snapshot for an endpoint at or
// before at (nil means the newest one). A file without the table answers nil.
func profileFillAt(db profileQuery, endpoint string, at *float64) ([]byte, float64, error) {
	query := `SELECT timestamp, payload FROM endpoint_profile_fill WHERE endpoint = ?`
	args := []interface{}{endpoint}
	if at != nil {
		query += ` AND timestamp <= ?`
		args = append(args, *at)
	}
	query += ` ORDER BY timestamp DESC LIMIT 1`

	var ts float64
	var payload []byte
	err := db.QueryRow(query, args...).Scan(&ts, &payload)
	if err == sql.ErrNoRows {
		return nil, 0, nil
	}
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	raw, err := gunzipBytes(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("filled profile %s at %v: %w", endpoint, ts, err)
	}
	return raw, ts, nil
}

// profileFillEndpoints lists endpoints that only recovered snapshots can serve,
// such as a day the app did not run.
func profileFillEndpoints(db *sql.DB, at *float64) ([]string, error) {
	query := `SELECT DISTINCT endpoint FROM endpoint_profile_fill`
	var args []interface{}
	if at != nil {
		query += ` WHERE timestamp <= ?`
		args = append(args, *at)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// LadderFill is one full ladder snapshot recovered for a timestamp the day does
// not already hold.
type LadderFill struct {
	Timestamp float64
	Endpoint  string
	Payload   []byte
}

// WriteFillLadders writes recovered snapshots into the additive fill table.
// They are normalised to the current storage shape and gzipped like live rows.
// maxBytes caps how much one ticker-day may add (0 means no cap).
func (dw *DataWriter) WriteFillLadders(ticker string, date time.Time, fills []LadderFill, maxBytes int) (int, int64, error) {
	if len(fills) == 0 {
		return 0, 0, nil
	}
	path := ProfileDBPath(dw.getDBPath(ticker, date))
	db, err := dw.pool.GetConnection(path, false)
	if err != nil {
		dw.pool.Discard(path)
		return 0, 0, fmt.Errorf("failed to open profile database: %w", err)
	}
	if err := ensureProfileFillTable(db); err != nil {
		dw.pool.Discard(path)
		return 0, 0, fmt.Errorf("failed to ensure fill table: %w", err)
	}
	markProfileStoreReady(path)

	stmt, err := db.Prepare(`
		INSERT INTO endpoint_profile_fill (timestamp, endpoint, payload) VALUES (?, ?, ?)
		ON CONFLICT(timestamp, endpoint) DO NOTHING
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to prepare fill insert: %w", err)
	}
	defer stmt.Close()

	written := 0
	var bytesWritten int64
	for _, fill := range fills {
		normal := normalizeProfile(fill.Payload)
		if !hasStrikeLadder(normal) {
			continue
		}
		payload, err := gzipJSON(json.RawMessage(normal))
		if err != nil {
			return written, bytesWritten, err
		}
		if maxBytes > 0 && bytesWritten+int64(len(payload)) > int64(maxBytes)*1024*1024 {
			break
		}
		result, err := stmt.Exec(fill.Timestamp, fill.Endpoint, payload)
		if err != nil {
			return written, bytesWritten, fmt.Errorf("failed to insert filled ladder %s: %w", fill.Endpoint, err)
		}
		if rows, err := result.RowsAffected(); err == nil && rows > 0 {
			written++
			bytesWritten += int64(len(payload))
		}
	}
	return written, bytesWritten, nil
}
