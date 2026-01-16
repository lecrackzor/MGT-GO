# API Client Layer

This package provides the API client and query system for Market Terminal Gexbot.

## Components

### Client (`client.go`)
- HTTP client with connection pooling
- Automatic retry logic for transient errors
- Rate limit detection and handling
- Subscription tier error handling
- Response time tracking

### QuerySystem (`query_system.go`)
- Query validation and filtering by subscription tier
- Parallel query execution using goroutines
- Endpoint cache management
- Thread-safe operations

### Endpoints (`endpoints.go`)
- Endpoint URL templates
- Subscription tier mapping
- Helper functions for tier filtering

### Errors (`errors.go`)
- Custom error types:
  - `RequestError` - HTTP request errors
  - `SubscriptionError` - Subscription tier errors
  - `RateLimitError` - Rate limit errors

## Features

- **Connection Pooling**: Reuses HTTP connections for better performance
- **Automatic Retries**: Retries transient errors with exponential backoff
- **Rate Limit Handling**: Detects and handles rate limit responses (429)
- **Subscription Tier Filtering**: Only queries endpoints available for user's subscription
- **Parallel Execution**: Executes multiple queries concurrently using goroutines

## Usage

```go
// Create API client
client := api.NewClient(apiKey, debugPrint)

// Create query system
querySystem := api.NewQuerySystem(settings, apiKey, client, debugPrint)

// Build query plan
queryPlan := []api.QueryPlanItem{
	{Ticker: "SPX", Endpoints: []string{"classic_zero", "state_zero"}},
}

// Validate and filter queries
queries := querySystem.ValidateAndFilterQueries(queryPlan)

// Execute queries in parallel
querySystem.ExecuteQueryPlan(queries, 96, func(q api.Query, result map[string]interface{}, err error) {
	if err != nil {
		log.Printf("Error: %v", err)
		return
	}
	// Process result
})
```

## Memory Visibility

All HTTP operations use Go's standard `net/http` package:
- **No C dependencies** - all memory allocations visible in Go profiler
- **Full visibility** - use `go tool pprof` to see exact allocations
- **Connection pooling** - managed by Go's HTTP transport
