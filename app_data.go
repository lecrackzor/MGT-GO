package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"market-terminal/internal/api"
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
// Reads through LoadChartData on a read-only connection. The one write it
// makes is the Ninja major levels it fetches for the day (see
// attachHistoricalMajors); the chart rows themselves are only read.
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
		// Collected already, not plotted by default.
		"sum_gex_vol",
		"sum_gex_oi",
		"major_call_gamma",
		"major_put_gamma",
		"zgr",
		"ocvr",
		"net_dex",
		"agg_dex",
		"gexoflow",
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
	a.attachHistoricalMajors(ticker, date, result)

	return result, nil
}

// attachHistoricalMajors fills major_call_gamma and major_put_gamma from the
// same Ninja history feed the gexbot indicator draws. Live major_pos_vol and
// major_neg_vol stay as the positive and negative gamma snapshots.
//
// A value already stored in the day wins. The feed is only asked to fill bars
// that are still empty, and whatever it returns is written back, so a session
// drawn once keeps both lines after the feed has pruned it.
func (a *App) attachHistoricalMajors(ticker string, date time.Time, result map[string]interface{}) {
	if a.apiClient == nil || a.settingsManager == nil {
		return
	}
	settings := a.settingsManager.GetSettings()
	if settings == nil || strings.TrimSpace(settings.NinjaAPIKey) == "" {
		return
	}
	times, ok := result["timestamp"].(database.ChartSeries)
	if !ok || len(times) == 0 {
		return
	}
	start, end := usSessionBounds(date)
	samples, err := a.historicalMajors(context.Background(), settings.NinjaAPIKey, ticker, date, start, end)
	if err != nil {
		a.debugPrint(fmt.Sprintf("GetChartData: historical majors for %s: %v", ticker, err), "app")
		return
	}
	if len(samples) == 0 {
		return
	}
	call, put := alignHistoricalMajors(times, samples)
	result["major_call_gamma"] = mergeMajorLevels(seriesOf(result["major_call_gamma"], len(times)), call)
	result["major_put_gamma"] = mergeMajorLevels(seriesOf(result["major_put_gamma"], len(times)), put)
	// Store what the feed just gave us. A chart is the only way some days are
	// ever drawn, and the feed forgets a session, so this is the one chance to
	// keep the levels for that day.
	if stored, err := a.storeMajorLevels(ticker, date, samples); err != nil {
		a.debugPrint(fmt.Sprintf("GetChartData: store historical majors for %s: %v", ticker, err), "error")
	} else if stored.Filled > 0 {
		a.debugPrint(fmt.Sprintf("GetChartData: stored %d major level row(s) for %s on %s", stored.Filled, ticker, date.Format("2006-01-02")), "app")
	}
}

// mergeMajorLevels keeps every stored value and fills the gaps from the feed.
func mergeMajorLevels(stored, aligned database.ChartSeries) database.ChartSeries {
	out := make(database.ChartSeries, len(aligned))
	for i := range aligned {
		if i < len(stored) && isLevelValue(stored[i]) {
			out[i] = stored[i]
			continue
		}
		out[i] = aligned[i]
	}
	return out
}

// seriesOf returns a chart column as a float series of exactly n values, with
// NaN where the column is shorter, longer, or not present.
func seriesOf(v interface{}, n int) database.ChartSeries {
	out := make(database.ChartSeries, n)
	for i := range out {
		out[i] = math.NaN()
	}
	switch series := v.(type) {
	case database.ChartSeries:
		for i := 0; i < n && i < len(series); i++ {
			out[i] = series[i]
		}
	case []interface{}:
		for i := 0; i < n && i < len(series); i++ {
			if f, ok := series[i].(float64); ok {
				out[i] = f
			}
		}
	case []float64:
		for i := 0; i < n && i < len(series); i++ {
			out[i] = series[i]
		}
	}
	return out
}

func usSessionBounds(date time.Time) (int64, int64) {
	loc := utils.GetMarketTimezone()
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	now := time.Now().In(loc)
	open := day.Add(9*time.Hour + 30*time.Minute)
	if day.Year() == now.Year() && day.YearDay() == now.YearDay() && now.Before(open) {
		day = day.AddDate(0, 0, -1)
		for day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			day = day.AddDate(0, 0, -1)
		}
		open = day.Add(9*time.Hour + 30*time.Minute)
	}
	closeAt := day.Add(16 * time.Hour)
	if closeAt.After(now) {
		closeAt = now
	}
	return open.Unix(), closeAt.Unix()
}

func alignHistoricalMajors(times database.ChartSeries, samples []api.HistMajor) (database.ChartSeries, database.ChartSeries) {
	sort.Slice(samples, func(i, j int) bool { return samples[i].Timestamp < samples[j].Timestamp })
	call := make(database.ChartSeries, len(times))
	put := make(database.ChartSeries, len(times))
	for i := range call {
		call[i] = math.NaN()
		put[i] = math.NaN()
	}
	if len(samples) == 0 {
		return call, put
	}
	idx := 0
	for i, ts := range times {
		bar := int64(ts)
		for idx+1 < len(samples) && samples[idx+1].Timestamp <= bar {
			idx++
		}
		sample := samples[idx]
		if sample.Timestamp > bar || bar-sample.Timestamp > 300 {
			continue
		}
		if sample.Call != 0 {
			call[i] = sample.Call
		}
		if sample.Put != 0 {
			put[i] = sample.Put
		}
	}
	return call, put
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
