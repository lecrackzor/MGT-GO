package database

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// legacyChart creates a ticker_data table that still carries the legacy
// profiles_blob column, with two scalar rows.
func legacyChart(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name+".db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE ticker_data (
			timestamp REAL PRIMARY KEY,
			spot REAL,
			zero_gamma REAL,
			profiles_blob BLOB
		) WITHOUT ROWID
	`); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeLegacyBlob(t *testing.T, db *sql.DB, ts float64, endpoints map[string]interface{}) {
	t.Helper()
	blob, err := gzipJSON(endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ticker_data (timestamp, spot, zero_gamma, profiles_blob) VALUES (?, ?, ?, ?)`,
		ts, 6000.0, 5990.0, blob); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateLegacyChartMovesBlobsToSiblingFile(t *testing.T) {
	dir := t.TempDir()
	path := legacyChart(t, dir, "SPX")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	writeLegacyBlob(t, db, 1000, map[string]interface{}{
		"classic_zero": map[string]interface{}{
			"spot":    6000.0,
			"strikes": json.RawMessage(`[[6000,1.0,2.0,[1.0,1.0,1.0,1.0]]]`),
		},
		"classic_one": map[string]interface{}{
			"spot":    6010.0,
			"strikes": json.RawMessage(`[[6010,2.0,3.0,[2.0,2.0,2.0,2.0]]]`),
		},
	})
	writeLegacyBlob(t, db, 1001, map[string]interface{}{
		"classic_zero": map[string]interface{}{
			"spot":    6001.0,
			"strikes": json.RawMessage(`[[6000,1.5,2.5,[1.5,1.5,1.5,1.5]]]`),
		},
	})
	db.Close()

	rep, err := MigrateLegacyChart(path)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped != "" {
		t.Fatalf("unexpected skip: %s", rep.Skipped)
	}
	if rep.BlobRows != 2 || rep.LadderRows != 3 {
		t.Fatalf("report = %+v", rep)
	}

	// The chart DB lost the column but kept its scalar rows.
	cdb, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer cdb.Close()
	var blobCols int
	if err := cdb.QueryRow(`SELECT count(*) FROM pragma_table_info('ticker_data') WHERE name='profiles_blob'`).Scan(&blobCols); err != nil {
		t.Fatal(err)
	}
	if blobCols != 0 {
		t.Fatalf("profiles_blob still present (%d)", blobCols)
	}
	var rows int
	if err := cdb.QueryRow(`SELECT count(*) FROM ticker_data`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("chart rows = %d, want 2", rows)
	}

	// The sibling file has all three endpoint rows.
	pdb, err := sql.Open("sqlite", ProfileDBPath(path))
	if err != nil {
		t.Fatal(err)
	}
	defer pdb.Close()
	var total int
	if err := pdb.QueryRow(`SELECT count(*) FROM endpoint_profile`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("endpoint rows = %d, want 3", total)
	}

	// The backup still holds the original column and blob bytes.
	if _, err := os.Stat(path + ".pre-migrate"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestMigrateLegacyChartSkipsFlatBlob(t *testing.T) {
	dir := t.TempDir()
	path := legacyChart(t, dir, "SPX")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Flat (pre-namespaced) blob: strikes/mini_contracts/max_priors at top.
	blob, err := gzipJSON(map[string]interface{}{
		"strikes":        json.RawMessage(`[[6000,1.0,2.0,[1.0]]]`),
		"mini_contracts": json.RawMessage(`[[6000,0.4,0.5,1.0,[1.0],0,null]]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ticker_data (timestamp, spot, profiles_blob) VALUES (?, ?, ?)`, 1000.0, 6000.0, blob); err != nil {
		t.Fatal(err)
	}
	db.Close()

	rep, err := MigrateLegacyChart(path)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped == "" {
		t.Fatalf("flat blob should be skipped, got %+v", rep)
	}
	if _, err := os.Stat(ProfileDBPath(path)); !os.IsNotExist(err) {
		t.Fatalf("sibling file should not be created for a flat blob")
	}
}

func TestMigrateLegacyChartSkipsEmptyColumn(t *testing.T) {
	dir := t.TempDir()
	path := legacyChart(t, dir, "SPX")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ticker_data (timestamp, spot, profiles_blob) VALUES (?, ?, NULL)`, 1000.0, 6000.0); err != nil {
		t.Fatal(err)
	}
	db.Close()

	rep, err := MigrateLegacyChart(path)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped == "" {
		t.Fatalf("empty column should be skipped, got %+v", rep)
	}
}
