package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"market-terminal/internal/api"
	"market-terminal/internal/backfill"
	"market-terminal/internal/config"
	"market-terminal/internal/database"
	"market-terminal/internal/utils"
)

// The major call and put gamma levels come from the Ninja historical majors
// feed, not from a chain snapshot, so a chart that is never opened never
// collects them and the pre-created columns stay zero. The feed also prunes
// old sessions, which means a level that is only ever held in memory is lost
// with the process and gone for good once the feed drops the day.
//
// So every level the app fetches is written to the day it belongs to:
//   - a chart window that loads a day stores the levels it just received,
//   - a live ticker is topped up once a minute whether or not a chart is open,
//   - an archive pass stores them for every enabled ticker at the close.
//
// FillMajorLevels only fills empty columns on timestamps that already exist,
// so all three paths can overlap without fighting each other.

// majorLevelInterval is how often live tickers ask the feed for levels that
// arrived since the last pass. The feed answers a whole session per call, and
// the chart cache serves an append-only refresh, so this is one request per
// ticker per minute at most.
const majorLevelInterval = time.Minute

// majorLevelsFromSamples aligns feed samples onto the bars that still need a
// level and returns only the bars the feed can speak for. A bar with no sample
// within the freshness window is skipped rather than stored as zero.
func majorLevelsFromSamples(timestamps database.ChartSeries, samples []api.HistMajor) []database.MajorLevel {
	if len(timestamps) == 0 || len(samples) == 0 {
		return nil
	}
	call, put := alignHistoricalMajors(timestamps, samples)
	out := make([]database.MajorLevel, 0, len(timestamps))
	for i, ts := range timestamps {
		c, p := call[i], put[i]
		if !isLevelValue(c) && !isLevelValue(p) {
			continue
		}
		if !isLevelValue(c) {
			c = 0
		}
		if !isLevelValue(p) {
			p = 0
		}
		out = append(out, database.MajorLevel{Timestamp: ts, Call: c, Put: p})
	}
	return out
}

func isLevelValue(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v != 0
}

// storeMajorLevels writes the levels the feed returned onto the timestamps of
// one ticker and day that have none yet, and reports what is left afterwards.
//
// Only empty rows are considered, so this is the gap fill as well as the live
// store: a row recovered later by the intraday pass is picked up on the next
// run, and a row already holding a level is never rewritten.
func (a *App) storeMajorLevels(ticker string, date time.Time, samples []api.HistMajor) (backfill.MajorsResult, error) {
	result := backfill.MajorsResult{Samples: len(samples)}
	if a.dataWriter == nil || a.dataLoader == nil || len(samples) == 0 {
		return result, nil
	}
	pending, err := a.dataLoader.LoadBarsMissingMajorLevels(ticker, date)
	if err != nil {
		return result, err
	}
	if len(pending) == 0 {
		return result, nil
	}
	levels := majorLevelsFromSamples(pending, samples)
	if len(levels) == 0 {
		// The feed answered but served none of this session's timestamps, so
		// nothing more can be filled from it.
		result.Pending = len(pending)
		return result, nil
	}
	filled, err := a.dataWriter.FillMajorLevels(ticker, date, levels)
	result.Filled = filled
	if err != nil {
		return result, err
	}
	// Re-read so the report is what the file actually holds, not an
	// assumption from the counts.
	if left, errLeft := a.dataLoader.LoadBarsMissingMajorLevels(ticker, date); errLeft == nil {
		result.Pending = len(left)
	}
	return result, nil
}

// syncMajorLevels fetches the session's levels and stores them. It shares the
// chart's cache, so a live chart of the same ticker does not add a request.
func (a *App) syncMajorLevels(ctx context.Context, ticker string, date time.Time) (backfill.MajorsResult, error) {
	if a.apiClient == nil || a.settingsManager == nil {
		return backfill.MajorsResult{}, nil
	}
	settings := a.settingsManager.GetSettings()
	if settings == nil {
		return backfill.MajorsResult{}, nil
	}
	start, end := usSessionBounds(date)
	samples, err := a.historicalMajors(ctx, settings.NinjaAPIKey, ticker, date, start, end)
	if err != nil {
		return backfill.MajorsResult{}, fmt.Errorf("fetch session: %w", err)
	}
	return a.storeMajorLevels(ticker, date, samples)
}

// startMajorLevelsSync tops up the levels for every live ticker while the
// market is open, so a ticker whose chart is never opened still keeps its
// series. Archive-only tickers are covered by the archive pass at the close.
func (a *App) startMajorLevelsSync() {
	if a.settingsManager == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(majorLevelInterval)
		defer ticker.Stop()
		for range ticker.C {
			settings := a.settingsManager.GetSettings()
			if settings == nil || !utils.IsMarketOpen() {
				continue
			}
			date := utils.GetMarketDate()
			day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())
			for _, name := range config.GetLiveTickers(settings.TickerConfigs) {
				res, err := a.syncMajorLevels(context.Background(), name, day)
				if err != nil {
					// Always logged: this is the only trace of a failed fill.
					utils.Logf("[majors] %s %s: %v", name, day.Format("2006-01-02"), err)
					continue
				}
				if res.Filled > 0 {
					utils.Logf("[majors] %s %s: filled %d timestamp(s)", name, day.Format("2006-01-02"), res.Filled)
				}
			}
		}
	}()
}
