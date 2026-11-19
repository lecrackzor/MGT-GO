package scheduler

import (
	"testing"
	"time"

	"market-terminal/internal/config"
)

func TestShouldPollSkipsClosedMarket(t *testing.T) {
	if shouldPoll(false, false) {
		t.Fatal("closed market should not poll")
	}
	if !shouldPoll(true, false) {
		t.Fatal("open market should poll")
	}
	if !shouldPoll(false, true) {
		t.Fatal("after-hours flag should allow a closed-market poll")
	}
}

func TestGetMinimumIntervalScalesByTickerCount(t *testing.T) {
	rlt := NewRateLimitTracker()
	now := float64(time.Now().Unix())
	// Each endpoint has its own budget of 60 per minute.
	rlt.RecordRequest("classic_zero", now, map[string]string{"X-RateLimit-Limit": "60"})
	rlt.RecordRequest("classic_one", now, map[string]string{"X-RateLimit-Limit": "60"})

	two := rlt.GetMinimumInterval(2, []string{"classic_zero", "classic_one"})
	if two != 2 {
		t.Fatalf("two tickers = %v, want 2", two)
	}
	// More endpoints per poll do not spend the same pool.
	wide := rlt.GetMinimumInterval(2, []string{"classic_zero", "classic_one", "classic_full"})
	if wide != 2 {
		t.Fatalf("three endpoints = %v, want 2", wide)
	}
}

func TestGetMinimumIntervalFallsBackToSharedPool(t *testing.T) {
	rlt := NewRateLimitTracker()
	// A limit seen without a bucket is treated as one shared pool.
	rlt.RecordRequest("", float64(time.Now().Unix()), map[string]string{"X-RateLimit-Limit": "60"})
	interval := rlt.GetMinimumInterval(2, []string{"classic_zero", "classic_one", "classic_full"})
	if interval != 6 {
		t.Fatalf("shared pool = %v, want 6", interval)
	}
}

func TestTightestBucketWins(t *testing.T) {
	rlt := NewRateLimitTracker()
	now := float64(time.Now().Unix())
	rlt.RecordRequest("classic_zero", now, map[string]string{"X-RateLimit-Limit": "60"})
	rlt.RecordRequest("orderflow", now, map[string]string{"X-RateLimit-Limit": "10"})
	if got := rlt.GetMinimumInterval(2, []string{"classic_zero", "orderflow"}); got != 12 {
		t.Fatalf("interval = %v, want 12", got)
	}
	if limit, _, _ := rlt.BucketSnapshot("orderflow"); limit != 10 {
		t.Fatalf("orderflow limit = %d", limit)
	}
}

func TestCalculateIntervalScalesOpenChartByTickerCount(t *testing.T) {
	settings := &config.Settings{
		CollectAllEndpoints:  true,
		APISubscriptionTiers: []string{"classic"},
	}
	uas := NewUnifiedAdaptiveScheduler(settings)
	uas.SetEnabledTickers([]string{"SPX", "QQQ"})
	uas.rateLimitTracker.RecordRequest("classic_zero", float64(time.Now().Unix()), map[string]string{
		"X-RateLimit-Limit": "60",
	})
	uas.rateLimitTracker.RecordRequest("classic_one", float64(time.Now().Unix()), map[string]string{
		"X-RateLimit-Limit": "60",
	})
	uas.rateLimitTracker.RecordRequest("classic_full", float64(time.Now().Unix()), map[string]string{
		"X-RateLimit-Limit": "60",
	})

	// Open chart base is 1s. Two tickers on a 60-per-minute endpoint budget
	// need a 2s gap; the three endpoints have separate pools.
	interval := uas.CalculateInterval("SPX", []interface{}{"SPX"})
	if interval != 2 {
		t.Fatalf("interval = %v, want 2", interval)
	}
}

func TestCalculateIntervalKeepsLiveTiersWithoutRateLimit(t *testing.T) {
	settings := &config.Settings{
		CollectAllEndpoints:  true,
		APISubscriptionTiers: []string{"classic"},
	}
	uas := NewUnifiedAdaptiveScheduler(settings)
	uas.SetEnabledTickers([]string{"SPX", "QQQ"})

	open := uas.CalculateInterval("SPX", []interface{}{"SPX"})
	if open != 1 {
		t.Fatalf("open chart = %v, want 1s", open)
	}
	other := uas.CalculateInterval("QQQ", []interface{}{"SPX"})
	if other != 6 {
		t.Fatalf("other ticker = %v, want 6s", other)
	}

	rate := 2000
	settings.TickerConfigs = map[string]config.TickerConfig{
		"QQQ": {RefreshRateMs: &rate},
	}
	uas.SetSettings(settings)
	overridden := uas.CalculateInterval("QQQ", []interface{}{"SPX"})
	if overridden != 2 {
		t.Fatalf("per-ticker override = %v, want 2s", overridden)
	}
}
