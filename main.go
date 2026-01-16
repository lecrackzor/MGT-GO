package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // Memory profiling
	"strings"
	_ "time/tzdata" // Embed IANA timezone database for Windows compatibility

	"github.com/wailsapp/wails/v3/pkg/application"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

//go:embed all:frontend
var frontend embed.FS

// appWrapper wraps the application to expose Window field
type appWrapper struct {
	app interface {
		Window() interface {
			NewWithOptions(options application.WebviewWindowOptions) *application.WebviewWindow
		}
	}
}

func (w *appWrapper) Window() interface {
	NewWithOptions(options application.WebviewWindowOptions) *application.WebviewWindow
} {
	// Use reflection or type assertion to access Window field
	// Since Window is a field, we need to access it differently
	// For now, we'll pass the app directly and handle it in app.go
	return nil // This will be handled differently
}

func main() {
	// Load settings first to check EnableLogging
	settingsManager := config.NewSettingsManager("")
	settings, err := settingsManager.LoadSettings()
	enableLogging := true // Default to true
	if err == nil && settings != nil {
		enableLogging = settings.EnableLogging
	}

	// Initialize file logger conditionally based on EnableLogging setting
	if enableLogging {
		if err := utils.InitLogger("./logs"); err != nil {
			log.Printf("WARNING: Failed to initialize file logger: %v. Continuing with console logging only.", err)
		} else {
			utils.Logf("File logger initialized - logs will be written to ./logs/ directory")
		}
	} else {
		log.Printf("File logging disabled by user setting")
	}

	// Start memory profiler (for debugging)
	go func() {
		// Try to start profiler, but don't fail if port is in use
		addr := "localhost:6060"
		utils.Logf("Memory profiler starting on http://%s/debug/pprof/", addr)
		utils.Logf("  - Heap: http://%s/debug/pprof/heap", addr)
		utils.Logf("  - Allocs: http://%s/debug/pprof/allocs", addr)
		utils.Logf("  - Goroutine: http://%s/debug/pprof/goroutine", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			utils.Logf("Memory profiler unavailable (port 6060 may be in use): %v", err)
		}
	}()

	// Create app instance
	appInstance := NewApp()

	// Create custom handler that serves assets and API routes
	assetHandler := application.AssetFileServerFS(frontend)

	// Wrap handler to add API routes
	// IMPORTANT: Don't intercept /wails/* paths - let Wails handle them
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Let Wails handle its own runtime endpoints
		if strings.HasPrefix(r.URL.Path, "/wails/") {
			// Pass through to Wails' internal handler
			assetHandler.ServeHTTP(w, r)
			return
		}

		// Handle frontend test endpoint - allows frontend to verify it's executing
		if r.URL.Path == "/api/frontend-test" {
			log.Println("[FRONTEND-TEST] Frontend test endpoint called - frontend JavaScript IS executing!")
			utils.Logf("[frontend-test] Frontend test endpoint called - frontend JavaScript IS executing!")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "Frontend is executing"})
			return
		}

		// Handle frontend logging endpoint - allows frontend to log to backend terminal and log file
		if r.URL.Path == "/api/frontend-log" && r.Method == "POST" {
			var logData struct {
				Level   string `json:"level"`
				Message string `json:"message"`
			}
			if err := json.NewDecoder(r.Body).Decode(&logData); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}
			// Log to terminal (stdout) - uppercase format for visibility
			logMsg := fmt.Sprintf("[FRONTEND-%s] %s", strings.ToUpper(logData.Level), logData.Message)
			log.Println(logMsg)
			// Log to file via utils.Logf - writes to both console and log file
			// Format: [frontend-{level}] {message} - matches other file log entries
			utils.Logf("[frontend-%s] %s", strings.ToLower(logData.Level), logData.Message)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		// Handle API routes
		if r.URL.Path == "/api/market-date" {
			// Get current market date
			marketDate := appInstance.GetCurrentMarketDate()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"date": marketDate})
			return
		}

		if r.URL.Path == "/api/market-hours-local" {
			// Get market hours in local timezone
			openTime, closeTime := appInstance.GetMarketHoursLocal()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"open": openTime, "close": closeTime})
			return
		}

		if r.URL.Path == "/api/settings" {
			// Get settings (for chart windows to access colors)
			settings := appInstance.GetSettings()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(settings)
			return
		}

		if r.URL.Path == "/api/available-dates" {
			// Get available dates
			dates := appInstance.GetAvailableDates()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(dates)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/chart-data/") {
			utils.Logf("[HTTP] Received chart-data request: %s", r.URL.Path)

			// Parse path: /api/chart-data/{ticker}/{date}
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/chart-data/"), "/")
			if len(parts) >= 2 {
				ticker := parts[0]
				dateStr := parts[1]

				utils.Logf("[HTTP] Parsed ticker=%s, date=%s", ticker, dateStr)

				// Call GetChartData method
				utils.Logf("[HTTP] Calling GetChartData for %s on %s", ticker, dateStr)
				data, err := appInstance.GetChartData(ticker, dateStr)
				if err != nil {
					utils.Logf("[HTTP] ERROR: GetChartData failed for %s: %v", ticker, err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				// Log response data summary
				timestampCount := 0
				if timestamps, ok := data["timestamp"].([]interface{}); ok {
					timestampCount = len(timestamps)
					// Debug: Log first and last timestamp values to diagnose TZ issues
					if len(timestamps) > 0 {
						utils.Logf("[HTTP] First timestamp for %s: %v (type: %T)", ticker, timestamps[0], timestamps[0])
						if len(timestamps) > 1 {
							utils.Logf("[HTTP] Last timestamp for %s: %v (type: %T)", ticker, timestamps[len(timestamps)-1], timestamps[len(timestamps)-1])
						}
					}
				}
				utils.Logf("[HTTP] GetChartData succeeded for %s: %d timestamps, sending JSON response", ticker, timestampCount)

				// Return JSON
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(data); err != nil {
					utils.Logf("[HTTP] ERROR: Failed to encode JSON for %s: %v", ticker, err)
					http.Error(w, "Failed to encode response", http.StatusInternalServerError)
					return
				}
				utils.Logf("[HTTP] Successfully sent JSON response for %s", ticker)
				return
			}
			utils.Logf("[HTTP] ERROR: Invalid API path format: %s (expected /api/chart-data/{ticker}/{date})", r.URL.Path)
			http.Error(w, "Invalid API path", http.StatusBadRequest)
			return
		}

		// Serve static assets
		assetHandler.ServeHTTP(w, r)
	})

	// Create application
	app := application.New(application.Options{
		Name:        "Market Terminal Gexbot",
		Description: "Market data terminal for GEXBot API",
		Assets: application.AssetOptions{
			Handler: apiHandler,
		},
		Services: []application.Service{
			application.NewService(appInstance),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// Set function to create windows in appInstance
	SetCreateWindowFunc(func(options application.WebviewWindowOptions) *application.WebviewWindow {
		return app.Window.NewWithOptions(options)
	})
	appInstance.SetApp(app)

	utils.Logf("Application created, services registered")
	utils.Logf("Starting app - window will be created in ServiceStartup after backend initialization")

	// app.Run() will start the Wails runtime and call ServiceStartup
	// ServiceStartup will create the main window after backend is ready
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
