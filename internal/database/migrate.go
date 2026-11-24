package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
)

// MigrateReport summarizes one legacy chart-database migration.
type MigrateReport struct {
	Path        string
	Skipped     string // reason, non-empty when nothing was migrated
	BlobRows    int
	Endpoints   int
	LadderRows  int
	BackupPath  string
	ProfilePath string
}

// legacyBlobEndpoints decodes one profiles_blob into its endpoint map. The
// values are kept as raw JSON so a re-encode is byte-exact.
func legacyBlobEndpoints(blob []byte) (map[string]json.RawMessage, error) {
	raw, err := gunzipBytes(blob)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("blob is not a JSON object: %w", err)
	}
	return m, nil
}

// InspectLegacyChart reports whether chartPath carries migratable legacy
// profile data, without modifying anything. A non-empty Skipped means it is
// either already clean or cannot be migrated (flat legacy format).
func InspectLegacyChart(chartPath string) (*MigrateReport, error) {
	rep, db, err := openAndInspect(chartPath)
	if err != nil {
		return rep, err
	}
	if db != nil {
		db.Close()
	}
	return rep, nil
}

// MigrateLegacyChart moves a legacy profiles_blob column into the sibling
// .profiles.db and drops the column from the chart database. The original
// chart file is copied to chartPath+".pre-migrate" first and is never removed,
// so a bad conversion can be rolled back. A flat (pre-namespaced) blob has no
// per-endpoint names and is left untouched.
func MigrateLegacyChart(chartPath string) (*MigrateReport, error) {
	rep, db, err := openAndInspect(chartPath)
	if err != nil {
		return rep, err
	}
	if db == nil || rep.Skipped != "" {
		if db != nil {
			db.Close()
		}
		return rep, nil
	}
	defer db.Close()

	// Write the sibling profile file to a temp path, then verify before any
	// change to the chart database.
	profilePath := ProfileDBPath(chartPath)
	profileTmp := profilePath + ".migrating"
	if err := migrateBlobsToProfileFile(db, profileTmp); err != nil {
		_ = os.Remove(profileTmp)
		return rep, err
	}

	endpoints, ladderRows, err := verifyProfileFile(db, profileTmp, rep.BlobRows)
	if err != nil {
		_ = os.Remove(profileTmp)
		return rep, fmt.Errorf("verification failed, chart database untouched: %w", err)
	}

	// Backup, then drop the column.
	backupPath := chartPath + ".pre-migrate"
	if err := copyFile(chartPath, backupPath); err != nil {
		_ = os.Remove(profileTmp)
		return rep, fmt.Errorf("backup: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE ticker_data DROP COLUMN profiles_blob`); err != nil {
		_ = os.Remove(profileTmp)
		return rep, fmt.Errorf("drop column: %w", err)
	}

	if err := os.Rename(profileTmp, profilePath); err != nil {
		return rep, fmt.Errorf("install profile file: %w", err)
	}

	rep.Endpoints = endpoints
	rep.LadderRows = ladderRows
	rep.BackupPath = backupPath
	rep.ProfilePath = profilePath
	return rep, nil
}

// openAndInspect opens the chart database and fills the detection fields of the
// report. A nil database is returned with a Skipped reason when there is
// nothing to do; a non-nil database with an empty Skipped means it is
// migratable and the caller owns the handle.
func openAndInspect(chartPath string) (*MigrateReport, *sql.DB, error) {
	rep := &MigrateReport{Path: chartPath}

	db, err := sql.Open("sqlite", chartPath)
	if err != nil {
		return rep, nil, err
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		db.Close()
		return rep, nil, fmt.Errorf("checkpoint: %w", err)
	}

	var blobCols int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('ticker_data') WHERE name = 'profiles_blob'`).Scan(&blobCols); err != nil {
		db.Close()
		return rep, nil, fmt.Errorf("read schema: %w", err)
	}
	if blobCols == 0 {
		rep.Skipped = "no profiles_blob column"
		return rep, db, nil
	}

	if err := db.QueryRow(`SELECT count(*) FROM ticker_data WHERE profiles_blob IS NOT NULL AND length(profiles_blob) > 0`).Scan(&rep.BlobRows); err != nil {
		db.Close()
		return rep, nil, fmt.Errorf("count blob rows: %w", err)
	}
	if rep.BlobRows == 0 {
		rep.Skipped = "profiles_blob column is empty"
		return rep, db, nil
	}

	profilePath := ProfileDBPath(chartPath)
	if _, err := os.Stat(profilePath); err == nil {
		rep.Skipped = "sibling profile file already exists"
		return rep, db, nil
	}

	// Detect the flat (pre-namespaced) format before touching anything.
	var firstBlob []byte
	if err := db.QueryRow(`SELECT profiles_blob FROM ticker_data WHERE profiles_blob IS NOT NULL AND length(profiles_blob) > 0 ORDER BY timestamp LIMIT 1`).Scan(&firstBlob); err != nil {
		db.Close()
		return rep, nil, fmt.Errorf("sample blob: %w", err)
	}
	firstEndpoints, err := legacyBlobEndpoints(firstBlob)
	if err != nil {
		db.Close()
		return rep, nil, err
	}
	if profilesAreLegacy(firstEndpoints) {
		rep.Skipped = "flat legacy format (no per-endpoint names)"
		return rep, db, nil
	}

	return rep, db, nil
}

// migrateBlobsToProfileFile streams every blob row into the endpoint_profile
// table, writing each endpoint's raw value byte-for-byte.
func migrateBlobsToProfileFile(db *sql.DB, profilePath string) error {
	out, err := sql.Open("sqlite", profilePath)
	if err != nil {
		return err
	}
	defer out.Close()
	if err := ensureProfileTable(out); err != nil {
		return err
	}

	rows, err := db.Query(`SELECT timestamp, profiles_blob FROM ticker_data WHERE profiles_blob IS NOT NULL AND length(profiles_blob) > 0 ORDER BY timestamp`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tx, err := out.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO endpoint_profile (timestamp, endpoint, payload) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rows.Next() {
		var ts float64
		var blob []byte
		if err := rows.Scan(&ts, &blob); err != nil {
			return err
		}
		endpoints, err := legacyBlobEndpoints(blob)
		if err != nil {
			return err
		}
		for name, raw := range endpoints {
			if !endpointNameOK(name) {
				continue
			}
			payload, err := gzipJSON(json.RawMessage(raw))
			if err != nil {
				return err
			}
			if _, err := stmt.Exec(ts, name, payload); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

// verifyProfileFile confirms the sibling file holds exactly the same
// (timestamp, endpoint, value) triples as the source blobs, byte for byte.
func verifyProfileFile(src *sql.DB, profilePath string, blobRows int) (endpoints, ladderRows int, err error) {
	out, err := sql.Open("sqlite", profilePath)
	if err != nil {
		return 0, 0, err
	}
	defer out.Close()

	rows, err := src.Query(`SELECT timestamp, profiles_blob FROM ticker_data WHERE profiles_blob IS NOT NULL AND length(profiles_blob) > 0 ORDER BY timestamp`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	want := 0
	for rows.Next() {
		var ts float64
		var blob []byte
		if err := rows.Scan(&ts, &blob); err != nil {
			return 0, 0, err
		}
		endpoints, err := legacyBlobEndpoints(blob)
		if err != nil {
			return 0, 0, err
		}
		for name, raw := range endpoints {
			if !endpointNameOK(name) {
				continue
			}
			want++
			var payload []byte
			err := out.QueryRow(`SELECT payload FROM endpoint_profile WHERE timestamp = ? AND endpoint = ?`, ts, name).Scan(&payload)
			if err != nil {
				return 0, 0, fmt.Errorf("row (%v, %s): %w", ts, name, err)
			}
			got, err := gunzipBytes(payload)
			if err != nil {
				return 0, 0, fmt.Errorf("row (%v, %s) decode: %w", ts, name, err)
			}
			if string(got) != string(raw) {
				return 0, 0, fmt.Errorf("row (%v, %s) mismatch", ts, name)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	var distinct int
	if err := out.QueryRow(`SELECT count(DISTINCT endpoint) FROM endpoint_profile`).Scan(&distinct); err != nil {
		return 0, 0, err
	}
	var total int
	if err := out.QueryRow(`SELECT count(*) FROM endpoint_profile`).Scan(&total); err != nil {
		return 0, 0, err
	}
	if total != want {
		return 0, 0, fmt.Errorf("profile rows = %d, want %d", total, want)
	}
	return distinct, total, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := out.ReadFrom(in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
