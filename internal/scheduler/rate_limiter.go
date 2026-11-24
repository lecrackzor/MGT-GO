package scheduler

import (
	"fmt"
	"sync"
	"time"
)

// Gexbot quotas are per endpoint group, not per endpoint. Every chain snapshot
// route the app polls (/{ticker}/{package}/{category}) draws from one shared
// pool, api_default; the majors and maxchange sub-routes use separate pools
// the app never requests. A poll that calls fourteen endpoints once each for
// one ticker therefore spends fourteen slots of the same pool, so the tracker
// keys its buckets by pool and sizes the poll gap from the pool's budget.
// See docs.gexbot.com/apidocs/general/rate-limits.
type endpointBucket struct {
	requestTimes []float64
	limit        int
	remaining    int
	reset        float64
}

// RateLimitTracker tracks API rate limits and ensures we respect them
type RateLimitTracker struct {
	mu              sync.RWMutex
	buckets         map[string]*endpointBucket
	sharedTimes     []float64 // all requests, used only while no budget is known
	sharedLimit     int       // largest limit seen, for the fallback estimate
	rateLimitWindow float64   // Default 60 second window
	isRateLimited   bool      // Currently rate limited
	retryAfter      float64   // When to retry after rate limit error
}

// NewRateLimitTracker creates a new rate limit tracker
func NewRateLimitTracker() *RateLimitTracker {
	return &RateLimitTracker{
		buckets:         make(map[string]*endpointBucket),
		sharedTimes:     make([]float64, 0, 2000),
		rateLimitWindow: 60.0,
	}
}

// RecordRequest records a successful API request and any rate-limit headers.
// The rate-limited flag is set only by HandleRateLimitError (HTTP 429).
func (rlt *RateLimitTracker) RecordRequest(endpoint string, requestTime float64, headers map[string]string) {
	rlt.mu.Lock()
	defer rlt.mu.Unlock()

	pool := "shared"
	if endpoint != "" {
		pool = endpointPool(endpoint)
	}
	bucket := rlt.buckets[pool]
	if bucket == nil {
		bucket = &endpointBucket{requestTimes: make([]float64, 0, 256)}
		rlt.buckets[pool] = bucket
	}
	bucket.requestTimes = appendWindow(bucket.requestTimes, requestTime, rlt.rateLimitWindow)

	rlt.sharedTimes = appendWindow(rlt.sharedTimes, requestTime, rlt.rateLimitWindow)

	if headers != nil {
		if limit := parseInt(headers["X-RateLimit-Limit"]); limit > 0 {
			bucket.limit = limit
			if limit > rlt.sharedLimit {
				rlt.sharedLimit = limit
			}
		}
		if remaining := parseInt(headers["X-RateLimit-Remaining"]); remaining >= 0 {
			bucket.remaining = remaining
		}
		if reset := parseFloat(headers["X-RateLimit-Reset"]); reset > 0 {
			bucket.reset = reset
		}
	}
}

// appendWindow appends a timestamp and drops entries outside the window. Times
// arrive nearly monotonic, so the trim starts at the front.
func appendWindow(times []float64, requestTime, window float64) []float64 {
	times = append(times, requestTime)
	cutoff := requestTime - window
	firstValid := 0
	for firstValid < len(times) && times[firstValid] <= cutoff {
		firstValid++
	}
	if firstValid > 0 {
		times = times[firstValid:]
	}
	return times
}

// HandleRateLimitError handles 429 Too Many Requests error
func (rlt *RateLimitTracker) HandleRateLimitError(retryAfter float64) {
	rlt.mu.Lock()
	defer rlt.mu.Unlock()

	currentTime := float64(time.Now().Unix())
	rlt.isRateLimited = true

	if retryAfter > 0 {
		rlt.retryAfter = currentTime + retryAfter
		return
	}
	// Fall back to the earliest reset any bucket advertised.
	earliest := 0.0
	for _, bucket := range rlt.buckets {
		if bucket.reset > currentTime && (earliest == 0 || bucket.reset < earliest) {
			earliest = bucket.reset
		}
	}
	if earliest > 0 {
		rlt.retryAfter = earliest
		return
	}
	// Default: wait 60 seconds if we don't know when to retry
	rlt.retryAfter = currentTime + 60.0
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

// GetMinimumInterval is the gap that keeps tickerCount tickers inside every
// quota pool the poll touches. One poll spends one call per ticker per
// endpoint, so all endpoints in the same pool draw from that pool's single
// budget. Until a pool's limit is known, the conservative shared-pool estimate
// is used instead.
func (rlt *RateLimitTracker) GetMinimumInterval(tickerCount int, endpoints []string) float64 {
	rlt.mu.RLock()
	defer rlt.mu.RUnlock()

	if tickerCount < 1 {
		tickerCount = 1
	}

	// Group the poll's endpoints by quota pool. Every endpoint the app polls
	// maps to api_default today, but a pool boundary must not be assumed.
	poolCounts := make(map[string]int)
	for _, endpoint := range endpoints {
		poolCounts[endpointPool(endpoint)]++
	}

	tightest := 0.0
	for pool, count := range poolCounts {
		bucket := rlt.buckets[pool]
		if bucket == nil || bucket.limit <= 0 {
			continue
		}
		interval := rlt.rateLimitWindow / float64(bucket.limit) * float64(count) * float64(tickerCount)
		if interval > tightest {
			tightest = interval
		}
	}
	if tightest > 0 {
		return tightest
	}

	// No per-pool budget yet. Assume one shared pool and count every call
	// the poll makes.
	if rlt.sharedLimit <= 0 {
		return 0
	}
	callCount := len(endpoints)
	if callCount == 0 {
		callCount = 1
	}
	interval := rlt.rateLimitWindow / float64(rlt.sharedLimit)
	interval *= float64(callCount)
	interval *= float64(tickerCount)
	return interval
}

// endpointPool maps an endpoint to the gexbot quota pool it draws from. Every
// endpoint the app polls is a chain snapshot route, and all of them share one
// pool: api_default. The majors (api_majors) and maxchange (api_max_change)
// sub-routes use separate pools the app never requests.
func endpointPool(endpoint string) string {
	return "api_default"
}

// BucketSnapshot reports what is known about one endpoint's pool budget. Tests
// use it; an unknown endpoint returns zeros.
func (rlt *RateLimitTracker) BucketSnapshot(endpoint string) (limit, remaining int, reset float64) {
	rlt.mu.RLock()
	defer rlt.mu.RUnlock()
	bucket := rlt.buckets[endpointPool(endpoint)]
	if bucket == nil {
		return 0, 0, 0
	}
	return bucket.limit, bucket.remaining, bucket.reset
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
