import { logToBackend } from '../shared/logging.js';

// Optimize data points if dataset is too large for performance.
// When over maxPoints we keep the last maxPoints only (sliding window) so that
// existing points never change when new data arrives - charts stay immutable.
// Returns a shallow copy so callers never mutate the cache.
export async function optimizeDataPoints(data, maxPoints = 30000) {
    if (!data.timestamp || data.timestamp.length === 0) {
        return data;
    }
    const len = data.timestamp.length;
    if (len <= maxPoints) {
        const copy = {};
        for (const key in data) {
            copy[key] = Array.isArray(data[key]) ? data[key].slice(0) : data[key];
        }
        return copy;
    }
    const drop = len - maxPoints;
    const trimmed = {};
    for (const key in data) {
        if (Array.isArray(data[key])) {
            trimmed[key] = data[key].slice(drop);
        } else {
            trimmed[key] = data[key];
        }
    }
    await logToBackend('info', `[Chart] Optimized data: ${len} -> ${trimmed.timestamp.length} points (kept last ${maxPoints})`);
    return trimmed;
}

// Function to get market date from backend
export async function getMarketDate(dateFromURL) {
    // First check if date was passed via URL parameter (from date selector)
    if (dateFromURL) {
        await logToBackend('info', `[Chart] Using date from URL parameter: ${dateFromURL}`);
        return dateFromURL;
    }
    
    // Otherwise, get current market date from backend API
    try {
        const response = await fetch('/api/market-date');
        if (response.ok) {
            const data = await response.json();
            await logToBackend('info', `[Chart] Using market date from API: ${data.date}`);
            return data.date || new Date().toISOString().split('T')[0];
        }
    } catch (error) {
        await logToBackend('warn', `[Chart] Could not fetch market date, using current date: ${error.message || error}`);
    }
    // Fallback to current date
    const fallbackDate = new Date().toISOString().split('T')[0];
    await logToBackend('warn', `[Chart] Using fallback date: ${fallbackDate}`);
    return fallbackDate;
}

// Resolve timezone offset (e.g. "-04:00") for a specific zone/date.
// Uses Intl so DST transitions are handled correctly.
export function getOffsetForZoneAtDate(timeZone, date) {
    const parts = new Intl.DateTimeFormat('en-US', {
        timeZone,
        timeZoneName: 'longOffset',
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false
    }).formatToParts(date);
    const tzPart = parts.find(p => p.type === 'timeZoneName');
    const raw = tzPart && tzPart.value ? tzPart.value : 'GMT-05:00';
    const normalized = raw.replace(/^GMT/, '');
    return /^[-+]\d{2}:\d{2}$/.test(normalized) ? normalized : '-05:00';
}

// Convert an ET wall-clock time (HH:MM) to local HH:MM for today's ET date.
export function convertETClockToLocalHHMM(hour, minute, referenceDate = new Date()) {
    const etDateParts = new Intl.DateTimeFormat('en-CA', {
        timeZone: 'America/New_York',
        year: 'numeric',
        month: '2-digit',
        day: '2-digit'
    }).format(referenceDate); // "YYYY-MM-DD"
    const etOffset = getOffsetForZoneAtDate('America/New_York', referenceDate);
    const etInstant = new Date(`${etDateParts}T${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}:00${etOffset}`);
    return `${String(etInstant.getHours()).padStart(2, '0')}:${String(etInstant.getMinutes()).padStart(2, '0')}`;
}

// Function to get market hours in local timezone
export async function getMarketHoursLocal() {
    try {
        const response = await fetch('/api/market-hours-local');
        if (response.ok) {
            const data = await response.json();
            await logToBackend('info', `[Chart] Fetched market hours from API (ET): ${data.open} - ${data.close}`);
            
            const [etOpenHour, etOpenMin] = data.open.split(':').map(Number);
            const [etCloseHour, etCloseMin] = data.close.split(':').map(Number);
            const localOpen = convertETClockToLocalHHMM(etOpenHour, etOpenMin);
            const localClose = convertETClockToLocalHHMM(etCloseHour, etCloseMin);
            
            await logToBackend('info', `[Chart] Converted ET to local: ${data.open} ET -> ${localOpen} local, ${data.close} ET -> ${localClose} local`);
            
            return { open: localOpen, close: localClose };
        }
    } catch (error) {
        await logToBackend('warn', `[Chart] Could not fetch market hours, using defaults: ${error.message || error}`);
    }
    // Fallback: Market hours are 9:30 AM - 4:00 PM ET (DST-aware conversion).
    const localOpen = convertETClockToLocalHHMM(9, 30);
    const localClose = convertETClockToLocalHHMM(16, 0);
    await logToBackend('warn', `[Chart] Using fallback market hours: 09:30 ET -> ${localOpen} local, 16:00 ET -> ${localClose} local`);
    return { open: localOpen, close: localClose };
}

// Function to filter data to market hours (9:30 AM - 4:00 PM ET in local time)
// dateStr parameter is the requested market date (YYYY-MM-DD format) to ensure correct filtering
export async function filterToMarketHours(labels, data, marketHoursLocal, dateStr) {
    if (!marketHoursLocal || !labels || labels.length === 0) {
        return { labels, data };
    }
    
    // Parse market hours (HH:MM format)
    const [openHour, openMin] = marketHoursLocal.open.split(':').map(Number);
    const [closeHour, closeMin] = marketHoursLocal.close.split(':').map(Number);
    
    // IMPORTANT: Use the requested dateStr to determine chart date, NOT labels[0]
    // This fixes the issue where old timestamps in the database would cause
    // the filter to use the wrong date, filtering out valid data
    let chartDate;
    if (dateStr) {
        // Parse dateStr (format: "YYYY-MM-DD") and create date at midnight in local timezone
        chartDate = new Date(dateStr + 'T00:00:00');
        if (isNaN(chartDate.getTime())) {
            await logToBackend('warn', `[Chart] filterToMarketHours: Failed to parse dateStr '${dateStr}', falling back to labels[0]`);
            chartDate = labels[0] ? new Date(labels[0]) : new Date();
        }
    } else {
        // Fallback to labels[0] if no dateStr provided
        chartDate = labels[0] ? new Date(labels[0]) : new Date();
    }
    chartDate.setHours(0, 0, 0, 0); // Ensure we're at midnight
    
    await logToBackend('info', `[Chart] filterToMarketHours: Using chart date ${chartDate.toLocaleDateString()} from ${dateStr ? 'dateStr parameter' : 'labels[0] fallback'}`);
    
    const marketOpen = new Date(chartDate);
    marketOpen.setHours(openHour, openMin, 0, 0);
    const marketClose = new Date(chartDate);
    marketClose.setHours(closeHour, closeMin, 0, 0);
    // When local close is earlier than open (e.g. 02:30 vs 20:00 in IST), close is next calendar day
    if (marketClose.getTime() <= marketOpen.getTime()) {
        marketClose.setDate(marketClose.getDate() + 1);
    }
    
    // Filter labels and all data arrays
    const filteredLabels = [];
    const filteredData = {};
    
    for (const key in data) {
        filteredData[key] = [];
    }
    
    for (let i = 0; i < labels.length; i++) {
        const labelTime = new Date(labels[i]);
        // Check if time is within market hours
        if (labelTime >= marketOpen && labelTime <= marketClose) {
            filteredLabels.push(labels[i]);
            for (const key in data) {
                if (Array.isArray(data[key]) && i < data[key].length) {
                    filteredData[key].push(data[key][i]);
                }
            }
        }
    }
    
    await logToBackend('info', `[Chart] Filtered to market hours: ${filteredLabels.length} points (from ${labels.length})`);
    return { labels: filteredLabels, data: filteredData };
}
