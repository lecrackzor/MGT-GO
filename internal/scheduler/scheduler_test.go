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

func TestGetMinimumIntervalScalesByEndpointCalls(t *testing.T) {
	rlt := NewRateLimitTracker()
	rlt.RecordRequest(float64(time.Now().Unix()), map[string]string{
		"X-RateLimit-Limit": "60",
	})
	one := rlt.GetMinimumInterval(2, 1)
	all := rlt.GetMinimumInterval(2, 3)
	if one != 2 {
		t.Fatalf("one call per ticker = %v, want 2", one)
	}
	if all != 6 {
		t.Fatalf("three calls per ticker = %v, want 6", all)
	}
}

func TestCalculateIntervalScalesOpenChartByPlannedEndpoints(t *testing.T) {
	settings := &config.Settings{
		CollectAllEndpoints:  true,
		APISubscriptionTiers: []string{"classic"},
	}
	uas := NewUnifiedAdaptiveScheduler(settings)
	uas.SetEnabledTickers([]string{"SPX", "QQQ"})
	uas.rateLimitTracker.RecordRequest(float64(time.Now().Unix()), map[string]string{
		"X-RateLimit-Limit": "60",
	})

	// Open chart base is 1s. Two tickers times three classic endpoints
	// at 60 requests per 60s require a 6s gap.
	interval := uas.CalculateInterval("SPX", []interface{}{"SPX"})
	if interval != 6 {
		t.Fatalf("interval = %v, want 6", interval)
	}
}

func TestCalculateIntervalKeepsLiveTiersWithoutRateLimit(t *testing.T) {
	settings := &config.Settings{
		CollectAllEndpoints:         true,
		APISubscriptionTiers:        []string{"classic"},
		ActiveTickerRefreshRateMs:   5000,
		DataCollectionRefreshRateMs: 30000,
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
