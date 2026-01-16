package config

// Thread Pool Configuration
const (
	// API Executor Thread Pool
	// Increased for better throughput - threads are cheap for I/O-bound API calls
	APIExecutorWorkers = 96 // Unified API executor workers

	// Write Executor Thread Pool
	// Increased to handle more parallel writes and reduce queue buildup
	WriteExecutorWorkers = 16 // File write operations

	// Chart Executor Thread Pool
	// Separate thread pool for chart operations (isolated from data collection)
	ChartExecutorWorkers = 16 // Chart loading and rendering operations
)

// HTTP Connection Pool Configuration
const (
	HTTPPoolConnections = 128 // Number of connection pools to cache
	HTTPPoolMaxSize     = 128 // Max connections per pool
	HTTPMaxRetries      = 0   // No retries - fail fast
)

// Timestamp Deduplication Tolerance (in seconds)
const (
	TimestampDedupToleranceDataLoading    = 0.1  // 100ms tolerance for data loading/writing
	TimestampDedupToleranceChartRendering = 0.01 // 10ms tolerance for chart rendering (more precise)
)

// Database Connection Pool Configuration
const (
	DBConnectionPoolMaxSize     = 20   // Maximum number of connections to keep
	DBConnectionIdleTimeoutSec  = 180.0 // Close connections idle for 3 minutes
	SchemaVersionCacheMaxSize   = 200  // Maximum number of cached schema versions
	NumpyArrayCacheMaxSize      = 200  // Maximum number of cached arrays
	MaxWarnedTickers            = 100  // Maximum number of tickers to track warnings for
)

// Health Check and Recovery Settings
const (
	HealthCheckIntervalMs      = 2000  // Check every 2 seconds
	APIRecoveryTimeoutSec      = 10.0  // Hide banner after 10 seconds of successful calls
	UpdateStuckThresholdMs     = 30000 // Consider update stuck after 30 seconds
	UpdateStuckCriticalMs      = 60000 // Critical stuck threshold: 60 seconds
	UITriggerThrottleMs        = 2000  // Throttle UI event triggers (2 seconds)
	AppUptimeThresholdSec      = 5.0   // More than 5 seconds since startup
	HealthCheckRetryDelayMs    = 2000  // Retry health check start in 2 seconds
	HealthCheckStartDelayMs    = 1000  // Initial delay before starting health check
)

// Circuit Breaker Configuration
const (
	BatchTimeoutCircuitBreakerThreshold    = 3   // Consecutive timeouts before pausing batch creation
	BatchTimeoutCircuitBreakerBackoffSec   = 30  // Seconds to pause before retry after circuit breaker trips
	BatchTimeoutCircuitBreakerSuccessReset = 2   // Successful batches to reset timeout counter
)

// File Write Batching Configuration
const (
	// Active ticker write thresholds (faster flushing for UI responsiveness)
	FileWriteIntervalActiveSec = 1.0 // Batch for 1s before flushing
	FileWriteCountThresholdActive = 5 // Batch up to 5 entries before flushing

	// Collection-only ticker write thresholds (less frequent flushing)
	FileWriteIntervalCollectionSec = 2.0 // Batch for 2s before flushing (was 5s - reduced for better UI responsiveness)
	FileWriteCountThresholdCollection = 5 // Batch up to 5 entries before flushing (was 20)
)

// Database Configuration
const (
	DatabaseConnectionPoolCleanupThreshold = 35 // Cleanup when pool exceeds this size
	LockAcquisitionTimeout                 = 0.5 // Timeout for acquiring ticker locks (in seconds)
	LockStuckThresholdSec                  = 1.0 // Consider a lock stuck if held for longer than this
)

// Cache Configuration
const (
	DefaultMaxCacheHours              = 9     // Maximum hours of data to cache
	MaxHistoricalDataEntriesPerTicker = 30000 // Maximum historical data entries per ticker (full trading day at 1s = ~23,400)
)

// Tier Configuration
var TierNames = []string{"classic", "state", "orderflow"}

// API Configuration
const (
	APIBaseURL = "https://api.gexbot.com"
)

// Shutdown Timeouts
const (
	ShutdownWriteExecutorTimeout  = 30.0 // Seconds to wait for write executor shutdown
	ShutdownDatabaseCloseTimeout = 10.0 // Seconds to wait for database close
	ShutdownFlushThreadTimeout    = 30.0 // Seconds to wait for flush thread
	ShutdownFinalFlushTimeout    = 60.0 // Seconds for final flush
	ShutdownLockAcquisitionTimeout = 5.0 // Seconds for lock acquisition during shutdown
)

// SQLite Optimization Intervals
const (
	SQLiteWalCheckpointInterval = 20 // Checkpoint WAL file every N flushes
	SQLiteVacuumInterval        = 50 // VACUUM database every N flushes
)

// Chart Rendering Performance Configuration
const (
	CrosshairUpdateIntervalMs    = 18    // ~55fps for crosshair position updates
	HTMLUpdateThrottleMs        = 8     // 8ms throttle (120fps) for HTML updates
	HTMLUpdateDistanceThreshold = 0.001 // Very small threshold - update HTML on any meaningful movement
	MaxChartRenderTimeMs         = 300   // Target: < 300ms per chart for periodic updates
	MaxTotalChartRenderTimeMs    = 1000  // Budget: 1000ms total for all charts per update cycle
	MaxSQLiteQueryTimeMs         = 100   // Alert if query takes > 100ms
	MaxChartDataPoints           = 50000 // Maximum points to load from SQLite for chart updates
	ChartDecimationThreshold    = 40000 // Decimation kicks in when dataset exceeds this many points
	ChartDecimationTarget        = 30000 // Target number of points after decimation (full trading day)
)

// Write Queue Performance Thresholds
const (
	MaxWriteBatchTimeMs = 50  // Alert if batch processing takes > 50ms
	MaxWriteQueueSize   = 1000 // Alert if queue has > 1000 pending writes
)

// Database Configuration
const (
	MaxPendingWritesPerTicker         = 2000  // Maximum pending writes per ticker before forcing flush
	MaxFlushRetryAttempts             = 5     // Retry failed flushes up to 5 times before overflow buffer
	OverflowBufferThresholdRatio      = 0.9   // Start writing overflow at 90% of limit
	MaxOverflowBufferEntries          = 10000 // Maximum entries per ticker in overflow buffer
	MaxOverflowBufferTotalEntries     = 100000 // Maximum total entries across all tickers
	OverflowBufferWarningThreshold    = 0.7   // Warn when overflow buffer reaches 70% of limit
	RetryBackoffInitialMs            = 100   // Initial retry delay: 100ms
	RetryBackoffMultiplier           = 2.0   // Double delay on each retry
	RetryBackoffMaxMs                = 2000  // Maximum delay: 2 seconds
	FlushOperationTimeoutSec         = 30    // Maximum time for a flush operation to complete
)

// Priority-based Flush Scheduling
const (
	FlushIntervalPriority0Ms = 100  // Priority 0 (HIGH): Flush within 100ms
	FlushIntervalPriority1Ms = 1000 // Priority 1 (MEDIUM): Flush within 1s
	FlushIntervalPriority2Ms = 5000 // Priority 2 (LOW): Flush within 5s
	BatchCountThresholdPriority0 = 1  // Priority 0 (HIGH): Flush immediately or 1 entry
	BatchCountThresholdPriority1 = 5  // Priority 1 (MEDIUM): Flush at 5 entries
	BatchCountThresholdPriority2 = 20 // Priority 2 (LOW): Flush at 20 entries
)

// Queue Monitoring Thresholds
const (
	QueueSizeWarningThresholdRatio  = 0.8 // Warn when queue reaches 80% of limit
	QueueSizeEmergencyThresholdRatio = 0.9 // Emergency mode at 90%
	AdaptiveBatchingEnabled         = true // Enable adaptive batching based on queue size
	AdaptiveBatchReductionRatio     = 0.5 // Reduce batch size by 50% when approaching limit
	MinBatchCount                   = 1   // Minimum batch count (never flush more frequently than this)
)

// Chart Update Coordinator Configuration
const (
	UpdateLineageMaxEntries              = 100 // Keep last 100 updates in lineage tracking
	ChartUIUpdateDebounceMs              = 50  // 50ms debounce window to prevent duplicate signals
	NumpyCacheWarningThresholdPct       = 80  // Warn when NumPy cache exceeds 80% capacity
	NumpyCacheTrimThresholdPct          = 80  // Trim cache when it exceeds 80% of max size
	NumpyCacheTrimToPct                 = 50  // Trim cache down to 50% of max size when threshold exceeded
	HistoricalCacheWarningThresholdRatio = 0.9 // Warn when historical cache reaches 90% of limit
	CounterCleanupThreshold             = 50  // Clean up counters when they exceed 50 entries
	MaxPendingChartUpdatesPerTicker      = 100 // Maximum pending chart updates per ticker
)

// Chart Rendering Configuration
const (
	ChartXAxisLeftPaddingRatio = 0.03 // 3% left padding to prevent chart from touching edge
	TrailingViewRightBufferSec = 300 // 5 minutes in seconds
	CrosshairAutoCenterIdleSec = 1.5 // Auto-center after 1.5 seconds of no mouse movement
)

// Cache Configuration
const (
	CachePromotionSafetyMarginSeconds = 60 // Safety margin for cache promotion (60 seconds)
	CachePromotionMinCacheSize        = 100 // Minimum cache size before promotion runs
)

// Query Plan Timing Tolerance
const (
	QueryPlanTimingToleranceMs = 500 // 500ms tolerance for fetch time calculations
)

// Dictionary Size Limits
const (
	MaxEndpointFetchTimesEntries = 1000 // Should never exceed this with proper cleanup
)

// Alert Configuration
const (
	DefaultAlertDisplayTimeoutMs = 60000 // 60 seconds
)

// SQLite Connection Configuration
const (
	SQLiteConnectionIdleTimeoutSeconds     = 10.0 // Close connections idle for 10 seconds
	SQLiteConnectionCleanupIntervalSeconds = 5.0  // Run cleanup every 5 seconds
	SQLiteCacheSizeMB                     = 5    // 5MB cache per connection
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
