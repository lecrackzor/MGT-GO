package database

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A profile flush still in flight while a chart window reads must not lose the
// sample. The flush reads the last stored ladder and then inserts, so the write
// arrives inside a transaction that has already read. With a deferred BEGIN,
// SQLite rejects that upgrade immediately (stale snapshot) instead of waiting,
// which dropped whole charm_one ladders in the 2026-11-18 log.
func TestWriteTransactionWaitsForConcurrentWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Tickers 11.18.2026", "SPY.profiles.db")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}

	first := NewConnectionPool(1, time.Minute, time.Minute)
	defer first.Close()
	second := NewConnectionPool(1, time.Minute, time.Minute)
	defer second.Close()

	writer, err := first.GetConnection(path, false)
	if err != nil {
		t.Fatalf("open first handle: %v", err)
	}
	if _, err := writer.Exec(`CREATE TABLE endpoint_profile (
		timestamp REAL, endpoint TEXT, payload BLOB,
		PRIMARY KEY(timestamp, endpoint)) WITHOUT ROWID`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	reader, err := second.GetConnection(path, false)
	if err != nil {
		t.Fatalf("open second handle: %v", err)
	}

	held, err := writer.Begin()
	if err != nil {
		t.Fatalf("begin held transaction: %v", err)
	}
	if _, err := held.Exec(`INSERT INTO endpoint_profile VALUES (1, 'classic', x'00')`); err != nil {
		t.Fatalf("insert while holding the lock: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		time.Sleep(250 * time.Millisecond)
		done <- held.Commit()
	}()

	tx, err := reader.Begin()
	if err != nil {
		t.Fatalf("begin second transaction: %v", err)
	}
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM endpoint_profile`).Scan(&count); err != nil {
		tx.Rollback()
		t.Fatalf("read before write: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO endpoint_profile VALUES (2, 'classic', x'00')`); err != nil {
		tx.Rollback()
		t.Fatalf("write after a read did not wait for the other writer: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit second transaction: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("commit held transaction: %v", err)
	}

	var rows int
	if err := writer.QueryRow(`SELECT count(*) FROM endpoint_profile`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("stored rows = %d, want 2", rows)
	}
}
