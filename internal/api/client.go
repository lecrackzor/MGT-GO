package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"market-terminal/internal/config"
)

// Client handles HTTP requests to the GEXBot API
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	mu         sync.RWMutex
	debugPrint func(string, string)
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

	return &Client{
		apiKey:     apiKey,
		baseURL:    config.APIBaseURL,
		httpClient: httpClient,
		debugPrint: debugPrint,
	}
}

// FetchEndpoint fetches data from a specific API endpoint
func (c *Client) FetchEndpoint(endpoint, ticker string) (map[string]interface{}, error) {
	// Get endpoint URL template
	urlTemplate, ok := Endpoints[endpoint]
	if !ok {
		return nil, fmt.Errorf("unknown endpoint: %s", endpoint)
	}

	// Build URL
	url := fmt.Sprintf(urlTemplate, c.baseURL, ticker, c.apiKey)

	// Retry logic for transient errors
	maxRetries := 3
	retryDelays := []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		requestStartTime := time.Now()
		
		c.debugPrint(fmt.Sprintf("API: Fetching %s for %s (attempt %d/%d)", endpoint, ticker, attempt+1, maxRetries), "api")

		// Make HTTP request
		resp, err := c.httpClient.Get(url)
		if err != nil {
			lastErr = err
			if attempt < maxRetries-1 {
				delay := retryDelays[attempt]
				c.debugPrint(fmt.Sprintf("⏳ Request error fetching %s for %s (attempt %d/%d) - retrying in %v", endpoint, ticker, attempt+1, maxRetries, delay), "api")
				time.Sleep(delay)
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
				Endpoint:  endpoint,
				Message:   fmt.Sprintf("Rate limit exceeded for %s on %s", endpoint, ticker),
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
			if attempt < maxRetries-1 {
				delay := retryDelays[attempt]
				time.Sleep(delay)
				continue
			}
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		// Parse JSON
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
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
		
		c.debugPrint(fmt.Sprintf("API: Successfully fetched %s for %s (response time: %.3fs, fields: %d)", 
			endpoint, ticker, responseTime.Seconds(), len(data)), "api")

		return data, nil
	}

	return nil, fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

// SetAPIKey updates the API key
func (c *Client) SetAPIKey(apiKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apiKey = apiKey
}

// Close closes the HTTP client (releases connections)
func (c *Client) Close() {
	// HTTP client will close connections on its own
}
