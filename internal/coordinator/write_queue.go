package coordinator

import (
	"fmt"
	"sync"

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

// processTask processes a write task.
// Flushing is handled entirely by the DataWriter (threshold-based flush in
// WriteDataEntry plus the 1s background flusher) - no duplicate flush here.
func (pwq *PriorityWriteQueue) processTask(ticker string) {
	pwq.mu.Lock()
	task, exists := pwq.pendingWrites[ticker]
	if !exists {
		pwq.mu.Unlock()
		return
	}
	// Remove from pending
	delete(pwq.pendingWrites, ticker)
	pwq.mu.Unlock()

	// Determine if ticker is active (priority 0)
	isActive := task.Priority == 0

	pwq.debugPrint(fmt.Sprintf("processTask: Processing write for %s (timestamp: %.0f, fields: %d, active: %v)",
		task.Ticker, task.Timestamp, len(task.Data), isActive), "write_queue")

	if err := pwq.dataWriter.WriteDataEntry(task.Ticker, task.Timestamp, task.Data, isActive); err != nil {
		pwq.debugPrint(fmt.Sprintf("processTask: Write failed for %s: %v", task.Ticker, err), "error")
	}
}

// GetPendingCount returns the number of pending writes
func (pwq *PriorityWriteQueue) GetPendingCount() int {
	pwq.mu.RLock()
	defer pwq.mu.RUnlock()
	return len(pwq.pendingWrites)
}
