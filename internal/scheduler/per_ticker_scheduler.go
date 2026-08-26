package scheduler

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"market-terminal/internal/utils"
)

// PerTickerScheduler manages individual goroutines for each ticker
// This is more idiomatic Go than a master timer checking all tickers
type PerTickerScheduler struct {
	mu                sync.RWMutex
	scheduler         *UnifiedAdaptiveScheduler
	getOpenCharts     func() []interface{}
	onTickerReady     func(string) // Called when a single ticker is ready
	debugPrint        func(string, string)
	tickerGoroutines  map[string]*TickerGoroutine
	enabledTickers    []string
	stopChan          chan struct{}
	isRunning         bool
	allowAfterHours   bool // Allow data collection outside market hours
}

// TickerGoroutine manages a single ticker's scheduling goroutine
type TickerGoroutine struct {
	ticker          string
	stopChan        chan struct{}
	timer           *time.Timer
	mu              sync.Mutex
	isRunning       bool
	fetchInProgress atomic.Bool // Guards against overlapping fetches for this ticker
}

// NewPerTickerScheduler creates a new per-ticker scheduler
func NewPerTickerScheduler(
	scheduler *UnifiedAdaptiveScheduler,
	getOpenCharts func() []interface{},
	onTickerReady func(string), // Single ticker callback
	debugPrint func(string, string),
	allowAfterHours bool, // Allow data collection outside market hours
) *PerTickerScheduler {
	return &PerTickerScheduler{
		scheduler:        scheduler,
		getOpenCharts:    getOpenCharts,
		onTickerReady:    onTickerReady,
		debugPrint:       debugPrint,
		tickerGoroutines: make(map[string]*TickerGoroutine),
		stopChan:         make(chan struct{}),
		allowAfterHours:  allowAfterHours,
	}
}

// Start starts the scheduler and spawns goroutines for enabled tickers
func (pts *PerTickerScheduler) Start() {
	pts.mu.Lock()
	defer pts.mu.Unlock()

	if pts.isRunning {
		pts.debugPrint("Per-ticker scheduler already running", "system")
		return
	}

	pts.isRunning = true
	pts.stopChan = make(chan struct{})

	// Log all enabled tickers before spawning
	log.Printf("[SCHEDULER-START] ===== STARTING PER-TICKER SCHEDULER =====")
	log.Printf("[SCHEDULER-START] Enabled tickers count: %d", len(pts.enabledTickers))
	log.Printf("[SCHEDULER-START] Enabled tickers list: %v", pts.enabledTickers)

	// Spawn goroutines for all enabled tickers
	for i, ticker := range pts.enabledTickers {
		log.Printf("[SCHEDULER-START] Spawning goroutine %d/%d for ticker: %s", i+1, len(pts.enabledTickers), ticker)
		pts.spawnTickerGoroutine(ticker)
	}

	pts.debugPrint("Per-ticker scheduler started", "system")
	log.Printf("[SCHEDULER-START] ===== SCHEDULER STARTED: %d goroutines spawned =====", len(pts.tickerGoroutines))
}

// Stop stops all ticker goroutines
func (pts *PerTickerScheduler) Stop() {
	pts.mu.Lock()
	defer pts.mu.Unlock()

	if !pts.isRunning {
		return
	}

	// Stop all ticker goroutines
	for ticker, goroutine := range pts.tickerGoroutines {
		pts.stopTickerGoroutine(ticker, goroutine)
	}

	close(pts.stopChan)
	pts.isRunning = false

	pts.debugPrint("Per-ticker scheduler stopped", "system")
	log.Printf("PerTickerScheduler: Stopped")
}

// TriggerImmediatePolling triggers immediate polling for all enabled tickers
// This is useful when date rollover occurs and we need to start collecting data for the new date
func (pts *PerTickerScheduler) TriggerImmediatePolling() {
	pts.mu.RLock()
	goroutines := make(map[string]*TickerGoroutine, len(pts.tickerGoroutines))
	for ticker, goroutine := range pts.tickerGoroutines {
		goroutines[ticker] = goroutine
	}
	pts.mu.RUnlock()

	log.Printf("PerTickerScheduler: Triggering immediate polling for %d tickers after date rollover", len(goroutines))

	for ticker, goroutine := range goroutines {
		pts.triggerFetch(ticker, goroutine)
	}
}

// UpdateTickers updates the list of enabled tickers
// Spawns new goroutines for newly enabled tickers
// Stops goroutines for disabled tickers
func (pts *PerTickerScheduler) UpdateTickers(tickers []string) {
	pts.mu.Lock()
	defer pts.mu.Unlock()

	// Log current state
	currentCount := len(pts.tickerGoroutines)
	log.Printf("PerTickerScheduler: UpdateTickers called - current: %d goroutines, new: %d tickers", currentCount, len(tickers))

	// Create a set of new tickers
	newTickers := make(map[string]bool)
	for _, ticker := range tickers {
		newTickers[ticker] = true
	}

	// Stop goroutines for tickers that are no longer enabled
	stoppedCount := 0
	for ticker, goroutine := range pts.tickerGoroutines {
		if !newTickers[ticker] {
			log.Printf("PerTickerScheduler: Stopping goroutine for disabled ticker: %s", ticker)
			pts.stopTickerGoroutine(ticker, goroutine)
			delete(pts.tickerGoroutines, ticker)
			stoppedCount++
		}
	}

	// Spawn goroutines for newly enabled tickers (only if scheduler is running)
	spawnedCount := 0
	if pts.isRunning {
		for _, ticker := range tickers {
			if _, exists := pts.tickerGoroutines[ticker]; !exists {
				log.Printf("PerTickerScheduler: Spawning goroutine for enabled ticker: %s", ticker)
				pts.spawnTickerGoroutine(ticker)
				spawnedCount++
			}
		}
	} else {
		log.Printf("PerTickerScheduler: Scheduler not running, not spawning new goroutines")
	}

	pts.enabledTickers = make([]string, len(tickers))
	copy(pts.enabledTickers, tickers)

	log.Printf("PerTickerScheduler: Updated to %d enabled tickers (stopped: %d, spawned: %d, active: %d)", 
		len(pts.enabledTickers), stoppedCount, spawnedCount, len(pts.tickerGoroutines))
}

// spawnTickerGoroutine spawns a goroutine for a single ticker
func (pts *PerTickerScheduler) spawnTickerGoroutine(ticker string) {
	if !pts.isRunning {
		return
	}

	goroutine := &TickerGoroutine{
		ticker:    ticker,
		stopChan:  make(chan struct{}),
		isRunning: true,
	}

	pts.tickerGoroutines[ticker] = goroutine

	// Start goroutine
	go pts.runTickerGoroutine(ticker, goroutine)

	log.Printf("PerTickerScheduler: Spawned goroutine for %s", ticker)
}

// stopTickerGoroutine stops a ticker's goroutine
func (pts *PerTickerScheduler) stopTickerGoroutine(ticker string, goroutine *TickerGoroutine) {
	goroutine.mu.Lock()
	defer goroutine.mu.Unlock()

	if !goroutine.isRunning {
		return
	}

	goroutine.isRunning = false
	close(goroutine.stopChan)

	if goroutine.timer != nil {
		goroutine.timer.Stop()
	}

	log.Printf("PerTickerScheduler: Stopped goroutine for %s", ticker)
}

// runTickerGoroutine runs the scheduling loop for a single ticker.
//
// The cadence is anchored: each cycle's fire time is computed from the previous
// scheduled fire time (not from fetch completion), so a 1s interval actually
// fires every 1s regardless of how long the fetch takes. Fetches run
// asynchronously; if the previous fetch is still in flight when the next tick
// fires, that tick is skipped instead of stacking requests.
func (pts *PerTickerScheduler) runTickerGoroutine(ticker string, goroutine *TickerGoroutine) {
	// Add panic recovery to prevent goroutine from crashing
	defer func() {
		if r := recover(); r != nil {
			pts.debugPrint(fmt.Sprintf("Ticker %s: ❌ PANIC in goroutine: %v", ticker, r), "error")
		}
		pts.debugPrint(fmt.Sprintf("Ticker %s: Goroutine exiting", ticker), "scheduler")
	}()

	// Check market hours before triggering immediate fetch on startup
	marketIsOpen := utils.IsMarketOpen()
	pts.debugPrint(fmt.Sprintf("Ticker %s: Starting goroutine (market open: %v, after-hours allowed: %v)",
		ticker, marketIsOpen, pts.allowAfterHours), "scheduler")

	if marketIsOpen || pts.allowAfterHours {
		pts.triggerFetch(ticker, goroutine)
	} else {
		pts.debugPrint(fmt.Sprintf("Ticker %s: Market is closed, skipping immediate fetch - will wait for market open", ticker), "scheduler")
	}

	// Anchor for drift-free scheduling
	nextFire := time.Now()
	lastMarketState := marketIsOpen

	for {
		// Check if we should stop
		goroutine.mu.Lock()
		if !goroutine.isRunning {
			goroutine.mu.Unlock()
			return
		}
		goroutine.mu.Unlock()

		// Determine interval for this cycle
		marketIsOpen := utils.IsMarketOpen()
		var interval float64

		if !marketIsOpen && !pts.allowAfterHours {
			// Market is closed - check again in 60 seconds
			interval = 60.0
			if marketIsOpen != lastMarketState {
				pts.debugPrint(fmt.Sprintf("Ticker %s: Market is closed, using 60s interval for next check", ticker), "scheduler")
				lastMarketState = marketIsOpen
			}
		} else {
			openCharts := pts.getOpenCharts()
			if openCharts == nil {
				openCharts = []interface{}{}
			}

			interval = pts.scheduler.CalculateInterval(ticker, openCharts)
			if interval <= 0 {
				interval = 5.0 // Default to 5 seconds
			}
		}

		// Advance the anchor. If we've fallen behind (e.g. system sleep or a
		// very long cycle), re-anchor to now instead of bursting missed ticks.
		nextFire = nextFire.Add(time.Duration(interval * float64(time.Second)))
		wait := time.Until(nextFire)
		if wait < 0 {
			nextFire = time.Now()
			wait = 0
		}

		// Create timer
		goroutine.mu.Lock()
		if !goroutine.isRunning {
			goroutine.mu.Unlock()
			return
		}
		goroutine.timer = time.NewTimer(wait)
		timer := goroutine.timer
		goroutine.mu.Unlock()

		select {
		case <-timer.C:
			// Timer fired - check market hours before fetching
			marketIsOpen := utils.IsMarketOpen()
			if marketIsOpen != lastMarketState {
				pts.debugPrint(fmt.Sprintf("Ticker %s: Market open state changed: %v", ticker, marketIsOpen), "scheduler")
				lastMarketState = marketIsOpen
			}

			if !marketIsOpen && !pts.allowAfterHours {
				// Market is closed - skip this fetch; next cycle waits 60s
				continue
			}

			pts.triggerFetch(ticker, goroutine)
		case <-goroutine.stopChan:
			pts.debugPrint(fmt.Sprintf("Ticker %s: Stop signal received, exiting goroutine", ticker), "scheduler")
			timer.Stop()
			return
		case <-pts.stopChan:
			pts.debugPrint(fmt.Sprintf("Ticker %s: Global stop signal received, exiting goroutine", ticker), "scheduler")
			timer.Stop()
			return
		}
	}
}

// triggerFetch dispatches a fetch for the ticker asynchronously.
// If a fetch for this ticker is still in flight, the call is skipped so
// requests never stack behind a slow response.
func (pts *PerTickerScheduler) triggerFetch(ticker string, goroutine *TickerGoroutine) {
	if pts.onTickerReady == nil {
		pts.debugPrint(fmt.Sprintf("Ticker %s: WARNING - onTickerReady callback is nil!", ticker), "error")
		return
	}

	// Skip while rate limited - the coordinator would drop the batch anyway
	if tracker := pts.scheduler.GetRateLimitTracker(); tracker != nil && tracker.IsRateLimited() {
		return
	}

	if !goroutine.fetchInProgress.CompareAndSwap(false, true) {
		pts.debugPrint(fmt.Sprintf("Ticker %s: Previous fetch still in progress, skipping this tick", ticker), "scheduler")
		return
	}

	pts.scheduler.RecordFetch(ticker)

	go func() {
		defer goroutine.fetchInProgress.Store(false)
		defer func() {
			if r := recover(); r != nil {
				pts.debugPrint(fmt.Sprintf("Ticker %s: ❌ PANIC in fetch: %v", ticker, r), "error")
			}
		}()
		pts.onTickerReady(ticker)
	}()
}

// IsRunning checks if the scheduler is running
func (pts *PerTickerScheduler) IsRunning() bool {
	pts.mu.RLock()
	defer pts.mu.RUnlock()
	return pts.isRunning
}

// GetActiveTickerCount returns the number of active ticker goroutines
func (pts *PerTickerScheduler) GetActiveTickerCount() int {
	pts.mu.RLock()
	defer pts.mu.RUnlock()
	return len(pts.tickerGoroutines)
}
