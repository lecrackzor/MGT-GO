package main

import (
	"fmt"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// OpenLogWindow opens the session log, or focuses it if it is already open.
func (a *App) OpenLogWindow() error {
	if a.appRef == nil {
		return fmt.Errorf("application not initialized")
	}

	a.logWindowLock.Lock()
	existing := a.logWindow
	a.logWindowLock.Unlock()
	if existing != nil {
		existing.Restore()
		existing.Focus()
		return nil
	}

	width, height := clampToWorkArea(a.appRef, 760, 520, 420, 280)
	window := createWindowFromApp(a.appRef, application.WebviewWindowOptions{
		Title:            "Market Terminal Log",
		Width:            width,
		Height:           height,
		MinWidth:         420,
		MinHeight:        280,
		InitialPosition:  application.WindowCentered,
		URL:              "/log.html",
		BackgroundColour: application.NewRGB(30, 30, 30),
	})
	if window == nil {
		return fmt.Errorf("failed to create log window")
	}

	window.OnWindowEvent(events.Common.WindowClosing, func(event *application.WindowEvent) {
		a.logWindowLock.Lock()
		if a.logWindow == window {
			a.logWindow = nil
		}
		a.logWindowLock.Unlock()
	})

	a.logWindowLock.Lock()
	a.logWindow = window
	a.logWindowLock.Unlock()
	return nil
}

func (a *App) watchMainWindow(window *application.WebviewWindow) {
	window.OnWindowEvent(events.Common.WindowDidResize, func(event *application.WindowEvent) {
		a.scheduleMainWindowSizeSave(window)
	})
	window.OnWindowEvent(events.Common.WindowClosing, func(event *application.WindowEvent) {
		a.cancelMainWindowSizeSave()
		a.persistMainWindowSize(window)
		a.closeLogWindow()
	})
}

func (a *App) scheduleMainWindowSizeSave(window *application.WebviewWindow) {
	a.windowSizeLock.Lock()
	defer a.windowSizeLock.Unlock()
	if a.windowSizeTimer != nil {
		a.windowSizeTimer.Stop()
	}
	a.windowSizeTimer = time.AfterFunc(500*time.Millisecond, func() {
		a.persistMainWindowSize(window)
	})
}

func (a *App) cancelMainWindowSizeSave() {
	a.windowSizeLock.Lock()
	timer := a.windowSizeTimer
	a.windowSizeTimer = nil
	a.windowSizeLock.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

func (a *App) persistMainWindowSize(window *application.WebviewWindow) {
	if window == nil || window.IsMaximised() || window.IsMinimised() {
		return
	}
	width, height := window.Size()
	if err := a.SaveWindowSize(width, height); err != nil {
		a.debugPrint(fmt.Sprintf("Failed to persist window size: %v", err), "error")
	}
}

func (a *App) closeLogWindow() {
	a.logWindowLock.Lock()
	window := a.logWindow
	a.logWindow = nil
	a.logWindowLock.Unlock()
	if window != nil {
		window.Close()
	}
}
