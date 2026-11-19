package database

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestTrimProfileFileDownsamplesAndReencodes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "SPY.profiles.db")
	db, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE endpoint_profile (
		timestamp REAL, endpoint TEXT, payload BLOB,
		PRIMARY KEY(timestamp, endpoint)) WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}

	const endpoint = "classic_zero"
	// 12 samples at 1s cadence, one strike drifting each second.
	// encodeProfile writes full ladders and deltas like the live writer.
	var prev []byte
	chain := 0
	insert := func(ts float64, v int) {
		t.Helper()
		next := []byte(fmt.Sprintf(`{"spot":%d,"strikes":[[100,%d,0]]}`, 10+v, v))
		next = normalizeProfile(next)
		stored, nextChain, skip, err := encodeProfile(prev, chain, next)
		if err != nil {
			t.Fatal(err)
		}
		prev, chain = next, nextChain
		if skip {
			return
		}
		payload, err := gzipJSON(json.RawMessage(stored))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO endpoint_profile VALUES (?, ?, ?)`, ts, endpoint, payload); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 12; i++ {
		insert(float64(1000+i), i)
	}
	db.Close()

	dst := filepath.Join(dir, "trimmed.profiles.db")
	report, err := TrimProfileFile(src, dst, 5.0)
	if err != nil {
		t.Fatal(err)
	}
	if !report.IntegrityOK {
		t.Fatalf("integrity = %v", report.IntegrityOK)
	}
	if len(report.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1", len(report.Endpoints))
	}
	ep := report.Endpoints[0]
	if ep.Mismatch {
		t.Fatal("kept sample did not reconstruct exactly")
	}
	if ep.Unreadable != 0 {
		t.Fatalf("unreadable = %d, want 0", ep.Unreadable)
	}
	// 12 samples at 1s, cadence 5s -> first + 1000,1005,1010 = 3 kept rows.
	if ep.KeptRows != 3 {
		t.Fatalf("kept = %d, want 3", ep.KeptRows)
	}
	if ep.DroppedRows != 9 {
		t.Fatalf("dropped = %d, want 9", ep.DroppedRows)
	}

	// The trimmed file must load through the app's own read path.
	read, err := sql.Open("sqlite", "file:"+dst+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	body, ts, _, err := profileAtLive(read, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ts != 1010 {
		t.Fatalf("newest timestamp = %v, want 1010 (last kept sample)", ts)
	}
	if !bytes.Contains(body, []byte(`"spot":20`)) {
		t.Fatalf("last ladder lost its spot value: %s", body)
	}
}
