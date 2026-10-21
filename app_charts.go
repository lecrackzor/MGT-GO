package main

import (
	"fmt"
	"net/url"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"market-terminal/internal/config"
)

// createWindowFunc is set from main.go to create windows
// This avoids circular dependency issues with application types
var createWindowFunc func(application.WebviewWindowOptions) *application.WebviewWindow

// SetCreateWindowFunc sets the function to create windows (called from main.go)
func SetCreateWindowFunc(fn func(application.WebviewWindowOptions) *application.WebviewWindow) {
	createWindowFunc = fn
}

func createWindowFromApp(appRef interface{}, options application.WebviewWindowOptions) *application.WebviewWindow {
	if createWindowFunc != nil {
		return createWindowFunc(options)
	}
	return nil
}

// clampToWorkArea keeps a window inside the primary monitor's work area.
// InitialPosition WindowCentered then places that size in the middle of the screen.
func clampToWorkArea(appRef interface{}, width, height, minW, minH int) (int, int) {
	app, ok := appRef.(*application.App)
	if !ok || app == nil || app.Screen == nil {
		return width, height
	}
	screen := app.Screen.GetPrimary()
	if screen == nil || screen.WorkArea.Width <= 0 || screen.WorkArea.Height <= 0 {
		return width, height
	}
	maxW := screen.WorkArea.Width
	maxH := screen.WorkArea.Height
	if width > maxW {
		width = maxW
	}
	if height > maxH {
		height = maxH
	}
	if width < minW && minW <= maxW {
		width = minW
	}
	if height < minH && minH <= maxH {
		height = minH
	}
	return width, height
}

// applyRuntimeSettings pushes the current settings into the collectors that keep their own pointer.
func (a *App) applyRuntimeSettings(settings *config.Settings) {
	if settings == nil {
		return
	}
	if a.apiClient != nil && settings.APITKey != "" {
		a.apiClient.SetAPIKey(settings.APITKey)
	}
	if a.querySystem != nil {
		a.querySystem.SetSettings(settings)
	}
	if a.dataWriter != nil {
		a.dataWriter.SetSettings(settings)
	}
	if a.dataLoader != nil {
		a.dataLoader.SetSettings(settings)
	}
	if a.scheduler != nil {
		a.scheduler.SetSettings(settings)
	}
}

func (a *App) unregisterChartTicker(ticker string) {
	if a.chartTracker != nil {
		a.chartTracker.UnregisterTicker(ticker)
		a.debugPrint(fmt.Sprintf("Unregistered ticker display: %s", ticker), "system")
	}
}

// releaseTickerDisplay drops high-priority polling only when neither window is open.
func (a *App) releaseTickerDisplay(ticker string) {
	a.chartWindowsLock.RLock()
	_, chartOpen := a.chartWindows[ticker]
	_, profileOpen := a.profileWindows[ticker]
	a.chartWindowsLock.RUnlock()
	if chartOpen || profileOpen {
		return
	}
	a.unregisterChartTicker(ticker)
}

func (a *App) watchChartWindow(ticker string, window *application.WebviewWindow) {
	window.OnWindowEvent(events.Common.WindowClosing, func(event *application.WindowEvent) {
		a.chartWindowsLock.Lock()
		if current, ok := a.chartWindows[ticker]; ok && current == window {
			delete(a.chartWindows, ticker)
		}
		a.chartWindowsLock.Unlock()
		a.releaseTickerDisplay(ticker)
	})
}

// RegisterTickerDisplay registers a ticker as being displayed in the frontend
func (a *App) RegisterTickerDisplay(ticker string) {
	if a.chartTracker != nil {
		a.chartTracker.RegisterTicker(ticker)
		a.debugPrint(fmt.Sprintf("Registered ticker display: %s", ticker), "system")
	}
}

// SetApp sets the Wails application reference (called from main.go)
func (a *App) SetApp(app interface{}) {
	a.appRef = app
}

// OpenChartWindow creates and opens a new chart window for a ticker
// dateStr is optional - if empty, chart will use current market date
func (a *App) OpenChartWindow(ticker string, dateStr string) error {
	if a.appRef == nil {
		return fmt.Errorf("application not initialized")
	}

	// Replace an existing window for this ticker. Drop it from the map
	// before Close() so the close callback does not race the new window.
	a.chartWindowsLock.Lock()
	existingWindow := a.chartWindows[ticker]
	if existingWindow != nil {
		delete(a.chartWindows, ticker)
	}
	a.chartWindowsLock.Unlock()
	if existingWindow != nil {
		a.debugPrint(fmt.Sprintf("OpenChartWindow: Closing existing window for %s before creating new one", ticker), "app")
		existingWindow.Close()
		a.releaseTickerDisplay(ticker)
	}

	return a.openChartWindow(ticker, dateStr, false)
}

func (a *App) openChartWindow(ticker string, dateStr string, dockProfile bool) error {
	if !config.ValidTicker(ticker) {
		return fmt.Errorf("unknown ticker %q", ticker)
	}
	page := fmt.Sprintf("/chart.html?ticker=%s", url.QueryEscape(ticker))
	if dateStr != "" {
		page += "&date=" + url.QueryEscape(dateStr)
	}
	if dockProfile {
		page += "&dock=1"
	}

	// Create new window using chart.html file with ticker and date parameters
	// The chart.html file will be served by the asset server
	chartWidth, chartHeight := clampToWorkArea(a.appRef, 1200, 800, 600, 400)
	window := createWindowFromApp(a.appRef, application.WebviewWindowOptions{
		Title:            fmt.Sprintf("%s Chart", ticker),
		Width:            chartWidth,
		Height:           chartHeight,
		MinWidth:         600,
		MinHeight:        400,
		InitialPosition:  application.WindowCentered,
		URL:              page,
		BackgroundColour: application.NewRGB(30, 30, 30),
	})

	if window == nil {
		return fmt.Errorf("failed to create chart window")
	}

	a.watchChartWindow(ticker, window)

	a.chartWindowsLock.Lock()
	a.chartWindows[ticker] = window
	a.chartWindowsLock.Unlock()

	a.RegisterTickerDisplay(ticker)
	return nil
}

// OpenProfileWindow opens a profile-only window. An existing one is focused.
// It does not replace the chart window for the same ticker.
func (a *App) OpenProfileWindow(ticker string, dateStr string) error {
	if a.appRef == nil {
		return fmt.Errorf("application not initialized")
	}
	a.chartWindowsLock.RLock()
	existing := a.profileWindows[ticker]
	a.chartWindowsLock.RUnlock()
	if existing != nil {
		existing.Restore()
		existing.Focus()
		return nil
	}

	page := fmt.Sprintf("/profile.html?ticker=%s", url.QueryEscape(ticker))
	if dateStr != "" {
		page += "&date=" + url.QueryEscape(dateStr)
	}
	width, height := a.profileWindowSize()
	x, y := a.profileWindowOrigin(width, height)
	window := createWindowFromApp(a.appRef, application.WebviewWindowOptions{
		Title:            fmt.Sprintf("%s Profile", ticker),
		Width:            width,
		Height:           height,
		MinWidth:         320,
		MinHeight:        420,
		InitialPosition:  application.WindowXY,
		X:                x,
		Y:                y,
		URL:              page,
		BackgroundColour: application.NewRGB(30, 30, 30),
	})
	if window == nil {
		return fmt.Errorf("failed to create profile window")
	}
	a.watchProfileWindow(ticker, window)
	a.chartWindowsLock.Lock()
	a.profileWindows[ticker] = window
	a.chartWindowsLock.Unlock()
	a.RegisterTickerDisplay(ticker)
	return nil
}

// CloseProfileWindow closes the profile-only window for a ticker, if one is open.
func (a *App) CloseProfileWindow(ticker string) error {
	a.chartWindowsLock.RLock()
	existing := a.profileWindows[ticker]
	a.chartWindowsLock.RUnlock()
	if existing != nil {
		existing.Close()
	}
	return nil
}

// AttachProfileToChart focuses the ticker's chart, or opens one docked to the side profile.
func (a *App) AttachProfileToChart(ticker string, dateStr string) error {
	if a.appRef == nil {
		return fmt.Errorf("application not initialized")
	}
	a.chartWindowsLock.RLock()
	existing := a.chartWindows[ticker]
	a.chartWindowsLock.RUnlock()
	if existing != nil {
		existing.Restore()
		existing.Focus()
		return nil
	}
	return a.openChartWindow(ticker, dateStr, true)
}

func (a *App) watchProfileWindow(ticker string, window *application.WebviewWindow) {
	window.OnWindowEvent(events.Common.WindowClosing, func(event *application.WindowEvent) {
		a.chartWindowsLock.Lock()
		if current, ok := a.profileWindows[ticker]; ok && current == window {
			delete(a.profileWindows, ticker)
		}
		a.chartWindowsLock.Unlock()
		a.releaseTickerDisplay(ticker)
	})
}

func (a *App) profileWindowSize() (int, int) {
	widthPct := 28.0
	if a.settingsManager != nil {
		if settings := a.settingsManager.GetSettings(); settings != nil {
			if v, ok := layoutNumber(settings.ProfileSettings["side_width_percent"]); ok && v > 0 {
				widthPct = clampLayout(v, 12, 50)
			}
		}
	}
	workW, workH := 1400, 900
	if app, ok := a.appRef.(*application.App); ok && app != nil && app.Screen != nil {
		if screen := app.Screen.GetPrimary(); screen != nil {
			if screen.WorkArea.Width > 0 {
				workW = screen.WorkArea.Width
			}
			if screen.WorkArea.Height > 0 {
				workH = screen.WorkArea.Height
			}
		}
	}
	width := int(float64(workW) * widthPct / 100)
	height := int(float64(workH) * 0.86)
	return clampToWorkArea(a.appRef, width, height, 320, 420)
}

func (a *App) profileWindowOrigin(width, height int) (int, int) {
	a.chartWindowsLock.RLock()
	index := len(a.profileWindows)
	a.chartWindowsLock.RUnlock()
	workX, workY, workW, workH := 80, 40, 1600, 1000
	if app, ok := a.appRef.(*application.App); ok && app != nil && app.Screen != nil {
		if screen := app.Screen.GetPrimary(); screen != nil && screen.WorkArea.Width > 0 {
			workX = screen.WorkArea.X
			workY = screen.WorkArea.Y
			workW = screen.WorkArea.Width
			workH = screen.WorkArea.Height
		}
	}
	x := workX + (workW-width)/2 + (index%6)*28
	y := workY + (workH-height)/2 + (index%6)*28
	if x < workX {
		x = workX
	}
	if y < workY {
		y = workY
	}
	if x+width > workX+workW {
		x = workX + workW - width
	}
	if y+height > workY+workH {
		y = workY + workH - height
	}
	if x < workX {
		x = workX
	}
	if y < workY {
		y = workY
	}
	return x, y
}

func (a *App) resizeOpenProfiles() {
	width, _ := a.profileWindowSize()
	a.chartWindowsLock.RLock()
	windows := make([]*application.WebviewWindow, 0, len(a.profileWindows))
	for _, window := range a.profileWindows {
		windows = append(windows, window)
	}
	a.chartWindowsLock.RUnlock()
	for _, window := range windows {
		if window == nil {
			continue
		}
		window.SetSize(width, window.Height())
	}
}
