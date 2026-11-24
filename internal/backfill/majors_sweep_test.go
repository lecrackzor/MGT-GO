package backfill

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"market-terminal/internal/api"
	"market-terminal/internal/config"
	"market-terminal/internal/database"
	"market-terminal/internal/utils"
)

// majorsHarness builds a manager whose level fill is a counter, so the sweep's
// decisions are visible without touching a network or a database.
func majorsHarness(t *testing.T, result MajorsResult, fail error) (*Manager, *[]string) {
	t.Helper()
	dir := t.TempDir()
	settings := &config.Settings{
		DataDirectory:       filepath.Join(dir, "Tickers"),
		EODBackfillEnabled:  true,
		CollectAllEndpoints: true,
		TickerConfigs: map[string]config.TickerConfig{
			"AAPL":   {CollectionEnabled: true},
			"SPX":    {CollectionEnabled: true},
			"ARCHVD": {CollectionEnabled: true, ArchiveOnly: true},
		},
	}
	writer := database.NewDataWriter(settings, func(string, string) {})
	t.Cleanup(func() { _ = writer.Close() })
	client := api.NewClient("test-key", func(string, string) {})
	t.Cleanup(func() { client.Close() })
	manager := New(client, writer, func() *config.Settings { return settings }, func(string, string) {})
	var calls []string
	manager.SetMajorLevelStore(func(ctx context.Context, ticker string, date time.Time) (MajorsResult, error) {
		calls = append(calls, date.Format("2006-01-02")+"|"+ticker)
		return result, fail
	})
	return manager, &calls
}

func TestSweepCoversEveryEnabledTickerAndSkipsWeekend(t *testing.T) {
	manager, calls := majorsHarness(t, MajorsResult{Filled: 5}, nil)

	// The sweep is relative to the current date, so assert on shape rather
	// than fixed dates: it must cover the past trading days only and never the
	// weekend, never today.
	manager.FillMajorsCatchUp(context.Background())

	if len(*calls) == 0 {
		t.Fatal("sweep made no calls")
	}
	et := utils.GetMarketTimezone()
	for _, call := range *calls {
		date, err := time.ParseInLocation("2006-01-02", call[:10], et)
		if err != nil {
			t.Fatalf("bad date in %q", call)
		}
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			t.Fatalf("swept a weekend day: %s", call)
		}
		now := time.Now().In(et)
		if date.Year() == now.Year() && date.YearDay() == now.YearDay() {
			t.Fatalf("swept the current session: %s", call)
		}
	}
	// Archive-only tickers are included: this fill does not touch the history
	// quota, so it does not need the live-only restriction.
	var sawArchiveOnly bool
	for _, call := range *calls {
		if len(call) > 11 && call[11:] == "ARCHVD" {
			sawArchiveOnly = true
			break
		}
	}
	if !sawArchiveOnly {
		t.Fatal("archive-only ticker was not swept")
	}
}

func TestSweepRecordsSettledDayAndDoesNotRepeatIt(t *testing.T) {
	manager, calls := majorsHarness(t, MajorsResult{Filled: 0, Pending: 0}, nil)
	manager.FillMajorsCatchUp(context.Background())
	first := len(*calls)
	if first == 0 {
		t.Fatal("first sweep made no calls")
	}

	// Second sweep inside the interval gate makes no calls at all.
	manager.FillMajorsCatchUp(context.Background())
	if len(*calls) != first {
		t.Fatalf("second sweep made %d extra call(s)", len(*calls)-first)
	}

	// Even with the gate bypassed (as the nightly roundup does), a settled day
	// is not requested again.
	manager.fillMajorsCatchUpLocked(context.Background())
	if len(*calls) != first {
		t.Fatalf("settled day was requested again: %d extra call(s)", len(*calls)-first)
	}
}

func TestSweepRetriesAPartialDayOnceMoreOnly(t *testing.T) {
	// A partial day is settled too: the feed only shrinks, so a second attempt
	// can only return less. It must not be retried forever.
	manager, calls := majorsHarness(t, MajorsResult{Filled: 3, Pending: 7, Samples: 100}, nil)
	manager.FillMajorsCatchUp(context.Background())
	first := len(*calls)
	manager.fillMajorsCatchUpLocked(context.Background())
	if len(*calls) != first {
		t.Fatalf("a partial day was requested again")
	}
}

func TestSweepLeavesAFailedDayUnsettled(t *testing.T) {
	manager, calls := majorsHarness(t, MajorsResult{}, context.DeadlineExceeded)
	manager.FillMajorsCatchUp(context.Background())
	first := len(*calls)
	if first == 0 {
		t.Fatal("first sweep made no calls")
	}
	// A transient failure must be retried, unlike a settled day.
	manager.fillMajorsCatchUpLocked(context.Background())
	if len(*calls) <= first {
		t.Fatal("a failed day was treated as settled and never retried")
	}
}

func TestSettledDayIsSkippedByTheArchiveFillToo(t *testing.T) {
	manager, calls := majorsHarness(t, MajorsResult{Filled: 1}, nil)
	manager.FillMajorsCatchUp(context.Background())
	first := len(*calls)
	if first == 0 {
		t.Fatal("first sweep made no calls")
	}
	// The archive pass fills with a store as well; a day the sweep just
	// settled must not be requested again through that path.
	et := utils.GetMarketTimezone()
	now := time.Now().In(et)
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, et).AddDate(0, 0, -1)
	for yesterday.Weekday() == time.Saturday || yesterday.Weekday() == time.Sunday {
		yesterday = yesterday.AddDate(0, 0, -1)
	}
	manager.fillTickerMajors(context.Background(), "AAPL", yesterday, manager.state(manager.settings()))
	if len(*calls) != first {
		t.Fatalf("a settled day was requested again by the archive fill")
	}
}

func TestMajorsStateSurvivesAReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store := newStateStore(path)
	store.setMajorsState("2026-11-23", "AAPL", majorsRecord{Reason: "complete", At: time.Now()})
	store.set("2026-11-23", "AAPL", record{Source: "eod"})

	reloaded := newStateStore(path)
	rec, ok := reloaded.majorsState("2026-11-23", "AAPL")
	if !ok || rec.Reason != "complete" {
		t.Fatalf("reloaded state = %+v, ok=%v", rec, ok)
	}
	if _, ok := reloaded.record("2026-11-23", "AAPL"); !ok {
		t.Fatal("the archive ledger was lost on reload")
	}
}
