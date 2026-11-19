package backfill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// record is the outcome of one ticker-day recovery, kept so a pass is not
// repeated and so a missing day is visible instead of silent.
type record struct {
	Source      string    `json:"source"`
	ArchiveDate string    `json:"archive_date,omitempty"`
	Scalars     int       `json:"scalars"`
	Ladders     int       `json:"ladders"`
	LadderBytes int64     `json:"ladder_bytes,omitempty"`
	At          time.Time `json:"at"`
	Skipped     bool      `json:"skipped,omitempty"`
	Reason      string    `json:"reason,omitempty"`
}

type stateFile struct {
	Dates map[string]map[string]record `json:"dates"`
}

// stateStore is the on-disk coverage ledger. It lives next to the day folders
// so moving the data folder moves the ledger with it.
type stateStore struct {
	mu   sync.Mutex
	path string
	data stateFile
}

func newStateStore(path string) *stateStore {
	store := &stateStore{path: path, data: stateFile{Dates: map[string]map[string]record{}}}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		var loaded stateFile
		if json.Unmarshal(raw, &loaded) == nil && loaded.Dates != nil {
			store.data = loaded
		}
	}
	return store
}

func (s *stateStore) record(date, ticker string) (record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byTicker, ok := s.data.Dates[date]
	if !ok {
		return record{}, false
	}
	rec, ok := byTicker[ticker]
	return rec, ok
}

func (s *stateStore) set(date, ticker string, rec record) {
	s.mu.Lock()
	byTicker, ok := s.data.Dates[date]
	if !ok {
		byTicker = make(map[string]record)
		s.data.Dates[date] = byTicker
	}
	byTicker[ticker] = rec
	s.mu.Unlock()
	s.save()
}

// save writes the ledger atomically and keeps the newest 45 sessions.
func (s *stateStore) save() {
	s.mu.Lock()
	dates := make([]string, 0, len(s.data.Dates))
	for date := range s.data.Dates {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	for len(dates) > 45 {
		delete(s.data.Dates, dates[0])
		dates = dates[1:]
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, raw, 0644); err != nil {
		return
	}
	if err := os.Rename(temp, s.path); err != nil {
		_ = os.Remove(temp)
	}
}
