package api

// Endpoints maps endpoint names to URL templates
// Template format: "%s/{ticker}/classic/zero?key=%s" (base, ticker, key)
var Endpoints = map[string]string{
	// Classic Subscription Endpoints
	"classic_full":         "%s/%s/classic/full?key=%s",
	"classic_zero":         "%s/%s/classic/zero?key=%s",
	"classic_one":          "%s/%s/classic/one?key=%s",
	"classic_full_majors":  "%s/%s/classic/full/majors?key=%s",
	"classic_zero_majors":  "%s/%s/classic/zero/majors?key=%s",
	"classic_one_majors":   "%s/%s/classic/one/majors?key=%s",
	"classic_full_maxchange": "%s/%s/classic/full/maxchange?key=%s",
	"classic_zero_maxchange": "%s/%s/classic/zero/maxchange?key=%s",
	"classic_one_maxchange":   "%s/%s/classic/one/maxchange?key=%s",

	// State Subscription Endpoints
	"state_full":         "%s/%s/state/full?key=%s",
	"state_zero":         "%s/%s/state/zero?key=%s",
	"state_one":          "%s/%s/state/one?key=%s",
	"state_full_majors":  "%s/%s/state/full/majors?key=%s",
	"state_zero_majors":  "%s/%s/state/zero/majors?key=%s",
	"state_one_majors":   "%s/%s/state/one/majors?key=%s",
	"state_full_maxchange": "%s/%s/state/full/maxchange?key=%s",
	"state_zero_maxchange": "%s/%s/state/zero/maxchange?key=%s",
	"state_one_maxchange":   "%s/%s/state/one/maxchange?key=%s",
	
	// Options Profile Greeks - New API structure (State subscription)
	"delta_zero": "%s/%s/state/delta_zero?key=%s",
	"gamma_zero": "%s/%s/state/gamma_zero?key=%s",
	"delta_one":  "%s/%s/state/delta_one?key=%s",
	"gamma_one":  "%s/%s/state/gamma_one?key=%s",
	
	// Options Profile Greeks - New API structure (Orderflow subscription)
	"charm_zero": "%s/%s/state/charm_zero?key=%s",
	"vanna_zero": "%s/%s/state/vanna_zero?key=%s",
	"charm_one":  "%s/%s/state/charm_one?key=%s",
	"vanna_one":  "%s/%s/state/vanna_one?key=%s",
	
	// Legacy endpoint names (deprecated, kept for backwards compatibility)
	"state_gamma":     "%s/%s/state/gamma_zero?key=%s",
	"state_onegamma":  "%s/%s/state/gamma_one?key=%s",
	"state_delta":     "%s/%s/state/delta_zero?key=%s",
	"state_onedelta":  "%s/%s/state/delta_one?key=%s",
	"state_vanna":     "%s/%s/state/vanna_zero?key=%s",
	"state_onevanna":  "%s/%s/state/vanna_one?key=%s",
	"state_charm":     "%s/%s/state/charm_zero?key=%s",
	"state_onecharm":  "%s/%s/state/charm_one?key=%s",

	// Orderflow Subscription Endpoints
	"orderflow": "%s/%s/orderflow/orderflow?key=%s",
}

// GetEndpointsForTiers returns all endpoints available for the given subscription tiers
func GetEndpointsForTiers(tiers []string) []string {
	tierEndpoints := map[string][]string{
		"classic": {
			"classic_full", "classic_zero", "classic_one",
			"classic_full_majors", "classic_zero_majors", "classic_one_majors",
			"classic_full_maxchange", "classic_zero_maxchange", "classic_one_maxchange",
		},
		"state": {
			"state_full", "state_zero", "state_one",
			"state_full_majors", "state_zero_majors", "state_one_majors",
			"state_full_maxchange", "state_zero_maxchange", "state_one_maxchange",
			"delta_zero", "gamma_zero", "delta_one", "gamma_one",
			"state_gamma", "state_onegamma", "state_delta", "state_onedelta",
		},
		"orderflow": {
			"orderflow",
			"charm_zero", "vanna_zero", "charm_one", "vanna_one",
			"state_vanna", "state_onevanna", "state_charm", "state_onecharm",
		},
	}

	result := make([]string, 0)
	seen := make(map[string]bool)

	for _, tier := range tiers {
		if endpoints, ok := tierEndpoints[tier]; ok {
			for _, endpoint := range endpoints {
				if !seen[endpoint] {
					result = append(result, endpoint)
					seen[endpoint] = true
				}
			}
		}
	}

	return result
}

// GetChartEndpointsForTiers returns only the endpoints needed for chart display
// This is a subset of all available endpoints, useful for minimal data collection
func GetChartEndpointsForTiers(tiers []string) []string {
	// Chart-only endpoints (minimal set for chart display)
	// These endpoints provide: spot, zero_gamma, major volumes, major gamma, major positions
	tierChartEndpoints := map[string][]string{
		"classic": {
			"classic_zero",        // spot, zero_gamma
			"classic_zero_majors", // major volumes, gamma, positions
		},
		"state": {
			"gamma_zero", // State tier gamma data
		},
		"orderflow": {
			// Orderflow doesn't have specific chart endpoints yet
		},
	}

	result := make([]string, 0)
	seen := make(map[string]bool)

	for _, tier := range tiers {
		if endpoints, ok := tierChartEndpoints[tier]; ok {
			for _, endpoint := range endpoints {
				if !seen[endpoint] {
					result = append(result, endpoint)
					seen[endpoint] = true
				}
			}
		}
	}

	return result
}

// GetEndpointTier returns the subscription tier required for an endpoint
func GetEndpointTier(endpoint string) string {
	classicEndpoints := map[string]bool{
		"classic_full": true, "classic_zero": true, "classic_one": true,
		"classic_full_majors": true, "classic_zero_majors": true, "classic_one_majors": true,
		"classic_full_maxchange": true, "classic_zero_maxchange": true, "classic_one_maxchange": true,
	}
	
	stateEndpoints := map[string]bool{
		"state_full": true, "state_zero": true, "state_one": true,
		"state_full_majors": true, "state_zero_majors": true, "state_one_majors": true,
		"state_full_maxchange": true, "state_zero_maxchange": true, "state_one_maxchange": true,
		"delta_zero": true, "gamma_zero": true, "delta_one": true, "gamma_one": true,
		"state_gamma": true, "state_onegamma": true, "state_delta": true, "state_onedelta": true,
	}
	
	orderflowEndpoints := map[string]bool{
		"orderflow": true,
		"charm_zero": true, "vanna_zero": true, "charm_one": true, "vanna_one": true,
		"state_vanna": true, "state_onevanna": true, "state_charm": true, "state_onecharm": true,
	}

	if classicEndpoints[endpoint] {
		return "classic"
	}
	if stateEndpoints[endpoint] {
		return "state"
	}
	if orderflowEndpoints[endpoint] {
		return "orderflow"
	}
	return "" // Unknown endpoint
}
