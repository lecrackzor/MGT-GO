package api

// Shared chart columns appear on more than one endpoint. Classic and State
// both return spot, zero_gamma, and the major levels, and the State model
// often reports them as 0 (or as the 1 DTE level). These tables say which
// endpoint owns each column, lowest rank winning, so a later State or Greek
// response never replaces a Classic value. Orderflow sits last for the fields
// it is allowed to write. The live coordinator and the backfill pass share
// these so recovered rows land on the same columns a live poll would.

var classicScalarRank = map[string]int{
	"classic_zero": 0,
	"classic_one":  1,
	"classic_full": 2,
	"state_zero":   3,
	"state_one":    4,
	"state_full":   5,
}

var gammaScalarRank = map[string]int{
	"gamma_zero": 0,
	"gamma_one":  1,
	"state_zero": 2,
	"state_one":  3,
	"state_full": 4,
}

func cloneRanks(src map[string]int) map[string]int {
	out := make(map[string]int, len(src)+1)
	for endpoint, rank := range src {
		out[endpoint] = rank
	}
	return out
}

var chartScalarSources = func() map[string]map[string]int {
	sources := make(map[string]map[string]int, 16)
	for _, field := range []string{
		"zero_gamma", "major_pos_vol", "major_neg_vol", "major_pos_oi",
		"major_neg_oi", "sum_gex_vol", "sum_gex_oi", "delta_risk_reversal",
		"min_dte", "sec_min_dte",
	} {
		sources[field] = cloneRanks(classicScalarRank)
	}
	for _, field := range []string{
		"major_long_gamma", "major_short_gamma", "major_positive", "major_negative",
	} {
		sources[field] = cloneRanks(gammaScalarRank)
	}

	// spot and the major levels are the only chart columns an Orderflow
	// account receives, and the only ones it must be allowed to write.
	spot := cloneRanks(classicScalarRank)
	spot["orderflow"] = 6
	sources["spot"] = spot
	for _, field := range []string{"major_long_gamma", "major_short_gamma", "major_positive", "major_negative"} {
		sources[field]["orderflow"] = 5
	}
	return sources
}()

// ChartScalarSource returns the endpoints allowed to write a shared chart
// column, ranked so the lowest value wins. ok is false when the column has no
// fixed source, in which case the caller should fall back to last-write-wins.
func ChartScalarSource(field string) (ranks map[string]int, ok bool) {
	ranks, ok = chartScalarSources[field]
	return ranks, ok
}
