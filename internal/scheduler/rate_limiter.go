package scheduler

import (
	"fmt"
	"sync"
	"time"
)

// RateLimitTracker tracks API rate limits and ensures we respect them
type RateLimitTracker struct {
	mu                   sync.RWMutex
	requestTimes         []float64 // Track request times in current window
	rateLimitWindow      float64   // Default 60 second window
	rateLimitMaxRequests int       // Max requests per window (from headers)
	rateLimitRemaining   int       // Remaining requests (from headers)
	rateLimitResetTime   float64   // When rate limit resets (from headers)
	isRateLimited        bool      // Currently rate limited
	retryAfter           float64   // When to retry after rate limit error
}

// NewRateLimitTracker creates a new rate limit tracker
func NewRateLimitTracker() *RateLimitTracker {
	return &RateLimitTracker{
		requestTimes:    make([]float64, 0, 2000),
		rateLimitWindow: 60.0,
	}
}

// RecordRequest records an API request
func (rlt *RateLimitTracker) RecordRequest(requestTime float64, success bool, headers map[string]string) {
	rlt.mu.Lock()
	defer rlt.mu.Unlock()

	// Add to request history and prune entries outside the window.
	// Times are appended monotonically, so we can trim from the front.
	rlt.requestTimes = append(rlt.requestTimes, requestTime)
	cutoffTime := requestTime - rlt.rateLimitWindow
	firstValid := 0
	for firstValid < len(rlt.requestTimes) && rlt.requestTimes[firstValid] <= cutoffTime {
		firstValid++
	}
	if firstValid > 0 {
		rlt.requestTimes = rlt.requestTimes[firstValid:]
	}

	// Update from headers if available
	if headers != nil {
		rlt.updateFromHeaders(headers)
	}

	// Note: the rate-limited flag is only set by HandleRateLimitError (429s),
	// not inferred from request counts - the counts feed GetMinimumInterval.
	if !success {
		rlt.isRateLimited = true
	}
}

// updateFromHeaders updates rate limit parameters from API response headers
func (rlt *RateLimitTracker) updateFromHeaders(headers map[string]string) {
	if limit, ok := headers["X-RateLimit-Limit"]; ok {
		if val := parseInt(limit); val > 0 {
			rlt.rateLimitMaxRequests = val
		}
	}

	if remaining, ok := headers["X-RateLimit-Remaining"]; ok {
		if val := parseInt(remaining); val >= 0 {
			rlt.rateLimitRemaining = val
		}
	}

	if reset, ok := headers["X-RateLimit-Reset"]; ok {
		if val := parseFloat(reset); val > 0 {
			rlt.rateLimitResetTime = val
		}
	}
}

// HandleRateLimitError handles 429 Too Many Requests error
func (rlt *RateLimitTracker) HandleRateLimitError(retryAfter float64) {
	rlt.mu.Lock()
	defer rlt.mu.Unlock()

	currentTime := float64(time.Now().Unix())
	rlt.isRateLimited = true

	if retryAfter > 0 {
		rlt.retryAfter = currentTime + retryAfter
	} else if rlt.rateLimitResetTime > currentTime {
		rlt.retryAfter = rlt.rateLimitResetTime
	} else {
		// Default: wait 60 seconds if we don't know when to retry
		rlt.retryAfter = currentTime + 60.0
	}
}

// IsRateLimited checks if we're currently rate limited
func (rlt *RateLimitTracker) IsRateLimited() bool {
	rlt.mu.Lock()
	defer rlt.mu.Unlock()

	// Clear the flag once the retry-after window has passed
	if rlt.retryAfter > 0 && float64(time.Now().Unix()) >= rlt.retryAfter {
		rlt.isRateLimited = false
		rlt.retryAfter = 0
	}

	return rlt.isRateLimited
}

// RetryAfterSeconds returns how many seconds remain until requests may resume.
// Returns 0 if not currently rate limited.
func (rlt *RateLimitTracker) RetryAfterSeconds() float64 {
	rlt.mu.RLock()
	defer rlt.mu.RUnlock()

	if !rlt.isRateLimited || rlt.retryAfter <= 0 {
		return 0
	}
	remaining := rlt.retryAfter - float64(time.Now().Unix())
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetMinimumInterval calculates minimum interval to respect rate limits
func (rlt *RateLimitTracker) GetMinimumInterval(tickerCount int) float64 {
	rlt.mu.RLock()
	defer rlt.mu.RUnlock()

	if rlt.rateLimitMaxRequests <= 0 {
		return 0.0 // No rate limit known
	}

	// Calculate minimum interval based on rate limit and ticker count
	// Ensure we don't exceed rate limit even with all tickers polling
	minInterval := rlt.rateLimitWindow / float64(rlt.rateLimitMaxRequests)
	
	// Scale by ticker count to ensure we don't exceed limit
	if tickerCount > 0 {
		minInterval *= float64(tickerCount)
	}

	return minInterval
}

// Helper functions
func parseInt(s string) int {
	var val int
	_, err := fmt.Sscanf(s, "%d", &val)
	if err != nil {
		return 0
	}
	return val
}

func parseFloat(s string) float64 {
	var val float64
	_, err := fmt.Sscanf(s, "%f", &val)
	if err != nil {
		return 0
	}
	return val
}
