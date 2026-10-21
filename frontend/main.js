// Main JavaScript entry point for Market Terminal Gexbot

import {
    CHART_SERIES_METADATA,
    getDefaultChartColors,
    isChartColorOffValue,
    getChartColorsOffFromSettings,
    computeNetGamma
} from './js/shared/series.js';

// Import App bindings and Events - the import map will route @wailsio/runtime to our shim
// We'll use dynamic import to handle any timing issues
let App = null;
let Events = null;
let initialized = false;

// Simple frontend logging function - sends logs to backend via HTTP endpoint
// This works immediately, no dependency on Wails runtime

async function logToBackend(level, message) {
    try {
        await fetch('/api/frontend-log', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ level, message })
        });
    } catch (err) {
        // Silently fail - we don't want logging failures to break the app
        console.error('[logToBackend] Failed:', err);
    }
}

// Ticker list constants (matching Python version)
const FUTURES = ["ES_SPX", "NQ_NDX"];
const INDEXES = ["SPX", "VIX", "NDX", "RUT", "IWM", "QQQ", "SPY"];
const MAG7 = ["AAPL", "AMZN", "GOOGL", "META", "MSFT", "NVDA", "TSLA"];
const STOCKS = ["AMD", "APP", "AVGO", "BABA", "COIN", "CRWD", "CRWV", "GLD", "GOOG", "GME", "HOOD", "HYG", "IBIT", "INTC", "IONQ", "MSTR", "MU", "NFLX", "PLTR", "SLV", "SMCI", "SNOW", "SOFI", "TLT", "TQQQ", "TSM", "UNH", "USO", "UVXY", "VALE"];
const ALL_TICKERS = [...FUTURES, ...INDEXES, ...MAG7, ...STOCKS];

function renderHiddenPlotCheckboxes() {
    const grid = document.getElementById('hidden-plots-grid');
    if (!grid || grid.dataset.ready === '1') return;
    grid.dataset.ready = '1';
    grid.replaceChildren();
    CHART_SERIES_METADATA.forEach((series) => {
        const label = document.createElement('label');
        label.style.cssText = 'display: flex; align-items: center; gap: 0.5rem; cursor: pointer; padding: 0.25rem;';
        const input = document.createElement('input');
        input.type = 'checkbox';
        input.id = `plot-${series.key}`;
        input.dataset.plot = series.key;
        input.checked = true;
        const span = document.createElement('span');
        span.textContent = series.label;
        label.append(input, span);
        grid.appendChild(label);
    });
}

renderHiddenPlotCheckboxes();

// Organize tickers by tier
function organizeTickersByTier(tickers) {
    const organized = {
        'Futures': [],
        'Indexes': [],
        'MAG7': [],
        'Stocks': [],
        'Other': []
    };
    
    tickers.forEach(ticker => {
        if (FUTURES.includes(ticker)) {
            organized['Futures'].push(ticker);
        } else if (INDEXES.includes(ticker)) {
            organized['Indexes'].push(ticker);
        } else if (MAG7.includes(ticker)) {
            organized['MAG7'].push(ticker);
        } else if (STOCKS.includes(ticker)) {
            organized['Stocks'].push(ticker);
        } else {
            organized['Other'].push(ticker);
        }
    });
    
    return organized;
}

// Wait for Wails runtime to be available
async function waitForWailsRuntime(maxAttempts = 100, delayMs = 100) {
    for (let i = 0; i < maxAttempts; i++) {
        // Check multiple possible locations for Wails runtime
        if (window._wails && window._wails.runtime && window._wails.runtime.Call) {
            return true;
        }
        if (window._wails && window._wails.Call) {
            return true;
        }
        if (window.Wails && window.Wails.Call) {
            return true;
        }
        await new Promise(resolve => setTimeout(resolve, delayMs));
    }
    console.warn('[Runtime] Wails runtime not detected after', maxAttempts * delayMs, 'ms, continuing anyway');
    console.warn('[Runtime] window._wails:', window._wails);
    console.warn('[Runtime] window.Wails:', window.Wails);
    return false;
}

// Load App bindings
// Note: Window is only created after backend is ready, so bindings should always be available
async function loadAppBindings() {
    try {
        
        // Wait for Wails runtime (should be ready since window is created after backend init)
        const appModule = await import('./bindings/market-terminal/app.js');
        
        App = appModule;
        
        if (!App) {
            throw new Error('App module is null or undefined after import');
        }
        
        // Verify critical methods exist
        if (typeof App.IsMarketOpen !== 'function') {
            console.error('[Bindings] ERROR: IsMarketOpen method not found');
            console.error('[Bindings] App type:', typeof App);
            console.error('[Bindings] App keys:', Object.keys(App || {}));
            throw new Error('IsMarketOpen method not found in App bindings');
        }
        
        if (typeof App.GetNextMarketOpenLocalTime !== 'function') {
            console.error('[Bindings] ERROR: GetNextMarketOpenLocalTime method not found');
            throw new Error('GetNextMarketOpenLocalTime method not found in App bindings');
        }
        
        if (typeof App.GetVersion !== 'function') {
            console.error('[Bindings] ERROR: GetVersion method not found');
            throw new Error('GetVersion method not found in App bindings');
        }
        
        
        // Make App available globally for HTML script block IMMEDIATELY
        window._app = App;
        window._appReady = true;
        
        // Log successful binding load using HTTP endpoint
        await logToBackend('INFO', 'App bindings loaded successfully');
        
        return true;
    } catch (error) {
        console.error('[Bindings] ===== FAILED to load App bindings =====');
        console.error('[Bindings] Error name:', error.name);
        console.error('[Bindings] Error message:', error.message);
        console.error('[Bindings] Error stack:', error.stack);
        console.error('[Bindings] Error:', error);
        // Log error to backend terminal
        await logToBackend('ERROR', `Failed to load App bindings: ${error.message || error}`);
        // This should not happen since window is created after backend is ready
        // But handle it gracefully if it does
        App = null;
        return false;
    }
}

let tickerChart = null;
let tickerData = {};
// NOTE: displayedTickers tracking is handled by the backend ChartTracker for CHART WINDOWS only
// The main table rows do NOT affect polling priority

// Settings cache in localStorage
const SETTINGS_CACHE_KEY = 'market-terminal-settings-cache';

// Drop the API key before anything is written to localStorage or another window.
function settingsWithoutKey(settings) {
    if (!settings || typeof settings !== 'object') {
        return settings;
    }
    const copy = { ...settings };
    if (typeof copy.APITKey === 'string' && copy.APITKey.length > 0) {
        copy.HasAPIKey = true;
    }
    delete copy.APITKey;
    return copy;
}

function setAppStatus(message, isError) {
    const status = document.getElementById('status');
    if (!status) return;
    status.textContent = message;
    status.style.color = isError ? '#f44336' : '';
}

// Load settings from localStorage cache
function loadSettingsFromCache() {
    try {
        const cached = localStorage.getItem(SETTINGS_CACHE_KEY);
        if (cached) {
            const parsed = JSON.parse(cached);
            if (parsed && parsed.settings && typeof parsed.settings.APITKey === 'string' && parsed.settings.APITKey.length > 0) {
                parsed.settings = settingsWithoutKey(parsed.settings);
                localStorage.setItem(SETTINGS_CACHE_KEY, JSON.stringify(parsed));
            } else if (parsed && parsed.settings) {
                parsed.settings = settingsWithoutKey(parsed.settings);
            }
            return parsed;
        }
    } catch (error) {
        console.warn('[Settings Cache] Failed to load from cache:', error);
    }
    return null;
}

// Save settings to localStorage cache
function saveSettingsToCache(settings) {
    try {
        const cacheData = {
            settings: settingsWithoutKey(settings),
            lastUpdated: new Date().toISOString()
        };
        localStorage.setItem(SETTINGS_CACHE_KEY, JSON.stringify(cacheData));
    } catch (error) {
        console.warn('[Settings Cache] Failed to save to cache:', error);
    }
}

// Get default settings (for immediate display)
function getDefaultSettings() {
    return {
        DataDirectory: 'Tickers',
        APISubscriptionTiers: ['classic'],
        TickerConfigs: {},
        UseMarketTime: false,
        Use24HourTime: true,
        EnableLogging: true,
        HiddenPlots: [],
        ChartColors: getDefaultChartColors(),
        ChartColorsOff: [],
        ChartZoomFilterPercent: 1.0,
        AutoFollowBufferPx: 16,
        PriceAxisLocation: 'left',
        ProfileSettings: { bar_width_percent: 22, profile_x_percent: 62, side_width_percent: 28, profile_side: 'right', show_priors: true, enabled: false }
    };
}

// Format time for display based on UseMarketTime setting
// If UseMarketTime is true, displays in Eastern Time (ET)
// Otherwise displays in user's local time
function formatTimeForDisplay(date, includeSeconds = true) {
    const cached = loadSettingsFromCache();
    const useMarketTime = cached?.settings?.UseMarketTime || false;
    
    const options = {
        hour: '2-digit',
        minute: '2-digit',
        hour12: false
    };
    
    if (includeSeconds) {
        options.second = '2-digit';
    }
    
    if (useMarketTime) {
        options.timeZone = 'America/New_York';
    }
    
    return date.toLocaleTimeString('en-US', options);
}

// Format date and time for display based on UseMarketTime setting
function formatDateTimeForDisplay(date) {
    const cached = loadSettingsFromCache();
    const useMarketTime = cached?.settings?.UseMarketTime || false;
    
    const options = {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false
    };
    
    if (useMarketTime) {
        options.timeZone = 'America/New_York';
        options.timeZoneName = 'short';
    }
    
    return date.toLocaleString('en-US', options);
}

// Initialize settings immediately (doesn't require backend)
let settingsFormLoading = false;
let settingsSaveTimer = 0;
let pendingTickerRefresh = false;

function scheduleSettingsSave(refreshTickers) {
    if (settingsFormLoading) return;
    if (refreshTickers) pendingTickerRefresh = true;
    clearTimeout(settingsSaveTimer);
    settingsSaveTimer = setTimeout(() => {
        flushSettingsSave();
    }, 200);
}

function flushSettingsSave() {
    if (!settingsSaveTimer && !pendingTickerRefresh) return;
    clearTimeout(settingsSaveTimer);
    settingsSaveTimer = 0;
    const refreshTickers = pendingTickerRefresh;
    pendingTickerRefresh = false;
    saveSettings({ refreshTickers: refreshTickers }).catch((error) => {
        console.error('Error saving settings:', error);
        setAppStatus('Settings were not saved', true);
    });
}

function settingsChangeNeedsTickerRefresh(target) {
    if (!target || !target.id) return false;
    return target.id.startsWith('ticker-') || target.id.startsWith('refresh-rate-') || target.id.startsWith('priority-');
}

function trapModalFocus(modal, onEscape) {
    if (!modal || modal.dataset.focusTrap === '1') return;
    modal.dataset.focusTrap = '1';
    modal.addEventListener('keydown', (e) => {
        if (modal.style.display === 'none' || modal.hasAttribute('hidden')) return;
        if (e.key === 'Escape') {
            if (typeof onEscape === 'function') {
                e.preventDefault();
                onEscape();
            }
            return;
        }
        if (e.key !== 'Tab') return;
        const items = [...modal.querySelectorAll('button, [href], input, select, textarea')]
            .filter((el) => !el.disabled && el.tabIndex >= 0);
        if (items.length === 0) return;
        const first = items[0];
        const last = items[items.length - 1];
        if (e.shiftKey && document.activeElement === first) {
            e.preventDefault();
            last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
            e.preventDefault();
            first.focus();
        }
    });
}

function initializeSettingsImmediate() {
    const settingsBtn = document.getElementById('settings-btn');
    const settingsModal = document.getElementById('settings-modal');
    const settingsClose = document.getElementById('settings-close');
    
    if (!settingsBtn || !settingsModal || !settingsClose) {
        console.error('Settings elements not found');
        return;
    }
    
    const logBtn = document.getElementById('log-btn');
    if (logBtn) {
        logBtn.addEventListener('click', async () => {
            if (!App || typeof App.OpenLogWindow !== 'function') {
                return;
            }
            try {
                await App.OpenLogWindow();
            } catch (err) {
                console.error('Failed to open log window:', err);
            }
        });
    }

    settingsBtn.addEventListener('click', async () => {
        settingsModal.style.display = 'block';
        settingsClose.focus();
        // Wait a bit for modal to be visible
        await new Promise(resolve => setTimeout(resolve, 100));
        
        // Load from cache first (instant display)
        const cachedSettings = loadSettingsFromCache();
        const defaultSettings = getDefaultSettings();
        const initialSettings = cachedSettings?.settings || defaultSettings;
        
        loadSettingsUI(initialSettings);
        
        // Then try to load from backend in background
        try {
            await loadSettingsFromBackend();
        } catch (err) {
            console.warn('[Settings] Backend load failed, using cache/defaults:', err);
            // Settings already loaded from cache, so this is fine
        }
    });
    
    function closeSettings() {
        flushSettingsSave();
        settingsModal.style.display = 'none';
    }

    settingsClose.addEventListener('click', closeSettings);
    trapModalFocus(settingsModal, closeSettings);
    
    window.addEventListener('click', (e) => {
        if (e.target === settingsModal) closeSettings();
    });

    settingsModal.addEventListener('change', (e) => {
        scheduleSettingsSave(settingsChangeNeedsTickerRefresh(e.target));
    });
    
    // Reset polling defaults button
    const resetPollingBtn = document.getElementById('reset-polling-defaults');
    if (resetPollingBtn) {
        resetPollingBtn.addEventListener('click', () => {
            // Reset all refresh rate inputs to 0 (auto) and priority to medium
            ALL_TICKERS.forEach(ticker => {
                const refreshInput = document.getElementById(`refresh-rate-${ticker}`);
                const prioritySelect = document.getElementById(`priority-${ticker}`);
                if (refreshInput) refreshInput.value = 0;
                if (prioritySelect) prioritySelect.value = 'medium';
            });
            scheduleSettingsSave(true);
        });
    }
    
}

// Market countdown timer
let marketCountdownInterval = null;

// Date selector state
let selectedDate = null; // Stores selected date as "YYYY-MM-DD" string
// When selectedDate === lastKnownMarketDate, we consider the user to be viewing "today" and will auto-advance when the market date rolls over
let lastKnownMarketDate = null;

// Fetch current market date from backend (cache-bust + no-store so webview never uses cached value)
async function fetchMarketDate() {
    const response = await fetch('/api/market-date?t=' + Date.now(), { cache: 'no-store' });
    if (!response.ok) return null;
    const data = await response.json();
    return data.date || null;
}

// Cached next-market-open time: refreshed once a minute instead of calling
// the backend on every 1s countdown tick
let cachedNextOpenISO = null;
let cachedNextOpenFetchedAt = 0;
const NEXT_OPEN_CACHE_MS = 60000;

// Update market status and countdown (runs every second - keep it quiet)
async function updateMarketStatus() {
    const marketStatusEl = document.getElementById('market-status');
    if (!marketStatusEl) {
        console.error('[Market Status] ERROR: market-status element not found!');
        return;
    }
    
    if (!App || typeof App.IsMarketOpen !== 'function' || typeof App.GetNextMarketOpenLocalTime !== 'function') {
        const errorMsg = '[Market Status] ERROR: App or required methods not available - This is why you see "Checking market status..."';
        console.error(errorMsg);
        await logToBackend('ERROR', errorMsg);
        marketStatusEl.textContent = 'Backend not connected';
        marketStatusEl.style.background = 'rgba(244, 67, 54, 0.2)';
        marketStatusEl.style.color = '#f44336';
        return;
    }
    
    try {
        // Check if market is open
        const isOpen = await App.IsMarketOpen();
        
        if (isOpen) {
            // Market is open
            cachedNextOpenISO = null;
            marketStatusEl.textContent = 'Market Open';
            marketStatusEl.style.background = 'rgba(76, 175, 80, 0.2)';
            marketStatusEl.style.color = '#4CAF50';
            return;
        }
        
        // Market is closed - get next open time (cached, refreshed once a minute)
        let nextOpenISO = cachedNextOpenISO;
        if (!nextOpenISO || (Date.now() - cachedNextOpenFetchedAt) >= NEXT_OPEN_CACHE_MS) {
            try {
                nextOpenISO = await App.GetNextMarketOpenLocalTime();
                cachedNextOpenISO = nextOpenISO;
                cachedNextOpenFetchedAt = Date.now();
            } catch (error) {
                console.error('[Market Status] ERROR calling GetNextMarketOpenLocalTime:', error);
                marketStatusEl.textContent = 'Market status error';
                marketStatusEl.style.background = 'rgba(158, 158, 158, 0.2)';
                marketStatusEl.style.color = '#9E9E9E';
                return;
            }
        }
        
        if (!nextOpenISO) {
            console.error('[Market Status] GetNextMarketOpenLocalTime returned null/undefined');
            marketStatusEl.textContent = 'Market Closed';
            marketStatusEl.style.background = 'rgba(255, 193, 7, 0.2)';
            marketStatusEl.style.color = '#FFC107';
            return;
        }
        
        // Parse the RFC3339 string - JavaScript will automatically convert ET to browser's local timezone
        const nextOpen = new Date(nextOpenISO);
        const now = new Date();
        
        // Validate date
        if (isNaN(nextOpen.getTime())) {
            console.error('[Market Status] Invalid date:', nextOpenISO);
            cachedNextOpenISO = null;
            marketStatusEl.textContent = 'Market Closed';
            return;
        }
        
        // Calculate time difference (both dates are in browser's local timezone)
        const diff = nextOpen.getTime() - now.getTime();
        
        if (diff <= 0) {
            // Time has passed, market should be open - invalidate cache and re-check
            cachedNextOpenISO = null;
            const recheckOpen = await App.IsMarketOpen();
            if (recheckOpen) {
                marketStatusEl.textContent = 'Market Open';
                marketStatusEl.style.background = 'rgba(76, 175, 80, 0.2)';
                marketStatusEl.style.color = '#4CAF50';
            } else {
                // Still closed, show generic message
                marketStatusEl.textContent = 'Market Closed';
                marketStatusEl.style.background = 'rgba(255, 193, 7, 0.2)';
                marketStatusEl.style.color = '#FFC107';
            }
            return;
        }
        
        // Calculate hours, minutes, seconds
        const hours = Math.floor(diff / (1000 * 60 * 60));
        const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60));
        const seconds = Math.floor((diff % (1000 * 60)) / 1000);
        
        // Format next open time based on UseMarketTime setting
        const cached = loadSettingsFromCache();
        const useMarketTime = cached?.settings?.UseMarketTime || false;
        const timeOptions = {
            hour: 'numeric',
            minute: '2-digit',
            hour12: true,
            timeZoneName: 'short'
        };
        if (useMarketTime) {
            timeOptions.timeZone = 'America/New_York';
        }
        const openTimeStr = nextOpen.toLocaleTimeString('en-US', timeOptions);
        
        // Format countdown message
        let countdownText;
        if (hours >= 24) {
            const days = Math.floor(hours / 24);
            const remainingHours = hours % 24;
            countdownText = `Market Closed - Opens in ${days}d ${remainingHours}h ${minutes}m (${openTimeStr})`;
        } else if (hours > 0) {
            countdownText = `Market Closed - Opens in ${hours}h ${minutes}m ${seconds}s (${openTimeStr})`;
        } else if (minutes > 0) {
            countdownText = `Market Closed - Opens in ${minutes}m ${seconds}s (${openTimeStr})`;
        } else {
            countdownText = `Market Closed - Opens in ${seconds}s (${openTimeStr})`;
        }
        
        marketStatusEl.textContent = countdownText;
        marketStatusEl.style.background = 'rgba(255, 193, 7, 0.2)';
        marketStatusEl.style.color = '#FFC107';
        
    } catch (error) {
        console.error('[Market Status] Error:', error);
        marketStatusEl.textContent = 'Market status unavailable';
        marketStatusEl.style.background = 'rgba(158, 158, 158, 0.2)';
        marketStatusEl.style.color = '#9E9E9E';
    }
}

// Start market countdown timer - simple and clean
function startMarketCountdown() {
    logToBackend('INFO', '[Market Countdown] Starting market countdown timer');
    
    // Clear existing interval if any
    if (marketCountdownInterval) {
        clearInterval(marketCountdownInterval);
    }
    
    // Update immediately
    updateMarketStatus();
    
    // Update every second
    marketCountdownInterval = setInterval(() => {
        updateMarketStatus();
    }, 1000);
}

// Stop market countdown timer
function stopMarketCountdown() {
    if (marketCountdownInterval) {
        clearInterval(marketCountdownInterval);
        marketCountdownInterval = null;
    }
}

// Initialize UI with cached data when backend is unavailable
async function initializeUIWithCache() {
    // Hide loading overlay and show app even in error state
    const loadingOverlay = document.getElementById('loading-overlay');
    if (loadingOverlay) {
        loadingOverlay.style.display = 'none';
    }
    const appContainer = document.getElementById('app');
    if (appContainer) {
        appContainer.style.display = 'block';
    }
    
    const errorMsg = 'ERROR: initializeUIWithCache() called - backend initialization FAILED';
    console.error('========================================');
    console.error(errorMsg);
    console.error('Current App state:', App);
    console.error('Initialized flag:', initialized);
    console.error('Stack trace:', new Error().stack);
    console.error('========================================');
    
    // Log to backend terminal
    await logToBackend('ERROR', errorMsg);
    await logToBackend('ERROR', `App state: ${App ? 'exists' : 'null'}, Initialized: ${initialized}`);
    
    const tbody = document.getElementById('ticker-table-body');
    if (tbody) {
        tbody.innerHTML = '<tr><td colspan="8" style="text-align: center; color: #ff9800;">Backend unavailable - please restart the application</td></tr>';
        console.error('[Cache] Set error message in ticker table');
    } else {
        console.error('[Cache] ERROR: ticker-table-body element not found!');
    }
    
    const statusEl = document.getElementById('status');
    if (statusEl) {
        statusEl.textContent = 'Backend unavailable';
        statusEl.style.color = '#f44336';
        console.error('[Cache] Set status to "Backend unavailable"');
    } else {
        console.error('[Cache] ERROR: status element not found!');
    }
}

// Main initialization function - called when backend is ready
async function initializeApplication() {
    if (initialized) {
        return true;
    }
    
    
    try {
        // Load bindings - should always succeed since backend is ready
        const bindingsLoaded = await loadAppBindings();
        
        if (!bindingsLoaded) {
            console.error('========================================');
            console.error('[Init] Step 1 FAILED: Bindings failed to load');
            console.error('[Init] This is a CRITICAL failure');
            console.error('[Init] App state:', App);
            console.error('[Init] Will retry on next attempt');
            console.error('========================================');
            const statusEl = document.getElementById('status');
            if (statusEl) {
                statusEl.textContent = 'Loading backend...';
                statusEl.style.color = '#ff9800';
            } else {
                console.error('[Init] ERROR: status element not found!');
            }
            // Don't mark as initialized, allow retry
            return false;
        }
        
        
        // Log successful binding load using HTTP endpoint
        await logToBackend('INFO', '[Init] App bindings loaded successfully');
        
        
        // Connect to backend (bindings are ready, so this should work)
        await connectToBackend();
        
        // Log successful connection using HTTP endpoint
        await logToBackend('INFO', '[Init] Backend connection successful');
        
        
        // Mark as initialized only after successful connection
        initialized = true;
        
        // Start countdown after backend is connected
        startMarketCountdown();
        
        return true;
    } catch (error) {
        console.error('========================================');
        console.error('[Init] ===== Initialization FAILED =====');
        console.error('[Init] Error name:', error.name);
        console.error('[Init] Error message:', error.message);
        console.error('[Init] Error stack:', error.stack);
        console.error('[Init] Full error object:', error);
        console.error('========================================');
        return false;
    }
}

// CRITICAL: Initialize immediately when script loads (before DOMContentLoaded)
// This ensures we start trying to connect as soon as possible

// Wait for DOM to be ready
window.addEventListener('DOMContentLoaded', async () => {
    
    // Initialize settings button immediately (doesn't need backend)
    initializeSettingsImmediate();
    watchProfileChoices();
    try {
        const settingsChannel = new BroadcastChannel('market-terminal-settings');
        settingsChannel.onmessage = (event) => {
            const settings = event.data && event.data.settings;
            if (!event.data || event.data.type !== 'settings-updated' || !settings || !settings.ProfileSettings) return;
            const profile = settings.ProfileSettings;
            const side = document.getElementById('profile-side');
            if (side && (profile.profile_side === 'left' || profile.profile_side === 'right')) side.value = profile.profile_side;
            const sideWidth = document.getElementById('profile-side-width');
            if (sideWidth && profile.side_width_percent != null) sideWidth.value = profile.side_width_percent;
            const bandWidth = document.getElementById('profile-band-width');
            if (bandWidth && profile.bar_width_percent != null) bandWidth.value = profile.bar_width_percent;
            const bandX = document.getElementById('profile-band-x');
            if (bandX && profile.profile_x_percent != null) bandX.value = profile.profile_x_percent;
            applyProfileChoiceFields(profile);
        };
    } catch (e) {}
    
    // Since window is created AFTER backend is ready, we can initialize directly
    // Window creation happens in ServiceStartup() after all backend initialization
    
    // Try initialization with retries (in case Wails runtime needs a moment)
    let retryCount = 0;
    const maxRetries = 15; // More retries
    const retryDelay = 200; // Short backoff only after a failed call
    
    async function tryInitialize() {
        if (initialized) {
            return;
        }
        
        const success = await initializeApplication();
        
        if (success) {
        } else if (retryCount < maxRetries - 1) {
            retryCount++;
            setTimeout(tryInitialize, retryDelay);
        } else {
            console.error('[Init] ===== All initialization attempts failed =====');
            const statusEl = document.getElementById('status');
            if (statusEl) {
                statusEl.textContent = 'Failed to connect - check console';
                statusEl.style.color = '#f44336';
            }
            await initializeUIWithCache();
        }
    }
    
    // Start first attempt immediately (window is already created after backend is ready)
    tryInitialize();
});

// Clean up on page unload. A settings edit still inside the 200ms debounce
// is written now; the request itself can still be cut off if the process exits.
window.addEventListener('beforeunload', () => {
    flushSettingsSave();
    stopMarketCountdown();
});

// Connect to backend
// Note: App bindings should already be loaded before this is called
async function connectToBackend() {
    const statusEl = document.getElementById('status');
    if (!statusEl) {
        console.error('[Connect] CRITICAL ERROR: Status element not found!');
        throw new Error('Status element not found');
    }
    
    try {
        statusEl.textContent = 'Connecting...';
        statusEl.style.color = '#ff9800';
        
        // App should already be loaded, but verify
        if (!App) {
            console.error('========================================');
            console.error('[Connect] CRITICAL ERROR: App bindings not loaded!');
            console.error('[Connect] App is:', App);
            console.error('[Connect] App type:', typeof App);
            console.error('========================================');
            throw new Error('App bindings not loaded. App is: ' + typeof App);
        }
        
        if (typeof App.GetVersion !== 'function') {
            throw new Error('Backend service not available.');
        }
        const version = await App.GetVersion();
        await logToBackend('INFO', 'Backend connected, version ' + version);
        
        // Update status
        statusEl.textContent = 'Connected';
        statusEl.style.color = '#4CAF50';
        
        // Check if first run
        let isFirstRun = false;
        try {
            if (typeof App.CheckFirstRun === 'function') {
                isFirstRun = await App.CheckFirstRun();
            } else {
                console.warn('[Connect] CheckFirstRun method not available, assuming not first run');
            }
        } catch (error) {
            console.error('[Connect] Error checking first run:', error);
            // Assume not first run if check fails
        }
        
        if (isFirstRun) {
            // Clear any cached settings from previous installations
            try {
                localStorage.removeItem(SETTINGS_CACHE_KEY);
            } catch (e) {
                console.warn('[Connect] Failed to clear cached settings:', e);
            }
            showStartupWizard();
        } else {
            // Initialize UI
            await initializeUI();
        }
        
        await logToBackend('INFO', 'Backend connection completed successfully');
    } catch (error) {
        console.error('========================================');
        console.error('[Connect] ===== Backend connection FAILED =====');
        console.error('[Connect] Error name:', error.name);
        console.error('[Connect] Error message:', error.message);
        console.error('[Connect] Error stack:', error.stack);
        console.error('[Connect] Full error:', error);
        console.error('[Connect] App state:', App);
        console.error('[Connect] This will trigger initializeUIWithCache()');
        console.error('========================================');
        statusEl.textContent = 'Connection error: ' + (error.message || 'Unknown error');
        statusEl.style.color = '#f44336';
        
        // Log error to backend
        await logToBackend('ERROR', `Backend connection failed: ${error.message || 'Unknown error'}`);
        
        // Try to load from cache as fallback
        await initializeUIWithCache();
        throw error; // Re-throw so caller knows it failed
    }
}

// Initialize UI components
async function initializeUI() {
    await logToBackend('INFO', '[InitializeUI] Starting UI initialization');
    
    // Hide loading overlay and show app
    const loadingOverlay = document.getElementById('loading-overlay');
    if (loadingOverlay) {
        loadingOverlay.style.display = 'none';
    }
    const appContainer = document.getElementById('app');
    if (appContainer) {
        appContainer.style.display = 'block';
    }
    
    try {
        // Check if App is available (should always be available since window is created after backend is ready)
        
        if (!App || typeof App.GetEnabledTickers !== 'function') {
            const errorMsg = '[InitializeUI] CRITICAL ERROR: App.GetEnabledTickers not available - This is why tickers are not showing!';
            console.error('========================================');
            console.error(errorMsg);
            console.error('[InitializeUI] App:', App);
            console.error('[InitializeUI] App type:', typeof App);
            console.error('[InitializeUI] App keys:', App ? Object.keys(App) : 'N/A');
            console.error('========================================');
            
            // Log to backend terminal
            await logToBackend('ERROR', errorMsg);
            await logToBackend('ERROR', `App: ${App ? 'exists' : 'null'}, GetEnabledTickers: ${typeof App?.GetEnabledTickers}, App keys: ${App ? Object.keys(App).join(', ') : 'N/A'}`);
            // Show error message in table
            const tbody = document.getElementById('ticker-table-body');
            if (tbody) {
                tbody.innerHTML = '<tr><td colspan="8" style="text-align: center; color: #f44336;">Backend error - please restart the application</td></tr>';
                console.error('[InitializeUI] Set error message in table');
            } else {
                console.error('[InitializeUI] ERROR: ticker-table-body element not found!');
            }
            return;
        }
        
        // Get enabled tickers
        let tickers = await App.GetEnabledTickers();
        cachedEnabledTickers = tickers; // seed the 1s-loop cache
        
        // Sort tickers by saved order if available
        if (tickers && tickers.length > 0) {
            try {
                const settings = await App.GetSettings();
                if (settings && settings.TickerOrder && settings.TickerOrder.length > 0) {
                    const tickerOrder = settings.TickerOrder;
                    tickers = sortTickersByOrder(tickers, tickerOrder);
                }
            } catch (e) {
                console.warn('[InitializeUI] Could not load ticker order, using default:', e);
            }
        }
        
        if (!tickers || tickers.length === 0) {
            console.warn('[InitializeUI] WARNING: No enabled tickers found');
            console.warn('[InitializeUI] This is why the table is empty!');
            const tbody = document.getElementById('ticker-table-body');
            if (tbody) {
                tbody.innerHTML = '<tr><td colspan="8" style="text-align: center; color: #ff9800;">No tickers enabled. Please configure tickers in Settings.</td></tr>';
            } else {
                console.error('[InitializeUI] ERROR: ticker-table-body element not found!');
            }
            return;
        }
        
        await logToBackend('INFO', `[InitializeUI] Initializing ticker table with ${tickers.length} tickers`);
        // Initialize ticker table
        initializeTickerTable(tickers);
        await logToBackend('INFO', '[InitializeUI] Ticker table initialized');
        
        // Initialize date selector
        await initializeDateSelector();
        await logToBackend('INFO', '[InitializeUI] Date selector initialized');
        
        // Settings already initialized in initializeSettingsImmediate()
        
        // Start periodic updates
        startPeriodicUpdates();
        
    } catch (error) {
        console.error('========================================');
        console.error('[InitializeUI] ===== UI initialization FAILED =====');
        console.error('[InitializeUI] Error name:', error.name);
        console.error('[InitializeUI] Error message:', error.message);
        console.error('[InitializeUI] Error stack:', error.stack);
        console.error('[InitializeUI] Full error:', error);
        console.error('========================================');
        const tbody = document.getElementById('ticker-table-body');
        if (tbody) {
            tbody.innerHTML = `<tr><td colspan="9" style="text-align: center; color: #f44336;">Error loading tickers: ${error.message}</td></tr>`;
        } else {
            console.error('[InitializeUI] ERROR: ticker-table-body element not found!');
        }
    }
}

// Initialize ticker table
function initializeTickerTable(tickers) {
    const tbody = document.getElementById('ticker-table-body');
    
    // NOTE: We no longer register/unregister tickers for the main table
    // RegisterTickerDisplay/UnregisterTickerDisplay is only for CHART WINDOWS
    // This ensures only tickers with open charts get high priority polling
    
    tbody.innerHTML = '';
    
    tickers.forEach(ticker => {
        const row = document.createElement('tr');
        row.id = `ticker-row-${ticker}`;
        row.dataset.ticker = ticker;
        row.draggable = true;
        row.style.cursor = 'pointer';
        row.title = `Click to open chart for ${ticker}`;
        row.innerHTML = `
            <td class="drag-cell"><span class="drag-handle" title="Drag to reorder">☰</span></td>
            <td><strong>${ticker}</strong></td>
            <td id="${ticker}-spot">-</td>
            <td id="${ticker}-zero-gamma">-</td>
            <td id="${ticker}-pos-gamma">-</td>
            <td id="${ticker}-neg-gamma">-</td>
            <td id="${ticker}-net">-</td>
            <td id="${ticker}-last-update">-</td>
            <td class="open-cell"><button class="chart-btn" data-ticker="${ticker}">Chart</button><button class="chart-btn profile-open-btn" data-ticker="${ticker}">Profile</button></td>
        `;
        
        // Add click handler to open chart
        row.addEventListener('click', (e) => {
            // Don't trigger if clicking the button or drag handle
            if (e.target.tagName !== 'BUTTON' && !e.target.classList.contains('drag-handle')) {
                openChart(ticker);
            }
        });
        
        // Button click handler
        const btn = row.querySelector('.chart-btn:not(.profile-open-btn)');
        if (btn) {
            btn.addEventListener('click', (e) => {
                e.stopPropagation();
                openChart(ticker);
            });
        }
        const profileBtn = row.querySelector('.profile-open-btn');
        if (profileBtn) {
            profileBtn.addEventListener('click', (e) => {
                e.stopPropagation();
                openProfile(ticker);
            });
        }
        
        // Drag event handlers for reordering
        row.addEventListener('dragstart', (e) => {
            row.classList.add('dragging');
            e.dataTransfer.effectAllowed = 'move';
            e.dataTransfer.setData('text/plain', ticker);
        });
        
        row.addEventListener('dragend', () => {
            row.classList.remove('dragging');
            // Remove drag-over class from all rows
            tbody.querySelectorAll('tr').forEach(r => r.classList.remove('drag-over'));
        });
        
        row.addEventListener('dragover', (e) => {
            e.preventDefault();
            e.dataTransfer.dropEffect = 'move';
            const draggingRow = tbody.querySelector('.dragging');
            if (draggingRow && draggingRow !== row) {
                row.classList.add('drag-over');
            }
        });
        
        row.addEventListener('dragleave', () => {
            row.classList.remove('drag-over');
        });
        
        row.addEventListener('drop', (e) => {
            e.preventDefault();
            row.classList.remove('drag-over');
            const draggedTicker = e.dataTransfer.getData('text/plain');
            const draggedRow = tbody.querySelector(`[data-ticker="${draggedTicker}"]`);
            if (draggedRow && draggedRow !== row) {
                // Insert before the drop target
                tbody.insertBefore(draggedRow, row);
                // Save the new order
                saveTickerOrder();
            }
        });
        
        tbody.appendChild(row);
    });
}

// Save ticker order to settings
async function saveTickerOrder() {
    try {
        const tbody = document.getElementById('ticker-table-body');
        const rows = tbody.querySelectorAll('tr[data-ticker]');
        const order = Array.from(rows).map(row => row.dataset.ticker);
        
        // Get current settings
        const settings = await App.GetSettings();
        settings.TickerOrder = order;
        
        // Save settings
        await App.SaveSettings(settings);
        saveSettingsToCache(settings);
    } catch (error) {
        console.error('[Ticker Order] Error saving order:', error);
        setAppStatus('Ticker order was not saved', true);
    }
}

// Sort tickers by saved order, putting unordered tickers at the end
function sortTickersByOrder(tickers, savedOrder) {
    if (!savedOrder || savedOrder.length === 0) {
        return tickers;
    }
    
    const orderMap = new Map();
    savedOrder.forEach((ticker, index) => {
        orderMap.set(ticker, index);
    });
    
    return [...tickers].sort((a, b) => {
        const orderA = orderMap.has(a) ? orderMap.get(a) : savedOrder.length + tickers.indexOf(a);
        const orderB = orderMap.has(b) ? orderMap.get(b) : savedOrder.length + tickers.indexOf(b);
        return orderA - orderB;
    });
}

// Resize main window to fit ticker count
// Load settings UI (doesn't require backend - uses provided settings object)
function loadSettingsUI(settings) {
    settingsFormLoading = true;
    try {
        
        if (!settings) {
            settings = getDefaultSettings();
        }
        
        // Load basic fields
        const dataDirInput = document.getElementById('data-dir');
        if (dataDirInput) {
            dataDirInput.value = settings.DataDirectory || 'Tickers';
        }
        
        // Show API key status
        const apiKeyInput = document.getElementById('api-key');
        const apiKeyStatus = document.getElementById('api-key-status');
        if (apiKeyInput) {
            apiKeyInput.value = '';
            // Check if API key exists in settings
            const hasApiKey = settings.HasAPIKey === true;
            apiKeyInput.placeholder = hasApiKey ? '••••••••••••••••' : 'Enter your API key';
            if (apiKeyStatus) {
                apiKeyStatus.textContent = hasApiKey ? '✓ API key is saved' : 'No API key configured';
                apiKeyStatus.style.color = hasApiKey ? '#4CAF50' : '#ff9800';
            }
        }
        
        // Load subscription tiers
        const tiers = settings.APISubscriptionTiers || ['classic'];
        const classicCheckbox = document.getElementById('settings-tier-classic');
        const stateCheckbox = document.getElementById('settings-tier-state');
        const orderflowCheckbox = document.getElementById('settings-tier-orderflow');
        
        if (classicCheckbox) {
            classicCheckbox.checked = tiers.includes('classic');
        }
        if (stateCheckbox) {
            stateCheckbox.checked = tiers.includes('state');
        }
        if (orderflowCheckbox) {
            orderflowCheckbox.checked = tiers.includes('orderflow');
        }
        
        // Load data collection mode
        const collectAllRadio = document.getElementById('collect-all');
        const collectChartsRadio = document.getElementById('collect-charts');
        // Default to 'all' if not set (CollectAllEndpoints defaults to true)
        const collectAll = settings.CollectAllEndpoints !== false;
        if (collectAllRadio) collectAllRadio.checked = collectAll;
        if (collectChartsRadio) collectChartsRadio.checked = !collectAll;
        
        // Load ticker selection (organized by tier)
        loadTickerSelection(settings);
        
        // Load chart colors
        loadChartColors(settings);
        
        // Load general settings
        loadGeneralSettings(settings);
        
        // Load hidden plots settings
        loadHiddenPlots(settings);
        
    } catch (error) {
        console.error('[Settings UI] Error loading UI:', error);
    } finally {
        settingsFormLoading = false;
    }
}

// Layer and lookback checkboxes. A chart edits these through its own POSTs,
// so a later Save must read the saved values rather than the stale form.
let profileChoicesDirty = false;
let profileChoicesWatched = false;

function watchProfileChoices() {
    if (profileChoicesWatched) return;
    profileChoicesWatched = true;
    ['profile-show-priors', 'profile-layer-classic', 'profile-layer-state', 'profile-layer-gamma', 'profile-layer-delta', 'profile-layer-vanna', 'profile-layer-charm'].forEach((id) => {
        const input = document.getElementById(id);
        if (!input) return;
        input.addEventListener('change', () => { profileChoicesDirty = true; });
    });
}

function applyProfileChoiceFields(profile) {
    watchProfileChoices();
    const profileSettings = profile || {};
    const profilePriorsInput = document.getElementById('profile-show-priors');
    if (profilePriorsInput) {
        profilePriorsInput.checked = profileSettings.show_priors !== false;
    }
    const savedLayers = profileSettings.layers && typeof profileSettings.layers === 'object' ? profileSettings.layers : null;
    ['classic', 'state', 'gamma', 'delta', 'vanna', 'charm'].forEach((id) => {
        const input = document.getElementById('profile-layer-' + id);
        if (!input) return;
        input.checked = savedLayers ? savedLayers[id] === true : id === 'classic';
    });
    updateProfileCollectOptions();
}

async function refreshUntouchedProfileChoices() {
    if (profileChoicesDirty) return;
    try {
        const res = await fetch('/api/settings');
        if (!res.ok) return;
        const latest = await res.json();
        if (latest && latest.ProfileSettings) applyProfileChoiceFields(latest.ProfileSettings);
    } catch (e) {}
}

// Load general settings into UI
function loadGeneralSettings(settings) {
    try {
        
        // UseMarketTime
        const useMarketTimeCheckbox = document.getElementById('use-market-time');
        if (useMarketTimeCheckbox) {
            useMarketTimeCheckbox.checked = settings.UseMarketTime || false;
        }
        
        // Use24HourTime
        const use24HourTimeCheckbox = document.getElementById('use-24-hour-time');
        if (use24HourTimeCheckbox) {
            use24HourTimeCheckbox.checked = settings.Use24HourTime !== false;
        }
        
        // EnableLogging
        const enableLoggingCheckbox = document.getElementById('enable-logging');
        if (enableLoggingCheckbox) {
            // Default to true if not explicitly set to false
            enableLoggingCheckbox.checked = settings.EnableLogging !== false;
        }
        
        // HideConsole
        const hideConsoleCheckbox = document.getElementById('hide-console');
        if (hideConsoleCheckbox) {
            // Default to true if not explicitly set to false
            hideConsoleCheckbox.checked = settings.HideConsole !== false;
        }
        
        // ChartZoomFilterPercent
        const chartZoomFilterInput = document.getElementById('chart-zoom-filter');
        if (chartZoomFilterInput) {
            chartZoomFilterInput.value = settings.ChartZoomFilterPercent || 1.0;
        }
        
        // AutoFollowBufferPx
        const autoFollowBufferInput = document.getElementById('auto-follow-buffer');
        if (autoFollowBufferInput) {
            const px = Number(settings.AutoFollowBufferPx);
            autoFollowBufferInput.value = Number.isFinite(px) && px >= 0 ? px : 16;
        }
        const profileSettings = settings.ProfileSettings || {};
        const profileSideInput = document.getElementById('profile-side');
        if (profileSideInput) {
            profileSideInput.value = profileSettings.profile_side === 'left' ? 'left' : 'right';
        }
        const profileSideWidthInput = document.getElementById('profile-side-width');
        if (profileSideWidthInput) {
            const sideWidth = Number(profileSettings.side_width_percent);
            profileSideWidthInput.value = (Number.isFinite(sideWidth) && sideWidth > 0) ? sideWidth : 28;
        }
        const profileWidthInput = document.getElementById('profile-band-width');
        if (profileWidthInput) {
            const width = Number(profileSettings.bar_width_percent);
            profileWidthInput.value = (Number.isFinite(width) && width > 0) ? width : 22;
        }
        const profileXInput = document.getElementById('profile-band-x');
        if (profileXInput) {
            const x = Number(profileSettings.profile_x_percent);
            profileXInput.value = Number.isFinite(x) ? x : 62;
        }
        const profileEnabledInput = document.getElementById('profile-enabled');
        if (profileEnabledInput) {
            profileEnabledInput.checked = profileSettings.enabled === true;
        }
        applyProfileChoiceFields(profileSettings);
        // PriceAxisLocation
        const priceAxisLocationSelect = document.getElementById('price-axis-location');
        if (priceAxisLocationSelect) {
            const loc = (settings.PriceAxisLocation || 'left').toLowerCase();
            priceAxisLocationSelect.value = (loc === 'right') ? 'right' : 'left';
        }
    } catch (error) {
        console.error('[General Settings] Error loading:', error);
    }
}

// Load hidden plots settings into UI
function loadHiddenPlots(settings) {
    try {
        
        const hiddenPlots = settings.HiddenPlots || [];
        const plotCheckboxes = document.querySelectorAll('#hidden-plots-grid input[type="checkbox"]');
        
        plotCheckboxes.forEach(checkbox => {
            const plotName = checkbox.dataset.plot;
            if (plotName) {
                // Checkbox is CHECKED if plot is NOT in hiddenPlots array
                checkbox.checked = !hiddenPlots.includes(plotName);
            }
        });
    } catch (error) {
        console.error('[Hidden Plots] Error loading:', error);
    }
}

// Save general settings from UI to settings object
function saveGeneralSettings(settings) {
    
    const useMarketTimeCheckbox = document.getElementById('use-market-time');
    if (useMarketTimeCheckbox) {
        settings.UseMarketTime = useMarketTimeCheckbox.checked;
    }
    
    const use24HourTimeCheckbox = document.getElementById('use-24-hour-time');
    if (use24HourTimeCheckbox) {
        settings.Use24HourTime = use24HourTimeCheckbox.checked;
    }
    
    const enableLoggingCheckbox = document.getElementById('enable-logging');
    if (enableLoggingCheckbox) {
        settings.EnableLogging = enableLoggingCheckbox.checked;
    }
    
    const hideConsoleCheckbox = document.getElementById('hide-console');
    if (hideConsoleCheckbox) {
        settings.HideConsole = hideConsoleCheckbox.checked;
    }
    
    const chartZoomFilterInput = document.getElementById('chart-zoom-filter');
    if (chartZoomFilterInput) {
        const value = parseFloat(chartZoomFilterInput.value);
        if (!isNaN(value) && value >= 0.01 && value <= 100) {
            settings.ChartZoomFilterPercent = value;
        } else {
            settings.ChartZoomFilterPercent = 1.0; // Default if invalid
        }
    }
    
    const autoFollowBufferInput = document.getElementById('auto-follow-buffer');
    if (autoFollowBufferInput) {
        const value = parseFloat(autoFollowBufferInput.value);
        if (!isNaN(value) && value >= 0 && value <= 400) {
            settings.AutoFollowBufferPx = value;
        } else {
            settings.AutoFollowBufferPx = 16;
        }
    }
    const priceAxisLocationSelect = document.getElementById('price-axis-location');
    if (priceAxisLocationSelect) {
        settings.PriceAxisLocation = priceAxisLocationSelect.value === 'right' ? 'right' : 'left';
    }
    if (!settings.ProfileSettings || typeof settings.ProfileSettings !== 'object') {
        settings.ProfileSettings = {};
    }
    const profileSideInput = document.getElementById('profile-side');
    if (profileSideInput) {
        settings.ProfileSettings.profile_side = profileSideInput.value === 'left' ? 'left' : 'right';
    }
    const profileSideWidthInput = document.getElementById('profile-side-width');
    if (profileSideWidthInput) {
        const sideWidth = parseFloat(profileSideWidthInput.value);
        settings.ProfileSettings.side_width_percent = (!isNaN(sideWidth) && sideWidth >= 12 && sideWidth <= 50) ? sideWidth : 28;
    }
    const profileWidthInput = document.getElementById('profile-band-width');
    if (profileWidthInput) {
        const value = parseFloat(profileWidthInput.value);
        settings.ProfileSettings.bar_width_percent = (!isNaN(value) && value >= 8 && value <= 70) ? value : 22;
    }
    const profileXInput = document.getElementById('profile-band-x');
    if (profileXInput) {
        const value = parseFloat(profileXInput.value);
        settings.ProfileSettings.profile_x_percent = (!isNaN(value) && value >= 0 && value <= 100) ? value : 62;
    }
    const profilePriorsInput = document.getElementById('profile-show-priors');
    if (profilePriorsInput) {
        settings.ProfileSettings.show_priors = profilePriorsInput.checked;
    }
    const profileEnabledInput = document.getElementById('profile-enabled');
    if (profileEnabledInput) {
        settings.ProfileSettings.enabled = profileEnabledInput.checked;
    }
    const layers = {};
    ['classic', 'state', 'gamma', 'delta', 'vanna', 'charm'].forEach((id) => {
        const input = document.getElementById('profile-layer-' + id);
        if (input) layers[id] = input.checked;
    });
    settings.ProfileSettings.layers = layers;
}

function updateProfileCollectOptions() {
    const collectAll = document.getElementById('collect-all');
    const enabled = document.getElementById('profile-enabled');
    const box = document.getElementById('profile-layer-options');
    if (!box) return;
    const locked = (collectAll && collectAll.checked) || !(enabled && enabled.checked);
    box.querySelectorAll('input').forEach((input) => {
        input.disabled = locked;
    });
}
window.updateProfileCollectOptions = updateProfileCollectOptions;

// Save hidden plots from UI to settings object
function saveHiddenPlots(settings) {
    
    const hiddenPlots = [];
    const plotCheckboxes = document.querySelectorAll('#hidden-plots-grid input[type="checkbox"]');
    
    plotCheckboxes.forEach(checkbox => {
        const plotName = checkbox.dataset.plot;
        if (plotName && !checkbox.checked) {
            // If checkbox is NOT checked, the plot is hidden
            hiddenPlots.push(plotName);
        }
    });
    
    settings.HiddenPlots = hiddenPlots;
}

// Load settings from backend (optional - updates UI if available)
async function loadSettingsFromBackend() {
    try {
        
        // Wait for App to be available (retry up to 3 times, shorter timeout)
        let retries = 0;
        const maxRetries = 3;
        while ((!App || typeof App.GetSettings !== 'function') && retries < maxRetries) {
            await new Promise(resolve => setTimeout(resolve, 300));
            retries++;
            if (!App) {
                await loadAppBindings();
            }
        }
        
        if (!App || typeof App.GetSettings !== 'function') {
            return null; // Not an error - just use cache/defaults
        }
        
        const settings = await App.GetSettings();
        
        if (settings) {
            // Update UI with backend settings
            loadSettingsUI(settings);
            // Cache the settings
            saveSettingsToCache(settings);
            return settings;
        }
        
        return null;
    } catch (error) {
        console.warn('[Settings Backend] Error loading from backend:', error);
        return null; // Not a fatal error - use cache/defaults
    }
}

// Legacy function name for compatibility
async function loadSettings() {
    return await loadSettingsFromBackend();
}

// Load ticker selection UI
function loadTickerSelection(settings) {
    const tickerSelection = document.getElementById('ticker-selection');
    if (!tickerSelection) {
        console.warn('[Ticker Selection] Element not found');
        return;
    }
    
    try {
        tickerSelection.innerHTML = '';
        const tickerConfigs = settings.TickerConfigs || {};
        const organized = organizeTickersByTier(ALL_TICKERS);
        const tierOrder = ['Futures', 'Indexes', 'MAG7', 'Stocks', 'Other'];
        
        // Add search box
        const searchBox = document.createElement('input');
        searchBox.type = 'text';
        searchBox.id = 'ticker-search';
        searchBox.placeholder = 'Search tickers...';
        searchBox.style.cssText = 'width: 100%; padding: 0.5rem; margin-bottom: 1rem; background: #2a2a2a; border: 1px solid #3a3a3a; border-radius: 4px; color: #e0e0e0;';
        tickerSelection.appendChild(searchBox);
        
        // Add search functionality
        searchBox.addEventListener('input', (e) => {
            const searchTerm = e.target.value.toLowerCase();
            const allLabels = tickerSelection.querySelectorAll('label[data-ticker]');
            allLabels.forEach(label => {
                const ticker = label.getAttribute('data-ticker').toLowerCase();
                const tierSection = label.closest('.ticker-tier-section');
                if (ticker.includes(searchTerm)) {
                    label.style.display = 'flex';
                    if (tierSection) tierSection.style.display = 'block';
                } else {
                    label.style.display = 'none';
                }
            });
            // Hide empty tier sections
            tickerSelection.querySelectorAll('.ticker-tier-section').forEach(section => {
                const visibleLabels = section.querySelectorAll('label[data-ticker][style*="display: flex"], label[data-ticker]:not([style*="display: none"])');
                if (visibleLabels.length === 0 && searchTerm !== '') {
                    section.style.display = 'none';
                } else {
                    section.style.display = 'block';
                }
            });
        });
        
        let enabledCount = 0;
        tierOrder.forEach(tier => {
            const tickersInTier = organized[tier];
            if (tickersInTier.length === 0) return;
            
            // Create tier section (compact)
            const tierSection = document.createElement('div');
            tierSection.className = 'ticker-tier-section';
            tierSection.style.cssText = 'margin-bottom: 0.75rem;';
            
            const tierHeader = document.createElement('h4');
            tierHeader.textContent = `${tier} (${tickersInTier.length})`;
            tierHeader.style.cssText = 'color: #4CAF50; margin-bottom: 0.35rem; font-size: 0.85rem; font-weight: 600;';
            tierSection.appendChild(tierHeader);
            
            const tickerGrid = document.createElement('div');
            tickerGrid.className = 'ticker-grid';
            tickerGrid.style.cssText = 'display: grid; grid-template-columns: 1fr; gap: 0.15rem;';
            
            tickersInTier.forEach(ticker => {
                const config = tickerConfigs[ticker];
                const enabled = config && config.CollectionEnabled !== false; // Default to enabled if not specified
                if (enabled) enabledCount++;
                
                // Default refresh rate to 0 (auto/disabled), priority to medium
                // Note: use explicit check for undefined/null since 0 is a valid value
                const refreshRate = config && config.RefreshRateMs !== undefined && config.RefreshRateMs !== null ? config.RefreshRateMs : 0;
                const priority = config && config.Priority ? config.Priority : 'medium';
                
                // Create compact ticker row
                const tickerRow = document.createElement('div');
                tickerRow.className = 'ticker-config-row';
                tickerRow.dataset.ticker = ticker;
                tickerRow.style.cssText = 'display: flex; align-items: center; gap: 0.5rem; padding: 0.35rem 0.5rem; background: #1f1f1f; border-radius: 3px; margin-bottom: 0.25rem; border: 1px solid #2a2a2a; transition: all 0.15s;';
                tickerRow.addEventListener('mouseenter', () => {
                    tickerRow.style.background = '#2a2a2a';
                    tickerRow.style.borderColor = '#3a3a3a';
                });
                tickerRow.addEventListener('mouseleave', () => {
                    tickerRow.style.background = '#1f1f1f';
                    tickerRow.style.borderColor = '#2a2a2a';
                });
                
                // Drag handle for reordering
                const dragHandle = document.createElement('span');
                dragHandle.className = 'drag-handle';
                dragHandle.innerHTML = '☰';
                dragHandle.title = 'Drag to reorder';
                dragHandle.style.cssText = 'cursor: grab; color: #666; font-size: 0.9rem; padding: 0 0.25rem; user-select: none;';
                dragHandle.draggable = true;
                
                // Checkbox
                const checkbox = document.createElement('input');
                checkbox.type = 'checkbox';
                checkbox.id = `ticker-${ticker}`;
                checkbox.checked = enabled;
                checkbox.style.cssText = 'cursor: pointer; width: 16px; height: 16px; flex-shrink: 0;';
                checkbox.addEventListener('change', () => {
                    updateTickerEnabledCount();
                });
                
                // Ticker name (compact)
                const tickerName = document.createElement('span');
                tickerName.textContent = ticker;
                tickerName.style.cssText = 'user-select: none; min-width: 60px; font-weight: 600; color: #4CAF50; font-size: 0.85rem;';
                
                // Refresh rate input (compact, with placeholder)
                const refreshInput = document.createElement('input');
                refreshInput.type = 'number';
                refreshInput.id = `refresh-rate-${ticker}`;
                refreshInput.value = refreshRate;
                refreshInput.min = '0';
                refreshInput.step = '1000';
                refreshInput.placeholder = '0=Auto';
                refreshInput.title = 'Refresh rate in ms (0 = auto based on priority)';
                refreshInput.style.cssText = 'width: 70px; padding: 0.25rem 0.35rem; background: #2a2a2a; border: 1px solid #3a3a3a; border-radius: 3px; color: #e0e0e0; font-size: 0.8rem; text-align: center;';
                
                // Priority select (compact)
                const prioritySelect = document.createElement('select');
                prioritySelect.id = `priority-${ticker}`;
                prioritySelect.title = 'Polling priority';
                prioritySelect.style.cssText = 'width: 75px; padding: 0.25rem; background: #2a2a2a; border: 1px solid #3a3a3a; border-radius: 3px; color: #e0e0e0; font-size: 0.8rem; cursor: pointer;';
                ['high', 'medium', 'low'].forEach(p => {
                    const option = document.createElement('option');
                    option.value = p;
                    option.textContent = p.charAt(0).toUpperCase() + p.slice(1);
                    if (p === priority) option.selected = true;
                    prioritySelect.appendChild(option);
                });
                
                tickerRow.appendChild(dragHandle);
                tickerRow.appendChild(checkbox);
                tickerRow.appendChild(tickerName);
                tickerRow.appendChild(refreshInput);
                tickerRow.appendChild(prioritySelect);
                
                // Drag event handlers for settings reordering
                tickerRow.draggable = true;
                
                tickerRow.addEventListener('dragstart', (e) => {
                    tickerRow.classList.add('dragging');
                    e.dataTransfer.effectAllowed = 'move';
                    e.dataTransfer.setData('text/plain', ticker);
                });
                
                tickerRow.addEventListener('dragend', () => {
                    tickerRow.classList.remove('dragging');
                    tickerGrid.querySelectorAll('.ticker-config-row').forEach(r => r.classList.remove('drag-over'));
                });
                
                tickerRow.addEventListener('dragover', (e) => {
                    e.preventDefault();
                    e.dataTransfer.dropEffect = 'move';
                    const draggingRow = tickerGrid.querySelector('.dragging');
                    if (draggingRow && draggingRow !== tickerRow) {
                        tickerRow.classList.add('drag-over');
                    }
                });
                
                tickerRow.addEventListener('dragleave', () => {
                    tickerRow.classList.remove('drag-over');
                });
                
                tickerRow.addEventListener('drop', (e) => {
                    e.preventDefault();
                    tickerRow.classList.remove('drag-over');
                    const draggedTicker = e.dataTransfer.getData('text/plain');
                    const draggedRow = tickerGrid.querySelector(`[data-ticker="${draggedTicker}"]`);
                    if (draggedRow && draggedRow !== tickerRow) {
                        tickerGrid.insertBefore(draggedRow, tickerRow);
                        scheduleSettingsSave(true);
                    }
                });
                
                tickerGrid.appendChild(tickerRow);
            });
            
            tierSection.appendChild(tickerGrid);
            tickerSelection.appendChild(tierSection);
        });
        
        // Show enabled count
        const countDisplay = document.createElement('div');
        countDisplay.id = 'ticker-enabled-count';
        countDisplay.textContent = `${enabledCount} ticker${enabledCount !== 1 ? 's' : ''} enabled`;
        countDisplay.style.cssText = 'margin-top: 1rem; padding: 0.5rem; background: #2a2a2a; border-radius: 4px; text-align: center; color: #4CAF50; font-weight: 600;';
        tickerSelection.appendChild(countDisplay);
        
    } catch (error) {
        console.error('[Ticker Selection] Error loading:', error);
        const tickerSelection = document.getElementById('ticker-selection');
        if (tickerSelection) {
            tickerSelection.innerHTML = `<div style="padding: 1rem; color: #f44336;">Error loading tickers: ${error.message}</div>`;
        }
    }
}

// Update ticker enabled count display
function updateTickerEnabledCount() {
    const countDisplay = document.getElementById('ticker-enabled-count');
    if (!countDisplay) return;
    
    let enabledCount = 0;
    ALL_TICKERS.forEach(ticker => {
        const checkbox = document.getElementById(`ticker-${ticker}`);
        if (checkbox && checkbox.checked) {
            enabledCount++;
        }
    });
    
    countDisplay.textContent = `${enabledCount} ticker${enabledCount !== 1 ? 's' : ''} enabled`;
}

// Load chart colors into UI
function loadChartColors(settings) {
    try {
        const colorsGrid = document.getElementById('chart-colors-grid');
        if (!colorsGrid) {
            console.warn('[Chart Colors] chart-colors-grid element not found');
            return;
        }
        
        colorsGrid.innerHTML = '';
        
        // Default chart colors
        const defaultColors = getDefaultChartColors();
        
        // Get chart colors from settings, use defaults if not available
        let chartColors = {};
        const colorsOff = getChartColorsOffFromSettings(settings);
        if (settings && settings.ChartColors) {
            chartColors = settings.ChartColors;
        } else {
        }
        
        CHART_SERIES_METADATA.forEach(series => {
            let colorValue = chartColors[series.key] || defaultColors[series.key];
            if (isChartColorOffValue(colorValue)) {
                colorValue = defaultColors[series.key];
            }
            const isOff = colorsOff.has(series.key);
            
            const colorItem = document.createElement('div');
            colorItem.style.cssText = 'display: flex; flex-direction: column; gap: 0.25rem;';
            
            const label = document.createElement('label');
            label.textContent = series.label;
            label.style.cssText = 'font-size: 0.85rem; color: #aaa;';
            
            const controlsRow = document.createElement('div');
            controlsRow.className = 'chart-color-controls';
            
            const colorInput = document.createElement('input');
            colorInput.type = 'color';
            colorInput.id = `color-${series.key}`;
            colorInput.value = colorValue;
            colorInput.disabled = isOff;
            colorInput.style.cssText = 'flex: 1; min-width: 0; height: 40px; border: 1px solid #3a3a3a; border-radius: 4px; cursor: pointer; opacity: ' + (isOff ? '0.45' : '1') + ';';
            
            const offLabel = document.createElement('label');
            offLabel.className = 'chart-color-off-label';
            offLabel.title = 'Hide on chart (data still collected)';
            
            const offCheckbox = document.createElement('input');
            offCheckbox.type = 'checkbox';
            offCheckbox.id = `color-off-${series.key}`;
            offCheckbox.checked = isOff;
            
            const offText = document.createElement('span');
            offText.textContent = 'Off';
            
            offLabel.appendChild(offCheckbox);
            offLabel.appendChild(offText);
            
            offCheckbox.addEventListener('change', () => {
                const off = offCheckbox.checked;
                colorInput.disabled = off;
                colorInput.style.opacity = off ? '0.45' : '1';
                colorInput.style.cursor = off ? 'not-allowed' : 'pointer';
            });
            
            controlsRow.appendChild(colorInput);
            controlsRow.appendChild(offLabel);
            colorItem.appendChild(label);
            colorItem.appendChild(controlsRow);
            colorsGrid.appendChild(colorItem);
        });
        
        
        // Reset colors button handler (remove old listeners first)
        const resetBtn = document.getElementById('reset-colors');
        if (resetBtn) {
            // Clone and replace to remove old event listeners
            const newResetBtn = resetBtn.cloneNode(true);
            resetBtn.parentNode.replaceChild(newResetBtn, resetBtn);
            
            newResetBtn.addEventListener('click', () => {
                CHART_SERIES_METADATA.forEach(series => {
                    const input = document.getElementById(`color-${series.key}`);
                    if (input) {
                        input.value = defaultColors[series.key];
                        input.disabled = false;
                        input.style.opacity = '1';
                        input.style.cursor = 'pointer';
                    }
                    const offInput = document.getElementById(`color-off-${series.key}`);
                    if (offInput) offInput.checked = false;
                });
                scheduleSettingsSave(false);
            });
        }
    } catch (error) {
        console.error('[Chart Colors] Error loading colors:', error);
        const colorsGrid = document.getElementById('chart-colors-grid');
        if (colorsGrid) {
            colorsGrid.innerHTML = `<div style="padding: 1rem; color: #f44336;">Error loading colors: ${error.message}</div>`;
        }
    }
}

// Save chart colors from UI to settings
function saveChartColors(settings) {
    if (!settings.ChartColors) {
        settings.ChartColors = {};
    }
    const colorsOff = [];
    CHART_SERIES_METADATA.forEach(series => {
        const input = document.getElementById(`color-${series.key}`);
        const offInput = document.getElementById(`color-off-${series.key}`);
        if (input) {
            settings.ChartColors[series.key] = input.value;
        }
        if (offInput && offInput.checked) {
            colorsOff.push(series.key);
        }
    });
    settings.ChartColorsOff = colorsOff;
}

// Save settings
async function saveSettings(options) {
    const refreshTickers = !options || options.refreshTickers !== false;
    try {
        
        // Get current settings (from backend if available, otherwise from cache/defaults)
        let settings = null;
        if (App && typeof App.GetSettings === 'function') {
            try {
                settings = await App.GetSettings();
            } catch (error) {
                console.warn('[Save Settings] Failed to load from backend, using cache/defaults:', error);
            }
        }
        
        // Fallback to cache or defaults
        if (!settings) {
            const cached = loadSettingsFromCache();
            settings = cached?.settings || getDefaultSettings();
        }
        
        if (!settings) {
            settings = getDefaultSettings();
        }
        
        // Update settings from UI
        const dataDirInput = document.getElementById('data-dir');
        if (dataDirInput) {
            settings.DataDirectory = dataDirInput.value || 'Tickers';
        }
        
        // If user entered a new API key in the settings form, include it so it is saved and poller uses it
        const apiKeyInput = document.getElementById('api-key');
        const apiKeyStatus = document.getElementById('api-key-status');
        const typedAPIKey = apiKeyInput ? apiKeyInput.value.trim() : '';
        if (typedAPIKey !== '') {
            settings.APITKey = typedAPIKey;
        }
        
        // Save subscription tiers - read checkboxes carefully
        const tiers = [];
        const classicCheckbox = document.getElementById('settings-tier-classic');
        const stateCheckbox = document.getElementById('settings-tier-state');
        const orderflowCheckbox = document.getElementById('settings-tier-orderflow');
        
        if (classicCheckbox && classicCheckbox.checked) {
            tiers.push('classic');
        }
        if (stateCheckbox && stateCheckbox.checked) {
            tiers.push('state');
        }
        if (orderflowCheckbox && orderflowCheckbox.checked) {
            tiers.push('orderflow');
        }
        
        if (tiers.length === 0) {
            tiers.push('classic'); // Default to classic if none selected
        }
        
        settings.APISubscriptionTiers = tiers;
        
        // Save data collection mode
        const collectAllRadio = document.getElementById('collect-all');
        settings.CollectAllEndpoints = collectAllRadio ? collectAllRadio.checked : true;
        
        // Save ticker selection and polling preferences
        if (!settings.TickerConfigs) {
            settings.TickerConfigs = {};
        }
        
        let enabledCount = 0;
        ALL_TICKERS.forEach(ticker => {
            const checkbox = document.getElementById(`ticker-${ticker}`);
            const enabled = checkbox ? checkbox.checked : false;
            if (enabled) enabledCount++;
            
            // Get polling preferences
            const refreshRateInput = document.getElementById(`refresh-rate-${ticker}`);
            const prioritySelect = document.getElementById(`priority-${ticker}`);
            
            let refreshRate = 0; // Default to 0 (auto)
            if (refreshRateInput) {
                const value = parseInt(refreshRateInput.value);
                if (!isNaN(value) && value >= 0) {
                    refreshRate = value;
                }
            }
            
            let priority = 'medium'; // Default
            if (prioritySelect) {
                priority = prioritySelect.value || 'medium';
            }
            
            if (!settings.TickerConfigs[ticker]) {
                settings.TickerConfigs[ticker] = {
                    Display: enabled,
                    CollectionEnabled: enabled,
                    Priority: priority,
                    RefreshRateMs: refreshRate
                };
            } else {
                // Update existing config
                const config = settings.TickerConfigs[ticker];
                config.CollectionEnabled = enabled;
                config.Display = enabled;
                config.Priority = priority;
                config.RefreshRateMs = refreshRate;
                settings.TickerConfigs[ticker] = config;
            }
        });
        
        // Save chart colors
        saveChartColors(settings);
        
        // Chart layer and lookback edits land on the server before this Save.
        // Pull those into the form unless this window edited them itself.
        await refreshUntouchedProfileChoices();

        // Save general settings (UseMarketTime, EnableLogging, HideConsole)
        saveGeneralSettings(settings);
        
        // Save hidden plots settings
        saveHiddenPlots(settings);
        
        // Save ticker order from settings modal
        const tickerOrder = [];
        document.querySelectorAll('#ticker-selection .ticker-config-row[data-ticker]').forEach(row => {
            tickerOrder.push(row.dataset.ticker);
        });
        if (tickerOrder.length > 0) {
            settings.TickerOrder = tickerOrder;
        }
        
        
        // Save to cache immediately (works even without backend)
        saveSettingsToCache(settings);
        
        // Try to save to backend if available
        if (App && typeof App.SaveSettings === 'function') {
            try {
                await App.SaveSettings(settings);
                profileChoicesDirty = false;
                if (typedAPIKey !== '' && apiKeyInput) {
                    apiKeyInput.value = '';
                    if (apiKeyStatus) {
                        apiKeyStatus.textContent = '✓ API key is saved';
                        apiKeyStatus.style.color = '#4CAF50';
                    }
                }
            } catch (error) {
                console.warn('[Save Settings] Failed to save to backend, but cached:', error);
                setAppStatus('Settings were not saved', true);
            }
        } else {
            setAppStatus('Settings were not saved', true);
        }
        
        // Broadcast settings update to all chart windows so they can refresh colors
        try {
            const settingsChannel = new BroadcastChannel('market-terminal-settings');
            settingsChannel.postMessage({ type: 'settings-updated', settings: settingsWithoutKey(settings) });
            settingsChannel.close();
        } catch (e) {
            console.warn('[Save Settings] Failed to broadcast settings update:', e);
        }
        
        if (!refreshTickers) return;

        if (App && typeof App.GetEnabledTickers === 'function') {
            try {
                let enabledTickers = await App.GetEnabledTickers();
                cachedEnabledTickers = enabledTickers; // refresh the 1s-loop cache
                
                if (enabledTickers && enabledTickers.length > 0) {
                    // Sort by the ticker order we just saved
                    if (settings.TickerOrder && settings.TickerOrder.length > 0) {
                        enabledTickers = sortTickersByOrder(enabledTickers, settings.TickerOrder);
                    }
                    initializeTickerTable(enabledTickers);
                    startPeriodicUpdates(); // Restart updates with new tickers
                } else {
                    console.warn('[Save Settings] No tickers enabled! GetEnabledTickers returned empty array');
                    // No tickers enabled - clear table
                    const tbody = document.getElementById('ticker-table-body');
                    if (tbody) {
                        tbody.innerHTML = '<tr><td colspan="8" style="text-align: center; color: #ff9800;">No tickers enabled. Please configure tickers in Settings.</td></tr>';
                    }
                }
            } catch (error) {
                console.error('[Save Settings] Failed to refresh main page tickers:', error);
            }
        } else {
            console.warn('[Save Settings] App.GetEnabledTickers not available');
        }
    } catch (saveError) {
        console.error('[Save Settings] Error saving settings:', saveError);
        throw saveError;
    }
}

// Periodic updates interval
let periodicUpdateInterval = null;
// Market date rollover check: when user is viewing "today", advance to new date after rollover (e.g. market open)
let marketDateRolloverInterval = null;

// Cached enabled-tickers list: seeded at init and refreshed on settings save,
// so the 1s update loop doesn't ask the backend for it on every tick
let cachedEnabledTickers = null;

async function getEnabledTickersCached() {
    if (cachedEnabledTickers === null) {
        cachedEnabledTickers = await App.GetEnabledTickers();
    }
    return cachedEnabledTickers;
}

// Start periodic updates
function startPeriodicUpdates() {
    // Clear existing interval if any
    if (periodicUpdateInterval) {
        clearInterval(periodicUpdateInterval);
        periodicUpdateInterval = null;
    }
    if (marketDateRolloverInterval) {
        clearInterval(marketDateRolloverInterval);
        marketDateRolloverInterval = null;
    }
    
    // Update every 1 second to reflect high-priority ticker updates
    periodicUpdateInterval = setInterval(async () => {
        await updateTickerData();
    }, 1000);
    
    // Check for market date rollover every 60s (e.g. app started before open; after 8:30 AM ET we should show today)
    marketDateRolloverInterval = setInterval(async () => {
        if (selectedDate === null || lastKnownMarketDate === null) return;
        if (selectedDate !== lastKnownMarketDate) return; // user is viewing a past date, don't auto-advance
        try {
            const currentMarketDate = await fetchMarketDate();
            if (!currentMarketDate) return;
            if (currentMarketDate === selectedDate) return; // no rollover
            // Market date rolled over (e.g. new day at 8:30 AM ET); switch to new date and refresh list
            lastKnownMarketDate = currentMarketDate;
            selectedDate = currentMarketDate;
            await loadAvailableDates();
            // Ensure dropdown and state show the new date (loadAvailableDates may have picked another if new date wasn't in list yet)
            selectedDate = currentMarketDate;
            const dateSelector = document.getElementById('date-selector');
            if (dateSelector) {
                for (let i = 0; i < dateSelector.options.length; i++) {
                    if (dateSelector.options[i].value === currentMarketDate) {
                        dateSelector.selectedIndex = i;
                        break;
                    }
                }
            }
            await updateTickerData();
        } catch (e) {
            // ignore
        }
    }, 60000);
    
    // Initial update
    updateTickerData();
}

// Update ticker data (parallelized for performance)
const loggedNoData = new Set();

let tickerUpdateGeneration = 0;

async function updateTickerData() {
    const generation = ++tickerUpdateGeneration;
    try {
        const tickers = await getEnabledTickersCached();
        // Determine which date to use for loading data
        let dateStr = selectedDate;
        const viewingToday = (selectedDate === lastKnownMarketDate);
        const useBackendToday = !dateStr || viewingToday;

        if (!dateStr) {
            try {
                const fetched = await fetchMarketDate();
                if (fetched) {
                    dateStr = fetched;
                    lastKnownMarketDate = fetched;
                } else {
                    dateStr = new Date().toISOString().split('T')[0];
                }
            } catch (error) {
                console.warn('[UpdateTickerData] Failed to get market date, using today:', error);
                dateStr = new Date().toISOString().split('T')[0];
            }
        }
        // Note: when viewing "today", market-date rollover is handled by the
        // 60s marketDateRolloverInterval - no per-second fetch here.

        // When showing "today", pass "" so backend resolves current market date at request time (rollover without restart)
        const dateStrForRequest = useBackendToday ? '' : dateStr;
        // Fetch all tickers in parallel for better performance
        const promises = tickers.map(ticker => 
            App.GetTickerData(ticker, dateStrForRequest)
                .then(data => ({ ticker, data, error: null }))
                .catch(error => ({ ticker, data: null, error }))
        );
        
        // Wait for all requests to complete
        const results = await Promise.all(promises);
        if (generation !== tickerUpdateGeneration) {
            return;
        }
        
        // Process results
        results.forEach(({ ticker, data, error }) => {
            if (error) {
                console.error(`Error loading data for ${ticker}:`, error);
                // Show error in table
                const row = document.getElementById(`${ticker}-spot`);
                if (row) {
                    row.textContent = 'Error';
                    row.title = error.message;
                }
            } else if (data && data.timestamp && Array.isArray(data.timestamp) && data.timestamp.length > 0) {
                updateTickerRow(ticker, data);
            } else {
                const noDataKey = ticker + '|' + (dateStrForRequest === '' ? 'today' : dateStr);
                if (!loggedNoData.has(noDataKey)) {
                    loggedNoData.add(noDataKey);
                }
                const row = document.getElementById(`${ticker}-spot`);
                if (row) {
                    row.textContent = 'No data';
                }
            }
        });
    } catch (error) {
        console.error('Error updating ticker data:', error);
    }
}

// Update ticker row in table
function updateTickerRow(ticker, data) {
    // NOTE: Do NOT register tickers here - RegisterTickerDisplay is only for CHART WINDOWS
    // The main table rows should NOT be treated as "displayed" for priority scheduling
    // Chart windows call RegisterTickerDisplay when opened via OpenChartWindow
    
    const spot = getLatestValue(data, 'spot');
    const zeroGamma = getLatestValue(data, 'zero_gamma');
    const posGamma = getLatestValue(data, 'major_pos_vol');
    const negGamma = getLatestValue(data, 'major_neg_vol');
    const netGamma = computeNetGamma(spot, posGamma, negGamma);
    
    if (spot !== null) {
        document.getElementById(`${ticker}-spot`).textContent = formatNumber(spot);
    }
    if (zeroGamma !== null) {
        document.getElementById(`${ticker}-zero-gamma`).textContent = formatNumber(zeroGamma);
    }
    if (posGamma !== null) {
        document.getElementById(`${ticker}-pos-gamma`).textContent = formatNumber(posGamma);
    }
    if (negGamma !== null) {
        document.getElementById(`${ticker}-neg-gamma`).textContent = formatNumber(negGamma);
    }
    if (netGamma !== null) {
        document.getElementById(`${ticker}-net`).textContent = formatNumber(netGamma);
    }
    
    // Get API timestamp from data (when the data was actually collected)
    const timestamps = data.timestamp;
    if (timestamps && Array.isArray(timestamps) && timestamps.length > 0) {
        const latestTimestamp = timestamps[timestamps.length - 1];
        // Convert Unix timestamp (seconds) to readable time
        const date = new Date(latestTimestamp * 1000);
        document.getElementById(`${ticker}-last-update`).textContent = formatTimeForDisplay(date);
    } else {
        // Fallback to current time if no timestamp available
        document.getElementById(`${ticker}-last-update`).textContent = formatTimeForDisplay(new Date());
    }
}

// Get latest value from data array
function getLatestValue(data, field) {
    if (!data[field] || !Array.isArray(data[field]) || data[field].length === 0) {
        return null;
    }
    const values = data[field];
    return values[values.length - 1];
}

// Format number for display
function formatNumber(value) {
    if (value === null || value === undefined) {
        return '-';
    }
    if (typeof value === 'number') {
        return value.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
    }
    return String(value);
}

// Today stays empty so the window resolves the session date on each read.
// A historical selection is pinned to that calendar day.
function windowDateArg() {
    if (!selectedDate || selectedDate === lastKnownMarketDate) {
        return '';
    }
    return selectedDate;
}

// Open chart for a ticker in a new window
async function openChart(ticker) {
    try {
        
        if (!App.OpenChartWindow) {
            throw new Error('OpenChartWindow method not found in App service. Try restarting the application.');
        }
        
        await App.OpenChartWindow(ticker, windowDateArg());
    } catch (error) {
        console.error(`Error opening chart for ${ticker}:`, error);
        alert(`Error opening chart: ${error.message}`);
    }
}

// Open a profile-only window. The chart for this ticker stays as it is.
async function openProfile(ticker) {
    try {
        if (!App.OpenProfileWindow) {
            throw new Error('OpenProfileWindow method not found. Restart the application to load it.');
        }
        await App.OpenProfileWindow(ticker, windowDateArg());
    } catch (error) {
        console.error(`Error opening profile for ${ticker}:`, error);
        alert(`Error opening profile: ${error.message}`);
    }
}

// Initialize date selector
async function initializeDateSelector() {
    const dateSelector = document.getElementById('date-selector');
    const todayBtn = document.getElementById('today-btn');
    
    if (!dateSelector) {
        console.error('[Date Selector] date-selector element not found');
        return;
    }
    
    // Load available dates
    await loadAvailableDates();
    
    // Set up change handler
    dateSelector.addEventListener('change', async (e) => {
        const index = e.target.selectedIndex;
        if (index >= 0) {
            const option = e.target.options[index];
            const dateStr = option.value;
            if (dateStr) {
                await onDateChanged(dateStr);
            }
        }
    });
    
    // Set up Today button
    if (todayBtn) {
        todayBtn.addEventListener('click', async () => {
            await setDateToToday();
        });
    }
}

// Load available dates from backend
async function loadAvailableDates() {
    const dateSelector = document.getElementById('date-selector');
    if (!dateSelector) return;
    
    try {
        dateSelector.innerHTML = '<option>Loading dates...</option>';
        
        const response = await fetch('/api/available-dates');
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        
        const dates = await response.json();
        
        // Get current market date for comparison
        let todayStr = null;
        try {
            todayStr = await fetchMarketDate();
        } catch (error) {
            console.warn('[Date Selector] Failed to get market date:', error);
        }
        
        // Clear and populate dropdown
        dateSelector.innerHTML = '';
        
        if (dates.length === 0) {
            dateSelector.innerHTML = '<option>No dates available</option>';
            selectedDate = null;
            return;
        }
        
        dates.forEach(dateStr => {
            const option = document.createElement('option');
            option.value = dateStr;
            
            // Format date as "MM/DD/YYYY"
            const [year, month, day] = dateStr.split('-');
            const formattedDate = `${month}/${day}/${year}`;
            
            // Add "(Today)" suffix if it's today
            if (dateStr === todayStr) {
                option.textContent = `${formattedDate} (Today)`;
            } else {
                option.textContent = formattedDate;
            }
            
            dateSelector.appendChild(option);
        });
        
        // Select today by default if available, otherwise select first (newest) date
        let defaultIndex = 0;
        if (todayStr) {
            for (let i = 0; i < dates.length; i++) {
                if (dates[i] === todayStr) {
                    defaultIndex = i;
                    break;
                }
            }
        }
        
        dateSelector.selectedIndex = defaultIndex;
        selectedDate = dates[defaultIndex];
        // Track "today" so we can auto-advance when market date rolls over (e.g. at market open)
        // If todayStr is null (fetch failed), treat selected date as "today" so we still use live date when available
        lastKnownMarketDate = todayStr || selectedDate;
        
        
        // Trigger initial date change to load data
        await onDateChanged(selectedDate);
        
    } catch (error) {
        console.error('[Date Selector] Error loading dates:', error);
        dateSelector.innerHTML = '<option>Error loading dates</option>';
        selectedDate = null;
    }
}

// Handle date change
async function onDateChanged(dateStr) {
    selectedDate = dateStr;
    
    // Refresh ticker table with new date
    await updateTickerData();
}

// Set date selector to today
async function setDateToToday() {
    try {
        const todayStr = await fetchMarketDate();
        if (!todayStr) {
            throw new Error('Failed to get market date');
        }
        lastKnownMarketDate = todayStr;
        selectedDate = todayStr;

        const dateSelector = document.getElementById('date-selector');
        if (!dateSelector) return;

        // Refresh date list first (backend always includes current market date now)
        await loadAvailableDates();

        // Ensure today is selected (list may have been rebuilt by loadAvailableDates)
        for (let i = 0; i < dateSelector.options.length; i++) {
            if (dateSelector.options[i].value === todayStr) {
                dateSelector.selectedIndex = i;
                selectedDate = todayStr;
                await updateTickerData();
                return;
            }
        }

        // If today still not in dropdown, add it and select (fallback)
        const option = document.createElement('option');
        option.value = todayStr;
        const [y, m, d] = todayStr.split('-');
        option.textContent = `${m}/${d}/${y} (Today)`;
        dateSelector.insertBefore(option, dateSelector.options[0]);
        dateSelector.selectedIndex = 0;
        selectedDate = todayStr;
        await updateTickerData();
    } catch (error) {
        console.error('[Date Selector] Error setting to today:', error);
    }
}

// Show startup wizard
function showStartupWizard() {
    // Hide loading overlay
    const loadingOverlay = document.getElementById('loading-overlay');
    if (loadingOverlay) {
        loadingOverlay.style.display = 'none';
    }
    
    // Show the app container (needed for the modal to be visible)
    const appContainer = document.getElementById('app');
    if (appContainer) {
        appContainer.style.display = 'block';
    }
    
    const wizardModal = document.getElementById('startup-wizard-modal');
    if (!wizardModal) {
        console.error('Startup wizard modal not found');
        return;
    }
    
    // Show modal - ensure it's visible
    wizardModal.removeAttribute('hidden');
    wizardModal.style.display = 'block';
    wizardModal.style.visibility = 'visible';
    wizardModal.style.opacity = '1';
    wizardModal.style.pointerEvents = 'auto';
    const wizardKey = document.getElementById('wizard-api-key');
    if (wizardKey) wizardKey.focus();
    trapModalFocus(wizardModal, null);
    
    // Handle wizard completion - use addEventListener to avoid multiple handlers
    const completeBtn = document.getElementById('wizard-complete');
    if (completeBtn) {
        // Remove any existing listeners by replacing the button
        const newCompleteBtn = completeBtn.cloneNode(true);
        completeBtn.parentNode.replaceChild(newCompleteBtn, completeBtn);
        
        // Get reference to the new button
        const btn = document.getElementById('wizard-complete');
        btn.addEventListener('click', async () => {
            try {
                const apiKey = document.getElementById('wizard-api-key').value.trim();
                const tiers = [];
                if (document.getElementById('wizard-tier-classic').checked) tiers.push('classic');
                if (document.getElementById('wizard-tier-state').checked) tiers.push('state');
                if (document.getElementById('wizard-tier-orderflow').checked) tiers.push('orderflow');
                
                if (!apiKey) {
                    alert('Please enter your API key');
                    return;
                }
                
                if (tiers.length === 0) {
                    alert('Please select at least one subscription tier');
                    return;
                }
                
                // Disable button during setup
                btn.disabled = true;
                btn.textContent = 'Saving...';
                
                
                if (apiKey.length === 0) {
                    console.error('ERROR: API key is empty before calling CompleteSetup!');
                    alert('API key cannot be empty!');
                    btn.disabled = false;
                    btn.textContent = 'Save & Continue';
                    return;
                }
                
                // Complete setup (use default tickers)
                try {
                    const result = await App.CompleteSetup(apiKey, tiers, []);
                } catch (setupError) {
                    console.error('Setup error:', setupError);
                    alert('Error completing setup: ' + (setupError.message || String(setupError)));
                    btn.disabled = false;
                    btn.textContent = 'Save & Continue';
                    return;
                }
                
                // Close modal IMMEDIATELY - don't wait for verification
                wizardModal.style.display = 'none';
                wizardModal.style.visibility = 'hidden';
                wizardModal.style.opacity = '0';
                wizardModal.style.pointerEvents = 'none';
                wizardModal.setAttribute('hidden', 'true');
                
                // Also hide the modal content
                const modalContent = wizardModal.querySelector('.modal-content');
                if (modalContent) {
                    modalContent.style.display = 'none';
                }
                
                // Force a reflow
                void wizardModal.offsetHeight;
                
                // Initialize UI immediately
                try {
                    await initializeUI();
                } catch (uiError) {
                    console.error('Error initializing UI:', uiError);
                    alert('Setup completed but there was an error initializing the UI: ' + (uiError.message || String(uiError)));
                }
            } catch (error) {
                console.error('Error completing setup:', error);
                alert('Error completing setup: ' + error.message);
                // Re-enable button on error
                btn.disabled = false;
                btn.textContent = 'Save & Continue';
            }
        });
    }
    
    // Allow closing by clicking outside (on backdrop)
    wizardModal.addEventListener('click', (e) => {
        if (e.target === wizardModal) {
            // Don't allow closing by clicking outside during first run
            // User must complete setup
        }
    });
}
