package database

// Offline compaction for archived profile files. A file is copied, its ladder
// chains are replayed and downsample-re-encoded to the current format, and the
// result is verified before it is ever swapped over the original. The source is
// never opened for writing.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// TrimReport is the result of compacting one profile file.
type TrimReport struct {
	Source      string
	Dest        string
	SourceBytes int64
	DestBytes   int64
	FillRows    int
	IntegrityOK bool
	Endpoints   []TrimEndpoint
}

// TrimEndpoint is the per-endpoint breakdown of one compaction.
type TrimEndpoint struct {
	Name            string
	SourceRows      int   // gzipped rows read from the source
	Reconstructible int   // rows whose full ladder could be replayed
	Unreadable      int   // rows whose chain was broken and could not be replayed
	KeptRows        int   // rows written after cadence downsampling and dedupe
	DroppedRows     int   // rows discarded by the cadence
	DroppedChanged  int   // dropped rows whose ladder differed from the last kept one
	SourcePayload   int64 // gzipped bytes read
	DestPayload     int64 // gzipped bytes written
	Mismatch        bool  // a kept sample did not reconstruct exactly
	Error           string
}

// TrimProfileFile copies src to dst, replaying each endpoint's ladder chain and
// re-encoding it at cadence seconds. The fill table, when present, is copied
// verbatim. The source is left untouched.
func TrimProfileFile(src, dst string, cadence float64) (*TrimReport, error) {
	if cadence <= 0 {
		cadence = 5.0
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("stat source: %w", err)
	}

	read, err := sql.Open("sqlite", "file:"+src+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open source: %w", err)
	}
	defer read.Close()

	endpoints, err := listProfileEndpoints(read, nil)
	if err != nil {
		return nil, fmt.Errorf("list endpoints: %w", err)
	}

	if dir := filepath.Dir(dst); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create dest dir: %w", err)
		}
	}

	out, err := sql.Open("sqlite", dst+"?_txlock=immediate&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, fmt.Errorf("open dest: %w", err)
	}
	out.SetMaxOpenConns(1)
	defer out.Close()

	if _, err := out.Exec(`
		CREATE TABLE endpoint_profile (
			timestamp REAL,
			endpoint TEXT,
			payload BLOB,
			PRIMARY KEY (timestamp, endpoint)
		) WITHOUT ROWID`); err != nil {
		return nil, fmt.Errorf("create dest table: %w", err)
	}

	report := &TrimReport{Source: src, Dest: dst, SourceBytes: srcInfo.Size()}

	// One transaction for the whole file: per-row autocommit would fsync on
	// every ladder, which is far too slow for a 300k-row archive.
	tx, err := out.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin dest transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO endpoint_profile VALUES (?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("prepare dest insert: %w", err)
	}

	sort.Strings(endpoints)
	for _, endpoint := range endpoints {
		report.Endpoints = append(report.Endpoints, trimEndpoint(read, stmt, endpoint, cadence))
	}
	stmt.Close()

	// The fill table is already normalized full ladders; copy it unchanged.
	if tableExists(read, "endpoint_profile_fill") {
		if _, err := tx.Exec(`
			CREATE TABLE IF NOT EXISTS endpoint_profile_fill (
				timestamp REAL NOT NULL,
				endpoint TEXT NOT NULL,
				payload BLOB NOT NULL,
				PRIMARY KEY (timestamp, endpoint)
			) WITHOUT ROWID`); err != nil {
			return nil, fmt.Errorf("create fill table: %w", err)
		}
		fillStmt, err := tx.Prepare(`INSERT INTO endpoint_profile_fill VALUES (?, ?, ?)`)
		if err != nil {
			return nil, fmt.Errorf("prepare fill insert: %w", err)
		}
		rows, err := read.Query(`SELECT timestamp, endpoint, payload FROM endpoint_profile_fill`)
		if err != nil {
			fillStmt.Close()
			return nil, fmt.Errorf("read fill: %w", err)
		}
		for rows.Next() {
			var ts float64
			var endpoint string
			var payload []byte
			if err := rows.Scan(&ts, &endpoint, &payload); err != nil {
				rows.Close()
				fillStmt.Close()
				return nil, fmt.Errorf("scan fill: %w", err)
			}
			if _, err := fillStmt.Exec(ts, endpoint, payload); err != nil {
				rows.Close()
				fillStmt.Close()
				return nil, fmt.Errorf("copy fill: %w", err)
			}
			report.FillRows++
		}
		rows.Close()
		fillStmt.Close()
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit dest: %w", err)
	}

	var integrity string
	if err := out.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return nil, fmt.Errorf("integrity check: %w", err)
	}
	report.IntegrityOK = integrity == "ok"

	if err := out.Close(); err != nil {
		return nil, fmt.Errorf("close dest: %w", err)
	}
	dstInfo, err := os.Stat(dst)
	if err != nil {
		return nil, fmt.Errorf("stat dest: %w", err)
	}
	report.DestBytes = dstInfo.Size()
	return report, nil
}

func trimEndpoint(read *sql.DB, stmt *sql.Stmt, endpoint string, cadence float64) TrimEndpoint {
	ep := TrimEndpoint{Name: endpoint}
	rows, err := read.Query(`SELECT timestamp, payload FROM endpoint_profile WHERE endpoint = ? ORDER BY timestamp ASC`, endpoint)
	if err != nil {
		ep.Error = err.Error()
		return ep
	}
	defer rows.Close()

	type snap struct {
		ts  float64
		raw []byte
	}
	var snaps []snap
	var cur []byte
	for rows.Next() {
		var ts float64
		var payload []byte
		if err := rows.Scan(&ts, &payload); err != nil {
			ep.Error = err.Error()
			return ep
		}
		ep.SourceRows++
		ep.SourcePayload += int64(len(payload))
		raw, err := gunzipBytes(payload)
		if err != nil {
			ep.Unreadable++
			cur = nil
			continue
		}
		if isProfileDelta(raw) {
			if cur == nil {
				ep.Unreadable++
				continue
			}
			merged, err := applyProfileDelta(cur, raw)
			if err != nil {
				ep.Unreadable++
				cur = nil
				continue
			}
			cur = merged
		} else {
			cur = raw
		}
		snaps = append(snaps, snap{ts: ts, raw: append([]byte(nil), cur...)})
	}
	ep.Reconstructible = len(snaps)

	var prev, replay []byte
	chain := 0
	lastKept := -1e18
	var lastKeptRaw []byte
	for _, s := range snaps {
		if s.ts-lastKept < cadence-profileSampleSlack {
			ep.DroppedRows++
			if lastKeptRaw != nil && !bytes.Equal(compact(lastKeptRaw), compact(s.raw)) {
				ep.DroppedChanged++
			}
			continue
		}
		next := normalizeProfile(s.raw)
		stored, nextChain, skip, err := encodeProfile(prev, chain, next)
		if err != nil {
			ep.Error = err.Error()
			return ep
		}
		prev, chain = next, nextChain

		// Replay what we just encoded and confirm it reconstructs the
		// normalized snapshot exactly. This proves the rewrite is lossless.
		if stored != nil {
			if isProfileDelta(stored) {
				merged, err := applyProfileDelta(replay, stored)
				if err != nil {
					ep.Mismatch = true
				} else {
					replay = merged
				}
			} else {
				replay = stored
			}
		}
		if !bytes.Equal(stripProfileMeta(replay), stripProfileMeta(next)) {
			ep.Mismatch = true
		}

		lastKept = s.ts
		lastKeptRaw = s.raw
		if skip {
			continue
		}
		payload, err := gzipJSON(json.RawMessage(stored))
		if err != nil {
			ep.Error = err.Error()
			return ep
		}
		if _, err := stmt.Exec(s.ts, endpoint, payload); err != nil {
			ep.Error = err.Error()
			return ep
		}
		ep.KeptRows++
		ep.DestPayload += int64(len(payload))
	}
	return ep
}

func tableExists(db *sql.DB, name string) bool {
	var got string
	return db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, name).Scan(&got) == nil
}
