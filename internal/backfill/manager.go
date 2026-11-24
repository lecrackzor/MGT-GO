package backfill

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"market-terminal/internal/api"
	"market-terminal/internal/config"
	"market-terminal/internal/database"
	"market-terminal/internal/utils"
)

// Report is the outcome of one recovery pass.
type Report struct {
	Phase       string    `json:"phase"`
	Date        string    `json:"date"`
	Running     bool      `json:"running"`
	Tickers     int       `json:"tickers"`
	Total       int       `json:"total,omitempty"`
	Requests    int       `json:"requests"`
	Scalars     int       `json:"scalars"`
	Ladders     int       `json:"ladders"`
	LadderBytes int64     `json:"ladder_bytes"`
	Skipped     []string  `json:"skipped,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	// Majors counts bars that gained a historical major level during the pass.
	Majors int `json:"majors"`
}

// Manager runs both recovery passes. It never assumes a Quant key: every
// category it requests is one the account's subscription already includes.
type Manager struct {
	client     *api.Client
	writer     *database.DataWriter
	settingsFn func() *config.Settings
	logf       func(string, string)

	mu       sync.Mutex
	running  bool
	report   Report
	states   map[string]*stateStore
	lastPass map[string]time.Time

	// rateLimitedUntil blocks intraday sweeps until this time after a 429,
	// so the periodic sweep backs off instead of hammering the API.
	rateLimitedUntil time.Time

	// intradayDenied is set once when the intraday history route answers 401
	// or 403: the account cannot use it, so the sweep stops for the session
	// instead of logging an error every interval.
	intradayDenied bool

	// storeMajors, when set, writes the session's historical major gamma
	// levels for one ticker. The archive pass runs it for every enabled
	// ticker, including archive-only ones that are never polled live and may
	// never have a chart opened.
	storeMajors func(ctx context.Context, ticker string, date time.Time) (MajorsResult, error)

	// majorsDryRun suppresses ledger writes during a sweep, so a dry run can
	// walk the same code path without settling days it did not write.
	majorsDryRun bool
}

// MajorsResult is the outcome of one historical-majors fill for a ticker-day.
// The caller uses it to decide whether the day is settled: the feed only
// shrinks, so a day with no samples left will never gain a level.
type MajorsResult struct {
	Filled  int // timestamps that gained a level
	Pending int // timestamps that still have none afterwards
	Samples int // samples the feed returned for the session
}

// majorsLookbackDays bounds the catch-up sweep. The feed keeps roughly a four
// day rolling window; asking for more only spends requests on empty answers.
const majorsLookbackDays = 4

// MajorsLookbackDays exposes the sweep's default window.
func MajorsLookbackDays() int { return majorsLookbackDays }

// majorsSweepInterval keeps the catch-up sweep from running on every startup.
const majorsSweepInterval = 6 * time.Hour

// New builds a manager. A nil client or writer disables every pass, so the app
// can run without recovery while the rest of the terminal keeps working.
func New(client *api.Client, writer *database.DataWriter, settingsFn func() *config.Settings, logf func(string, string)) *Manager {
	return &Manager{
		client:     client,
		writer:     writer,
		settingsFn: settingsFn,
		logf:       logf,
		states:     make(map[string]*stateStore),
		lastPass:   make(map[string]time.Time),
	}
}

func (m *Manager) settings() *config.Settings {
	if m == nil || m.settingsFn == nil {
		return nil
	}
	return m.settingsFn()
}

// SetMajorLevelStore installs the callback the recovery passes use to fill the
// historical major gamma levels for the rows they recover. Without it the
// passes still recover chart rows; only the level fill is skipped.
func (m *Manager) SetMajorLevelStore(fn func(ctx context.Context, ticker string, date time.Time) (MajorsResult, error)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.storeMajors = fn
	m.mu.Unlock()
}

// majorLevelStore returns the installed callback under the lock.
func (m *Manager) majorLevelStore() func(ctx context.Context, ticker string, date time.Time) (MajorsResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storeMajors
}

// SetMajorsDryRun makes a sweep report what it would do without recording the
// outcome. The ledger stays untouched, so a dry run cannot settle a day.
func (m *Manager) SetMajorsDryRun(on bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.majorsDryRun = on
	m.mu.Unlock()
}

func (m *Manager) majorsIsDryRun() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.majorsDryRun
}

func (m *Manager) log(category, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	if m.logf != nil {
		m.logf(message, category)
		return
	}
	log.Printf("[backfill] %s", message)
}

// Status returns the current or last pass.
func (m *Manager) Status() Report {
	if m == nil {
		return Report{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	report := m.report
	report.Running = m.running
	return report
}

// publishProgress snapshots a running pass into m.report so Status() and the
// log window's progress bar see live counters, not the zeroed report begin()
// installed. It copies only the scalar fields the UI reads; the Skipped list is
// left to finish().
func (m *Manager) publishProgress(phase, date string, started time.Time, done, total, requests, scalars, ladders int, ladderBytes int64) {
	m.mu.Lock()
	m.report = Report{
		Phase:       phase,
		Date:        date,
		Running:     true,
		Tickers:     done,
		Total:       total,
		Requests:    requests,
		Scalars:     scalars,
		Ladders:     ladders,
		LadderBytes: ladderBytes,
		StartedAt:   started,
	}
	m.mu.Unlock()
}

func (m *Manager) begin(phase, date string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return false
	}
	m.running = true
	m.report = Report{Phase: phase, Date: date, Running: true, StartedAt: time.Now()}
	return true
}

func (m *Manager) finish(report Report) Report {
	report.FinishedAt = time.Now()
	report.Running = false
	m.mu.Lock()
	m.report = report
	m.running = false
	m.mu.Unlock()
	return report
}

// enabled reports whether a pass can run at all.
func (m *Manager) enabled(settings *config.Settings) bool {
	return m != nil && settings != nil && settings.EODBackfillEnabled && m.client != nil && m.writer != nil
}

// tickers returns the tickers the user enabled, in a stable order. The
// scheduler owns the same list, so recovery never invents a ticker.
func (m *Manager) tickers(settings *config.Settings) []string {
	tickers := config.GetEnabledTickers(settings.TickerConfigs)
	sort.Strings(tickers)
	return tickers
}

// liveTickers returns only the tickers polled live, excluding archive-only
// tickers. The intraday gap fill uses the quota-limited history route, so an
// archive-only ticker must not spend that quota: the EOD archive covers it.
func (m *Manager) liveTickers(settings *config.Settings) []string {
	tickers := config.GetLiveTickers(settings.TickerConfigs)
	sort.Strings(tickers)
	return tickers
}

// endpoints is the exact endpoint set the account can call, from the same tier
// mapping the live collector uses.
func (m *Manager) endpoints(settings *config.Settings) []string {
	if settings.CollectAllEndpoints {
		return api.GetEndpointsForTiers(settings.APISubscriptionTiers)
	}
	return api.GetChartEndpointsForTiers(settings.APISubscriptionTiers)
}

// intOr reads a positive setting, else the fallback.
func intOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

// CatchUp recovers the most recent completed session that is not in the ledger
// yet. The archive serves exactly one report — the latest session whose 17:00
// ET export exists — so exactly one date is attempted, never two.
func (m *Manager) CatchUp(ctx context.Context) {
	settings := m.settings()
	if !m.enabled(settings) {
		return
	}
	location := utils.GetMarketTimezone()
	now := time.Now().In(location)
	marketDate := utils.GetMarketDate()

	// The archive for the market date exists only from 17:00 ET. Once it is
	// out, it is the latest report, and any older session has already rolled
	// off — attempting the day before would download the same archive once per
	// ticker just to discover it has moved past that session.
	archiveOpen := time.Date(marketDate.Year(), marketDate.Month(), marketDate.Day(), 17, 0, 0, 0, location)
	if !now.Before(archiveOpen) {
		m.RunEOD(ctx, marketDate)
		return
	}
	// Before the close, the latest report is still the previous trading day.
	previous := utils.GetLastTradingDay(marketDate.AddDate(0, 0, -1))
	if !previous.Equal(marketDate) {
		m.RunEOD(ctx, previous)
	}
}

// RunEODIfDue runs the archive pass once the configured time has passed. It
// retries a partial pass at most every fifteen minutes and never starts after
// 19:00 ET, so the app can quit instead of waiting.
func (m *Manager) RunEODIfDue(ctx context.Context, now time.Time) {
	settings := m.settings()
	if !m.enabled(settings) {
		return
	}
	now = now.In(utils.GetMarketTimezone())
	if now.Hour() >= 19 {
		return
	}
	hour, minute := parseClock(settings.EODBackfillTime, 17, 15)
	if now.Hour()*60+now.Minute() < hour*60+minute {
		return
	}
	date := utils.GetMarketDate()
	if !m.markPass(date.Format("2006-01-02"), 15*time.Minute) {
		return
	}
	m.RunEOD(ctx, date)
}

func (m *Manager) markPass(date string, interval time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.lastPass[date]; ok && time.Since(last) < interval {
		return false
	}
	m.lastPass[date] = time.Now()
	return true
}

// FillToday closes today's gaps from the intraday history route. It runs right
// after the close and stops on a rate limit, leaving the archive pass to cover
// whatever is left.
func (m *Manager) FillToday(ctx context.Context) Report {
	_, closeAt := sessionBounds(utils.GetMarketDate())
	return m.fillToday(ctx, closeAt)
}

// fillToday sweeps today's gaps up to end (Unix seconds). end is the session
// close at quit, or "now" for an intraday sweep, so a sweep never requests the
// not-yet-elapsed remainder of the session.
func (m *Manager) fillToday(ctx context.Context, end float64) Report {
	settings := m.settings()
	if !m.enabled(settings) {
		return Report{}
	}
	date := utils.GetMarketDate()
	dateStr := date.Format("2006-01-02")
	if !m.begin("intraday", dateStr) {
		return m.Status()
	}
	report := Report{Phase: "intraday", Date: dateStr, StartedAt: time.Now()}
	tickers := m.liveTickers(settings)
	report.Total = len(tickers)
	m.log("backfill", "intraday gap fill %s starting: %d ticker(s)", dateStr, len(tickers))
	m.publishProgress("intraday", dateStr, report.StartedAt, 0, len(tickers), 0, 0, 0, 0)

	for _, ticker := range tickers {
		if ctx.Err() != nil {
			break
		}
		report.Tickers++
		if err := m.fillTickerIntraday(ctx, settings, ticker, date, end, &report); err != nil {
			if isSubscriptionDenied(err) {
				// The intraday route is not on this account's plan. Stop the
				// sweep, don't mark it as an error, and don't retry it again.
				m.log("backfill", "intraday history is not authorized for this account (%s); archive pass still runs", err)
				m.mu.Lock()
				m.intradayDenied = true
				m.mu.Unlock()
				break
			}
			report.Skipped = append(report.Skipped, ticker+": "+err.Error())
			report.LastError = err.Error()
			m.log("backfill", "%s history gap fill for %s: %v", ticker, dateStr, err)
			if isRateLimited(err) {
				m.backOff(err)
				break
			}
		}
		m.publishProgress("intraday", dateStr, report.StartedAt, report.Tickers, len(tickers), report.Requests, report.Scalars, report.Ladders, report.LadderBytes)
	}
	m.log("backfill", "intraday gap fill %s: %d tickers, %d requests, %d rows, %d ladders, %d major levels",
		dateStr, report.Tickers, report.Requests, report.Scalars, report.Ladders, report.Majors)
	return m.finish(report)
}

// RunIntradayIfDue fills today's gaps on an interval while the market is open.
// It is the autonomous counterpart to FillToday (which runs at quit): each sweep
// is small, bounded by IntradayFillMaxRequestsPerCategory, and a rate limit pauses
// the sweep for the Retry-After window so the live poll is not starved.
func (m *Manager) RunIntradayIfDue(ctx context.Context, now time.Time) {
	settings := m.settings()
	if !m.enabled(settings) || !settings.IntradayBackfillEnabled {
		return
	}
	now = now.In(utils.GetMarketTimezone())
	if !utils.IsMarketOpenAt(now) {
		return
	}
	m.mu.Lock()
	backoff := m.rateLimitedUntil
	denied := m.intradayDenied
	m.mu.Unlock()
	if denied || now.Before(backoff) {
		return
	}
	interval := time.Duration(intOr(settings.IntradayBackfillIntervalMinutes, 30)) * time.Minute
	if !m.markPass("intraday-"+now.Format("2006-01-02"), interval) {
		return
	}
	// Bound the sweep to the current moment so it only recovers elapsed gaps.
	m.fillToday(ctx, float64(now.Unix()))
}

// backOff pauses the intraday sweep after a rate limit. Retry-After wins when
// the API sent one; otherwise a five minute cool-down applies.
func (m *Manager) backOff(err error) {
	var limited *api.RateLimitError
	if !errors.As(err, &limited) {
		return
	}
	delay := 5 * time.Minute
	if seconds, convErr := strconv.Atoi(strings.TrimSpace(limited.RetryAfter)); convErr == nil && seconds > 0 {
		if seconds > 900 {
			seconds = 900
		}
		delay = time.Duration(seconds) * time.Second
	}
	m.mu.Lock()
	m.rateLimitedUntil = time.Now().Add(delay)
	m.mu.Unlock()
	m.log("backfill", "rate limited on intraday sweep; backing off for %v", delay)
}

func (m *Manager) fillTickerIntraday(ctx context.Context, settings *config.Settings, ticker string, date time.Time, end float64, report *Report) error {
	chartPath := database.DBPath(settings.DataDirectory, ticker, date, nil, "backfill")
	if chartPath == "" {
		return nil
	}
	open, _ := sessionBounds(date)
	closeAt := end
	minGap := float64(intOr(settings.IntradayFillMinGapSeconds, 45))
	gaps, err := missingRanges(chartPath, open, closeAt, minGap)
	if err != nil {
		return err
	}
	if len(gaps) == 0 {
		// No missing bars to recover, but a bar can exist without its major
		// level. Top those up anyway; the fill only touches empty columns.
		report.Majors += m.fillTickerMajors(ctx, ticker, date, nil)
		return nil
	}
	gaps = splitGaps(gaps, 8*3600)
	cadence := database.ProfileSampleSeconds(settings)
	profilePath := database.ProfileDBPath(chartPath)
	budget := settings.IntradayFillMaxRequestsPerCategory

	scalars := make(map[float64]map[string]interface{})
	scalarRanks := make(map[float64]map[string]int)
	fills := make([]database.LadderFill, 0)
	lastFill := make(map[string]float64)

	for _, category := range api.HistoryCategoriesForEndpoints(m.endpoints(settings)) {
		live, err := liveProfileTimestamps(profilePath, category.Endpoint, open, closeAt)
		if err != nil {
			return err
		}
		requests := 0
		for _, span := range gaps {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if budget > 0 && requests >= budget {
				break
			}
			samples, err := m.client.FetchIntradayHistory(ctx, ticker, category.Package, category.Category, int64(span.start), int64(span.end))
			report.Requests++
			requests++
			if err != nil {
				return err
			}
			if len(samples) == 0 {
				continue
			}
			for _, sample := range samples {
				if sample.Timestamp < open || sample.Timestamp > closeAt {
					continue
				}
				rowScalars, rowProfiles := database.SplitBackfillSample(sample.Payload)
				mergeScalars(scalars, scalarRanks, sample.Timestamp, category.Endpoint, rowScalars)
				if !config.KeepProfileEndpoint(settings, category.Endpoint) {
					continue
				}
				payload := database.BackfillLadderPayload(rowProfiles)
				if payload == nil || covered(live, sample.Timestamp, cadence) {
					continue
				}
				if last, ok := lastFill[category.Endpoint]; ok && sample.Timestamp-last < cadence-0.05 {
					continue
				}
				lastFill[category.Endpoint] = sample.Timestamp
				fills = append(fills, database.LadderFill{
					Timestamp: sample.Timestamp,
					Endpoint:  category.Endpoint,
					Payload:   payload,
				})
			}
		}
	}

	if err := m.writeRecovered(ticker, date, settings, scalars, fills, report); err != nil {
		return err
	}
	report.Majors += m.fillTickerMajors(ctx, ticker, date, nil)
	return nil
}

// fillTickerMajors runs the historical majors fill for one ticker and day and
// returns how many timestamps gained a level. A failure here never fails the
// pass: the levels are an addition to the recovered rows, not the recovery
// itself.
//
// A non-nil store records the outcome. Only a completed past session should be
// passed a store: the current session still gains rows, so settling it would
// stop the next sweep from covering them.
func (m *Manager) fillTickerMajors(ctx context.Context, ticker string, date time.Time, store *stateStore) int {
	fill := m.majorLevelStore()
	if fill == nil || ctx.Err() != nil {
		return 0
	}
	dateStr := date.Format("2006-01-02")
	if store != nil && !m.majorsIsDryRun() {
		// A settled day is not asked for again. The feed only shrinks, so a
		// second request returns the same coverage or less; the remaining
		// empty rows are ones it never served (pre-market, or pruned).
		if _, settled := store.majorsState(dateStr, ticker); settled {
			return 0
		}
	}
	res, err := fill(ctx, ticker, date)
	if err != nil {
		m.log("backfill", "major levels for %s on %s: %v", ticker, dateStr, err)
		if store != nil && !m.majorsIsDryRun() {
			// Not settled: a transient failure must be retried, unlike a
			// session the feed genuinely no longer serves.
			store.forgetMajorsState(dateStr, ticker)
		}
		return 0
	}
	if store != nil && !m.majorsIsDryRun() {
		reason := "complete"
		switch {
		case res.Samples == 0 && res.Pending > 0:
			reason = "unavailable"
		case res.Pending > 0:
			reason = "partial"
		}
		store.setMajorsState(dateStr, ticker, majorsRecord{
			Reason:  reason,
			Pending: res.Pending,
			At:      time.Now(),
		})
	}
	return res.Filled
}

// FillMajorsCatchUp fills historical major levels for every enabled ticker on
// any past session that still has empty rows, at most once per interval. Used
// at startup, where a backlog is taken while the feed still serves it.
func (m *Manager) FillMajorsCatchUp(ctx context.Context) int {
	settings := m.settings()
	if !m.enabled(settings) || m.majorLevelStore() == nil {
		return 0
	}
	if !m.markPass("majors-catchup-"+utils.GetMarketDate().Format("2006-01-02"), majorsSweepInterval) {
		return 0
	}
	return m.SweepMajors(ctx, majorsLookbackDays)
}

// fillMajorsCatchUpLocked is the sweep for a caller that already holds the
// pass (the nightly archive roundup), so it does not need the interval gate.
func (m *Manager) fillMajorsCatchUpLocked(ctx context.Context) int {
	if !m.enabled(m.settings()) || m.majorLevelStore() == nil {
		return 0
	}
	return m.SweepMajors(ctx, majorsLookbackDays)
}

// SweepMajors fills levels for every enabled ticker on each past session within
// the last `days` calendar days that the ledger has not settled, and reports how
// many timestamps gained a value. Weekends are skipped and the current session
// is never touched: it is still gaining rows and the live fill covers it.
//
// A session is settled once it is complete, partial, or no longer served. The
// feed only shrinks, so re-asking can only return less; only a failed request
// leaves the day unsettled for the next sweep.
func (m *Manager) SweepMajors(ctx context.Context, days int) int {
	settings := m.settings()
	if !m.enabled(settings) || m.majorLevelStore() == nil || days <= 0 {
		return 0
	}
	store := m.state(settings)
	tickers := m.tickers(settings)
	et := utils.GetMarketTimezone()
	now := time.Now().In(et)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, et)

	total := 0
	checked := 0
	for offset := 1; offset <= days; offset++ {
		day := today.AddDate(0, 0, -offset)
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		dateStr := day.Format("2006-01-02")
		for _, ticker := range tickers {
			if ctx.Err() != nil {
				return total
			}
			if _, settled := store.majorsState(dateStr, ticker); settled {
				continue
			}
			checked++
			total += m.fillTickerMajors(ctx, ticker, day, store)
		}
	}
	if checked > 0 {
		m.log("backfill", "major levels catch-up: %d ticker-day(s) checked, %d timestamp(s) filled",
			checked, total)
	}
	return total
}

// RunEOD recovers one session from the end-of-day archive. It is the authority
// for a day: the archive replaces nothing, it only adds what is missing.
func (m *Manager) RunEOD(ctx context.Context, date time.Time) Report {
	settings := m.settings()
	if !m.enabled(settings) {
		return Report{}
	}
	dateStr := date.Format("2006-01-02")
	if !m.begin("eod", dateStr) {
		return m.Status()
	}
	store := m.state(settings)
	queue := m.tickers(settings)
	report := Report{Phase: "eod", Date: dateStr, StartedAt: time.Now(), Total: len(queue)}
	m.log("backfill", "archive pass %s starting: %d ticker(s) to check", dateStr, len(queue))
	m.publishProgress("eod", dateStr, report.StartedAt, 0, len(queue), 0, 0, 0, 0)

	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan string)
	for worker := 0; worker < 2; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ticker := range jobs {
				if ctx.Err() != nil {
					continue
				}
				scalars, ladders, bytes, requests, err := m.recoverTickerEOD(ctx, settings, store, ticker, date)
				// The majors feed is independent of the archive, so this runs
				// even when the ledger already considers the day settled: a day
				// recovered before this fill existed still has empty columns.
				majors := m.fillTickerMajors(ctx, ticker, date, store)
				mu.Lock()
				report.Majors += majors
				report.Tickers++
				report.Requests += requests
				report.Scalars += scalars
				report.Ladders += ladders
				report.LadderBytes += bytes
				done := report.Tickers
				if err != nil {
					report.Skipped = append(report.Skipped, ticker+": "+err.Error())
					report.LastError = err.Error()
				}
				m.publishProgress("eod", dateStr, report.StartedAt, done, len(queue), report.Requests, report.Scalars, report.Ladders, report.LadderBytes)
				mu.Unlock()

				if err != nil {
					m.log("backfill", "archive pass %s: %s failed [%d/%d]%s: %v", dateStr, ticker, done, len(queue), etaSuffix(report.StartedAt, done, len(queue)), err)
				} else if scalars > 0 || ladders > 0 {
					m.log("backfill", "archive pass %s: %s recovered %d scalars, %d ladders (%d KB) [%d/%d]%s", dateStr, ticker, scalars, ladders, bytes/1024, done, len(queue), etaSuffix(report.StartedAt, done, len(queue)))
				} else {
					m.log("backfill", "archive pass %s: %s already recovered, skipped [%d/%d]%s", dateStr, ticker, done, len(queue), etaSuffix(report.StartedAt, done, len(queue)))
				}
			}
		}()
	}
	for _, ticker := range queue {
		jobs <- ticker
	}
	close(jobs)
	wg.Wait()

	m.log("backfill", "archive pass %s: %d tickers, %d scalars, %d ladders (%d KB), %d major levels",
		dateStr, report.Tickers, report.Scalars, report.Ladders, report.LadderBytes/1024, report.Majors)
	// The archive only covers its own day. Older sessions that still have
	// empty level columns are picked up here, while the feed still serves
	// them, and each one is settled in the ledger so it is not asked again.
	m.fillMajorsCatchUpLocked(ctx)
	return m.finish(report)
}

func (m *Manager) recoverTickerEOD(ctx context.Context, settings *config.Settings, store *stateStore, ticker string, date time.Time) (int, int, int64, int, error) {
	dateStr := date.Format("2006-01-02")
	if rec, ok := store.record(dateStr, ticker); ok && rec.Source == "eod" {
		// A ledger record means this ticker-day is settled: either recovered
		// successfully, or permanently skipped because the archive moved past
		// this session. Neither is worth another download.
		return 0, 0, 0, 0, nil
	}
	if ctx.Err() != nil {
		return 0, 0, 0, 0, ctx.Err()
	}

	open, closeAt := sessionBounds(date)
	cadence := database.ProfileSampleSeconds(settings)
	profilePath := database.ProfileDBPath(database.DBPath(settings.DataDirectory, ticker, date, nil, "backfill"))

	scalars := make(map[float64]map[string]interface{})
	scalarRanks := make(map[float64]map[string]int)
	fills := make(map[string][]database.LadderFill)
	live := make(map[string][]float64)
	lastFill := make(map[string]float64)

	visit := func(endpoint string, sample api.HistorySample) error {
		if sample.Timestamp < open || sample.Timestamp > closeAt {
			return nil
		}
		rowScalars, rowProfiles := database.SplitBackfillSample(sample.Payload)
		mergeScalars(scalars, scalarRanks, sample.Timestamp, endpoint, rowScalars)
		if endpoint == "" || !config.KeepProfileEndpoint(settings, endpoint) {
			return nil
		}
		payload := database.BackfillLadderPayload(rowProfiles)
		if payload == nil {
			return nil
		}
		timestamps, known := live[endpoint]
		if !known {
			timestamps, _ = liveProfileTimestamps(profilePath, endpoint, open, closeAt)
			live[endpoint] = timestamps
		}
		if covered(timestamps, sample.Timestamp, cadence) {
			return nil
		}
		if last, ok := lastFill[endpoint]; ok && sample.Timestamp-last < cadence-0.05 {
			return nil
		}
		lastFill[endpoint] = sample.Timestamp
		fills[endpoint] = append(fills[endpoint], database.LadderFill{
			Timestamp: sample.Timestamp,
			Endpoint:  endpoint,
			Payload:   payload,
		})
		return nil
	}

	archiveDate, err := m.client.ReadEODArchive(ctx, ticker, visit)
	if err != nil {
		if isRateLimited(err) {
			return 0, 0, 0, 1, err
		}
		// A 404 or 503 can be transient, so no ledger entry is written and the
		// next scheduled pass retries.
		return 0, 0, 0, 1, err
	}
	if archiveDate != "" && archiveDate != dateStr {
		want, wantErr := time.Parse("2006-01-02", dateStr)
		got, gotErr := time.Parse("2006-01-02", archiveDate)
		switch {
		case wantErr == nil && gotErr == nil && got.Before(want):
			// The server has not published this session yet. Retry later.
			return 0, 0, 0, 1, fmt.Errorf("archive still serves %s", archiveDate)
		case wantErr == nil && gotErr == nil && got.After(want):
			// The window closed. This day cannot be recovered without a Quant
			// key, so record it once instead of burning requests.
			store.set(dateStr, ticker, record{Source: "eod", ArchiveDate: archiveDate, At: time.Now(), Skipped: true, Reason: "archive moved past this session"})
			return 0, 0, 0, 1, fmt.Errorf("archive is for %s", archiveDate)
		}
	}

	report := &Report{}
	var flat []database.LadderFill
	endpoints := make([]string, 0, len(fills))
	for endpoint := range fills {
		endpoints = append(endpoints, endpoint)
	}
	sort.Strings(endpoints)
	for _, endpoint := range endpoints {
		flat = append(flat, fills[endpoint]...)
	}
	if err := m.writeRecovered(ticker, date, settings, scalars, flat, report); err != nil {
		return report.Scalars, report.Ladders, report.LadderBytes, 1, err
	}
	store.set(dateStr, ticker, record{
		Source:      "eod",
		ArchiveDate: dateStr,
		Scalars:     report.Scalars,
		Ladders:     report.Ladders,
		LadderBytes: report.LadderBytes,
		At:          time.Now(),
	})
	return report.Scalars, report.Ladders, report.LadderBytes, 1, nil
}

// writeRecovered writes the chart rows first, then the ladders into the
// additive fill table. Neither write can change a live row or a live chain.
func (m *Manager) writeRecovered(ticker string, date time.Time, settings *config.Settings, scalars map[float64]map[string]interface{}, fills []database.LadderFill, report *Report) error {
	rows := flattenScalars(scalars)
	if len(rows) > 0 {
		written, err := m.writer.WriteBackfillScalars(ticker, date, rows)
		if err != nil {
			return fmt.Errorf("chart rows: %w", err)
		}
		report.Scalars += written
	}
	if len(fills) > 0 {
		written, bytes, err := m.writer.WriteFillLadders(ticker, date, fills, settings.EODMaxLadderMB)
		if err != nil {
			return fmt.Errorf("ladders: %w", err)
		}
		report.Ladders += written
		report.LadderBytes += bytes
	}
	return nil
}

func (m *Manager) state(settings *config.Settings) *stateStore {
	path := filepath.Join(database.DataRoot(settings.DataDirectory), "eod-state.json")
	m.mu.Lock()
	defer m.mu.Unlock()
	if store, ok := m.states[path]; ok {
		return store
	}
	store := newStateStore(path)
	m.states[path] = store
	return store
}

func mergeScalars(into map[float64]map[string]interface{}, ranks map[float64]map[string]int, ts float64, endpoint string, scalars map[string]interface{}) {
	if len(scalars) == 0 {
		return
	}
	row, ok := into[ts]
	if !ok {
		row = make(map[string]interface{}, len(scalars))
		into[ts] = row
	}
	rowRanks := ranks[ts]
	if rowRanks == nil {
		rowRanks = make(map[string]int)
		ranks[ts] = rowRanks
	}
	for key, value := range scalars {
		// Shared chart columns (zero_gamma, spot, major levels) are owned by a
		// specific endpoint, lowest rank winning. The archive visits classic and
		// state files for the same timestamp, and the state model reports these
		// as 0, so a plain merge would let state erase the classic value. An
		// empty endpoint (the non-zip stream fallback) keeps last-write-wins.
		if endpoint != "" {
			if owners, owned := api.ChartScalarSource(key); owned {
				rank, allowed := owners[endpoint]
				if !allowed {
					continue
				}
				if prev, seen := rowRanks[key]; seen && rank >= prev {
					continue
				}
				rowRanks[key] = rank
			}
		}
		row[key] = value
	}
}

func flattenScalars(rows map[float64]map[string]interface{}) []database.BackfillRow {
	out := make([]database.BackfillRow, 0, len(rows))
	for ts, scalars := range rows {
		out = append(out, database.BackfillRow{Timestamp: ts, Scalars: scalars})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out
}

// parseClock reads "HH:MM" in ET, falling back to the default time.
func parseClock(value string, fallbackHour, fallbackMinute int) (int, int) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallbackHour, fallbackMinute
	}
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return fallbackHour, fallbackMinute
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return fallbackHour, fallbackMinute
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return fallbackHour, fallbackMinute
	}
	return hour, minute
}

func isRateLimited(err error) bool {
	var limited *api.RateLimitError
	return errors.As(err, &limited)
}

func isSubscriptionDenied(err error) bool {
	var denied *api.SubscriptionError
	return errors.As(err, &denied)
}

// etaSuffix projects when the pass finishes from linear progress, formatted for
// a log line. done is the number of tickers completed; total is the queue size.
func etaSuffix(started time.Time, done, total int) string {
	if done <= 0 || total <= 0 || done >= total {
		return ""
	}
	elapsed := time.Since(started)
	if elapsed <= 0 {
		return ""
	}
	eta := started.Add(elapsed * time.Duration(total) / time.Duration(done))
	return fmt.Sprintf(" (ETA %s)", eta.Format("15:04:05 MST"))
}
