package scheduler

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// MasterTimerScheduler checks all tickers periodically and batches ready ones
type MasterTimerScheduler struct {
	mu                sync.RWMutex
	scheduler         *UnifiedAdaptiveScheduler
	enabledTickers    []string
	getOpenCharts     func() []interface{}
	onTickersReady    func([]string)
	debugPrint        func(string, string)
	ticker            *time.Ticker
	stopChan          chan struct{}
	isRunning         bool
	checkInterval     time.Duration
}

// NewMasterTimerScheduler creates a new master timer scheduler
func NewMasterTimerScheduler(
	scheduler *UnifiedAdaptiveScheduler,
	getOpenCharts func() []interface{},
	onTickersReady func([]string),
	debugPrint func(string, string),
) *MasterTimerScheduler {
	return &MasterTimerScheduler{
		scheduler:      scheduler,
		getOpenCharts:  getOpenCharts,
		onTickersReady: onTickersReady,
		debugPrint:     debugPrint,
		checkInterval:  100 * time.Millisecond, // Check every 100ms
		stopChan:       make(chan struct{}),
	}
}

// Start starts the master timer
func (mts *MasterTimerScheduler) Start() {
	mts.mu.Lock()
	defer mts.mu.Unlock()

	if mts.isRunning {
		mts.debugPrint("Master timer already running", "system")
		return
	}

	mts.ticker = time.NewTicker(mts.checkInterval)
	mts.isRunning = true
	mts.stopChan = make(chan struct{})

	// Start goroutine to check tickers
	go mts.run()

	mts.debugPrint("Master timer scheduler started", "system")
	log.Printf("MasterTimerScheduler: Started with %d enabled tickers", len(mts.enabledTickers))

	// Trigger immediate check on startup
	if len(mts.enabledTickers) > 0 {
		mts.debugPrint("Triggering immediate check on startup", "system")
		log.Printf("MasterTimerScheduler: Triggering immediate check for %d tickers", len(mts.enabledTickers))
		mts.checkAllTickers()
	} else {
		log.Printf("MasterTimerScheduler: No enabled tickers - timer running but no data collection will occur")
	}
}

// Stop stops the master timer
func (mts *MasterTimerScheduler) Stop() {
	mts.mu.Lock()
	defer mts.mu.Unlock()

	if !mts.isRunning {
		return
	}

	if mts.ticker != nil {
		mts.ticker.Stop()
	}
	close(mts.stopChan)
	mts.isRunning = false

	mts.debugPrint("Master timer scheduler stopped", "system")
}

// UpdateTickers updates the list of enabled tickers
func (mts *MasterTimerScheduler) UpdateTickers(tickers []string) {
	mts.mu.Lock()
	defer mts.mu.Unlock()
	mts.enabledTickers = make([]string, len(tickers))
	copy(mts.enabledTickers, tickers)
}

// run runs the main loop
func (mts *MasterTimerScheduler) run() {
	tickCount := 0
	for {
		select {
		case <-mts.ticker.C:
			tickCount++
			if tickCount%10 == 0 { // Log every 10 ticks (1 second)
				mts.mu.RLock()
				tickerCount := len(mts.enabledTickers)
				mts.mu.RUnlock()
				log.Printf("MasterTimerScheduler: Tick #%d, checking %d enabled tickers", tickCount, tickerCount)
			}
			mts.checkAllTickers()
		case <-mts.stopChan:
			log.Printf("MasterTimerScheduler: Stopped after %d ticks", tickCount)
			return
		}
	}
}

// checkAllTickers checks all tickers and batches ready ones
func (mts *MasterTimerScheduler) checkAllTickers() {
	mts.mu.RLock()
	tickers := make([]string, len(mts.enabledTickers))
	copy(tickers, mts.enabledTickers)
	mts.mu.RUnlock()

	if len(tickers) == 0 {
		// No tickers enabled, skip check
		return
	}

	// Get open charts
	openCharts := mts.getOpenCharts()
	if openCharts == nil {
		openCharts = []interface{}{}
	}

	// Find ready tickers
	readyTickers := make([]string, 0)
	for _, ticker := range tickers {
		if mts.scheduler.ShouldFetchTicker(ticker, openCharts) {
			readyTickers = append(readyTickers, ticker)
		}
	}

	// Call callback with ready tickers
	if len(readyTickers) > 0 && mts.onTickersReady != nil {
		mts.debugPrint(fmt.Sprintf("Master timer: %d tickers ready to fetch: %v", len(readyTickers), readyTickers), "scheduler")
		mts.onTickersReady(readyTickers)
	}
}

// IsRunning checks if the master timer is running
func (mts *MasterTimerScheduler) IsRunning() bool {
	mts.mu.RLock()
	defer mts.mu.RUnlock()
	return mts.isRunning
}
