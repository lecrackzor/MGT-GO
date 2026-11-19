package database

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

func TestSplitKeepsNumericZero(t *testing.T) {
	scalars, profiles := splitCollectedFields(map[string]interface{}{
		"spot":       0.0,
		"zero_gamma": 1.25,
		"label":      "",
		"flag":       false,
		"ladder":     json.RawMessage(`[9,8]`),
	})
	if scalars["spot"] != 0.0 {
		t.Fatalf("spot zero was dropped: %#v", scalars)
	}
	if scalars["zero_gamma"] != 1.25 {
		t.Fatalf("zero_gamma = %#v", scalars["zero_gamma"])
	}
	if _, ok := scalars["label"]; ok {
		t.Fatal("empty string stored as a scalar")
	}
	if _, ok := scalars["flag"]; ok {
		t.Fatal("false stored as a scalar")
	}
	if _, ok := profiles["ladder"]; !ok {
		t.Fatal("raw profile was decoded or dropped")
	}
}

func TestFlushKeepsZeroAndDoesNotWipeOtherColumns(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: filepath.Join(dir, "Tickers"), CollectAllEndpoints: true}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 11, 0, 0, 0, et)
	ts := float64(when.Unix())
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	ticker := "SPX"

	dw.mu.Lock()
	dw.pendingWrites[ticker] = []*PendingWrite{{
		Ticker:    ticker,
		Timestamp: ts,
		Scalars:   map[string]interface{}{"spot": 0.0, "zero_gamma": 1.5},
		Profiles:  map[string]interface{}{"strikes": json.RawMessage(`[1,2,3]`)},
		Date:      date,
	}}
	dw.mu.Unlock()
	if err := dw.FlushTicker(ticker); err != nil {
		t.Fatal(err)
	}

	dw.mu.Lock()
	dw.pendingWrites[ticker] = []*PendingWrite{{
		Ticker:    ticker,
		Timestamp: ts,
		Scalars:   map[string]interface{}{"major_pos_vol": 4.0},
		Date:      date,
	}}
	dw.mu.Unlock()
	if err := dw.FlushTicker(ticker); err != nil {
		t.Fatal(err)
	}

	path := dw.getDBPath(ticker, date)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var spot, zeroGamma, posVol sql.NullFloat64
	var blob []byte
	err = db.QueryRow(
		`SELECT spot, zero_gamma, major_pos_vol, profiles_blob FROM ticker_data WHERE timestamp = ?`, ts,
	).Scan(&spot, &zeroGamma, &posVol, &blob)
	if err != nil {
		t.Fatal(err)
	}
	if !spot.Valid || spot.Float64 != 0 {
		t.Fatalf("spot = %+v, want 0", spot)
	}
	if !zeroGamma.Valid || zeroGamma.Float64 != 1.5 {
		t.Fatalf("zero_gamma = %+v, want 1.5", zeroGamma)
	}
	if !posVol.Valid || posVol.Float64 != 4 {
		t.Fatalf("major_pos_vol = %+v, want 4", posVol)
	}

	if len(blob) != 0 {
		t.Fatalf("chart file still holds a profile blob (%d bytes)", len(blob))
	}
	profileDB, err := sql.Open("sqlite", ProfileDBPath(path))
	if err != nil {
		t.Fatal(err)
	}
	defer profileDB.Close()
	var payload []byte
	if err := profileDB.QueryRow(`SELECT payload FROM endpoint_profile WHERE endpoint = ?`, "strikes").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	zr.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`[1,2,3]`)) {
		t.Fatalf("profile payload re-encoded or lost: %s", raw)
	}

	var indexes int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name LIKE 'idx_timestamp%'`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 0 {
		t.Fatalf("redundant timestamp indexes still present: %d", indexes)
	}

	dl := NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })
	loaded, err := dl.LoadChartData(ticker, date, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded["spot"]) != 1 || loaded["spot"][0] != 0 {
		t.Fatalf("chart spot = %v", loaded["spot"])
	}
	if len(loaded["major_neg_vol"]) != 1 || !math.IsNaN(loaded["major_neg_vol"][0]) {
		t.Fatalf("missing major_neg_vol = %v, want NaN", loaded["major_neg_vol"])
	}
	encoded, err := loaded["major_neg_vol"].MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[null]" {
		t.Fatalf("encoded gap = %s", encoded)
	}
	encodedSpot, err := loaded["spot"].MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(encodedSpot) != "[0]" {
		t.Fatalf("encoded spot = %s", encodedSpot)
	}

	legacy, err := dl.LoadProfile(ticker, date, &ts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !legacy.Legacy {
		t.Fatal("flat strikes blob should be marked legacy")
	}
	if _, ok := loaded["profiles_blob"]; ok {
		t.Fatal("chart load included the profile blob")
	}
}

func TestProfileEndpointsMergeOnSameTimestamp(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: filepath.Join(dir, "Tickers"), CollectAllEndpoints: true}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 11, 0, 0, 0, et)
	ts := float64(when.Unix())
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	ticker := "SPX"

	dw.mu.Lock()
	dw.pendingWrites[ticker] = []*PendingWrite{{
		Ticker:    ticker,
		Timestamp: ts,
		Scalars:   map[string]interface{}{"spot": 10.0, "zero_gamma": 2.0},
		Profiles: map[string]interface{}{
			"classic_zero": map[string]interface{}{"strikes": json.RawMessage(`[[100,5]]`), "spot": 10.0},
		},
		Date: date,
	}}
	dw.mu.Unlock()
	if err := dw.FlushTicker(ticker); err != nil {
		t.Fatal(err)
	}

	dw.mu.Lock()
	dw.pendingWrites[ticker] = []*PendingWrite{{
		Ticker:    ticker,
		Timestamp: ts,
		Scalars:   map[string]interface{}{"spot": 11.0},
		Profiles: map[string]interface{}{
			"classic_one": map[string]interface{}{"strikes": json.RawMessage(`[[200,7]]`)},
		},
		Date: date,
	}}
	dw.mu.Unlock()
	if err := dw.FlushTicker(ticker); err != nil {
		t.Fatal(err)
	}

	dl := NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })
	snap, err := dl.LoadProfile(ticker, date, &ts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Missing || snap.Legacy {
		t.Fatalf("snapshot = %+v", snap)
	}
	if !bytes.Contains(snap.Profiles["classic_zero"], []byte(`[[100,5]]`)) {
		t.Fatalf("classic_zero lost: %s", snap.Profiles["classic_zero"])
	}
	if !bytes.Contains(snap.Profiles["classic_one"], []byte(`[[200,7]]`)) {
		t.Fatalf("classic_one lost: %s", snap.Profiles["classic_one"])
	}
	if snap.Spot == nil || *snap.Spot != 11 {
		t.Fatalf("spot = %v", snap.Spot)
	}

	chart, err := dl.LoadChartData(ticker, date, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(chart["spot"]) != 1 || chart["spot"][0] != 11 {
		t.Fatalf("chart spot = %v", chart["spot"])
	}

	chartDB, err := sql.Open("sqlite", dw.getDBPath(ticker, date))
	if err != nil {
		t.Fatal(err)
	}
	defer chartDB.Close()
	var blob []byte
	if err := chartDB.QueryRow(`SELECT profiles_blob FROM ticker_data WHERE timestamp = ?`, ts).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if len(blob) != 0 {
		t.Fatalf("chart file still holds a profile blob (%d bytes)", len(blob))
	}

	var endpoints int
	profileDB, err := sql.Open("sqlite", ProfileDBPath(dw.getDBPath(ticker, date)))
	if err != nil {
		t.Fatal(err)
	}
	defer profileDB.Close()
	if err := profileDB.QueryRow(`SELECT count(*) FROM endpoint_profile WHERE timestamp = ?`, ts).Scan(&endpoints); err != nil {
		t.Fatal(err)
	}
	if endpoints != 2 {
		t.Fatalf("endpoint rows = %d, want 2", endpoints)
	}
	filtered, err := dl.LoadProfile(ticker, date, &ts, []string{"classic_one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := filtered.Profiles["classic_zero"]; ok {
		t.Fatal("endpoint filter returned classic_zero")
	}
	if !bytes.Contains(filtered.Profiles["classic_one"], []byte(`[[200,7]]`)) {
		t.Fatalf("filtered classic_one = %s", filtered.Profiles["classic_one"])
	}
}

func TestLoadProfileFallsBackToNamespacedBlob(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: filepath.Join(dir, "Tickers")}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 12, 0, 0, 0, et)
	ts := float64(when.Unix())
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	ticker := "QQQ"

	dw.mu.Lock()
	dw.pendingWrites[ticker] = []*PendingWrite{{
		Ticker:    ticker,
		Timestamp: ts,
		Scalars:   map[string]interface{}{"spot": 4.0},
		Date:      date,
	}}
	dw.mu.Unlock()
	if err := dw.FlushTicker(ticker); err != nil {
		t.Fatal(err)
	}

	blob, err := gzipJSON(map[string]interface{}{
		"classic_zero": map[string]interface{}{"strikes": json.RawMessage(`[[9,1]]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	chartDB, err := sql.Open("sqlite", dw.getDBPath(ticker, date))
	if err != nil {
		t.Fatal(err)
	}
	defer chartDB.Close()
	if _, err := chartDB.Exec(`UPDATE ticker_data SET profiles_blob = ? WHERE timestamp = ?`, blob, ts); err != nil {
		t.Fatal(err)
	}

	dl := NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })
	snap, err := dl.LoadProfile(ticker, date, &ts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Missing || snap.Legacy {
		t.Fatalf("snapshot = %+v", snap)
	}
	if !bytes.Contains(snap.Profiles["classic_zero"], []byte(`[[9,1]]`)) {
		t.Fatalf("fallback payload = %s", snap.Profiles["classic_zero"])
	}
}

func TestLoadChartDataKeepsNewestRows(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: filepath.Join(dir, "Tickers")}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 11, 0, 0, 0, et)
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	base := float64(when.Unix())
	writes := make([]*PendingWrite, 0, 5)
	for i := 0; i < 5; i++ {
		writes = append(writes, &PendingWrite{
			Ticker:    "SPX",
			Timestamp: base + float64(i),
			Scalars:   map[string]interface{}{"spot": float64(i + 1)},
			Date:      date,
		})
	}
	dw.mu.Lock()
	dw.pendingWrites["SPX"] = writes
	dw.mu.Unlock()
	if err := dw.FlushTicker("SPX"); err != nil {
		t.Fatal(err)
	}

	dl := NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })
	loaded, err := dl.LoadChartData("SPX", date, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := loaded["timestamp"]
	if len(got) != 3 || got[0] != base+2 || got[1] != base+3 || got[2] != base+4 {
		t.Fatalf("timestamps = %v, want the newest three ascending", got)
	}
	if loaded["spot"][0] != 3 || loaded["spot"][2] != 5 {
		t.Fatalf("spots = %v", loaded["spot"])
	}
}

func TestLoadTickerDataKeepsStoredZeroGamma(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: filepath.Join(dir, "Tickers")}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 11, 0, 0, 0, et)
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	base := float64(when.Unix())
	dw.mu.Lock()
	dw.pendingWrites["SPX"] = []*PendingWrite{
		{
			Ticker:    "SPX",
			Timestamp: base,
			Scalars:   map[string]interface{}{"spot": 10.0, "zero_gamma": 12.0},
			Date:      date,
		},
		{
			Ticker:    "SPX",
			Timestamp: base + 1,
			Scalars:   map[string]interface{}{"spot": 10.0, "zero_gamma": 0.0},
			Date:      date,
		},
	}
	dw.mu.Unlock()
	if err := dw.FlushTicker("SPX"); err != nil {
		t.Fatal(err)
	}

	dl := NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })
	latest, err := dl.LoadTickerData("SPX", date)
	if err != nil {
		t.Fatal(err)
	}
	zg, _ := latest["zero_gamma"].([]interface{})
	if len(zg) != 1 || zg[0] != 0.0 {
		t.Fatalf("zero_gamma = %#v, want stored 0", latest["zero_gamma"])
	}
}

func TestLoaderFollowsUpdatedDataDirectory(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "old")
	second := filepath.Join(dir, "new")
	dl := NewDataLoader(&config.Settings{DataDirectory: first}, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })
	when := time.Date(2026, 10, 16, 12, 0, 0, 0, utils.GetMarketTimezone())

	oldPath := dl.getDBPath("SPX", when)
	if !strings.HasPrefix(oldPath, first) {
		t.Fatalf("startup path = %q", oldPath)
	}
	dl.SetSettings(&config.Settings{DataDirectory: second})
	got := dl.getDBPath("SPX", when)
	if !strings.HasPrefix(got, second) {
		t.Fatalf("updated path = %q, want prefix %q", got, second)
	}
}

func TestLoadStoredSession(t *testing.T) {
	root := filepath.Join("..", "..", "Tickers")
	dbPath := filepath.Join(root+" 10.16.2026", "SPY.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Skip("local 10/16 session is not in this workspace")
	}
	settings := &config.Settings{DataDirectory: root}
	dl := NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })

	when := time.Date(2026, 10, 16, 0, 0, 0, 0, utils.GetMarketTimezone())
	loaded, err := dl.LoadChartData("SPY", when, 30000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded["timestamp"]) < 1000 {
		t.Fatalf("expected a full session, got %d rows", len(loaded["timestamp"]))
	}
	latest, err := dl.LoadTickerData("SPY", when)
	if err != nil {
		t.Fatal(err)
	}
	spots, _ := latest["spot"].([]interface{})
	if len(spots) != 1 {
		t.Fatalf("table row spot = %#v", latest["spot"])
	}
	encoded, err := json.Marshal(loaded["spot"])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("NaN")) {
		t.Fatal("spot series encoded NaN")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var indexes int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name LIKE 'idx_timestamp%'`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 0 {
		t.Fatalf("10/16 file still has %d timestamp indexes", indexes)
	}
}

func TestChartRowIsVisibleBeforeCollectionBatch(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: filepath.Join(dir, "Tickers"), CollectAllEndpoints: true}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 11, 0, 0, 0, et)
	first := float64(when.Unix())
	second := first + 2
	ticker := "ES_SPX"
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)

	if err := dw.WriteDataEntry(ticker, first, map[string]interface{}{
		"spot":         1.0,
		"classic_zero": json.RawMessage(`[1]`),
	}, false); err != nil {
		t.Fatal(err)
	}

	path := dw.getDBPath(ticker, date)
	deadline := time.Now().Add(2 * time.Second)
	for {
		db, err := sql.Open("sqlite", path)
		if err == nil {
			var spot sql.NullFloat64
			err = db.QueryRow(`SELECT spot FROM ticker_data WHERE timestamp = ?`, first).Scan(&spot)
			db.Close()
			if err == nil && spot.Valid && spot.Float64 == 1 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("first row was not flushed")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := dw.WriteDataEntry(ticker, second, map[string]interface{}{
		"spot":         2.0,
		"classic_zero": json.RawMessage(`[2]`),
	}, false); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var spot sql.NullFloat64
	if err := db.QueryRow(`SELECT spot FROM ticker_data WHERE timestamp = ?`, second).Scan(&spot); err != nil {
		t.Fatal(err)
	}
	if !spot.Valid || spot.Float64 != 2 {
		t.Fatalf("spot = %+v, want 2 before the collection batch", spot)
	}

	dw.mu.Lock()
	pending := len(dw.pendingWrites[ticker])
	dw.mu.Unlock()
	if pending == 0 {
		t.Fatal("ladder batch was flushed with the chart row")
	}

	if _, err := os.Stat(ProfileDBPath(path)); err == nil {
		profileDB, err := sql.Open("sqlite", ProfileDBPath(path))
		if err != nil {
			t.Fatal(err)
		}
		defer profileDB.Close()
		var n int
		err = profileDB.QueryRow(`SELECT count(*) FROM endpoint_profile WHERE timestamp = ?`, second).Scan(&n)
		if err != nil && !strings.Contains(err.Error(), "no such table") {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("ladder for the new sample was written immediately: %d", n)
		}
	}
}

func TestProfileFlushFailureRetriesSameTimestamp(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: filepath.Join(dir, "Tickers"), CollectAllEndpoints: true}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 11, 0, 0, 0, et)
	ts := float64(when.Unix())
	ticker := "SPX"
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	write := &PendingWrite{
		Ticker:    ticker,
		Timestamp: ts,
		Scalars:   map[string]interface{}{"spot": 2.0},
		Profiles:  map[string]interface{}{"classic_zero": json.RawMessage(`[2]`)},
		Date:      date,
	}
	dw.mu.Lock()
	dw.lastFlushTime[ticker] = time.Now()
	dw.firstPendingTime[ticker] = time.Now()
	dw.pendingWrites[ticker] = []*PendingWrite{write}
	dw.mu.Unlock()

	if err := dw.commitChartScalars(write); err != nil {
		t.Fatal(err)
	}
	dw.failNextProfileFlush.Store(true)
	if err := dw.FlushTicker(ticker); err == nil {
		t.Fatal("profile commit failure should be returned")
	}

	path := dw.getDBPath(ticker, date)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var spot sql.NullFloat64
	if err := db.QueryRow(`SELECT spot FROM ticker_data WHERE timestamp = ?`, ts).Scan(&spot); err != nil {
		t.Fatal(err)
	}
	if !spot.Valid || spot.Float64 != 2 {
		t.Fatalf("spot = %+v, want the chart row kept after the profile failure", spot)
	}

	dw.mu.Lock()
	pending := len(dw.pendingWrites[ticker])
	dw.mu.Unlock()
	if pending == 0 {
		t.Fatal("failed ladder was dropped instead of requeued")
	}

	if err := dw.FlushTicker(ticker); err != nil {
		t.Fatal(err)
	}
	profileDB, err := sql.Open("sqlite", ProfileDBPath(path))
	if err != nil {
		t.Fatal(err)
	}
	defer profileDB.Close()
	var n int
	if err := profileDB.QueryRow(`SELECT count(*) FROM endpoint_profile WHERE timestamp = ? AND endpoint = ?`, ts, "classic_zero").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("retried ladder rows = %d, want 1", n)
	}
}
