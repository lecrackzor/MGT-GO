package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Settings represents the application settings
type Settings struct {
	mu sync.RWMutex // Protects settings access

	// API Key is loaded from environment variable GEXBOT_API_KEY first, then from config file
	// Note: omitempty is removed so API key is always written when present
	APITKey                        string                      `yaml:"api_key"`
	APISubscriptionTiers           []string                    `yaml:"api_subscription_tiers"`
	CollectAllEndpoints            bool                        `yaml:"collect_all_endpoints"` // true = collect all available data, false = chart data only
	ActiveTickerRefreshRateMs      int                         `yaml:"active_ticker_refresh_rate_ms"`
	DataCollectionRefreshRateMs    int                         `yaml:"data_collection_refresh_rate_ms"`
	DataDirectory                  string                      `yaml:"data_directory"`
	TrimDataStartTime              string                      `yaml:"trim_data_start_time"`
	TrimDataEndTime                string                      `yaml:"trim_data_end_time"`
	EnableDebug                    bool                        `yaml:"enable_debug"`
	EnableLogging                  bool                        `yaml:"enable_logging"`
	HideConsole                    bool                        `yaml:"hide_console"`
	UseMarketTime                  bool                        `yaml:"use_market_time"` // Display times in ET instead of local time
	HiddenPlots                    []string                    `yaml:"hidden_plots"`    // Plots hidden by default on charts
	ShowCrosshair                  bool                        `yaml:"show_crosshair"`
	ShowDialogWarnings             bool                        `yaml:"show_dialog_warnings"`
	CrosshairColor                 string                      `yaml:"crosshair_color"`
	CrosshairTextSize              int                         `yaml:"crosshair_text_size"`
	CrosshairTextPlacement         string                      `yaml:"crosshair_text_placement"`
	CrosshairBackgroundOpacity     int                         `yaml:"crosshair_background_opacity"`
	CrosshairAxisMarkersEnabled    bool                        `yaml:"crosshair_axis_markers_enabled"`
	CrosshairAxisMarkerSide        string                      `yaml:"crosshair_axis_marker_side"`
	AlertDisplayTimeoutMs         int                         `yaml:"alert_display_timeout_ms"`
	PriceColor                     string                      `yaml:"price_color"`
	PriceFilterThresholdFuturesPercent float64                 `yaml:"price_filter_threshold_futures_percent"`
	PriceFilterThresholdStocksPercent   float64                `yaml:"price_filter_threshold_stocks_percent"`
	LegendOpacity                 int                         `yaml:"legend_opacity"`
	LegendFontColor                string                      `yaml:"legend_font_color"`
	LegendFontSize                 int                         `yaml:"legend_font_size"`
	LegendBackgroundTransparent    bool                        `yaml:"legend_background_transparent"`
	LegendBackgroundColor          string                      `yaml:"legend_background_color"`
	PriceAxisLocation              string                      `yaml:"price_axis_location"`
	ChartTimezone                  string                      `yaml:"chart_timezone"`
	Alerts                         []interface{}               `yaml:"alerts"`
	ProfileSettings                map[string]interface{}     `yaml:"profile_settings"`
	Classic                        map[string]interface{}     `yaml:"classic"`
	State                          map[string]interface{}     `yaml:"state"`
	Orderflow                      map[string]interface{}     `yaml:"orderflow"`
	Charts                         []interface{}               `yaml:"charts"`
	Tickers                        []interface{}               `yaml:"tickers"`
	TickerConfigs                  map[string]TickerConfig    `yaml:"ticker_configs"`
	TickerOrder                    []string                    `yaml:"ticker_order,omitempty"` // User-defined ticker display order
	ChartColors                    map[string]string           `yaml:"chart_colors"` // Color preferences for chart data series
	ChartZoomFilterPercent         float64                    `yaml:"chart_zoom_filter_percent"` // Default Y-axis zoom filter as % of current spot price
	AutoFollowBufferPercent        float64                    `yaml:"auto_follow_buffer_percent"` // Buffer percentage for auto-follow (default 10%)
	WindowWidth                    int                         `yaml:"window_width,omitempty"`  // Last saved window width
	WindowHeight                   int                         `yaml:"window_height,omitempty"` // Last saved window height
}

// SettingsManager manages loading and saving settings
type SettingsManager struct {
	configFile string
	settings   *Settings
	mu         sync.RWMutex
}

// GetConfigDir returns the user config directory path
func GetConfigDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config directory: %w", err)
	}
	return filepath.Join(configDir, ConfigDirName), nil
}

// GetConfigPath returns the full path to the config file
func GetConfigPath() (string, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, ConfigFileName), nil
}

// NewSettingsManager creates a new settings manager
// If configFile is empty, uses default user config directory
func NewSettingsManager(configFile string) *SettingsManager {
	if configFile == "" {
		// Use default config path
		if path, err := GetConfigPath(); err == nil {
			configFile = path
		} else {
			// Fallback to current directory
			configFile = ConfigFileName
		}
	}

	return &SettingsManager{
		configFile: configFile,
		settings:   getDefaultSettings(),
	}
}

// LoadSettings loads settings from file
// API key is loaded from environment variable GEXBOT_API_KEY first, then from config file
func (sm *SettingsManager) LoadSettings() (*Settings, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if config file exists
	if _, err := os.Stat(sm.configFile); os.IsNotExist(err) {
		// File doesn't exist - check for old JSON file and migrate
		oldSettingsPath := filepath.Join(".", OldSettingsFileName)
		if migrated, err := MigrateOldSettings(oldSettingsPath, sm.configFile); err != nil {
			return nil, fmt.Errorf("migration failed: %w", err)
		} else if !migrated {
			// No old file, return defaults
			sm.settings = getDefaultSettings()
			// Load API key from environment
			sm.settings.APITKey = os.Getenv(APIKeyEnvVar)
			return sm.settings, nil
		}
		// Migration succeeded, continue to load new file
	}

	// Read config file
	data, err := os.ReadFile(sm.configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	var settings Settings
	if err := yaml.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Load API key from environment variable first, fallback to config file
	apiKey := os.Getenv(APIKeyEnvVar)
	if apiKey == "" {
		// Use API key from config file
		apiKey = settings.APITKey
		if apiKey != "" {
			log.Printf("API key loaded from config file (length: %d)", len(apiKey))
		} else {
			log.Printf("WARNING: API key is empty in both environment variable and config file")
		}
	} else {
		log.Printf("API key loaded from environment variable (length: %d)", len(apiKey))
		// If env var is set, use it but also preserve the one from config file for fallback
		if settings.APITKey != "" && settings.APITKey != apiKey {
			log.Printf("NOTE: Environment variable API key differs from config file, using env var")
		}
	}
	settings.APITKey = apiKey

	// Initialize TickerConfigs if nil
	if settings.TickerConfigs == nil {
		settings.TickerConfigs = make(map[string]TickerConfig)
	}

	sm.settings = &settings
	return sm.settings, nil
}

// SaveSettings saves settings to file
// Note: API key is NOT saved to file (should be in environment variable)
// If saveAPIKey is true, the API key will be saved (for first-time setup only)
func (sm *SettingsManager) SaveSettings(settings *Settings) error {
	return sm.SaveSettingsWithOptions(settings, false)
}

// SaveSettingsWithOptions saves settings with options
// saveAPIKey: if true, saves the API key to file (for first-time setup)
func (sm *SettingsManager) SaveSettingsWithOptions(settings *Settings, saveAPIKey bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Update internal settings
	sm.settings = settings

	// Create directory if it doesn't exist
	dir := filepath.Dir(sm.configFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create a copy for saving
	saveSettings := *settings
	
	log.Printf("SaveSettingsWithOptions: Input settings API key length: %d, saveAPIKey: %v", len(settings.APITKey), saveAPIKey)
	
	if !saveAPIKey {
		// For normal saves, preserve existing API key from file if it exists
		// This prevents wiping the API key when user changes other settings
		if saveSettings.APITKey == "" {
			// Try to load existing API key from file
			if existingData, err := os.ReadFile(sm.configFile); err == nil {
				var existingSettings Settings
				if err := yaml.Unmarshal(existingData, &existingSettings); err == nil {
					if existingSettings.APITKey != "" {
						saveSettings.APITKey = existingSettings.APITKey
						log.Printf("SaveSettingsWithOptions: Preserved existing API key from file (length: %d)", len(saveSettings.APITKey))
					} else {
						log.Printf("SaveSettingsWithOptions: No API key in existing file")
					}
				} else {
					log.Printf("SaveSettingsWithOptions: Failed to parse existing file: %v", err)
				}
			} else {
				log.Printf("SaveSettingsWithOptions: Could not read existing file: %v", err)
			}
		} else {
			// Settings already has API key - preserve it even for normal saves
			log.Printf("SaveSettingsWithOptions: Preserving API key from input settings (length: %d)", len(saveSettings.APITKey))
		}
	} else {
		// saveAPIKey=true: Save the API key to file (for first-time setup)
		if saveSettings.APITKey == "" {
			log.Printf("ERROR: SaveSettingsWithOptions called with saveAPIKey=true but API key is empty!")
		} else {
			log.Printf("SaveSettingsWithOptions: Saving API key to file (length: %d)", len(saveSettings.APITKey))
		}
	}
	
	log.Printf("SaveSettingsWithOptions: Final saveSettings API key length: %d", len(saveSettings.APITKey))

	// Marshal to YAML
	data, err := yaml.Marshal(&saveSettings)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}
	
	// Verify API key is in the marshaled data
	if saveSettings.APITKey != "" {
		// Check if API key appears in YAML string with a non-empty value
		dataStr := string(data)
		// Check for both "api_key:" and that it's not just "api_key: \"\""
		hasApiKeyField := strings.Contains(dataStr, "api_key:")
		hasEmptyApiKey := strings.Contains(dataStr, "api_key: \"\"") || strings.Contains(dataStr, "api_key: ''")
		
		if !hasApiKeyField || (hasApiKeyField && hasEmptyApiKey && saveAPIKey) {
			log.Printf("WARNING: API key not found or empty in marshaled YAML data! Forcing inclusion.")
			// Force include API key by creating a map and marshaling
			settingsMap := make(map[string]interface{})
			if err := yaml.Unmarshal(data, &settingsMap); err == nil {
				settingsMap["api_key"] = saveSettings.APITKey
				if newData, err := yaml.Marshal(&settingsMap); err == nil {
					data = newData
					log.Printf("SaveSettingsWithOptions: Forced API key into YAML data (length: %d)", len(saveSettings.APITKey))
					// Verify it's actually there now
					if strings.Contains(string(newData), "api_key:") && !strings.Contains(string(newData), "api_key: \"\"") {
						log.Printf("SaveSettingsWithOptions: Verified API key is now in YAML data")
					} else {
						log.Printf("ERROR: SaveSettingsWithOptions: API key still not properly in YAML after forcing!")
					}
				} else {
					log.Printf("ERROR: SaveSettingsWithOptions: Failed to marshal settings map with API key: %v", err)
				}
			} else {
				log.Printf("ERROR: SaveSettingsWithOptions: Failed to unmarshal data to map: %v", err)
			}
		} else {
			log.Printf("SaveSettingsWithOptions: API key verified in marshaled YAML data")
		}
	}

	// Write to file
	if err := os.WriteFile(sm.configFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	// Verify file was actually written
	if _, err := os.Stat(sm.configFile); os.IsNotExist(err) {
		return fmt.Errorf("config file was not created: %s", sm.configFile)
	}

	// Log success
	log.Printf("Settings file written successfully: %s (size: %d bytes)", sm.configFile, len(data))

	return nil
}

// GetConfigPath returns the config file path
func (sm *SettingsManager) GetConfigPath() string {
	return sm.configFile
}

// SetSettings updates the internal settings (thread-safe)
// Note: This preserves the API key if the new settings don't have one
func (sm *SettingsManager) SetSettings(settings *Settings) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	// Preserve API key if new settings don't have one
	if settings.APITKey == "" && sm.settings != nil && sm.settings.APITKey != "" {
		settings.APITKey = sm.settings.APITKey
		log.Printf("SetSettings: Preserved existing API key (length: %d)", len(settings.APITKey))
	}
	
	sm.settings = settings
}

// GetSettings returns current settings (thread-safe)
func (sm *SettingsManager) GetSettings() *Settings {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.settings
}

// SaveWindowDimensions saves only window dimensions without full settings reload
// This is a lightweight operation for use during window resize
func (sm *SettingsManager) SaveWindowDimensions(width, height int) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Update in-memory settings
	if sm.settings != nil {
		sm.settings.WindowWidth = width
		sm.settings.WindowHeight = height
	}

	// Read existing file to preserve all other settings
	existingData, err := os.ReadFile(sm.configFile)
	if err != nil {
		// If file doesn't exist, create it with current settings (including window dimensions)
		if os.IsNotExist(err) {
			log.Printf("Config file doesn't exist, creating with window dimensions: %dx%d", width, height)
			// Use current in-memory settings or create new default settings
			settingsToSave := sm.settings
			if settingsToSave == nil {
				settingsToSave = getDefaultSettings()
			}
			settingsToSave.WindowWidth = width
			settingsToSave.WindowHeight = height
			
			// Ensure config directory exists
			configDir := filepath.Dir(sm.configFile)
			if err := os.MkdirAll(configDir, 0755); err != nil {
				return fmt.Errorf("failed to create config directory: %w", err)
			}
			
			// Write new config file
			data, err := yaml.Marshal(settingsToSave)
			if err != nil {
				return fmt.Errorf("failed to marshal settings: %w", err)
			}
			
			if err := os.WriteFile(sm.configFile, data, 0644); err != nil {
				return fmt.Errorf("failed to write config file: %w", err)
			}
			
			log.Printf("Window dimensions saved to new config file: %dx%d", width, height)
			return nil
		}
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse existing settings
	var existingSettings Settings
	if err := yaml.Unmarshal(existingData, &existingSettings); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	// Only update window dimensions
	existingSettings.WindowWidth = width
	existingSettings.WindowHeight = height

	// Write back
	data, err := yaml.Marshal(&existingSettings)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	if err := os.WriteFile(sm.configFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	log.Printf("Window dimensions saved: %dx%d", width, height)
	return nil
}

// GetDefaultSettings returns default settings (exported for use in app.go)
func GetDefaultSettings() *Settings {
	return getDefaultSettings()
}

// getDefaultSettings returns default settings
func getDefaultSettings() *Settings {
	return &Settings{
		APITKey:                        "", // Loaded from environment variable
		APISubscriptionTiers:           []string{"classic"},
		CollectAllEndpoints:            true, // Default to collecting all available data
		ActiveTickerRefreshRateMs:      5000,
		DataCollectionRefreshRateMs:    30000,
		DataDirectory:                  "Tickers",
		TrimDataStartTime:              "09:33",
		TrimDataEndTime:                "16:00",
		EnableDebug:                    false,
		EnableLogging:                  true,
		HideConsole:                    true,
		UseMarketTime:                  false, // Default to local time
		HiddenPlots:                    []string{}, // No plots hidden by default
		ShowCrosshair:                  true,
		ShowDialogWarnings:             true,
		CrosshairColor:                 "808080",
		CrosshairTextSize:              12,
		CrosshairTextPlacement:         "top_center",
		CrosshairBackgroundOpacity:     180,
		CrosshairAxisMarkersEnabled:     true,
		CrosshairAxisMarkerSide:        "right",
		AlertDisplayTimeoutMs:          60000,
		PriceColor:                     "ffffff",
		PriceFilterThresholdFuturesPercent: 3.0,
		PriceFilterThresholdStocksPercent:   7.0,
		LegendOpacity:                  100,
		LegendFontColor:                "ffffff",
		LegendFontSize:                 12,
		LegendBackgroundTransparent:    true,
		LegendBackgroundColor:         "000000",
		PriceAxisLocation:              "left",
		ChartTimezone:                  "market",
		ChartZoomFilterPercent:         1.0, // Default 1% of current spot price
		AutoFollowBufferPercent:        1.0, // Default 1% buffer on the right
		Alerts:                         []interface{}{},
		ProfileSettings: map[string]interface{}{
			"center_x":           0.5,
			"bar_width_percent": 30.0,
			"bar_direction":     "both",
			"show_priors":       true,
			"show_reference_lines": true,
		},
		Classic: map[string]interface{}{
			"colors": map[string]interface{}{
				"zero_gamma":                        "fcb103",
				"pos_gamma":                         "00ff00",
				"neg_gamma":                         "ff0000",
				"major_pos_oi":                      "00942a",
				"major_neg_oi":                      "b10000",
				"classic_zero_majors_zero_gamma":    "fcb103",
				"classic_one_majors_zero_gamma":     "fcb103",
				"classic_full_majors_zero_gamma":    "fcb103",
			},
			"symbols": map[string]interface{}{},
		},
		State: map[string]interface{}{
			"colors": map[string]interface{}{
				"long_gamma":                        "00ffff",
				"short_gamma":                       "ae4ad5",
				"major_positive":                    "00ff00",
				"major_negative":                    "ff0000",
				"state_zero_majors_mpos_vol":        "00ff80",
				"state_zero_majors_mneg_vol":        "ff0080",
				"state_one_majors_mpos_vol":         "00ffa0",
				"state_one_majors_mneg_vol":         "00ffa0",
				"state_full_majors_mpos_vol":        "00ffc0",
				"state_full_majors_mneg_vol":        "ff00c0",
			},
			"symbols": map[string]interface{}{},
		},
		Orderflow: map[string]interface{}{
			"colors": map[string]interface{}{
				"delta":                             "00ffff",
				"volume":                            "808080",
				"orderflow_zero_majors_delta":      "00ff80",
				"orderflow_zero_majors_volume":     "808080",
				"orderflow_one_majors_delta":       "00ffa0",
				"orderflow_one_majors_volume":      "808080",
				"orderflow_full_majors_delta":      "00ffc0",
				"orderflow_full_majors_volume":     "808080",
			},
			"symbols": map[string]interface{}{},
		},
		Charts:      []interface{}{},
		Tickers:     []interface{}{},
		TickerConfigs: make(map[string]TickerConfig),
		ChartColors: map[string]string{
			"spot":              "#4CAF50",
			"zero_gamma":        "#FF9800",
			"major_pos_vol":     "#2196F3",
			"major_neg_vol":     "#F44336",
			"major_long_gamma":  "#9C27B0",
			"major_short_gamma": "#00BCD4",
			"major_positive":    "#8BC34A",
			"major_negative":    "#FF5722",
			"major_pos_oi":      "#3F51B5",
			"major_neg_oi":      "#E91E63",
		},
	}
}
