package database

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"market-terminal/internal/utils"
)

func TestPruneOldDaysKeepsTodayAndRecent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Tickers")
	location := utils.GetMarketTimezone()
	now := time.Date(2026, 11, 18, 11, 0, 0, 0, location)

	mkdir := func(name string) string {
		path := filepath.Join(filepath.Dir(root), name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	prefix := filepath.Base(root) + " "
	today := mkdir(prefix + now.Format(dayFolderLayout))
	recent := mkdir(prefix + now.AddDate(0, 0, -3).Format(dayFolderLayout))
	old := mkdir(prefix + now.AddDate(0, 0, -40).Format(dayFolderLayout))
	other := mkdir("backup 11.01.2026")

	removed, err := PruneOldDays(root, 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %v, want one folder", removed)
	}
	for _, path := range []string{today, recent, other} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s was removed: %v", path, err)
		}
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old folder still exists: %v", err)
	}

	// 0 disables pruning entirely.
	kept, err := PruneOldDays(root, 0, now)
	if err != nil || len(kept) != 0 {
		t.Fatalf("keep-all removed %v (%v)", kept, err)
	}
}
