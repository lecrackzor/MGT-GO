package api

import (
	"fmt"
	"sync"

	"market-terminal/internal/config"
)

// QuerySystem handles query planning and execution
type QuerySystem struct {
	settings      *config.Settings
	apiKey        string
	client        *Client
	debugPrint    func(string, string)
	mu            sync.RWMutex
	endpointCache map[string][]string
	cacheValid    bool
}

// GetClient returns the API client
func (qs *QuerySystem) GetClient() *Client {
	return qs.client
}

// NewQuerySystem creates a new query system
func NewQuerySystem(settings *config.Settings, apiKey string, client *Client, debugPrint func(string, string)) *QuerySystem {
	return &QuerySystem{
		settings:      settings,
		apiKey:        apiKey,
		client:        client,
		debugPrint:    debugPrint,
		endpointCache: make(map[string][]string),
		cacheValid:    false,
	}
}

// SetAPIKey updates the API key
func (qs *QuerySystem) SetAPIKey(apiKey string) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	qs.apiKey = apiKey
}

// InvalidateEndpointCache invalidates the endpoint cache
func (qs *QuerySystem) InvalidateEndpointCache() {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	qs.cacheValid = false
	qs.endpointCache = make(map[string][]string)
}

// ValidateAndFilterQueries validates and filters queries based on subscription tiers
// Accepts both QueryPlanItem slice (from coordinator) and converts to Query slice
func (qs *QuerySystem) ValidateAndFilterQueries(queryPlan interface{}) []Query {
	var items []QueryPlanItem
	
	// Handle different input types
	switch v := queryPlan.(type) {
	case []QueryPlanItem:
		items = v
	default:
		return []Query{}
	}
	qs.mu.RLock()
	defer qs.mu.RUnlock()

	// Get subscription tiers from settings
	tiers := qs.settings.APISubscriptionTiers
	if len(tiers) == 0 {
		tiers = []string{"classic"} // Default
	}

	// Filter valid tiers
	validTiers := []string{"classic", "state", "orderflow"}
	filteredTiers := make([]string, 0)
	for _, tier := range tiers {
		for _, valid := range validTiers {
			if tier == valid {
				filteredTiers = append(filteredTiers, tier)
				break
			}
		}
	}
	if len(filteredTiers) == 0 {
		filteredTiers = []string{"classic"}
	}

	// Get available endpoints for tiers
	availableEndpoints := GetEndpointsForTiers(filteredTiers)
	availableSet := make(map[string]bool)
	for _, ep := range availableEndpoints {
		availableSet[ep] = true
	}

	// Validate queries
	validatedQueries := make([]Query, 0)
	for _, item := range items {
		// Filter endpoints to only those that exist and are in subscription tier
		validEndpoints := make([]string, 0)
		for _, endpoint := range item.Endpoints {
			// Check if endpoint exists
			if _, exists := Endpoints[endpoint]; !exists {
				continue
			}

			// Check if endpoint is in subscription tier
			endpointTier := GetEndpointTier(endpoint)
			if endpointTier == "" {
				// Unknown endpoint - allow it (might be custom)
				validEndpoints = append(validEndpoints, endpoint)
			} else {
				// Check if tier is in subscription
				tierAllowed := false
				for _, tier := range filteredTiers {
					if endpointTier == tier {
						tierAllowed = true
						break
					}
				}
				if tierAllowed {
					validEndpoints = append(validEndpoints, endpoint)
				}
			}
		}

		// Add validated queries
		for _, endpoint := range validEndpoints {
			validatedQueries = append(validatedQueries, Query{
				Ticker:   item.Ticker,
				Endpoint: endpoint,
			})
		}
	}

	return validatedQueries
}

// ExecuteQueryPlan executes queries in parallel using goroutines
func (qs *QuerySystem) ExecuteQueryPlan(queries []Query, maxWorkers int, resultCallback func(Query, map[string]interface{}, error)) {
	if len(queries) == 0 {
		return
	}

	// Create worker pool
	semaphore := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for _, query := range queries {
		wg.Add(1)
		go func(q Query) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Fetch endpoint
			result, err := qs.client.FetchEndpoint(q.Endpoint, q.Ticker)
			if err != nil {
				qs.debugPrint(fmt.Sprintf("Error fetching %s for %s: %v", q.Endpoint, q.Ticker, err), "api")
			}

			// Call callback
			if resultCallback != nil {
				resultCallback(q, result, err)
			}
		}(query)
	}

	// Wait for all workers
	wg.Wait()
}

// QueryPlanItem represents a ticker with its endpoints
type QueryPlanItem struct {
	Ticker    string
	Endpoints []string
}

// Query represents a single query
type Query struct {
	Ticker   string
	Endpoint string
}
