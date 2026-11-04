package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HistMajor is one sample from the Ninja historical majors feed.
// Call is major_call_gamma. Put is major_put_gamma. Zero is zero_gamma.
type HistMajor struct {
	Timestamp int64
	Call      float64
	Put       float64
	Zero      float64
}

// FetchHistoricalMajors loads the same series the NinjaTrader gexbot indicator draws.
// POST https://api.gex.bot/v2/ninja/hist/{ticker}/classic/gex_zero/majors/7
// The custom REST key is rejected for this route; it needs the Ninja integration key.
func (c *Client) FetchHistoricalMajors(ctx context.Context, ninjaKey, ticker string, start, end int64) ([]HistMajor, error) {
	ninjaKey = strings.TrimSpace(ninjaKey)
	if ninjaKey == "" {
		return nil, fmt.Errorf("ninja API key is not set")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	url := "https://api.gex.bot/v2/ninja/hist/" + ticker + "/classic/gex_zero/majors/7"
	body, err := json.Marshal(map[string]int64{"start": start, "end": end})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Basic "+ninjaKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "NinjaTrader 8")
	req.Header.Set("X-gexbot-plugin", "v2.2026.07.07")

	c.requestCount.Add(1)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(raw)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("historical majors HTTP %d: %s", resp.StatusCode, msg)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := make([]HistMajor, 0, len(rows))
	for _, row := range rows {
		ts, ok := jsonFloat(row["timestamp"])
		if !ok || ts <= 0 {
			continue
		}
		out = append(out, HistMajor{
			Timestamp: int64(ts),
			Call:      firstFloat(row, "major_call_gamma"),
			Put:       firstFloat(row, "major_put_gamma"),
			Zero:      firstFloat(row, "zero_gamma"),
		})
	}
	return out, nil
}

func firstFloat(row map[string]interface{}, names ...string) float64 {
	for _, name := range names {
		if v, ok := jsonFloat(row[name]); ok && v != 0 {
			return v
		}
	}
	return 0
}

func jsonFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
