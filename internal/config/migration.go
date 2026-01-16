package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MigrateOldSettings migrates old JSON settings file to new YAML format
// Returns true if migration was performed, false if old file doesn't exist
func MigrateOldSettings(oldSettingsPath string, newConfigPath string) (bool, error) {
	// Check if old file exists
	if _, err := os.Stat(oldSettingsPath); os.IsNotExist(err) {
		return false, nil // No old file, nothing to migrate
	}

	// Read old JSON file
	data, err := os.ReadFile(oldSettingsPath)
	if err != nil {
		return false, fmt.Errorf("failed to read old settings file: %w", err)
	}

	// Parse JSON
	var oldSettings map[string]interface{}
	if err := json.Unmarshal(data, &oldSettings); err != nil {
		return false, fmt.Errorf("failed to parse old settings file: %w", err)
	}

	// Extract API key (will be moved to environment variable)
	apiKey, _ := oldSettings["api_key"].(string)

	// Create new settings struct from old data
	newSettings := &Settings{
		APITKey:                        "", // Will be loaded from env var
		APISubscriptionTiers:           getStringSlice(oldSettings, "api_subscription_tiers", []string{"classic"}),
		ActiveTickerRefreshRateMs:      getInt(oldSettings, "active_ticker_refresh_rate_ms", 5000),
		DataCollectionRefreshRateMs:    getInt(oldSettings, "data_collection_refresh_rate_ms", 30000),
		DataDirectory:                  getString(oldSettings, "data_directory", "Tickers"),
		TrimDataStartTime:              getString(oldSettings, "trim_data_start_time", "09:33"),
		TrimDataEndTime:                getString(oldSettings, "trim_data_end_time", "16:00"),
		EnableDebug:                    getBool(oldSettings, "enable_debug", false),
		HideConsole:                    getBool(oldSettings, "hide_console", true),
		ShowCrosshair:                  getBool(oldSettings, "show_crosshair", true),
		ShowDialogWarnings:             getBool(oldSettings, "show_dialog_warnings", true),
		CrosshairColor:                 getString(oldSettings, "crosshair_color", "808080"),
		CrosshairTextSize:              getInt(oldSettings, "crosshair_text_size", 12),
		CrosshairTextPlacement:         getString(oldSettings, "crosshair_text_placement", "top_center"),
		CrosshairBackgroundOpacity:      getInt(oldSettings, "crosshair_background_opacity", 180),
		CrosshairAxisMarkersEnabled:     getBool(oldSettings, "crosshair_axis_markers_enabled", true),
		CrosshairAxisMarkerSide:        getString(oldSettings, "crosshair_axis_marker_side", "right"),
		AlertDisplayTimeoutMs:           getInt(oldSettings, "alert_display_timeout_ms", 60000),
		PriceColor:                     getString(oldSettings, "price_color", "ffffff"),
		PriceFilterThresholdFuturesPercent: getFloat64(oldSettings, "price_filter_threshold_futures_percent", 3.0),
		PriceFilterThresholdStocksPercent:   getFloat64(oldSettings, "price_filter_threshold_stocks_percent", 7.0),
		LegendOpacity:                   getInt(oldSettings, "legend_opacity", 100),
		LegendFontColor:                getString(oldSettings, "legend_font_color", "ffffff"),
		LegendFontSize:                  getInt(oldSettings, "legend_font_size", 12),
		LegendBackgroundTransparent:    getBool(oldSettings, "legend_background_transparent", true),
		LegendBackgroundColor:         getString(oldSettings, "legend_background_color", "000000"),
		PriceAxisLocation:              getString(oldSettings, "price_axis_location", "left"),
		ChartTimezone:                  getString(oldSettings, "chart_timezone", "market"),
		Alerts:                         getInterfaceSlice(oldSettings, "alerts"),
		ProfileSettings:                getMap(oldSettings, "profile_settings"),
		Classic:                        getMap(oldSettings, "classic"),
		State:                          getMap(oldSettings, "state"),
		Orderflow:                      getMap(oldSettings, "orderflow"),
		Charts:                         getInterfaceSlice(oldSettings, "charts"),
		Tickers:                        getInterfaceSlice(oldSettings, "tickers"),
	}

	// Migrate ticker_configs if present
	if tickerConfigsRaw, ok := oldSettings["ticker_configs"].(map[string]interface{}); ok {
		tickerConfigs := make(map[string]TickerConfig)
		for ticker, configRaw := range tickerConfigsRaw {
			if configMap, ok := configRaw.(map[string]interface{}); ok {
				tickerConfigs[ticker] = TickerConfig{
					Display:           getBool(configMap, "display", true),
					CollectionEnabled: getBool(configMap, "collection_enabled", true),
					Priority:          getString(configMap, "priority", "medium"),
					RefreshRateMs:     getIntPtr(configMap, "refresh_rate_ms"),
				}
			}
		}
		newSettings.TickerConfigs = tickerConfigs
	}

	// Create config directory
	configDir := filepath.Dir(newConfigPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create config directory: %w", err)
	}

	// Save new YAML config
	manager := NewSettingsManager(newConfigPath)
	if err := manager.SaveSettings(newSettings); err != nil {
		return false, fmt.Errorf("failed to save new config: %w", err)
	}

	// If API key was found, create a note file with instructions
	if apiKey != "" {
		notePath := filepath.Join(configDir, "API_KEY_MIGRATION.txt")
		noteContent := fmt.Sprintf(`API Key Migration Notice

Your API key was found in the old settings file and has been migrated.

IMPORTANT: For security, please set the API key as an environment variable:

Windows (PowerShell):
  $env:GEXBOT_API_KEY = "%s"

Windows (CMD):
  set GEXBOT_API_KEY=%s

Linux/Mac:
  export GEXBOT_API_KEY=%s

Or add it to your shell profile (.bashrc, .zshrc, etc.) for persistence.

The application will check the environment variable first, then fall back to the config file.
`, apiKey, apiKey, apiKey)

		if err := os.WriteFile(notePath, []byte(noteContent), 0644); err != nil {
			// Non-fatal, just log
			fmt.Printf("Warning: Could not create migration note file: %v\n", err)
		}
	}

	return true, nil
}

// Helper functions for type-safe extraction
func getString(m map[string]interface{}, key string, defaultValue string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return defaultValue
}

func getInt(m map[string]interface{}, key string, defaultValue int) int {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case float64:
			return int(v)
		}
	}
	return defaultValue
}

func getFloat64(m map[string]interface{}, key string, defaultValue float64) float64 {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		}
	}
	return defaultValue
}

func getBool(m map[string]interface{}, key string, defaultValue bool) bool {
	if val, ok := m[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return defaultValue
}

func getStringSlice(m map[string]interface{}, key string, defaultValue []string) []string {
	if val, ok := m[key]; ok {
		if arr, ok := val.([]interface{}); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
	}
	return defaultValue
}

func getInterfaceSlice(m map[string]interface{}, key string) []interface{} {
	if val, ok := m[key]; ok {
		if arr, ok := val.([]interface{}); ok {
			return arr
		}
	}
	return []interface{}{}
}

func getMap(m map[string]interface{}, key string) map[string]interface{} {
	if val, ok := m[key]; ok {
		if m, ok := val.(map[string]interface{}); ok {
			return m
		}
	}
	return make(map[string]interface{})
}

func getIntPtr(m map[string]interface{}, key string) *int {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case int:
			return &v
		case float64:
			i := int(v)
			return &i
		}
	}
	return nil
}
