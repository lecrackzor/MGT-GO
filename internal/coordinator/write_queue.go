package coordinator

import (
	"fmt"
	"sync"
	"time"

	"market-terminal/internal/database"
)

// WriteTask represents a database write task
type WriteTask struct {
	Ticker    string
	Timestamp float64
	Data      map[string]interface{}
	Priority  int // 0=high, 1=medium, 2=low
}

// PriorityWriteQueue manages priority-based database writes
type PriorityWriteQueue struct {
	mu            sync.RWMutex
	dataWriter    *database.DataWriter
	pendingWrites map[string]*WriteTask // ticker -> task (only latest per ticker)
	debugPrint    func(string, string)
}

// NewPriorityWriteQueue creates a new priority write queue
func NewPriorityWriteQueue(dataWriter *database.DataWriter, debugPrint func(string, string)) *PriorityWriteQueue {
	return &PriorityWriteQueue{
		dataWriter:    dataWriter,
		pendingWrites: make(map[string]*WriteTask),
		debugPrint:    debugPrint,
	}
}

// Enqueue enqueues a write task
func (pwq *PriorityWriteQueue) Enqueue(ticker string, timestamp float64, data map[string]interface{}, priority int) {
	pwq.mu.Lock()
	defer pwq.mu.Unlock()

	// Store latest task per ticker (overwrites previous if exists)
	pwq.pendingWrites[ticker] = &WriteTask{
		Ticker:    ticker,
		Timestamp: timestamp,
		Data:      data,
		Priority:  priority,
	}

	pwq.debugPrint(fmt.Sprintf("Enqueue: Queued write for %s (timestamp: %.0f, fields: %d, priority: %d)", 
		ticker, timestamp, len(data), priority), "write_queue")

	// Process immediately (non-blocking)
	go pwq.processTask(ticker)
}

// processTask processes a write task
func (pwq *PriorityWriteQueue) processTask(ticker string) {
	pwq.debugPrint(fmt.Sprintf("processTask: Starting processing for %s", ticker), "write_queue")
	
	pwq.mu.Lock()
	task, exists := pwq.pendingWrites[ticker]
	if !exists {
		pwq.mu.Unlock()
		pwq.debugPrint(fmt.Sprintf("processTask: No pending write found for %s (may have been processed already)", ticker), "write_queue")
		return
	}
	// Remove from pending
	delete(pwq.pendingWrites, ticker)
	pwq.mu.Unlock()

	// Determine if ticker is active (priority 0)
	isActive := task.Priority == 0

	pwq.debugPrint(fmt.Sprintf("processTask: Processing write for %s (timestamp: %.0f, fields: %d, active: %v, priority: %d)", 
		task.Ticker, task.Timestamp, len(task.Data), isActive, task.Priority), "write_queue")

	// Write to database with retry logic
	maxRetries := 3
	retryDelays := []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second}
	
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		pwq.debugPrint(fmt.Sprintf("processTask: Calling WriteDataEntry for %s (attempt %d/%d)", 
			task.Ticker, attempt+1, maxRetries), "write_queue")
		
		err := pwq.dataWriter.WriteDataEntry(task.Ticker, task.Timestamp, task.Data, isActive)
		if err == nil {
			// Success
			pwq.debugPrint(fmt.Sprintf("processTask: Successfully queued write for %s (attempt %d)", 
				task.Ticker, attempt+1), "write_queue")
			break
		}
		
		lastErr = err
		if attempt < maxRetries-1 {
			delay := retryDelays[attempt]
			pwq.debugPrint(fmt.Sprintf("processTask: ⏳ Write error for %s (attempt %d/%d) - retrying in %v: %v", 
				task.Ticker, attempt+1, maxRetries, delay, err), "error")
			time.Sleep(delay)
			continue
		}
	}
	
	// If all retries failed, try synchronous fallback
	if lastErr != nil {
		pwq.debugPrint(fmt.Sprintf("❌ CRITICAL: All async write retries failed for %s, attempting synchronous fallback: %v", 
			task.Ticker, lastErr), "error")
		
		// Synchronous fallback - write directly without queue
		err := pwq.dataWriter.WriteDataEntry(task.Ticker, task.Timestamp, task.Data, isActive)
		if err != nil {
			pwq.debugPrint(fmt.Sprintf("❌ CRITICAL: Synchronous fallback also failed for %s: %v", task.Ticker, err), "error")
			// Data collection must continue even if write fails
			return
		}
		
		pwq.debugPrint(fmt.Sprintf("✅ CRITICAL RECOVERY: Synchronous write succeeded for %s after async failure", task.Ticker), "system")
	}
	
	pwq.debugPrint(fmt.Sprintf("Successfully queued write for %s", task.Ticker), "write_queue")

	// Flush if needed (for active tickers, flush immediately)
	if isActive {
		pwq.debugPrint(fmt.Sprintf("processTask: Scheduling immediate flush for active ticker %s", task.Ticker), "write_queue")
		go func() {
			time.Sleep(100 * time.Millisecond) // Small delay to allow batching
			pwq.debugPrint(fmt.Sprintf("processTask: Executing flush for active ticker %s", task.Ticker), "write_queue")
			if err := pwq.dataWriter.FlushTicker(task.Ticker); err != nil {
				pwq.debugPrint(fmt.Sprintf("processTask: ❌ Flush failed for %s: %v", task.Ticker, err), "error")
			} else {
				pwq.debugPrint(fmt.Sprintf("processTask: ✅ Flush completed for %s", task.Ticker), "write_queue")
			}
		}()
	} else {
		pwq.debugPrint(fmt.Sprintf("processTask: Ticker %s is not active (priority %d), flush will happen on threshold", 
			task.Ticker, task.Priority), "write_queue")
	}
}

// GetPendingCount returns the number of pending writes
func (pwq *PriorityWriteQueue) GetPendingCount() int {
	pwq.mu.RLock()
	defer pwq.mu.RUnlock()
	return len(pwq.pendingWrites)
}
