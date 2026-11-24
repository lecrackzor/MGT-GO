package database

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

// writeRows seeds a day with one scalar column so every timestamp exists.
func writeRows(t *testing.T, dw *DataWriter, ticker string, date time.Time, timestamps []float64) {
	t.Helper()
	dw.mu.Lock()
	writes := make([]*PendingWrite, 0, len(timestamps))
	for _, ts := range timestamps {
		writes = append(writes, &PendingWrite{
			Ticker:    ticker,
			Timestamp: ts,
			Scalars:   map[string]interface{}{"spot": ts},
			Date:      date,
		})
	}
	dw.pendingWrites[ticker] = writes
	dw.mu.Unlock()
	if err := dw.FlushTicker(ticker); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func majorLevelSetup(t *testing.T) (*DataWriter, *DataLoader, string, time.Time) {
	t.Helper()
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: filepath.Join(dir, "Tickers")}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })
	dl := NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 11, 20, 10, 0, 0, 0, et)
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	return dw, dl, "NQ_NDX", date
}

func TestFillMajorLevelsFillsOnlyEmptyColumns(t *testing.T) {
	dw, dl, ticker, date := majorLevelSetup(t)
	base := float64(date.Add(10 * time.Hour).Unix())
	ts := []float64{base, base + 1, base + 2}
	writeRows(t, dw, ticker, date, ts)

	// The second bar already holds a call level and must not be replaced, and
	// its missing put level must still be filled.
	if _, err := dw.FillMajorLevels(ticker, date, []MajorLevel{
		{Timestamp: base + 1, Call: 111},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	filled, err := dw.FillMajorLevels(ticker, date, []MajorLevel{
		{Timestamp: base, Call: 10, Put: 20},
		{Timestamp: base + 1, Call: 999, Put: 999},
		{Timestamp: base + 2, Call: 30, Put: 40},
	})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if filled != 3 {
		t.Fatalf("filled = %d, want 3 (bar 1 gains only its put level)", filled)
	}

	got := readMajorLevels(t, dw, ticker, date, ts)
	if got[0].Call != 10 || got[0].Put != 20 {
		t.Fatalf("bar 0 = %+v", got[0])
	}
	if got[1].Call != 111 {
		t.Fatalf("bar 1 call level was overwritten: %+v", got[1])
	}
	if got[1].Put != 999 {
		t.Fatalf("bar 1 put level was not filled: %+v", got[1])
	}
	if got[2].Call != 30 || got[2].Put != 40 {
		t.Fatalf("bar 2 = %+v", got[2])
	}

	// A second identical pass changes nothing.
	again, err := dw.FillMajorLevels(ticker, date, []MajorLevel{
		{Timestamp: base, Call: 10, Put: 20},
		{Timestamp: base + 2, Call: 30, Put: 40},
	})
	if err != nil {
		t.Fatalf("second fill: %v", err)
	}
	if again != 0 {
		t.Fatalf("second pass filled %d rows, want 0", again)
	}

	if pending, err := dl.LoadBarsMissingMajorLevels(ticker, date); err != nil {
		t.Fatal(err)
	} else if len(pending) != 0 {
		t.Fatalf("bars still missing = %v, want none", pending)
	}
}

func TestFillMajorLevelsNeverInsertsRows(t *testing.T) {
	dw, dl, ticker, date := majorLevelSetup(t)
	base := float64(date.Add(10 * time.Hour).Unix())
	writeRows(t, dw, ticker, date, []float64{base})

	if _, err := dw.FillMajorLevels(ticker, date, []MajorLevel{
		{Timestamp: base, Call: 5, Put: 6},
		{Timestamp: base + 60, Call: 7, Put: 8}, // no bar here
	}); err != nil {
		t.Fatalf("fill: %v", err)
	}

	if pending, err := dl.LoadBarsMissingMajorLevels(ticker, date); err != nil {
		t.Fatal(err)
	} else if len(pending) != 0 {
		t.Fatalf("the existing bar was not filled: %v", pending)
	}
	all, err := dl.LoadBarsMissingMajorLevels(ticker, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("pending = %v", all)
	}
	// Confirm the phantom timestamp was not created.
	dw.mu.Lock()
	path := dw.getDBPath(ticker, date)
	dw.mu.Unlock()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM ticker_data").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1: a level outside a bar must not create a row", count)
	}
}

func TestLoadBarsMissingMajorLevelsReportsOnlyEmptyBars(t *testing.T) {
	dw, dl, ticker, date := majorLevelSetup(t)
	base := float64(date.Add(10 * time.Hour).Unix())
	ts := []float64{base, base + 1, base + 2, base + 3}
	writeRows(t, dw, ticker, date, ts)
	if _, err := dw.FillMajorLevels(ticker, date, []MajorLevel{{Timestamp: base + 1, Call: 1, Put: 2}}); err != nil {
		t.Fatal(err)
	}

	pending, err := dl.LoadBarsMissingMajorLevels(ticker, date)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{base, base + 2, base + 3}
	if len(pending) != len(want) {
		t.Fatalf("pending = %v, want %v", pending, want)
	}
	for i := range want {
		if pending[i] != want[i] {
			t.Fatalf("pending = %v, want %v", pending, want)
		}
	}
}

func TestLoadBarsMissingMajorLevelsOnDayWithoutTheColumns(t *testing.T) {
	dw, dl, ticker, date := majorLevelSetup(t)
	base := float64(date.Add(10 * time.Hour).Unix())
	writeRows(t, dw, ticker, date, []float64{base, base + 1})

	// The writer pre-creates both columns, so drop them to imitate a day
	// recovered from the archive, which only creates columns the archive has.
	path := dl.getDBPath(ticker, date)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"major_call_gamma", "major_put_gamma"} {
		if _, err := db.Exec("ALTER TABLE ticker_data DROP COLUMN " + col); err != nil {
			db.Close()
			t.Fatalf("drop %s: %v", col, err)
		}
	}
	db.Close()

	// Absent column means every row is missing a level, so all of them are
	// offered to the fill (which adds the columns before writing).
	pending, err := dl.LoadBarsMissingMajorLevels(ticker, date)
	if err != nil {
		t.Fatalf("a day without the columns must not error: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %v, want both rows", pending)
	}

	// And the fill then creates the columns and populates them.
	filled, err := dw.FillMajorLevels(ticker, date, []MajorLevel{
		{Timestamp: base, Call: 1, Put: 2},
		{Timestamp: base + 1, Call: 3, Put: 4},
	})
	if err != nil {
		t.Fatalf("fill on a day without the columns: %v", err)
	}
	if filled != 2 {
		t.Fatalf("filled = %d, want 2", filled)
	}
	got := readMajorLevels(t, dw, ticker, date, []float64{base, base + 1})
	if got[0].Call != 1 || got[1].Put != 4 {
		t.Fatalf("values = %+v", got)
	}
	if pending, err := dl.LoadBarsMissingMajorLevels(ticker, date); err != nil {
		t.Fatal(err)
	} else if len(pending) != 0 {
		t.Fatalf("still pending after fill = %v", pending)
	}
}

// readMajorLevels reads the two columns for the given timestamps.
func readMajorLevels(t *testing.T, dw *DataWriter, ticker string, date time.Time, ts []float64) []MajorLevel {
	t.Helper()
	dw.mu.Lock()
	path := dw.getDBPath(ticker, date)
	dw.mu.Unlock()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := make([]MajorLevel, 0, len(ts))
	for _, stamp := range ts {
		var call, put float64
		if err := db.QueryRow("SELECT COALESCE(major_call_gamma,0), COALESCE(major_put_gamma,0) FROM ticker_data WHERE timestamp = ?", stamp).Scan(&call, &put); err != nil {
			t.Fatalf("read %v: %v", stamp, err)
		}
		out = append(out, MajorLevel{Timestamp: stamp, Call: call, Put: put})
	}
	return out
}
