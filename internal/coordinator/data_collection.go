package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
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
	enabledTickers      []string
	getShuttingDown     func() bool
	getOpenCharts       func() []interface{}
	debugPrint          func(string, string)
	debugOn             func(string) bool
	perTickerScheduler  *scheduler.PerTickerScheduler // Reference to trigger immediate polling
	currentMarketDate   time.Time                     // Track current market date for rollover detection
	dateMonitorStopChan chan struct{}                 // Channel to stop date monitor
	dateMonitorWg       sync.WaitGroup                // Wait group for date monitor goroutine
}

// NewDataCollectionCoordinator creates a new data collection coordinator
func NewDataCollectionCoordinator(
	querySystem *api.QuerySystem,
	dataWriter *database.DataWriter,
	scheduler *scheduler.UnifiedAdaptiveScheduler,
	enabledTickers []string,
	getShuttingDown func() bool,
	getOpenCharts func() []interface{},
	debugPrint func(string, string),
) *DataCollectionCoordinator {
	tickers := make([]string, len(enabledTickers))
	copy(tickers, enabledTickers)
	return &DataCollectionCoordinator{
		querySystem:         querySystem,
		dataWriter:          dataWriter,
		scheduler:           scheduler,
		enabledTickers:      tickers,
		getShuttingDown:     getShuttingDown,
		getOpenCharts:       getOpenCharts,
		debugPrint:          debugPrint,
		perTickerScheduler:  nil, // Will be set by app.go after scheduler is created
		currentMarketDate:   utils.GetMarketDate(),
		dateMonitorStopChan: nil, // Will be created when monitor starts
	}
}

// SetDebugGate skips formatting log lines when the category is disabled.
func (dcc *DataCollectionCoordinator) SetDebugGate(fn func(string) bool) {
	dcc.debugOn = fn
}

func (dcc *DataCollectionCoordinator) logf(category, format string, args ...interface{}) {
	if dcc.debugOn != nil && !dcc.debugOn(category) {
		return
	}
	if dcc.debugPrint == nil {
		return
	}
	dcc.debugPrint(fmt.Sprintf(format, args...), category)
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

	stopChan := make(chan struct{})
	dcc.dateMonitorStopChan = stopChan
	dcc.dateMonitorWg.Add(1)

	go dcc.dateRolloverMonitor(stopChan)
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

// dateRolloverMonitor monitors for date rollover and handles it.
// stopChan is the channel Start created. Stop nils the struct field, so the
// goroutine must keep this local copy or a stop during checkDateRollover
// makes the next select wait on nil and Wait never returns.
func (dcc *DataCollectionCoordinator) dateRolloverMonitor(stopChan <-chan struct{}) {
	defer dcc.dateMonitorWg.Done()

	// Check every 30 seconds for date rollover
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	dcc.debugPrint("Date rollover monitor: Starting monitoring loop", "coordinator")

	for {
		select {
		case <-stopChan:
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
		dcc.logf("coordinator", "Date rollover detected: %s -> %s",
			oldDateOnly.Format("2006-01-02"), newDateOnly.Format("2006-01-02"))
		log.Printf("DataCollectionCoordinator: ===== DATE ROLLOVER DETECTED: %s -> %s =====",
			oldDateOnly.Format("2006-01-02"), newDateOnly.Format("2006-01-02"))

		// Update tracked date
		dcc.mu.Lock()
		dcc.currentMarketDate = newDate
		dcc.mu.Unlock()

		// Flush all pending writes for the old date
		dcc.debugPrint("Date rollover: Flushing all pending writes for old date", "coordinator")
		if err := dcc.dataWriter.FlushAllTickers(); err != nil {
			dcc.logf("error", "Date rollover: Error flushing all tickers: %v", err)
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

		dcc.logf("coordinator", "Date rollover: Completed handling rollover to %s",
			newDateOnly.Format("2006-01-02"))
		log.Printf("DataCollectionCoordinator: Date rollover handling completed for %s",
			newDateOnly.Format("2006-01-02"))
	}
}

// UpdateEnabledTickers updates which tickers are eligible for collection.
func (dcc *DataCollectionCoordinator) UpdateEnabledTickers(tickers []string) {
	dcc.mu.Lock()
	defer dcc.mu.Unlock()
	dcc.enabledTickers = make([]string, len(tickers))
	copy(dcc.enabledTickers, tickers)
	log.Printf("DataCollectionCoordinator: Updated enabled tickers to %d: %v", len(tickers), tickers)
}

// ProcessTickerBatch processes a batch of tickers
func (dcc *DataCollectionCoordinator) ProcessTickerBatch(tickers []string) {
	if len(tickers) == 0 {
		dcc.debugPrint("ProcessTickerBatch called with empty ticker list", "coordinator")
		return
	}

	dcc.logf("coordinator", "ProcessTickerBatch called with %d tickers: %v", len(tickers), tickers)

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

	dcc.mu.RLock()
	enabled := make([]string, len(dcc.enabledTickers))
	copy(enabled, dcc.enabledTickers)
	dcc.mu.RUnlock()

	validatedQueries := dcc.querySystem.BuildValidatedQueries(tickers, enabled)
	if len(validatedQueries) == 0 {
		dcc.debugPrint("No queries to run - skipping batch", "coordinator")
		return
	}
	dcc.logf("coordinator", "Validated %d queries", len(validatedQueries))

	// One 429 cancels the rest of this wave. A call already inside Do stops
	// with the context; later batches still wait out the limiter backoff.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

			// Skip remaining queries once this wave is cancelled or the backoff is set.
			if ctx.Err() != nil || rateLimitTracker.IsRateLimited() {
				return
			}

			// Fetch endpoint
			result, err := dcc.querySystem.GetClient().FetchEndpoint(ctx, q.Endpoint, q.Ticker)
			var rateLimitErr *api.RateLimitError
			if errors.As(err, &rateLimitErr) {
				cancel()
			}
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

	// Aggregate results by ticker in endpoint order, not map iteration order.
	tickerData := dcc.aggregateResults(validatedQueries, results, fetchErrors)

	for ticker, data := range tickerData {
		if len(data) == 0 {
			dcc.logf("coordinator", "No data collected for %s", ticker)
			continue
		}
		dcc.logf("coordinator", "Processing completed data for %s (fields: %d)", ticker, len(data))
		dcc.ProcessCompletedTickerData(ticker, data)
	}
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
	tracker.RecordRequest(now, headers)
}

// Chart columns come from the zero-DTE endpoint that actually computes them.
// classic_one / classic_full fill a column only when classic_zero did not.
// gamma_one fills only when gamma_zero did not. State and the other greeks
// repeat these keys (often as 0, or as the 1 DTE level) and must not replace them.
// spot uses the same classic rank so a later state or greek price cannot replace it.
var chartScalarSources = map[string]map[string]int{
	"spot":                {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"zero_gamma":          {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"major_pos_vol":       {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"major_neg_vol":       {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"major_pos_oi":        {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"major_neg_oi":        {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"sum_gex_vol":         {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"sum_gex_oi":          {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"delta_risk_reversal": {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"min_dte":             {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"sec_min_dte":         {"classic_zero": 0, "classic_one": 1, "classic_full": 2},
	"major_long_gamma":    {"gamma_zero": 0, "gamma_one": 1},
	"major_short_gamma":   {"gamma_zero": 0, "gamma_one": 1},
	"major_positive":      {"gamma_zero": 0, "gamma_one": 1},
	"major_negative":      {"gamma_zero": 0, "gamma_one": 1},
}

// keepChartScalar reports whether this endpoint may write the shared chart column.
// Lower rank wins. A field with no owner list keeps the previous last-write behavior.
func keepChartScalar(written map[string]map[string]int, ticker, endpoint, field string) bool {
	owners, owned := chartScalarSources[field]
	if !owned {
		return true
	}
	rank, ok := owners[endpoint]
	if !ok {
		return false
	}
	ranks := written[ticker]
	if ranks == nil {
		ranks = make(map[string]int)
		written[ticker] = ranks
	}
	if prev, seen := ranks[field]; seen && rank >= prev {
		return false
	}
	ranks[field] = rank
	return true
}

// aggregateResults merges endpoint payloads in the order they were planned.
// Scalars still share one map. Arrays and objects are kept under profiles[endpoint]
// so two ladders that both use "strikes" do not overwrite each other.
// A null from a later endpoint does not erase a value already kept.
// The row timestamp is the newest timestamp across the endpoints.
func (dcc *DataCollectionCoordinator) aggregateResults(
	queries []api.Query,
	results map[api.Query]map[string]interface{},
	fetchErrors map[api.Query]error,
) map[string]map[string]interface{} {
	tickerData := make(map[string]map[string]interface{})
	newest := make(map[string]float64)
	haveTS := make(map[string]bool)
	scalarRank := make(map[string]map[string]int)

	for _, query := range queries {
		data, exists := tickerData[query.Ticker]
		if !exists {
			data = make(map[string]interface{})
			tickerData[query.Ticker] = data
		}
		result := results[query]
		if result == nil {
			continue
		}
		if ts, ok := floatFrom(result["timestamp"]); ok {
			ts = unixSeconds(ts)
			if !haveTS[query.Ticker] || ts > newest[query.Ticker] {
				newest[query.Ticker] = ts
				haveTS[query.Ticker] = true
			}
		}
		endpointPayload := make(map[string]interface{})
		hasProfile := false
		for key, value := range result {
			if key == "_response_headers" || key == "_response_time" || key == "timestamp" {
				continue
			}
			if isBlank(value) {
				continue
			}
			if isProfileValue(value) {
				endpointPayload[key] = value
				hasProfile = true
				continue
			}
			endpointPayload[key] = value
			if keepChartScalar(scalarRank, query.Ticker, query.Endpoint, key) {
				data[key] = value
			}
		}
		if hasProfile {
			profs, _ := data["profiles"].(map[string]interface{})
			if profs == nil {
				profs = make(map[string]interface{})
				data["profiles"] = profs
			}
			profs[query.Endpoint] = endpointPayload
		}
	}
	for ticker, data := range tickerData {
		if haveTS[ticker] {
			data["timestamp"] = newest[ticker]
		}
	}

	for query, err := range fetchErrors {
		dcc.debugPrint("Error fetching "+query.Endpoint+" for "+query.Ticker+": "+err.Error(), "api")
	}

	return tickerData
}

func floatFrom(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func unixSeconds(v float64) float64 {
	if v > 1e10 {
		return v / 1000
	}
	return v
}

func isProfileValue(v interface{}) bool {
	switch v.(type) {
	case []interface{}, map[string]interface{}, json.RawMessage:
		return true
	default:
		return false
	}
}

func isBlank(v interface{}) bool {
	if v == nil {
		return true
	}
	raw, ok := v.(json.RawMessage)
	if !ok {
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

// ProcessCompletedTickerData writes completed ticker data.
func (dcc *DataCollectionCoordinator) ProcessCompletedTickerData(ticker string, data map[string]interface{}) {
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

	isActive := priority == 0
	dcc.logf("coordinator", "Writing %s (timestamp: %.0f, fields: %d, priority: %d)",
		ticker, timestampSeconds, len(data), priority)
	go func() {
		if err := dcc.dataWriter.WriteDataEntry(ticker, timestampSeconds, data, isActive); err != nil {
			dcc.logf("error", "Write failed for %s: %v", ticker, err)
		}
	}()
}
