package config

import (
	"reflect"
	"sort"
	"testing"
)

// GetLiveTickers must exclude archive-only tickers while GetEnabledTickers
// keeps them, so archive-only tickers are backfilled but never polled live.
func TestLiveVsEnabledTickers(t *testing.T) {
	configs := map[string]TickerConfig{
		"SPX":  {CollectionEnabled: true},
		"AAPL": {CollectionEnabled: true, ArchiveOnly: true},
		"TSLA": {CollectionEnabled: true},
		"VIX":  {CollectionEnabled: false, ArchiveOnly: true},
		"OFF":  {},
	}

	enabled := GetEnabledTickers(configs)
	sort.Strings(enabled)
	wantEnabled := []string{"AAPL", "SPX", "TSLA"}
	if !reflect.DeepEqual(enabled, wantEnabled) {
		t.Fatalf("enabled = %v, want %v", enabled, wantEnabled)
	}

	live := GetLiveTickers(configs)
	sort.Strings(live)
	wantLive := []string{"SPX", "TSLA"}
	if !reflect.DeepEqual(live, wantLive) {
		t.Fatalf("live = %v, want %v", live, wantLive)
	}
}
