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

// MissingDataHint explains where the day folders went when a relative data
// directory resolves next to the executable but the data lives elsewhere. A
// copied build started from a build folder otherwise opens an empty window and
// creates a new day folder beside itself without saying so.
//
// It returns "" when there is nothing to report: the resolved root already has
// day folders, the working directory has none either, or the two point at the
// same tree. The working directory is the only other place checked, so this
// costs two directory reads and never walks the disk.
func MissingDataHint(dataDir string) string {
	if filepath.IsAbs(strings.TrimSpace(dataDir)) {
		return ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return missingDataHint(DataRoot(dataDir), cwd)
}

// missingDataHint holds the decision so a test can supply both roots.
func missingDataHint(root, cwd string) string {
	if hasDayFolders(root) {
		return ""
	}
	elsewhere := filepath.Join(cwd, filepath.Base(root))
	if strings.EqualFold(filepath.Clean(elsewhere), filepath.Clean(root)) || !hasDayFolders(elsewhere) {
		return ""
	}
	return fmt.Sprintf("data directory %s has no day folders; %s does, so start the app from that folder or set data_directory to it", root, elsewhere)
}

// hasDayFolders reports whether root's day folders exist. A day folder is named
// "<root> MM.DD.YYYY" and sits beside the prefix, so the parent is what holds it.
func hasDayFolders(root string) bool {
	entries, err := os.ReadDir(filepath.Dir(root))
	if err != nil {
		return false
	}
	prefix := filepath.Base(root) + " "
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), prefix) && isDayFolderName(entry.Name()) {
			return true
		}
	}
	return false
}

func isDayFolderName(name string) bool {
	if len(name) < 12 || name[len(name)-11] != ' ' {
		return false
	}
	for _, r := range name[len(name)-10:] {
		if r != '.' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
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
