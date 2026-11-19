package database

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

func TestProfileDeltaKeepsUnchangedStrikesAndExactChanges(t *testing.T) {
	base := []byte(`{"spot":10,"strikes":[[100,1,0,[9,8,7,6,5]],[110,2,0,[1,1,1,1,1]]]}`)
	next := []byte(`{"spot":11,"strikes":[[100,1,0,[9,8,7,6,5]],[110,9,0,[4,3,2,1,0]]]}`)
	stored, chain, skip, err := encodeProfile(base, 0, next)
	if err != nil {
		t.Fatal(err)
	}
	if skip || chain != 1 || !bytes.Contains(stored, []byte(`"_d":1`)) {
		t.Fatalf("stored chain %d skip %v %s", chain, skip, stored)
	}
	if bytes.Contains(stored, []byte(`[100,1,0,[9,8,7,6,5]]`)) {
		t.Fatalf("unchanged strike was stored again: %s", stored)
	}
	if !bytes.Contains(stored, []byte(`[110,9,0,[4,3,2,1,0]]`)) {
		t.Fatalf("changed strike missing: %s", stored)
	}
	got, err := applyProfileDelta(base, stored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`[100,1,0,[9,8,7,6,5]]`)) || !bytes.Contains(got, []byte(`[110,9,0,[4,3,2,1,0]]`)) {
		t.Fatalf("rebuilt ladder = %s", got)
	}
	if !bytes.Contains(got, []byte(`"spot":11`)) {
		t.Fatalf("spot was not carried onto the rebuilt ladder: %s", got)
	}

	same, chain, skip, err := encodeProfile(next, chain, next)
	if err != nil {
		t.Fatal(err)
	}
	if !skip || same != nil || chain != 1 {
		t.Fatalf("identical sample stored again: skip %v chain %d %s", skip, chain, same)
	}

	shifted := []byte(`{"spot":11,"strikes":[[105,3,0,[0,0,0,0,0]]]}`)
	full, chain, skip, err := encodeProfile(next, 1, shifted)
	if err != nil {
		t.Fatal(err)
	}
	if skip || chain != 0 || bytes.Contains(full, []byte(`"_d"`)) {
		t.Fatalf("strike-list change should be a full ladder: chain %d skip %v %s", chain, skip, full)
	}
}

func TestProfileKeyframeBoundsTheDeltaChain(t *testing.T) {
	prev := []byte(`{"strikes":[[100,1],[110,2]]}`)
	chain := 0
	var stored []byte
	for i := 1; i <= profileKeyframeEvery; i++ {
		next := []byte(`{"strikes":[[100,1],[110,` + jsonNumber(i) + `]]}`)
		var skip bool
		var err error
		stored, chain, skip, err = encodeProfile(prev, chain, next)
		if err != nil {
			t.Fatal(err)
		}
		if skip {
			t.Fatalf("sample %d was treated as unchanged", i)
		}
		prev = next
	}
	if chain != 0 || bytes.Contains(stored, []byte(`"_d"`)) {
		t.Fatalf("sample %d should be a full ladder, chain %d %s", profileKeyframeEvery, chain, stored)
	}
}

func TestProfileDeltaRoundTripFromDisk(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: dir + "/Tickers", CollectAllEndpoints: true, ProfileSampleSeconds: 1}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })
	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 11, 0, 0, 0, et)
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	t0 := float64(when.Unix())
	t1 := t0 + 1
	t2 := t0 + 2
	first := map[string]interface{}{
		"spot":    10.0,
		"strikes": json.RawMessage(`[[100,1,0,[9,8,7,6,5]],[110,2,0,[1,1,1,1,1]]]`),
	}
	second := map[string]interface{}{
		"spot":    11.0,
		"strikes": json.RawMessage(`[[100,1,0,[9,8,7,6,5]],[110,9,0,[4,3,2,1,0]]]`),
	}
	write := func(ts float64, profile map[string]interface{}) {
		t.Helper()
		dw.mu.Lock()
		dw.pendingWrites["SPX"] = []*PendingWrite{{
			Ticker:    "SPX",
			Timestamp: ts,
			Scalars:   map[string]interface{}{"spot": profile["spot"], "zero_gamma": 1.0},
			Profiles:  map[string]interface{}{"classic_zero": profile},
			Date:      date,
		}}
		dw.mu.Unlock()
		if err := dw.FlushTicker("SPX"); err != nil {
			t.Fatal(err)
		}
	}
	write(t0, first)
	write(t1, second)
	write(t2, second)

	dl := NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })
	snap, err := dl.LoadProfile("SPX", date, &t1, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := snap.Profiles["classic_zero"]
	if !bytes.Contains(body, []byte(`[[100,1,0],[110,9,0]]`)) {
		t.Fatalf("rebuilt profile = %s", body)
	}
	if bytes.Contains(body, []byte(`"_f"`)) {
		t.Fatalf("storage marker leaked into the served ladder: %s", body)
	}
	earlier, err := dl.LoadProfile("SPX", date, &t0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(earlier.Profiles["classic_zero"], []byte(`[110,2,0]`)) {
		t.Fatalf("earlier profile changed: %s", earlier.Profiles["classic_zero"])
	}

	path := ProfileDBPath(dw.getDBPath("SPX", date))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM endpoint_profile WHERE endpoint = ?`, "classic_zero").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rows = %d, want the first ladder and one change (identical third sample omitted)", n)
	}
	var payload []byte
	if err := db.QueryRow(`SELECT payload FROM endpoint_profile WHERE endpoint = ? AND timestamp = ?`, "classic_zero", t1).Scan(&payload); err != nil {
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
	if bytes.Contains(raw, []byte(`[100,1,0]`)) {
		t.Fatalf("stored change still contains the quiet strike: %s", raw)
	}
}

func TestProfileDeltaRoundTripWhenStrikeReturns(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: dir + "/Tickers", CollectAllEndpoints: true, ProfileSampleSeconds: 1}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })
	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 11, 0, 0, 0, et)
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	t0 := float64(when.Unix())
	ladder := func(second int) map[string]interface{} {
		return map[string]interface{}{
			"spot":    10.0,
			"strikes": json.RawMessage(`[[100,1],[110,` + jsonNumber(second) + `]]`),
		}
	}
	write := func(ts float64, second int) {
		t.Helper()
		dw.mu.Lock()
		dw.pendingWrites["SPX"] = []*PendingWrite{{
			Ticker:    "SPX",
			Timestamp: ts,
			Scalars:   map[string]interface{}{"spot": 10.0},
			Profiles:  map[string]interface{}{"classic_zero": ladder(second)},
			Date:      date,
		}}
		dw.mu.Unlock()
		if err := dw.FlushTicker("SPX"); err != nil {
			t.Fatal(err)
		}
	}
	write(t0, 2)
	write(t0+1, 9)
	write(t0+2, 2)

	dl := NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })
	at := t0 + 2
	snap, err := dl.LoadProfile("SPX", date, &at, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := snap.Profiles["classic_zero"]
	if !bytes.Contains(body, []byte(`[110,2]`)) || bytes.Contains(body, []byte(`[110,9]`)) {
		t.Fatalf("returned strike stuck at the intermediate value: %s", body)
	}
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// A day that started before this build holds legacy rows (1s cadence, lookbacks
// inside each ladder row) followed by version 2 rows (sparse cadence, trimmed
// rows). Both must read back exactly as they were written, and no stored row is
// ever rewritten.
func TestProfileMixedFormatChainRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SPX.profiles.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ensureProfileTable(db); err != nil {
		t.Fatal(err)
	}

	legacyFull := []byte(`{"spot":10,"strikes":[[100,1,0,[9,8,7,6,5]],[110,2,0,[1,1,1,1,1]]]}`)
	legacyDelta := []byte(`{"_d":1,"n":1,"strikes":[[110,9,0,[4,3,2,1,0]]]}`)
	v2Full := normalizeProfile([]byte(`{"spot":11,"strikes":[[100,1,0,[9,8,7,6,5]],[110,12,0,[4,3,2,1,0]]],"mini_contracts":[[100,0.4,0.5,1.2,[1,2,3],0,null]]}`))
	v2Next := normalizeProfile([]byte(`{"spot":11,"strikes":[[100,1,0,[9,8,7,6,5]],[110,13,0,[4,3,2,1,0]]],"mini_contracts":[[100,0.4,0.5,1.2,[1,2,3],0,null]]}`))
	v2Delta, _, _, err := encodeProfile(v2Full, 0, v2Next)
	if err != nil {
		t.Fatal(err)
	}

	write := func(ts float64, raw []byte) {
		t.Helper()
		payload, err := gzipJSON(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO endpoint_profile (timestamp, endpoint, payload) VALUES (?, ?, ?)`, ts, "classic_zero", payload); err != nil {
			t.Fatal(err)
		}
	}
	write(1000, legacyFull)
	write(1001, legacyDelta)
	write(1002, v2Full)
	write(1003, v2Delta)

	at := func(ts float64) string {
		t.Helper()
		body, got, _, err := profileAt(db, "classic_zero", &ts)
		if err != nil {
			t.Fatal(err)
		}
		if got != ts {
			t.Fatalf("rebuilt %v at %v", got, ts)
		}
		if bytes.Contains(body, []byte(`"_f"`)) || bytes.Contains(body, []byte(`"_d"`)) {
			t.Fatalf("storage marker served at %v: %s", ts, body)
		}
		return string(body)
	}

	if got := at(1001); !bytes.Contains([]byte(got), []byte(`[110,9,0,[4,3,2,1,0]]`)) {
		t.Fatalf("legacy delta lost its lookbacks: %s", got)
	}
	if got := at(1002); !bytes.Contains([]byte(got), []byte(`[[100,1,0],[110,12,0]]`)) {
		t.Fatalf("version 2 full ladder = %s", got)
	} else if !bytes.Contains([]byte(got), []byte(`[100,0.4,0.5,1.2]`)) || bytes.Contains([]byte(got), []byte(`null`)) {
		t.Fatalf("mini_contracts was not trimmed: %s", got)
	}
	if got := at(1003); !bytes.Contains([]byte(got), []byte(`[110,13,0]`)) {
		t.Fatalf("version 2 delta = %s", got)
	}

	var legacy []byte
	if err := db.QueryRow(`SELECT payload FROM endpoint_profile WHERE timestamp = 1000`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(legacy))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	zr.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`[9,8,7,6,5]`)) {
		t.Fatalf("legacy row was rewritten: %s", raw)
	}
}

func TestNormalizeProfileTrimsLadderRows(t *testing.T) {
	raw := []byte(`{"spot":10,"strikes":[[6325.5,1,2,[9,8,7,6,5]]],"mini_contracts":[[100,0.426,0,1.5,[51.41,57.72,30.76],0,null]],"max_priors":[[6325,-1418]]}`)
	got := normalizeProfile(raw)
	if bytes.Contains(got, []byte(`[9,8,7,6,5]`)) || bytes.Contains(got, []byte(`[51.41,57.72,30.76]`)) {
		t.Fatalf("lookbacks survived: %s", got)
	}
	if bytes.Contains(got, []byte(`null`)) {
		t.Fatalf("constant put volume survived: %s", got)
	}
	for _, want := range []string{`[6325.5,1,2]`, `[100,0.426,0,1.5]`, `"max_priors"`, `"_f":2`} {
		if !bytes.Contains(got, []byte(want)) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	if !profileIsCurrent(got) {
		t.Fatalf("normalized payload is not current: %s", got)
	}
	if profileIsCurrent(raw) {
		t.Fatal("a live payload must not be mistaken for a stored one")
	}
}

func TestLadderCadenceSkipsAndForcedFlushKeepsLast(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: dir + "/Tickers", CollectAllEndpoints: true, ProfileSampleSeconds: 5}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 11, 0, 0, 0, et)
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	t0 := float64(when.Unix())

	queue := func(ts float64, second int) {
		dw.mu.Lock()
		dw.pendingWrites["SPX"] = append(dw.pendingWrites["SPX"], &PendingWrite{
			Ticker:    "SPX",
			Timestamp: ts,
			Scalars:   map[string]interface{}{"spot": 10.0},
			Profiles: map[string]interface{}{
				"classic_zero": map[string]interface{}{"spot": 10.0, "strikes": json.RawMessage(`[[100,1,0],[110,` + jsonNumber(second) + `,0]]`)},
			},
			Date: date,
		})
		dw.mu.Unlock()
	}

	for i := 0; i < 3; i++ {
		queue(t0+float64(i), i)
		if err := dw.FlushTicker("SPX"); err != nil {
			t.Fatal(err)
		}
	}
	queue(t0+6, 6)
	if err := dw.FlushTicker("SPX"); err != nil {
		t.Fatal(err)
	}

	// Not due yet, but a forced flush keeps the newest sample.
	queue(t0+7, 7)
	if err := dw.FlushAllTickers(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", ProfileDBPath(dw.getDBPath("SPX", date)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT timestamp FROM endpoint_profile WHERE endpoint = ? ORDER BY timestamp`, "classic_zero")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var stored []float64
	for rows.Next() {
		var ts float64
		if err := rows.Scan(&ts); err != nil {
			t.Fatal(err)
		}
		stored = append(stored, ts)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []float64{t0, t0 + 6, t0 + 7}
	if len(stored) != len(want) {
		t.Fatalf("stored ladders = %v, want %v", stored, want)
	}
	for i := range want {
		if stored[i] != want[i] {
			t.Fatalf("stored ladders = %v, want %v", stored, want)
		}
	}
}

// A version 2 ladder is stored without lookbacks. The window still has to show
// the dots, so they are rebuilt from the snapshots already on disk and the
// payload is served in the same shape the API returns.
func TestSyntheticPriorsMatchStoredHistory(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: dir + "/Tickers", CollectAllEndpoints: true, ProfileSampleSeconds: 1}
	dw := NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dw.Close() })

	et := utils.GetMarketTimezone()
	when := time.Date(2026, 10, 16, 11, 0, 0, 0, et)
	date := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, et)
	t0 := float64(when.Unix())
	last := t0 + 1800

	offsets := []float64{0, 900, 1500, 1740, 1800}
	for i, offset := range offsets {
		value := jsonNumber((i + 1) * 10)
		dw.mu.Lock()
		dw.pendingWrites["SPX"] = append(dw.pendingWrites["SPX"], &PendingWrite{
			Ticker:    "SPX",
			Timestamp: t0 + offset,
			Scalars:   map[string]interface{}{"spot": 10.0},
			Profiles: map[string]interface{}{
				"classic_zero": map[string]interface{}{
					"spot":    10.0,
					"strikes": json.RawMessage(`[[100,` + value + `,0]]`),
				},
				"gamma_zero": map[string]interface{}{
					"mini_contracts": json.RawMessage(`[[100,0.4,0.5,` + value + `]]`),
				},
			},
			Date: date,
		})
		dw.mu.Unlock()
		if err := dw.FlushTicker("SPX"); err != nil {
			t.Fatal(err)
		}
	}

	db, err := sql.Open("sqlite", ProfileDBPath(dw.getDBPath("SPX", date)))
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	err = db.QueryRow(`SELECT payload FROM endpoint_profile WHERE endpoint = ? AND timestamp = ?`, "classic_zero", last).Scan(&stored)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(stored))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	zr.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`[100,50,0]`)) {
		t.Fatalf("stored ladder should have no lookbacks: %s", raw)
	}

	dl := NewDataLoader(settings, func(string, string) {})
	t.Cleanup(func() { _ = dl.Close() })
	snap, err := dl.LoadProfile("SPX", date, &last, []string{"classic_zero", "gamma_zero"})
	if err != nil {
		t.Fatal(err)
	}
	classic := string(snap.Profiles["classic_zero"])
	// 1m, 5m, 10m, 15m, 30m before the requested sample.
	if !bytes.Contains([]byte(classic), []byte(`[100,50,0,[40,30,20,20,10]]`)) {
		t.Fatalf("classic lookbacks = %s", classic)
	}
	greek := string(snap.Profiles["gamma_zero"])
	// 5m, 15m, 30m before the requested sample.
	if !bytes.Contains([]byte(greek), []byte(`[100,0.4,0.5,50,[30,20,10],null,null]`)) {
		t.Fatalf("greek lookbacks = %s", greek)
	}
}

// Reads a real day folder when one is present and checks that the loaders still
// return sorted chart series and an untrimmed ladder for a legacy day. Skips in
// a clean checkout.
func TestReadExistingDayIsStable(t *testing.T) {
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
		date, err := time.Parse("01.02.2006", strings.TrimPrefix(entry.Name(), "Tickers "))
		if err != nil {
			continue
		}
		days = append(days, dayFolder{name: entry.Name(), date: date})
	}
	if len(days) == 0 {
		t.Skip("no day folders in the repo")
	}
	sort.Slice(days, func(i, j int) bool { return days[i].date.After(days[j].date) })
	t.Logf("day folders found: %d (newest %s), root %s", len(days), days[0].name, root)

	for _, day := range days {
		if _, err := os.Stat(filepath.Join(root, day.name, "SPY.db")); err != nil {
			continue
		}
		settings := &config.Settings{DataDirectory: filepath.Join(root, "Tickers"), CollectAllEndpoints: true}
		dl := NewDataLoader(settings, func(string, string) {})
		t.Cleanup(func() { _ = dl.Close() })
		date := time.Date(day.date.Year(), day.date.Month(), day.date.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())

		chart, err := dl.LoadChartData("SPY", date, 5000, nil)
		if err != nil {
			t.Fatal(err)
		}
		times := chart["timestamp"]
		for i := 1; i < len(times); i++ {
			if times[i] <= times[i-1] {
				t.Fatalf("chart timestamps are not ascending at %d", i)
			}
		}
		if got := len(chart["spot"]); got != 0 && got != len(times) {
			t.Fatalf("spot series length %d, timestamps %d", got, len(times))
		}

		snap, err := dl.LoadProfile("SPY", date, nil, []string{"classic_zero"})
		if err != nil {
			t.Fatal(err)
		}
		body := snap.Profiles["classic_zero"]
		if len(body) == 0 {
			continue
		}
		if bytes.Contains(body, []byte(`"_f"`)) || bytes.Contains(body, []byte(`"_d"`)) {
			t.Fatalf("storage marker served from %s: %s", day.name, body)
		}
		if !bytes.Contains(body, []byte(`"strikes"`)) {
			t.Fatalf("classic ladder lost its strikes in %s: %s", day.name, body)
		}
		return
	}
	t.Skip("no day folder with SPY data")
}

// TestProfileAtLiveWidensPastLongDeltaRun reproduces the corruption an older
// build left behind: its flushes were dropped by lock contention, so a delta
// chain could grow past profileKeyframeEvery without a keyframe. Reading the
// newest sample must widen its window until it finds the full ladder instead
// of failing with "missing its full ladder".
func TestProfileAtLiveWidensPastLongDeltaRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SPY.profiles.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE endpoint_profile (
		timestamp REAL, endpoint TEXT, payload BLOB,
		PRIMARY KEY(timestamp, endpoint)) WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}

	const endpoint = "classic_zero"
	store := func(ts float64, raw string) {
		t.Helper()
		payload, err := gzipJSON(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO endpoint_profile VALUES (?, ?, ?)`, ts, endpoint, payload); err != nil {
			t.Fatal(err)
		}
	}

	store(1, `{"spot":0,"strikes":[[100,1,0]]}`)
	for i := 1; i <= 45; i++ {
		raw := `{"_d":1,"n":` + strconv.Itoa(i) + `,"set":{"spot":` + strconv.Itoa(i) + `}}`
		store(float64(i+1), raw)
	}

	body, ts, chain, err := profileAtLive(db, endpoint, nil)
	if err != nil {
		t.Fatalf("profileAtLive: %v", err)
	}
	if !bytes.Contains(body, []byte(`"spot":45`)) {
		t.Fatalf("rebuilt body did not reach the last delta: %s", body)
	}
	if ts != 46 {
		t.Fatalf("newest timestamp = %v, want 46", ts)
	}
	if chain != 45 {
		t.Fatalf("chain = %d, want 45", chain)
	}
}
