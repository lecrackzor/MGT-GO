package database

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
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
	settings := &config.Settings{DataDirectory: dir + "/Tickers", CollectAllEndpoints: true}
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
	if !bytes.Contains(body, []byte(`[[100,1,0,[9,8,7,6,5]]`)) || !bytes.Contains(body, []byte(`[110,9,0,[4,3,2,1,0]]`)) {
		t.Fatalf("rebuilt profile = %s", body)
	}
	earlier, err := dl.LoadProfile("SPX", date, &t0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(earlier.Profiles["classic_zero"], []byte(`[110,2,0,[1,1,1,1,1]]`)) {
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
	if bytes.Contains(raw, []byte(`[100,1,0,[9,8,7,6,5]]`)) {
		t.Fatalf("stored change still contains the quiet strike: %s", raw)
	}
}

func TestProfileDeltaRoundTripWhenStrikeReturns(t *testing.T) {
	dir := t.TempDir()
	settings := &config.Settings{DataDirectory: dir + "/Tickers", CollectAllEndpoints: true}
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
