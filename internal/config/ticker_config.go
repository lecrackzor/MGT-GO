package config

// TickerConfig represents configuration for a single ticker
type TickerConfig struct {
	Display           bool   `yaml:"display" json:"Display"`
	CollectionEnabled bool   `yaml:"collection_enabled" json:"CollectionEnabled"`
	Priority          string `yaml:"priority" json:"Priority"`             // "high", "medium", "low"
	RefreshRateMs     *int   `yaml:"refresh_rate_ms" json:"RefreshRateMs"` // Optional override, 0 = use priority-based scheduling
	ArchiveOnly       bool   `yaml:"archive_only" json:"ArchiveOnly"`      // collect via the EOD archive only; never poll live
}

// GetEnabledTickers filters ticker configs to return only those with collection_enabled=true.
// Archive-only tickers stay enabled: they are recovered from the end-of-day archive.
func GetEnabledTickers(tickerConfigs map[string]TickerConfig) []string {
	enabled := make([]string, 0)
	for ticker, config := range tickerConfigs {
		if config.CollectionEnabled {
			enabled = append(enabled, ticker)
		}
	}
	return enabled
}

// GetLiveTickers filters ticker configs to the tickers that are polled live:
// collection-enabled and not archive-only. The scheduler and coordinator use
// this list, so an archive-only ticker never costs an intraday request.
func GetLiveTickers(tickerConfigs map[string]TickerConfig) []string {
	live := make([]string, 0)
	for ticker, config := range tickerConfigs {
		if config.CollectionEnabled && !config.ArchiveOnly {
			live = append(live, ticker)
		}
	}
	return live
}
