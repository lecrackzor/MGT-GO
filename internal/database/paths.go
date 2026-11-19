package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

// DataRoot is the folder-name prefix for daily ticker directories.
// A relative name is resolved next to the executable. The working directory
// is not used, so starting the app from another folder still opens the same files.
// Test and go-run binaries live under the temp directory and keep the relative name.
func DataRoot(dataDir string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = "Tickers"
	}
	if filepath.IsAbs(dataDir) {
		return dataDir
	}
	exe, err := os.Executable()
	if err != nil {
		return dataDir
	}
	// Tests and go run use another binary name. Only the installed app
	// should pin its data folder to the executable directory.
	if !strings.EqualFold(filepath.Base(exe), "market-terminal.exe") {
		return dataDir
	}
	return filepath.Join(filepath.Dir(exe), dataDir)
}

// MarketDataDir returns the daily data directory for a ticker date.
// Weekends and full NYSE closures roll back to the previous trading day. The 8:30 ET session
// rollover is applied by the caller (GetMarketDate / GetMarketDateForDate)
// before this function sees the date.
func MarketDataDir(dataDir string, date time.Time) string {
	dataDir = DataRoot(dataDir)

	dateET := date.In(utils.GetMarketTimezone())
	// Weekends and full closures use the previous trading day. The 8:30 ET
	// session rollover is applied by the caller before this function sees the date.
	marketDate := utils.GetLastTradingDay(dateET)

	return fmt.Sprintf("%s %s", dataDir, marketDate.Format("01.02.2006"))
}

// DBPath returns the SQLite file path for a ticker and date, creating the
// directory when it does not exist.
func DBPath(dataDir, ticker string, date time.Time, debugPrint func(string, string), category string) string {
	if !config.ValidTicker(ticker) {
		if debugPrint != nil {
			debugPrint(fmt.Sprintf("DBPath: rejected ticker %q", ticker), category)
		}
		return ""
	}
	dir := MarketDataDir(dataDir, date)
	if debugPrint != nil {
		debugPrint(fmt.Sprintf("getDBPath: %s -> %s", ticker, dir), category)
	}
	if err := os.MkdirAll(dir, 0755); err != nil && debugPrint != nil {
		debugPrint(fmt.Sprintf("getDBPath: WARNING - Failed to create directory %s: %v", dir, err), "error")
	}
	return filepath.Join(dir, fmt.Sprintf("%s.db", ticker))
}

// ProfileDBPath is the sibling file that holds one gzipped ladder per endpoint.
// The chart file and this file can be copied separately.
func ProfileDBPath(chartPath string) string {
	ext := filepath.Ext(chartPath)
	base := strings.TrimSuffix(chartPath, ext)
	return base + ".profiles.db"
}
