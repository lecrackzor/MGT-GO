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

func TestEndpointPoolMapsSnapshotsToOnePool(t *testing.T) {
	for _, ep := range []string{
		"classic_full", "classic_zero", "classic_one",
		"state_full", "state_zero", "state_one",
		"delta_zero", "gamma_zero", "delta_one", "gamma_one",
		"vanna_zero", "charm_zero", "vanna_one", "charm_one",
		"orderflow",
	} {
		if got := endpointPool(ep); got != "api_default" {
			t.Fatalf("endpointPool(%q) = %q, want api_default", ep, got)
		}
	}
}

func TestGetMinimumIntervalSharesOnePool(t *testing.T) {
	rlt := NewRateLimitTracker()
	now := float64(time.Now().Unix())
	// One pool limit learned through one endpoint's headers.
	rlt.RecordRequest("classic_zero", now, map[string]string{"X-RateLimit-Limit": "60"})

	// Two endpoints in the same pool, two tickers: each sweep spends four
	// slots of one 60/min budget, so the gap is 4s, not the 2s the old
	// per-endpoint model produced.
	two := rlt.GetMinimumInterval(2, []string{"classic_zero", "classic_one"})
	if two != 4 {
		t.Fatalf("two tickers on a shared pool = %v, want 4", two)
	}
}

func TestGetMinimumIntervalCountsEverySnapshotEndpoint(t *testing.T) {
	rlt := NewRateLimitTracker()
	now := float64(time.Now().Unix())
	rlt.RecordRequest("classic_zero", now, map[string]string{"X-RateLimit-Limit": "60"})

	endpoints := []string{
		"classic_full", "classic_zero", "classic_one",
		"state_full", "state_zero", "state_one",
		"delta_zero", "gamma_zero", "delta_one", "gamma_one",
		"vanna_zero", "charm_zero", "vanna_one", "charm_one",
	}
	got := rlt.GetMinimumInterval(1, endpoints)
	// Fourteen endpoints share one 60/min pool: one ticker needs fourteen
	// slots per sweep, so the gap is 14s, not 1s.
	if got != 14 {
		t.Fatalf("interval = %v, want 14 (14 endpoints, one shared pool)", got)
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

	// Open chart base is 1s. Two tickers on a 60-per-minute pool with three
	// classic endpoints per sweep need a 6s gap (3 endpoints * 2 tickers).
	interval := uas.CalculateInterval("SPX", []interface{}{"SPX"})
	if interval != 6 {
		t.Fatalf("interval = %v, want 6", interval)
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
