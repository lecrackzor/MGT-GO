# Changelog

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
