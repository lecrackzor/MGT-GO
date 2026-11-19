package scheduler

import (
	"fmt"
	"sync"
	"time"
)

// Gexbot quotas are per endpoint pool: each endpoint has its own budget, and a
// poll that calls eleven endpoints once each does not spend eleven slots of the
// same pool. The tracker therefore keeps one bucket per endpoint, fed by that
// endpoint's own X-RateLimit-* headers, and sizes the poll gap from the
// tightest bucket the poll actually uses.
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

	if endpoint == "" {
		endpoint = "shared"
	}
	bucket := rlt.buckets[endpoint]
	if bucket == nil {
		bucket = &endpointBucket{requestTimes: make([]float64, 0, 256)}
		rlt.buckets[endpoint] = bucket
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
// endpoint budget the poll touches. Each bucket receives one call per ticker
// per poll, so only the ticker count scales the gap, not the endpoint count.
// Until a bucket's limit is known, the conservative shared-pool estimate is
// used instead.
func (rlt *RateLimitTracker) GetMinimumInterval(tickerCount int, endpoints []string) float64 {
	rlt.mu.RLock()
	defer rlt.mu.RUnlock()

	if tickerCount < 1 {
		tickerCount = 1
	}
	if len(endpoints) == 0 {
		endpoints = []string{""}
	}

	tightest := 0.0
	for _, endpoint := range endpoints {
		bucket := rlt.buckets[endpoint]
		if bucket == nil || bucket.limit <= 0 {
			continue
		}
		interval := rlt.rateLimitWindow / float64(bucket.limit) * float64(tickerCount)
		if interval > tightest {
			tightest = interval
		}
	}
	if tightest > 0 {
		return tightest
	}

	// No per-endpoint budget yet. Assume one shared pool and count every call
	// the poll makes, as the previous implementation did.
	if rlt.sharedLimit <= 0 {
		return 0
	}
	interval := rlt.rateLimitWindow / float64(rlt.sharedLimit)
	interval *= float64(len(endpoints))
	interval *= float64(tickerCount)
	return interval
}

// BucketSnapshot reports what is known about one endpoint's budget. Tests and
// the settings window use it; an unknown endpoint returns zeros.
func (rlt *RateLimitTracker) BucketSnapshot(endpoint string) (limit, remaining int, reset float64) {
	rlt.mu.RLock()
	defer rlt.mu.RUnlock()
	bucket := rlt.buckets[endpoint]
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
