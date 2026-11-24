# Gexbot API reference and client layer

This package is the only place the app talks to Gexbot. It holds both the client
and the endpoint facts we rely on. Check this file before adding a route, a
category, or a subscription check; then verify the claim against
<https://docs.gexbot.com/apidocs/> and update the "Last checked" date below.

- Last checked against the live docs: **2026-11-21**
- Docs: <https://docs.gexbot.com/apidocs/>
- Full docs text for searching: <https://docs.gexbot.com/llms-full.txt>
- Official OpenAPI spec: <https://github.com/nfa-llc/gexbot-openapi>

## Host and version

Every request is built from `config.APIBaseURL`:

```text
https://api.gexbot.com/v2
```

- `api.gexbot.com` and `api.gex.bot` both answer today; the docs use
  `api.gex.bot`. Keep one base for snapshots and history.
- The `/hist` routes are served **only** under `/v2`. The unversioned host
  answers `401 Unauthorized` for a valid key (see CHANGELOG v1.2.1).
- The snapshot routes work on both the versioned and unversioned host.
- The NinjaTrader history route is hardcoded to `api.gex.bot/v2` in
  `hist_majors.go`; it takes the Ninja integration key, not our REST key.

## Authentication

```http
Authorization: Bearer <YOUR_API_KEY>
User-Agent: MarketTerminalGexbot/1.0
Accept: application/json
```

- A `User-Agent` header is required even on public routes. A request without
  one returns `400 {"error":"User-Agent not found."}`.
- Custom keys look like `gexbot_custom_<43 chars>`; Research keys look like
  `research_<43 chars>`. They are not interchangeable.
- `GET /whoami` validates a key and returns
  `entitlements.gexbot.level` (one string) plus `addons` and `permissions`.
  The old `level`/`max_level`/`addons` fields are deprecated. We do not call
  it yet; it could replace the manual tier checkboxes in Settings.

## Public routes

| Route | Notes |
| --- | --- |
| `GET /tickers` | `stocks`, `indexes`, `futures`. No key, still needs `User-Agent`. Cached for a day. |
| `GET /tickers/quant` | Quant-only WebSocket symbols. No key. |
| `GET /{package}/categories` | `classic`, `state`, or `orderflow` category names. No key. |
| `GET /time` | Server clock (`server_time_ms`). No key. |
| `GET /diagnostics/ping` | Network probe. No key. |

The app does **not** fetch `/tickers` at runtime. `internal/config/tickers.go`
is the single source of truth and is served to the frontend at `/api/tickers`.
When the live `/tickers` list changes, update `tickers.go` by hand. On
2026-11-21 the live list matched ours except for `ANT`, which the API lists but
which answered `500` ("no live data") on `classic/zero`, so it was not added.

## Snapshot routes we collect

Templates in `endpoints.go`; tier filters in `GetEndpointsForTiers`.

| Endpoint | Path | Tier |
| --- | --- | --- |
| `classic_full` | `/{ticker}/classic/full` | Classic |
| `classic_zero` | `/{ticker}/classic/zero` | Classic |
| `classic_one` | `/{ticker}/classic/one` | Classic |
| `state_full` | `/{ticker}/state/full` | State |
| `state_zero` | `/{ticker}/state/zero` | State |
| `state_one` | `/{ticker}/state/one` | State |
| `delta_*`, `gamma_*`, `vanna_*`, `charm_*` | `/{ticker}/state/{greek}_{zero,one}` | State |

- `full` is the 90-day aggregate, `zero` the nearest expiry (0 DTE), `one` the
  next expiry (1 DTE). The docs also accept the aliases `gex_full`,
  `gex_zero`, `gex_one`, and `delta`/`onedelta`, `onegamma`, `onevanna`,
  `onecharm`. Either spelling is valid.
- **All four greeks need State or higher.** Orderflow includes them too. A
  Classic-only key gets `403 {"error":"Classic package does not have access."}`.
  This is also why `frontend/js/chart/profile.js` allows vanna/charm on state.
- The app intentionally omits the tier's `majors` and `maxchange` routes: the
  chain response already carries `major_pos_vol`, `major_neg_vol`,
  `major_pos_oi`, `major_neg_oi`, and `zero_gamma`. `max_priors` (six
  `[strike, gex]` pairs for current, 1, 5, 10, 15, 30 min) now arrives inside
  the chain response as well and is stored raw but not drawn.
- `orderflow` (`/{ticker}/orderflow/orderflow`) is the Orderflow flag-through.
  The docs schema names the levels `z_mlgamma`, `z_msgamma`, `zero_mcall`,
  `zero_mput`; `data_collection.go` maps `major_long_gamma` and friends. Not
  verifiable with this account (Classic + State), so treat the mapping as
  unconfirmed until an Orderflow key is tried.

## Snapshot fields the app uses

- Classic/State chain: `timestamp`, `ticker`, `spot`, `zero_gamma`,
  `major_pos_vol`, `major_neg_vol`, `major_pos_oi`, `major_neg_oi`,
  `sum_gex_vol`, `sum_gex_oi`, `min_dte`, `sec_min_dte`, `strikes`.
  `major_pos_oi`/`major_neg_oi`/`sum_gex_oi` are `0` or "not applicable" on
  State because that model is volume-only.
- `strikes` rows are `[strike, gex_volume, gex_oi, [four priors]]`.
- Greeks: `mini_contracts` rows are
  `[strike, call_ivol, put_ivol, greek, [three priors], put_cvolume,
  put_cvolume_priors]`. In public API responses `put_cvolume` is `0` and
  `put_cvolume_priors` is `null`.
- `delta_risk_reversal` is discontinued but still present; ignore it.
- `spot` is `null` when the caller's plan hides spot.

## Recovery routes

### Intraday history — `POST /hist/{ticker}/{package}/{category}`

- Classic and higher, and the plan must include the requested package.
- Body `{"start": <unix>, "end": <unix>}`, seconds or milliseconds (the server
  reads `start` to pick the unit; `end` must match).
- Maximum range is **8 hours**. `backfill/session.go` splits gaps with
  `splitGaps(gaps, 8*3600)`.
- Streams NDJSON with `Accept: application/x-ndjson` (also supports
  `application/json-seq`, `application/protobuf`, or `application/stream+json`).
- Categories are the recovery names, not the snapshot URLs; the translation
  table is `categories.go`. `volume_zero`/`volume_one` are rejected.
- It has its **own small daily quota** (`api_history`). Use it for gaps only,
  never as the primary feed.

### End-of-day archive — `GET /hist/eod/{ticker}`

- Every paid tier, including Classic. `Accept: application/zip`.
- The current day's report becomes eligible at **5:00 PM ET**; before that the
  route returns the most recent earlier report, so read the date from
  `Content-Disposition`.
- Only the latest report is available: download each one before 5:00 PM ET on
  the next trading day.
- Archive paths are `{TICKER}/{package}/{category}/{date}_{TICKER}_{package}_{category}.json.gz`.
  `archivePathParts` reads package and category from the third- and
  second-to-last segments.
- Available files depend on the tier: Classic ships the three classic GEX
  files; State adds state GEX plus all eight greek files; Orderflow/Quant add
  `orderflow/orderflow`. The archive never includes `volume_*`.

## Quotas and rate limits

- Quotas are **per endpoint pool, per account**. All keys of an account share
  the pool.
- Chain snapshots share `api_default`. `majors` (`api_majors`) and
  `maxchange` (`api_max_change`) are separate pools. History and futures
  conversion have their own fixed pools.
- Daily quotas reset at **12:00 AM ET**; monthly on the 1st at 12:00 AM ET.
- Live calculations refresh at most once per second; polling faster returns
  duplicate snapshots and burns quota.
- A short-window burst can return `429` with `Retry-After`. `client.go` honours
  `Retry-After` (capped at 30 s) and otherwise backs off 150 ms / 500 ms / 1.5 s
  with jitter.

## Market hours and freshness

- Live data is 09:30–16:00 ET. Outside that window the API returns the final
  snapshot of the completed session; nothing is recalculated overnight.
- The active 0 DTE / 1 DTE expiries roll during the **08:00–08:30 AM ET**
  premarket refresh. Classic OI levels are published by 08:30 ET; State and
  Orderflow stream the new expiries from 09:30 ET.
- The payload `timestamp` is the freshness signal: values frozen while HTTP
  still succeeds means a stale feed, not a quiet market.

## Everything-on collection and reading the load

To run the full Classic+State suite at the highest sustainable cadence:

- `collect_all_endpoints: true` and `api_subscription_tiers: [classic, state]`
  (14 snapshot endpoints per ticker).
- Enable every ticker (`collection_enabled: true`). The per-ticker scheduler
  is what spaces the work, and `collect_all_endpoints` is what makes a poll
  call all 14 endpoints for a ticker.
- `profile_sample_seconds: 1` writes the ladder body on every poll instead of
  every 5 s, so the profile DB matches the chart cadence.

Then read the load back from:

- The `[API-USAGE]` log line (once a minute): session request count, per-minute
  rate, and projected per-hour rate.
- The `[RATE-LIMIT]` lines: a 429 backs the whole wave off and the per-ticker
  scheduler waits it out.
- The header request counter for the session.
- `go test ./internal/coordinator/ -run TestFullSuiteLoadHarness -v` prints a
  synthetic sweep (req/s, peak in-flight, goroutines, heap) for 8/24/48
  tickers against a local stand-in, so load can be measured without a live
  session.

Note: the poll gap is throttled by the `api_default` pool the moment its limit
is learned (`scheduler.GetMinimumInterval`), so "turn everything on at 1 s" is
still bounded — the scheduler widens the gap instead of hammering the server.

A live run needs a trading session (Mon–Fri 09:30–16:00 ET). The app polls only
while the market is open.

## Error handling in `client.go`

| Status | Handling |
| --- | --- |
| `400` | `RequestError`. Bad ticker, category, or missing header. |
| `401` | `SubscriptionError`. Bad or missing key. Retrying will not help. |
| `403` | `SubscriptionError`. The plan does not include the endpoint. |
| `404` | `RequestError`. No max-change data or no ticker report yet. |
| `429` | `RateLimitError`, with `Retry-After`; the scheduler backs the wave off. |
| `5xx` | `RequestError`; retried up to 3 attempts with backoff. |
| Truncated JSON | Retried once, then `RequestError`. |

`decodeResponse` keeps arrays and objects as `json.RawMessage` so profile
ladders are not decoded and re-encoded. Scalars are decoded.

## Routes we do not use

| Route | Why not |
| --- | --- |
| `GET /{ticker}/{package}/{category}/majors` | The chain already carries every major level. |
| `GET /{ticker}/{package}/{category}/maxchange` | `max_priors` ships in the chain response. |
| `GET /whoami` | Settings asks for tiers by hand. Candidate replacement. |
| `GET /futures/conversion` | The chart has a manual multiplier/offset. This route returns the live basis (`future_contract`, `multiplier`, `additive`) for SPX/ES, NDX/NQ and others. |
| `GET /options/{ticker}/expiries` | Quant only; we use 0/1/90 DTE. |
| `POST /negotiate`, WebSockets | Quant only. REST polling is the current design. |
| `/research/*` | Needs a separate Research key. |
| `POST /ninja/hist/...` | Only for the Ninja integration key; used by `hist_majors.go`. |

## Client components

### Client (`client.go`)

- Pooled `http.Client` (`config.HTTPPoolConnections`), 30 s timeout, reused
  connections. The docs explicitly ask for connection reuse.
- Three attempts per fetch, jittered backoff, `Retry-After` support.
- Per-attempt request counter; the header and the once-a-minute usage log read it.
- `SetBaseURL` exists for recovery tests.

### QuerySystem (`query_system.go`)

- Builds one query per ticker/endpoint and filters by tier.
- `CollectAllEndpoints` collects the full tier set; otherwise chart-only
  endpoints plus the profile layers.
- Dedupes by ticker + URL and caches the endpoint plan until settings change.

### Endpoints (`endpoints.go`)

- URL templates and `GetEndpointsForTiers` / `GetChartEndpointsForTiers`.
- The tier lists are the source of truth; legacy aliases are excluded so a
  route is never fetched twice.

### History (`history.go`, `categories.go`)

- Intraday NDJSON/JSON-array parser and the EOD ZIP reader.
- `HistoryCategory` is the single snapshot-to-recovery category translation.

### Errors (`errors.go`)

- `RequestError`, `SubscriptionError`, `RateLimitError`.

## Usage

```go
client := api.NewClient(apiKey, debugPrint)
querySystem := api.NewQuerySystem(settings, client)
queries := querySystem.BuildValidatedQueries([]string{"SPX"}, []string{"SPX"})
```

## Memory visibility

All HTTP operations use Go's standard `net/http` package: no C dependencies,
all allocations visible in the Go profiler, and pooling managed by the Go
transport.
