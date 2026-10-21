package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

// Client handles HTTP requests to the GEXBot API
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	userAgent  string
	mu         sync.RWMutex
	debugPrint func(string, string)
	debugOn    func(string) bool

	// Request usage tracking (every HTTP attempt counts against the API quota)
	requestCount     atomic.Int64 // Total requests this session
	lastLoggedCount  atomic.Int64 // Count at last usage log line
	sessionStartTime time.Time
	stop             chan struct{}
	stopOnce         sync.Once
}

// NewClient creates a new API client with connection pooling
func NewClient(apiKey string, debugPrint func(string, string)) *Client {
	// Create HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        config.HTTPPoolConnections,
		MaxIdleConnsPerHost: config.HTTPPoolMaxSize,
		IdleConnTimeout:     90 * time.Second,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	client := &Client{
		apiKey:           apiKey,
		baseURL:          config.APIBaseURL,
		httpClient:       httpClient,
		userAgent:        "MarketTerminalGexbot/1.0",
		debugPrint:       debugPrint,
		sessionStartTime: time.Now(),
		stop:             make(chan struct{}),
	}

	// Log request usage once a minute so burn rate against the monthly
	// quota is visible in the log file
	go client.usageLogger()

	return client
}

// SetDebugGate skips formatting log lines when the category is disabled.
func (c *Client) SetDebugGate(fn func(string) bool) {
	c.debugOn = fn
}

func (c *Client) logf(category, format string, args ...interface{}) {
	if c.debugOn != nil && !c.debugOn(category) {
		return
	}
	if c.debugPrint != nil {
		c.debugPrint(fmt.Sprintf(format, args...), category)
	}
}

// usageLogger periodically logs API request usage (once a minute, only when
// requests were made in that window)
func (c *Client) usageLogger() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			total := c.requestCount.Load()
			last := c.lastLoggedCount.Load()
			if total == last {
				continue
			}
			c.lastLoggedCount.Store(total)

			elapsed := time.Since(c.sessionStartTime)
			perMinute := total - last
			utils.Logf("[API-USAGE] %d requests this session (%.1fh) | %d in last minute | projected %d/hour",
				total, elapsed.Hours(), perMinute, perMinute*60)
		}
	}
}

// getAPIKey returns the current API key in a thread-safe way.
func (c *Client) getAPIKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiKey
}

// FetchEndpoint fetches data from a specific API endpoint.
// ctx cancels a batch: a new attempt does not start after it is done, and a
// request already inside Do is aborted with the context.
func (c *Client) FetchEndpoint(ctx context.Context, endpoint, ticker string) (map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	apiKey := strings.TrimSpace(c.getAPIKey())
	if apiKey == "" {
		return nil, &SubscriptionError{
			Endpoint: endpoint,
			Message:  "API key is not configured. Set GEXBOT_API_KEY or save an API key in settings to use Bearer authentication.",
		}
	}

	// Get endpoint URL template
	urlTemplate, ok := Endpoints[endpoint]
	if !ok {
		return nil, fmt.Errorf("unknown endpoint: %s", endpoint)
	}

	// Build URL
	url := fmt.Sprintf(urlTemplate, c.baseURL, ticker)

	// Retry logic for transient errors
	maxRetries := 3
	retryDelays := []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		requestStartTime := time.Now()

		c.logf("api", "API: Fetching %s for %s (attempt %d/%d)", endpoint, ticker, attempt+1, maxRetries)

		// Build authenticated request (GEXBot now requires header-based auth).
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to build request for %s/%s: %w", endpoint, ticker, err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Accept", "application/json")

		// Make HTTP request (every attempt counts against the API quota)
		c.requestCount.Add(1)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxRetries-1 && ctx.Err() == nil {
				delay := retryDelays[attempt]
				c.logf("api", "⏳ Request error fetching %s for %s (attempt %d/%d) - retrying in %v", endpoint, ticker, attempt+1, maxRetries, delay)
				if !sleepContext(ctx, delay) {
					return nil, ctx.Err()
				}
				continue
			}
			return nil, fmt.Errorf("request error after %d attempts: %w", maxRetries, err)
		}

		// Calculate response time
		responseTime := time.Since(requestStartTime)

		// Check status code
		if resp.StatusCode == 401 {
			resp.Body.Close()
			return nil, &SubscriptionError{
				Endpoint: endpoint,
				Message:  fmt.Sprintf("Unauthorized access to %s for %s. Check API key and subscription tier.", endpoint, ticker),
			}
		} else if resp.StatusCode == 403 {
			resp.Body.Close()
			return nil, &SubscriptionError{
				Endpoint: endpoint,
				Message:  fmt.Sprintf("Access forbidden to %s for %s. This endpoint requires a subscription tier you don't have.", endpoint, ticker),
			}
		} else if resp.StatusCode == 429 {
			// Rate limit exceeded
			retryAfter := resp.Header.Get("Retry-After")
			resp.Body.Close()
			return nil, &RateLimitError{
				Endpoint:   endpoint,
				Message:    fmt.Sprintf("Rate limit exceeded for %s on %s", endpoint, ticker),
				RetryAfter: retryAfter,
			}
		} else if resp.StatusCode != 200 {
			// Read error body
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			bodyStr := string(body)
			if len(bodyStr) > 200 {
				bodyStr = bodyStr[:200]
			}
			return nil, &RequestError{
				Endpoint:   endpoint,
				StatusCode: resp.StatusCode,
				Message:    fmt.Sprintf("HTTP %d error fetching %s for %s: %s", resp.StatusCode, endpoint, ticker, bodyStr),
			}
		}

		// Read response body
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			if attempt < maxRetries-1 && ctx.Err() == nil {
				delay := retryDelays[attempt]
				if !sleepContext(ctx, delay) {
					return nil, ctx.Err()
				}
				continue
			}
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		// Scalars are decoded. Arrays and objects stay raw so profiles can be
		// gzipped without being decoded and encoded again.
		data, err := decodeResponse(body)
		if err != nil {
			return nil, &RequestError{
				Endpoint:      endpoint,
				Message:       fmt.Sprintf("Invalid JSON response from %s for %s: %v", endpoint, ticker, err),
				OriginalError: err,
			}
		}

		// Extract rate limit headers
		rateLimitHeaders := make(map[string]string)
		for _, headerName := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Retry-After"} {
			if val := resp.Header.Get(headerName); val != "" {
				rateLimitHeaders[headerName] = val
			}
		}

		// Add headers to response data
		if len(rateLimitHeaders) > 0 {
			data["_response_headers"] = rateLimitHeaders
		}

		// Add response time
		data["_response_time"] = responseTime.Seconds()

		c.logf("api", "API: Successfully fetched %s for %s (response time: %.3fs, fields: %d)",
			endpoint, ticker, responseTime.Seconds(), len(data))

		return data, nil
	}

	return nil, fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

// decodeResponse keeps arrays and objects as raw JSON and decodes scalars.
func decodeResponse(body []byte) (map[string]interface{}, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	data := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		value = bytes.TrimSpace(value)
		if len(value) == 0 || string(value) == "null" {
			data[key] = nil
			continue
		}
		switch value[0] {
		case '[', '{':
			data[key] = json.RawMessage(value)
		default:
			var decoded interface{}
			if err := json.Unmarshal(value, &decoded); err != nil {
				return nil, err
			}
			data[key] = decoded
		}
	}
	return data, nil
}

// SetAPIKey updates the API key
func (c *Client) SetAPIKey(apiKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apiKey = apiKey
}

// Close stops the usage logger and releases idle connections.
func (c *Client) Close() {
	c.stopOnce.Do(func() { close(c.stop) })
	c.httpClient.CloseIdleConnections()
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
