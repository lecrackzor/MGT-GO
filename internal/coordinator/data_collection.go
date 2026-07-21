package coordinator

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"market-terminal/internal/api"
	"market-terminal/internal/database"
	"market-terminal/internal/scheduler"
	"market-terminal/internal/utils"
)

// DataCollectionCoordinator coordinates data collection operations
type DataCollectionCoordinator struct {
	mu                  sync.RWMutex
	querySystem         *api.QuerySystem
	dataWriter          *database.DataWriter
	scheduler           *scheduler.UnifiedAdaptiveScheduler
	queryPlanner        *SmartQueryPlanner
	writeQueue          *PriorityWriteQueue
	getShuttingDown     func() bool
	getOpenCharts       func() []interface{}
	debugPrint          func(string, string)
	tickersInProgress   map[string]bool
	inProgressLock      sync.RWMutex
	perTickerScheduler  *scheduler.PerTickerScheduler // Reference to trigger immediate polling
	currentMarketDate   time.Time // Track current market date for rollover detection
	dateMonitorStopChan chan struct{} // Channel to stop date monitor
	dateMonitorWg       sync.WaitGroup // Wait group for date monitor goroutine
}

// NewDataCollectionCoordinator creates a new data collection coordinator
func NewDataCollectionCoordinator(
	querySystem *api.QuerySystem,
	dataWriter *database.DataWriter,
	scheduler *scheduler.UnifiedAdaptiveScheduler,
	queryPlanner *SmartQueryPlanner,
	writeQueue *PriorityWriteQueue,
	getShuttingDown func() bool,
	getOpenCharts func() []interface{},
	debugPrint func(string, string),
) *DataCollectionCoordinator {
	return &DataCollectionCoordinator{
		querySystem:        querySystem,
		dataWriter:         dataWriter,
		scheduler:          scheduler,
		queryPlanner:       queryPlanner,
		writeQueue:         writeQueue,
		getShuttingDown:    getShuttingDown,
		getOpenCharts:      getOpenCharts,
		debugPrint:         debugPrint,
		tickersInProgress:  make(map[string]bool),
		perTickerScheduler: nil, // Will be set by app.go after scheduler is created
		currentMarketDate: utils.GetMarketDate(),
		dateMonitorStopChan: nil, // Will be created when monitor starts
	}
}

// SetPerTickerScheduler sets the per-ticker scheduler reference (called by app.go)
func (dcc *DataCollectionCoordinator) SetPerTickerScheduler(perTickerScheduler *scheduler.PerTickerScheduler) {
	dcc.mu.Lock()
	defer dcc.mu.Unlock()
	dcc.perTickerScheduler = perTickerScheduler
}

// StartDateRolloverMonitor starts a goroutine that monitors for date rollover
// When date rollover is detected (at 8:30 AM ET or on first open after market open),
// it flushes all pending writes and triggers immediate polling for all tickers
func (dcc *DataCollectionCoordinator) StartDateRolloverMonitor() {
	dcc.mu.Lock()
	defer dcc.mu.Unlock()
	
	if dcc.dateMonitorStopChan != nil {
		dcc.debugPrint("Date rollover monitor already running", "coordinator")
		return
	}
	
	dcc.dateMonitorStopChan = make(chan struct{})
	dcc.dateMonitorWg.Add(1)
	
	go dcc.dateRolloverMonitor()
	dcc.debugPrint("Date rollover monitor started", "coordinator")
	log.Printf("DataCollectionCoordinator: Date rollover monitor started (current market date: %s)", 
		dcc.currentMarketDate.Format("2006-01-02"))
}

// StopDateRolloverMonitor stops the date rollover monitor
func (dcc *DataCollectionCoordinator) StopDateRolloverMonitor() {
	dcc.mu.Lock()
	stopChan := dcc.dateMonitorStopChan
	dcc.dateMonitorStopChan = nil
	dcc.mu.Unlock()
	
	if stopChan != nil {
		close(stopChan)
		dcc.dateMonitorWg.Wait()
		dcc.debugPrint("Date rollover monitor stopped", "coordinator")
	}
}

// dateRolloverMonitor monitors for date rollover and handles it
func (dcc *DataCollectionCoordinator) dateRolloverMonitor() {
	defer dcc.dateMonitorWg.Done()
	
	// Check every 30 seconds for date rollover
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	dcc.debugPrint("Date rollover monitor: Starting monitoring loop", "coordinator")
	
	for {
		select {
		case <-dcc.dateMonitorStopChan:
			dcc.debugPrint("Date rollover monitor: Stop signal received", "coordinator")
			return
		case <-ticker.C:
			dcc.checkDateRollover()
		}
	}
}

// checkDateRollover checks if the market date has changed and handles rollover
func (dcc *DataCollectionCoordinator) checkDateRollover() {
	dcc.mu.Lock()
	oldDate := dcc.currentMarketDate
	dcc.mu.Unlock()
	
	// Get current market date
	newDate := utils.GetMarketDate()
	
	// Extract just the date part (ignore time) for comparison
	oldDateOnly := time.Date(oldDate.Year(), oldDate.Month(), oldDate.Day(), 0, 0, 0, 0, oldDate.Location())
	newDateOnly := time.Date(newDate.Year(), newDate.Month(), newDate.Day(), 0, 0, 0, 0, newDate.Location())
	
	// Check if date has changed
	if !oldDateOnly.Equal(newDateOnly) {
		dcc.debugPrint(fmt.Sprintf("Date rollover detected: %s -> %s", 
			oldDateOnly.Format("2006-01-02"), newDateOnly.Format("2006-01-02")), "coordinator")
		log.Printf("DataCollectionCoordinator: ===== DATE ROLLOVER DETECTED: %s -> %s =====", 
			oldDateOnly.Format("2006-01-02"), newDateOnly.Format("2006-01-02"))
		
		// Update tracked date
		dcc.mu.Lock()
		dcc.currentMarketDate = newDate
		dcc.mu.Unlock()
		
		// Flush all pending writes for the old date
		dcc.debugPrint("Date rollover: Flushing all pending writes for old date", "coordinator")
		if err := dcc.dataWriter.FlushAllTickers(); err != nil {
			dcc.debugPrint(fmt.Sprintf("Date rollover: Error flushing all tickers: %v", err), "error")
			log.Printf("DataCollectionCoordinator: ERROR - Failed to flush all tickers on date rollover: %v", err)
		} else {
			dcc.debugPrint("Date rollover: Successfully flushed all tickers", "coordinator")
			log.Printf("DataCollectionCoordinator: Successfully flushed all tickers on date rollover")
		}
		
		// Trigger immediate polling for all enabled tickers
		dcc.mu.RLock()
		perTickerScheduler := dcc.perTickerScheduler
		dcc.mu.RUnlock()
		
		if perTickerScheduler != nil {
			dcc.debugPrint("Date rollover: Triggering immediate polling for all tickers", "coordinator")
			log.Printf("DataCollectionCoordinator: Triggering immediate polling for all tickers on date rollover")
			perTickerScheduler.TriggerImmediatePolling()
		} else {
			dcc.debugPrint("Date rollover: WARNING - perTickerScheduler is nil, cannot trigger polling", "error")
			log.Printf("DataCollectionCoordinator: WARNING - perTickerScheduler is nil, cannot trigger polling on date rollover")
		}
		
		dcc.debugPrint(fmt.Sprintf("Date rollover: Completed handling rollover to %s", 
			newDateOnly.Format("2006-01-02")), "coordinator")
		log.Printf("DataCollectionCoordinator: Date rollover handling completed for %s", 
			newDateOnly.Format("2006-01-02"))
	}
}

// UpdateEnabledTickers updates the query planner's enabled tickers list
// This should be called when the user enables/disables tickers in settings
func (dcc *DataCollectionCoordinator) UpdateEnabledTickers(tickers []string) {
	dcc.mu.Lock()
	defer dcc.mu.Unlock()
	if dcc.queryPlanner != nil {
		dcc.queryPlanner.SetEnabledTickers(tickers)
		log.Printf("DataCollectionCoordinator: Updated enabled tickers to %d: %v", len(tickers), tickers)
	}
}

// ProcessTickerBatch processes a batch of tickers
func (dcc *DataCollectionCoordinator) ProcessTickerBatch(tickers []string) {
	if len(tickers) == 0 {
		dcc.debugPrint("ProcessTickerBatch called with empty ticker list", "coordinator")
		return
	}

	dcc.debugPrint(fmt.Sprintf("ProcessTickerBatch called with %d tickers: %v", len(tickers), tickers), "coordinator")

	// Check if shutting down
	if dcc.getShuttingDown() {
		dcc.debugPrint("Shutting down, skipping batch", "coordinator")
		return
	}

	// Skip the batch entirely while rate limited (429 backoff)
	rateLimitTracker := dcc.scheduler.GetRateLimitTracker()
	if rateLimitTracker.IsRateLimited() {
		utils.Logf("[RATE-LIMIT] Skipping batch for %v (retry in %.0fs)",
			tickers, rateLimitTracker.RetryAfterSeconds())
		return
	}

	// Build query plan
	plan := dcc.queryPlanner.BuildOptimizedPlan(tickers)
	if len(plan) == 0 {
		dcc.debugPrint("No query plan items - skipping batch", "coordinator")
		return
	}

	// Validate and filter queries (also deduplicates alias endpoints by URL)
	validatedQueries := dcc.querySystem.ValidateAndFilterQueries(plan)
	dcc.debugPrint(fmt.Sprintf("Validated %d queries (from %d plan items)", len(validatedQueries), len(plan)), "coordinator")

	// Track tickers in progress
	dcc.inProgressLock.Lock()
	for _, item := range plan {
		dcc.tickersInProgress[item.Ticker] = true
	}
	dcc.inProgressLock.Unlock()

	// Execute queries in parallel
	results := make(map[api.Query]map[string]interface{})
	fetchErrors := make(map[api.Query]error)
	var wg sync.WaitGroup
	var mu sync.Mutex

	maxWorkers := 96 // From config
	semaphore := make(chan struct{}, maxWorkers)

	for _, query := range validatedQueries {
		wg.Add(1)
		go func(q api.Query) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Skip remaining queries in this batch if a 429 already tripped the limiter
			if rateLimitTracker.IsRateLimited() {
				return
			}

			// Fetch endpoint
			result, err := dcc.querySystem.GetClient().FetchEndpoint(q.Endpoint, q.Ticker)
			dcc.recordRequestOutcome(rateLimitTracker, result, err)

			mu.Lock()
			if err != nil {
				fetchErrors[q] = err
			} else {
				results[q] = result
			}
			mu.Unlock()
		}(query)
	}

	wg.Wait()

	// Aggregate results by ticker
	tickerData := dcc.aggregateResults(plan, results, fetchErrors)

	// Process each ticker's data
	for ticker, data := range tickerData {
		if data != nil {
			dcc.debugPrint(fmt.Sprintf("Processing completed data for %s (fields: %d)", ticker, len(data)), "coordinator")
			dcc.ProcessCompletedTickerData(ticker, data, float64(time.Now().Unix()))
		} else {
			dcc.debugPrint(fmt.Sprintf("No data collected for %s", ticker), "coordinator")
		}
	}

	// Clean up in-progress tracking
	dcc.inProgressLock.Lock()
	for _, item := range plan {
		delete(dcc.tickersInProgress, item.Ticker)
	}
	dcc.inProgressLock.Unlock()
}

// recordRequestOutcome feeds request results into the rate limit tracker.
// On 429 it activates the global backoff (honoring Retry-After when provided);
// on success it records the request and any X-RateLimit-* headers.
func (dcc *DataCollectionCoordinator) recordRequestOutcome(
	tracker *scheduler.RateLimitTracker,
	result map[string]interface{},
	err error,
) {
	now := float64(time.Now().Unix())

	if err != nil {
		var rateLimitErr *api.RateLimitError
		if errors.As(err, &rateLimitErr) {
			retryAfter := 0.0
			if rateLimitErr.RetryAfter != "" {
				if v, parseErr := strconv.ParseFloat(rateLimitErr.RetryAfter, 64); parseErr == nil {
					retryAfter = v
				}
			}
			tracker.HandleRateLimitError(retryAfter)
			utils.Logf("[RATE-LIMIT] 429 rate limit hit (%s) - backing off %.0fs",
				rateLimitErr.Endpoint, tracker.RetryAfterSeconds())
		}
		return
	}

	var headers map[string]string
	if result != nil {
		if h, ok := result["_response_headers"].(map[string]string); ok {
			headers = h
		}
	}
	tracker.RecordRequest(now, true, headers)
}

// aggregateResults aggregates API results by ticker
func (dcc *DataCollectionCoordinator) aggregateResults(
	plan []api.QueryPlanItem,
	results map[api.Query]map[string]interface{},
	fetchErrors map[api.Query]error,
) map[string]map[string]interface{} {
	tickerData := make(map[string]map[string]interface{})

	// Initialize ticker data structures
	for _, item := range plan {
		if _, exists := tickerData[item.Ticker]; !exists {
			tickerData[item.Ticker] = make(map[string]interface{})
		}
	}

	// Aggregate results
	for query, result := range results {
		if result == nil {
			continue
		}

		ticker := query.Ticker
		data := tickerData[ticker]

		// Merge result into ticker data
		for key, value := range result {
			// Skip metadata keys
			if key == "_response_headers" || key == "_response_time" {
				continue
			}
			data[key] = value
		}
	}

	// Log errors
	for query, err := range fetchErrors {
		dcc.debugPrint("Error fetching "+query.Endpoint+" for "+query.Ticker+": "+err.Error(), "api")
	}

	return tickerData
}

// ProcessCompletedTickerData enqueues completed ticker data for writing
func (dcc *DataCollectionCoordinator) ProcessCompletedTickerData(ticker string, data map[string]interface{}, scheduledUpdateTime float64) {
	// Calculate timestamp
	currentTime := float64(time.Now().Unix())
	var timestampSeconds float64
	if apiTimestamp, ok := data["timestamp"].(float64); ok {
		// Check if timestamp is in milliseconds (> 1e10)
		if apiTimestamp > 1e10 {
			timestampSeconds = apiTimestamp / 1000.0
		} else {
			timestampSeconds = apiTimestamp
		}
	} else {
		timestampSeconds = currentTime
	}

	// Check if shutting down
	if dcc.getShuttingDown() {
		return
	}

	// Determine priority based on ticker visibility
	priority := 1 // Default to MEDIUM priority
	openCharts := dcc.getOpenCharts()
	if openCharts != nil {
		// Check if ticker is in any open chart
		for _, chartTicker := range openCharts {
			if chartTickerStr, ok := chartTicker.(string); ok && chartTickerStr == ticker {
				priority = 0 // HIGH priority for displayed tickers
				break
			}
		}
	}

	// Enqueue write
	dcc.debugPrint(fmt.Sprintf("Enqueuing write for %s (timestamp: %.0f, fields: %d, priority: %d)",
		ticker, timestampSeconds, len(data), priority), "coordinator")
	dcc.writeQueue.Enqueue(ticker, timestampSeconds, data, priority)
}

// IsTickerInProgress checks if a ticker is currently being processed
func (dcc *DataCollectionCoordinator) IsTickerInProgress(ticker string) bool {
	dcc.inProgressLock.RLock()
	defer dcc.inProgressLock.RUnlock()
	return dcc.tickersInProgress[ticker]
}
