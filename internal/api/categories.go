package api

// HistoryCategory maps a snapshot endpoint to the names the intraday history
// route and the end-of-day archive use. The snapshot routes and the recovery
// routes do not share category names, so this is the only translation table.
type HistoryCategory struct {
	Endpoint string
	Package  string
	Category string
}

var historyCategories = []HistoryCategory{
	{Endpoint: "classic_zero", Package: "classic", Category: "gex_zero"},
	{Endpoint: "classic_one", Package: "classic", Category: "gex_one"},
	{Endpoint: "classic_full", Package: "classic", Category: "gex_full"},
	{Endpoint: "state_zero", Package: "state", Category: "gex_zero"},
	{Endpoint: "state_one", Package: "state", Category: "gex_one"},
	{Endpoint: "state_full", Package: "state", Category: "gex_full"},
	{Endpoint: "gamma_zero", Package: "state", Category: "gamma_zero"},
	{Endpoint: "gamma_one", Package: "state", Category: "gamma_one"},
	{Endpoint: "delta_zero", Package: "state", Category: "delta_zero"},
	{Endpoint: "delta_one", Package: "state", Category: "delta_one"},
	{Endpoint: "vanna_zero", Package: "state", Category: "vanna_zero"},
	{Endpoint: "vanna_one", Package: "state", Category: "vanna_one"},
	{Endpoint: "charm_zero", Package: "state", Category: "charm_zero"},
	{Endpoint: "charm_one", Package: "state", Category: "charm_one"},
	{Endpoint: "orderflow", Package: "orderflow", Category: "orderflow"},
}

// HistoryCategoryForEndpoint resolves one snapshot endpoint. The second result
// is false when the recovery routes have no name for it, so it is never
// requested.
func HistoryCategoryForEndpoint(endpoint string) (HistoryCategory, bool) {
	for _, entry := range historyCategories {
		if entry.Endpoint == endpoint {
			return entry, true
		}
	}
	return HistoryCategory{}, false
}

// HistoryCategoriesForEndpoints keeps only the endpoints the account can call,
// in a stable order and without duplicates.
func HistoryCategoriesForEndpoints(endpoints []string) []HistoryCategory {
	wanted := make(map[string]bool, len(endpoints))
	for _, endpoint := range endpoints {
		wanted[endpoint] = true
	}
	out := make([]HistoryCategory, 0, len(endpoints))
	for _, entry := range historyCategories {
		if wanted[entry.Endpoint] {
			out = append(out, entry)
			delete(wanted, entry.Endpoint)
		}
	}
	return out
}

// EndpointForArchive maps an archive entry path back to its snapshot endpoint.
// An unknown entry returns "" and is skipped without being decoded.
func EndpointForArchive(pkg, category string) string {
	for _, entry := range historyCategories {
		if entry.Package == pkg && entry.Category == category {
			return entry.Endpoint
		}
	}
	return ""
}

// ArchivePath is the entry prefix for one category inside the day archive.
func (h HistoryCategory) ArchivePath() string {
	return h.Package + "/" + h.Category
}
