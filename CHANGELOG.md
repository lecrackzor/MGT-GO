# Changelog

## 2026-11-19 - v1.2.0

### Settings window

- Settings move out of the modal that sat inside the main window and into their own resizable window (`/settings.html`) with a sidebar: API, Storage, Tickers, Plots & Colors, General, Profile. The gear button opens it; it re-focuses instead of duplicating when it is already open.
- Plots and colors share one pane. Every series has a color swatch and a `Hide` toggle, laid out in aligned columns that reflow as the window resizes.
- Changes are staged until Save. Save writes through the same backend, broadcasts to the chart windows, and closes the window. Cancel and Escape close it without saving; the footer shows an unsaved-changes dot. Reset to defaults keeps your API keys, tiers, and enabled tickers.
- Fix: subscription tiers and collection mode render from the saved settings every time the window opens instead of appearing unchecked.
- The window reopens on the last pane you used. Enter saves, Escape closes.
- Fix: the intraday gap-fill controls are gone. Only the end-of-day archive recovery pass is exposed, matching the plan that includes `/hist/eod` but not the intraday history route.

### Charts

- Chart header bar: ticker, date, session pill with a countdown to the next open or close, spot with change, the session's API request count, and a Pin button that keeps the window on top.
- Toolbar is grouped into View, Profile, Info, then Reset, Auto-Follow, and Screenshot. Profile layers collapse into a `Layers` menu.
- The series legend is clickable and drives which plots are drawn. The choice is stored with the window.
- The crosshair info box shows per-series values and deltas, resizes with its content, and no longer carries permanent horizontal and vertical scrollbars. It can be pinned so the crosshair does not move it.
- Fix: right-click `Lookback dots` now works when the profile is docked to the side or open in its own window. The setting is written to the chart preferences and broadcast to the profile window.
- Fix: the right-click auto-follow toggle reads the checkbox instead of negating the previous state.
- Fix: opening a chart or profile window twice in quick succession closes the duplicate instead of leaking a second window.

### Main window

- The header shows the live API request count for this session and a backfill status chip that opens the session log when clicked.

### Build

- Windows resource files (`rsrc_windows_amd64.syso`) are generated from `winres/winres.json` so the executable and its shortcuts show the app icon again. Build with `go-winres make`, or `task windows:build` which regenerates the resource first so a fresh clone still gets an icon.
- Version info reads `Market Terminal` 1.2.0.0 with the author and MIT copyright filled in, instead of blank fields.
- Regenerated the Wails bindings so the new window methods are callable; the committed bindings had drifted from the Go settings struct.
- Removed the old in-window settings modal and its dead code from `main.js`, and the superseded `frontend/mock`. Ticker priority values are normalized to `high`/`medium`/`low`.
- Removed the hardcoded ticker fallback from `settings.js` so the ticker list has one source of truth (`/api/tickers`).
- Tidied generated clutter (`bin/`, unused icon PNGs) and ignored `trimmed-out/`.
- Added 13 tickers the API now serves but the app did not know: `BOIL`, `DDOG`, `DIA`, `NVO`, `ORCL`, `PL`, `RDDT`, `SHOP`, `SNDK`, `SPCX`, `UBER`, `UNG`, `ZS`. Verified they return live data.

### Docs and repository

- One `README.md` replaces `SETUP.md` and `QUICK_START.md`, which still described `go mod init` and `cd GO`.
- Added the MIT `LICENSE`; `DISCLAIMER.md` no longer claims "all rights reserved".
- Dropped the unused `package.json`.
- `gofmt` is now clean and CI checks it; CI also builds for Windows.


## 2026-11-18

Existing days are read exactly as they were written. Nothing on disk is rewritten or re-encoded.

### Storage

- Option ladders now store one full copy every 40 samples instead of every sample, and the deltas omit the lookback priors the reader can rebuild. On a recorded SPY session that is 681 bytes per row instead of 784.
- Ladder samples default to one every 5 seconds. The 0/1/90 DTE ladders change slowly and the chart row keeps the poll cadence, so the displayed levels are unaffected. `ProfileSampleSeconds` in Settings sets it back to 1 if you want the old density.
- The stored ladder and the format marker are versioned. A ladder written in the older shape is never diffed against a newer one, so a day cannot mix two layouts.
- Lookback priors are rebuilt when the profile is read, from samples already on disk at 60, 300, 600, 900, and 1800 seconds back (300, 900, and 1800 for greek layers). Old days show the same dots they showed before.
- Chart rows are written in one prepared statement per column set instead of one per row.
- After the last write of a session the file is optimized and its WAL is truncated. Folders older than `DataRetentionDays` are pruned at startup.

### Recovering missing history

- The `/hist` and `/hist/eod` endpoints are used to fill gaps that polling missed. Recovered rows go into their own table in the profile file, so a fill can never alter the live ladder chain.
- A ticker whose requests failed is retried later. A ticker recovered once is skipped on the next pass.
- The fill runs when the app closes after the market close, and the archive pass runs on startup for the previous session plus at 17:15 ET. Both run on Classic, State, or Orderflow access, without a quant key.

### Correctness

- A profile sample is no longer dropped when the chart window reads the same file while it is written. The write transaction took the lock after reading, which SQLite rejects at once instead of waiting; whole charm ladders were lost this way.
- Reading a ladder that has more deltas than expected now widens its search until it finds the full base instead of failing with "missing its full ladder". Older files with gaps from the lost writes open normally again.
- Rate limiting is tracked per endpoint, so a gap in polling scales with the number of tickers rather than the number of endpoints.
- A 5xx answer or a truncated JSON body is retried with backoff.
- State-only and Orderflow-only access still fill spot and the major chart levels.
- Chart levels use the newest row at or before the requested time instead of reporting empty.
- Ninja historical majors are cached for 60 seconds instead of requested on every chart poll.
- Greek and State profile layers are disabled in the window when the key cannot serve them, instead of polling into 403s.

## 2026-10-21

This build is meant to be left running through a normal session.

### Session clock

- A chart or profile opened on Today follows the trading day. After the 8:30 AM ET rollover it moves to the new session instead of staying on yesterday.
- The 8:30 rollover still saves the previous day. It does not start a round of API calls while the market is closed.
- NYSE holidays and 1:00 PM early closes for 2026 through 2028 are treated as closed. A holiday uses the previous trading day, so the app does not open an empty session folder or poll until 4:00 PM.
- A custom poll interval shows up on the main window. ES and NQ set to 1 second update about that often, instead of the last-update time jumping forward every few seconds.
- If Gexbot answers with a rate limit, the rest of that request wave stops. Later polls wait out the backoff.

### Charts and profiles

- A profile can sit beside a chart or open in its own window. Layers are Classic, State, Gamma, Delta, Vanna, and Charm, for 0 DTE, 1 DTE, and 90 DTE.
- Those layer checkboxes belong to the window. Turning Classic on in one chart or profile does not change the others. The checkboxes in Settings still choose which ladders are collected.
- Average or Scatter, DTE, price ticks, lookback dots, and the profile side and width stay with that window too. Settings still sets the default for a window that has not chosen its own.
- Option ladders are stored in a file next to the chart database. If that write fails, the same sample is tried again. The chart row is kept.
- A new sample that reuses the last chart timestamp replaces that point. It no longer stays invisible until the chart is reloaded.
- The main table ignores a slow poll that finishes after a newer one, so an older quote cannot paint over a fresh row.

### Settings and the API key

- The API key stays in `%AppData%\market-terminal\config.yaml`. The window, the saved page data, and other windows are told only whether a key is set.
- That config file is readable by your Windows account only.
- A settings change that does not save shows an error in the status line. Closing the window sends a change that was still waiting to be saved.
- The first-run wizard opens on the API key field. Tab stays inside the wizard and inside Settings.

### Log

- The Log button opens the current session log. File logging still has to be on in Settings.
