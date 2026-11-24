import { CHART_SERIES_METADATA, getDefaultChartColors, isChartColorOffValue, getChartColorsOffFromSettings } from './js/shared/series.js';

let App = null;
// Single source of truth: the backend serves the ticker list from /api/tickers.
// If the fetch fails the table stays empty rather than drifting from Go.
let ALL_TICKERS = [];

let dirty = false;
let currentSettings = null; // last known clean settings from the backend

const $ = (id) => document.getElementById(id);

function markDirty() {
    dirty = true;
    $('dirty').style.display = 'inline';
}

function clearDirty() {
    dirty = false;
    $('dirty').style.display = 'none';
}

async function loadAppBindings() {
    // The Wails runtime shim routes binding calls over fetch to /wails/runtime,
    // so there is no injected window._wails to wait for. Import directly like
    // main.js does instead of polling for up to 10 seconds.
    const mod = await import('./bindings/market-terminal/app.js');
    App = mod;
    return App;
}

async function loadKnownTickers() {
    try {
        const res = await fetch('/api/tickers');
        if (!res.ok) return;
        const body = await res.json();
        const tickers = Array.isArray(body) ? body : (body && Array.isArray(body.tickers) ? body.tickers : null);
        if (tickers && tickers.length) ALL_TICKERS = tickers;
    } catch (e) {}
}

function priorityValue(raw) {
    const v = String(raw || '').toLowerCase();
    return (v === 'high' || v === 'low') ? v : 'medium';
}

// Rows currently shown (search filtering sets display:none). Bulk actions and
// the header checkboxes only touch these, so a search narrows the change.
function visibleTickerRows() {
    return Array.prototype.filter.call(
        $('ticker-selection').querySelectorAll('tr'),
        (tr) => tr.style.display !== 'none' && tr.dataset.ticker,
    );
}

// Keep the "All" and "Archive only" header checkboxes in step with the visible
// rows: checked when all agree, indeterminate when only some do.
function syncBulkControls() {
    const rows = visibleTickerRows();
    const all = $('tickers-all-enabled');
    const arc = $('tickers-all-archive');
    if (rows.length === 0) {
        all.checked = false; all.indeterminate = false;
        arc.checked = false; arc.indeterminate = false;
        return;
    }
    const enabledCount = rows.filter((tr) => tr.querySelector('input.enabled-check').checked).length;
    all.checked = enabledCount === rows.length;
    all.indeterminate = enabledCount > 0 && enabledCount < rows.length;
    const archiveCount = rows.filter((tr) => tr.querySelector('input.archive-check').checked).length;
    arc.checked = archiveCount === rows.length;
    arc.indeterminate = archiveCount > 0 && archiveCount < rows.length;
}

// Priority and refresh only affect live polling, so they are inert on an
// archive-only row. Grey them out so the row reads as "download after close".
function syncRowDisabled(tr) {
    const archive = tr.querySelector('input.archive-check').checked;
    const refreshInput = tr.querySelector('input[type=number]');
    const sel = tr.querySelector('select');
    if (refreshInput) { refreshInput.disabled = archive; refreshInput.style.opacity = archive ? '0.45' : ''; }
    if (sel) { sel.disabled = archive; sel.style.opacity = archive ? '0.45' : ''; }
}

// ---------- render ----------

function renderTiers(settings) {
    const tiers = Array.isArray(settings.APISubscriptionTiers) ? settings.APISubscriptionTiers : ['classic'];
    $('settings-tier-classic').checked = tiers.includes('classic');
    $('settings-tier-state').checked = tiers.includes('state');
    $('settings-tier-orderflow').checked = tiers.includes('orderflow');
}

function renderCollectionMode(settings) {
    const all = settings.CollectAllEndpoints !== false;
    $('collect-all').checked = all;
    $('collect-charts').checked = !all;
}

function renderProfile(settings) {
    const p = settings.ProfileSettings || {};
    $('profile-enabled').checked = p.enabled === true;
    const layers = p.layers && typeof p.layers === 'object' ? p.layers : null;
    ['classic', 'state', 'gamma', 'delta', 'vanna', 'charm'].forEach((id) => {
        $('profile-layer-' + id).checked = layers ? layers[id] === true : id === 'classic';
    });
    $('profile-side').value = p.profile_side === 'left' ? 'left' : 'right';
    const sw = Number(p.side_width_percent);
    $('profile-side-width').value = (Number.isFinite(sw) && sw > 0) ? sw : 28;
    $('profile-show-priors').checked = p.show_priors !== false;
}

function renderTickers(settings) {
    const tbody = $('ticker-selection');
    tbody.innerHTML = '';
    if (ALL_TICKERS.length === 0) {
        const tr = document.createElement('tr');
        const td = document.createElement('td');
        td.colSpan = 6;
        td.style.cssText = 'text-align: center; color: #ff9800; padding: 1rem;';
        td.textContent = 'Could not load the ticker list. Reopen Settings once the backend is ready.';
        tr.appendChild(td);
        tbody.appendChild(tr);
        return;
    }
    const configs = settings.TickerConfigs || {};
    ALL_TICKERS.forEach((ticker) => {
        const cfg = configs[ticker] || {};
        const enabled = cfg.CollectionEnabled === true;
        const archiveOnly = cfg.ArchiveOnly === true;
        const refresh = (cfg.RefreshRateMs !== undefined && cfg.RefreshRateMs !== null) ? cfg.RefreshRateMs : 0;
        const priority = cfg.Priority ? priorityValue(cfg.Priority) : 'medium';

        const tr = document.createElement('tr');
        tr.dataset.ticker = ticker;
        tr.draggable = true;

        const tdHandle = document.createElement('td');
        tdHandle.className = 'handle';
        tdHandle.textContent = '☰';

        const tdEnabled = document.createElement('td');
        const chk = document.createElement('input');
        chk.type = 'checkbox';
        chk.className = 'enabled-check';
        chk.checked = enabled;
        chk.addEventListener('change', () => { markDirty(); syncBulkControls(); });
        tdEnabled.appendChild(chk);

        const tdArchive = document.createElement('td');
        const archiveChk = document.createElement('input');
        archiveChk.type = 'checkbox';
        archiveChk.className = 'archive-check';
        archiveChk.checked = archiveOnly;
        archiveChk.title = 'Skip live polling; download from the end-of-day archive instead.';
        archiveChk.addEventListener('change', () => { markDirty(); syncBulkControls(); syncRowDisabled(tr); });
        tdArchive.appendChild(archiveChk);

        const tdName = document.createElement('td');
        tdName.textContent = ticker;

        const tdRefresh = document.createElement('td');
        const refreshInput = document.createElement('input');
        refreshInput.type = 'number';
        refreshInput.className = 'small';
        refreshInput.min = '0';
        refreshInput.value = refresh;
        refreshInput.addEventListener('change', markDirty);
        tdRefresh.appendChild(refreshInput);

        const tdPriority = document.createElement('td');
        const sel = document.createElement('select');
        ['high', 'medium', 'low'].forEach((p) => {
            const opt = document.createElement('option');
            opt.value = p;
            opt.textContent = p.charAt(0).toUpperCase() + p.slice(1);
            if (p === priority) opt.selected = true;
            sel.appendChild(opt);
        });
        sel.addEventListener('change', markDirty);
        tdPriority.appendChild(sel);

        tr.append(tdHandle, tdEnabled, tdArchive, tdName, tdRefresh, tdPriority);
        tbody.appendChild(tr);
        syncRowDisabled(tr);
    });

    syncBulkControls();

    // drag reorder
    let dragRow = null;
    tbody.querySelectorAll('tr').forEach((row) => {
        row.addEventListener('dragstart', () => { dragRow = row; row.classList.add('dragging'); });
        row.addEventListener('dragend', () => { row.classList.remove('dragging'); dragRow = null; });
        row.addEventListener('dragover', (e) => {
            e.preventDefault();
            const after = getDragAfterElement(tbody, e.clientY);
            if (after == null) tbody.appendChild(dragRow);
            else tbody.insertBefore(dragRow, after);
        });
    });
}

function getDragAfterElement(container, y) {
    const els = [...container.querySelectorAll('tr:not(.dragging)')];
    return els.reduce((closest, child) => {
        const box = child.getBoundingClientRect();
        const offset = y - box.top - box.height / 2;
        if (offset < 0 && offset > closest.offset) return { offset, element: child };
        return closest;
    }, { offset: Number.NEGATIVE_INFINITY }).element;
}

function renderColors(settings) {
    const grid = $('chart-colors-grid');
    grid.innerHTML = '';
    const defaults = getDefaultChartColors();
    const chartColors = settings.ChartColors || {};
    const hidden = new Set([
        ...(settings.HiddenPlots || []),
        ...getChartColorsOffFromSettings(settings),
    ]);

    CHART_SERIES_METADATA.forEach((series) => {
        let value = chartColors[series.key] || defaults[series.key];
        if (isChartColorOffValue(value)) value = defaults[series.key];
        const isOff = hidden.has(series.key);

        const item = document.createElement('div');
        item.className = 'field color-field';

        const label = document.createElement('label');
        label.textContent = series.label;
        label.title = series.label;

        const input = document.createElement('input');
        input.type = 'color';
        input.value = value;
        input.disabled = isOff;
        input.style.opacity = isOff ? '0.45' : '1';

        const offLabel = document.createElement('label');
        offLabel.className = 'color-off-label';
        offLabel.title = 'Hide on chart by default';
        const offChk = document.createElement('input');
        offChk.type = 'checkbox';
        offChk.checked = isOff;
        const offText = document.createElement('span');
        offText.textContent = 'Hide';
        offLabel.append(offChk, offText);

        offChk.addEventListener('change', () => {
            input.disabled = offChk.checked;
            input.style.opacity = offChk.checked ? '0.45' : '1';
            markDirty();
        });
        input.addEventListener('input', () => { markDirty(); });

        item.append(label, input, offLabel);
        grid.appendChild(item);
    });
}

function renderGeneral(settings) {
    $('close-after-market').checked = settings.CloseAfterMarketClose === true;
    $('start-before-open').checked = settings.StartBeforeMarketOpen === true;
    $('use-market-time').checked = settings.UseMarketTime === true;
    $('use-24-hour-time').checked = settings.Use24HourTime !== false;
    $('enable-logging').checked = settings.EnableLogging !== false;
    $('hide-console').checked = settings.HideConsole !== false;
    $('chart-zoom-filter').value = settings.ChartZoomFilterPercent || 1.0;
    const px = Number(settings.AutoFollowBufferPx);
    $('auto-follow-buffer').value = (Number.isFinite(px) && px >= 0) ? px : 16;
    $('price-axis-location').value = (settings.PriceAxisLocation || 'left').toLowerCase() === 'right' ? 'right' : 'left';
}

function renderStorage(settings) {
    $('data-dir').value = settings.DataDirectory || 'Tickers';
    $('data-retention-days').value = settings.DataRetentionDays || 0;
    $('eod-backfill-enabled').checked = settings.EODBackfillEnabled === true;
    $('eod-backfill-time').value = settings.EODBackfillTime || '17:15';
    $('eod-max-ladder-mb').value = settings.EODMaxLadderMB || 0;
    $('intraday-backfill-enabled').checked = settings.IntradayBackfillEnabled === true;
    $('intraday-backfill-interval').value = settings.IntradayBackfillIntervalMinutes || 30;
    $('intraday-fill-min-gap').value = settings.IntradayFillMinGapSeconds || 45;
    $('archive-task-enabled').checked = settings.ArchiveTaskEnabled === true;
    $('archive-task-time').value = settings.ArchiveTaskTime || '17:15';
}

function renderApi(settings) {
    $('api-key').value = '';
    $('api-key').placeholder = settings.HasAPIKey ? '••••••••••••••••' : 'Enter your API key';
    $('api-key-status').textContent = settings.HasAPIKey ? '✓ API key is saved' : 'No API key configured';
    $('api-key-status').style.color = settings.HasAPIKey ? '#4CAF50' : '#ff9800';
    $('ninja-api-key').value = '';
    $('ninja-api-key').placeholder = settings.HasNinjaAPIKey ? '••••••••••••••••' : 'Enter your NinjaTrader API key';
    $('ninja-api-key-status').textContent = settings.HasNinjaAPIKey
        ? '✓ NinjaTrader key is saved'
        : 'No NinjaTrader key configured. Historical call and put gamma stay off.';
    $('ninja-api-key-status').style.color = settings.HasNinjaAPIKey ? '#4CAF50' : '#ff9800';
}

function renderAll(settings) {
    renderApi(settings);
    renderTiers(settings);
    renderCollectionMode(settings);
    renderStorage(settings);
    renderTickers(settings);
    renderColors(settings);
    renderGeneral(settings);
    renderProfile(settings);
}
// ---------- save ----------

function collect(settings) {
    // keys
    const apiKey = $('api-key').value.trim();
    if (apiKey) settings.APITKey = apiKey;
    const ninjaKey = $('ninja-api-key').value.trim();
    if (ninjaKey) settings.NinjaAPIKey = ninjaKey;

    // tiers
    const tiers = [];
    if ($('settings-tier-classic').checked) tiers.push('classic');
    if ($('settings-tier-state').checked) tiers.push('state');
    if ($('settings-tier-orderflow').checked) tiers.push('orderflow');
    settings.APISubscriptionTiers = tiers.length ? tiers : ['classic'];

    settings.CollectAllEndpoints = $('collect-all').checked;
    settings.DataDirectory = $('data-dir').value || 'Tickers';
    const retention = parseInt($('data-retention-days').value, 10);
    settings.DataRetentionDays = (!isNaN(retention) && retention >= 0) ? retention : 0;

    // backfill / recovery
    settings.EODBackfillEnabled = $('eod-backfill-enabled').checked;
    settings.EODBackfillTime = ($('eod-backfill-time').value || '').trim() || '17:15';
    const ladderMB = parseInt($('eod-max-ladder-mb').value, 10);
    settings.EODMaxLadderMB = (!isNaN(ladderMB) && ladderMB >= 0) ? ladderMB : 0;
    settings.IntradayBackfillEnabled = $('intraday-backfill-enabled').checked;
    const intervalMin = parseInt($('intraday-backfill-interval').value, 10);
    settings.IntradayBackfillIntervalMinutes = (!isNaN(intervalMin) && intervalMin >= 1) ? intervalMin : 30;
    const minGap = parseInt($('intraday-fill-min-gap').value, 10);
    settings.IntradayFillMinGapSeconds = (!isNaN(minGap) && minGap >= 1) ? minGap : 45;
    settings.ArchiveTaskEnabled = $('archive-task-enabled').checked;
    settings.ArchiveTaskTime = ($('archive-task-time').value || '').trim() || '17:15';

    // tickers
    if (!settings.TickerConfigs) settings.TickerConfigs = {};
    const order = [];
    $('ticker-selection').querySelectorAll('tr').forEach((tr) => {
        const ticker = tr.dataset.ticker;
        order.push(ticker);
        const enabled = tr.querySelector('input.enabled-check').checked;
        const archiveOnly = tr.querySelector('input.archive-check').checked;
        const refresh = parseInt(tr.querySelector('input[type=number]').value, 10) || 0;
        const priority = tr.querySelector('select').value;
        const cfg = settings.TickerConfigs[ticker] || {};
        cfg.Display = enabled;
        cfg.CollectionEnabled = enabled;
        cfg.ArchiveOnly = archiveOnly;
        cfg.Priority = priority;
        cfg.RefreshRateMs = refresh;
        settings.TickerConfigs[ticker] = cfg;
    });
    settings.TickerOrder = order;

    // colors & plots (merged): one Hide toggle drives both the default-visibility
    // list and the color "off" list so they stay in sync.
    if (!settings.ChartColors) settings.ChartColors = {};
    const colorsOff = [];
    const hiddenPlots = [];
    // Rebuild colors from the rendered grid (order-preserving, uses metadata).
    const colorItems = $('chart-colors-grid').querySelectorAll('.color-field');
    const colorKeys = CHART_SERIES_METADATA.map((s) => s.key);
    colorItems.forEach((item, idx) => {
        const key = colorKeys[idx];
        if (!key) return;
        const input = item.querySelector('input[type=color]');
        const offChk = item.querySelector('.color-off-label input');
        if (input) settings.ChartColors[key] = input.value;
        if (offChk && offChk.checked) { colorsOff.push(key); hiddenPlots.push(key); }
    });
    settings.ChartColorsOff = colorsOff;
    settings.HiddenPlots = hiddenPlots;

    // general
    settings.CloseAfterMarketClose = $('close-after-market').checked;
    settings.StartBeforeMarketOpen = $('start-before-open').checked;
    settings.UseMarketTime = $('use-market-time').checked;
    settings.Use24HourTime = $('use-24-hour-time').checked;
    settings.EnableLogging = $('enable-logging').checked;
    settings.HideConsole = $('hide-console').checked;
    const zf = parseFloat($('chart-zoom-filter').value);
    settings.ChartZoomFilterPercent = (!isNaN(zf) && zf >= 0.01 && zf <= 100) ? zf : 1.0;
    const buf = parseFloat($('auto-follow-buffer').value);
    settings.AutoFollowBufferPx = (!isNaN(buf) && buf >= 0 && buf <= 400) ? buf : 16;
    settings.PriceAxisLocation = $('price-axis-location').value;

    // profile
    if (!settings.ProfileSettings || typeof settings.ProfileSettings !== 'object') settings.ProfileSettings = {};
    const p = settings.ProfileSettings;
    p.profile_side = $('profile-side').value;
    const sw = parseFloat($('profile-side-width').value);
    p.side_width_percent = (!isNaN(sw) && sw >= 12 && sw <= 50) ? sw : 28;
    p.show_priors = $('profile-show-priors').checked;
    p.enabled = $('profile-enabled').checked;
    const layers = {};
    ['classic', 'state', 'gamma', 'delta', 'vanna', 'charm'].forEach((id) => { layers[id] = $('profile-layer-' + id).checked; });
    p.layers = layers;

    return settings;
}

function settingsWithoutKey(s) {
    const copy = { ...s };
    if (typeof copy.APITKey === 'string' && copy.APITKey.length) copy.HasAPIKey = true;
    if (typeof copy.NinjaAPIKey === 'string' && copy.NinjaAPIKey.length) copy.HasNinjaAPIKey = true;
    delete copy.APITKey;
    delete copy.NinjaAPIKey;
    return copy;
}

async function save() {
    if (!App) return;
    let settings = currentSettings ? { ...currentSettings } : {};
    // deep-copy maps so the "clean" snapshot is not mutated
    settings = JSON.parse(JSON.stringify(settings));
    collect(settings);

    // Save through the backend, then re-read the canonical (redacted) settings.
    await App.SaveSettings(settings);
    const fresh = await App.GetSettings();
    if (fresh) {
        currentSettings = fresh;
        renderAll(fresh);
    }
    clearDirty();
    broadcast(settings);
    // Save is the window's commit action; close it so the next open shows the
    // saved state instead of a stale form.
    if (typeof App.CloseSettingsWindow === 'function') {
        try {
            await App.CloseSettingsWindow();
        } catch (e) {
            console.error('[Settings] CloseSettingsWindow failed:', e);
        }
    }
}

function setSavedFlash() {
    $('save').textContent = 'Saved';
    setTimeout(() => { $('save').textContent = 'Save'; }, 1200);
}

function broadcast(settings) {
    try {
        const ch = new BroadcastChannel('market-terminal-settings');
        ch.postMessage({ type: 'settings-updated', settings: settingsWithoutKey(settings) });
        ch.close();
    } catch (e) {}
}

async function cancel() {
    clearDirty();
    if (App && typeof App.CloseSettingsWindow === 'function') {
        try {
            await App.CloseSettingsWindow();
        } catch (e) {
            console.error('[Settings] CloseSettingsWindow failed:', e);
        }
    }
}

function resetToDefaults() {
    const dlg = $('dialog');
    dlg.style.display = 'flex';
}

// ---------- boot ----------

const PANE_KEY = 'market-terminal-settings-pane';

async function init() {
    wireSidebar();
    wireFooter();
    restorePane();
    try {
        // Tickers and bindings are independent; load them together.
        await Promise.all([loadKnownTickers(), loadAppBindings()]);
        const settings = await App.GetSettings();
        currentSettings = settings || {};
        renderAll(currentSettings);
    } catch (e) {
        console.error('[Settings] Failed to load:', e);
    }
}

function restorePane() {
    let pane = null;
    try { pane = localStorage.getItem(PANE_KEY); } catch (e) {}
    const items = document.querySelectorAll('#sidebar .item');
    if (!pane) return;
    for (const it of items) {
        if (it.dataset.pane === pane) { it.click(); return; }
    }
}

function wireSidebar() {
    const items = document.querySelectorAll('#sidebar .item');
    items.forEach((it) => it.addEventListener('click', () => {
        items.forEach((x) => x.classList.remove('active'));
        it.classList.add('active');
        document.querySelectorAll('.pane').forEach((p) => p.hidden = true);
        $('pane-' + it.dataset.pane).hidden = false;
        try { localStorage.setItem(PANE_KEY, it.dataset.pane); } catch (e) {}
    }));
}

function wireFooter() {
    $('save').addEventListener('click', save);
    $('cancel').addEventListener('click', cancel);
    $('reset-defaults').addEventListener('click', resetToDefaults);
    $('dialog-cancel').addEventListener('click', () => { $('dialog').style.display = 'none'; });
    $('dialog-confirm').addEventListener('click', async () => {
        $('dialog').style.display = 'none';
        // Reset non-secret fields to defaults, keep keys/tiers/tickers/data dir.
        if (!App) return;
        const settings = { ...currentSettings };
        settings.HiddenPlots = [];
        settings.ChartColors = getDefaultChartColors();
        settings.ChartColorsOff = [];
        settings.ChartZoomFilterPercent = 1.0;
        settings.AutoFollowBufferPx = 16;
        settings.CloseAfterMarketClose = false;
        settings.StartBeforeMarketOpen = false;
        settings.UseMarketTime = false;
        settings.Use24HourTime = true;
        settings.EODBackfillEnabled = true;
        settings.EODBackfillTime = '17:15';
        settings.IntradayBackfillEnabled = true;
        settings.IntradayBackfillIntervalMinutes = 30;
        settings.IntradayFillMinGapSeconds = 45;
        settings.EODMaxLadderMB = 200;
        settings.PriceAxisLocation = 'left';
        settings.ProfileSettings = { ...(settings.ProfileSettings || {}), profile_side: 'right', side_width_percent: 28, show_priors: true, enabled: false };
        // reset polling
        Object.keys(settings.TickerConfigs || {}).forEach((t) => {
            const c = settings.TickerConfigs[t];
            c.RefreshRateMs = 0;
            c.Priority = 'medium';
            settings.TickerConfigs[t] = c;
        });
        await App.SaveSettings(settings);
        const fresh = await App.GetSettings();
        if (fresh) { currentSettings = fresh; renderAll(fresh); }
        clearDirty();
        broadcast(settings);
        setSavedFlash();
    });

    // dirty tracking on any input change (excluding the search box and the bulk
    // priority dropdown, which only takes effect when Apply is pressed)
    document.addEventListener('change', (e) => {
        if (e.target && (e.target.id === 'ticker-search' || e.target.id === 'tickers-bulk-priority')) return;
        if (e.target && e.target.closest && e.target.closest('.pane, .footer')) markDirty();
    });
    document.addEventListener('input', (e) => {
        if (e.target && e.target.type === 'color') return; // handled by input handler
        if (e.target && e.target.id === 'ticker-search') return;
        if (e.target && e.target.closest && e.target.closest('.pane')) markDirty();
    });

    // search filter
    $('ticker-search').addEventListener('input', (e) => {
        const term = e.target.value.toLowerCase();
        $('ticker-selection').querySelectorAll('tr').forEach((tr) => {
            tr.style.display = tr.dataset.ticker.toLowerCase().includes(term) ? '' : 'none';
        });
        syncBulkControls();
    });

    $('reset-polling-defaults').addEventListener('click', () => {
        $('ticker-selection').querySelectorAll('tr').forEach((tr) => {
            tr.querySelector('input[type=number]').value = 0;
            tr.querySelector('select').value = 'medium';
        });
        markDirty();
    });

    // Header "All" checkbox: tri-state. Clicking checks every shown row when
    // unchecked/mixed, and unchecks every shown row when fully checked.
    $('tickers-all-enabled').addEventListener('change', (e) => {
        const checked = e.target.checked;
        visibleTickerRows().forEach((tr) => { tr.querySelector('input.enabled-check').checked = checked; });
        markDirty();
        syncBulkControls();
    });

    $('tickers-all-archive').addEventListener('change', (e) => {
        const checked = e.target.checked;
        visibleTickerRows().forEach((tr) => {
            tr.querySelector('input.archive-check').checked = checked;
            syncRowDisabled(tr);
        });
        markDirty();
        syncBulkControls();
    });

    // Priority dropdown applies to every shown row the moment it is picked.
    $('tickers-bulk-priority').addEventListener('change', (e) => {
        const value = e.target.value;
        if (!value) return;
        visibleTickerRows().forEach((tr) => { tr.querySelector('select').value = value; });
        e.target.value = ''; // reset so it reads as an action, not a state
        markDirty();
    });

    $('reset-colors').addEventListener('click', () => {
        const defaults = getDefaultChartColors();
        const keys = CHART_SERIES_METADATA.map((s) => s.key);
        $('chart-colors-grid').querySelectorAll('.color-field').forEach((item, idx) => {
            const key = keys[idx];
            if (!key) return;
            const input = item.querySelector('input[type=color]');
            const offChk = item.querySelector('.color-off-label input');
            if (input) { input.value = defaults[key]; input.disabled = false; input.style.opacity = '1'; }
            if (offChk) offChk.checked = false;
        });
        markDirty();
    });

    // Enter saves, Escape cancels. Native controls (select, color) and the
    // confirm dialog keep their own key handling.
    window.addEventListener('keydown', (e) => {
        const dlg = $('dialog');
        if (dlg && dlg.style.display !== 'none') {
            if (e.key === 'Escape') { e.preventDefault(); dlg.style.display = 'none'; }
            return;
        }
        const el = document.activeElement;
        if (el && (el.tagName === 'SELECT' || el.type === 'color' || el.tagName === 'TEXTAREA')) return;
        if (e.key === 'Enter') {
            e.preventDefault();
            save();
        } else if (e.key === 'Escape') {
            e.preventDefault();
            cancel();
        }
    });
}

window.addEventListener('DOMContentLoaded', init);
