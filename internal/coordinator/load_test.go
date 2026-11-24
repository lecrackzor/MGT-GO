package coordinator

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"market-terminal/internal/api"
	"market-terminal/internal/config"
	"market-terminal/internal/database"
	"market-terminal/internal/scheduler"
)

// loadServer stands in for the Gexbot host and counts concurrent requests so
// the harness can report peak in-flight load. latency simulates a real network
// round-trip so the concurrency bound (not the in-process handler speed) is
// what the peak-in-flight number measures.
type loadServer struct {
	server   *httptest.Server
	latency  time.Duration
	requests atomic.Int64
	inFlight atomic.Int64
	peak     atomic.Int64
}

func newLoadServer(t *testing.T) *loadServer {
	ls := &loadServer{latency: 50 * time.Millisecond}
	ls.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := ls.inFlight.Add(1)
		defer ls.inFlight.Add(-1)
		for {
			peak := ls.peak.Load()
			if cur <= peak || ls.peak.CompareAndSwap(peak, cur) {
				break
			}
		}
		ls.requests.Add(1)
		time.Sleep(ls.latency)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"timestamp":1700000000,"ticker":"SPX","spot":6000.0,"zero_gamma":5990.0,"strikes":[[6000,1.0,2.0,[1.0,1.0,1.0,1.0]]]}`)
	}))
	t.Cleanup(ls.server.Close)
	return ls
}

func heapAllocBytes() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// TestFullSuiteLoadHarness drives the real fetch -> aggregate -> write pipeline
// against a local stand-in for Gexbot and reports the per-sweep load. It is a
// measurement, not an assertion: the numbers print with -v and are meant to be
// read before and after collection-load changes.
func TestFullSuiteLoadHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full-suite load harness in -short mode")
	}

	ls := newLoadServer(t)
	client := api.NewClient("test-key", nil)
	client.SetBaseURL(ls.server.URL)

	settings := &config.Settings{
		APISubscriptionTiers: []string{"classic", "state"},
		CollectAllEndpoints:  true,
		DataDirectory:        t.TempDir(),
		ProfileSampleSeconds: 1,
	}
	querySystem := api.NewQuerySystem(settings, client)
	dataWriter := database.NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = dataWriter.Close() })

	uas := scheduler.NewUnifiedAdaptiveScheduler(settings)

	dcc := NewDataCollectionCoordinator(
		querySystem,
		dataWriter,
		uas,
		nil,
		func() bool { return false },
		func() []interface{} { return nil },
		func(string, string) {},
	)

	// The throttled minimum interval the scheduler would enforce once a pool
	// limit is learned, shown for reference next to the burst numbers.
	rlt := scheduler.NewRateLimitTracker()
	rlt.RecordRequest("classic_zero", float64(time.Now().Unix()), map[string]string{"X-RateLimit-Limit": "60"})

	for _, n := range []int{8, 24, 48} {
		if n > len(config.KnownTickerList) {
			t.Fatalf("ticker count %d exceeds known list %d", n, len(config.KnownTickerList))
		}
		tickers := config.KnownTickerList[:n]
		dcc.UpdateEnabledTickers(tickers)

		before := ls.requests.Load()
		start := time.Now()
		dcc.ProcessTickerBatch(tickers)
		elapsed := time.Since(start)
		requests := ls.requests.Load() - before

		reqPerSec := float64(requests) / elapsed.Seconds()
		minGap := rlt.GetMinimumInterval(n, api.PlannedEndpoints(settings))

		t.Logf("tickers=%d sweep: %d requests in %.2fs = %.0f req/s, peak in-flight=%d, goroutines=%d, heap=%d KiB, throttled min interval=%.1fs",
			n, requests, elapsed.Seconds(), reqPerSec, ls.peak.Load(), runtime.NumGoroutine(), heapAllocBytes()/1024, minGap)
	}

	// Let the async write goroutines finish so the data writer closes cleanly
	// before t.TempDir removes its directory.
	time.Sleep(300 * time.Millisecond)
}
