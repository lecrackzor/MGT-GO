package scheduler

import (
	"fmt"
	"log"
	"sync"
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
	ticker      string
	stopChan    chan struct{}
	timer       *time.Timer
	mu          sync.Mutex
	isRunning   bool
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

// runTickerGoroutine runs the scheduling loop for a single ticker
func (pts *PerTickerScheduler) runTickerGoroutine(ticker string, goroutine *TickerGoroutine) {
	// Add panic recovery to prevent goroutine from crashing
	defer func() {
		if r := recover(); r != nil {
			pts.debugPrint(fmt.Sprintf("Ticker %s: ❌ PANIC in goroutine: %v", ticker, r), "error")
			// Try to restart the goroutine
			pts.debugPrint(fmt.Sprintf("Ticker %s: Attempting to restart goroutine after panic", ticker), "scheduler")
			// Don't restart automatically - let health check handle it
		}
		pts.debugPrint(fmt.Sprintf("Ticker %s: Goroutine exiting", ticker), "scheduler")
	}()

	// Check market hours before triggering immediate fetch on startup
	// Only fetch if market is open (or after-hours is explicitly allowed)
	marketIsOpen := utils.IsMarketOpen()
	shouldFetchOnStartup := marketIsOpen || pts.allowAfterHours
	pts.debugPrint(fmt.Sprintf("Ticker %s: Starting goroutine (market open: %v, after-hours allowed: %v)", 
		ticker, marketIsOpen, pts.allowAfterHours), "scheduler")
	
	if shouldFetchOnStartup {
		pts.debugPrint(fmt.Sprintf("Ticker %s: Market is open, triggering immediate fetch", ticker), "scheduler")
		if pts.onTickerReady != nil {
			pts.onTickerReady(ticker)
			pts.debugPrint(fmt.Sprintf("Ticker %s: Immediate fetch triggered", ticker), "scheduler")
		} else {
			pts.debugPrint(fmt.Sprintf("Ticker %s: WARNING - onTickerReady callback is nil!", ticker), "error")
		}
	} else {
		pts.debugPrint(fmt.Sprintf("Ticker %s: Market is closed, skipping immediate fetch - will wait for market open", ticker), "scheduler")
	}

	// Track last market state to only log on changes
	lastMarketState := marketIsOpen
	loopCount := 0
	for {
		loopCount++
		// Only log loop iteration every 10 iterations to reduce noise
		if loopCount%10 == 0 {
			pts.debugPrint(fmt.Sprintf("Ticker %s: Loop iteration %d", ticker, loopCount), "scheduler")
		}
		// Check if we should stop
		goroutine.mu.Lock()
		if !goroutine.isRunning {
			goroutine.mu.Unlock()
			return
		}
		goroutine.mu.Unlock()

		// Check market hours first - if closed, use longer interval to avoid excessive checks
		marketIsOpen := utils.IsMarketOpen()
		var interval float64
		
		if !marketIsOpen && !pts.allowAfterHours {
			// Market is closed - use a longer interval (60 seconds) to check again
			interval = 60.0
			// Only log when market state changes
			if marketIsOpen != lastMarketState {
				pts.debugPrint(fmt.Sprintf("Ticker %s: Market is closed, using 60s interval for next check", ticker), "scheduler")
				lastMarketState = marketIsOpen
			}
		} else {
			// Market is open - calculate normal interval
			openCharts := pts.getOpenCharts()
			if openCharts == nil {
				openCharts = []interface{}{}
			}

			interval = pts.scheduler.CalculateInterval(ticker, openCharts)
			if interval <= 0 {
				interval = 5.0 // Default to 5 seconds
			}
		}

		// Record that we're about to fetch (prevents immediate re-fetch)
		pts.scheduler.RecordFetch(ticker)

		// Create timer
		goroutine.mu.Lock()
		if !goroutine.isRunning {
			goroutine.mu.Unlock()
			return
		}

		goroutine.timer = time.NewTimer(time.Duration(interval * float64(time.Second)))
		timer := goroutine.timer
		goroutine.mu.Unlock()

		// Wait for timer or stop signal
		pts.debugPrint(fmt.Sprintf("Ticker %s: Waiting for timer (interval: %.2fs) or stop signal", ticker, interval), "scheduler")
		select {
		case <-timer.C:
			// Timer fired - check market hours before fetching
			marketIsOpen := utils.IsMarketOpen()
			shouldFetch := marketIsOpen || pts.allowAfterHours
			
			// Only log timer firing if market state changed or if market is open
			if marketIsOpen != lastMarketState || marketIsOpen {
				pts.debugPrint(fmt.Sprintf("Ticker %s: Timer fired (market open: %v, after-hours allowed: %v)", 
					ticker, marketIsOpen, pts.allowAfterHours), "scheduler")
				lastMarketState = marketIsOpen
			}
			
			if !shouldFetch {
				// Market is closed and after-hours not allowed - skip this fetch
				// Use a longer interval (60 seconds) to check again when market might be open
				// Only log if state changed
				if marketIsOpen != lastMarketState {
					pts.debugPrint(fmt.Sprintf("Ticker %s: Market closed, skipping fetch - will check again in 60s", ticker), "scheduler")
					lastMarketState = marketIsOpen
				}
				// Continue loop with a longer wait time to avoid excessive checks when market is closed
				// The next iteration will recalculate the interval, but we'll use a minimum of 60s when closed
				continue
			}
			
			// Market is open - trigger fetch
			log.Printf("[TICKER-FETCH] %s: Timer fired, triggering fetch (interval was: %.2fs)", ticker, interval)
			pts.debugPrint(fmt.Sprintf("Ticker %s: Market is open, triggering fetch (interval: %.2fs)", 
				ticker, interval), "scheduler")
			if pts.onTickerReady != nil {
				pts.onTickerReady(ticker)
				log.Printf("[TICKER-FETCH] %s: Fetch callback completed", ticker)
				pts.debugPrint(fmt.Sprintf("Ticker %s: Fetch callback completed, continuing loop", ticker), "scheduler")
			} else {
				log.Printf("[TICKER-FETCH] %s: ERROR - onTickerReady is nil!", ticker)
				pts.debugPrint(fmt.Sprintf("Ticker %s: WARNING - onTickerReady is nil, cannot fetch!", ticker), "error")
			}
			// Continue loop to schedule next timer
		case <-goroutine.stopChan:
			// Stop signal received
			pts.debugPrint(fmt.Sprintf("Ticker %s: Stop signal received, exiting goroutine", ticker), "scheduler")
			timer.Stop()
			return
		case <-pts.stopChan:
			// Global stop signal
			pts.debugPrint(fmt.Sprintf("Ticker %s: Global stop signal received, exiting goroutine", ticker), "scheduler")
			timer.Stop()
			return
		}
		
		pts.debugPrint(fmt.Sprintf("Ticker %s: Continuing loop after timer/stop check", ticker), "scheduler")
	}
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
