package database

import (
	"fmt"
	"sync"
	"time"
)

// QueryCacheEntry represents a cached query result
type QueryCacheEntry struct {
	Data      map[string][]interface{}
	Timestamp time.Time
}

// QueryCache provides query result caching with TTL
// Matches Python version: 5-second TTL, 50 query limit, LRU eviction
type QueryCache struct {
	mu          sync.RWMutex
	cache       map[string]*QueryCacheEntry
	maxSize     int
	ttl         time.Duration
	accessOrder []string // LRU order (most recent at end)
}

// NewQueryCache creates a new query cache
func NewQueryCache(maxSize int, ttlSeconds float64) *QueryCache {
	return &QueryCache{
		cache:       make(map[string]*QueryCacheEntry),
		maxSize:     maxSize,
		ttl:         time.Duration(ttlSeconds * float64(time.Second)),
		accessOrder: make([]string, 0),
	}
}

// Get retrieves a cached query result if it exists and is not expired
func (qc *QueryCache) Get(key string) (map[string][]interface{}, bool) {
	qc.mu.RLock()
	defer qc.mu.RUnlock()
	
	entry, exists := qc.cache[key]
	if !exists {
		return nil, false
	}
	
	// Check if entry is expired
	if time.Since(entry.Timestamp) > qc.ttl {
		return nil, false
	}
	
	return entry.Data, true
}

// Set stores a query result in the cache
func (qc *QueryCache) Set(key string, data map[string][]interface{}) {
	qc.mu.Lock()
	defer qc.mu.Unlock()
	
	// Remove expired entries first
	qc.cleanupExpired()
	
	// Check if we need to evict (LRU)
	if len(qc.cache) >= qc.maxSize && qc.cache[key] == nil {
		// Evict least recently used (first in accessOrder)
		if len(qc.accessOrder) > 0 {
			oldestKey := qc.accessOrder[0]
			delete(qc.cache, oldestKey)
			qc.accessOrder = qc.accessOrder[1:]
		}
	}
	
	// Add/update entry
	qc.cache[key] = &QueryCacheEntry{
		Data:      data,
		Timestamp: time.Now(),
	}
	
	// Update access order (move to end if exists, append if new)
	qc.updateAccessOrder(key)
}

// updateAccessOrder updates the LRU access order
func (qc *QueryCache) updateAccessOrder(key string) {
	// Remove key from accessOrder if it exists
	for i, k := range qc.accessOrder {
		if k == key {
			qc.accessOrder = append(qc.accessOrder[:i], qc.accessOrder[i+1:]...)
			break
		}
	}
	// Add to end (most recent)
	qc.accessOrder = append(qc.accessOrder, key)
}

// cleanupExpired removes expired entries from cache
func (qc *QueryCache) cleanupExpired() {
	now := time.Now()
	expiredKeys := make([]string, 0)
	
	for key, entry := range qc.cache {
		if now.Sub(entry.Timestamp) > qc.ttl {
			expiredKeys = append(expiredKeys, key)
		}
	}
	
	for _, key := range expiredKeys {
		delete(qc.cache, key)
		// Remove from access order
		for i, k := range qc.accessOrder {
			if k == key {
				qc.accessOrder = append(qc.accessOrder[:i], qc.accessOrder[i+1:]...)
				break
			}
		}
	}
}

// Clear clears all cache entries
func (qc *QueryCache) Clear() {
	qc.mu.Lock()
	defer qc.mu.Unlock()
	
	qc.cache = make(map[string]*QueryCacheEntry)
	qc.accessOrder = make([]string, 0)
}

// Size returns current cache size
func (qc *QueryCache) Size() int {
	qc.mu.RLock()
	defer qc.mu.RUnlock()
	return len(qc.cache)
}

// GenerateCacheKey generates a cache key for a query
func GenerateCacheKey(ticker string, dateStr string, startTime, endTime float64) string {
	if startTime > 0 && endTime > 0 {
		return fmt.Sprintf("%s:%s:%.3f:%.3f", ticker, dateStr, startTime, endTime)
	}
	return fmt.Sprintf("%s:%s", ticker, dateStr)
}
