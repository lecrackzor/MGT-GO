package database

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver - full memory visibility
)

// ConnectionPool manages database connections with idle timeout
type ConnectionPool struct {
	mu                sync.RWMutex
	connections       map[string]*pooledConnection
	maxSize           int
	idleTimeout       time.Duration
	cleanupInterval   time.Duration
	cleanupTimer      *time.Timer
	stopCleanup       chan struct{}
}

type pooledConnection struct {
	db          *sql.DB
	lastUsed    time.Time
	filepath    string
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(maxSize int, idleTimeout, cleanupInterval time.Duration) *ConnectionPool {
	pool := &ConnectionPool{
		connections:     make(map[string]*pooledConnection),
		maxSize:         maxSize,
		idleTimeout:     idleTimeout,
		cleanupInterval: cleanupInterval,
		stopCleanup:     make(chan struct{}),
	}

	// Start periodic cleanup
	pool.startCleanup()

	return pool
}

// GetConnection gets or creates a database connection
func (p *ConnectionPool) GetConnection(filepath string, readOnly bool) (*sql.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if connection exists and is still valid
	if pc, exists := p.connections[filepath]; exists {
		// Check if connection is still valid
		if err := pc.db.Ping(); err == nil {
			// Update last used time
			pc.lastUsed = time.Now()
			return pc.db, nil
		}
		// Connection is invalid - remove it
		pc.db.Close()
		delete(p.connections, filepath)
	}

	// Create new connection
	var db *sql.DB
	var err error

	if readOnly {
		// Read-only connection
		db, err = sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", filepath))
	} else {
		// Read-write connection
		db, err = sql.Open("sqlite", filepath)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection
	if err := p.configureConnection(db, readOnly); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to configure connection: %w", err)
	}

	// Add to pool
	p.connections[filepath] = &pooledConnection{
		db:       db,
		lastUsed: time.Now(),
		filepath: filepath,
	}

	return db, nil
}

// configureConnection sets SQLite PRAGMA options
func (p *ConnectionPool) configureConnection(db *sql.DB, readOnly bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Common settings
	_, err = conn.ExecContext(nil, "PRAGMA journal_mode=WAL")
	if err != nil {
		return err
	}

	_, err = conn.ExecContext(nil, "PRAGMA synchronous=NORMAL")
	if err != nil {
		return err
	}

	// Cache size: -20000 = 20MB (matches Python optimized version)
	// Negative value = KB, so -20000 = 20MB
	_, err = conn.ExecContext(nil, "PRAGMA cache_size=-20000")
	if err != nil {
		return err
	}

	_, err = conn.ExecContext(nil, "PRAGMA temp_store=MEMORY")
	if err != nil {
		return err
	}

	// Memory-mapped I/O: 256MB (matches Python optimized version from CHANGELOG)
	// This improves performance for large database files
	_, err = conn.ExecContext(nil, "PRAGMA mmap_size=268435456") // 256MB
	if err != nil {
		// Ignore if not supported
	}

	if readOnly {
		// Read-only specific settings
		_, err = conn.ExecContext(nil, "PRAGMA query_only=1")
		if err != nil {
			return err
		}

		_, err = conn.ExecContext(nil, "PRAGMA read_uncommitted=1")
		if err != nil {
			return err
		}

		_, err = conn.ExecContext(nil, "PRAGMA busy_timeout=10000") // 10 seconds
		if err != nil {
			return err
		}
	} else {
		// Write connection settings
		// Page size (only affects new databases)
		_, err = conn.ExecContext(nil, "PRAGMA page_size=8192")
		if err != nil {
			// Ignore if database already exists
		}
	}

	return nil
}

// startCleanup starts periodic cleanup of idle connections
func (p *ConnectionPool) startCleanup() {
	p.cleanupTimer = time.NewTimer(p.cleanupInterval)
	go func() {
		for {
			select {
			case <-p.cleanupTimer.C:
				p.cleanupIdleConnections()
				p.cleanupTimer.Reset(p.cleanupInterval)
			case <-p.stopCleanup:
				return
			}
		}
	}()
}

// cleanupIdleConnections closes connections that have been idle too long
func (p *ConnectionPool) cleanupIdleConnections() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for filepath, pc := range p.connections {
		if now.Sub(pc.lastUsed) > p.idleTimeout {
			pc.db.Close()
			delete(p.connections, filepath)
		}
	}
}

// Close closes all connections and stops cleanup
// Ensures WAL checkpoint is performed before closing to clean up .db-wal and .db-shm files
func (p *ConnectionPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Stop cleanup goroutine
	close(p.stopCleanup)
	if p.cleanupTimer != nil {
		p.cleanupTimer.Stop()
	}

	// Checkpoint WAL and close all connections
	// This ensures WAL data is merged into main DB and WAL/SHM files are deleted
	for _, pc := range p.connections {
		// Perform WAL checkpoint before closing
		// This merges WAL data into main DB and deletes .db-wal and .db-shm files
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, err := pc.db.Conn(ctx)
		if err == nil {
			// Execute WAL checkpoint (TRUNCATE mode moves WAL data to main DB and truncates WAL file)
			_, err = conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
			if err != nil {
				// Log but continue - checkpoint failure shouldn't prevent shutdown
				// Note: We can't use debugPrint here as we don't have access to it
				// The error will be visible in logs if logging is enabled at a higher level
			}
			conn.Close()
		}
		cancel()
		
		// Close the database connection
		pc.db.Close()
	}
	
	// Clear connections map
	p.connections = make(map[string]*pooledConnection)

	return nil
}

// Size returns current pool size
func (p *ConnectionPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.connections)
}
