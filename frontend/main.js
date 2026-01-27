// Main JavaScript entry point for Market Terminal Gexbot

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
            console.log('[Runtime] Wails runtime detected at window._wails.runtime.Call after', i * delayMs, 'ms');
            return true;
        }
        if (window._wails && window._wails.Call) {
            console.log('[Runtime] Wails runtime detected at window._wails.Call after', i * delayMs, 'ms');
            return true;
        }
        if (window.Wails && window.Wails.Call) {
            console.log('[Runtime] Wails runtime detected at window.Wails.Call after', i * delayMs, 'ms');
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
        console.log('[Bindings] ===== Loading App bindings =====');
        console.log('[Bindings] Step 1: Checking Wails runtime availability...');
        console.log('[Bindings] window._wails:', !!window._wails, window._wails);
        console.log('[Bindings] window.Wails:', !!window.Wails, window.Wails);
        console.log('[Bindings] window.wails:', !!window.wails, window.wails);
        console.log('[Bindings] window.location:', window.location.href);
        
        // Wait for Wails runtime (should be ready since window is created after backend init)
        console.log('[Bindings] Step 2: Waiting for Wails runtime...');
        console.log('[Bindings] Current runtime state - window._wails:', window._wails);
        console.log('[Bindings] Current runtime state - window.Wails:', window.Wails);
        const runtimeReady = await waitForWailsRuntime(50, 100); // Wait up to 5 seconds
        console.log('[Bindings] Runtime ready:', runtimeReady);
        if (runtimeReady) {
            console.log('[Bindings] Runtime details - window._wails.runtime:', window._wails?.runtime);
            console.log('[Bindings] Runtime details - window._wails.runtime.Call:', window._wails?.runtime?.Call);
        }
        
        if (!runtimeReady) {
            console.warn('[Bindings] WARNING: Wails runtime not detected after waiting');
            console.warn('[Bindings] Continuing anyway - bindings might still work...');
        }
        
        console.log('[Bindings] Step 3: Importing bindings module...');
        // Import bindings (they will use the shim via import map)
        const appModule = await import('./bindings/market-terminal/app.js');
        console.log('[Bindings] Module imported:', appModule);
        console.log('[Bindings] Module keys:', Object.keys(appModule));
        console.log('[Bindings] Module has LogFrontend?', 'LogFrontend' in appModule);
        console.log('[Bindings] typeof appModule.LogFrontend:', typeof appModule.LogFrontend);
        
        App = appModule;
        console.log('[Bindings] Step 4: App bindings assigned');
        console.log('[Bindings] App object:', App);
        console.log('[Bindings] App type:', typeof App);
        console.log('[Bindings] Available methods:', App ? Object.keys(App) : 'App is null');
        console.log('[Bindings] App.LogFrontend exists?', App && 'LogFrontend' in App);
        console.log('[Bindings] App.LogFrontend type:', App ? typeof App.LogFrontend : 'N/A');
        
        if (!App) {
            throw new Error('App module is null or undefined after import');
        }
        
        console.log('[Bindings] Step 5: Verifying critical methods...');
        // Verify critical methods exist
        if (typeof App.IsMarketOpen !== 'function') {
            console.error('[Bindings] ERROR: IsMarketOpen method not found');
            console.error('[Bindings] App type:', typeof App);
            console.error('[Bindings] App keys:', Object.keys(App || {}));
            throw new Error('IsMarketOpen method not found in App bindings');
        }
        console.log('[Bindings] ✓ IsMarketOpen found');
        
        if (typeof App.GetNextMarketOpenLocalTime !== 'function') {
            console.error('[Bindings] ERROR: GetNextMarketOpenLocalTime method not found');
            throw new Error('GetNextMarketOpenLocalTime method not found in App bindings');
        }
        console.log('[Bindings] ✓ GetNextMarketOpenLocalTime found');
        
        if (typeof App.Greet !== 'function') {
            console.error('[Bindings] ERROR: Greet method not found');
            throw new Error('Greet method not found in App bindings');
        }
        console.log('[Bindings] ✓ Greet found');
        
        console.log('[Bindings] ===== SUCCESS: All critical methods found =====');
        
        // Make App available globally for HTML script block IMMEDIATELY
        window._app = App;
        window._appReady = true;
        console.log('[Bindings] App made available globally - window._app set');
        console.log('[Bindings] App methods:', App ? Object.keys(App) : 'App is null');
        
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

// Load settings from localStorage cache
function loadSettingsFromCache() {
    try {
        const cached = localStorage.getItem(SETTINGS_CACHE_KEY);
        if (cached) {
            return JSON.parse(cached);
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
            settings: settings,
            lastUpdated: new Date().toISOString()
        };
        localStorage.setItem(SETTINGS_CACHE_KEY, JSON.stringify(cacheData));
        console.log('[Settings Cache] Saved to cache');
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
        EnableLogging: true,
        HiddenPlots: [],
        ChartColors: {
            'spot': '#4CAF50',
            'zero_gamma': '#FF9800',
            'major_pos_vol': '#2196F3',
            'major_neg_vol': '#F44336',
            'major_long_gamma': '#9C27B0',
            'major_short_gamma': '#00BCD4',
            'major_positive': '#8BC34A',
            'major_negative': '#FF5722',
            'major_pos_oi': '#3F51B5',
            'major_neg_oi': '#E91E63'
        },
        ChartZoomFilterPercent: 1.0,
        AutoFollowBufferPercent: 1.0
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
function initializeSettingsImmediate() {
    const settingsBtn = document.getElementById('settings-btn');
    const settingsModal = document.getElementById('settings-modal');
    const settingsClose = document.getElementById('settings-close');
    const saveBtn = document.getElementById('save-settings');
    
    if (!settingsBtn || !settingsModal || !settingsClose || !saveBtn) {
        console.error('Settings elements not found');
        return;
    }
    
    settingsBtn.addEventListener('click', async () => {
        settingsModal.style.display = 'block';
        // Wait a bit for modal to be visible
        await new Promise(resolve => setTimeout(resolve, 100));
        
        // Load from cache first (instant display)
        const cachedSettings = loadSettingsFromCache();
        const defaultSettings = getDefaultSettings();
        const initialSettings = cachedSettings?.settings || defaultSettings;
        
        console.log('[Settings] Loading from cache/defaults first');
        loadSettingsUI(initialSettings);
        
        // Then try to load from backend in background
        try {
            await loadSettingsFromBackend();
        } catch (err) {
            console.warn('[Settings] Backend load failed, using cache/defaults:', err);
            // Settings already loaded from cache, so this is fine
        }
    });
    
    settingsClose.addEventListener('click', () => {
        settingsModal.style.display = 'none';
    });
    
    window.addEventListener('click', (e) => {
        if (e.target === settingsModal) {
            settingsModal.style.display = 'none';
        }
    });
    
    saveBtn.addEventListener('click', async () => {
        try {
            await saveSettings();
            settingsModal.style.display = 'none';
        } catch (error) {
            console.error('Error saving settings:', error);
            alert('Error saving settings: ' + error.message);
        }
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
            console.log('[Settings] Reset all polling settings to defaults (0=auto, medium priority)');
        });
    }
    
    console.log('Settings button initialized');
}

// Market countdown timer
let marketCountdownInterval = null;

// Date selector state
let selectedDate = null; // Stores selected date as "YYYY-MM-DD" string

// Update market status and countdown - completely rewritten to be simple and reliable
async function updateMarketStatus() {
    const marketStatusEl = document.getElementById('market-status');
    if (!marketStatusEl) {
        console.error('[Market Status] ERROR: market-status element not found!');
        return;
    }
    
    // Check if App is available
    console.log('[Market Status] Checking App availability:', {
        App: typeof App,
        IsMarketOpen: typeof App?.IsMarketOpen,
        GetNextMarketOpenLocalTime: typeof App?.GetNextMarketOpenLocalTime,
        AppKeys: App ? Object.keys(App) : 'App is null/undefined'
    });
    
    if (!App || typeof App.IsMarketOpen !== 'function' || typeof App.GetNextMarketOpenLocalTime !== 'function') {
        const errorMsg = '[Market Status] ERROR: App or required methods not available - This is why you see "Checking market status..."';
        console.error('========================================');
        console.error(errorMsg);
        console.error('[Market Status] App exists:', !!App);
        console.error('[Market Status] App type:', typeof App);
        console.error('[Market Status] IsMarketOpen type:', typeof App?.IsMarketOpen);
        console.error('[Market Status] GetNextMarketOpenLocalTime type:', typeof App?.GetNextMarketOpenLocalTime);
        console.error('[Market Status] App keys:', App ? Object.keys(App) : 'N/A');
        console.error('========================================');
        
        // Log to backend terminal
        await logToBackend('ERROR', errorMsg);
        await logToBackend('ERROR', `App exists: ${!!App}, IsMarketOpen: ${typeof App?.IsMarketOpen}, GetNextMarketOpenLocalTime: ${typeof App?.GetNextMarketOpenLocalTime}`);
        marketStatusEl.textContent = 'Backend not connected';
        marketStatusEl.style.background = 'rgba(244, 67, 54, 0.2)';
        marketStatusEl.style.color = '#f44336';
        return;
    }
    
    try {
        // Show browser's local time
        const browserNow = new Date();
        const browserTimeStr = browserNow.toLocaleString('en-US', {
            timeZoneName: 'short',
            hour12: false
        });
        const browserTZ = Intl.DateTimeFormat().resolvedOptions().timeZone;
        console.log('[Market Status] Browser local time:', browserTimeStr, 'Timezone:', browserTZ);
        console.log('[Market Status] Browser Date object:', browserNow.toString(), 'ISO:', browserNow.toISOString());
        
        // Check if market is open
        const isOpen = await App.IsMarketOpen();
        
        if (isOpen) {
            // Market is open
            marketStatusEl.textContent = 'Market Open';
            marketStatusEl.style.background = 'rgba(76, 175, 80, 0.2)';
            marketStatusEl.style.color = '#4CAF50';
            return;
        }
        
        // Market is closed - get next open time and calculate countdown
        console.log('[Market Status] === CALLING GetNextMarketOpenLocalTime ===');
        console.log('[Market Status] App object:', App);
        console.log('[Market Status] App.GetNextMarketOpenLocalTime type:', typeof App.GetNextMarketOpenLocalTime);
        
        let nextOpenISO;
        try {
            nextOpenISO = await App.GetNextMarketOpenLocalTime();
            console.log('[Market Status] SUCCESS: Received nextOpenISO:', nextOpenISO);
            console.log('[Market Status] nextOpenISO type:', typeof nextOpenISO);
            console.log('[Market Status] nextOpenISO length:', nextOpenISO?.length);
        } catch (error) {
            console.error('[Market Status] ERROR calling GetNextMarketOpenLocalTime:', error);
            marketStatusEl.textContent = 'Market status error';
            marketStatusEl.style.background = 'rgba(158, 158, 158, 0.2)';
            marketStatusEl.style.color = '#9E9E9E';
            return;
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
        
        console.log('[Market Status] === DATE PARSING DEBUG ===');
        console.log('[Market Status] nextOpenISO (RFC3339 from backend):', nextOpenISO);
        console.log('[Market Status] nextOpen (parsed Date object):', nextOpen.toString());
        console.log('[Market Status] nextOpen.getTime() (milliseconds):', nextOpen.getTime());
        console.log('[Market Status] nextOpen.toISOString() (UTC):', nextOpen.toISOString());
        console.log('[Market Status] nextOpen local string:', nextOpen.toLocaleString('en-US', { 
            timeZoneName: 'short',
            hour12: false,
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit'
        }));
        console.log('[Market Status] now (browser Date object):', now.toString());
        console.log('[Market Status] now.getTime() (milliseconds):', now.getTime());
        console.log('[Market Status] now.toISOString() (UTC):', now.toISOString());
        console.log('[Market Status] now local string:', now.toLocaleString('en-US', { 
            timeZoneName: 'short',
            hour12: false,
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit'
        }));
        
        // Validate date
        if (isNaN(nextOpen.getTime())) {
            console.error('[Market Status] Invalid date:', nextOpenISO);
            marketStatusEl.textContent = 'Market Closed';
            return;
        }
        
        // Calculate time difference (both dates are in browser's local timezone)
        const diff = nextOpen.getTime() - now.getTime();
        console.log('[Market Status] Time difference (ms):', diff);
        
        if (diff <= 0) {
            // Time has passed, market should be open - re-check
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
        
        console.log('[Market Status] Countdown calculation:', {
            diffMs: diff,
            hours: hours,
            minutes: minutes,
            seconds: seconds
        });
        
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
        
        console.log('[Market Status] Formatted open time string:', openTimeStr);
        
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
        
        console.log('[Market Status] Final countdown text:', countdownText);
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
    console.log('[Market Countdown] Starting market countdown...');
    logToBackend('INFO', '[Market Countdown] Starting market countdown timer');
    console.log('[Market Countdown] App available:', !!App);
    console.log('[Market Countdown] App.IsMarketOpen available:', typeof App?.IsMarketOpen);
    
    // Clear existing interval if any
    if (marketCountdownInterval) {
        console.log('[Market Countdown] Clearing existing interval');
        clearInterval(marketCountdownInterval);
    }
    
    // Update immediately
    console.log('[Market Countdown] Calling updateMarketStatus() immediately...');
    updateMarketStatus();
    
    // Update every second
    marketCountdownInterval = setInterval(() => {
        updateMarketStatus();
    }, 1000);
    console.log('[Market Countdown] Interval set, will update every second');
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
        console.log('[Init] Already initialized, skipping');
        return true;
    }
    
    console.log('========================================');
    console.log('[Init] ===== Starting application initialization =====');
    console.log('[Init] Timestamp:', new Date().toISOString());
    console.log('[Init] Window location:', window.location.href);
    console.log('[Init] Document ready state:', document.readyState);
    console.log('========================================');
    
    try {
        // Load bindings - should always succeed since backend is ready
        console.log('[Init] Step 1: Loading App bindings...');
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
        
        console.log('[Init] Step 1 SUCCESS: Bindings loaded');
        
        // Log successful binding load using HTTP endpoint
        await logToBackend('INFO', '[Init] App bindings loaded successfully');
        
        console.log('[Init] Step 2: Connecting to backend...');
        
        // Connect to backend (bindings are ready, so this should work)
        await connectToBackend();
        
        // Log successful connection using HTTP endpoint
        await logToBackend('INFO', '[Init] Backend connection successful');
        
        console.log('[Init] Step 2 SUCCESS: Backend connected');
        console.log('[Init] Step 3: Starting market countdown...');
        
        // Mark as initialized only after successful connection
        initialized = true;
        
        // Start countdown after backend is connected
        startMarketCountdown();
        
        console.log('========================================');
        console.log('[Init] ===== Initialization complete =====');
        console.log('========================================');
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
console.log('===== Market Terminal Gexbot - Script loaded =====');
console.log('[Init] Script execution started at:', new Date().toISOString());
console.log('[Init] This is main.js module - if you see this, the module loaded successfully');

// IMMEDIATE TEST: Try to log to backend using HTTP endpoint
console.log('[Init] Frontend JavaScript is executing - this message should appear in backend logs');
logToBackend('INFO', '[Init] Frontend JavaScript executing - main.js module loaded');
logToBackend('ERROR', '[Init] ERROR test - this should appear in backend logs');
logToBackend('WARN', '[Init] WARN test - this should appear in backend logs');

// Wait for DOM to be ready
window.addEventListener('DOMContentLoaded', async () => {
    console.log('[Init] DOMContentLoaded fired at:', new Date().toISOString());
    
    // Initialize settings button immediately (doesn't need backend)
    initializeSettingsImmediate();
    
    // Since window is created AFTER backend is ready, we can initialize directly
    // Window creation happens in ServiceStartup() after all backend initialization
    console.log('[Init] Window was created after backend ready - initializing now...');
    
    // Try initialization with retries (in case Wails runtime needs a moment)
    let retryCount = 0;
    const maxRetries = 15; // More retries
    const retryDelay = 1000; // 1 second between retries
    
    async function tryInitialize() {
        if (initialized) {
            console.log('[Init] Already initialized, stopping retries');
            return;
        }
        
        console.log(`[Init] ===== Attempt ${retryCount + 1}/${maxRetries} =====`);
        const success = await initializeApplication();
        
        if (success) {
            console.log('[Init] ===== SUCCESS! Initialization complete =====');
        } else if (retryCount < maxRetries - 1) {
            retryCount++;
            console.log(`[Init] Failed, will retry in ${retryDelay}ms (attempt ${retryCount + 1}/${maxRetries})`);
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
    console.log('[Init] Starting initialization immediately...');
    tryInitialize();
});

// Clean up on page unload
window.addEventListener('beforeunload', () => {
    stopMarketCountdown();
});

// Save window size on resize (debounced)
let resizeTimeout = null;
let lastSavedWidth = 0;
let lastSavedHeight = 0;

// Get reliable window dimensions
// In Wails, we need to account for the fact that Width/Height in WebviewWindowOptions
// are the outer window dimensions, while innerWidth/innerHeight are content dimensions.
// We add a small offset to approximate the outer dimensions.
function getWindowDimensions() {
    // Try outerWidth/outerHeight first (works in some environments)
    let width = window.outerWidth;
    let height = window.outerHeight;
    
    // If outer dimensions are available and valid, use them
    if (width && width >= 100 && height && height >= 100) {
        return { width, height, source: 'outer' };
    }
    
    // Fall back to inner dimensions + estimated chrome
    // Windows title bar is typically ~30-40px, borders ~2px each side
    const chromeWidth = 4;  // Left + right borders
    const chromeHeight = 40; // Title bar + top/bottom borders
    
    width = (window.innerWidth || document.documentElement.clientWidth) + chromeWidth;
    height = (window.innerHeight || document.documentElement.clientHeight) + chromeHeight;
    
    return { width, height, source: 'inner+chrome' };
}

// Function to save window dimensions (reusable)
async function saveWindowDimensionsNow() {
    const dims = getWindowDimensions();
    
    // Only save if dimensions are valid
    if (dims.width >= 600 && dims.height >= 400) {
        if (App && typeof App.SaveWindowSize === 'function') {
            try {
                await App.SaveWindowSize(dims.width, dims.height);
                lastSavedWidth = dims.width;
                lastSavedHeight = dims.height;
                console.log('[WindowSize] Saved window size:', dims.width, 'x', dims.height, `(${dims.source})`);
                await logToBackend('info', `[WindowSize] Saved window size: ${dims.width}x${dims.height} (${dims.source})`);
                return true;
            } catch (e) {
                console.warn('[WindowSize] Failed to save window size:', e);
                await logToBackend('error', `[WindowSize] Failed to save: ${e.message}`);
                return false;
            }
        } else {
            console.warn('[WindowSize] App.SaveWindowSize not available');
            return false;
        }
    }
    return false;
}

window.addEventListener('resize', () => {
    // Debounce - save 1 second after user stops resizing
    if (resizeTimeout) {
        clearTimeout(resizeTimeout);
    }
    resizeTimeout = setTimeout(async () => {
        // Use the centralized dimension getter for consistency
        const dims = getWindowDimensions();
        
        console.log('[Resize] Detected dimensions:', dims.width, 'x', dims.height, `(${dims.source})`, '- raw outer:', window.outerWidth, 'x', window.outerHeight, ', inner:', window.innerWidth, 'x', window.innerHeight);
        
        // Only save if dimensions are valid and changed
        if (dims.width >= 600 && dims.height >= 400 && (dims.width !== lastSavedWidth || dims.height !== lastSavedHeight)) {
            await saveWindowDimensionsNow();
        }
    }, 1000);
});

// Save window dimensions when window is about to close
window.addEventListener('beforeunload', async (e) => {
    // Save dimensions synchronously if possible, or use sendBeacon for async
    const dims = getWindowDimensions();
    if (dims.width >= 600 && dims.height >= 400 && App && typeof App.SaveWindowSize === 'function') {
        try {
            // Try to save synchronously (may not work in all browsers, but worth trying)
            // For Wails, the backend should handle this
            await App.SaveWindowSize(dims.width, dims.height);
            console.log('[BeforeUnload] Saved window size:', dims.width, 'x', dims.height);
        } catch (e) {
            console.warn('[BeforeUnload] Failed to save window size:', e);
        }
    }
});

// Connect to backend
// Note: App bindings should already be loaded before this is called
async function connectToBackend() {
    console.log('[Connect] ===== Starting backend connection =====');
    const statusEl = document.getElementById('status');
    if (!statusEl) {
        console.error('[Connect] CRITICAL ERROR: Status element not found!');
        throw new Error('Status element not found');
    }
    
    try {
        statusEl.textContent = 'Connecting...';
        statusEl.style.color = '#ff9800';
        console.log('[Connect] Status set to "Connecting..."');
        
        // App should already be loaded, but verify
        if (!App) {
            console.error('========================================');
            console.error('[Connect] CRITICAL ERROR: App bindings not loaded!');
            console.error('[Connect] App is:', App);
            console.error('[Connect] App type:', typeof App);
            console.error('========================================');
            throw new Error('App bindings not loaded. App is: ' + typeof App);
        }
        
        console.log('[Connect] App is loaded, checking methods...');
        console.log('[Connect] App.Greet type:', typeof App.Greet);
        console.log('[Connect] App methods:', Object.keys(App || {}));
        
        if (typeof App.Greet !== 'function') {
            console.error('========================================');
            console.error('[Connect] CRITICAL ERROR: App.Greet is not a function!');
            console.error('[Connect] App.Greet type:', typeof App.Greet);
            console.error('[Connect] App object:', App);
            console.error('[Connect] Available App methods:', Object.keys(App || {}));
            console.error('========================================');
            throw new Error('Backend service not available. App.Greet is: ' + typeof App.Greet);
        }
        
        console.log('[Connect] Calling App.Greet("Market Terminal")...');
        // Test greeting
        const greeting = await App.Greet('Market Terminal');
        console.log('[Connect] Greeting received:', greeting);
        
        // Log successful connection using HTTP endpoint
        await logToBackend('INFO', 'Backend connection successful - Greet test passed');
        
        console.log('[Connect] Calling App.GetVersion()...');
        // Get version
        const version = await App.GetVersion();
        console.log(`[Connect] Version received: ${version}`);
        
        // Update status
        statusEl.textContent = 'Connected';
        statusEl.style.color = '#4CAF50';
        console.log('[Connect] Status set to "Connected"');
        
        // Check if first run
        let isFirstRun = false;
        try {
            if (typeof App.CheckFirstRun === 'function') {
                console.log('[Connect] Calling App.CheckFirstRun()...');
                isFirstRun = await App.CheckFirstRun();
                console.log('[Connect] CheckFirstRun result:', isFirstRun);
            } else {
                console.warn('[Connect] CheckFirstRun method not available, assuming not first run');
            }
        } catch (error) {
            console.error('[Connect] Error checking first run:', error);
            // Assume not first run if check fails
        }
        
        if (isFirstRun) {
            console.log('[Connect] First run detected, clearing cached settings and showing startup wizard');
            // Clear any cached settings from previous installations
            try {
                localStorage.removeItem(SETTINGS_CACHE_KEY);
                console.log('[Connect] Cleared cached settings');
            } catch (e) {
                console.warn('[Connect] Failed to clear cached settings:', e);
            }
            showStartupWizard();
        } else {
            console.log('[Connect] Not first run, initializing UI...');
            // Initialize UI
            await initializeUI();
        }
        
        console.log('[Connect] ===== Backend connection successful =====');
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
        console.log('[Connect] Backend connection failed, trying to load from cache...');
        await initializeUIWithCache();
        throw error; // Re-throw so caller knows it failed
    }
}

// Initialize UI components
async function initializeUI() {
    console.log('[InitializeUI] ===== Starting UI initialization =====');
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
        console.log('[InitializeUI] Checking App availability...');
        console.log('[InitializeUI] App exists:', !!App);
        console.log('[InitializeUI] App type:', typeof App);
        console.log('[InitializeUI] App.GetEnabledTickers type:', typeof App?.GetEnabledTickers);
        
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
        
        console.log('[InitializeUI] Calling App.GetEnabledTickers()...');
        // Get enabled tickers
        let tickers = await App.GetEnabledTickers();
        console.log('[InitializeUI] Enabled tickers received:', tickers);
        console.log('[InitializeUI] Ticker count:', tickers ? tickers.length : 0);
        
        // Sort tickers by saved order if available
        if (tickers && tickers.length > 0) {
            try {
                const settings = await App.GetSettings();
                if (settings && settings.TickerOrder && settings.TickerOrder.length > 0) {
                    const tickerOrder = settings.TickerOrder;
                    tickers = sortTickersByOrder(tickers, tickerOrder);
                    console.log('[InitializeUI] Tickers sorted by saved order');
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
        
        console.log('[InitializeUI] Initializing ticker table with', tickers.length, 'tickers...');
        await logToBackend('INFO', `[InitializeUI] Initializing ticker table with ${tickers.length} tickers`);
        // Initialize ticker table
        initializeTickerTable(tickers);
        await logToBackend('INFO', '[InitializeUI] Ticker table initialized');
        
        console.log('[InitializeUI] Initializing date selector...');
        // Initialize date selector
        await initializeDateSelector();
        await logToBackend('INFO', '[InitializeUI] Date selector initialized');
        
        console.log('[InitializeUI] Starting periodic updates...');
        // Settings already initialized in initializeSettingsImmediate()
        
        // Start periodic updates
        startPeriodicUpdates();
        
        // Verify SaveWindowSize binding is available
        if (App && typeof App.SaveWindowSize === 'function') {
            console.log('[InitializeUI] SaveWindowSize binding is available');
            await logToBackend('INFO', '[InitializeUI] SaveWindowSize binding available');
            
            // Start periodic window size checking (as backup for resize events)
            startWindowSizeMonitor();
        } else {
            console.warn('[InitializeUI] SaveWindowSize binding NOT available');
            await logToBackend('WARN', '[InitializeUI] SaveWindowSize binding NOT available');
        }
        
        console.log('[InitializeUI] ===== UI initialization complete =====');
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
            tbody.innerHTML = `<tr><td colspan="8" style="text-align: center; color: #f44336;">Error loading tickers: ${error.message}</td></tr>`;
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
            <td id="${ticker}-last-update">-</td>
            <td><button class="chart-btn" data-ticker="${ticker}">📊 Chart</button></td>
        `;
        
        // Add click handler to open chart
        row.addEventListener('click', (e) => {
            // Don't trigger if clicking the button or drag handle
            if (e.target.tagName !== 'BUTTON' && !e.target.classList.contains('drag-handle')) {
                openChart(ticker);
            }
        });
        
        // Button click handler
        const btn = row.querySelector('.chart-btn');
        if (btn) {
            btn.addEventListener('click', (e) => {
                e.stopPropagation();
                openChart(ticker);
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
        console.log('[Ticker Order] Saved order:', order);
    } catch (error) {
        console.error('[Ticker Order] Error saving order:', error);
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
    try {
        console.log('[Settings UI] Loading UI with settings:', settings ? 'settings object' : 'null');
        
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
            const hasApiKey = settings.APITKey && settings.APITKey.length > 0;
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
            console.log('[Settings UI] Classic tier:', classicCheckbox.checked);
        }
        if (stateCheckbox) {
            stateCheckbox.checked = tiers.includes('state');
            console.log('[Settings UI] State tier:', stateCheckbox.checked);
        }
        if (orderflowCheckbox) {
            orderflowCheckbox.checked = tiers.includes('orderflow');
            console.log('[Settings UI] Orderflow tier:', orderflowCheckbox.checked);
        }
        
        // Load data collection mode
        const collectAllRadio = document.getElementById('collect-all');
        const collectChartsRadio = document.getElementById('collect-charts');
        // Default to 'all' if not set (CollectAllEndpoints defaults to true)
        const collectAll = settings.CollectAllEndpoints !== false;
        if (collectAllRadio) collectAllRadio.checked = collectAll;
        if (collectChartsRadio) collectChartsRadio.checked = !collectAll;
        console.log('[Settings UI] Data collection mode:', collectAll ? 'all' : 'charts');
        
        // Load ticker selection (organized by tier)
        loadTickerSelection(settings);
        
        // Load chart colors
        loadChartColors(settings);
        
        // Load general settings
        loadGeneralSettings(settings);
        
        // Load hidden plots settings
        loadHiddenPlots(settings);
        
        console.log('[Settings UI] UI loaded successfully');
    } catch (error) {
        console.error('[Settings UI] Error loading UI:', error);
    }
}

// Load general settings into UI
function loadGeneralSettings(settings) {
    try {
        console.log('[General Settings] Loading...');
        
        // UseMarketTime
        const useMarketTimeCheckbox = document.getElementById('use-market-time');
        if (useMarketTimeCheckbox) {
            useMarketTimeCheckbox.checked = settings.UseMarketTime || false;
            console.log('[General Settings] UseMarketTime:', useMarketTimeCheckbox.checked);
        }
        
        // EnableLogging
        const enableLoggingCheckbox = document.getElementById('enable-logging');
        if (enableLoggingCheckbox) {
            // Default to true if not explicitly set to false
            enableLoggingCheckbox.checked = settings.EnableLogging !== false;
            console.log('[General Settings] EnableLogging:', enableLoggingCheckbox.checked);
        }
        
        // HideConsole
        const hideConsoleCheckbox = document.getElementById('hide-console');
        if (hideConsoleCheckbox) {
            // Default to true if not explicitly set to false
            hideConsoleCheckbox.checked = settings.HideConsole !== false;
            console.log('[General Settings] HideConsole:', hideConsoleCheckbox.checked);
        }
        
        // ChartZoomFilterPercent
        const chartZoomFilterInput = document.getElementById('chart-zoom-filter');
        if (chartZoomFilterInput) {
            chartZoomFilterInput.value = settings.ChartZoomFilterPercent || 1.0;
            console.log('[General Settings] ChartZoomFilterPercent:', chartZoomFilterInput.value);
        }
        
        // AutoFollowBufferPercent
        const autoFollowBufferInput = document.getElementById('auto-follow-buffer');
        if (autoFollowBufferInput) {
            autoFollowBufferInput.value = settings.AutoFollowBufferPercent || 1.0;
            console.log('[General Settings] AutoFollowBufferPercent:', autoFollowBufferInput.value);
        }
    } catch (error) {
        console.error('[General Settings] Error loading:', error);
    }
}

// Load hidden plots settings into UI
function loadHiddenPlots(settings) {
    try {
        console.log('[Hidden Plots] Loading...');
        
        const hiddenPlots = settings.HiddenPlots || [];
        const plotCheckboxes = document.querySelectorAll('#hidden-plots-grid input[type="checkbox"]');
        
        plotCheckboxes.forEach(checkbox => {
            const plotName = checkbox.dataset.plot;
            if (plotName) {
                // Checkbox is CHECKED if plot is NOT in hiddenPlots array
                checkbox.checked = !hiddenPlots.includes(plotName);
                console.log(`[Hidden Plots] ${plotName}: ${checkbox.checked ? 'visible' : 'hidden'}`);
            }
        });
    } catch (error) {
        console.error('[Hidden Plots] Error loading:', error);
    }
}

// Save general settings from UI to settings object
function saveGeneralSettings(settings) {
    console.log('[General Settings] Saving...');
    
    const useMarketTimeCheckbox = document.getElementById('use-market-time');
    if (useMarketTimeCheckbox) {
        settings.UseMarketTime = useMarketTimeCheckbox.checked;
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
        if (!isNaN(value) && value >= 0 && value <= 50) {
            settings.AutoFollowBufferPercent = value;
        } else {
            settings.AutoFollowBufferPercent = 1.0; // Default if invalid
        }
    }
    
    console.log('[General Settings] Saved:', {
        UseMarketTime: settings.UseMarketTime,
        EnableLogging: settings.EnableLogging,
        HideConsole: settings.HideConsole,
        ChartZoomFilterPercent: settings.ChartZoomFilterPercent,
        AutoFollowBufferPercent: settings.AutoFollowBufferPercent
    });
}

// Save hidden plots from UI to settings object
function saveHiddenPlots(settings) {
    console.log('[Hidden Plots] Saving...');
    
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
    console.log('[Hidden Plots] Saved hidden plots:', hiddenPlots);
}

// Load settings from backend (optional - updates UI if available)
async function loadSettingsFromBackend() {
    try {
        console.log('[Settings Backend] Attempting to load from backend...');
        
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
            console.log('[Settings Backend] Backend not available, using cache/defaults');
            return null; // Not an error - just use cache/defaults
        }
        
        console.log('[Settings Backend] Calling App.GetSettings()...');
        const settings = await App.GetSettings();
        console.log('[Settings Backend] Received settings:', settings ? 'settings object' : 'null/undefined');
        
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
                console.log(`[Ticker Selection] Loading ${ticker}: config.RefreshRateMs=${config?.RefreshRateMs}, using refreshRate=${refreshRate}`);
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
        
        console.log('[Ticker Selection] Loaded', enabledCount, 'enabled tickers');
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
        console.log('[Chart Colors] Loading chart colors...');
        const colorsGrid = document.getElementById('chart-colors-grid');
        if (!colorsGrid) {
            console.warn('[Chart Colors] chart-colors-grid element not found');
            return;
        }
        
        colorsGrid.innerHTML = '';
        
        // Default chart colors
        const defaultColors = {
            'spot': '#4CAF50',
            'zero_gamma': '#FF9800',
            'major_pos_vol': '#2196F3',
            'major_neg_vol': '#F44336',
            'major_long_gamma': '#9C27B0',
            'major_short_gamma': '#00BCD4',
            'major_positive': '#8BC34A',
            'major_negative': '#FF5722',
            'major_pos_oi': '#3F51B5',
            'major_neg_oi': '#E91E63'
        };
        
        // Get chart colors from settings, use defaults if not available
        let chartColors = {};
        if (settings && settings.ChartColors) {
            chartColors = settings.ChartColors;
            console.log('[Chart Colors] Using colors from settings:', Object.keys(chartColors));
        } else {
            console.log('[Chart Colors] No ChartColors in settings, using defaults');
        }
        
        Object.keys(defaultColors).forEach(series => {
            const colorValue = chartColors[series] || defaultColors[series];
            
            const colorItem = document.createElement('div');
            colorItem.style.cssText = 'display: flex; flex-direction: column; gap: 0.25rem;';
            
            const label = document.createElement('label');
            label.textContent = series.replace(/_/g, ' ').replace(/\b\w/g, l => l.toUpperCase());
            label.style.cssText = 'font-size: 0.85rem; color: #aaa;';
            
            const colorInput = document.createElement('input');
            colorInput.type = 'color';
            colorInput.id = `color-${series}`;
            colorInput.value = colorValue;
            colorInput.style.cssText = 'width: 100%; height: 40px; border: 1px solid #3a3a3a; border-radius: 4px; cursor: pointer;';
            
            colorItem.appendChild(label);
            colorItem.appendChild(colorInput);
            colorsGrid.appendChild(colorItem);
        });
        
        console.log('[Chart Colors] Loaded', Object.keys(defaultColors).length, 'color pickers');
        
        // Reset colors button handler (remove old listeners first)
        const resetBtn = document.getElementById('reset-colors');
        if (resetBtn) {
            // Clone and replace to remove old event listeners
            const newResetBtn = resetBtn.cloneNode(true);
            resetBtn.parentNode.replaceChild(newResetBtn, resetBtn);
            
            newResetBtn.addEventListener('click', () => {
                Object.keys(defaultColors).forEach(series => {
                    const input = document.getElementById(`color-${series}`);
                    if (input) input.value = defaultColors[series];
                });
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
    console.log('[Chart Colors] Saving chart colors...');
    if (!settings.ChartColors) {
        settings.ChartColors = {};
    }
    const defaultColors = {
        'spot': '#4CAF50',
        'zero_gamma': '#FF9800',
        'major_pos_vol': '#2196F3',
        'major_neg_vol': '#F44336',
        'major_long_gamma': '#9C27B0',
        'major_short_gamma': '#00BCD4',
        'major_positive': '#8BC34A',
        'major_negative': '#FF5722',
        'major_pos_oi': '#3F51B5',
        'major_neg_oi': '#E91E63'
    };
    Object.keys(defaultColors).forEach(series => {
        const input = document.getElementById(`color-${series}`);
        if (input) {
            settings.ChartColors[series] = input.value;
        }
    });
    console.log('[Chart Colors] Chart colors saved to settings object.');
}

// Save settings
async function saveSettings() {
    try {
        console.log('[Save Settings] Starting save...');
        
        // Get current settings (from backend if available, otherwise from cache/defaults)
        let settings = null;
        if (App && typeof App.GetSettings === 'function') {
            try {
                settings = await App.GetSettings();
                console.log('[Save Settings] Loaded settings from backend');
            } catch (error) {
                console.warn('[Save Settings] Failed to load from backend, using cache/defaults:', error);
            }
        }
        
        // Fallback to cache or defaults
        if (!settings) {
            const cached = loadSettingsFromCache();
            settings = cached?.settings || getDefaultSettings();
            console.log('[Save Settings] Using cached/default settings');
        }
        
        if (!settings) {
            settings = getDefaultSettings();
        }
        
        // Update settings from UI
        const dataDirInput = document.getElementById('data-dir');
        if (dataDirInput) {
            settings.DataDirectory = dataDirInput.value || 'Tickers';
        }
        
        // Save subscription tiers - read checkboxes carefully
        const tiers = [];
        const classicCheckbox = document.getElementById('settings-tier-classic');
        const stateCheckbox = document.getElementById('settings-tier-state');
        const orderflowCheckbox = document.getElementById('settings-tier-orderflow');
        
        if (classicCheckbox && classicCheckbox.checked) {
            tiers.push('classic');
            console.log('[Save Settings] Classic tier selected');
        }
        if (stateCheckbox && stateCheckbox.checked) {
            tiers.push('state');
            console.log('[Save Settings] State tier selected');
        }
        if (orderflowCheckbox && orderflowCheckbox.checked) {
            tiers.push('orderflow');
            console.log('[Save Settings] Orderflow tier selected');
        }
        
        if (tiers.length === 0) {
            tiers.push('classic'); // Default to classic if none selected
            console.log('[Save Settings] No tiers selected, defaulting to classic');
        }
        
        settings.APISubscriptionTiers = tiers;
        console.log('[Save Settings] Subscription tiers:', tiers);
        
        // Save data collection mode
        const collectAllRadio = document.getElementById('collect-all');
        settings.CollectAllEndpoints = collectAllRadio ? collectAllRadio.checked : true;
        console.log('[Save Settings] Data collection mode:', settings.CollectAllEndpoints ? 'all' : 'charts');
        
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
                console.log(`[Save Settings] Ticker ${ticker}: input value="${refreshRateInput.value}", parsed=${value}, isNaN=${isNaN(value)}`);
                if (!isNaN(value) && value >= 0) {
                    refreshRate = value;
                }
            }
            console.log(`[Save Settings] Ticker ${ticker}: final refreshRate=${refreshRate}`);
            
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
            console.log('[Save Settings] Saved ticker order:', tickerOrder.length, 'tickers');
        }
        
        console.log('[Save Settings] Saving settings with ticker configs:', Object.keys(settings.TickerConfigs));
        
        // Save to cache immediately (works even without backend)
        saveSettingsToCache(settings);
        console.log('[Save Settings] Settings saved to cache');
        
        // Try to save to backend if available
        if (App && typeof App.SaveSettings === 'function') {
            try {
                await App.SaveSettings(settings);
                console.log('[Save Settings] Settings saved to backend successfully');
            } catch (error) {
                console.warn('[Save Settings] Failed to save to backend, but cached:', error);
                // Still show success since cache was saved
            }
        } else {
            console.log('[Save Settings] Backend not available, settings saved to cache only');
        }
        
        alert('Settings saved successfully!');
        
        // Broadcast settings update to all chart windows so they can refresh colors
        try {
            const settingsChannel = new BroadcastChannel('market-terminal-settings');
            settingsChannel.postMessage({ type: 'settings-updated', settings: settings });
            settingsChannel.close();
            console.log('[Save Settings] Broadcast settings update to chart windows');
        } catch (e) {
            console.warn('[Save Settings] Failed to broadcast settings update:', e);
        }
        
        // Reload settings UI to reflect changes (from cache, which we just updated)
        loadSettingsUI(settings);
        
        // Refresh main page ticker list to show newly enabled tickers
        // Wait a moment for backend to process the save
        await new Promise(resolve => setTimeout(resolve, 500));
        
        if (App && typeof App.GetEnabledTickers === 'function') {
            try {
                console.log('[Save Settings] Calling GetEnabledTickers()...');
                let enabledTickers = await App.GetEnabledTickers();
                console.log('[Save Settings] GetEnabledTickers returned:', enabledTickers, 'length:', enabledTickers?.length);
                
                if (enabledTickers && enabledTickers.length > 0) {
                    // Sort by the ticker order we just saved
                    if (settings.TickerOrder && settings.TickerOrder.length > 0) {
                        enabledTickers = sortTickersByOrder(enabledTickers, settings.TickerOrder);
                    }
                    console.log('[Save Settings] Refreshing main page with tickers:', enabledTickers);
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
        alert('Error saving settings: ' + (saveError.message || String(saveError)));
        throw saveError;
    }
}

// Periodic updates interval
let periodicUpdateInterval = null;

// Start periodic updates
// Monitor window size and save periodically (backup for resize events)
let windowSizeMonitorInterval = null;
let monitoredWidth = 0;
let monitoredHeight = 0;

function startWindowSizeMonitor() {
    // Clear existing interval
    if (windowSizeMonitorInterval) {
        clearInterval(windowSizeMonitorInterval);
    }
    
    // Check window size every 5 seconds
    windowSizeMonitorInterval = setInterval(async () => {
        const dims = getWindowDimensions();
        
        // Only save if size changed and is valid
        if (dims.width >= 600 && dims.height >= 400 && (dims.width !== monitoredWidth || dims.height !== monitoredHeight)) {
            if (await saveWindowDimensionsNow()) {
                monitoredWidth = dims.width;
                monitoredHeight = dims.height;
            }
        }
    }, 5000);
    
    // Also save initial size after a delay (but only if it's different from what we've already saved)
    setTimeout(async () => {
        const dims = getWindowDimensions();
        if (dims.width >= 600 && dims.height >= 400) {
            // Only save if different from what we've already saved (avoid overwriting with same value)
            if (dims.width !== lastSavedWidth || dims.height !== lastSavedHeight) {
                if (await saveWindowDimensionsNow()) {
                    monitoredWidth = dims.width;
                    monitoredHeight = dims.height;
                }
            } else {
                // Update monitored values even if we don't save (to avoid unnecessary saves)
                monitoredWidth = dims.width;
                monitoredHeight = dims.height;
                console.log('[WindowMonitor] Initial window size matches saved size:', dims.width, 'x', dims.height);
            }
        }
    }, 2000);
}

function startPeriodicUpdates() {
    // Clear existing interval if any
    if (periodicUpdateInterval) {
        clearInterval(periodicUpdateInterval);
        periodicUpdateInterval = null;
    }
    
    // Update every 1 second to reflect high-priority ticker updates
    periodicUpdateInterval = setInterval(async () => {
        await updateTickerData();
    }, 1000);
    
    // Initial update
    updateTickerData();
}

// Update ticker data (parallelized for performance)
async function updateTickerData() {
    try {
        const tickers = await App.GetEnabledTickers();
        // Use selected date if available, otherwise use current market date
        let dateStr = selectedDate;
        if (!dateStr) {
            // Fallback to current market date
            try {
                const response = await fetch('/api/market-date');
                if (response.ok) {
                    const data = await response.json();
                    dateStr = data.date;
                } else {
                    // Last resort: use today's date
                    const now = new Date();
                    dateStr = now.toISOString().split('T')[0];
                }
            } catch (error) {
                console.warn('[UpdateTickerData] Failed to get market date, using today:', error);
                const now = new Date();
                dateStr = now.toISOString().split('T')[0];
            }
        }
        
        // Fetch all tickers in parallel for better performance
        const promises = tickers.map(ticker => 
            App.GetTickerData(ticker, dateStr)
                .then(data => ({ ticker, data, error: null }))
                .catch(error => ({ ticker, data: null, error }))
        );
        
        // Wait for all requests to complete
        const results = await Promise.all(promises);
        
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
                // Update table
                updateTickerRow(ticker, data);
            } else {
                // No data available - show placeholder
                console.log(`No data available for ${ticker} on ${dateStr}`);
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

// Open chart for a ticker in a new window
async function openChart(ticker) {
    try {
        console.log(`Attempting to open chart for ${ticker}...`);
        console.log('App object:', App);
        console.log('OpenChartWindow function:', App.OpenChartWindow);
        
        if (!App.OpenChartWindow) {
            throw new Error('OpenChartWindow method not found in App service. Try restarting the application.');
        }
        
        // Use selected date if available, otherwise use current market date
        let dateStr = selectedDate;
        if (!dateStr) {
            try {
                const response = await fetch('/api/market-date');
                if (response.ok) {
                    const data = await response.json();
                    dateStr = data.date;
                } else {
                    const now = new Date();
                    dateStr = now.toISOString().split('T')[0];
                }
            } catch (error) {
                console.warn('[OpenChart] Failed to get market date, using today:', error);
                const now = new Date();
                dateStr = now.toISOString().split('T')[0];
            }
        }
        
        // Pass date as parameter to chart window (OpenChartWindow accepts optional dateStr)
        // If dateStr is empty, chart will use current market date
        await App.OpenChartWindow(ticker, dateStr || '');
        console.log(`Chart window opened for ${ticker} with date ${dateStr || 'current market date'}`);
    } catch (error) {
        console.error(`Error opening chart for ${ticker}:`, error);
        alert(`Error opening chart: ${error.message}`);
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
        console.log('[Date Selector] Loaded dates:', dates);
        
        // Get current market date for comparison
        let todayStr = null;
        try {
            const marketDateResponse = await fetch('/api/market-date');
            if (marketDateResponse.ok) {
                const marketDateData = await marketDateResponse.json();
                todayStr = marketDateData.date;
            }
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
        
        console.log('[Date Selector] Default date selected:', selectedDate);
        
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
    console.log('[Date Selector] Date changed to:', dateStr);
    selectedDate = dateStr;
    
    // Refresh ticker table with new date
    await updateTickerData();
}

// Set date selector to today
async function setDateToToday() {
    try {
        const response = await fetch('/api/market-date');
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        
        const data = await response.json();
        const todayStr = data.date;
        
        const dateSelector = document.getElementById('date-selector');
        if (!dateSelector) return;
        
        // Find today's date in the dropdown
        for (let i = 0; i < dateSelector.options.length; i++) {
            if (dateSelector.options[i].value === todayStr) {
                dateSelector.selectedIndex = i;
                await onDateChanged(todayStr);
                return;
            }
        }
        
        // If today not found, select first item
        if (dateSelector.options.length > 0) {
            dateSelector.selectedIndex = 0;
            await onDateChanged(dateSelector.options[0].value);
        }
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
                
                console.log('Starting setup with API key length:', apiKey.length, 'tiers:', tiers);
                
                if (apiKey.length === 0) {
                    console.error('ERROR: API key is empty before calling CompleteSetup!');
                    alert('API key cannot be empty!');
                    btn.disabled = false;
                    btn.textContent = 'Save & Continue';
                    return;
                }
                
                // Complete setup (use default tickers)
                try {
                    console.log('Calling App.CompleteSetup with API key length:', apiKey.length);
                    const result = await App.CompleteSetup(apiKey, tiers, []);
                    console.log('CompleteSetup returned:', result);
                    console.log('Setup completed successfully');
                } catch (setupError) {
                    console.error('Setup error:', setupError);
                    alert('Error completing setup: ' + (setupError.message || String(setupError)));
                    btn.disabled = false;
                    btn.textContent = 'Save & Continue';
                    return;
                }
                
                // Close modal IMMEDIATELY - don't wait for verification
                console.log('Closing wizard immediately...');
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
                console.log('Initializing UI...');
                try {
                    await initializeUI();
                    console.log('Startup wizard completed - UI initialized');
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
