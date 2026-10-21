package utils

import (
	"log"
	"time"
)

// MARKET_TIMEZONE represents US Eastern Time (where market operates)
// Go uses "America/New_York" for Eastern Time
var MARKET_TIMEZONE *time.Location

func init() {
	var err error
	MARKET_TIMEZONE, err = time.LoadLocation("America/New_York")
	if err != nil {
		// Log warning - this should not happen if time/tzdata is imported in main.go
		log.Printf("WARNING: Failed to load America/New_York timezone: %v - falling back to UTC (market hours will be incorrect!)", err)
		MARKET_TIMEZONE = time.UTC
	} else {
		log.Printf("[TIMEZONE] Successfully loaded America/New_York timezone")
	}
}

// GetMarketTimezone returns the market timezone (Eastern Time)
// This allows other packages to access MARKET_TIMEZONE
func GetMarketTimezone() *time.Location {
	return MARKET_TIMEZONE
}

// NowMarketTime returns current time in market timezone (Eastern Time)
func NowMarketTime() time.Time {
	return time.Now().In(MARKET_TIMEZONE)
}

// MarketOpenCloseTimes returns market open and close times for a given date in Eastern Time.
// A regular session is 9:30 AM–4:00 PM ET. An NYSE early close ends at 1:00 PM ET.
func MarketOpenCloseTimes(date time.Time) (time.Time, time.Time) {
	date = date.In(MARKET_TIMEZONE)

	marketOpen := time.Date(date.Year(), date.Month(), date.Day(), 9, 30, 0, 0, MARKET_TIMEZONE)
	closeHour, closeMin := 16, 0
	if isEarlyClose(date) {
		closeHour, closeMin = 13, 0
	}
	marketClose := time.Date(date.Year(), date.Month(), date.Day(), closeHour, closeMin, 0, 0, MARKET_TIMEZONE)

	return marketOpen, marketClose
}

// IsMarketOpen reports whether the US stock market is open right now.
func IsMarketOpen() bool {
	return IsMarketOpenAt(NowMarketTime())
}

// IsMarketOpenAt reports whether the market is open at a specific Eastern Time.
// Weekends and full NYSE closures are closed. The open and close minutes count as open.
func IsMarketOpenAt(now time.Time) bool {
	now = now.In(MARKET_TIMEZONE)
	if IsWeekend(now) || IsFullMarketClosure(now) {
		return false
	}
	marketOpen, marketClose := MarketOpenCloseTimes(now)
	return !now.Before(marketOpen) && !now.After(marketClose)
}

// IsAfterHours checks if current time is after hours.
// After hours includes pre-market (before 9:25 AM), post-market (after 5 minutes past the close), weekends, and full closures.
func IsAfterHours() bool {
	now := NowMarketTime()

	if IsWeekend(now) || IsFullMarketClosure(now) {
		return true
	}

	marketOpen, marketClose := MarketOpenCloseTimes(now)
	extendedOpen := marketOpen.Add(-5 * time.Minute)  // 5 minutes before open
	extendedClose := marketClose.Add(5 * time.Minute) // 5 minutes after close

	return now.Before(extendedOpen) || now.After(extendedClose)
}

// IsWeekend checks if a date falls on a weekend (Saturday or Sunday)
func IsWeekend(date time.Time) bool {
	date = date.In(MARKET_TIMEZONE)
	weekday := date.Weekday()
	return weekday == time.Saturday || weekday == time.Sunday
}

// GetLastTradingDay walks backward over weekends and full NYSE closures.
// A weekday that is not a closure is returned unchanged.
func GetLastTradingDay(date time.Time) time.Time {
	date = date.In(MARKET_TIMEZONE)
	for IsWeekend(date) || IsFullMarketClosure(date) {
		date = date.AddDate(0, 0, -1)
	}
	return date
}

// NextSessionOpen is the next 9:30 ET open at or after now.
// A full closure and a session that has already closed are skipped.
func NextSessionOpen(now time.Time) time.Time {
	now = now.In(MARKET_TIMEZONE)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, MARKET_TIMEZONE)
	for i := 0; i < 14; i++ {
		day := start.AddDate(0, 0, i)
		if IsWeekend(day) || IsFullMarketClosure(day) {
			continue
		}
		open, closeAt := MarketOpenCloseTimes(day)
		if i == 0 && !now.Before(closeAt) {
			continue
		}
		return open
	}
	return time.Time{}
}

// GetMarketDate returns the session date in Eastern Time.
// The session rolls at 8:30 AM ET. Weekends and full NYSE closures use the previous trading day,
// and so does the morning before the rollover.
func GetMarketDate() time.Time {
	now := NowMarketTime()
	rolloverTime := time.Date(now.Year(), now.Month(), now.Day(), 8, 30, 0, 0, MARKET_TIMEZONE)
	if now.Before(rolloverTime) {
		now = now.AddDate(0, 0, -1)
	}
	return GetLastTradingDay(now)
}

// GetMarketDateForDate converts any date to the session date.
// Before 8:30 AM ET belongs to the previous day. Weekends and full closures belong to the previous trading day.
func GetMarketDateForDate(date time.Time) time.Time {
	date = date.In(MARKET_TIMEZONE)

	rolloverTime := time.Date(date.Year(), date.Month(), date.Day(), 8, 30, 0, 0, MARKET_TIMEZONE)
	if date.Before(rolloverTime) {
		date = date.AddDate(0, 0, -1)
	}
	return GetLastTradingDay(date)
}

// ParseDateInET parses a date string in "YYYY-MM-DD" format, assuming it's in Eastern Time
// This ensures date strings are interpreted as ET dates, not UTC
// Returns a time.Time at midnight ET for the given date
func ParseDateInET(dateStr string) (time.Time, error) {
	// Parse the date string to extract year, month, day components
	// Use ParseInLocation to parse directly in ET timezone, avoiding UTC conversion
	// Format: "2006-01-02" (YYYY-MM-DD)
	layout := "2006-01-02"
	t, err := time.ParseInLocation(layout, dateStr, MARKET_TIMEZONE)
	if err != nil {
		return time.Time{}, err
	}
	// Return the date at midnight ET (ParseInLocation already creates it in the correct timezone)
	return t, nil
}

// NYSE cash-session closures and 1:00 PM ET early closes, 2026–2028.
// Source: NYSE Group holiday calendar announced for those years.
var nyseFullClosures = map[string]struct{}{
	"2026-01-01": {},
	"2026-01-19": {},
	"2026-02-16": {},
	"2026-04-03": {},
	"2026-05-25": {},
	"2026-06-19": {},
	"2026-07-03": {},
	"2026-09-07": {},
	"2026-11-26": {},
	"2026-12-25": {},

	"2027-01-01": {},
	"2027-01-18": {},
	"2027-02-15": {},
	"2027-03-26": {},
	"2027-05-31": {},
	"2027-06-18": {},
	"2027-07-05": {},
	"2027-09-06": {},
	"2027-11-25": {},
	"2027-12-24": {},

	"2028-01-17": {},
	"2028-02-21": {},
	"2028-04-14": {},
	"2028-05-29": {},
	"2028-06-19": {},
	"2028-07-04": {},
	"2028-09-04": {},
	"2028-11-23": {},
	"2028-12-25": {},
}

var nyseEarlyCloses = map[string]struct{}{
	"2026-11-27": {},
	"2026-12-24": {},
	"2027-11-26": {},
	"2028-07-03": {},
	"2028-11-24": {},
}

func sessionDateKey(date time.Time) string {
	date = date.In(MARKET_TIMEZONE)
	return date.Format("2006-01-02")
}

// IsFullMarketClosure reports an NYSE session that does not open at all.
func IsFullMarketClosure(date time.Time) bool {
	_, ok := nyseFullClosures[sessionDateKey(date)]
	return ok
}

func isEarlyClose(date time.Time) bool {
	_, ok := nyseEarlyCloses[sessionDateKey(date)]
	return ok
}
