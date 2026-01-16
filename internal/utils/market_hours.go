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

// MarketOpenCloseTimes returns market open and close times for a given date in Eastern Time
// Market hours: 9:30 AM - 4:00 PM ET, Monday-Friday
func MarketOpenCloseTimes(date time.Time) (time.Time, time.Time) {
	// Ensure date is in market timezone
	date = date.In(MARKET_TIMEZONE)
	
	// Market opens at 9:30 AM ET
	marketOpen := time.Date(date.Year(), date.Month(), date.Day(), 9, 30, 0, 0, MARKET_TIMEZONE)
	
	// Market closes at 4:00 PM ET
	marketClose := time.Date(date.Year(), date.Month(), date.Day(), 16, 0, 0, 0, MARKET_TIMEZONE)
	
	return marketOpen, marketClose
}

// IsMarketOpen checks if the US stock market is currently open
// Market hours are 9:30 AM - 4:00 PM Eastern Time, Monday-Friday only
func IsMarketOpen() bool {
	now := NowMarketTime()
	today := now
	
	// Check if it's a weekend (Saturday=6, Sunday=0 in Go)
	weekday := today.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}
	
	marketOpen, marketClose := MarketOpenCloseTimes(today)
	return now.After(marketOpen) && now.Before(marketClose) || now.Equal(marketOpen) || now.Equal(marketClose)
}

// IsWithinExtendedHours checks if current time is within extended hours
// Extended hours: N minutes before market open and after market close
// Default is 5 minutes before 9:30 AM and 5 minutes after 4:00 PM
func IsWithinExtendedHours(extendedMinutes int) bool {
	if extendedMinutes <= 0 {
		extendedMinutes = 5 // Default 5 minutes
	}
	
	now := NowMarketTime()
	today := now
	
	// Weekends are not considered extended hours
	weekday := today.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}
	
	marketOpen, marketClose := MarketOpenCloseTimes(today)
	extendedOpen := marketOpen.Add(-time.Duration(extendedMinutes) * time.Minute)
	extendedClose := marketClose.Add(time.Duration(extendedMinutes) * time.Minute)
	
	return (now.After(extendedOpen) || now.Equal(extendedOpen)) && 
		   (now.Before(extendedClose) || now.Equal(extendedClose))
}

// IsAfterHours checks if current time is after hours
// After hours includes pre-market (before 9:25 AM), post-market (after 4:05 PM), and weekends
func IsAfterHours() bool {
	now := NowMarketTime()
	today := now
	
	// Weekends are always after hours
	weekday := today.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return true
	}
	
	marketOpen, marketClose := MarketOpenCloseTimes(today)
	extendedOpen := marketOpen.Add(-5 * time.Minute) // 5 minutes before open
	extendedClose := marketClose.Add(5 * time.Minute) // 5 minutes after close
	
	return now.Before(extendedOpen) || now.After(extendedClose)
}

// IsWeekend checks if a date falls on a weekend (Saturday or Sunday)
func IsWeekend(date time.Time) bool {
	date = date.In(MARKET_TIMEZONE)
	weekday := date.Weekday()
	return weekday == time.Saturday || weekday == time.Sunday
}

// GetLastTradingDay returns the last trading day (Friday) if the date is a weekend,
// otherwise returns the date unchanged
func GetLastTradingDay(date time.Time) time.Time {
	date = date.In(MARKET_TIMEZONE)
	weekday := date.Weekday()
	
	if weekday == time.Saturday {
		// Saturday: go back 1 day to Friday
		return date.AddDate(0, 0, -1)
	} else if weekday == time.Sunday {
		// Sunday: go back 2 days to Friday
		return date.AddDate(0, 0, -2)
	}
	
	return date
}

// GetMarketDate returns the current market date in Eastern Time
// Date rolls over at 8:30 AM ET (1 hour before market open at 9:30 AM ET)
// Before 8:30 AM ET: returns yesterday's date
// 8:30 AM ET or later: returns today's date
func GetMarketDate() time.Time {
	now := NowMarketTime()
	
	// Date rollover happens at 8:30 AM ET
	rolloverTime := time.Date(now.Year(), now.Month(), now.Day(), 8, 30, 0, 0, MARKET_TIMEZONE)
	
	if now.Before(rolloverTime) {
		// Before 8:30 AM ET: use yesterday's date
		yesterday := now.AddDate(0, 0, -1)
		// Log for debugging
		Logf("[GetMarketDate] Before 8:30 AM ET rollover: now=%s, rolloverTime=%s, returning yesterday: %s", 
			now.Format("2006-01-02 15:04:05 MST"), 
			rolloverTime.Format("2006-01-02 15:04:05 MST"),
			yesterday.Format("2006-01-02 15:04:05 MST"))
		return yesterday
	}
	
	// 8:30 AM ET or later: use today's date
	Logf("[GetMarketDate] After 8:30 AM ET rollover: now=%s, rolloverTime=%s, returning today: %s", 
		now.Format("2006-01-02 15:04:05 MST"), 
		rolloverTime.Format("2006-01-02 15:04:05 MST"),
		now.Format("2006-01-02 15:04:05 MST"))
	return now
}

// GetMarketDateForDate converts any date to the market date, handling weekends and rollover logic
// If the date is a weekend, returns the last Friday
// If the date is before 8:30 AM ET, returns the previous day
// Otherwise returns the date as-is
func GetMarketDateForDate(date time.Time) time.Time {
	date = date.In(MARKET_TIMEZONE)
	
	// First, handle weekend dates (use last Friday)
	if IsWeekend(date) {
		return GetLastTradingDay(date)
	}
	
	// Check if before 8:30 AM ET rollover time
	rolloverTime := time.Date(date.Year(), date.Month(), date.Day(), 8, 30, 0, 0, MARKET_TIMEZONE)
	if date.Before(rolloverTime) {
		// Before 8:30 AM ET: use previous day
		return date.AddDate(0, 0, -1)
	}
	
	// 8:30 AM ET or later: use the date as-is
	return date
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
