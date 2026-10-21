package config

// KnownTickers matches ALL_TICKERS in frontend/main.js.
// A ticker that is not in this set is not a database filename.
var KnownTickers = map[string]struct{}{
	"ES_SPX": {}, "NQ_NDX": {},
	"SPX": {}, "VIX": {}, "NDX": {}, "RUT": {}, "IWM": {}, "QQQ": {}, "SPY": {},
	"AAPL": {}, "AMZN": {}, "GOOGL": {}, "META": {}, "MSFT": {}, "NVDA": {}, "TSLA": {},
	"AMD": {}, "APP": {}, "AVGO": {}, "BABA": {}, "COIN": {}, "CRWD": {}, "CRWV": {},
	"GLD": {}, "GOOG": {}, "GME": {}, "HOOD": {}, "HYG": {}, "IBIT": {}, "INTC": {},
	"IONQ": {}, "MSTR": {}, "MU": {}, "NFLX": {}, "PLTR": {}, "SLV": {}, "SMCI": {},
	"SNOW": {}, "SOFI": {}, "TLT": {}, "TQQQ": {}, "TSM": {}, "UNH": {}, "USO": {},
	"UVXY": {}, "VALE": {},
}

// ValidTicker reports whether ticker is one of the symbols the app collects.
func ValidTicker(ticker string) bool {
	_, ok := KnownTickers[ticker]
	return ok
}
