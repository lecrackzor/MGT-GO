package config

// TickerConfig represents configuration for a single ticker
type TickerConfig struct {
	Display           bool   `yaml:"display" json:"Display"`
	CollectionEnabled bool   `yaml:"collection_enabled" json:"CollectionEnabled"`
	Priority          string `yaml:"priority" json:"Priority"` // "high", "medium", "low"
	RefreshRateMs     *int   `yaml:"refresh_rate_ms" json:"RefreshRateMs"` // Optional override, 0 = use priority-based scheduling
}

// GetEnabledTickers filters ticker configs to return only those with collection_enabled=true
func GetEnabledTickers(tickerConfigs map[string]TickerConfig) []string {
	enabled := make([]string, 0)
	for ticker, config := range tickerConfigs {
		if config.CollectionEnabled {
			enabled = append(enabled, ticker)
		}
	}
	return enabled
}
