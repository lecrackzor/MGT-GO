package main

import (
	"context"
	"sort"
	"time"

	"market-terminal/internal/api"
)

// The Ninja history feed answers a whole session per call, and the chart polls
// it every 1.5 s while only a few new bars exist. A cached session plus an
// append-only refresh turns that into one request a minute, and single-flight
// makes concurrent charts of the same ticker share one request.
const (
	majorsCacheTTL     = 60 * time.Second
	majorsNegativeTTL  = 10 * time.Minute
	majorsCacheMaxKeys = 64
)

type majorsEntry struct {
	samples    []api.HistMajor
	fetched    time.Time
	newest     int64
	negative   bool
	negativeAt time.Time
	flight     chan struct{}
}

// historicalMajors returns the session's major gamma samples, cached per
// ticker and date. A second chart of the same ticker never starts a second
// download, and a stale cache keeps a poll from blocking.
func (a *App) historicalMajors(ctx context.Context, apiKey, ticker string, date time.Time, start, end int64) ([]api.HistMajor, error) {
	key := ticker + "|" + date.Format("2006-01-02")

	a.majorsMu.Lock()
	if a.majorsCache == nil {
		a.majorsCache = make(map[string]*majorsEntry)
	}
	entry := a.majorsCache[key]
	if entry == nil {
		entry = &majorsEntry{}
		a.majorsCache[key] = entry
	}
	now := time.Now()
	if entry.negative && now.Sub(entry.negativeAt) < majorsNegativeTTL {
		a.majorsMu.Unlock()
		return nil, nil
	}
	if len(entry.samples) > 0 && now.Sub(entry.fetched) < majorsCacheTTL {
		cached := append([]api.HistMajor(nil), entry.samples...)
		a.majorsMu.Unlock()
		return cached, nil
	}
	if entry.flight != nil {
		// A refresh is already running. With data in hand, answer from the
		// cache instead of making this poll wait for the network.
		if len(entry.samples) > 0 {
			cached := append([]api.HistMajor(nil), entry.samples...)
			a.majorsMu.Unlock()
			return cached, nil
		}
		flight := entry.flight
		a.majorsMu.Unlock()
		select {
		case <-flight:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		a.majorsMu.Lock()
		entry = a.majorsCache[key]
		if entry == nil {
			a.majorsMu.Unlock()
			return nil, nil
		}
		cached := append([]api.HistMajor(nil), entry.samples...)
		err := a.majorsErr[key]
		a.majorsMu.Unlock()
		return cached, err
	}

	// Append-only refresh: only ask for what is newer than the cache.
	from := start
	if len(entry.samples) > 0 && entry.newest+1 > from {
		from = entry.newest + 1
	}
	flight := make(chan struct{})
	entry.flight = flight
	a.majorsMu.Unlock()

	var (
		samples []api.HistMajor
		err     error
	)
	if from <= end {
		samples, err = a.apiClient.FetchHistoricalMajors(ctx, apiKey, ticker, from, end)
	}

	a.majorsMu.Lock()
	entry = a.majorsCache[key]
	if entry == nil {
		entry = &majorsEntry{}
		a.majorsCache[key] = entry
	}
	if err != nil {
		// A ticker the feed does not carry is remembered for ten minutes; a
		// transient failure keeps the previous samples and retries next poll.
		if len(entry.samples) == 0 {
			entry.negative = true
			entry.negativeAt = now
		}
	} else {
		entry.negative = false
		entry.samples = mergeMajors(entry.samples, samples)
		entry.fetched = time.Now()
		if n := len(entry.samples); n > 0 {
			entry.newest = entry.samples[n-1].Timestamp
		}
	}
	if a.majorsErr == nil {
		a.majorsErr = make(map[string]error)
	}
	a.majorsErr[key] = err
	entry.flight = nil
	close(flight)
	a.pruneMajorsCacheLocked()
	cached := append([]api.HistMajor(nil), entry.samples...)
	a.majorsMu.Unlock()
	return cached, err
}

// pruneMajorsCacheLocked keeps the cache bounded. Entries still in flight are
// kept so their waiters can finish.
func (a *App) pruneMajorsCacheLocked() {
	if len(a.majorsCache) <= majorsCacheMaxKeys {
		return
	}
	var oldestKey string
	var oldest time.Time
	for key, entry := range a.majorsCache {
		if entry.flight != nil {
			continue
		}
		stamp := entry.fetched
		if stamp.IsZero() {
			stamp = entry.negativeAt
		}
		if oldestKey == "" || stamp.Before(oldest) {
			oldestKey, oldest = key, stamp
		}
	}
	if oldestKey != "" {
		delete(a.majorsCache, oldestKey)
		delete(a.majorsErr, oldestKey)
	}
}

// mergeMajors appends newer samples and drops any timestamp already held, so a
// refresh that overlaps the cache cannot duplicate a bar.
func mergeMajors(cached, fresh []api.HistMajor) []api.HistMajor {
	if len(cached) == 0 {
		out := append([]api.HistMajor(nil), fresh...)
		sortMajors(out)
		return out
	}
	last := cached[len(cached)-1].Timestamp
	out := append([]api.HistMajor(nil), cached...)
	for _, sample := range fresh {
		if sample.Timestamp > last {
			out = append(out, sample)
		}
	}
	sortMajors(out)
	return out
}

func sortMajors(samples []api.HistMajor) {
	sort.Slice(samples, func(i, j int) bool { return samples[i].Timestamp < samples[j].Timestamp })
}
