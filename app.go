package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"gopkg.in/yaml.v3"

	"market-terminal/internal/api"
	"market-terminal/internal/charts"
	"market-terminal/internal/config"
	"market-terminal/internal/coordinator"
	"market-terminal/internal/database"
	"market-terminal/internal/scheduler"
	"market-terminal/internal/utils"
)

// App struct represents the main application
type App struct {
	ctx                context.Context
	appRef             interface{} // Reference to Wails application (set via SetApp)
	settingsManager    *config.SettingsManager
	dataWriter         *database.DataWriter
	dataLoader         *database.DataLoader
	apiClient          *api.Client
	querySystem        *api.QuerySystem
	scheduler          *scheduler.UnifiedAdaptiveScheduler
	perTickerScheduler *scheduler.PerTickerScheduler
	queryPlanner       *coordinator.SmartQueryPlanner
	writeQueue         *coordinator.PriorityWriteQueue
	coordinator        *coordinator.DataCollectionCoordinator
	chartTracker       *charts.ChartTracker
	healthCheck        *coordinator.HealthCheck
	enabledTickers     []string
	shuttingDown       bool
	shutdownLock       sync.RWMutex
	debugPrint         func(string, string)
	chartWindows       map[string]*application.WebviewWindow // Track open chart windows
	chartWindowsLock   sync.RWMutex
	mainWindow         *application.WebviewWindow // Main application window
}

// NewApp creates a new App instance
func NewApp() *App {
	// Initialize settings manager (uses default user config directory)
	settingsManager := config.NewSettingsManager("")

	// Load settings
	settings, err := settingsManager.LoadSettings()
	if err != nil {
		log.Printf("Warning: Failed to load settings: %v. Using defaults.", err)
		settings = config.GetDefaultSettings()
	}

	// Debug print function - uses file logger
	debugPrint := func(msg, category string) {
		if settings.EnableDebug || category == "error" || category == "system" || category == "writer" || category == "coordinator" || category == "scheduler" || category == "write_queue" || category == "app" {
			// Use file logger for all debug messages
			utils.Logf("[%s] %s", category, msg)
		}
	}

	// Get enabled tickers from settings
	enabledTickers := getEnabledTickers(settings)

	// MISSION CRITICAL: Log current time when App is created
	nowSystem := time.Now()
	nowMarket := utils.NowMarketTime()
	log.Printf("[TIME] App created at - System: %s, Market (ET): %s",
		nowSystem.Format("2006-01-02 15:04:05 MST"),
		nowMarket.Format("2006-01-02 15:04:05 MST"))
	utils.Logf("[system] App created at - System: %s, Market (ET): %s",
		nowSystem.Format("2006-01-02 15:04:05 MST"),
		nowMarket.Format("2006-01-02 15:04:05 MST"))

	// Initialize database components
	dataWriter := database.NewDataWriter(settings, debugPrint)
	dataLoader := database.NewDataLoader(settings, debugPrint)

	// Initialize API client
	apiClient := api.NewClient(settings.APITKey, debugPrint)

	// Initialize query system
	querySystem := api.NewQuerySystem(settings, settings.APITKey, apiClient, debugPrint)

	// Initialize scheduler
	adaptiveScheduler := scheduler.NewUnifiedAdaptiveScheduler(settings, false)
	adaptiveScheduler.SetEnabledTickers(enabledTickers)

	// Initialize query planner
	queryPlanner := coordinator.NewSmartQueryPlanner(settings, enabledTickers, querySystem)

	// Initialize write queue
	writeQueue := coordinator.NewPriorityWriteQueue(dataWriter, debugPrint)

	// Initialize chart tracker
	chartTracker := charts.NewChartTracker()

	// Create app instance first (will be fully initialized below)
	app := &App{
		settingsManager: settingsManager,
		dataWriter:      dataWriter,
		dataLoader:      dataLoader,
		apiClient:       apiClient,
		querySystem:     querySystem,
		scheduler:       adaptiveScheduler,
		queryPlanner:    queryPlanner,
		writeQueue:      writeQueue,
		chartTracker:    chartTracker,
		enabledTickers:  enabledTickers,
		debugPrint:      debugPrint,
		chartWindows:    make(map[string]*application.WebviewWindow),
	}

	// Initialize data collection coordinator (with reference to app)
	getShuttingDown := func() bool {
		app.shutdownLock.RLock()
		defer app.shutdownLock.RUnlock()
		return app.shuttingDown
	}

	getOpenCharts := func() []interface{} {
		// Get displayed tickers from chart tracker
		displayedTickers := chartTracker.GetDisplayedTickers()
		// Convert to []interface{} for compatibility
		result := make([]interface{}, len(displayedTickers))
		for i, ticker := range displayedTickers {
			result[i] = ticker
		}
		return result
	}

	coordinator := coordinator.NewDataCollectionCoordinator(
		querySystem,
		dataWriter,
		adaptiveScheduler,
		queryPlanner,
		writeQueue,
		getShuttingDown,
		getOpenCharts,
		debugPrint,
	)
	app.coordinator = coordinator

	// After-hours collection is NOT allowed - only poll during market hours
	allowAfterHours := false

	// Initialize per-ticker scheduler (more idiomatic Go)
	perTickerScheduler := scheduler.NewPerTickerScheduler(
		adaptiveScheduler,
		getOpenCharts,
		func(ticker string) {
			// Callback when a single ticker is ready to fetch
			log.Printf("[FETCH-CALLBACK] ===== onTickerReady called for: %s =====", ticker)
			coordinator.ProcessTickerBatch([]string{ticker})
		},
		debugPrint,
		allowAfterHours,
	)
	perTickerScheduler.UpdateTickers(enabledTickers)
	app.perTickerScheduler = perTickerScheduler

	// Set per-ticker scheduler reference in coordinator for date rollover handling
	coordinator.SetPerTickerScheduler(perTickerScheduler)

	return app
}

// getEnabledTickers extracts enabled tickers from settings
// Returns empty array if no tickers are enabled (doesn't crash)
func getEnabledTickers(settings *config.Settings) []string {
	// Parse from ticker_configs if available
	if settings != nil && len(settings.TickerConfigs) > 0 {
		enabled := config.GetEnabledTickers(settings.TickerConfigs)
		if len(enabled) > 0 {
			return enabled
		}
	}

	// Return empty array if no tickers enabled (don't use fallback - let user configure)
	return []string{}
}

// ServiceStartup is called when the app starts (implements ServiceStartup interface)
func (a *App) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	a.ctx = ctx

	// MISSION CRITICAL: Log current time immediately on startup
	nowSystem := time.Now()
	nowMarket := utils.NowMarketTime()
	isMarketOpen := utils.IsMarketOpen()
	marketOpenET, marketCloseET := utils.MarketOpenCloseTimes(nowMarket)

	log.Printf("=== SYSTEM TIME ON STARTUP ===")
	log.Printf("[TIME] System local time: %s (%s)",
		nowSystem.Format("2006-01-02 15:04:05 MST"), nowSystem.Location().String())
	log.Printf("[TIME] Market time (ET): %s (%s)",
		nowMarket.Format("2006-01-02 15:04:05 MST"), nowMarket.Location().String())
	log.Printf("[TIME] Time difference: %s",
		nowSystem.Sub(nowMarket.In(nowSystem.Location())).String())
	log.Printf("[TIME] Market is open: %v", isMarketOpen)
	log.Printf("[TIME] Market hours today: %s - %s ET",
		marketOpenET.Format("15:04:05"), marketCloseET.Format("15:04:05"))

	utils.Logf("[system] === SYSTEM TIME ON STARTUP ===")
	utils.Logf("[system] System local time: %s (%s)",
		nowSystem.Format("2006-01-02 15:04:05 MST"), nowSystem.Location().String())
	utils.Logf("[system] Market time (ET): %s (%s)",
		nowMarket.Format("2006-01-02 15:04:05 MST"), nowMarket.Location().String())
	utils.Logf("[system] Time difference: %s",
		nowSystem.Sub(nowMarket.In(nowSystem.Location())).String())
	utils.Logf("[system] Market is open: %v", isMarketOpen)
	utils.Logf("[system] Market hours today: %s - %s ET",
		marketOpenET.Format("15:04:05"), marketCloseET.Format("15:04:05"))

	utils.Logf("ServiceStartup called - window should be visible now")
	a.debugPrint("Market Terminal Gexbot (Go/Wails) starting up...", "system")

	// Create today's data directory (like Python version does)
	settings := a.settingsManager.GetSettings()
	dataDir := settings.DataDirectory
	if dataDir == "" {
		dataDir = "Tickers"
	}

	// Get today's date (handle weekends like Python version)
	today := time.Now()
	weekday := today.Weekday()
	if weekday == time.Saturday {
		today = today.AddDate(0, 0, -1) // Use Friday
	} else if weekday == time.Sunday {
		today = today.AddDate(0, 0, -2) // Use Friday
	}

	dateStr := today.Format("01.02.2006")
	dataDirPath := fmt.Sprintf("%s %s", dataDir, dateStr)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dataDirPath, 0755); err != nil {
		utils.Logf("WARNING: Failed to create data directory %s: %v", dataDirPath, err)
	} else {
		utils.Logf("Data directory ready: %s", dataDirPath)
		a.debugPrint(fmt.Sprintf("Data directory: %s", dataDirPath), "system")
	}

	// Start per-ticker scheduler to begin data collection (non-blocking)
	go func() {
		// Small delay to ensure window is fully initialized
		time.Sleep(500 * time.Millisecond)
		if a.perTickerScheduler != nil {
			// Check if scheduler is already running
			if a.perTickerScheduler.IsRunning() {
				utils.Logf("Per-ticker scheduler already running")
				return
			}

			a.perTickerScheduler.Start()
			a.debugPrint("Per-ticker scheduler started", "system")
			utils.Logf("Per-ticker scheduler started - data collection should begin")

			// Verify it's actually running
			if a.perTickerScheduler.IsRunning() {
				utils.Logf("✓ Per-ticker scheduler confirmed running with %d active tickers", a.perTickerScheduler.GetActiveTickerCount())
			} else {
				utils.Logf("✗ WARNING: Per-ticker scheduler Start() called but IsRunning() returns false")
			}

			// Start health check system
			if a.healthCheck != nil {
				a.healthCheck.Start()
				utils.Logf("Health check system started")
			}

			// Start date rollover monitor
			if a.coordinator != nil {
				a.coordinator.StartDateRolloverMonitor()
				utils.Logf("Date rollover monitor started")
			}

			// Check API key
			apiKey := settings.APITKey
			if apiKey == "" {
				utils.Logf("WARNING: API key not configured. Set GEXBOT_API_KEY environment variable or configure in settings.")
				a.debugPrint("API key not configured - data collection will fail", "error")
			} else {
				utils.Logf("API key configured (length: %d)", len(apiKey))
				utils.Logf("Enabled tickers: %v (count: %d)", a.enabledTickers, len(a.enabledTickers))
				utils.Logf("Subscription tiers: %v", settings.APISubscriptionTiers)
			}
		} else {
			utils.Logf("WARNING: perTickerScheduler is nil")
		}
	}()

	// Create main window now that backend is fully initialized and Wails runtime is ready
	utils.Logf("Creating main window - backend is ready, bindings will be available")

	// Use saved window dimensions or defaults
	windowWidth := 900
	windowHeight := 900
	currentSettings := a.settingsManager.GetSettings()
	if currentSettings != nil {
		// Use saved dimensions if they're valid (>= minimum size)
		// If saved dimensions are 0 or invalid, use defaults
		if currentSettings.WindowWidth >= 600 {
			windowWidth = currentSettings.WindowWidth
			utils.Logf("Using saved window width: %d", windowWidth)
		} else {
			utils.Logf("Saved window width (%d) is invalid, using default: %d", currentSettings.WindowWidth, windowWidth)
		}
		if currentSettings.WindowHeight >= 400 {
			windowHeight = currentSettings.WindowHeight
			utils.Logf("Using saved window height: %d", windowHeight)
		} else {
			utils.Logf("Saved window height (%d) is invalid, using default: %d", currentSettings.WindowHeight, windowHeight)
		}
		utils.Logf("Window dimensions: %dx%d (saved: %dx%d)", windowWidth, windowHeight, currentSettings.WindowWidth, currentSettings.WindowHeight)
	} else {
		utils.Logf("Window dimensions: Using defaults %dx%d (settings not loaded)", windowWidth, windowHeight)
	}

	mainWindow := createWindowFromApp(a.appRef, application.WebviewWindowOptions{
		Title:            "Market Terminal Gexbot",
		Width:            windowWidth,
		Height:           windowHeight,
		MinWidth:         600,
		MinHeight:        400,
		URL:              "/index.html", // Use embedded filesystem
		BackgroundColour: application.NewRGB(30, 30, 30),
	})

	if mainWindow == nil {
		utils.Logf("ERROR: Main window creation failed")
		a.debugPrint("Failed to create main window", "error")
		// Continue anyway - user might see error in logs
	} else {
		a.mainWindow = mainWindow
		utils.Logf("Main window created successfully - frontend will load with bindings ready")
		a.debugPrint("Main window created - backend ready", "system")
		// Note: Frontend will initialize automatically since window is created after backend is ready
		// The frontend has a timeout fallback that will trigger initialization
	}

	utils.Logf("ServiceStartup completed successfully")
	return nil
}

// ServiceShutdown is called when the app shuts down (implements ServiceShutdown interface)
func (a *App) ServiceShutdown() error {
	a.debugPrint("Shutting down...", "system")

	// Set shutting down flag
	a.shutdownLock.Lock()
	a.shuttingDown = true
	a.shutdownLock.Unlock()

	// Close all chart windows first to prevent WebView2 cleanup errors
	a.chartWindowsLock.Lock()
	chartWindowCount := len(a.chartWindows)
	for ticker, window := range a.chartWindows {
		if window != nil {
			a.debugPrint(fmt.Sprintf("ServiceShutdown: Closing chart window for %s", ticker), "system")
			window.Close()
		}
	}
	a.chartWindows = make(map[string]*application.WebviewWindow)
	a.chartWindowsLock.Unlock()
	if chartWindowCount > 0 {
		a.debugPrint(fmt.Sprintf("ServiceShutdown: Closed %d chart window(s)", chartWindowCount), "system")
	}

	// Stop health check system
	if a.healthCheck != nil {
		a.healthCheck.Stop()
	}

	// Stop date rollover monitor
	if a.coordinator != nil {
		a.coordinator.StopDateRolloverMonitor()
		a.debugPrint("Date rollover monitor stopped", "system")
	}

	// Stop per-ticker scheduler
	if a.perTickerScheduler != nil {
		a.perTickerScheduler.Stop()
	}

	// Close database connections (this will flush pending writes and checkpoint WAL files)
	// This ensures .db-wal and .db-shm files are cleaned up on shutdown
	a.debugPrint("ServiceShutdown: Closing database connections and flushing pending writes", "system")
	if a.dataWriter != nil {
		if err := a.dataWriter.Close(); err != nil {
			a.debugPrint(fmt.Sprintf("ServiceShutdown: Warning - error closing data writer: %v", err), "error")
		} else {
			a.debugPrint("ServiceShutdown: Data writer closed successfully", "system")
		}
	}
	if a.dataLoader != nil {
		if err := a.dataLoader.Close(); err != nil {
			a.debugPrint(fmt.Sprintf("ServiceShutdown: Warning - error closing data loader: %v", err), "error")
		} else {
			a.debugPrint("ServiceShutdown: Data loader closed successfully", "system")
		}
	}
	a.debugPrint("ServiceShutdown: Database cleanup completed", "system")

	// Close API client
	if a.apiClient != nil {
		a.apiClient.Close()
	}

	return nil
}

// Greet returns a greeting message (for testing)
func (a *App) Greet(name string) string {
	return "Hello, " + name + "! Market Terminal Gexbot is running."
}

// GetVersion returns the application version
func (a *App) GetVersion() string {
	return "1.0.0 (Go/Wails)"
}

// ResizeMainWindow resizes the main window to the specified dimensions
func (a *App) ResizeMainWindow(width, height int) {
	if a.mainWindow != nil {
		a.mainWindow.SetSize(width, height)
		utils.Logf("Main window resized to %dx%d", width, height)
	}
}

// SaveWindowSize saves the current window dimensions to settings
// Uses lightweight SaveWindowDimensions to avoid full settings reload
func (a *App) SaveWindowSize(width, height int) error {
	if width < 600 || height < 400 {
		return nil // Don't save invalid sizes
	}

	if err := a.settingsManager.SaveWindowDimensions(width, height); err != nil {
		a.debugPrint(fmt.Sprintf("Failed to save window size: %v", err), "error")
		return err
	}

	return nil
}

// CheckFirstRun checks if this is the first run (no API key configured)
func (a *App) CheckFirstRun() bool {
	configPath, err := config.GetConfigPath()
	if err != nil {
		log.Printf("CheckFirstRun: Could not get config path: %v", err)
		return true // Assume first run if we can't get config path
	}

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("CheckFirstRun: Config file does not exist: %s", configPath)
		return true // Config file doesn't exist, first run
	}

	// Config file exists, check if it has been configured
	// Reload settings to get latest from file
	settings, err := a.settingsManager.LoadSettings()
	if err != nil {
		log.Printf("CheckFirstRun: Failed to load settings: %v", err)
		return true // Assume first run if we can't load
	}

	apiKey := settings.APITKey
	hasTiers := len(settings.APISubscriptionTiers) > 0

	log.Printf("CheckFirstRun: Config exists, API key length: %d, has tiers: %v", len(apiKey), hasTiers)

	// Show wizard if API key is missing, regardless of tiers
	// This ensures users can always set their API key even if tiers were previously configured
	return apiKey == ""
}

// CompleteSetup completes the first-time setup
// apiKey: API key (will be set as environment variable or in config)
// subscriptionTiers: List of subscription tiers (e.g., ["classic", "state"])
// initialTickers: List of initial tickers to enable
func (a *App) CompleteSetup(apiKey string, subscriptionTiers []string, initialTickers []string) error {
	settings := a.settingsManager.GetSettings()

	// Update API key (prefer environment variable, but allow config for now)
	if apiKey != "" {
		settings.APITKey = apiKey
		log.Printf("CompleteSetup: Setting API key in settings (length: %d)", len(apiKey))
		// Note: In production, this should be set as environment variable
		// For now, we'll save it to config for convenience
		log.Println("WARNING: API key saved to config. For production, set GEXBOT_API_KEY environment variable.")
	} else {
		log.Printf("ERROR: CompleteSetup called with empty API key!")
		return fmt.Errorf("API key cannot be empty")
	}

	// Update subscription tiers
	if len(subscriptionTiers) > 0 {
		settings.APISubscriptionTiers = subscriptionTiers
	} else {
		// Default to classic if none selected
		settings.APISubscriptionTiers = []string{"classic"}
	}

	// Initialize ticker configs if needed
	if settings.TickerConfigs == nil {
		settings.TickerConfigs = make(map[string]config.TickerConfig)
	}

	// Enable initial tickers
	defaultTickers := []string{"SPX", "ES_SPX", "SPY", "QQQ", "NDX", "NQ_NDX", "IWM", "VIX"}
	tickersToEnable := initialTickers
	if len(tickersToEnable) == 0 {
		tickersToEnable = defaultTickers
	}

	for _, ticker := range tickersToEnable {
		if _, exists := settings.TickerConfigs[ticker]; !exists {
			refreshRate := 5000
			settings.TickerConfigs[ticker] = config.TickerConfig{
				Display:           true,
				CollectionEnabled: true,
				Priority:          "1",
				RefreshRateMs:     &refreshRate,
			}
		} else {
			// Update existing config
			tickerConfig := settings.TickerConfigs[ticker]
			tickerConfig.Display = true
			tickerConfig.CollectionEnabled = true
			settings.TickerConfigs[ticker] = tickerConfig
		}
	}

	// CRITICAL: Verify API key is in settings before saving
	log.Printf("CompleteSetup: About to save - settings.APITKey length: %d, apiKey param length: %d", len(settings.APITKey), len(apiKey))
	if settings.APITKey == "" {
		log.Printf("ERROR: CompleteSetup: settings.APITKey is empty before save!")
		return fmt.Errorf("API key is empty in settings before save")
	}

	// Save settings - for first-time setup, we need to save the API key
	// Use SaveSettingsWithOptions to save the API key during setup
	log.Printf("CompleteSetup: Calling SaveSettingsWithOptions with saveAPIKey=true, API key length: %d", len(settings.APITKey))
	if err := a.settingsManager.SaveSettingsWithOptions(settings, true); err != nil {
		log.Printf("ERROR: Failed to save settings: %v", err)
		return fmt.Errorf("failed to save settings: %w", err)
	}

	log.Printf("CompleteSetup: Settings saved successfully to: %s", a.settingsManager.GetConfigPath())
	log.Printf("CompleteSetup: API key saved (length: %d), subscription tiers: %v", len(apiKey), subscriptionTiers)

	// Verify the file was written correctly - read it back and check API key
	configPath := a.settingsManager.GetConfigPath()
	if verifyData, err := os.ReadFile(configPath); err == nil {
		var verifySettings config.Settings
		if err := yaml.Unmarshal(verifyData, &verifySettings); err == nil {
			if verifySettings.APITKey == "" {
				log.Printf("ERROR: CompleteSetup: API key verification failed - file contains empty API key!")
				return fmt.Errorf("API key was not saved to file - verification failed")
			} else if verifySettings.APITKey != apiKey {
				log.Printf("ERROR: CompleteSetup: API key mismatch! Expected length %d, got length %d", len(apiKey), len(verifySettings.APITKey))
				return fmt.Errorf("API key mismatch in saved file")
			} else {
				log.Printf("CompleteSetup: Verified API key in saved file (length: %d)", len(verifySettings.APITKey))
			}
		} else {
			log.Printf("ERROR: CompleteSetup: Failed to parse saved file for verification: %v", err)
			return fmt.Errorf("failed to verify saved settings: %w", err)
		}
	} else {
		log.Printf("ERROR: CompleteSetup: Failed to read saved file for verification: %v", err)
		return fmt.Errorf("failed to verify saved file exists: %w", err)
	}

	// Reload settings to ensure consistency
	reloadedSettings, err := a.settingsManager.LoadSettings()
	if err != nil {
		log.Printf("WARNING: Failed to reload settings: %v", err)
		// Continue with current settings, but ensure API key is set
		reloadedSettings = settings
		if apiKey != "" {
			reloadedSettings.APITKey = apiKey
			log.Printf("CompleteSetup: Restored API key in settings after reload failure")
		}
	} else {
		// Ensure API key is set in reloaded settings (it should be from file now)
		if reloadedSettings.APITKey == "" {
			log.Printf("WARNING: API key was lost during reload - restoring it")
			reloadedSettings.APITKey = apiKey
			// Save again to ensure it persists
			if err := a.settingsManager.SaveSettingsWithOptions(reloadedSettings, true); err != nil {
				log.Printf("ERROR: Failed to re-save API key after reload: %v", err)
			} else {
				log.Printf("CompleteSetup: Re-saved API key after reload")
			}
		} else if reloadedSettings.APITKey != apiKey {
			log.Printf("WARNING: API key mismatch after reload - expected length %d, got length %d", len(apiKey), len(reloadedSettings.APITKey))
			reloadedSettings.APITKey = apiKey
			// Save again to ensure correct key persists
			if err := a.settingsManager.SaveSettingsWithOptions(reloadedSettings, true); err != nil {
				log.Printf("ERROR: Failed to re-save correct API key: %v", err)
			}
		}
		log.Printf("CompleteSetup: Settings reloaded - API key length: %d, tiers: %v", len(reloadedSettings.APITKey), reloadedSettings.APISubscriptionTiers)
	}
	a.settingsManager.SetSettings(reloadedSettings)

	// Update enabled tickers
	a.enabledTickers = getEnabledTickers(settings)
	if a.scheduler != nil {
		a.scheduler.SetEnabledTickers(a.enabledTickers)
	}
	if a.perTickerScheduler != nil {
		a.perTickerScheduler.UpdateTickers(a.enabledTickers)
	}

	// Update API client with new key
	if a.apiClient != nil && apiKey != "" {
		a.apiClient.SetAPIKey(apiKey)
	}
	if a.querySystem != nil && apiKey != "" {
		a.querySystem.SetAPIKey(apiKey)
	}

	a.debugPrint("First-time setup completed", "system")
	return nil
}

// GetSettings returns current settings
func (a *App) GetSettings() *config.Settings {
	return a.settingsManager.GetSettings()
}

// SaveSettings saves settings
// Note: API key is preserved from existing settings (not overwritten by frontend)
func (a *App) SaveSettings(settings *config.Settings) error {
	a.debugPrint(fmt.Sprintf("SaveSettings called - saving to: %s", a.settingsManager.GetConfigPath()), "app")

	// Debug: Log incoming ticker configs
	a.debugPrint(fmt.Sprintf("SaveSettings: Received settings with %d ticker configs", len(settings.TickerConfigs)), "app")
	for ticker, config := range settings.TickerConfigs {
		refreshRateStr := "nil"
		if config.RefreshRateMs != nil {
			refreshRateStr = fmt.Sprintf("%d", *config.RefreshRateMs)
		}
		a.debugPrint(fmt.Sprintf("SaveSettings: Ticker %s - CollectionEnabled=%v, Display=%v, Priority=%v, RefreshRateMs=%v",
			ticker, config.CollectionEnabled, config.Display, config.Priority, refreshRateStr), "app")
	}

	// Preserve existing API key (frontend shouldn't send it for security)
	currentSettings := a.settingsManager.GetSettings()
	if settings.APITKey == "" && currentSettings.APITKey != "" {
		settings.APITKey = currentSettings.APITKey
		a.debugPrint(fmt.Sprintf("SaveSettings: Preserved existing API key (length: %d)", len(settings.APITKey)), "app")
	}

	// Save settings (API key will NOT be saved to file - only in memory)
	if err := a.settingsManager.SaveSettings(settings); err != nil {
		a.debugPrint(fmt.Sprintf("ERROR: SaveSettings failed: %v", err), "error")
		return err
	}

	a.debugPrint("Settings saved successfully", "app")

	// Reload settings to ensure consistency
	reloadedSettings, err := a.settingsManager.LoadSettings()
	if err != nil {
		a.debugPrint(fmt.Sprintf("WARNING: Failed to reload settings after save: %v", err), "error")
	} else {
		// Preserve API key in reloaded settings (it won't be in file, but should be in memory)
		if reloadedSettings.APITKey == "" && settings.APITKey != "" {
			reloadedSettings.APITKey = settings.APITKey
			a.debugPrint(fmt.Sprintf("SaveSettings: Restored API key in reloaded settings (length: %d)", len(reloadedSettings.APITKey)), "app")
		}
		a.settingsManager.SetSettings(reloadedSettings)

		// Update scheduler settings so it sees new priorities and refresh rates
		if a.scheduler != nil {
			a.scheduler.SetSettings(reloadedSettings)
			a.debugPrint("Scheduler: Updated settings reference", "app")
		}

		// Debug: Log reloaded ticker configs
		a.debugPrint(fmt.Sprintf("SaveSettings: Reloaded settings has %d ticker configs", len(reloadedSettings.TickerConfigs)), "app")
		for ticker, config := range reloadedSettings.TickerConfigs {
			refreshRateStr := "nil"
			if config.RefreshRateMs != nil {
				refreshRateStr = fmt.Sprintf("%d", *config.RefreshRateMs)
			}
			a.debugPrint(fmt.Sprintf("SaveSettings: Reloaded ticker %s - CollectionEnabled=%v, Display=%v, Priority=%v, RefreshRateMs=%v",
				ticker, config.CollectionEnabled, config.Display, config.Priority, refreshRateStr), "app")
		}

		// Update enabled tickers - always check if they changed (not just length)
		newEnabledTickers := getEnabledTickers(reloadedSettings)
		a.debugPrint(fmt.Sprintf("SaveSettings: getEnabledTickers returned %d tickers: %v", len(newEnabledTickers), newEnabledTickers), "app")

		// Check if tickers actually changed (compare sets, not just length)
		tickersChanged := false
		if len(newEnabledTickers) != len(a.enabledTickers) {
			tickersChanged = true
		} else {
			// Same length - check if contents are different
			oldTickersSet := make(map[string]bool)
			for _, ticker := range a.enabledTickers {
				oldTickersSet[ticker] = true
			}
			for _, ticker := range newEnabledTickers {
				if !oldTickersSet[ticker] {
					tickersChanged = true
					break
				}
			}
		}

		if tickersChanged {
			a.debugPrint(fmt.Sprintf("Enabled tickers changed: %v -> %v", a.enabledTickers, newEnabledTickers), "app")
			a.enabledTickers = newEnabledTickers
			if a.scheduler != nil {
				a.scheduler.SetEnabledTickers(newEnabledTickers)
			}
			if a.perTickerScheduler != nil {
				a.perTickerScheduler.UpdateTickers(newEnabledTickers)
				a.debugPrint(fmt.Sprintf("PerTickerScheduler: Updated to %d enabled tickers", len(newEnabledTickers)), "app")
			}
			// Also update the query planner via the coordinator
			if a.coordinator != nil {
				a.coordinator.UpdateEnabledTickers(newEnabledTickers)
				a.debugPrint(fmt.Sprintf("Coordinator: Updated enabled tickers to %d", len(newEnabledTickers)), "app")
			}
			// Update the query planner directly as well (belt and suspenders)
			if a.queryPlanner != nil {
				a.queryPlanner.SetEnabledTickers(newEnabledTickers)
				a.debugPrint(fmt.Sprintf("QueryPlanner: Updated enabled tickers to %d", len(newEnabledTickers)), "app")
			}
		} else {
			a.debugPrint(fmt.Sprintf("Enabled tickers unchanged: %v", newEnabledTickers), "app")
		}
	}

	return nil
}

// GetEnabledTickers returns the list of enabled tickers
// Returns empty array if no tickers are enabled (doesn't crash)
// Always reads from current settings to ensure it reflects the latest state
func (a *App) GetEnabledTickers() []string {
	// Always read from current settings, not cached value
	// This ensures it reflects the latest state even if a.enabledTickers is stale
	settings := a.settingsManager.GetSettings()

	// Debug logging
	a.debugPrint(fmt.Sprintf("GetEnabledTickers: Settings has %d ticker configs", len(settings.TickerConfigs)), "app")
	for ticker, config := range settings.TickerConfigs {
		a.debugPrint(fmt.Sprintf("GetEnabledTickers: Ticker %s - CollectionEnabled=%v, Display=%v",
			ticker, config.CollectionEnabled, config.Display), "app")
	}

	enabled := getEnabledTickers(settings)
	a.debugPrint(fmt.Sprintf("GetEnabledTickers: Returning %d enabled tickers: %v", len(enabled), enabled), "app")

	return enabled
}

// GetTickerData loads ticker data from the database
// dateStr is in format "2006-01-02" (YYYY-MM-DD)
// Returns map[string][]interface{} where each key is a field name and value is an array of values
// Returns empty data if database doesn't exist yet (data collection hasn't started)
// CRITICAL: Uses LoadTickerData instead of LoadFromFile to skip profiles_blob and prevent memory issues
func (a *App) GetTickerData(ticker string, dateStr string) (map[string]interface{}, error) {
	// Log memory usage before loading data
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)
	a.debugPrint(fmt.Sprintf("GetTickerData: Memory before loading %s: Alloc=%d MB, Sys=%d MB, HeapAlloc=%d MB",
		ticker, mBefore.Alloc/1024/1024, mBefore.Sys/1024/1024, mBefore.HeapAlloc/1024/1024), "memory")

	// Parse date string in ET (not UTC)
	date, err := utils.ParseDateInET(dateStr)
	if err != nil {
		// Try current market date if parsing fails
		date = utils.GetMarketDate()
		// Extract just the date part at midnight ET
		date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())
	}

	// Load data using lightweight LoadTickerData (skips profiles_blob)
	// This prevents massive memory usage from decompressing profiles
	data, err := a.dataLoader.LoadTickerData(ticker, date)
	if err != nil {
		// If there's an actual error (not just missing file), return it
		a.debugPrint(fmt.Sprintf("GetTickerData: Error loading data for %s: %v", ticker, err), "error")
		return nil, err
	}

	// Convert to map[string]interface{} for JSON serialization
	result := make(map[string]interface{})
	for k, v := range data {
		result[k] = v
	}

	// If no data, return empty structure (not nil)
	if len(result) == 0 {
		// Return structure with empty arrays for expected fields
		result["timestamp"] = []interface{}{}
		result["spot"] = []interface{}{}
		result["zero_gamma"] = []interface{}{}
		result["major_pos_vol"] = []interface{}{}
		result["major_neg_vol"] = []interface{}{}
	}

	// Log memory usage after loading data
	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)
	memDelta := int64(mAfter.Alloc) - int64(mBefore.Alloc)
	a.debugPrint(fmt.Sprintf("GetTickerData: Memory after loading %s: Alloc=%d MB, Sys=%d MB, HeapAlloc=%d MB, Delta=+%d MB",
		ticker, mAfter.Alloc/1024/1024, mAfter.Sys/1024/1024, mAfter.HeapAlloc/1024/1024, memDelta/1024/1024), "memory")

	return result, nil
}

// GetTickerDataRange loads ticker data within a time range
// dateStr is in format "2006-01-02" (YYYY-MM-DD)
// Returns map[string][]interface{} where each key is a field name and value is an array of values
func (a *App) GetTickerDataRange(ticker string, dateStr string, startTime, endTime float64) (map[string]interface{}, error) {
	// Parse date string in ET (not UTC)
	date, err := utils.ParseDateInET(dateStr)
	if err != nil {
		// Try current market date if parsing fails
		date = utils.GetMarketDate()
		// Extract just the date part at midnight ET
		date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())
	}

	// Load data (returns map[string][]interface{})
	data, err := a.dataLoader.LoadTimeRange(ticker, date, startTime, endTime)
	if err != nil {
		return nil, err
	}

	// Convert to map[string]interface{} for JSON serialization
	result := make(map[string]interface{})
	for k, v := range data {
		result[k] = v
	}

	return result, nil
}

// filterChartData filters out NaN and 0 values per-field while maintaining timestamp alignment
// This prevents vertical lines in charts and reduces memory usage
// Each field is filtered independently - invalid values are replaced with nil (Chart.js will skip them)
func filterChartData(data map[string][]interface{}) map[string][]interface{} {
	timestamps, hasTimestamps := data["timestamp"]
	if !hasTimestamps || len(timestamps) == 0 {
		return data
	}

	filtered := make(map[string][]interface{})

	// Always include timestamps (no filtering)
	filtered["timestamp"] = timestamps

	// Filter each field independently (replace NaN/zero with nil)
	for key, values := range data {
		if key == "timestamp" {
			continue
		}

		filtered[key] = make([]interface{}, len(timestamps))
		for i := 0; i < len(timestamps) && i < len(values); i++ {
			val := values[i]

			// Check for nil
			if val == nil {
				filtered[key][i] = nil
				continue
			}

			// Check for NaN, Inf; allow 0 so charts can show data when DB has 0 (e.g. placeholder or pre-market)
			if f, ok := val.(float64); ok {
				if math.IsNaN(f) || math.IsInf(f, 0) {
					filtered[key][i] = nil
				} else {
					filtered[key][i] = val
				}
			} else if i64, ok := val.(int64); ok {
				filtered[key][i] = float64(i64)
			} else if i32, ok := val.(int32); ok {
				filtered[key][i] = float64(i32)
			} else {
				filtered[key][i] = val
			}
		}
		// Forward-fill nils so chart always receives numbers (avoids "No valid data points" when DB has NULLs)
		var last interface{}
		for i := range filtered[key] {
			if filtered[key][i] != nil {
				last = filtered[key][i]
			} else if last != nil {
				filtered[key][i] = last
			} else {
				filtered[key][i] = 0.0
			}
		}
	}

	// Note: Filtering stats are logged in GetChartData, not here (no access to debugPrint)
	return filtered
}

// GetChartData serves chart data for chart windows
// Loads data with limits and filters to reduce memory usage
// ticker: Ticker symbol
// dateStr: Date in format "2006-01-02" (YYYY-MM-DD)
func (a *App) GetChartData(ticker string, dateStr string) (map[string]interface{}, error) {
	// Log memory usage before loading data
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)
	a.debugPrint(fmt.Sprintf("GetChartData: Memory before loading %s: Alloc=%d MB, Sys=%d MB, HeapAlloc=%d MB",
		ticker, mBefore.Alloc/1024/1024, mBefore.Sys/1024/1024, mBefore.HeapAlloc/1024/1024), "memory")

	// Parse date string in ET (not UTC)
	date, err := utils.ParseDateInET(dateStr)
	if err != nil {
		a.debugPrint(fmt.Sprintf("GetChartData: Failed to parse date '%s', using current market date: %v", dateStr, err), "error")
		// Try current market date if parsing fails
		date = utils.GetMarketDate()
		// Extract just the date part at midnight ET
		date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, utils.GetMarketTimezone())
		dateStr = date.Format("2006-01-02")
	}
	a.debugPrint(fmt.Sprintf("GetChartData: Parsed date for %s: %s (original: %s, ET: %s)",
		ticker, date.Format("2006-01-02"), dateStr, date.Format("2006-01-02 15:04:05 MST")), "app")

	const maxRows = 30000 // Maximum rows to load (full trading day at 1s = ~23,400)

	a.debugPrint(fmt.Sprintf("GetChartData: Loading chart data for %s on %s (max %d rows, skipping profiles)", ticker, dateStr, maxRows), "app")

	// Load chart data (only required columns, no profiles_blob)
	// This prevents massive memory usage from decompressing profiles
	data, err := a.dataLoader.LoadChartData(ticker, date, maxRows)
	if err != nil {
		a.debugPrint(fmt.Sprintf("GetChartData: Error loading data for %s: %v", ticker, err), "error")
		return nil, err
	}

	// Log data before filtering
	beforeFilterCount := 0
	if timestamps, ok := data["timestamp"]; ok {
		beforeFilterCount = len(timestamps)
	}
	a.debugPrint(fmt.Sprintf("GetChartData: Data loaded for %s: %d timestamps before filtering", ticker, beforeFilterCount), "app")

	// Filter out NaN and 0 values to prevent vertical lines and reduce memory
	filteredData := filterChartData(data)

	// Log data after filtering
	afterFilterCount := 0
	if timestamps, ok := filteredData["timestamp"]; ok {
		afterFilterCount = len(timestamps)
	}
	a.debugPrint(fmt.Sprintf("GetChartData: Data filtered for %s: %d timestamps after filtering (removed %d)", ticker, afterFilterCount, beforeFilterCount-afterFilterCount), "app")

	// Only send required fields to frontend (reduces JSON size and memory)
	requiredFields := []string{
		"timestamp",
		"spot",
		"zero_gamma",
		"major_pos_vol",     // Positive gamma
		"major_neg_vol",     // Negative gamma
		"major_long_gamma",  // Long gamma
		"major_short_gamma", // Short gamma
		"major_positive",    // Major positive strike
		"major_negative",    // Major negative strike
		"major_pos_oi",      // Major positive OI
		"major_neg_oi",      // Major negative OI
	}
	result := make(map[string]interface{})
	for _, field := range requiredFields {
		if values, ok := filteredData[field]; ok {
			result[field] = values
		} else {
			result[field] = []interface{}{}
		}
	}

	// Log filtering results
	originalCount := 0
	if timestamps, ok := data["timestamp"]; ok {
		originalCount = len(timestamps)
	}
	filteredCount := 0
	if timestamps, ok := result["timestamp"].([]interface{}); ok {
		filteredCount = len(timestamps)
	}

	if originalCount > 0 {
		a.debugPrint(fmt.Sprintf("GetChartData: Filtered %s: %d -> %d points (removed %d invalid)",
			ticker, originalCount, filteredCount, originalCount-filteredCount), "app")
	} else {
		a.debugPrint(fmt.Sprintf("GetChartData: No data found for %s on %s", ticker, dateStr), "app")
	}

	// Return empty structure if no valid data
	if len(result) == 0 || filteredCount == 0 {
		result["timestamp"] = []interface{}{}
		result["spot"] = []interface{}{}
		result["zero_gamma"] = []interface{}{}
		result["major_pos_vol"] = []interface{}{}
		result["major_neg_vol"] = []interface{}{}
	}

	// Log memory usage after loading data
	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)
	memDelta := int64(mAfter.Alloc) - int64(mBefore.Alloc)
	a.debugPrint(fmt.Sprintf("GetChartData: Memory after loading %s: Alloc=%d MB, Sys=%d MB, HeapAlloc=%d MB, Delta=+%d MB",
		ticker, mAfter.Alloc/1024/1024, mAfter.Sys/1024/1024, mAfter.HeapAlloc/1024/1024, memDelta/1024/1024), "memory")

	return result, nil
}

// GetCurrentMarketDate returns the current market date in Eastern Time as "YYYY-MM-DD"
// Date rolls over at 8:30 AM ET (1 hour before market open)
func (a *App) GetCurrentMarketDate() string {
	marketDate := utils.GetMarketDate()
	return marketDate.Format("2006-01-02")
}

// GetAvailableDates returns a list of available dates (newest first) from data directories
// Scans for directories matching "Tickers MM.DD.YYYY" pattern
// Returns dates in "YYYY-MM-DD" format, sorted newest first
func (a *App) GetAvailableDates() []string {
	settings := a.settingsManager.GetSettings()
	dataDir := settings.DataDirectory
	if dataDir == "" {
		dataDir = "Tickers"
	}

	prefix := dataDir + " "
	currentDir, err := os.Getwd()
	if err != nil {
		a.debugPrint(fmt.Sprintf("GetAvailableDates: Failed to get current directory: %v", err), "error")
		return []string{}
	}

	entries, err := os.ReadDir(currentDir)
	if err != nil {
		a.debugPrint(fmt.Sprintf("GetAvailableDates: Failed to read directory: %v", err), "error")
		return []string{}
	}

	var availableDates []time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		// Extract date string (format: "MM.DD.YYYY")
		dateStr := strings.TrimPrefix(name, prefix)

		// Parse date
		date, err := time.Parse("01.02.2006", dateStr)
		if err != nil {
			// Skip invalid date formats
			continue
		}

		// Check if directory has any database files
		dirPath := filepath.Join(currentDir, name)
		files, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}

		hasData := false
		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(file.Name(), ".db") {
				hasData = true
				break
			}
		}

		if hasData {
			availableDates = append(availableDates, date)
		}
	}

	// Sort newest first
	sort.Slice(availableDates, func(i, j int) bool {
		return availableDates[i].After(availableDates[j])
	})

	// Convert to "YYYY-MM-DD" format strings
	result := make([]string, len(availableDates))
	for i, date := range availableDates {
		result[i] = date.Format("2006-01-02")
	}

	return result
}

// GetMarketHoursLocal returns market open and close times in user's local timezone
// Returns (openTime, closeTime) as "HH:MM" format strings
// Note: Since we can't determine the user's browser timezone from Go, we return ET times
// The frontend JavaScript will convert these to the user's local timezone
func (a *App) GetMarketHoursLocal() (string, string) {
	// Get current market date
	marketDate := utils.GetMarketDate()

	// Get market open/close times in Eastern Time
	marketOpenET, marketCloseET := utils.MarketOpenCloseTimes(marketDate)

	// Return ET times formatted as "HH:MM"
	// Frontend will convert to local timezone using JavaScript Date conversion
	openStr := marketOpenET.Format("15:04")
	closeStr := marketCloseET.Format("15:04")

	return openStr, closeStr
}

// IsMarketOpen checks if the market is currently open
func (a *App) IsMarketOpen() bool {
	// MISSION CRITICAL: Log immediately when function is called
	log.Printf("=== IsMarketOpen CALLED ===")
	utils.Logf("[system] === IsMarketOpen CALLED ===")

	nowMarket := utils.NowMarketTime()
	nowLocal := time.Now()
	isOpen := utils.IsMarketOpen()

	// MISSION CRITICAL: Show current times - use multiple logging methods
	log.Printf("[TIME] IsMarketOpen: Current market time (ET)=%s (%s)",
		nowMarket.Format("2006-01-02 15:04:05 MST"), nowMarket.Location().String())
	log.Printf("[TIME] IsMarketOpen: Current local time=%s (%s)",
		nowLocal.Format("2006-01-02 15:04:05 MST"), nowLocal.Location().String())
	log.Printf("[TIME] IsMarketOpen: Market is open=%v", isOpen)

	utils.Logf("[system] IsMarketOpen: Current market time (ET)=%s (%s)",
		nowMarket.Format("2006-01-02 15:04:05 MST"), nowMarket.Location().String())
	utils.Logf("[system] IsMarketOpen: Current local time=%s (%s)",
		nowLocal.Format("2006-01-02 15:04:05 MST"), nowLocal.Location().String())
	utils.Logf("[system] IsMarketOpen: Market is open=%v", isOpen)

	return isOpen
}

// GetNextMarketOpenTime returns the next market open time in ISO format (Eastern Time)
func (a *App) GetNextMarketOpenTime() string {
	now := utils.NowMarketTime()
	today := now

	// Check if it's a weekend
	weekday := today.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		// Weekend: next market open is Monday at 9:30 AM ET
		daysUntilMonday := int(time.Monday - weekday)
		if daysUntilMonday <= 0 {
			daysUntilMonday += 7
		}
		nextMonday := today.AddDate(0, 0, daysUntilMonday)
		nextOpen := time.Date(nextMonday.Year(), nextMonday.Month(), nextMonday.Day(), 9, 30, 0, 0, utils.GetMarketTimezone())
		return nextOpen.Format(time.RFC3339)
	}

	// Weekday: check if before or after market hours today
	marketOpen, marketClose := utils.MarketOpenCloseTimes(today)

	if now.Before(marketOpen) {
		// Before market open today: return today's open time
		return marketOpen.Format(time.RFC3339)
	} else if now.After(marketClose) || now.Equal(marketClose) {
		// After market close: return next weekday's open time
		daysToAdd := 1
		if weekday == time.Friday {
			daysToAdd = 3 // Friday -> Monday
		}
		nextDay := today.AddDate(0, 0, daysToAdd)
		nextOpen := time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 9, 30, 0, 0, utils.GetMarketTimezone())
		return nextOpen.Format(time.RFC3339)
	}

	// Market is currently open: return current time (shouldn't happen, but handle it)
	return marketOpen.Format(time.RFC3339)
}

// GetNextMarketOpenLocalTime returns the next market open time in user's local timezone
// Matches Python's get_next_market_open_time() logic exactly
// Returns ISO format string in local timezone for JavaScript Date parsing
func (a *App) GetNextMarketOpenLocalTime() string {
	// MISSION CRITICAL: Log immediately when function is called
	log.Printf("=== GetNextMarketOpenLocalTime CALLED ===")
	utils.Logf("[system] === GetNextMarketOpenLocalTime CALLED ===")

	nowMarket := utils.NowMarketTime() // Current time in ET
	nowLocal := time.Now()             // Current time in server's local timezone
	today := nowMarket

	// Get market open/close times for today
	marketOpenET, marketCloseET := utils.MarketOpenCloseTimes(today)

	// MISSION CRITICAL: Show current times - use multiple logging methods
	log.Printf("[TIME] GetNextMarketOpenLocalTime: Current market time (ET)=%s (%s)",
		nowMarket.Format("2006-01-02 15:04:05 MST"), nowMarket.Location().String())
	log.Printf("[TIME] GetNextMarketOpenLocalTime: Current local time=%s (%s)",
		nowLocal.Format("2006-01-02 15:04:05 MST"), nowLocal.Location().String())
	log.Printf("[TIME] GetNextMarketOpenLocalTime: Time difference between ET and local=%s",
		nowLocal.Sub(nowMarket.In(nowLocal.Location())).String())

	utils.Logf("[system] GetNextMarketOpenLocalTime: Current market time (ET)=%s (%s)",
		nowMarket.Format("2006-01-02 15:04:05 MST"), nowMarket.Location().String())
	utils.Logf("[system] GetNextMarketOpenLocalTime: Current local time=%s (%s)",
		nowLocal.Format("2006-01-02 15:04:05 MST"), nowLocal.Location().String())
	utils.Logf("[system] GetNextMarketOpenLocalTime: Time difference between ET and local=%s",
		nowLocal.Sub(nowMarket.In(nowLocal.Location())).String())

	var targetOpenET time.Time

	// Check if today is a weekend (Saturday=6, Sunday=0 in Go)
	todayWeekday := today.Weekday()
	utils.Logf("[system] GetNextMarketOpenLocalTime: Weekday=%v (0=Sunday, 6=Saturday)", todayWeekday)

	if todayWeekday == time.Saturday || todayWeekday == time.Sunday {
		// It's a weekend, find next Monday
		daysUntilMonday := int(time.Monday - todayWeekday)
		if daysUntilMonday <= 0 {
			daysUntilMonday += 7 // If Sunday, Monday is 1 day away
		}
		nextTradingDay := today.AddDate(0, 0, daysUntilMonday)
		targetOpenET, _ = utils.MarketOpenCloseTimes(nextTradingDay)
		utils.Logf("[system] GetNextMarketOpenLocalTime: Weekend detected, next Monday ET=%s",
			targetOpenET.Format("2006-01-02 15:04:05 MST"))
	} else {
		// It's a weekday, check if market has opened/closed today
		utils.Logf("[system] GetNextMarketOpenLocalTime: Weekday - market open ET=%s, close ET=%s",
			marketOpenET.Format("15:04:05"), marketCloseET.Format("15:04:05"))

		if nowMarket.Before(marketOpenET) {
			// Market hasn't opened yet today
			targetOpenET = marketOpenET
			utils.Logf("[system] GetNextMarketOpenLocalTime: Before market open, using today's open ET=%s",
				targetOpenET.Format("2006-01-02 15:04:05 MST"))
		} else if nowMarket.After(marketCloseET) || nowMarket.Equal(marketCloseET) {
			// Market has closed, find next trading day
			nextDay := today.AddDate(0, 0, 1)
			// Skip weekends (find next weekday)
			for nextDay.Weekday() == time.Saturday || nextDay.Weekday() == time.Sunday {
				nextDay = nextDay.AddDate(0, 0, 1)
			}
			targetOpenET, _ = utils.MarketOpenCloseTimes(nextDay)
			utils.Logf("[system] GetNextMarketOpenLocalTime: After market close, next trading day ET=%s",
				targetOpenET.Format("2006-01-02 15:04:05 MST"))
		} else {
			// Market is open (shouldn't reach here, but handle it)
			// Return today's open time as fallback
			targetOpenET = marketOpenET
			utils.Logf("[system] GetNextMarketOpenLocalTime: Market is open, using today's open as fallback ET=%s",
				targetOpenET.Format("2006-01-02 15:04:05 MST"))
		}
	}

	// Convert target open time to server's local timezone for logging
	targetOpenLocal := targetOpenET.In(time.Local)

	// CRITICAL: Format RFC3339 with explicit timezone offset
	// RFC3339 format: "2006-01-02T15:04:05-05:00" (includes timezone offset)
	// This ensures JavaScript Date.parse() correctly converts ET to browser's local timezone
	result := targetOpenET.Format(time.RFC3339)

	// Verify the RFC3339 string is correct by parsing it back
	parsedBack, err := time.Parse(time.RFC3339, result)
	if err != nil {
		log.Printf("[TIME] ERROR: Failed to parse RFC3339 back: %v", err)
	} else {
		parsedBackLocal := parsedBack.In(time.Local)
		log.Printf("[TIME] GetNextMarketOpenLocalTime: RFC3339 verification - parsed back to local=%s",
			parsedBackLocal.Format("2006-01-02 15:04:05 MST"))
	}

	// MISSION CRITICAL: Show conversion - use multiple logging methods
	log.Printf("[TIME] GetNextMarketOpenLocalTime: Target open ET=%s",
		targetOpenET.Format("2006-01-02 15:04:05 MST"))
	log.Printf("[TIME] GetNextMarketOpenLocalTime: Target open local (server)=%s (%s)",
		targetOpenLocal.Format("2006-01-02 15:04:05 MST"), targetOpenLocal.Location().String())
	log.Printf("[TIME] GetNextMarketOpenLocalTime: Returning RFC3339 (ET)=%s", result)
	log.Printf("[TIME] GetNextMarketOpenLocalTime: Expected browser local time (CST): %s",
		targetOpenLocal.Format("2006-01-02 15:04:05 MST"))

	utils.Logf("[system] GetNextMarketOpenLocalTime: Target open ET=%s",
		targetOpenET.Format("2006-01-02 15:04:05 MST"))
	utils.Logf("[system] GetNextMarketOpenLocalTime: Target open local (server)=%s (%s)",
		targetOpenLocal.Format("2006-01-02 15:04:05 MST"), targetOpenLocal.Location().String())
	utils.Logf("[system] GetNextMarketOpenLocalTime: Returning RFC3339=%s", result)
	utils.Logf("[system] GetNextMarketOpenLocalTime: Expected browser local time (CST): %s",
		targetOpenLocal.Format("2006-01-02 15:04:05 MST"))

	return result
}

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

// LogFrontend logs a message from the frontend to the backend console and log file
// This allows frontend errors to appear in the terminal window
func (a *App) LogFrontend(level string, message string) {
	// Log to both stdout (terminal) and file logger
	logMsg := fmt.Sprintf("[FRONTEND-%s] %s", level, message)
	log.Println(logMsg)                            // Terminal/stdout
	utils.Logf("[frontend-%s] %s", level, message) // File logger
	a.debugPrint(message, "frontend")
}

// TestFrontendConnection is a simple test method that the frontend can call immediately
// This verifies the frontend JavaScript is executing and can reach the backend
func (a *App) TestFrontendConnection() string {
	log.Println("[FRONTEND-TEST] TestFrontendConnection called - frontend JavaScript is executing!")
	utils.Logf("[frontend-test] TestFrontendConnection called - frontend JavaScript is executing!")
	return "SUCCESS: Frontend can call backend methods"
}

// RegisterTickerDisplay registers a ticker as being displayed in the frontend
func (a *App) RegisterTickerDisplay(ticker string) {
	if a.chartTracker != nil {
		a.chartTracker.RegisterTicker(ticker)
		a.debugPrint(fmt.Sprintf("Registered ticker display: %s", ticker), "system")
	}
}

// UnregisterTickerDisplay unregisters a ticker from being displayed
func (a *App) UnregisterTickerDisplay(ticker string) {
	if a.chartTracker != nil {
		a.chartTracker.UnregisterTicker(ticker)
		a.debugPrint(fmt.Sprintf("Unregistered ticker display: %s", ticker), "system")
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

	// Check if window already exists and close it before creating new one (prevents memory leaks)
	a.chartWindowsLock.Lock()
	if existingWindow, exists := a.chartWindows[ticker]; exists && existingWindow != nil {
		// Close existing window to free memory
		a.debugPrint(fmt.Sprintf("OpenChartWindow: Closing existing window for %s before creating new one", ticker), "app")
		existingWindow.Close()
		delete(a.chartWindows, ticker)
	}
	a.chartWindowsLock.Unlock()

	// Build URL with ticker and optional date parameter
	url := fmt.Sprintf("/chart.html?ticker=%s", ticker)
	if dateStr != "" {
		url += fmt.Sprintf("&date=%s", dateStr)
	}

	// Create new window using chart.html file with ticker and date parameters
	// The chart.html file will be served by the asset server
	window := createWindowFromApp(a.appRef, application.WebviewWindowOptions{
		Title:            fmt.Sprintf("%s Chart", ticker),
		Width:            1200,
		Height:           800,
		MinWidth:         600,
		MinHeight:        400,
		URL:              url,
		BackgroundColour: application.NewRGB(30, 30, 30),
	})

	if window == nil {
		return fmt.Errorf("failed to create chart window")
	}

	// Store window reference
	a.chartWindowsLock.Lock()
	a.chartWindows[ticker] = window
	a.chartWindowsLock.Unlock()

	// Register ticker as displayed
	a.RegisterTickerDisplay(ticker)

	// Note: Window close handling will be done when window is actually closed
	// We track windows in chartWindows map and clean up on next open if needed

	return nil
}

// VerifyDataCollection verifies that data collection is working
// Returns a map with verification results
func (a *App) VerifyDataCollection() map[string]interface{} {
	result := make(map[string]interface{})

	// Check if scheduler is running
	if a.perTickerScheduler != nil {
		result["scheduler_running"] = a.perTickerScheduler.IsRunning()
		result["active_tickers"] = a.perTickerScheduler.GetActiveTickerCount()
	} else {
		result["scheduler_running"] = false
		result["active_tickers"] = 0
	}

	// Check enabled tickers
	result["enabled_tickers"] = a.enabledTickers
	result["enabled_ticker_count"] = len(a.enabledTickers)

	// Check API key
	settings := a.settingsManager.GetSettings()
	result["api_key_configured"] = settings.APITKey != ""
	result["api_key_length"] = len(settings.APITKey)
	result["subscription_tiers"] = settings.APISubscriptionTiers

	// Check if coordinator is processing
	if a.coordinator != nil {
		// We can't easily check if coordinator is processing without exposing internal state
		result["coordinator_initialized"] = true
	} else {
		result["coordinator_initialized"] = false
	}

	// Check data directory
	dataDir := settings.DataDirectory
	if dataDir == "" {
		dataDir = "Tickers"
	}
	today := time.Now()
	weekday := today.Weekday()
	if weekday == time.Saturday {
		today = today.AddDate(0, 0, -1)
	} else if weekday == time.Sunday {
		today = today.AddDate(0, 0, -2)
	}
	dateStr := today.Format("01.02.2006")
	dataDirPath := fmt.Sprintf("%s %s", dataDir, dateStr)

	// Check if data directory exists
	if _, err := os.Stat(dataDirPath); err == nil {
		result["data_directory_exists"] = true
		result["data_directory"] = dataDirPath

		// Count database files
		files, err := os.ReadDir(dataDirPath)
		if err == nil {
			dbCount := 0
			for _, file := range files {
				if !file.IsDir() && len(file.Name()) > 3 && file.Name()[len(file.Name())-3:] == ".db" {
					dbCount++
				}
			}
			result["database_files"] = dbCount
		} else {
			result["database_files"] = 0
		}
	} else {
		result["data_directory_exists"] = false
		result["data_directory"] = dataDirPath
		result["database_files"] = 0
	}

	return result
}
