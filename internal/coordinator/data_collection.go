package coordinator

import (
	"fmt"
	"log"
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
	healthCheck         *HealthCheck // Optional health check reference
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
		healthCheck:        nil, // Will be set by app.go after health check is created
		perTickerScheduler: nil,  // Will be set by app.go after scheduler is created
		currentMarketDate: utils.GetMarketDate(),
		dateMonitorStopChan: nil, // Will be created when monitor starts
	}
}

// SetHealthCheck sets the health check reference (called by app.go)
func (dcc *DataCollectionCoordinator) SetHealthCheck(healthCheck *HealthCheck) {
	dcc.mu.Lock()
	defer dcc.mu.Unlock()
	dcc.healthCheck = healthCheck
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
	log.Printf("DataCollectionCoordinator: Processing batch of %d tickers: %v", len(tickers), tickers)
	
	// Log open charts for priority calculation
	openCharts := dcc.getOpenCharts()
	log.Printf("DataCollectionCoordinator: Open charts: %v", openCharts)
	
	// Record fetch for health check (if health check is available)
	// This will be set by app.go after health check is created
	if dcc.healthCheck != nil {
		for _, ticker := range tickers {
			dcc.healthCheck.RecordFetch(ticker)
		}
	}

	// Check if shutting down
	if dcc.getShuttingDown() {
		dcc.debugPrint("Shutting down, skipping batch", "coordinator")
		return
	}

	// Build query plan
	plan := dcc.queryPlanner.BuildOptimizedPlan(tickers)
	log.Printf("DataCollectionCoordinator: Query plan generated with %d items", len(plan))
	if len(plan) == 0 {
		log.Printf("DataCollectionCoordinator: No query plan items - skipping batch")
		return
	}
	
	// Log plan details
	for _, item := range plan {
		log.Printf("DataCollectionCoordinator: Plan item - Ticker: %s, Endpoints: %v (count: %d)", item.Ticker, item.Endpoints, len(item.Endpoints))
	}

	// Convert to query system format
	queries := make([]api.Query, 0)
	for _, item := range plan {
		for _, endpoint := range item.Endpoints {
			queries = append(queries, api.Query{
				Ticker:   item.Ticker,
				Endpoint: endpoint,
			})
		}
	}

	// Convert plan to query system format
	planItems := make([]api.QueryPlanItem, 0, len(plan))
	for _, item := range plan {
		planItems = append(planItems, api.QueryPlanItem{
			Ticker:    item.Ticker,
			Endpoints: item.Endpoints,
		})
	}

	// Validate and filter queries
	validatedQueries := dcc.querySystem.ValidateAndFilterQueries(planItems)
	log.Printf("DataCollectionCoordinator: Validated %d queries (from %d plan items)", len(validatedQueries), len(planItems))

	// Set update in progress for health check
	if dcc.healthCheck != nil {
		dcc.healthCheck.SetUpdateInProgress(true)
	}
	
	// Track tickers in progress
	dcc.inProgressLock.Lock()
	for _, item := range plan {
		dcc.tickersInProgress[item.Ticker] = true
	}
	dcc.inProgressLock.Unlock()

	// Execute queries in parallel
	results := make(map[api.Query]map[string]interface{})
	errors := make(map[api.Query]error)
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

			// Fetch endpoint
			log.Printf("DataCollectionCoordinator: Fetching %s for %s", q.Endpoint, q.Ticker)
			result, err := dcc.querySystem.GetClient().FetchEndpoint(q.Endpoint, q.Ticker)
			
			mu.Lock()
			if err != nil {
				errors[q] = err
				log.Printf("DataCollectionCoordinator: Error fetching %s for %s: %v", q.Endpoint, q.Ticker, err)
			} else {
				results[q] = result
				fieldCount := 0
				if result != nil {
					fieldCount = len(result)
				}
				log.Printf("DataCollectionCoordinator: Successfully fetched %s for %s (fields: %d)", q.Endpoint, q.Ticker, fieldCount)
			}
			mu.Unlock()
		}(query)
	}

	wg.Wait()

	// Aggregate results by ticker
	tickerData := dcc.aggregateResults(plan, results, errors)

	// Process each ticker's data
	log.Printf("DataCollectionCoordinator: Processing data for %d tickers", len(tickerData))
	for ticker, data := range tickerData {
		if data != nil {
			dcc.debugPrint(fmt.Sprintf("Processing completed data for %s (fields: %d)", ticker, len(data)), "coordinator")
			log.Printf("DataCollectionCoordinator: Processing data for %s with %d fields", ticker, len(data))
			result := dcc.ProcessCompletedTickerData(ticker, data, float64(time.Now().Unix()))
			log.Printf("DataCollectionCoordinator: Completed processing for %s - timestamp: %.2f, priority: %v, interval: %.2f", 
				ticker, result["timestamp_seconds"], result["priority"], result["interval"])
		} else {
			dcc.debugPrint(fmt.Sprintf("No data collected for %s", ticker), "coordinator")
			log.Printf("DataCollectionCoordinator: No data collected for %s", ticker)
		}
	}

	// Clean up in-progress tracking
	dcc.inProgressLock.Lock()
	for _, item := range plan {
		delete(dcc.tickersInProgress, item.Ticker)
	}
	dcc.inProgressLock.Unlock()
	
	// Clear update in progress for health check
	if dcc.healthCheck != nil {
		dcc.healthCheck.SetUpdateInProgress(false)
	}
}

// aggregateResults aggregates API results by ticker
func (dcc *DataCollectionCoordinator) aggregateResults(
	plan []QueryPlanItem,
	results map[api.Query]map[string]interface{},
	errors map[api.Query]error,
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
	for query, err := range errors {
		dcc.debugPrint("Error fetching "+query.Endpoint+" for "+query.Ticker+": "+err.Error(), "api")
	}

	return tickerData
}

// ProcessCompletedTickerData processes completed ticker data
func (dcc *DataCollectionCoordinator) ProcessCompletedTickerData(ticker string, data map[string]interface{}, scheduledUpdateTime float64) map[string]interface{} {
	// Update scheduler state
	currentTime := float64(time.Now().Unix())
	dcc.scheduler.RecordFetch(ticker)

	// Calculate timestamp
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
		return map[string]interface{}{"timestamp_seconds": timestampSeconds, "skipped": true}
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
	dcc.debugPrint(fmt.Sprintf("Write enqueued for %s", ticker), "coordinator")

	// Calculate interval
	interval := dcc.scheduler.CalculateInterval(ticker, openCharts)

	return map[string]interface{}{
		"timestamp_seconds": timestampSeconds,
		"priority":          priority,
		"interval":          interval,
	}
}

// IsTickerInProgress checks if a ticker is currently being processed
func (dcc *DataCollectionCoordinator) IsTickerInProgress(ticker string) bool {
	dcc.inProgressLock.RLock()
	defer dcc.inProgressLock.RUnlock()
	return dcc.tickersInProgress[ticker]
}
