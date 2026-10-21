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
	mu              sync.RWMutex
	connections     map[string]*pooledConnection
	maxSize         int
	idleTimeout     time.Duration
	cleanupInterval time.Duration
	cleanupTimer    *time.Timer
	stopCleanup     chan struct{}
}

type pooledConnection struct {
	db       *sql.DB
	lastUsed time.Time
	filepath string
}

// NewConnectionPool creates a new connection pool.
// maxSize is stored and not enforced. A pooled handle may still be inside a
// query, and closing it to honor the cap would fail that query. Idle files
// already close after idleTimeout.
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

// GetConnection gets or creates a database connection.
// The pool lock is not held across Ping or open. One slow file must not
// block every other file. The lock is taken again only to publish the handle.
func (p *ConnectionPool) GetConnection(filepath string, readOnly bool) (*sql.DB, error) {
	p.mu.Lock()
	if pc, exists := p.connections[filepath]; exists {
		// A connection used in the last 30 seconds is reused without a ping.
		// Discard removes it when a query fails, and the next checkout opens a new one.
		if time.Since(pc.lastUsed) < 30*time.Second {
			pc.lastUsed = time.Now()
			db := pc.db
			p.mu.Unlock()
			return db, nil
		}
		db := pc.db
		p.mu.Unlock()

		if err := db.Ping(); err == nil {
			p.mu.Lock()
			if cur, ok := p.connections[filepath]; ok && cur.db == db {
				cur.lastUsed = time.Now()
				p.mu.Unlock()
				return db, nil
			}
			p.mu.Unlock()
		} else {
			p.mu.Lock()
			if cur, ok := p.connections[filepath]; ok && cur.db == db {
				delete(p.connections, filepath)
				p.mu.Unlock()
				db.Close()
			} else {
				p.mu.Unlock()
			}
		}
	} else {
		p.mu.Unlock()
	}

	db, err := p.openConnection(filepath, readOnly)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	if cur, ok := p.connections[filepath]; ok {
		cur.lastUsed = time.Now()
		p.mu.Unlock()
		db.Close()
		return cur.db, nil
	}
	p.connections[filepath] = &pooledConnection{
		db:       db,
		lastUsed: time.Now(),
		filepath: filepath,
	}
	p.mu.Unlock()
	return db, nil
}

// openConnection opens one file outside the pool lock.
func (p *ConnectionPool) openConnection(filepath string, readOnly bool) (*sql.DB, error) {
	var db *sql.DB
	var err error

	if readOnly {
		db, err = sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", filepath))
	} else {
		db, err = sql.Open("sqlite", filepath)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Single underlying connection per file. This guarantees:
	// 1. PRAGMAs set in configureConnection apply to ALL statements (SQLite
	//    pragmas are per-connection; with multiple pooled conns only the
	//    first one would be configured)
	// 2. One file never has competing write handles (prevents SQLITE_BUSY)
	db.SetMaxOpenConns(1)

	if err := p.configureConnection(db, readOnly); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to configure connection: %w", err)
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

	// Wait up to 10s for locks instead of failing immediately with SQLITE_BUSY.
	// Applies to both readers and writers - checkpoints and commits from other
	// handles (e.g. loader vs writer pools) briefly lock the same WAL files.
	_, err = conn.ExecContext(nil, "PRAGMA busy_timeout=10000")
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

// CheckpointAll runs a TRUNCATE WAL checkpoint on all open connections.
// Called periodically by the writer's background flusher (and on Close)
// to keep WAL files from growing unbounded.
func (p *ConnectionPool) CheckpointAll() {
	// Snapshot handles under lock, checkpoint outside it
	p.mu.RLock()
	dbs := make([]*sql.DB, 0, len(p.connections))
	for _, pc := range p.connections {
		dbs = append(dbs, pc.db)
	}
	p.mu.RUnlock()

	for _, db := range dbs {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		// busy_timeout on the connection handles transient locks
		db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
		cancel()
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

// Discard closes and forgets the connection for a file so the next checkout opens a new one.
func (p *ConnectionPool) Discard(filepath string) {
	if p == nil || filepath == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if pc, ok := p.connections[filepath]; ok {
		pc.db.Close()
		delete(p.connections, filepath)
	}
}

// Size returns current pool size
func (p *ConnectionPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.connections)
}
