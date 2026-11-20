package database

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Measures how much a stored ladder shrinks under format v2, using rows from a
// real day folder. Skips in a clean checkout. The number reported is gzip size,
// which is what the file actually holds.
func TestProfileV2StorageReduction(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate the test file")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("no repo root: %v", err)
	}
	type dayFolder struct {
		name string
		date time.Time
	}
	var days []dayFolder
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "Tickers ") {
			continue
		}
		date, parseErr := time.Parse("01.02.2006", strings.TrimPrefix(entry.Name(), "Tickers "))
		if parseErr != nil {
			continue
		}
		days = append(days, dayFolder{name: entry.Name(), date: date})
	}
	if len(days) == 0 {
		t.Skip("no day folders")
	}
	sort.Slice(days, func(i, j int) bool { return days[i].date.After(days[j].date) })

	var before, after int64
	var rows, legacy int
	var fullBefore, fullAfter int64
	var fullRows int
	for _, day := range days {
		path := filepath.Join(root, day.name, "SPY.profiles.db")
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		db, openErr := sql.Open("sqlite", "file:"+path+"?mode=ro")
		if openErr != nil {
			continue
		}
		result, queryErr := db.Query(`
			SELECT payload FROM endpoint_profile
			WHERE payload IS NOT NULL
			ORDER BY timestamp LIMIT 40
		`)
		if queryErr != nil {
			db.Close()
			continue
		}
		for result.Next() {
			var blob []byte
			if result.Scan(&blob) != nil {
				continue
			}
			raw, gunzipErr := gunzipBytes(blob)
			if gunzipErr != nil {
				continue
			}
			oldSize, errOld := gzipJSON(raw)
			newRaw := normalizeProfile(raw)
			newSize, errNew := gzipJSON(newRaw)
			if errOld != nil || errNew != nil {
				continue
			}
			before += int64(len(oldSize))
			after += int64(len(newSize))
			rows++
			if !profileIsCurrent(raw) {
				legacy++
			}
			// A full ladder carries the whole strike list; a delta carries only
			// the strikes that changed and was already small.
			if !bytes.Contains(raw, []byte(`"_d"`)) && bytes.Contains(raw, []byte(`"strikes"`)) {
				fullBefore += int64(len(oldSize))
				fullAfter += int64(len(newSize))
				fullRows++
			}
			if rows >= 200 {
				break
			}
		}
		result.Close()
		db.Close()
		if rows >= 200 {
			break
		}
	}
	if rows == 0 {
		t.Skip("no stored ladders to measure")
	}
	// Every row is already format v2: there is nothing left to shrink, so the
	// comparison would be 1.00x by construction. The trimming itself is covered
	// by TestNormalizeProfileTrimsLadderRows.
	if legacy == 0 {
		t.Skipf("all %d sampled rows are already format v2", rows)
	}
	ratio := float64(before) / float64(after)
	t.Logf("profile row size: %d rows, %d -> %d bytes (%.2fx smaller per row)", rows, before/int64(rows), after/int64(rows), ratio)
	if fullRows > 0 {
		fullRatio := float64(fullBefore) / float64(fullAfter)
		t.Logf("full ladders: %d rows, %d -> %d bytes (%.2fx smaller)", fullRows, fullBefore/int64(fullRows), fullAfter/int64(fullRows), fullRatio)
	}
	t.Logf("with a 5 s cadence instead of 1 s, the profile file writes about %.0fx fewer rows", 5.0)
	if after >= before {
		t.Fatalf("format v2 did not shrink the payload: %d -> %d", before, after)
	}
}

// A day folder written by an older build must still decode and must never be
// rewritten. The decoder path is read only; nothing in this test writes.
func TestLegacyDayFolderIsNotRewritten(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate the test file")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("no repo root: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "Tickers ") {
			continue
		}
		path := filepath.Join(root, entry.Name(), "SPY.profiles.db")
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		modified := info.ModTime()
		db, openErr := sql.Open("sqlite", "file:"+path+"?mode=ro")
		if openErr != nil {
			continue
		}
		rows, queryErr := db.Query(`SELECT timestamp, payload FROM endpoint_profile ORDER BY timestamp LIMIT 20`)
		if queryErr == nil {
			for rows.Next() {
				var ts float64
				var blob []byte
				if rows.Scan(&ts, &blob) != nil {
					continue
				}
				raw, gunzipErr := gunzipBytes(blob)
				if gunzipErr != nil {
					t.Fatalf("%s: stored payload does not gunzip: %v", path, gunzipErr)
				}
				if len(raw) == 0 || !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
					t.Fatalf("%s at %v: payload does not decode as an object", path, ts)
				}
			}
			rows.Close()
		}
		db.Close()
		after, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if !after.ModTime().Equal(modified) {
			t.Fatalf("%s was modified by a read", path)
		}
		return
	}
	t.Skip("no profile file to inspect")
}
