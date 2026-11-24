// Command majors-backfill harvests the historical major gamma levels that the
// NinjaTrader majors feed still serves and writes them onto chart timestamps
// that already exist. It runs as a dry run by default; pass --apply to write.
//
// The feed keeps a rolling window of a few sessions and prunes from the front,
// and there is no archive route for these levels, so this is the only way to
// recover a session collected before the live fill existed.
//
// It shares the app's sweep, including the ledger: a session that is complete,
// partial, or no longer served is not requested again, so running this twice
// costs nothing. Only a failed request leaves a day open for a retry.
//
// It only fills empty columns on rows that already exist: it never inserts a
// row and never rewrites a level that is already stored.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"market-terminal/internal/api"
	"market-terminal/internal/backfill"
	"market-terminal/internal/config"
	"market-terminal/internal/database"
	"market-terminal/internal/utils"
)

func main() {
	apply := flag.Bool("apply", false, "write the levels (default is a dry run)")
	days := flag.Int("days", backfill.MajorsLookbackDays(), "how many calendar days back to attempt")
	tickersFlag := flag.String("tickers", "", "comma-separated tickers (default: every enabled ticker)")
	flag.Parse()

	settingsManager := config.NewSettingsManager("")
	settings, err := settingsManager.LoadSettings()
	if err != nil {
		fatal("load settings: %v", err)
	}
	if strings.TrimSpace(settings.NinjaAPIKey) == "" {
		path, _ := config.GetConfigPath()
		fatal("ninja_api_key is not set in %s", path)
	}
	settings = selectTickers(settings, *tickersFlag)
	tickers := config.GetEnabledTickers(settings.TickerConfigs)
	sort.Strings(tickers)
	if len(tickers) == 0 {
		fatal("no tickers to check")
	}

	client := api.NewClient(settings.APITKey, quiet)
	defer client.Close()
	writer := database.NewDataWriter(settings, quiet)
	defer writer.Close()
	loader := database.NewDataLoader(settings, quiet)
	defer loader.Close()

	manager := backfill.New(client, writer, func() *config.Settings { return settings }, func(string, string) {})
	manager.SetMajorsDryRun(!*apply)
	manager.SetMajorLevelStore(func(ctx context.Context, ticker string, date time.Time) (backfill.MajorsResult, error) {
		return harvest(ctx, *apply, client, writer, loader, settings, ticker, date)
	})

	fmt.Printf("mode: %s\n", modeLabel(*apply))
	fmt.Printf("tickers: %d (%s)\n", len(tickers), strings.Join(tickers, ", "))
	fmt.Printf("lookback: %d calendar day(s), weekends skipped\n", *days)

	filled := manager.SweepMajors(context.Background(), *days)
	if *apply {
		fmt.Printf("\ntotal timestamps written: %d\n", filled)
	} else {
		fmt.Printf("\ntotal timestamps that would be written: %d\n", filled)
	}
}

// harvest is one ticker-day: read the timestamps that still need a level, ask
// the feed for the session, and write the matches. It mirrors the app's own
// fill so the dry run reports exactly what --apply would do.
func harvest(ctx context.Context, apply bool, client *api.Client, writer *database.DataWriter, loader *database.DataLoader, settings *config.Settings, ticker string, date time.Time) (backfill.MajorsResult, error) {
	pending, err := loader.LoadBarsMissingMajorLevels(ticker, date)
	if err != nil {
		return backfill.MajorsResult{}, fmt.Errorf("read chart rows: %w", err)
	}
	if len(pending) == 0 {
		fmt.Printf("  %-8s %s nothing to fill\n", ticker, date.Format("2006-01-02"))
		return backfill.MajorsResult{}, nil
	}
	open, closeAt := sessionBounds(date)
	samples, err := client.FetchHistoricalMajors(ctx, settings.NinjaAPIKey, ticker, open, closeAt)
	if err != nil {
		return backfill.MajorsResult{}, fmt.Errorf("feed: %w", err)
	}
	result := backfill.MajorsResult{Samples: len(samples)}
	if len(samples) == 0 {
		result.Pending = len(pending)
		fmt.Printf("  %-8s %s feed no longer serves this session (0 samples); %d empty row(s) left\n",
			ticker, date.Format("2006-01-02"), len(pending))
		return result, nil
	}
	levels := alignToRows(pending, samples)
	if len(levels) == 0 {
		result.Pending = len(pending)
		fmt.Printf("  %-8s %s feed has %d sample(s) but none cover the %d empty row(s)\n",
			ticker, date.Format("2006-01-02"), len(samples), len(pending))
		return result, nil
	}
	ordered := append([]api.HistMajor(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Timestamp < ordered[j].Timestamp })
	et := utils.GetMarketTimezone()
	fmt.Printf("  %-8s %s feed %d sample(s) %s..%s ET -> %d of %d empty row(s)\n",
		ticker, date.Format("2006-01-02"), len(samples),
		time.Unix(ordered[0].Timestamp, 0).In(et).Format("15:04:05"),
		time.Unix(ordered[len(ordered)-1].Timestamp, 0).In(et).Format("15:04:05"),
		len(levels), len(pending))
	if !apply {
		result.Filled = len(levels)
		return result, nil
	}
	written, err := writer.FillMajorLevels(ticker, date, levels)
	if err != nil {
		return result, fmt.Errorf("write: %w", err)
	}
	result.Filled = written
	if left, errLeft := loader.LoadBarsMissingMajorLevels(ticker, date); errLeft == nil {
		result.Pending = len(left)
	} else {
		result.Pending = len(pending) - written
	}
	fmt.Printf("  %-8s %s wrote %d timestamp(s), %d left empty\n",
		ticker, date.Format("2006-01-02"), written, result.Pending)
	return result, nil
}

// alignToRows matches the app's rule: use the latest sample at or before the
// timestamp, and only when that sample is no more than 300 s old.
func alignToRows(timestamps database.ChartSeries, samples []api.HistMajor) []database.MajorLevel {
	if len(timestamps) == 0 || len(samples) == 0 {
		return nil
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Timestamp < samples[j].Timestamp })
	out := make([]database.MajorLevel, 0, len(timestamps))
	idx := 0
	for _, ts := range timestamps {
		row := int64(ts)
		for idx+1 < len(samples) && samples[idx+1].Timestamp <= row {
			idx++
		}
		sample := samples[idx]
		if sample.Timestamp > row || row-sample.Timestamp > 300 {
			continue
		}
		call, put := sample.Call, sample.Put
		if !liveValue(call) && !liveValue(put) {
			continue
		}
		if !liveValue(call) {
			call = 0
		}
		if !liveValue(put) {
			put = 0
		}
		out = append(out, database.MajorLevel{Timestamp: ts, Call: call, Put: put})
	}
	return out
}

func liveValue(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v != 0
}

// sessionBounds returns the US session open and close for a day in Unix
// seconds, so a request never spans two sessions.
func sessionBounds(day time.Time) (int64, int64) {
	et := utils.GetMarketTimezone()
	start := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, et)
	end := time.Date(day.Year(), day.Month(), day.Day(), 16, 0, 0, 0, et)
	return start.Unix(), end.Unix()
}

// selectTickers returns settings whose ticker map holds only the requested
// tickers, so the shared sweep walks exactly that set.
func selectTickers(settings *config.Settings, value string) *config.Settings {
	if strings.TrimSpace(value) == "" {
		return settings
	}
	wanted := make(map[string]config.TickerConfig)
	for _, part := range strings.Split(value, ",") {
		name := strings.ToUpper(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if !config.ValidTicker(name) {
			fmt.Fprintf(os.Stderr, "skip %s: not a known ticker\n", name)
			continue
		}
		wanted[name] = config.TickerConfig{CollectionEnabled: true}
	}
	settings.TickerConfigs = wanted
	return settings
}

func modeLabel(apply bool) string {
	if apply {
		return "APPLY (writing levels onto existing rows)"
	}
	return "DRY RUN (pass --apply to write)"
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// quiet satisfies the packages' debug printer; this command reports its own
// results and does not want a per-query log line.
func quiet(string, string) {}
