package config

// Ticker groups are the single source of truth for the symbol list. The
// frontend builds its ticker table from /api/tickers, so Go and JS cannot
// drift. Order is the display order.
var TickerGroups = []struct {
	Name    string
	Tickers []string
}{
	{"futures", []string{"ES_SPX", "NQ_NDX"}},
	{"indexes", []string{"SPX", "VIX", "NDX", "RUT", "IWM", "QQQ", "SPY"}},
	{"mag7", []string{"AAPL", "AMZN", "GOOGL", "META", "MSFT", "NVDA", "TSLA"}},
	{"stocks", []string{
		"AMD", "APP", "AVGO", "BABA", "BOIL", "COIN", "CRWD", "CRWV", "DDOG", "DIA",
		"GLD", "GOOG", "GME", "HOOD", "HYG", "IBIT", "INTC", "IONQ", "MSTR", "MU",
		"NFLX", "NVO", "ORCL", "PL", "PLTR", "RDDT", "SHOP", "SLV", "SMCI", "SNDK",
		"SNOW", "SPCX", "SOFI", "TLT", "TQQQ", "TSM", "UBER", "UNG", "UNH", "USO",
		"UVXY", "VALE", "ZS",
	}},
}

// KnownTickerList is every symbol the app collects, in display order.
var KnownTickerList = func() []string {
	out := make([]string, 0, 48)
	for _, group := range TickerGroups {
		out = append(out, group.Tickers...)
	}
	return out
}()

// KnownTickers is the membership set. A ticker outside it is not a database
// filename.
var KnownTickers = func() map[string]struct{} {
	set := make(map[string]struct{}, len(KnownTickerList))
	for _, ticker := range KnownTickerList {
		set[ticker] = struct{}{}
	}
	return set
}()

// ValidTicker reports whether ticker is one of the symbols the app collects.
func ValidTicker(ticker string) bool {
	_, ok := KnownTickers[ticker]
	return ok
}
