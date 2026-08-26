package api

import (
	"sync"

	"market-terminal/internal/config"
)

// QuerySystem handles query planning and execution
type QuerySystem struct {
	settings   *config.Settings
	apiKey     string
	client     *Client
	debugPrint func(string, string)
	mu         sync.RWMutex
}

// GetClient returns the API client
func (qs *QuerySystem) GetClient() *Client {
	return qs.client
}

// NewQuerySystem creates a new query system
func NewQuerySystem(settings *config.Settings, apiKey string, client *Client, debugPrint func(string, string)) *QuerySystem {
	return &QuerySystem{
		settings:   settings,
		apiKey:     apiKey,
		client:     client,
		debugPrint: debugPrint,
	}
}

// SetAPIKey updates the API key
func (qs *QuerySystem) SetAPIKey(apiKey string) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	qs.apiKey = apiKey
}

// ValidateAndFilterQueries validates and filters queries based on subscription tiers,
// expanding plan items into individual queries and deduplicating by resolved URL
func (qs *QuerySystem) ValidateAndFilterQueries(items []QueryPlanItem) []Query {
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
	// Dedup by resolved URL: legacy endpoint aliases map to the same URL as
	// their canonical names, so fetching both would waste API requests.
	seenURLs := make(map[string]bool)
	for _, item := range items {
		// Filter endpoints to only those that exist and are in subscription tier
		validEndpoints := make([]string, 0)
		for _, endpoint := range item.Endpoints {
			// Check if endpoint exists
			urlTemplate, exists := Endpoints[endpoint]
			if !exists {
				continue
			}

			// Skip endpoints whose URL was already planned for this ticker
			urlKey := item.Ticker + "|" + urlTemplate
			if seenURLs[urlKey] {
				continue
			}
			seenURLs[urlKey] = true

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
