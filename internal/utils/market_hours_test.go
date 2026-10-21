package utils

import (
	"testing"
	"time"
)

func TestGetMarketDateForDateRollover(t *testing.T) {
	loc := GetMarketTimezone()
	before := time.Date(2026, 10, 14, 8, 29, 0, 0, loc)
	if got := GetMarketDateForDate(before); got.Day() != 13 || got.Month() != time.October {
		t.Fatalf("before 8:30 ET got %v, want Oct 13", got)
	}
	at := time.Date(2026, 10, 14, 8, 30, 0, 0, loc)
	if got := GetMarketDateForDate(at); got.Day() != 14 || got.Month() != time.October {
		t.Fatalf("at 8:30 ET got %v, want Oct 14", got)
	}
}

func TestGetMarketDateForDateWeekend(t *testing.T) {
	loc := GetMarketTimezone()
	saturday := time.Date(2026, 10, 17, 15, 0, 0, 0, loc)
	got := GetMarketDateForDate(saturday)
	if got.Weekday() != time.Friday || got.Day() != 16 {
		t.Fatalf("Saturday got %v, want Friday Oct 16", got)
	}
	sunday := time.Date(2026, 10, 18, 10, 0, 0, 0, loc)
	got = GetMarketDateForDate(sunday)
	if got.Weekday() != time.Friday || got.Day() != 16 {
		t.Fatalf("Sunday got %v, want Friday Oct 16", got)
	}
	mondayEarly := time.Date(2026, 10, 19, 7, 0, 0, 0, loc)
	got = GetMarketDateForDate(mondayEarly)
	if got.Weekday() != time.Friday || got.Day() != 16 {
		t.Fatalf("Monday before rollover got %v, want Friday Oct 16", got)
	}
}

func TestHolidaySessionUsesPreviousTradingDay(t *testing.T) {
	loc := GetMarketTimezone()
	holiday := time.Date(2026, 7, 3, 11, 0, 0, 0, loc)
	if IsMarketOpenAt(holiday) {
		t.Fatal("Independence Day observed should be closed")
	}
	got := GetMarketDateForDate(holiday)
	if got.Month() != time.July || got.Day() != 2 {
		t.Fatalf("holiday session = %v, want July 2", got)
	}
	openDay := time.Date(2026, 7, 2, 10, 0, 0, 0, loc)
	if !IsMarketOpenAt(openDay) {
		t.Fatal("July 2 2026 10:00 ET should be open")
	}
}

func TestEarlyCloseEndsAt1300(t *testing.T) {
	loc := GetMarketTimezone()
	day := time.Date(2026, 11, 27, 0, 0, 0, 0, loc)
	_, closeAt := MarketOpenCloseTimes(day)
	if closeAt.Hour() != 13 || closeAt.Minute() != 0 {
		t.Fatalf("early close = %v, want 13:00", closeAt)
	}
	if !IsMarketOpenAt(time.Date(2026, 11, 27, 12, 0, 0, 0, loc)) {
		t.Fatal("noon on an early-close day should be open")
	}
	if IsMarketOpenAt(time.Date(2026, 11, 27, 13, 30, 0, 0, loc)) {
		t.Fatal("1:30 PM on an early-close day should be closed")
	}
	if !IsMarketOpenAt(time.Date(2026, 11, 27, 13, 0, 0, 0, loc)) {
		t.Fatal("the 1:00 PM close minute should still count as open")
	}
}
