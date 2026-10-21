package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	_ "time/tzdata" // Embed IANA timezone database for Windows compatibility

	"github.com/wailsapp/wails/v3/pkg/application"

	"market-terminal/internal/config"
	"market-terminal/internal/utils"
)

//go:embed all:frontend
var frontend embed.FS

func main() {
	hideConsoleWindow()

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

		// Handle frontend logging endpoint - allows frontend to log to backend terminal and log file
		if r.URL.Path == "/api/frontend-log" && r.Method == "POST" {
			var logData struct {
				Level   string `json:"level"`
				Message string `json:"message"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
			if err := decoder.Decode(&logData); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}
			// Always log warnings/errors; info/debug chatter only when EnableDebug is set
			level := strings.ToLower(logData.Level)
			logIt := level == "warn" || level == "warning" || level == "error"
			if !logIt {
				if s := appInstance.GetSettings(); s != nil && s.EnableDebug {
					logIt = true
				}
			}
			if logIt {
				// Log to terminal (stdout) - uppercase format for visibility
				log.Println(fmt.Sprintf("[FRONTEND-%s] %s", strings.ToUpper(logData.Level), logData.Message))
				// Log to file via utils.Logf - writes to both console and log file
				utils.Logf("[frontend-%s] %s", level, logData.Message)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		// Handle API routes
		if r.URL.Path == "/api/log" {
			path, text, ok := utils.CurrentLogTail(80_000)
			if !ok {
				path = ""
				text = ""
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			json.NewEncoder(w).Encode(map[string]string{"path": path, "text": text})
			return
		}

		if r.URL.Path == "/api/market-date" {
			// Get current market date (never cache - date rolls over at 8:30 AM ET)
			marketDate := appInstance.GetCurrentMarketDate()
			nowET := utils.NowMarketTime()
			log.Printf("[api/market-date] requested -> %s (now ET: %s)", marketDate, nowET.Format("2006-01-02 15:04:05 MST"))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
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

		if r.URL.Path == "/api/profile-layers" && r.Method == http.MethodPost {
			var layers map[string]bool
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
			if err := decoder.Decode(&layers); err != nil {
				http.Error(w, "invalid profile layers", http.StatusBadRequest)
				return
			}
			if err := appInstance.UpdateProfileLayers(layers); err != nil {
				utils.Logf("[HTTP] ERROR: profile layers failed: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
			return
		}

		if r.URL.Path == "/api/profile-layout" && r.Method == http.MethodPost {
			var body map[string]interface{}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12))
			if err := decoder.Decode(&body); err != nil {
				http.Error(w, "invalid profile layout", http.StatusBadRequest)
				return
			}
			if err := appInstance.UpdateProfileLayout(body); err != nil {
				utils.Logf("[HTTP] ERROR: profile layout failed: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
			return
		}

		if r.URL.Path == "/api/profile-priors" && r.Method == http.MethodPost {
			var body struct {
				ShowPriors bool `json:"show_priors"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12))
			if err := decoder.Decode(&body); err != nil {
				http.Error(w, "invalid profile priors", http.StatusBadRequest)
				return
			}
			if err := appInstance.UpdateProfilePriors(body.ShowPriors); err != nil {
				utils.Logf("[HTTP] ERROR: profile priors failed: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
			return
		}

		if r.URL.Path == "/api/available-dates" {
			// Get available dates (includes current market date so "Today" is always selectable)
			dates := appInstance.GetAvailableDates()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			json.NewEncoder(w).Encode(dates)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/profile/") {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/profile/"), "/")
			if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
				if !config.ValidTicker(parts[0]) {
					http.Error(w, "unknown ticker", http.StatusBadRequest)
					return
				}
				snap, err := appInstance.GetProfile(parts[0], parts[1], r.URL.Query().Get("at"), r.URL.Query().Get("endpoints"))
				if err != nil {
					utils.Logf("[HTTP] ERROR: GetProfile failed for %s: %v", parts[0], err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				if err := json.NewEncoder(w).Encode(snap); err != nil {
					http.Error(w, "Failed to encode response", http.StatusInternalServerError)
				}
				return
			}
			http.Error(w, "Invalid API path", http.StatusBadRequest)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/chart-data/") {
			// Per-request logging only when debugging - chart windows poll this
			// endpoint every 1.5s and the log lines add up fast
			httpDebug := false
			if s := appInstance.GetSettings(); s != nil {
				httpDebug = s.EnableDebug
			}

			// Parse path: /api/chart-data/{ticker}/{date}
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/chart-data/"), "/")
			if len(parts) >= 2 {
				ticker := parts[0]
				if !config.ValidTicker(ticker) {
					http.Error(w, "unknown ticker", http.StatusBadRequest)
					return
				}
				dateStr := parts[1]
				sinceStr := r.URL.Query().Get("since")

				if httpDebug {
					utils.Logf("[HTTP] chart-data request: ticker=%s, date=%s, since=%s", ticker, dateStr, sinceStr)
				}

				// Call GetChartData method (since optional for incremental load)
				data, err := appInstance.GetChartData(ticker, dateStr, sinceStr)
				if err != nil {
					utils.Logf("[HTTP] ERROR: GetChartData failed for %s: %v", ticker, err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				if httpDebug {
					utils.Logf("[HTTP] GetChartData succeeded for %s: %d timestamps", ticker, seriesLen(data["timestamp"]))
				}

				// Return JSON
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(data); err != nil {
					utils.Logf("[HTTP] ERROR: Failed to encode JSON for %s: %v", ticker, err)
					http.Error(w, "Failed to encode response", http.StatusInternalServerError)
					return
				}
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
		// Prevent a second app instance from silently doubling API usage.
		// A second launch focuses the existing window and exits.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.market-terminal.gexbot",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				utils.Logf("Second instance launch detected - focusing existing window")
				appInstance.FocusMainWindow()
			},
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
