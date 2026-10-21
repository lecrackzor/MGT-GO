package api

import (
	"testing"

	"market-terminal/internal/config"
)

func endpointsOf(queries []Query) []string {
	out := make([]string, len(queries))
	for i, q := range queries {
		out[i] = q.Ticker + ":" + q.Endpoint
	}
	return out
}

func TestBuildValidatedQueriesClassicEnabledOnly(t *testing.T) {
	settings := &config.Settings{
		APISubscriptionTiers: []string{"classic"},
		CollectAllEndpoints:  true,
	}
	qs := NewQuerySystem(settings, nil)
	got := endpointsOf(qs.BuildValidatedQueries([]string{"SPX", "QQQ"}, []string{"SPX"}))
	want := []string{"SPX:classic_full", "SPX:classic_zero", "SPX:classic_one"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestBuildValidatedQueriesChartOnlyAndHidden(t *testing.T) {
	chartOnly := &config.Settings{
		APISubscriptionTiers: []string{"classic"},
		CollectAllEndpoints:  false,
	}
	qs := NewQuerySystem(chartOnly, nil)
	got := endpointsOf(qs.BuildValidatedQueries([]string{"SPX"}, []string{"SPX"}))
	if len(got) != 1 || got[0] != "SPX:classic_zero" {
		t.Fatalf("chart-only got %v", got)
	}

	chartOnly.HiddenPlots = []string{
		"spot", "zero_gamma", "major_pos_vol", "major_neg_vol", "major_pos_oi", "major_neg_oi",
	}
	qs.SetSettings(chartOnly)
	got = endpointsOf(qs.BuildValidatedQueries([]string{"SPX"}, []string{"SPX"}))
	if len(got) != 0 {
		t.Fatalf("all plots hidden got %v", got)
	}
}

func TestChartDataProfileFollowsCheckedLayers(t *testing.T) {
	settings := &config.Settings{
		APISubscriptionTiers: []string{"classic", "state"},
		CollectAllEndpoints:  false,
		ProfileSettings: map[string]interface{}{
			"enabled": true,
			"layers": map[string]interface{}{
				"classic": true,
			},
		},
	}
	qs := NewQuerySystem(settings, nil)
	got := map[string]bool{}
	for _, q := range qs.BuildValidatedQueries([]string{"SPX"}, []string{"SPX"}) {
		got[q.Endpoint] = true
	}
	for _, name := range []string{"classic_full", "classic_zero", "classic_one"} {
		if !got[name] {
			t.Fatalf("missing %s in %v", name, got)
		}
	}
	for _, name := range []string{"state_zero", "gamma_one", "vanna_zero", "delta_one"} {
		if got[name] {
			t.Fatalf("unchecked layer was requested: %v", got)
		}
	}
	// gamma_zero is the state-tier chart line, not a profile request.
	if !got["gamma_zero"] {
		t.Fatalf("chart gamma line was dropped: %v", got)
	}
}

func TestBuildValidatedQueriesStateTierExcludesClassic(t *testing.T) {
	settings := &config.Settings{
		APISubscriptionTiers: []string{"state"},
		CollectAllEndpoints:  true,
	}
	qs := NewQuerySystem(settings, nil)
	got := qs.BuildValidatedQueries([]string{"SPX"}, []string{"SPX"})
	seen := map[string]bool{}
	for _, q := range got {
		seen[q.Endpoint] = true
		if q.Ticker != "SPX" {
			t.Fatalf("unexpected ticker %s", q.Ticker)
		}
	}
	if !seen["gamma_zero"] || !seen["state_full"] || !seen["vanna_zero"] || !seen["charm_one"] {
		t.Fatalf("missing state endpoints: %v", endpointsOf(got))
	}
	if seen["classic_full"] || seen["classic_zero"] {
		t.Fatalf("classic endpoints included for state tier: %v", endpointsOf(got))
	}
}

func TestVannaAndCharmCollectedOnce(t *testing.T) {
	greek := []string{"vanna_zero", "vanna_one", "charm_zero", "charm_one"}
	for _, tiers := range [][]string{{"state"}, {"orderflow"}, {"state", "orderflow"}, {"orderflow", "state"}} {
		settings := &config.Settings{
			APISubscriptionTiers: tiers,
			CollectAllEndpoints:  true,
		}
		qs := NewQuerySystem(settings, nil)
		counts := map[string]int{}
		for _, q := range qs.BuildValidatedQueries([]string{"SPX"}, []string{"SPX"}) {
			counts[q.Endpoint]++
		}
		for _, name := range greek {
			if counts[name] != 1 {
				t.Fatalf("tiers %v: %s counted %d", tiers, name, counts[name])
			}
		}
		if counts["classic_zero"] != 0 || counts["classic_full"] != 0 {
			t.Fatalf("tiers %v included classic: %v", tiers, counts)
		}
	}
}

func TestPlannedEndpointsPerTicker(t *testing.T) {
	collectAll := &config.Settings{
		APISubscriptionTiers: []string{"classic"},
		CollectAllEndpoints:  true,
	}
	if n := PlannedEndpointsPerTicker(collectAll); n != 3 {
		t.Fatalf("collect-all classic = %d, want 3", n)
	}
	chartOnly := &config.Settings{
		APISubscriptionTiers: []string{"classic"},
		CollectAllEndpoints:  false,
	}
	if n := PlannedEndpointsPerTicker(chartOnly); n != 1 {
		t.Fatalf("chart-only classic = %d, want 1", n)
	}
	if n := PlannedEndpointsPerTicker(nil); n != 1 {
		t.Fatalf("nil settings = %d, want 1", n)
	}
}
