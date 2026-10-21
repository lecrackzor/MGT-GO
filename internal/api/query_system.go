package api

import (
	"strings"
	"sync"

	"market-terminal/internal/config"
)

// QuerySystem validates which endpoints to fetch for the current settings.
type QuerySystem struct {
	settings *config.Settings
	client   *Client
	mu       sync.RWMutex

	cacheKey        string
	cachedEndpoints []string
}

// GetClient returns the API client
func (qs *QuerySystem) GetClient() *Client {
	return qs.client
}

// NewQuerySystem creates a new query system
func NewQuerySystem(settings *config.Settings, client *Client) *QuerySystem {
	return &QuerySystem{
		settings: settings,
		client:   client,
	}
}

// SetSettings updates the settings used for endpoint planning.
func (qs *QuerySystem) SetSettings(settings *config.Settings) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	qs.settings = settings
	qs.cachedEndpoints = nil
	qs.cacheKey = ""
}

// endpointPlotsMap maps API endpoints to the plot names they provide.
// An endpoint is skipped in chart-only mode when every plot it provides is hidden.
var endpointPlotsMap = map[string][]string{
	"classic_zero": {"spot", "zero_gamma", "major_pos_vol", "major_neg_vol", "major_pos_oi", "major_neg_oi"},
	"gamma_zero":   {"zero_gamma", "major_long_gamma", "major_short_gamma", "major_positive", "major_negative"},
}

// BuildValidatedQueries selects endpoints for the given tickers and returns
// one query per ticker/endpoint, filtered by subscription tier and deduped by URL.
func (qs *QuerySystem) BuildValidatedQueries(tickers []string, enabledTickers []string) []Query {
	qs.mu.RLock()
	settings := qs.settings
	key := endpointCacheKey(settings)
	endpoints := qs.cachedEndpoints
	cached := qs.cacheKey == key && endpoints != nil
	qs.mu.RUnlock()
	if !cached {
		endpoints = endpointsForSettings(settings)
		qs.mu.Lock()
		if endpointCacheKey(qs.settings) == key {
			qs.cachedEndpoints = endpoints
			qs.cacheKey = key
		} else {
			endpoints = endpointsForSettings(qs.settings)
		}
		qs.mu.Unlock()
	}

	enabled := make(map[string]bool, len(enabledTickers))
	for _, ticker := range enabledTickers {
		enabled[ticker] = true
	}

	items := make([]QueryPlanItem, 0, len(tickers))
	for _, ticker := range tickers {
		if !enabled[ticker] {
			continue
		}
		items = append(items, QueryPlanItem{
			Ticker:    ticker,
			Endpoints: endpoints,
		})
	}
	return validateQueries(settings, items)
}

func endpointCacheKey(settings *config.Settings) string {
	if settings == nil {
		return "classic|1|0||"
	}
	all := "0"
	if settings.CollectAllEndpoints {
		all = "1"
	}
	profile := "0"
	if config.ProfileEnabled(settings) {
		profile = "1"
	}
	layers := config.ProfileLayers(settings)
	selected := make([]string, 0, len(config.ProfileLayerIDs))
	for _, id := range config.ProfileLayerIDs {
		if layers[id] {
			selected = append(selected, id)
		}
	}
	return strings.Join(settings.APISubscriptionTiers, ",") + "|" + all + "|" + profile + "|" + strings.Join(selected, ",") + "|" + strings.Join(settings.HiddenPlots, ",")
}

// PlannedEndpointsPerTicker is how many HTTP calls one ticker poll makes.
func PlannedEndpointsPerTicker(settings *config.Settings) int {
	if settings == nil {
		return 1
	}
	n := len(endpointsForSettings(settings))
	if n < 1 {
		return 1
	}
	return n
}

func endpointsForSettings(settings *config.Settings) []string {
	tiers := []string{"classic"}
	collectAll := true
	var hiddenPlots []string
	if settings != nil {
		if len(settings.APISubscriptionTiers) > 0 {
			tiers = settings.APISubscriptionTiers
		}
		collectAll = settings.CollectAllEndpoints
		hiddenPlots = settings.HiddenPlots
	}

	if collectAll {
		return GetEndpointsForTiers(tiers)
	}

	endpoints := GetChartEndpointsForTiers(tiers)
	if len(hiddenPlots) > 0 {
		endpoints = filterEndpointsByHiddenPlots(endpoints, hiddenPlots)
	}
	if config.ProfileEnabled(settings) {
		endpoints = mergeEndpoints(endpoints, config.ProfileEndpoints(settings))
	}
	return endpoints
}

func mergeEndpoints(base []string, extra []string) []string {
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, name := range base {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, name := range extra {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func filterEndpointsByHiddenPlots(endpoints []string, hiddenPlots []string) []string {
	hiddenSet := make(map[string]bool, len(hiddenPlots))
	for _, plot := range hiddenPlots {
		hiddenSet[plot] = true
	}

	filtered := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		plots, exists := endpointPlotsMap[endpoint]
		if !exists {
			filtered = append(filtered, endpoint)
			continue
		}
		allHidden := true
		for _, plot := range plots {
			if !hiddenSet[plot] {
				allHidden = false
				break
			}
		}
		if !allHidden {
			filtered = append(filtered, endpoint)
		}
	}
	return filtered
}

func tierCollects(tiers []string, endpoint string) bool {
	for _, name := range GetEndpointsForTiers(tiers) {
		if name == endpoint {
			return true
		}
	}
	for _, name := range GetChartEndpointsForTiers(tiers) {
		if name == endpoint {
			return true
		}
	}
	return false
}

func validateQueries(settings *config.Settings, items []QueryPlanItem) []Query {
	tiers := []string{"classic"}
	if settings != nil && len(settings.APISubscriptionTiers) > 0 {
		tiers = settings.APISubscriptionTiers
	}

	validTiers := []string{"classic", "state", "orderflow"}
	filteredTiers := make([]string, 0, len(tiers))
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

	validatedQueries := make([]Query, 0)
	seenURLs := make(map[string]bool)
	for _, item := range items {
		for _, endpoint := range item.Endpoints {
			urlTemplate, exists := Endpoints[endpoint]
			if !exists {
				continue
			}

			urlKey := item.Ticker + "|" + urlTemplate
			if seenURLs[urlKey] {
				continue
			}
			seenURLs[urlKey] = true

			// The tier lists are the source of truth. Vanna and charm are on
			// both State and Orderflow, so a single required-tier label
			// would drop them for one of those subscriptions.
			if !tierCollects(filteredTiers, endpoint) {
				continue
			}

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
