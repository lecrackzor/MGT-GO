// Package backfill recovers chart rows and ladders the live poll did not
// collect. It uses only the endpoints a Classic, State or Orderflow account can
// call: the intraday history route right after the close, and the end-of-day
// archive for the previous session. A recovery pass never rewrites a live row
// and never inserts into a live delta chain.
package backfill

import (
	"database/sql"
	"os"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"market-terminal/internal/utils"
)

// gap is a missing span inside one session, in Unix seconds.
type gap struct {
	start float64
	end   float64
}

// sessionBounds is the cash session for a market date, 09:30 to 16:00 ET by
// default and 13:00 on an early close.
func sessionBounds(date time.Time) (float64, float64) {
	open, closeAt := utils.MarketOpenCloseTimes(date)
	return float64(open.Unix()), float64(closeAt.Unix())
}

// missingRanges lists the parts of a session with no chart row. A range shorter
// than minGap is not worth a request and is left to the archive pass.
func missingRanges(chartPath string, open, closeAt, minGap float64) ([]gap, error) {
	timestamps, err := chartTimestamps(chartPath, open, closeAt)
	if err != nil {
		return nil, err
	}
	if len(timestamps) == 0 {
		if closeAt-open <= minGap {
			return nil, nil
		}
		return []gap{{start: open, end: closeAt}}, nil
	}
	var gaps []gap
	previous := open - 1
	for _, ts := range timestamps {
		if ts-previous-1 >= minGap {
			gaps = append(gaps, gap{start: previous + 1, end: ts - 1})
		}
		previous = ts
	}
	if closeAt-previous >= minGap {
		gaps = append(gaps, gap{start: previous + 1, end: closeAt})
	}
	return gaps, nil
}

// splitGaps cuts a range at the eight hour history limit.
func splitGaps(gaps []gap, maxSeconds float64) []gap {
	var out []gap
	for _, item := range gaps {
		for start := item.start; start <= item.end; start += maxSeconds {
			end := start + maxSeconds - 1
			if end > item.end {
				end = item.end
			}
			out = append(out, gap{start: start, end: end})
		}
	}
	return out
}

// chartTimestamps lists the seconds a chart file already holds. A missing file
// or table answers with nothing, so the whole session is recoverable.
func chartTimestamps(path string, start, end float64) ([]float64, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	rows, err := db.Query(`SELECT timestamp FROM ticker_data WHERE timestamp >= ? AND timestamp <= ? ORDER BY timestamp`, start, end)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var ts float64
		if err := rows.Scan(&ts); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

// liveProfileTimestamps lists the seconds the live delta chain already covers for
// one endpoint. Recovered ladders are only written where this is empty.
func liveProfileTimestamps(path, endpoint string, start, end float64) ([]float64, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	rows, err := db.Query(`SELECT timestamp FROM endpoint_profile WHERE endpoint = ? AND timestamp >= ? AND timestamp <= ? ORDER BY timestamp`, endpoint, start, end)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var ts float64
		if err := rows.Scan(&ts); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

// covered reports whether a live ladder sits within one cadence step before ts.
func covered(live []float64, ts, cadence float64) bool {
	i := sort.SearchFloat64s(live, ts)
	return i > 0 && ts-live[i-1] <= cadence
}

func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}
