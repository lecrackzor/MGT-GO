package main

import (
	"context"
	"time"

	"market-terminal/internal/api"
	"market-terminal/internal/backfill"
	"market-terminal/internal/config"
	"market-terminal/internal/database"
	"market-terminal/internal/utils"
)

// runArchiveAndExit is the --archive-run entry point. It runs with no window:
// it recovers the latest session from the end-of-day archive for every enabled
// ticker, retrying until the archive is published or the 19:00 ET cutoff, then
// shuts down. The scheduled task invokes it so a machine that is normally off
// after hours still gathers the day's data.
func runArchiveAndExit() {
	hideConsoleWindow()

	if err := utils.InitLogger("./logs"); err == nil {
		utils.Logf("[archive-run] File logger initialized")
	}

	settingsManager := config.NewSettingsManager("")
	settings, err := settingsManager.LoadSettings()
	if err != nil || settings == nil {
		utils.Logf("[archive-run] ERROR: failed to load settings: %v", err)
		return
	}
	if !settings.EODBackfillEnabled {
		utils.Logf("[archive-run] EOD backfill is disabled; nothing to do")
		return
	}

	debugOn := func(category string) bool {
		return category == "error" || category == "system" || category == "backfill"
	}
	debugPrint := func(msg, category string) {
		if debugOn(category) {
			utils.Logf("[%s] %s", category, msg)
		}
	}

	client := api.NewClient(settings.APITKey, debugPrint)
	client.SetDebugGate(debugOn)
	defer client.Close()
	writer := database.NewDataWriter(settings, debugPrint)
	writer.SetDebugGate(debugOn)
	defer writer.Close()

	manager := backfill.New(client, writer, settingsManager.GetSettings, debugPrint)

	// The archive serves exactly one report: the latest completed session. On a
	// weekday after 17:00 ET that is today; on a weekend it is Friday. Recovering
	// anything older is impossible without a Quant key, so only the latest date
	// is attempted here (unlike the interactive CatchUp, which also probes the
	// previous day and records it as unavailable).
	location := utils.GetMarketTimezone()
	date := utils.GetMarketDate()
	deadline := time.Date(date.Year(), date.Month(), date.Day(), 19, 0, 0, 0, location)
	dateStr := date.Format("2006-01-02")
	utils.Logf("[archive-run] recovering %s for %d enabled ticker(s)", dateStr, len(config.GetEnabledTickers(settings.TickerConfigs)))

	ctx := context.Background()
	for {
		report := manager.RunEOD(ctx, date)
		utils.Logf("[archive-run] pass %s: %d tickers, %d scalars, %d ladders, %d skipped",
			dateStr, report.Tickers, report.Scalars, report.Ladders, len(report.Skipped))

		if len(report.Skipped) == 0 {
			utils.Logf("[archive-run] all tickers recovered; exiting")
			break
		}

		now := time.Now().In(location)
		if !now.Before(deadline) {
			utils.Logf("[archive-run] reached the %s ET cutoff with %d ticker(s) not recovered; exiting",
				deadline.Format("15:04"), len(report.Skipped))
			break
		}

		// The archive may not be published yet. Wait, then retry the tickers the
		// ledger has not recorded.
		utils.Logf("[archive-run] %d ticker(s) not recovered yet; retrying in 5m", len(report.Skipped))
		select {
		case <-time.After(5 * time.Minute):
		case <-ctx.Done():
			return
		}
	}

	// Flush and close the writer before the process exits.
	_ = writer.Close()
}
