package charts

import (
	"sync"
)

// ChartTracker tracks which tickers are currently displayed in charts
type ChartTracker struct {
	displayedTickers map[string]bool
	mu               sync.RWMutex
}

// NewChartTracker creates a new chart tracker
func NewChartTracker() *ChartTracker {
	return &ChartTracker{
		displayedTickers: make(map[string]bool),
	}
}

// RegisterTicker marks a ticker as displayed
func (ct *ChartTracker) RegisterTicker(ticker string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.displayedTickers[ticker] = true
}

// UnregisterTicker marks a ticker as not displayed
func (ct *ChartTracker) UnregisterTicker(ticker string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	delete(ct.displayedTickers, ticker)
}

// GetDisplayedTickers returns a list of all currently displayed tickers
func (ct *ChartTracker) GetDisplayedTickers() []string {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	tickers := make([]string, 0, len(ct.displayedTickers))
	for ticker := range ct.displayedTickers {
		tickers = append(tickers, ticker)
	}
	return tickers
}

// IsTickerDisplayed checks if a ticker is currently displayed
func (ct *ChartTracker) IsTickerDisplayed(ticker string) bool {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return ct.displayedTickers[ticker]
}

// GetDisplayedTickerCount returns the number of displayed tickers
func (ct *ChartTracker) GetDisplayedTickerCount() int {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return len(ct.displayedTickers)
}
