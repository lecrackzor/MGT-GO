package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"market-terminal/internal/api"
	"market-terminal/internal/config"
	"market-terminal/internal/database"
	"market-terminal/internal/utils"
)

// storeTestApp builds an App with only the two dependencies storeMajorLevels
// needs, so the write path can be exercised without a Wails runtime.
func storeTestApp(t *testing.T) (*App, *database.DataWriter, *config.Settings, string, time.Time) {
	t.Helper()
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: filepath.Join(dir, "Tickers")}
	dw := database.NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })
	dl := database.NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 11, 20, 10, 0, 0, 0, et)
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	app := &App{dataWriter: dw, dataLoader: dl}
	return app, dw, settings, "NQ_NDX", date
}

// seedBars writes n one-second bars starting at the session open.
func seedBars(t *testing.T, dw *database.DataWriter, ticker string, date time.Time, n int) []float64 {
	t.Helper()
	et := utils.GetMarketTimezone()
	base := float64(time.Date(date.Year(), date.Month(), date.Day(), 9, 30, 0, 0, et).Unix())
	ts := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		stamp := base + float64(i)
		ts = append(ts, stamp)
		if err := dw.WriteDataEntry(ticker, stamp, map[string]interface{}{"spot": 100 + float64(i)}, true); err != nil {
			t.Fatalf("write %v: %v", stamp, err)
		}
	}
	if err := dw.FlushTicker(ticker); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return ts
}

func samplesFor(ts []float64) []api.HistMajor {
	out := make([]api.HistMajor, 0, len(ts))
	for i, stamp := range ts {
		out = append(out, api.HistMajor{
			Timestamp: int64(stamp),
			Call:      float64(1000 + i),
			Put:       float64(2000 + i),
		})
	}
	return out
}

func countFilled(t *testing.T, dbPath string) (filled, total int) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT COUNT(*), SUM(CASE WHEN major_call_gamma IS NOT NULL AND major_call_gamma <> 0 THEN 1 ELSE 0 END) FROM ticker_data`).Scan(&total, &filled); err != nil {
		t.Fatal(err)
	}
	return filled, total
}

func TestStoreMajorLevelsFillsAndThenRefillsABlankedBlock(t *testing.T) {
	app, dw, settings, ticker, date := storeTestApp(t)
	ts := seedBars(t, dw, ticker, date, 300)
	samples := samplesFor(ts)
	path := database.DBPath(settings.DataDirectory, ticker, date, nil, "test")

	filled, err := app.storeMajorLevels(ticker, date, samples)
	if err != nil {
		t.Fatalf("first store: %v", err)
	}
	if filled.Filled != 300 || filled.Pending != 0 {
		t.Fatalf("first store = %+v, want 300 filled and 0 pending", filled)
	}

	// A second pass has nothing to do.
	again, err := app.storeMajorLevels(ticker, date, samples)
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	if again.Filled != 0 {
		t.Fatalf("second store filled %d, want 0", again.Filled)
	}

	// Blank a block, the way a recovery pass or a repair would leave rows.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE ticker_data SET major_call_gamma = 0, major_put_gamma = 0 WHERE timestamp >= ? AND timestamp <= ?`, ts[10], ts[59]); err != nil {
		db.Close()
		t.Fatalf("blank: %v", err)
	}
	db.Close()

	refilled, err := app.storeMajorLevels(ticker, date, samples)
	if err != nil {
		t.Fatalf("refill: %v", err)
	}
	if refilled.Filled != 50 {
		t.Fatalf("refill wrote %d, want 50", refilled.Filled)
	}
	if refilled.Pending != 0 {
		t.Fatalf("refill reports %d pending, want 0", refilled.Pending)
	}
	filledCount, total := countFilled(t, path)
	if filledCount != total {
		t.Fatalf("filled %d of %d rows after refill", filledCount, total)
	}
}

func TestStoreMajorLevelsWithoutSamplesIsNoOp(t *testing.T) {
	app, dw, _, ticker, date := storeTestApp(t)
	ts := seedBars(t, dw, ticker, date, 10)
	if n, err := app.storeMajorLevels(ticker, date, nil); err != nil || n.Filled != 0 {
		t.Fatalf("nil samples = (%+v, %v)", n, err)
	}
	if n, err := app.storeMajorLevels(ticker, date, samplesFor(ts)[:0]); err != nil || n.Filled != 0 {
		t.Fatalf("empty slice = (%+v, %v)", n, err)
	}
}

// A feed that answers but serves none of the day's timestamps must report the
// remainder as pending, which is what settles the day in the ledger.
func TestStoreMajorLevelsReportsPendingWhenFeedDoesNotCoverTheDay(t *testing.T) {
	app, dw, _, ticker, date := storeTestApp(t)
	ts := seedBars(t, dw, ticker, date, 40)
	// A sample far outside the 300 s hold window of any row.
	far := samplesFor(ts[:1])
	far[0].Timestamp = int64(ts[0]) - 50_000

	res, err := app.storeMajorLevels(ticker, date, far)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if res.Filled != 0 || res.Pending != 40 || res.Samples != 1 {
		t.Fatalf("result = %+v, want 0 filled and 40 pending", res)
	}
}
