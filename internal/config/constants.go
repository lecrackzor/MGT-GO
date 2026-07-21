package config

// HTTP Connection Pool Configuration
const (
	HTTPPoolConnections = 128 // Number of connection pools to cache
	HTTPPoolMaxSize     = 128 // Max connections per pool
)

// Database Connection Pool Configuration
const (
	DBConnectionPoolMaxSize    = 20    // Maximum number of connections to keep
	DBConnectionIdleTimeoutSec = 180.0 // Close connections idle for 3 minutes
)

// File Write Batching Configuration
const (
	FileWriteIntervalCollectionSec    = 2.0 // Batch for 2s before flushing
	FileWriteCountThresholdCollection = 5   // Batch up to 5 entries before flushing
)

// SQLite Connection Configuration
const (
	SQLiteConnectionIdleTimeoutSeconds     = 10.0 // Close connections idle for 10 seconds
	SQLiteConnectionCleanupIntervalSeconds = 5.0  // Run cleanup every 5 seconds
)

// API Configuration
const (
	APIBaseURL = "https://api.gexbot.com"
)

// Config Directory and Environment Variables
const (
	// ConfigDirName is the name of the config directory in user's home/config directory
	ConfigDirName = "market-terminal"
	// ConfigFileName is the name of the config file
	ConfigFileName = "config.yaml"
	// APIKeyEnvVar is the environment variable name for the API key
	APIKeyEnvVar = "GEXBOT_API_KEY"
	// OldSettingsFileName is the old JSON settings file name (for migration)
	OldSettingsFileName = "market_terminal_settings.json"
)
