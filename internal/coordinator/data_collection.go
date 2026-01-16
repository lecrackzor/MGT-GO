package coordinator

import (
	"fmt"
	"log"
	"sync"
	"time"

	"market-terminal/internal/api"
	"market-terminal/internal/database"
	"market-terminal/internal/scheduler"
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
		querySystem:       querySystem,
		dataWriter:        dataWriter,
		scheduler:         scheduler,
		queryPlanner:      queryPlanner,
		writeQueue:        writeQueue,
		getShuttingDown:   getShuttingDown,
		getOpenCharts:     getOpenCharts,
		debugPrint:        debugPrint,
		tickersInProgress: make(map[string]bool),
		healthCheck:       nil, // Will be set by app.go after health check is created
	}
}

// SetHealthCheck sets the health check reference (called by app.go)
func (dcc *DataCollectionCoordinator) SetHealthCheck(healthCheck *HealthCheck) {
	dcc.mu.Lock()
	defer dcc.mu.Unlock()
	dcc.healthCheck = healthCheck
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
