package main

import (
	"context"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

func (a *App) watchMarketClose() {
	if a.ctx == nil {
		return
	}
	go a.marketCloseLoop(a.ctx)
}

func (a *App) marketCloseLoop(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	seenOpen := utils.IsMarketOpen()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			settings := a.settingsManager.GetSettings()
			enabled := settings != nil && settings.CloseAfterMarketClose
			open := utils.IsMarketOpen()
			quit, nextSeen := utils.ShouldQuitAfterClose(enabled, seenOpen, open)
			seenOpen = nextSeen
			if quit {
				a.fillTodayBeforeQuit()
				a.requestQuit("Market closed. Close after the market closes is on.")
				return
			}
		}
	}
}

// fillTodayBeforeQuit closes the session's gaps from the intraday history route
// before the app exits. It is bounded so a slow or rate limited API cannot hold
// the quit; the archive pass covers whatever is left on the next start.
func (a *App) fillTodayBeforeQuit() {
	if a.backfillManager == nil {
		return
	}
	settings := a.settingsManager.GetSettings()
	if settings == nil || !settings.EODBackfillEnabled || !settings.IntradayBackfillEnabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	report := a.backfillManager.FillToday(ctx)
	utils.Logf("[system] History gap fill before quit: %d tickers, %d requests, %d rows, %d ladders",
		report.Tickers, report.Requests, report.Scalars, report.Ladders)
}

// watchBackfill recovers the last completed session at startup and runs the
// archive pass after the configured evening time while the app is still open.
func (a *App) watchBackfill() {
	if a.ctx == nil || a.backfillManager == nil {
		return
	}
	go func() {
		a.backfillManager.CatchUp(a.ctx)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case now := <-ticker.C:
				a.backfillManager.RunEODIfDue(a.ctx, now)
				a.backfillManager.RunIntradayIfDue(a.ctx, now)
			}
		}
	}()
}

// BackfillStatus is served to the UI so a recovery pass is visible.
func (a *App) BackfillStatus() interface{} {
	if a.backfillManager == nil {
		return nil
	}
	return a.backfillManager.Status()
}

func (a *App) requestQuit(reason string) {
	a.shutdownLock.Lock()
	if a.shuttingDown {
		a.shutdownLock.Unlock()
		return
	}
	a.shuttingDown = true
	a.shutdownLock.Unlock()
	utils.Logf("[system] %s", reason)
	if app, ok := a.appRef.(*application.App); ok && app != nil {
		app.Quit()
	}
}

func (a *App) syncAutoStart(settings *config.Settings) {
	enabled := settings != nil && settings.StartBeforeMarketOpen
	// A save that does not touch this setting must not spawn schtasks: the
	// settings window now closes as soon as the save returns.
	a.autoStartLock.Lock()
	if a.autoStartKnown && a.autoStartEnabled == enabled {
		a.autoStartLock.Unlock()
		return
	}
	a.autoStartLock.Unlock()
	if err := syncAutoStartTask(enabled); err != nil {
		utils.Logf("[system] start before the open: %v", err)
		return
	}
	a.autoStartLock.Lock()
	a.autoStartKnown = true
	a.autoStartEnabled = enabled
	a.autoStartLock.Unlock()
}
