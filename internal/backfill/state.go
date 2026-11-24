package backfill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	// Majors records the outcome of the historical-majors fill per
	// "date|ticker", so a settled session is not re-requested. It is separate
	// from Dates because the levels are an independent source with its own
	// failure modes: a session can be fully archived and still have no levels.
	Majors map[string]majorsRecord `json:"majors,omitempty"`
}

// majorsRecord is the last majors fill for one ticker-day.
type majorsRecord struct {
	// Reason is "complete" (nothing left to fill), "partial" (the feed only
	// covered part of the session) or "unavailable" (the feed no longer
	// serves it). Any of them settles the day: the feed only shrinks, so a
	// second attempt can only return less.
	Reason  string    `json:"reason"`
	Pending int       `json:"pending,omitempty"`
	At      time.Time `json:"at"`
}

// stateStore is the on-disk coverage ledger. It lives next to the day folders
// so moving the data folder moves the ledger with it.
type stateStore struct {
	mu   sync.Mutex
	path string
	data stateFile
}

func newStateStore(path string) *stateStore {
	store := &stateStore{path: path, data: stateFile{Dates: map[string]map[string]record{}, Majors: map[string]majorsRecord{}}}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		var loaded stateFile
		if json.Unmarshal(raw, &loaded) == nil && loaded.Dates != nil {
			if loaded.Majors == nil {
				loaded.Majors = map[string]majorsRecord{}
			}
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

func majorsKey(date, ticker string) string {
	return date + "|" + ticker
}

// majorsState returns the recorded outcome for one ticker-day, if any.
func (s *stateStore) majorsState(date, ticker string) (majorsRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.data.Majors[majorsKey(date, ticker)]
	return rec, ok
}

// setMajorsState records that a ticker-day needs no further majors request.
func (s *stateStore) setMajorsState(date, ticker string, rec majorsRecord) {
	s.mu.Lock()
	if s.data.Majors == nil {
		s.data.Majors = map[string]majorsRecord{}
	}
	s.data.Majors[majorsKey(date, ticker)] = rec
	s.mu.Unlock()
	s.save()
}

// forgetMajorsState drops a record so the next sweep looks at the day again.
// Used when a fill failed transiently and must not be treated as settled.
func (s *stateStore) forgetMajorsState(date, ticker string) {
	s.mu.Lock()
	delete(s.data.Majors, majorsKey(date, ticker))
	s.mu.Unlock()
	s.save()
}

// save writes the ledger atomically and keeps the newest 45 sessions.
func (s *stateStore) save() {
	s.mu.Lock()
	// The window is the union of both maps' dates: a majors record can exist
	// for a session the archive ledger has no entry for, and pruning on the
	// archive dates alone would delete it right after it was written.
	seen := make(map[string]bool, len(s.data.Dates)+len(s.data.Majors))
	for date := range s.data.Dates {
		seen[date] = true
	}
	for key := range s.data.Majors {
		if i := strings.IndexByte(key, '|'); i > 0 {
			seen[key[:i]] = true
		}
	}
	dates := make([]string, 0, len(seen))
	for date := range seen {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	kept := make(map[string]bool, len(dates))
	for len(dates) > 45 {
		kept[dates[0]] = false
		delete(seen, dates[0])
		dates = dates[1:]
	}
	for _, date := range dates {
		kept[date] = true
	}
	for date := range s.data.Dates {
		if !kept[date] {
			delete(s.data.Dates, date)
		}
	}
	for key := range s.data.Majors {
		if i := strings.IndexByte(key, '|'); i <= 0 || !kept[key[:i]] {
			delete(s.data.Majors, key)
		}
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
