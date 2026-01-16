package coordinator

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// HealthCheck monitors system health and detects stuck updates
type HealthCheck struct {
	mu                    sync.RWMutex
	coordinator           *DataCollectionCoordinator
	perTickerScheduler    interface {
		IsRunning() bool
		GetActiveTickerCount() int
	}
	debugPrint            func(string, string)
	
	// Tracking state
	lastFetchTimes        map[string]float64 // ticker -> last fetch time
	lastCheckTime         float64
	updateStartTime       *float64
	updateInProgress      bool
	recoveryAttempts      int
	lastRecoveryTime      float64
	
	// Thresholds
	stuckThresholdMs      float64 // 30 seconds
	criticalStuckMs       float64 // 60 seconds
	checkIntervalMs      float64 // 2 seconds
	
	// Control
	stopChan              chan struct{}
	isRunning             bool
	ticker                *time.Ticker
}

// NewHealthCheck creates a new health check system
func NewHealthCheck(
	coordinator *DataCollectionCoordinator,
	perTickerScheduler interface {
		IsRunning() bool
		GetActiveTickerCount() int
	},
	debugPrint func(string, string),
) *HealthCheck {
	return &HealthCheck{
		coordinator:        coordinator,
		perTickerScheduler: perTickerScheduler,
		debugPrint:         debugPrint,
		lastFetchTimes:     make(map[string]float64),
		lastCheckTime:      float64(time.Now().Unix()),
		stuckThresholdMs:   30000,  // 30 seconds
		criticalStuckMs:    60000,  // 60 seconds
		checkIntervalMs:    2000,   // 2 seconds
		stopChan:           make(chan struct{}),
	}
}

// Start starts the health check system
func (hc *HealthCheck) Start() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	
	if hc.isRunning {
		return
	}
	
	hc.isRunning = true
	hc.ticker = time.NewTicker(time.Duration(hc.checkIntervalMs) * time.Millisecond)
	
	go hc.run()
	
	hc.debugPrint("Health check system started", "system")
	log.Println("HealthCheck: Started monitoring system health")
}

// Stop stops the health check system
func (hc *HealthCheck) Stop() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	
	if !hc.isRunning {
		return
	}
	
	hc.isRunning = false
	close(hc.stopChan)
	
	if hc.ticker != nil {
		hc.ticker.Stop()
	}
	
	hc.debugPrint("Health check system stopped", "system")
}

// RecordFetch records that a ticker was fetched (called by coordinator)
func (hc *HealthCheck) RecordFetch(ticker string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	
	hc.lastFetchTimes[ticker] = float64(time.Now().Unix())
}

// SetUpdateInProgress sets the update in progress flag
func (hc *HealthCheck) SetUpdateInProgress(inProgress bool) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	
	hc.updateInProgress = inProgress
	if inProgress {
		now := float64(time.Now().Unix())
		hc.updateStartTime = &now
	} else {
		hc.updateStartTime = nil
	}
}

// run runs the health check loop
func (hc *HealthCheck) run() {
	for {
		select {
		case <-hc.ticker.C:
			hc.performCheck()
		case <-hc.stopChan:
			return
		}
	}
}

// performCheck performs a health check
func (hc *HealthCheck) performCheck() {
	hc.mu.Lock()
	currentTime := float64(time.Now().Unix()) * 1000 // milliseconds
	lastCheckTime := hc.lastCheckTime
	updateInProgress := hc.updateInProgress
	updateStartTime := hc.updateStartTime
	lastFetchTimes := make(map[string]float64)
	for k, v := range hc.lastFetchTimes {
		lastFetchTimes[k] = v
	}
	hc.mu.Unlock()
	
	// Check if scheduler is running
	if !hc.perTickerScheduler.IsRunning() {
		hc.debugPrint("⚠️ Health check: Per-ticker scheduler is not running", "error")
		log.Println("HealthCheck: WARNING - Per-ticker scheduler is not running")
		// Attempt recovery
		hc.triggerRecovery("Scheduler not running")
		return
	}
	
	// Check for stuck update
	if updateInProgress && updateStartTime != nil {
		updateDuration := currentTime - (*updateStartTime * 1000) // Convert to milliseconds
		if updateDuration > hc.criticalStuckMs {
			hc.debugPrint(fmt.Sprintf("⚠️ Health check: Update stuck for %.1fs (critical threshold)", updateDuration/1000), "error")
			log.Printf("HealthCheck: CRITICAL - Update stuck for %.1fs", updateDuration/1000)
			// Reset update flag and trigger recovery
			hc.SetUpdateInProgress(false)
			hc.triggerRecovery(fmt.Sprintf("Update stuck for %.1fs", updateDuration/1000))
			return
		} else if updateDuration > hc.stuckThresholdMs {
			hc.debugPrint(fmt.Sprintf("⚠️ Health check: Update in progress for %.1fs (monitoring)", updateDuration/1000), "system")
			log.Printf("HealthCheck: WARNING - Update in progress for %.1fs", updateDuration/1000)
		}
	}
	
	// Check if fetch times are stuck (no recent fetches)
	timeSinceLastCheck := currentTime - lastCheckTime
	if timeSinceLastCheck > 10000 { // 10 seconds
		// Check if any tickers have fetched recently
		nowSeconds := currentTime / 1000
		recentFetches := 0
		for _, fetchTime := range lastFetchTimes {
			if nowSeconds-fetchTime < 60 { // Within last 60 seconds
				recentFetches++
			}
		}
		
		if recentFetches == 0 && hc.perTickerScheduler.GetActiveTickerCount() > 0 {
			hc.debugPrint("⚠️ Health check: No recent fetches detected (possible stall)", "error")
			log.Println("HealthCheck: WARNING - No recent fetches detected")
			hc.triggerRecovery("No recent fetches detected")
			return
		}
	}
	
	// Update last check time
	hc.mu.Lock()
	hc.lastCheckTime = currentTime
	hc.mu.Unlock()
}

// triggerRecovery triggers a recovery action
func (hc *HealthCheck) triggerRecovery(reason string) {
	hc.mu.Lock()
	currentTime := float64(time.Now().Unix()) * 1000
	
	// Throttle recovery attempts (max 1 per 30 seconds)
	timeSinceLastRecovery := currentTime - hc.lastRecoveryTime
	if timeSinceLastRecovery < 30000 { // 30 seconds
		hc.mu.Unlock()
		return
	}
	
	hc.recoveryAttempts++
	hc.lastRecoveryTime = currentTime
	attempts := hc.recoveryAttempts
	hc.mu.Unlock()
	
	hc.debugPrint(fmt.Sprintf("🔄 Health check recovery triggered: %s (attempt %d)", reason, attempts), "system")
	log.Printf("HealthCheck: Recovery triggered - %s (attempt %d)", reason, attempts)
	
	// Reset update flag if stuck
	hc.SetUpdateInProgress(false)
	
	// Log recovery action
	hc.debugPrint(fmt.Sprintf("✅ Health check recovery completed: %s", reason), "system")
}

// GetStatus returns current health check status
func (hc *HealthCheck) GetStatus() map[string]interface{} {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	
	status := make(map[string]interface{})
	status["is_running"] = hc.isRunning
	status["scheduler_running"] = hc.perTickerScheduler.IsRunning()
	status["active_tickers"] = hc.perTickerScheduler.GetActiveTickerCount()
	status["update_in_progress"] = hc.updateInProgress
	status["recovery_attempts"] = hc.recoveryAttempts
	status["last_check_time"] = hc.lastCheckTime
	
	if hc.updateStartTime != nil {
		status["update_duration_ms"] = (float64(time.Now().Unix())*1000) - (*hc.updateStartTime * 1000)
	}
	
	return status
}
