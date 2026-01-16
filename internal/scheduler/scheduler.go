package scheduler

import (
	"log"
	"sync"
	"time"

	"market-terminal/internal/config"
)

// UnifiedAdaptiveScheduler provides priority-based scheduling for ticker data collection
type UnifiedAdaptiveScheduler struct {
	mu                    sync.RWMutex
	rateLimitTracker      *RateLimitTracker
	lastFetchTimes        map[string]float64 // ticker -> last fetch time
	tickerIntervals       map[string]float64 // ticker -> current interval
	enabledTickers        []string
	settings              *config.Settings
	isTestingBranch       bool
	endpointFetchTimes    map[string]float64 // endpoint -> last fetch time
	endpointFetchLock     sync.RWMutex
}

// NewUnifiedAdaptiveScheduler creates a new unified adaptive scheduler
func NewUnifiedAdaptiveScheduler(settings *config.Settings, isTestingBranch bool) *UnifiedAdaptiveScheduler {
	return &UnifiedAdaptiveScheduler{
		rateLimitTracker:   NewRateLimitTracker(),
		lastFetchTimes:     make(map[string]float64),
		tickerIntervals:    make(map[string]float64),
		enabledTickers:     make([]string, 0),
		settings:           settings,
		isTestingBranch:    isTestingBranch,
		endpointFetchTimes: make(map[string]float64),
	}
}

// SetEnabledTickers sets the list of enabled tickers
func (uas *UnifiedAdaptiveScheduler) SetEnabledTickers(tickers []string) {
	uas.mu.Lock()
	defer uas.mu.Unlock()
	uas.enabledTickers = make([]string, len(tickers))
	copy(uas.enabledTickers, tickers)
}

// SetSettings updates the settings reference (call after saving settings)
func (uas *UnifiedAdaptiveScheduler) SetSettings(settings *config.Settings) {
	uas.mu.Lock()
	defer uas.mu.Unlock()
	uas.settings = settings
}

// CalculateInterval calculates the polling interval for a ticker based on priority
func (uas *UnifiedAdaptiveScheduler) CalculateInterval(ticker string, openCharts []interface{}) float64 {
	uas.mu.RLock()
	defer uas.mu.RUnlock()

	// Determine priority based on ticker visibility
	priority := uas.getTickerPriority(ticker, openCharts)
	
	// Get ticker count
	tickerCount := len(uas.enabledTickers)

	// Calculate interval based on priority and ticker count
	var interval float64
	var priorityName string
	switch priority {
	case 0: // High priority (in chart) - ALWAYS 1 second regardless of ticker count
		interval = 1.0
		priorityName = "HIGH"
	case 1: // Medium priority (enabled but not in chart)
		priorityName = "MEDIUM"
		if tickerCount <= 5 {
			interval = 6.0
		} else if tickerCount <= 20 {
			interval = 10.0
		} else {
			interval = 15.0
		}
	default: // Low priority
		priorityName = "LOW"
		if tickerCount <= 5 {
			interval = 16.0
		} else if tickerCount <= 20 {
			interval = 22.0
		} else {
			interval = 30.0
		}
	}

	baseInterval := interval // Store for logging

	// Check for per-ticker refresh rate override
	refreshRateMs := uas.getTickerRefreshRate(ticker)
	if refreshRateMs > 0 {
		interval = float64(refreshRateMs) / 1000.0
	}

	// Ensure minimum interval based on rate limits
	minInterval := uas.rateLimitTracker.GetMinimumInterval(tickerCount)
	if minInterval > 0 && interval < minInterval {
		interval = minInterval
	}

	// Log interval calculation for debugging
	log.Printf("[SCHEDULER] %s: priority=%s(%d), tickerCount=%d, baseInterval=%.1fs, refreshOverride=%dms, finalInterval=%.1fs, openCharts=%d",
		ticker, priorityName, priority, tickerCount, baseInterval, refreshRateMs, interval, len(openCharts))

	return interval
}

// getTickerPriority determines the priority of a ticker (0=high, 1=medium, 2=low)
func (uas *UnifiedAdaptiveScheduler) getTickerPriority(ticker string, openCharts []interface{}) int {
	// Check if ticker is in any open chart (highest priority - overrides user setting)
	// openCharts contains ticker strings from the chart tracker
	if openCharts != nil {
		for _, chartItem := range openCharts {
			if chartTicker, ok := chartItem.(string); ok && chartTicker == ticker {
				return 0 // High priority - ticker is displayed in a chart
			}
		}
	}

	// Check user-configured priority from settings
	if uas.settings != nil && uas.settings.TickerConfigs != nil {
		if tickerConfig, exists := uas.settings.TickerConfigs[ticker]; exists {
			switch tickerConfig.Priority {
			case "high":
				return 0 // High priority - user configured
			case "low":
				return 2 // Low priority - user configured
			default:
				return 1 // Medium priority (default)
			}
		}
	}

	// Check if ticker is enabled (medium priority)
	// This includes collection-only tickers (collection_enabled=true, display=false)
	for _, t := range uas.enabledTickers {
		if t == ticker {
			return 1 // Medium priority - enabled for collection but not displayed
		}
	}

	return 2 // Low priority - not enabled
}

// getTickerRefreshRate gets the per-ticker refresh rate override (in milliseconds)
// Returns 0 if disabled/not set (use priority-based scheduling)
// Returns minimum 1000ms if a custom rate is set
func (uas *UnifiedAdaptiveScheduler) getTickerRefreshRate(ticker string) int {
	if uas.settings == nil {
		return 0
	}
	
	// Access ticker configs directly from settings
	if uas.settings.TickerConfigs == nil {
		return 0
	}
	
	if tickerConfig, exists := uas.settings.TickerConfigs[ticker]; exists {
		if tickerConfig.RefreshRateMs != nil {
			rate := *tickerConfig.RefreshRateMs
			if rate == 0 {
				return 0 // Disabled - use priority-based scheduling
			}
			if rate < 1000 {
				return 1000 // Enforce minimum 1000ms
			}
			return rate
		}
	}
	
	return 0 // Default: use priority-based scheduling
}

// ShouldFetchTicker checks if a ticker should be fetched now
func (uas *UnifiedAdaptiveScheduler) ShouldFetchTicker(ticker string, openCharts []interface{}) bool {
	uas.mu.RLock()
	defer uas.mu.RUnlock()

	currentTime := time.Now().Unix()
	lastFetch := uas.lastFetchTimes[ticker]
	interval := uas.CalculateInterval(ticker, openCharts)

	// SAFETY CHECK: Ensure interval is valid
	if interval <= 0 {
		interval = 5.0
	}

	// CRITICAL: If ticker has never been fetched (lastFetch == 0), it should be fetched immediately
	if lastFetch == 0 {
		return true
	}

	// Simple check: has enough time passed since last fetch?
	timeSinceFetch := float64(currentTime) - lastFetch
	return timeSinceFetch >= interval
}

// RecordFetch records that a ticker was fetched
func (uas *UnifiedAdaptiveScheduler) RecordFetch(ticker string) {
	uas.mu.Lock()
	defer uas.mu.Unlock()
	uas.lastFetchTimes[ticker] = float64(time.Now().Unix())
}

// CanFetchEndpoint checks if an endpoint can be fetched now (per-endpoint throttling)
func (uas *UnifiedAdaptiveScheduler) CanFetchEndpoint(endpoint string) bool {
	uas.endpointFetchLock.RLock()
	defer uas.endpointFetchLock.RUnlock()

	currentTime := time.Now().Unix()
	lastFetch := uas.endpointFetchTimes[endpoint]
	timeSinceLastFetch := float64(currentTime) - lastFetch

	// Minimum 1 second between calls to same endpoint
	const MIN_ENDPOINT_INTERVAL = 1.0
	return timeSinceLastFetch >= MIN_ENDPOINT_INTERVAL
}

// RecordEndpointFetch records that an endpoint was fetched
func (uas *UnifiedAdaptiveScheduler) RecordEndpointFetch(endpoint string) {
	uas.endpointFetchLock.Lock()
	defer uas.endpointFetchLock.Unlock()
	uas.endpointFetchTimes[endpoint] = float64(time.Now().Unix())
}

// GetRateLimitTracker returns the rate limit tracker
func (uas *UnifiedAdaptiveScheduler) GetRateLimitTracker() *RateLimitTracker {
	return uas.rateLimitTracker
}
