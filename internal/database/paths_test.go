package database

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"market-terminal/internal/utils"
)

func TestMarketDataDirWeekendRollsToFriday(t *testing.T) {
	loc := utils.GetMarketTimezone()
	cases := []struct {
		name string
		date time.Time
		want string
	}{
		{"wednesday", time.Date(2026, 10, 14, 15, 0, 0, 0, loc), "Tickers 10.14.2026"},
		{"saturday", time.Date(2026, 10, 17, 12, 0, 0, 0, loc), "Tickers 10.16.2026"},
		{"sunday", time.Date(2026, 10, 18, 12, 0, 0, 0, loc), "Tickers 10.16.2026"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MarketDataDir("Tickers", tc.date); got != tc.want {
				t.Fatalf("MarketDataDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDataRootKeepsAbsolutePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Tickers")
	if got := DataRoot(root); got != root {
		t.Fatalf("DataRoot() = %q", got)
	}
}

func TestDataRootRelativeStaysRelativeForTestBinary(t *testing.T) {
	if got := DataRoot("Tickers"); got != "Tickers" {
		t.Fatalf("DataRoot() = %q, want Tickers", got)
	}
}

func TestDBPathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 10, 16, 12, 0, 0, 0, utils.GetMarketTimezone())
	if got := DBPath(root, "../x", day, nil, "test"); got != "" {
		t.Fatalf("DBPath() = %q, want empty", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "x.db")); err == nil {
		t.Fatal("rejected ticker created a database outside the data directory")
	}
}

func TestMarketDataDirEmptyUsesTickers(t *testing.T) {
	loc := utils.GetMarketTimezone()
	day := time.Date(2026, 10, 14, 12, 0, 0, 0, loc)
	if got := MarketDataDir("", day); got != "Tickers 10.14.2026" {
		t.Fatalf("MarketDataDir() = %q", got)
	}
}

func TestDBPathCreatesDailyFile(t *testing.T) {
	loc := utils.GetMarketTimezone()
	root := t.TempDir()
	day := time.Date(2026, 10, 17, 12, 0, 0, 0, loc) // Saturday → Friday
	got := DBPath(root, "SPX", day, nil, "test")
	want := filepath.Join(root+" 10.16.2026", "SPX.db")
	if got != want {
		t.Fatalf("DBPath() = %q, want %q", got, want)
	}
	if info, err := os.Stat(filepath.Dir(got)); err != nil || !info.IsDir() {
		t.Fatalf("data dir not created: %v", err)
	}
}
