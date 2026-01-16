package coordinator

import (
	"market-terminal/internal/api"
	"market-terminal/internal/config"
)

// QueryPlanItem represents a ticker with its endpoints
type QueryPlanItem struct {
	Ticker    string
	Endpoints []string
}

// QueryPlan represents a query plan
type QueryPlan struct {
	Items []QueryPlanItem
}

// SmartQueryPlanner builds optimized query plans
type SmartQueryPlanner struct {
	settings        *config.Settings
	enabledTickers  []string
	querySystem     *api.QuerySystem
}

// NewSmartQueryPlanner creates a new smart query planner
func NewSmartQueryPlanner(settings *config.Settings, enabledTickers []string, querySystem *api.QuerySystem) *SmartQueryPlanner {
	return &SmartQueryPlanner{
		settings:       settings,
		enabledTickers: enabledTickers,
		querySystem:    querySystem,
	}
}

// endpointPlotsMap maps API endpoints to the plot names they provide
// Used to determine if an endpoint can be skipped when all its plots are hidden
var endpointPlotsMap = map[string][]string{
	"classic_zero":        {"spot", "zero_gamma"},
	"classic_zero_majors": {"major_pos_vol", "major_neg_vol", "major_positive", "major_negative", "major_pos_oi", "major_neg_oi", "major_long_gamma", "major_short_gamma"},
	"gamma_zero":          {"zero_gamma", "major_long_gamma", "major_short_gamma"},
}

// BuildOptimizedPlan builds an optimized query plan for the given tickers
func (sqp *SmartQueryPlanner) BuildOptimizedPlan(tickersToFetch []string) []QueryPlanItem {
	// Get endpoints based on subscription tiers and collection mode
	tiers := sqp.settings.APISubscriptionTiers
	if len(tiers) == 0 {
		tiers = []string{"classic"}
	}

	var endpoints []string
	if sqp.settings.CollectAllEndpoints {
		// Collect all available endpoints for the user's subscription tiers
		endpoints = api.GetEndpointsForTiers(tiers)
	} else {
		// Only collect endpoints needed for chart display
		endpoints = api.GetChartEndpointsForTiers(tiers)
		
		// When in chart-only mode, filter out endpoints where ALL plots are hidden
		hiddenPlots := sqp.settings.HiddenPlots
		if len(hiddenPlots) > 0 {
			endpoints = sqp.filterEndpointsByHiddenPlots(endpoints, hiddenPlots)
		}
	}

	// Build plan
	plan := make([]QueryPlanItem, 0)
	for _, ticker := range tickersToFetch {
		// Check if ticker is enabled
		isEnabled := false
		for _, enabled := range sqp.enabledTickers {
			if enabled == ticker {
				isEnabled = true
				break
			}
		}
		if !isEnabled {
			continue
		}

		plan = append(plan, QueryPlanItem{
			Ticker:    ticker,
			Endpoints: endpoints,
		})
	}

	return plan
}

// filterEndpointsByHiddenPlots filters out endpoints where ALL plots are hidden
// An endpoint is only skipped if every plot it provides is in the hiddenPlots list
func (sqp *SmartQueryPlanner) filterEndpointsByHiddenPlots(endpoints []string, hiddenPlots []string) []string {
	// Create a set of hidden plots for fast lookup
	hiddenSet := make(map[string]bool)
	for _, plot := range hiddenPlots {
		hiddenSet[plot] = true
	}

	filtered := make([]string, 0)
	for _, endpoint := range endpoints {
		plots, exists := endpointPlotsMap[endpoint]
		if !exists {
			// Unknown endpoint - include it by default
			filtered = append(filtered, endpoint)
			continue
		}

		// Check if ALL plots from this endpoint are hidden
		allHidden := true
		for _, plot := range plots {
			if !hiddenSet[plot] {
				allHidden = false
				break
			}
		}

		if !allHidden {
			// At least one plot is visible, include this endpoint
			filtered = append(filtered, endpoint)
		}
	}

	return filtered
}

// SetEnabledTickers updates the list of enabled tickers
func (sqp *SmartQueryPlanner) SetEnabledTickers(tickers []string) {
	sqp.enabledTickers = make([]string, len(tickers))
	copy(sqp.enabledTickers, tickers)
}
