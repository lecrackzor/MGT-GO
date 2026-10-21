package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"market-terminal/internal/database"
	"market-terminal/internal/utils"
)

// GetCurrentMarketDate returns the current market date in Eastern Time as "YYYY-MM-DD"
// Date rolls over at 8:30 AM ET (1 hour before market open)
func (a *App) GetCurrentMarketDate() string {
	marketDate := utils.GetMarketDate()
	return marketDate.Format("2006-01-02")
}

// GetAvailableDates returns a list of available dates (newest first) from data directories
// Scans for directories matching "Tickers MM.DD.YYYY" pattern
// Returns dates in "YYYY-MM-DD" format, sorted newest first
func (a *App) GetAvailableDates() []string {
	settings := a.settingsManager.GetSettings()
	dataDir := settings.DataDirectory
	if dataDir == "" {
		dataDir = "Tickers"
	}

	root := database.DataRoot(dataDir)
	prefix := filepath.Base(root) + " "
	currentDir := filepath.Dir(root)
	if currentDir == "" {
		currentDir = "."
	}

	entries, err := os.ReadDir(currentDir)
	if err != nil {
		a.debugPrint(fmt.Sprintf("GetAvailableDates: Failed to read directory: %v", err), "error")
		return []string{}
	}

	var availableDates []time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		// Extract date string (format: "MM.DD.YYYY")
		dateStr := strings.TrimPrefix(name, prefix)

		// Parse date
		date, err := time.Parse("01.02.2006", dateStr)
		if err != nil {
			// Skip invalid date formats
			continue
		}

		// A weekend folder is not a session. Friday is the last valid day.
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			continue
		}
		dirPath := filepath.Join(currentDir, name)
		files, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}

		hasData := false
		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(file.Name(), ".db") {
				hasData = true
				break
			}
		}

		if hasData {
			availableDates = append(availableDates, date)
		}
	}

	// Sort newest first
	sort.Slice(availableDates, func(i, j int) bool {
		return availableDates[i].After(availableDates[j])
	})

	// The current session is included even when its folder is still empty.
	// On a weekend that session is Friday, not Saturday or Sunday.
	currentMarket := utils.GetMarketDate()
	todayMidnight := time.Date(currentMarket.Year(), currentMarket.Month(), currentMarket.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())
	todayStr := todayMidnight.Format("2006-01-02")
	hasToday := false
	for _, d := range availableDates {
		if d.Format("2006-01-02") == todayStr {
			hasToday = true
			break
		}
	}
	if !hasToday {
		availableDates = append([]time.Time{todayMidnight}, availableDates...)
	}

	// Convert to "YYYY-MM-DD" format strings
	result := make([]string, len(availableDates))
	for i, date := range availableDates {
		result[i] = date.Format("2006-01-02")
	}

	return result
}

// GetMarketHoursLocal returns market open and close times in user's local timezone
// Returns (openTime, closeTime) as "HH:MM" format strings
// Note: Since we can't determine the user's browser timezone from Go, we return ET times
// The frontend JavaScript will convert these to the user's local timezone
func (a *App) GetMarketHoursLocal() (string, string) {
	// Get current market date
	marketDate := utils.GetMarketDate()

	// Get market open/close times in Eastern Time
	marketOpenET, marketCloseET := utils.MarketOpenCloseTimes(marketDate)

	// Return ET times formatted as "HH:MM"
	// Frontend will convert to local timezone using JavaScript Date conversion
	openStr := marketOpenET.Format("15:04")
	closeStr := marketCloseET.Format("15:04")

	return openStr, closeStr
}

// IsMarketOpen checks if the market is currently open.
// Called by the frontend every second, so only log when the state changes.
func (a *App) IsMarketOpen() bool {
	isOpen := utils.IsMarketOpen()

	a.marketOpenLogLock.Lock()
	if a.lastLoggedMarketOpen == nil || *a.lastLoggedMarketOpen != isOpen {
		a.lastLoggedMarketOpen = &isOpen
		a.marketOpenLogLock.Unlock()
		utils.Logf("[system] IsMarketOpen: Market open state = %v (ET: %s)",
			isOpen, utils.NowMarketTime().Format("2006-01-02 15:04:05 MST"))
	} else {
		a.marketOpenLogLock.Unlock()
	}

	return isOpen
}

// GetNextMarketOpenLocalTime returns the next market open time as an RFC3339
// string with explicit ET offset - JavaScript Date parsing converts it to the
// browser's local timezone. Called repeatedly by the frontend when the market
// is closed, so it only logs when the computed time changes.
func (a *App) GetNextMarketOpenLocalTime() string {
	nowMarket := utils.NowMarketTime() // Current time in ET
	today := nowMarket

	targetOpenET := utils.NextSessionOpen(today)

	// RFC3339 includes the timezone offset, so JavaScript Date.parse()
	// correctly converts ET to the browser's local timezone
	result := targetOpenET.Format(time.RFC3339)

	a.marketOpenLogLock.Lock()
	changed := a.lastLoggedNextOpen != result
	if changed {
		a.lastLoggedNextOpen = result
	}
	a.marketOpenLogLock.Unlock()
	if changed {
		utils.Logf("[system] GetNextMarketOpenLocalTime: Next market open = %s (ET now: %s)",
			result, nowMarket.Format("2006-01-02 15:04:05 MST"))
	}

	return result
}
