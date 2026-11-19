package backfill

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"market-terminal/internal/api"
	"market-terminal/internal/config"
	"market-terminal/internal/database"
	"market-terminal/internal/utils"
)

// A session is recovered once. A second pass must write nothing, so a nightly
// retry cannot duplicate rows or burn requests.
func TestEODPassIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	et := utils.GetMarketTimezone()
	date := time.Date(2026, 11, 17, 0, 0, 0, 0, et)
	open, _ := utils.MarketOpenCloseTimes(date)
	base := open.Unix()

	archive := buildArchive(t, "SPX", "classic", "gex_zero", date, base)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hist/eod/SPX" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="SPX_2026-11-17.zip"`)
		w.Header().Set("Content-Type", "application/zip")
		w.Write(archive)
	}))
	defer server.Close()

	settings := &config.Settings{
		DataDirectory:        filepath.Join(dir, "Tickers"),
		APISubscriptionTiers: []string{"classic"},
		CollectAllEndpoints:  true,
		EODBackfillEnabled:   true,
		EODMaxLadderMB:       200,
		ProfileSampleSeconds: 1,
		TickerConfigs:        map[string]config.TickerConfig{"SPX": {CollectionEnabled: true}},
	}
	client := api.NewClient("test-key", func(string, string) {})
	client.SetBaseURL(server.URL)
	defer client.Close()
	writer := database.NewDataWriter(settings, func(string, string) {})
	defer writer.Close()

	manager := New(client, writer, func() *config.Settings { return settings }, func(string, string) {})
	first := manager.RunEOD(context.Background(), date)
	if first.LastError != "" {
		t.Fatalf("pass error: %s", first.LastError)
	}
	if first.Scalars != 10 || first.Ladders != 10 {
		t.Fatalf("first pass scalars=%d ladders=%d, want 10 each", first.Scalars, first.Ladders)
	}

	chartPath := database.DBPath(settings.DataDirectory, "SPX", date, nil, "backfill")
	chartDB, err := sql.Open("sqlite", chartPath)
	if err != nil {
		t.Fatal(err)
	}
	defer chartDB.Close()
	var rows int
	if err := chartDB.QueryRow(`SELECT count(*) FROM ticker_data`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 10 {
		t.Fatalf("chart rows = %d", rows)
	}
	var spot sql.NullFloat64
	if err := chartDB.QueryRow(`SELECT spot FROM ticker_data WHERE timestamp = ?`, float64(base)).Scan(&spot); err != nil {
		t.Fatal(err)
	}
	if !spot.Valid || spot.Float64 != 6000 {
		t.Fatalf("recovered spot = %+v", spot)
	}

	fillDB, err := sql.Open("sqlite", database.ProfileDBPath(chartPath))
	if err != nil {
		t.Fatal(err)
	}
	defer fillDB.Close()
	var ladders int
	if err := fillDB.QueryRow(`SELECT count(*) FROM endpoint_profile_fill WHERE endpoint = 'classic_zero'`).Scan(&ladders); err != nil {
		t.Fatal(err)
	}
	if ladders != 10 {
		t.Fatalf("filled ladders = %d", ladders)
	}
	var live int
	if err := fillDB.QueryRow(`SELECT count(*) FROM endpoint_profile`).Scan(&live); err != nil && !strings.Contains(err.Error(), "no such table") {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("recovery wrote %d rows into the live delta chain", live)
	}

	second := manager.RunEOD(context.Background(), date)
	if second.Scalars != 0 || second.Ladders != 0 {
		t.Fatalf("second pass wrote scalars=%d ladders=%d, want 0", second.Scalars, second.Ladders)
	}
}

// A filled ladder must be readable through the normal profile path without
// touching the live chain.
func TestFilledLadderIsServed(t *testing.T) {
	dir := t.TempDir()
	et := utils.GetMarketTimezone()
	date := time.Date(2026, 11, 17, 0, 0, 0, 0, et)
	open, _ := utils.MarketOpenCloseTimes(date)
	base := float64(open.Unix()) + 60

	settings := &config.Settings{
		DataDirectory:        filepath.Join(dir, "Tickers"),
		CollectAllEndpoints:  true,
		ProfileSampleSeconds: 5,
	}
	writer := database.NewDataWriter(settings, func(string, string) {})
	defer writer.Close()
	payload := []byte(`{"strikes":[[6000,11,0,[1,2,3,4,5]]]}`)
	if _, _, err := writer.WriteFillLadders("SPX", date, []database.LadderFill{{
		Timestamp: base,
		Endpoint:  "classic_zero",
		Payload:   payload,
	}}, 0); err != nil {
		t.Fatal(err)
	}

	loader := database.NewDataLoader(settings, func(string, string) {})
	defer loader.Close()
	snap, err := loader.LoadProfile("SPX", date, &base, []string{"classic_zero"})
	if err != nil {
		t.Fatal(err)
	}
	body := string(snap.Profiles["classic_zero"])
	if !bytes.Contains([]byte(body), []byte(`[6000,11,0`)) {
		t.Fatalf("filled ladder = %s", body)
	}
	if snap.Timestamp != base {
		t.Fatalf("snapshot timestamp = %v, want %v", snap.Timestamp, base)
	}
}

func TestMissingRangesIgnoresShortGaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SPX.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE ticker_data (timestamp REAL PRIMARY KEY) WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []float64{1000, 1001, 1101, 1999} {
		if _, err := db.Exec(`INSERT INTO ticker_data (timestamp) VALUES (?)`, ts); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	gaps, err := missingRanges(path, 1000, 2000, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 2 {
		t.Fatalf("gaps = %+v", gaps)
	}
	if gaps[0].start != 1002 || gaps[0].end != 1100 {
		t.Fatalf("first gap = %+v", gaps[0])
	}
	if gaps[1].start != 1102 || gaps[1].end != 1998 {
		t.Fatalf("second gap = %+v", gaps[1])
	}

	whole, err := missingRanges(filepath.Join(t.TempDir(), "ABSENT.db"), 1000, 2000, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 1 || whole[0].start != 1000 || whole[0].end != 2000 {
		t.Fatalf("missing file gaps = %+v", whole)
	}
}

// buildArchive builds the gzipped JSON entries the real archive holds.
func buildArchive(t *testing.T, ticker, pkg, category string, date time.Time, base int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	name := fmt.Sprintf("%s/%s/%s/%s_%s.json.gz", ticker, pkg, category, date.Format("2006-01-02"), category)
	entry, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(entry)
	for i := int64(0); i < 10; i++ {
		sample := map[string]interface{}{
			"timestamp":  float64(base + i),
			"spot":       6000.0 + float64(i),
			"zero_gamma": 5990.0,
			"strikes":    json.RawMessage(`[[6000,11,0,[1,2,3,4,5]],[6010,` + fmt.Sprint(i) + `,0,[1,2,3,4,5]]]`),
		}
		line, err := json.Marshal(sample)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gz.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The ledger lives next to the day folders and survives a restart.
func TestStateStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eod-state.json")
	store := newStateStore(path)
	store.set("2026-11-17", "SPX", record{Source: "eod", ArchiveDate: "2026-11-17", Scalars: 3, Ladders: 2, At: time.Now()})

	reloaded := newStateStore(path)
	got, ok := reloaded.record("2026-11-17", "SPX")
	if !ok || got.Scalars != 3 || got.Ladders != 2 {
		t.Fatalf("record = %+v ok=%v", got, ok)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
