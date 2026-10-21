package main

import (
	"context"
	"fmt"
	"log"
	"os"
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
	coordinator        *coordinator.DataCollectionCoordinator
	chartTracker       *charts.ChartTracker
	enabledTickers     []string
	shuttingDown       bool
	shutdownLock       sync.RWMutex
	debugPrint         func(string, string)
	chartWindows       map[string]*application.WebviewWindow // Track open chart windows
	profileWindows     map[string]*application.WebviewWindow // Profile-only windows, separate from charts
	chartWindowsLock   sync.RWMutex
	logWindow          *application.WebviewWindow
	logWindowLock      sync.Mutex
	mainWindow         *application.WebviewWindow // Main application window
	windowSizeTimer    *time.Timer
	windowSizeLock     sync.Mutex

	// Tracks last logged market-open state so the 1s frontend poll
	// only produces a log line on state changes
	lastLoggedMarketOpen *bool
	lastLoggedNextOpen   string
	marketOpenLogLock    sync.Mutex
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

	// Debug print function - uses file logger.
	// Only errors and system events are always logged; everything else
	// (hot-path scheduler/coordinator/writer chatter) requires EnableDebug.
	debugOn := func(category string) bool {
		return settings.EnableDebug || category == "error" || category == "system"
	}
	debugPrint := func(msg, category string) {
		if debugOn(category) {
			// Use file logger for all debug messages
			utils.Logf("[%s] %s", category, msg)
		}
	}

	// Get enabled tickers from settings
	enabledTickers := getEnabledTickers(settings)

	// MISSION CRITICAL: Log current time when App is created
	nowSystem := time.Now()
	nowMarket := utils.NowMarketTime()
	utils.Logf("[system] App created at - System: %s, Market (ET): %s",
		nowSystem.Format("2006-01-02 15:04:05 MST"),
		nowMarket.Format("2006-01-02 15:04:05 MST"))

	// Initialize database components
	dataWriter := database.NewDataWriter(settings, debugPrint)
	dataWriter.SetDebugGate(debugOn)
	dataLoader := database.NewDataLoader(settings, debugPrint)
	dataLoader.SetDebugGate(debugOn)

	// Initialize API client and query planner
	apiClient := api.NewClient(settings.APITKey, debugPrint)
	apiClient.SetDebugGate(debugOn)
	querySystem := api.NewQuerySystem(settings, apiClient)

	// Initialize scheduler (interval policy + rate limiter; loops are per-ticker)
	adaptiveScheduler := scheduler.NewUnifiedAdaptiveScheduler(settings)
	adaptiveScheduler.SetEnabledTickers(enabledTickers)

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
		chartTracker:    chartTracker,
		enabledTickers:  enabledTickers,
		debugPrint:      debugPrint,
		chartWindows:    make(map[string]*application.WebviewWindow),
		profileWindows:  make(map[string]*application.WebviewWindow),
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
		enabledTickers,
		getShuttingDown,
		getOpenCharts,
		debugPrint,
	)
	coordinator.SetDebugGate(debugOn)
	app.coordinator = coordinator

	// After-hours collection is NOT allowed - only poll during market hours
	allowAfterHours := false

	// Initialize per-ticker scheduler (more idiomatic Go)
	perTickerScheduler := scheduler.NewPerTickerScheduler(
		adaptiveScheduler,
		getOpenCharts,
		func(ticker string) {
			if debugOn("scheduler") {
				debugPrint(fmt.Sprintf("onTickerReady called for: %s", ticker), "scheduler")
			}
			coordinator.ProcessTickerBatch([]string{ticker})
		},
		debugPrint,
		allowAfterHours,
	)
	perTickerScheduler.SetDebugGate(debugOn)
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

	dataDirPath := database.MarketDataDir(dataDir, utils.GetMarketDate())

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
				utils.Logf("API key configured")
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

	windowWidth, windowHeight = clampToWorkArea(a.appRef, windowWidth, windowHeight, 600, 400)

	mainWindow := createWindowFromApp(a.appRef, application.WebviewWindowOptions{
		Title:            "Market Terminal Gexbot",
		Width:            windowWidth,
		Height:           windowHeight,
		MinWidth:         600,
		MinHeight:        400,
		InitialPosition:  application.WindowCentered,
		URL:              "/index.html", // Use embedded filesystem
		BackgroundColour: application.NewRGB(30, 30, 30),
	})

	if mainWindow == nil {
		utils.Logf("ERROR: Main window creation failed")
		a.debugPrint("Failed to create main window", "error")
		// Continue anyway - user might see error in logs
	} else {
		a.mainWindow = mainWindow
		a.watchMainWindow(mainWindow)
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

	// Close chart windows without holding the map lock across Close(),
	// because the close callback also updates the map.
	a.chartWindowsLock.Lock()
	chartWindowCount := len(a.chartWindows)
	toClose := make(map[string]*application.WebviewWindow, chartWindowCount)
	for ticker, window := range a.chartWindows {
		toClose[ticker] = window
	}
	a.chartWindows = make(map[string]*application.WebviewWindow)
	a.chartWindowsLock.Unlock()
	for ticker, window := range toClose {
		a.unregisterChartTicker(ticker)
		if window != nil {
			a.debugPrint(fmt.Sprintf("ServiceShutdown: Closing chart window for %s", ticker), "system")
			window.Close()
		}
	}
	if chartWindowCount > 0 {
		a.debugPrint(fmt.Sprintf("ServiceShutdown: Closed %d chart window(s)", chartWindowCount), "system")
	}
	a.closeLogWindow()

	// Stop date rollover monitor
	if a.coordinator != nil {
		a.coordinator.StopDateRolloverMonitor()
		a.debugPrint("Date rollover monitor stopped", "system")
	}

	// Stop per-ticker scheduler
	if a.perTickerScheduler != nil {
		a.perTickerScheduler.Stop()
		a.perTickerScheduler.Wait()
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

// GetVersion returns the application version
func (a *App) GetVersion() string {
	return "1.0.0 (Go/Wails)"
}

// FocusMainWindow brings the main window to the foreground.
// Used when a second app instance is launched (single-instance guard).
func (a *App) FocusMainWindow() {
	if a.mainWindow != nil {
		a.mainWindow.Restore()
		a.mainWindow.Focus()
	}
}

// SaveWindowSize saves the current window dimensions to settings
// Uses lightweight SaveWindowDimensions to avoid full settings reload
func (a *App) SaveWindowSize(width, height int) error {
	if width < 600 || height < 400 {
		return nil // Don't save invalid sizes
	}
	current := a.settingsManager.GetSettings()
	if current != nil && current.WindowWidth == width && current.WindowHeight == height {
		return nil
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
	if apiKey == "" {
		log.Printf("CheckFirstRun: config exists but the API key is empty")
	}

	// Show wizard if API key is missing, regardless of tiers
	// This ensures users can always set their API key even if tiers were previously configured
	return apiKey == ""
}

// CompleteSetup completes the first-time setup
// apiKey: API key (will be set as environment variable or in config)
// subscriptionTiers: List of subscription tiers (e.g., ["classic", "state"])
// initialTickers: List of initial tickers to enable
func (a *App) CompleteSetup(apiKey string, subscriptionTiers []string, initialTickers []string) error {
	settings, err := config.CloneSettings(a.settingsManager.GetSettings())
	if err != nil {
		return err
	}

	// Update API key (prefer environment variable, but allow config for now)
	if apiKey != "" {
		settings.APITKey = apiKey
		a.debugPrint(fmt.Sprintf("CompleteSetup: Setting API key in settings (length: %d)", len(apiKey)), "app")
		a.debugPrint("API key saved to config. For production, set GEXBOT_API_KEY environment variable.", "app")
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

	a.debugPrint(fmt.Sprintf("CompleteSetup: About to save - API key length: %d", len(settings.APITKey)), "app")
	if settings.APITKey == "" {
		log.Printf("ERROR: CompleteSetup: settings.APITKey is empty before save!")
		return fmt.Errorf("API key is empty in settings before save")
	}

	// Save settings - for first-time setup, we need to save the API key
	// Use SaveSettingsWithOptions to save the API key during setup
	a.debugPrint(fmt.Sprintf("CompleteSetup: Saving settings with API key (length: %d)", len(settings.APITKey)), "app")
	if err := a.settingsManager.SaveSettingsWithOptions(settings, true); err != nil {
		log.Printf("ERROR: Failed to save settings: %v", err)
		return fmt.Errorf("failed to save settings: %w", err)
	}

	a.debugPrint(fmt.Sprintf("CompleteSetup: Settings saved to %s (tiers: %v)", a.settingsManager.GetConfigPath(), subscriptionTiers), "app")

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
				a.debugPrint(fmt.Sprintf("CompleteSetup: Verified API key in saved file (length: %d)", len(verifySettings.APITKey)), "app")
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
			a.debugPrint("CompleteSetup: Restored API key in settings after reload failure", "app")
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
				a.debugPrint("CompleteSetup: Re-saved API key after reload", "app")
			}
		} else if reloadedSettings.APITKey != apiKey {
			log.Printf("WARNING: API key mismatch after reload - expected length %d, got length %d", len(apiKey), len(reloadedSettings.APITKey))
			reloadedSettings.APITKey = apiKey
			// Save again to ensure correct key persists
			if err := a.settingsManager.SaveSettingsWithOptions(reloadedSettings, true); err != nil {
				log.Printf("ERROR: Failed to re-save correct API key: %v", err)
			}
		}
		a.debugPrint(fmt.Sprintf("CompleteSetup: Settings reloaded - API key length: %d, tiers: %v", len(reloadedSettings.APITKey), reloadedSettings.APISubscriptionTiers), "app")
	}
	a.settingsManager.SetSettings(reloadedSettings)
	a.applyRuntimeSettings(reloadedSettings)

	// Update enabled tickers
	a.enabledTickers = getEnabledTickers(reloadedSettings)
	if a.scheduler != nil {
		a.scheduler.SetEnabledTickers(a.enabledTickers)
	}
	if a.perTickerScheduler != nil {
		a.perTickerScheduler.UpdateTickers(a.enabledTickers)
	}
	if a.coordinator != nil {
		a.coordinator.UpdateEnabledTickers(a.enabledTickers)
	}

	a.debugPrint("First-time setup completed", "system")
	return nil
}

// UpdateProfileLayers saves which ladders chart-data mode should collect.
func (a *App) UpdateProfileLayers(layers map[string]bool) error {
	if a.settingsManager == nil {
		return fmt.Errorf("settings are not ready")
	}
	current, err := config.CloneSettings(a.settingsManager.GetSettings())
	if err != nil {
		return err
	}
	if current.ProfileSettings == nil {
		current.ProfileSettings = map[string]interface{}{}
	}
	raw := make(map[string]interface{}, len(config.ProfileLayerIDs))
	for _, id := range config.ProfileLayerIDs {
		raw[id] = layers[id]
	}
	current.ProfileSettings["layers"] = raw
	return a.SaveSettings(current)
}

// UpdateProfileLayout saves the side and on-chart placement fields.
func (a *App) UpdateProfileLayout(body map[string]interface{}) error {
	if a.settingsManager == nil {
		return fmt.Errorf("settings are not ready")
	}
	current, err := config.CloneSettings(a.settingsManager.GetSettings())
	if err != nil {
		return err
	}
	if current.ProfileSettings == nil {
		current.ProfileSettings = map[string]interface{}{}
	}
	mergeProfileLayout(current.ProfileSettings, body)
	if err := a.SaveSettings(current); err != nil {
		return err
	}
	if _, ok := body["side_width_percent"]; ok {
		a.resizeOpenProfiles()
	}
	return nil
}

// UpdateProfilePriors saves whether lookback dots are drawn.
func (a *App) UpdateProfilePriors(show bool) error {
	if a.settingsManager == nil {
		return fmt.Errorf("settings are not ready")
	}
	current, err := config.CloneSettings(a.settingsManager.GetSettings())
	if err != nil {
		return err
	}
	if current.ProfileSettings == nil {
		current.ProfileSettings = map[string]interface{}{}
	}
	current.ProfileSettings["show_priors"] = show
	return a.SaveSettings(current)
}

// GetSettings returns current settings for the webview.
// The API key is omitted. Internal callers use settingsManager.GetSettings.
func (a *App) GetSettings() *config.Settings {
	return config.RedactedSettings(a.settingsManager.GetSettings())
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

	// Preserve existing API key when frontend doesn't send one (e.g. user didn't change it)
	currentSettings := a.settingsManager.GetSettings()
	if settings.APITKey == "" && currentSettings.APITKey != "" {
		settings.APITKey = currentSettings.APITKey
		a.debugPrint(fmt.Sprintf("SaveSettings: Preserved existing API key (length: %d)", len(settings.APITKey)), "app")
	}

	// When user provided a new API key, persist it to file and use saveAPIKey=true so poller and all consumers get it
	saveAPIKey := settings.APITKey != "" && settings.APITKey != currentSettings.APITKey
	if saveAPIKey {
		a.debugPrint(fmt.Sprintf("SaveSettings: New API key provided (length: %d), will save to file and update runtime", len(settings.APITKey)), "app")
	}
	if err := a.settingsManager.SaveSettingsWithOptions(settings, saveAPIKey); err != nil {
		a.debugPrint(fmt.Sprintf("ERROR: SaveSettings failed: %v", err), "error")
		return err
	}

	a.debugPrint("Settings saved successfully", "app")

	// Reload settings to ensure consistency
	reloadedSettings, err := a.settingsManager.LoadSettings()
	if err != nil {
		a.debugPrint(fmt.Sprintf("WARNING: Failed to reload settings after save: %v", err), "error")
		// Still apply new API key to runtime so poller uses it even if reload failed
		if saveAPIKey && settings.APITKey != "" {
			a.applyRuntimeSettings(settings)
			a.debugPrint(fmt.Sprintf("SaveSettings: Updated API key after reload failure (length: %d)", len(settings.APITKey)), "app")
		}
	} else {
		// Preserve API key in reloaded settings when it wasn't written to file (normal save)
		if reloadedSettings.APITKey == "" && settings.APITKey != "" {
			reloadedSettings.APITKey = settings.APITKey
			a.debugPrint(fmt.Sprintf("SaveSettings: Restored API key in reloaded settings (length: %d)", len(reloadedSettings.APITKey)), "app")
		}
		a.settingsManager.SetSettings(reloadedSettings)
		a.applyRuntimeSettings(reloadedSettings)
		a.debugPrint("SaveSettings: Updated runtime settings", "app")

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
