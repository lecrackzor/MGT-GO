package scheduler

import (
	"fmt"
	"sync"
	"time"
)

// RateLimitTracker tracks API rate limits and ensures we respect them
type RateLimitTracker struct {
	mu                    sync.RWMutex
	requestTimes          []float64 // Track request times in current window
	rateLimitWindow       float64   // Default 60 second window
	rateLimitMaxRequests  int       // Max requests per window (discovered)
	rateLimitRemaining    int       // Remaining requests (from headers)
	rateLimitResetTime    float64   // When rate limit resets (from headers)
	isRateLimited         bool      // Currently rate limited
	retryAfter            float64   // When to retry after rate limit error

	// 429 error monitoring and adaptive throttling
	rateLimitErrors       []float64 // Track last 100 rate limit errors (timestamp)
	lightThrottleEnabled  bool      // Enable light throttling if 429s are frequent
	lightThrottleInterval float64   // 200ms minimum between same endpoint calls
	lastEndpointCallTimes  map[string]float64 // endpoint -> last call time
	rateLimitErrorThreshold int     // Enable light throttle if 5+ 429s in last 60 seconds
}

// NewRateLimitTracker creates a new rate limit tracker
func NewRateLimitTracker() *RateLimitTracker {
	return &RateLimitTracker{
		requestTimes:          make([]float64, 0, 2000),
		rateLimitWindow:       60.0,
		rateLimitErrors:       make([]float64, 0, 100),
		lastEndpointCallTimes: make(map[string]float64),
		lightThrottleInterval: 0.2, // 200ms
		rateLimitErrorThreshold: 5,
	}
}

// RecordRequest records an API request
func (rlt *RateLimitTracker) RecordRequest(requestTime float64, success bool, headers map[string]string) {
	rlt.mu.Lock()
	defer rlt.mu.Unlock()

	// Add to request history
	rlt.requestTimes = append(rlt.requestTimes, requestTime)

	// Clean old requests outside window (keep last 2000)
	if len(rlt.requestTimes) > 2000 {
		cutoffTime := requestTime - rlt.rateLimitWindow
		// Remove old entries
		newTimes := make([]float64, 0, 2000)
		for _, t := range rlt.requestTimes {
			if t > cutoffTime {
				newTimes = append(newTimes, t)
			}
		}
		rlt.requestTimes = newTimes
	}

	// Update from headers if available
	if headers != nil {
		rlt.updateFromHeaders(headers)
	}

	// Check if we're rate limited
	if !success {
		rlt.isRateLimited = true
	} else if rlt.rateLimitMaxRequests > 0 && len(rlt.requestTimes) >= rlt.rateLimitMaxRequests {
		rlt.isRateLimited = true
	} else {
		rlt.isRateLimited = false
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

	currentTime := time.Now().Unix()
	rlt.isRateLimited = true

	// Track 429 error for monitoring
	rlt.rateLimitErrors = append(rlt.rateLimitErrors, float64(currentTime))
	if len(rlt.rateLimitErrors) > 100 {
		rlt.rateLimitErrors = rlt.rateLimitErrors[1:]
	}

	// Check if we should enable light throttling
	rlt.updateLightThrottleStatus(float64(currentTime))

	if retryAfter > 0 {
		rlt.retryAfter = float64(currentTime) + retryAfter
	} else if rlt.rateLimitResetTime > 0 {
		rlt.retryAfter = rlt.rateLimitResetTime
	} else {
		// Default: wait 60 seconds if we don't know when to retry
		rlt.retryAfter = float64(currentTime) + 60.0
	}
}

// updateLightThrottleStatus updates light throttle status based on 429 error frequency
func (rlt *RateLimitTracker) updateLightThrottleStatus(currentTime float64) {
	// Clean old errors (outside 60 second window)
	cutoffTime := currentTime - 60.0
	newErrors := make([]float64, 0)
	for _, t := range rlt.rateLimitErrors {
		if t > cutoffTime {
			newErrors = append(newErrors, t)
		}
	}
	rlt.rateLimitErrors = newErrors

	// Enable light throttle if we have too many 429s
	errorCount := len(rlt.rateLimitErrors)
	if errorCount >= rlt.rateLimitErrorThreshold {
		if !rlt.lightThrottleEnabled {
			rlt.lightThrottleEnabled = true
		}
	} else if rlt.lightThrottleEnabled && errorCount < rlt.rateLimitErrorThreshold/2 {
		// Disable if errors drop below half threshold
		rlt.lightThrottleEnabled = false
	}
}

// CanMakeRequestWithLightThrottle checks if request can be made with light throttling
func (rlt *RateLimitTracker) CanMakeRequestWithLightThrottle(endpoint string) bool {
	rlt.mu.RLock()
	defer rlt.mu.RUnlock()

	if !rlt.lightThrottleEnabled {
		return true // No throttling
	}

	currentTime := time.Now().Unix()
	lastCall := rlt.lastEndpointCallTimes[endpoint]
	timeSince := float64(currentTime) - lastCall

	return timeSince >= rlt.lightThrottleInterval
}

// RecordEndpointCall records that an endpoint was called
func (rlt *RateLimitTracker) RecordEndpointCall(endpoint string) {
	rlt.mu.Lock()
	defer rlt.mu.Unlock()

	if rlt.lightThrottleEnabled {
		currentTime := float64(time.Now().Unix())
		rlt.lastEndpointCallTimes[endpoint] = currentTime

		// Clean up old entries (older than 1 second)
		cutoffTime := currentTime - 1.0
		for ep, t := range rlt.lastEndpointCallTimes {
			if t < cutoffTime {
				delete(rlt.lastEndpointCallTimes, ep)
			}
		}
	}
}

// IsRateLimited checks if we're currently rate limited
func (rlt *RateLimitTracker) IsRateLimited() bool {
	rlt.mu.RLock()
	defer rlt.mu.RUnlock()

	// Check if retry_after time has passed
	if rlt.retryAfter > 0 {
		currentTime := float64(time.Now().Unix())
		if currentTime >= rlt.retryAfter {
			rlt.mu.RUnlock()
			rlt.mu.Lock()
			rlt.isRateLimited = false
			rlt.retryAfter = 0
			rlt.mu.Unlock()
			rlt.mu.RLock()
		}
	}

	return rlt.isRateLimited
}

// CanMakeRequest checks if we can make a request based on rate limits
func (rlt *RateLimitTracker) CanMakeRequest() bool {
	rlt.mu.RLock()
	defer rlt.mu.RUnlock()

	if rlt.isRateLimited {
		return false
	}

	// Check if we're within rate limit based on request history
	if rlt.rateLimitMaxRequests > 0 {
		return len(rlt.requestTimes) < rlt.rateLimitMaxRequests
	}

	return true
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
