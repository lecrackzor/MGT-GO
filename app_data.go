package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"market-terminal/internal/config"
	"market-terminal/internal/database"
	"market-terminal/internal/utils"
)

// GetTickerData loads ticker data from the database
// dateStr is in format "2006-01-02" (YYYY-MM-DD). Empty string means "current market date" (resolved at request time so rollover works without restart).
// Returns map[string][]interface{} where each key is a field name and value is an array of values
// Returns empty data if database doesn't exist yet (data collection hasn't started)
// Loads the latest chart row. Profile ladders are not included.
func (a *App) GetTickerData(ticker string, dateStr string) (map[string]interface{}, error) {
	if !config.ValidTicker(ticker) {
		return nil, fmt.Errorf("unknown ticker %q", ticker)
	}
	var date time.Time
	if strings.TrimSpace(dateStr) == "" {
		// Empty = "today" — resolve at request time so 8:30 AM ET rollover works without restart
		md := utils.GetMarketDate()
		date = time.Date(md.Year(), md.Month(), md.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())
	} else {
		var err error
		date, err = utils.ParseDateInET(dateStr)
		if err != nil {
			md := utils.GetMarketDate()
			date = time.Date(md.Year(), md.Month(), md.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())
		}
	}

	// Load data using lightweight LoadTickerData (skips profiles_blob)
	// This prevents massive memory usage from decompressing profiles
	data, err := a.dataLoader.LoadTickerData(ticker, date)
	if err != nil {
		a.debugPrint(fmt.Sprintf("GetTickerData: Error loading data for %s: %v", ticker, err), "error")
		return nil, err
	}

	if len(data) == 0 {
		data = map[string]interface{}{
			"timestamp":     []interface{}{},
			"spot":          []interface{}{},
			"zero_gamma":    []interface{}{},
			"major_pos_vol": []interface{}{},
			"major_neg_vol": []interface{}{},
		}
	}
	return data, nil
}

// GetChartData serves chart data for chart windows.
// READ-ONLY: Only reads from the database via LoadChartData (read-only connection). Never writes to the database.
// ticker: Ticker symbol
// dateStr: Date in format "2006-01-02" (YYYY-MM-DD)
// sinceStr: Optional; if non-empty, rows with timestamp >= sinceStr (Unix seconds, float) are returned so the last point can be replaced
func (a *App) GetChartData(ticker string, dateStr string, sinceStr string) (map[string]interface{}, error) {
	if !config.ValidTicker(ticker) {
		return nil, fmt.Errorf("unknown ticker %q", ticker)
	}
	// Parse date string in ET (not UTC)
	date, err := utils.ParseDateInET(dateStr)
	if err != nil {
		a.debugPrint(fmt.Sprintf("GetChartData: Failed to parse date '%s', using current market date: %v", dateStr, err), "error")
		// Try current market date if parsing fails
		date = utils.GetMarketDate()
		// Extract just the date part at midnight ET
		date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())
		dateStr = date.Format("2006-01-02")
	}
	a.debugPrint(fmt.Sprintf("GetChartData: Parsed date for %s: %s (original: %s, ET: %s)",
		ticker, date.Format("2006-01-02"), dateStr, date.Format("2006-01-02 15:04:05 MST")), "app")

	const maxRows = 30000 // Maximum rows to load (full trading day at 1s = ~23,400)

	var since *float64
	if sinceStr != "" {
		if v, errParse := strconv.ParseFloat(strings.TrimSpace(sinceStr), 64); errParse == nil {
			since = &v
			a.debugPrint(fmt.Sprintf("GetChartData: Incremental load for %s on %s (since=%v)", ticker, dateStr, v), "app")
		}
	}
	if since == nil {
		a.debugPrint(fmt.Sprintf("GetChartData: Loading chart data for %s on %s (max %d rows, skipping profiles)", ticker, dateStr, maxRows), "app")
	}

	data, err := a.dataLoader.LoadChartData(ticker, date, maxRows, since)
	if err != nil {
		a.debugPrint(fmt.Sprintf("GetChartData: Error loading data for %s: %v", ticker, err), "error")
		return nil, err
	}

	requiredFields := []string{
		"timestamp",
		"spot",
		"zero_gamma",
		"major_pos_vol",
		"major_neg_vol",
		"major_long_gamma",
		"major_short_gamma",
		"major_positive",
		"major_negative",
		"major_pos_oi",
		"major_neg_oi",
	}
	result := make(map[string]interface{}, len(requiredFields)+1)
	for _, field := range requiredFields {
		if values, ok := data[field]; ok {
			result[field] = values
		} else {
			result[field] = database.ChartSeries{}
		}
	}
	if since != nil {
		result["incremental"] = true
	}

	pointCount := 0
	if timestamps, ok := result["timestamp"].(database.ChartSeries); ok {
		pointCount = len(timestamps)
	}
	if pointCount == 0 {
		a.debugPrint(fmt.Sprintf("GetChartData: No data found for %s on %s", ticker, dateStr), "app")
	}

	return result, nil
}

// GetProfile returns one gunzipped profile snapshot. atStr is unix seconds.
// An empty atStr returns the latest row that has a profile.
func (a *App) GetProfile(ticker string, dateStr string, atStr string, endpointsStr string) (*database.ProfileSnapshot, error) {
	if !config.ValidTicker(ticker) {
		return nil, fmt.Errorf("unknown ticker %q", ticker)
	}
	date, err := utils.ParseDateInET(dateStr)
	if err != nil {
		date = utils.GetMarketDate()
		date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())
	}
	var at *float64
	if strings.TrimSpace(atStr) != "" {
		if v, errParse := strconv.ParseFloat(strings.TrimSpace(atStr), 64); errParse == nil {
			at = &v
		}
	}
	var endpoints []string
	if strings.TrimSpace(endpointsStr) != "" {
		endpoints = strings.Split(endpointsStr, ",")
	}
	snap, err := a.dataLoader.LoadProfile(ticker, date, at, endpoints)
	if err != nil {
		a.debugPrint(fmt.Sprintf("GetProfile: Error loading %s on %s: %v", ticker, dateStr, err), "error")
		return nil, err
	}
	return snap, nil
}

func seriesLen(v interface{}) int {
	switch s := v.(type) {
	case database.ChartSeries:
		return len(s)
	case []interface{}:
		return len(s)
	default:
		return 0
	}
}
