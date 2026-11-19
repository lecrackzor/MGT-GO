// Info logs are sent only when EnableDebug is on. Errors and warnings always go through.
let debugEnabled = false;

export function setChartDebug(enabled) {
    debugEnabled = !!enabled;
}

export function isChartDebug() {
    return debugEnabled;
}

export async function logToBackend(level, message) {
    const normalized = String(level || '').toLowerCase();
    if (normalized === 'info' && !debugEnabled) {
        return;
    }
    try {
        await fetch('/api/frontend-log', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ level, message })
        });
    } catch (err) {
        // Logging must not break the chart.
    }
}
