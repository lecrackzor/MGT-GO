import { logToBackend, setChartDebug } from '../shared/logging.js';
import { getDefaultChartColors, datasetLabelsFromMetadata, priceScaleTag, isChartColorOffValue, computeNetSeries } from '../shared/series.js';
import { splitSegmentsToLwArrays, SegmentPrimitive, segmentPairsToLineDataWithGaps, HorizontalSegmentsPaneView } from './series.js';
import { optimizeDataPoints, getMarketDate, getMarketHoursLocal, filterToMarketHours } from './data.js';
import { installZoom } from './zoom.js';
import { installCrosshair } from './crosshair.js';
import { installProfile, chartProfileFields, readChartPrefs, chartPrefsKey } from './profile.js';

function fillRoundRect(ctx, x, y, w, h, radius, fill, stroke) {
    const r = Math.max(0, Math.min(radius || 0, w / 2, h / 2));
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
    if (fill && fill !== 'rgba(0, 0, 0, 0)' && fill !== 'transparent') {
        ctx.fillStyle = fill;
        ctx.fill();
    }
    if (stroke && stroke !== 'rgba(0, 0, 0, 0)' && stroke !== 'transparent') {
        ctx.strokeStyle = stroke;
        ctx.lineWidth = 1;
        ctx.stroke();
    }
}

function paintRange(ctx, el, x, y, w, h) {
    const min = Number(el.min);
    const max = Number(el.max);
    const value = Number(el.value);
    const t = max > min ? Math.max(0, Math.min(1, (value - min) / (max - min))) : 0;
    const trackY = y + h / 2;
    const inset = 2;
    const trackH = 4;
    ctx.fillStyle = '#3a3a3a';
    fillRoundRect(ctx, x + inset, trackY - trackH / 2, Math.max(1, w - inset * 2), trackH, 2, '#3a3a3a', '');
    if (t > 0) fillRoundRect(ctx, x + inset, trackY - trackH / 2, Math.max(1, (w - inset * 2) * t), trackH, 2, '#4CAF50', '');
    const thumb = Math.max(10, Math.min(14, h - 2));
    const cx = x + inset + (w - inset * 2) * t;
    ctx.beginPath();
    ctx.arc(cx, trackY, thumb / 2, 0, Math.PI * 2);
    ctx.fillStyle = '#f2f2f2';
    ctx.fill();
    ctx.strokeStyle = '#8a8a8a';
    ctx.lineWidth = 1;
    ctx.stroke();
}

function paintCheckbox(ctx, el, x, y, w, h) {
    const box = Math.max(11, Math.min(w, h, 14));
    const bx = x + (w - box) / 2;
    const by = y + (h - box) / 2;
    fillRoundRect(ctx, bx, by, box, box, 2, el.checked ? '#4CAF50' : '#1a1a1a', el.checked ? '#5CBF60' : '#6a6a6a');
    if (!el.checked) return;
    ctx.strokeStyle = '#fff';
    ctx.lineWidth = 1.6;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    ctx.beginPath();
    ctx.moveTo(bx + box * 0.22, by + box * 0.52);
    ctx.lineTo(bx + box * 0.42, by + box * 0.72);
    ctx.lineTo(bx + box * 0.78, by + box * 0.28);
    ctx.stroke();
}

function paintControls(ctx, el, origin) {
    el.querySelectorAll('button, input, select').forEach(function (node) {
        const style = getComputedStyle(node);
        if (style.display === 'none' || style.visibility === 'hidden') return;
        const rect = node.getBoundingClientRect();
        if (rect.width < 1 || rect.height < 1) return;
        const x = rect.left - origin.left;
        const y = rect.top - origin.top;
        ctx.save();
        const opacity = Number(style.opacity);
        if (Number.isFinite(opacity)) ctx.globalAlpha = opacity;
        const tag = node.tagName;
        if (tag === 'INPUT' && node.type === 'range') paintRange(ctx, node, x, y, rect.width, rect.height);
        else if (tag === 'INPUT' && (node.type === 'checkbox' || node.type === 'radio')) paintCheckbox(ctx, node, x, y, rect.width, rect.height);
        else if (tag === 'BUTTON' || tag === 'SELECT') {
            const radius = parseFloat(style.borderTopLeftRadius) || 2;
            fillRoundRect(ctx, x, y, rect.width, rect.height, radius, style.backgroundColor, style.borderTopColor);
            if (tag === 'SELECT') {
                const text = node.options && node.selectedIndex >= 0 ? node.options[node.selectedIndex].text : '';
                ctx.font = style.font || '11px sans-serif';
                ctx.fillStyle = style.color || '#e0e0e0';
                ctx.textBaseline = 'middle';
                ctx.textAlign = 'left';
                ctx.fillText(text, x + 6, y + rect.height / 2);
            }
        }
        ctx.restore();
    });
}

function paintPanelText(ctx, el, origin) {
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    let node;
    while ((node = walker.nextNode())) {
        const text = node.textContent.replace(/\s+/g, ' ').trim();
        if (!text) continue;
        const parent = node.parentElement;
        if (!parent || parent.tagName === 'OPTION' || parent.tagName === 'SELECT') continue;
        const style = getComputedStyle(parent);
        if (style.display === 'none' || style.visibility === 'hidden') continue;
        const range = document.createRange();
        range.selectNodeContents(node);
        const rect = range.getBoundingClientRect();
        range.detach();
        if (rect.width < 1 || rect.height < 1) continue;
        ctx.font = style.font || '12px sans-serif';
        ctx.fillStyle = style.color || '#d6d6d6';
        ctx.textBaseline = 'top';
        ctx.textAlign = 'left';
        ctx.fillText(text, rect.left - origin.left, rect.top - origin.top);
    }
}

function paintPanel(ctx, el, origin) {
    if (!el || el.hidden) return;
    const style = getComputedStyle(el);
    if (style.display === 'none' || style.visibility === 'hidden') return;
    const rect = el.getBoundingClientRect();
    if (rect.width < 2 || rect.height < 2) return;
    const x = rect.left - origin.left;
    const y = rect.top - origin.top;
    ctx.save();
    ctx.beginPath();
    ctx.rect(x, y, rect.width, rect.height);
    ctx.clip();
    const bg = style.backgroundColor;
    if (bg && bg !== 'rgba(0, 0, 0, 0)' && bg !== 'transparent') {
        ctx.fillStyle = bg;
        ctx.fillRect(x, y, rect.width, rect.height);
    }
    const border = style.borderTopColor;
    const borderW = parseFloat(style.borderTopWidth);
    if (border && borderW > 0 && border !== 'rgba(0, 0, 0, 0)') {
        ctx.fillStyle = border;
        ctx.fillRect(x, y, rect.width, borderW);
    }
    paintControls(ctx, el, origin);
    paintPanelText(ctx, el, origin);
    ctx.restore();
}

function captureWindow() {
    const root = document.body;
    const origin = root.getBoundingClientRect();
    const width = Math.max(1, root.clientWidth);
    const height = Math.max(1, root.clientHeight);
    const dpr = window.devicePixelRatio || 1;
    const out = document.createElement('canvas');
    out.width = Math.round(width * dpr);
    out.height = Math.round(height * dpr);
    const ctx = out.getContext('2d');
    ctx.scale(dpr, dpr);
    const pageBg = getComputedStyle(root).backgroundColor;
    ctx.fillStyle = pageBg && pageBg !== 'rgba(0, 0, 0, 0)' ? pageBg : '#1a1a1a';
    ctx.fillRect(0, 0, width, height);
    ['#chart-header', '#chart-toolbar', '#profile-replay', '#status', '#profile-pane'].forEach(function (sel) {
        const el = document.querySelector(sel);
        if (!el) return;
        const style = getComputedStyle(el);
        const rect = el.getBoundingClientRect();
        if (rect.width < 2 || rect.height < 2 || style.display === 'none') return;
        const bg = style.backgroundColor;
        if (bg && bg !== 'rgba(0, 0, 0, 0)' && bg !== 'transparent') {
            ctx.fillStyle = bg;
            ctx.fillRect(rect.left - origin.left, rect.top - origin.top, rect.width, rect.height);
        }
    });
    root.querySelectorAll('canvas').forEach(function (node) {
        const rect = node.getBoundingClientRect();
        if (rect.width < 2 || rect.height < 2 || node.width < 1) return;
        try {
            ctx.drawImage(node, rect.left - origin.left, rect.top - origin.top, rect.width, rect.height);
        } catch (err) {}
    });
    ['#chart-header', '#chart-toolbar', '#profile-replay', '#status', '#profile-metrics', '#profile-note', '#profile-chart-note', '#crosshair-info-box', '#level-alert-pop'].forEach(function (sel) {
        paintPanel(ctx, document.querySelector(sel), origin);
    });
    return out;
}

        // Lightweight Charts: time is UTC seconds (Unix timestamp)
        function dateToLWTime(d) {
            const date = d instanceof Date ? d : new Date(d);
            return Math.floor(date.getTime() / 1000);
        }
        function lwTimeToDate(t) {
            return new Date(t * 1000);
        }
        
        // Info logs are sent only when EnableDebug is on. Errors and warnings always go through.
        
        // Wrap everything in an async IIFE since we're not using modules
        (async function() {
            var live;
            var zoomHelpers;
            try {
            // Get ticker and date from URL parameters
            const urlParams = new URLSearchParams(window.location.search);
            const ticker = urlParams.get('ticker') || 'SPX';
            const dateFromURL = urlParams.get('date'); // Optional date parameter
            
            // Log ticker immediately
            try {
                fetch('/api/frontend-log', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ level: 'info', message: `[Chart] Ticker from URL: ${ticker}` })
                }).catch(() => {});
            } catch (e) {
                // Ignore
            }
            
            const chartTickerEl = document.getElementById('chart-ticker');
            if (chartTickerEl) chartTickerEl.textContent = ticker;
            const chartDateEl = document.getElementById('chart-date');
            if (chartDateEl) chartDateEl.textContent = dateFromURL || 'Live';
            await logToBackend('info', `[Chart] Header set: ${ticker} (${dateFromURL || 'live'})`);
            
            // Initialize chart (Lightweight Charts)
            const chartContainer = document.getElementById('chart-div');
            if (!chartContainer) {
                await logToBackend('error', '[Chart] CRITICAL: Chart container #chart-div not found!');
                throw new Error('Chart container not found');
            }
            const statusEl = document.getElementById('status');
            await logToBackend('info', '[Chart] Chart container found');
            
            let userHasZoomed = false;
            let savedZoomState = null;
            let initialZoomSet = false;
            let firstChartLoadDone = false;
            let chartDataMinTime = null;
            let chartDataMaxTime = null;
            let chartDataLastLogicalIndex = null;
            let autoFollowEnabled = true;
            let chartIsLive = !dateFromURL;
            let chartLiveResolved = !dateFromURL;
            let updateInterval = null;
            let lwSeriesByEndpoint = {};
            let lwSeriesList = [];
            let lastValueByEndpoint = {};
            let segmentPrimitive = null;
            let useSegmentPrimitive = false;
            let segmentPrimitiveAttachedToPane = false;
            let appliedLevelHistoryStyle = null;
            let currentChartDateStr = '';
            let chartUpdateId = 0; // Guard: only the latest updateChart() run may modify the chart (prevents doubled series on date change)
            function loadChartPrefs(tickerKey, dateStr) {
                const empty = { seriesTitleVisible: {}, priceLabelsFollowCrosshair: false, hiddenSeries: [], infoBoxCollapsed: false };
                try {
                    const p = readChartPrefs(tickerKey, dateStr);
                    if (!p || typeof p !== 'object' || Object.keys(p).length === 0) return Object.assign(empty, chartProfileFields(null));
                    return Object.assign({}, p, {
                        seriesTitleVisible: p.seriesTitleVisible && typeof p.seriesTitleVisible === 'object' ? p.seriesTitleVisible : (p.lastValueVisible && typeof p.lastValueVisible === 'object' ? p.lastValueVisible : {}),
                        priceLabelsFollowCrosshair: !!p.priceLabelsFollowCrosshair,
                        hiddenSeries: Array.isArray(p.hiddenSeries) ? p.hiddenSeries : [],
                        infoBoxCollapsed: p.infoBoxCollapsed === true
                    }, chartProfileFields(p));
                } catch (e) { return Object.assign(empty, chartProfileFields(null)); }
            }
            function saveChartPrefs(tickerKey, dateStr, prefs) {
                try {
                    localStorage.setItem(chartPrefsKey(tickerKey), JSON.stringify(prefs));
                } catch (e) {}
            }
            let currentChartPrefs = loadChartPrefs('', ''); // seriesTitleVisible[endpoint]=false hides series name (text left of value); value on scale always shown unless follow crosshair
            
            const createChartFn = (typeof LightweightCharts !== 'undefined' && LightweightCharts.createChart)
                ? LightweightCharts.createChart
                : (typeof createChart !== 'undefined' ? createChart : (window.createChart || (window.LightweightCharts && window.LightweightCharts.createChart)));
            const chart = createChartFn(chartContainer, {
                autoSize: true,
                layout: {
                    background: { color: '#1a1a1a' },
                    textColor: '#e0e0e0',
                    attributionLogo: false
                },
                grid: {
                    vertLines: { color: 'rgba(255, 255, 255, 0.1)' },
                    horzLines: { color: 'rgba(255, 255, 255, 0.1)' }
                },
                rightPriceScale: {
                    borderColor: 'rgba(255, 255, 255, 0.1)',
                    scaleMargins: { top: 0.1, bottom: 0.1 }
                },
                leftPriceScale: { visible: false },
                timeScale: {
                    borderColor: 'rgba(255, 255, 255, 0.1)',
                    timeVisible: true,
                    secondsVisible: true,
                    enableConflation: false,
                    minBarSpacing: 0.001,
                    fixLeftEdge: false,
                    fixRightEdge: false,
                    allowVisibleRangeOutsideData: true,
                    shiftVisibleRangeOnNewBar: false,
                    tickMarkFormatter: function(time, tickMarkType, locale) {
                        const t = typeof time === 'number' ? time : (time && typeof time === 'object' && 'timestamp' in time ? time.timestamp : (time && typeof time === 'object' && 'year' in time ? undefined : Number(time)));
                        if (t === undefined || t === null || !Number.isFinite(t)) return null;
                        const date = new Date(t * 1000);
                        const options = getTimeScaleFormatOptions(chart);
                        return date.toLocaleTimeString('en-US', options);
                    }
                },
                crosshair: {
                    mode: 1,
                    horzLine: { visible: false, labelVisible: false },
                    vertLine: { visible: true, labelVisible: false }
                },
                handleScroll: { mouseWheel: false, vertTouchDrag: true, horzTouchDrag: true },
                handleScale: { mouseWheel: false, pinch: true },
                priceScaleLabelsSource: currentChartPrefs.priceLabelsFollowCrosshair ? 'crosshair' : 'lastVisible'
            });
            ['crosshair-line', 'crosshair-x-marker', 'crosshair-pulse-dots', 'profile-replay-marker'].forEach(function (id) {
                const el = document.getElementById(id);
                if (el && el.parentElement === chartContainer) chartContainer.appendChild(el);
            });
            
            chart.priceScale('right').applyOptions({ visible: true, ticksVisible: false, autoScale: false });
            chart.priceScale('left').applyOptions({ visible: false, autoScale: false });
            (function ensureSegmentPrimitiveOnPane() {
                if (segmentPrimitive) return;
                try {
                    if (typeof chart.panes === 'function') {
                        var panes = chart.panes();
                        if (panes && panes.length > 0) {
                            segmentPrimitive = new SegmentPrimitive();
                            panes[0].attachPrimitive(segmentPrimitive);
                            useSegmentPrimitive = true;
                            segmentPrimitiveAttachedToPane = true;
                        }
                    }
                } catch (e) {}
            })();
            chart.applyOptions({
                crosshair: {
                    mode: 1,
                    horzLine: { visible: false, labelVisible: false },
                    vertLine: { visible: true, labelVisible: false }
                }
            });
            let programmaticRangeChange = false;
            var lastProgrammaticRangeChangeTime = 0;
            let programmaticRangeToken = 0;
            // True only after the pointer has actually dragged the plot or time axis.
            // A click, a data poll, and our own follow scroll must not clear auto-follow.
            let userMovingTime = false;
            let panOrigin = null;
            // The chart applies setVisibleLogicalRange on its next animation frame.
            // Clearing the guard with setTimeout(0) happens first, so that scroll
            // looks like a user pan and auto-follow turns straight back off
            // unless the view was already sitting on the follow range.
            function markProgrammaticRange() {
                programmaticRangeChange = true;
                lastProgrammaticRangeChangeTime = Date.now();
                const token = ++programmaticRangeToken;
                requestAnimationFrame(function () {
                    requestAnimationFrame(function () {
                        if (token !== programmaticRangeToken) return;
                        programmaticRangeChange = false;
                    });
                });
            }
            function disengageAutoFollow() {
                userHasZoomed = true;
                if (typeof saveZoomState === 'function') savedZoomState = saveZoomState();
                if (!autoFollowEnabled) return;
                autoFollowEnabled = false;
                try { syncAutoFollowButton(); } catch (e) {}
            }
            try { chart.timeScale().applyOptions({ shiftVisibleRangeOnNewBar: false }); } catch (e) {}
            chart.timeScale().subscribeVisibleLogicalRangeChange(() => {
                if (programmaticRangeChange || !userMovingTime) return;
                disengageAutoFollow();
            });
            
            if (typeof ResizeObserver !== 'undefined') {
                new ResizeObserver((entries) => {
                    const hit = entries.find((entry) => entry.target === chartContainer);
                    if (!hit) return;
                    const w = chartContainer.clientWidth;
                    const h = chartContainer.clientHeight;
                    if (w > 0 && h > 0) resizeChart(w, h);
                }).observe(chartContainer);
            }
            
            live = {
                get chart() { return chart; },
                get userHasZoomed() { return userHasZoomed; },
                set userHasZoomed(v) { userHasZoomed = v; },
                get savedZoomState() { return savedZoomState; },
                set savedZoomState(v) { savedZoomState = v; },
                fitSessionInset: function () { fitSessionInset(); },
                resizeChart: function (w, h) { resizeChart(w, h); },
                get programmaticRangeChange() { return programmaticRangeChange; },
                set programmaticRangeChange(v) { programmaticRangeChange = v; },
                markProgrammaticRange: function () { markProgrammaticRange(); },
                get lastProgrammaticRangeChangeTime() { return lastProgrammaticRangeChangeTime; },
                set lastProgrammaticRangeChangeTime(v) { lastProgrammaticRangeChangeTime = v; },
                get chartDataCache() { return chartDataCache; },
                get crosshairLine() { return crosshairLine; },
                get crosshairXMarker() { return crosshairXMarker; },
                get crosshairPulseDots() { return crosshairPulseDots; },
                get crosshairInfoBox() { return crosshairInfoBox; },
                get crosshairInfoContent() { return crosshairInfoContent; },
                get crosshairInfoHeader() { return crosshairInfoHeader; },
                get crosshairInfoBoxToggle() { return crosshairInfoBoxToggle; },
                get lwTimeToDate() { return lwTimeToDate; },
                get formatChartTime() { return formatChartTime; },
                get globalChartSettings() { return globalChartSettings; },
                priceScaleId: function () { return globalChartSettings.PriceAxisLocation === 'left' ? 'left' : 'right'; },
                get lwSeriesList() { return lwSeriesList; },
                get lwSeriesByEndpoint() { return lwSeriesByEndpoint; },
                get isMouseOverChart() { return isMouseOverChart; },
                set isMouseOverChart(v) { isMouseOverChart = v; },
                applySeriesAppearance: function (endpoint, opts) { applySeriesAppearance(endpoint, opts); },
                levelHistoryStyle: function () { return levelHistoryStyle(); },
                setLevelHistoryStyle: function (style) {
                    currentChartPrefs.levelHistoryStyle = style === 'scatter' ? 'scatter' : 'average';
                    saveChartPrefs(ticker, currentChartDateStr || dateFromURL || '', currentChartPrefs);
                    paintLevelHistory();
                },
                levelHistoryWarmupDrop: function () { return levelHistoryWarmupDrop(); },
                setLevelHistoryWarmupDrop: function (on) {
                    currentChartPrefs.levelHistoryWarmupDrop = !!on;
                    saveChartPrefs(ticker, currentChartDateStr || dateFromURL || '', currentChartPrefs);
                    paintLevelHistory();
                },
                get lastValueByEndpoint() { return lastValueByEndpoint; },
                get currentChartPrefs() { return currentChartPrefs; },
                get currentChartDateStr() { return currentChartDateStr; },
                set currentChartDateStr(v) { currentChartDateStr = v; },
                get infoBoxAspectRatio() { return infoBoxAspectRatio; },
                set infoBoxAspectRatio(v) { infoBoxAspectRatio = v; },
                get autoFollowEnabled() { return autoFollowEnabled; },
                set autoFollowEnabled(v) {
                    autoFollowEnabled = !!v;
                    if (autoFollowEnabled) scheduleFollow();
                },
                get saveChartPrefs() { return saveChartPrefs; },
                get setPriceScaleFromSpotOnly() { return zoomHelpers.setPriceScaleFromSpotOnly; },
                get followSpotPrice() { return zoomHelpers.followSpotPrice; },
                get setVisiblePriceRange() { return zoomHelpers.setVisiblePriceRange; },
                get ticker() { return ticker; },
                get dateFromURL() { return dateFromURL; },
            };
            installProfile(live);
            setupHeaderReadouts();
            zoomHelpers = installZoom(live);
            var applyPriceZoom = zoomHelpers.applyPriceZoom;
            var applyTimeZoom = zoomHelpers.applyTimeZoom;
            var saveZoomState = zoomHelpers.saveZoomState;
            var restoreZoomState = zoomHelpers.restoreZoomState;
            var setPriceScaleFromSpotOnly = zoomHelpers.setPriceScaleFromSpotOnly;
            // Full control of wheel zoom: price scale = Y only, time scale = X only, chart body = both (center-based)
            const PRICE_SCALE_WIDTH = 70;
            const TIME_SCALE_HEIGHT = 40;
            chartContainer.addEventListener('wheel', function (e) {
                const rect = chartContainer.getBoundingClientRect();
                const axisOnLeft = globalChartSettings.PriceAxisLocation === 'left';
                const isOverPriceScale = axisOnLeft
                    ? (e.clientX <= rect.left + PRICE_SCALE_WIDTH)
                    : (e.clientX >= rect.right - PRICE_SCALE_WIDTH);
                const isOverTimeScale = (e.clientY >= rect.bottom - TIME_SCALE_HEIGHT);
                e.preventDefault();
                e.stopPropagation();
                if (typeof live.zoomChartProfileX === 'function' && live.zoomChartProfileX(e.clientX, e.clientY, e.deltaY)) return;
                if (isOverPriceScale) {
                    applyPriceZoom(e.deltaY);
                } else if (isOverTimeScale) {
                    disengageAutoFollow();
                    applyTimeZoom(e.deltaY);
                } else {
                    applyPriceZoom(e.deltaY);
                    disengageAutoFollow();
                    applyTimeZoom(e.deltaY);
                }
            }, { passive: false, capture: true });
            chartContainer.addEventListener('pointerdown', function (e) {
                if (e.button !== 0) return;
                const rect = chartContainer.getBoundingClientRect();
                const axisOnLeft = globalChartSettings.PriceAxisLocation === 'left';
                const onPrice = axisOnLeft
                    ? (e.clientX <= rect.left + PRICE_SCALE_WIDTH)
                    : (e.clientX >= rect.right - PRICE_SCALE_WIDTH);
                if (onPrice) {
                    live.autoPrice = 'user';
                    panOrigin = null;
                    return;
                }
                panOrigin = { x: e.clientX, y: e.clientY };
                userMovingTime = false;
            });
            chartContainer.addEventListener('pointermove', function (e) {
                if (!panOrigin) return;
                if (Math.abs(e.clientX - panOrigin.x) < 4 && Math.abs(e.clientY - panOrigin.y) < 4) return;
                panOrigin = null;
                userMovingTime = true;
                disengageAutoFollow();
            });
            function endTimePan() {
                panOrigin = null;
                userMovingTime = false;
            }
            window.addEventListener('pointerup', endTimePan);
            window.addEventListener('pointercancel', endTimePan);
            
            await logToBackend('info', '[Chart] Lightweight Charts instance created');
        
        // Crosshair Info Box Setup
        let crosshairLine = null;
        let crosshairXMarker = null;
        let crosshairPulseDots = null;
        let crosshairInfoBox = null;
        let crosshairInfoContent = null;
        let crosshairInfoHeader = null;
        let crosshairInfoBoxToggle = null;
        
        try {
            crosshairLine = document.getElementById('crosshair-line');
            crosshairXMarker = document.getElementById('crosshair-x-marker');
            crosshairPulseDots = document.getElementById('crosshair-pulse-dots');
            crosshairInfoBox = document.getElementById('crosshair-info-box');
            crosshairInfoContent = document.getElementById('crosshair-info-box-content');
            crosshairInfoHeader = document.getElementById('crosshair-info-box-header');
            crosshairInfoBoxToggle = document.querySelector('.crosshair-info-box-toggle');
        } catch (e) {
            console.error('[Chart] Error getting crosshair elements:', e);
        }
        
        let isDraggingInfoBox = false;
        let dragStartX = 0;
        let dragStartY = 0;
        let infoBoxStartX = 0;
        let infoBoxStartY = 0;
        let isMouseOverChart = false; // Track if mouse is over the chart
        // isPanning is now declared before chart creation
        
        // Restore saved dimensions and position from localStorage
        if (crosshairInfoBox) {
            try {
                // Check if localStorage is available
                if (typeof localStorage === 'undefined' || localStorage === null) {
                    console.warn('[Chart] localStorage is not available, skipping tooltip restore');
                } else {
                    const savedWidth = localStorage.getItem('chartTooltipWidth');
                    const savedHeight = localStorage.getItem('chartTooltipHeight');
                    const savedLeft = localStorage.getItem('chartTooltipLeft');
                    const savedTop = localStorage.getItem('chartTooltipTop');
                    
                    if (savedWidth && savedHeight) {
                        const width = parseInt(savedWidth, 10);
                        // Width is remembered; height is not, so the box always
                        // grows to fit its rows instead of clipping them.
                        if (!isNaN(width) && width >= 200) {
                            crosshairInfoBox.style.width = width + 'px';
                            if (typeof logToBackend === 'function') {
                                logToBackend('info', `[Chart] Restored tooltip width: ${width}`).catch(() => {});
                            }
                        }
                    }
                    
                    if (savedLeft && savedTop) {
                        const left = parseInt(savedLeft, 10);
                        const top = parseInt(savedTop, 10);
                        const containerEl = document.getElementById('chart-container');
                        const cw = containerEl && containerEl.offsetWidth ? containerEl.offsetWidth : (typeof window !== 'undefined' && window.innerWidth ? window.innerWidth : 1200);
                        const ch = containerEl && containerEl.offsetHeight ? containerEl.offsetHeight : (typeof window !== 'undefined' && window.innerHeight ? window.innerHeight - 100 : 600);
                        const boxW = crosshairInfoBox.offsetWidth || 220;
                        const boxH = crosshairInfoBox.offsetHeight || 100;
                        const clampLeft = Math.max(0, Math.min(left, cw - Math.min(boxW, 80)));
                        const clampTop = Math.max(0, Math.min(top, ch - Math.min(boxH, 80)));
                        if (!isNaN(left) && !isNaN(top) && left >= -100 && top >= -100) {
                            crosshairInfoBox.style.left = clampLeft + 'px';
                            crosshairInfoBox.style.top = clampTop + 'px';
                            crosshairInfoBox.style.right = 'auto';
                            crosshairInfoBox.style.bottom = 'auto';
                            if (typeof logToBackend === 'function') {
                                logToBackend('info', `[Chart] Restored tooltip position: ${clampLeft}, ${clampTop}`).catch(() => {});
                            }
                        } else {
                            crosshairInfoBox.style.right = '20px';
                            crosshairInfoBox.style.top = '20px';
                            crosshairInfoBox.style.left = 'auto';
                            crosshairInfoBox.style.bottom = 'auto';
                        }
                    }
                }
            } catch (e) {
                console.error('[Chart] Error restoring tooltip dimensions/position:', e);
            }
        }
        
        // Track user resizes and save the width. Height stays content-driven:
        // pinning it to the first aspect ratio clipped the rows and left
        // scrollbars on the legend at all times.
        let resizeTimeout = null;
        let infoBoxAspectRatio = null;
        if (typeof ResizeObserver !== 'undefined' && crosshairInfoBox) {
            try {
                const resizeObserver = new ResizeObserver((entries) => {
                    try {
                        if (crosshairInfoBox.classList.contains('collapsed')) return;
                        for (const entry of entries) {
                            if (entry && entry.contentRect) {
                                const { width, height } = entry.contentRect;
                                if (infoBoxAspectRatio == null) {
                                    infoBoxAspectRatio = height / width;
                                }
                                requestAnimationFrame(() => clampInfoBoxMinHeight());
                                // Debounce saves to avoid too many localStorage writes
                                if (resizeTimeout !== null) {
                                    clearTimeout(resizeTimeout);
                                }
                                resizeTimeout = setTimeout(() => {
                                    try {
                                        if (typeof localStorage !== 'undefined' && localStorage !== null) {
                                            if (!isNaN(width) && !isNaN(height) && width > 0 && height > 0) {
                                                localStorage.setItem('chartTooltipWidth', Math.round(width).toString());
                                                localStorage.setItem('chartTooltipHeight', Math.round(height).toString());
                                                console.log('[Chart] Saved tooltip dimensions:', Math.round(width), 'x', Math.round(height));
                                            }
                                        }
                                    } catch (e) {
                                        console.error('[Chart] Error saving tooltip dimensions:', e);
                                    }
                                }, 300); // Save 300ms after resize ends
                            }
                        }
                    } catch (e) {
                        console.error('[Chart] Error in ResizeObserver callback:', e);
                    }
                });
                if (crosshairInfoBox) {
                    resizeObserver.observe(crosshairInfoBox);
                }
            } catch (e) {
                console.error('[Chart] Error creating ResizeObserver:', e);
            }
        }
        
        // Collapse/expand info box: hide content and shrink box to header-only when collapsed; restore size when expanded.
        let infoBoxExpandedWidth = null;
        let infoBoxExpandedHeight = null;
        let infoBoxStateKey = '';
        function infoBoxPrefsKey() {
            return (ticker || '') + '|' + (currentChartDateStr || dateFromURL || '') + '|' + (currentChartPrefs.infoBoxCollapsed ? '1' : '0');
        }
        function setInfoBoxCollapsed(collapsed) {
            const box = document.getElementById('crosshair-info-box');
            const contentEl = document.getElementById('crosshair-info-box-content');
            const headerEl = document.getElementById('crosshair-info-box-header');
            const toggle = box ? box.querySelector('.crosshair-info-box-toggle') : null;
            if (!box || !contentEl) return;
            if (collapsed && !box.classList.contains('collapsed') && box.offsetWidth > 40 && box.offsetHeight > 40) {
                infoBoxExpandedWidth = box.offsetWidth;
                infoBoxExpandedHeight = box.offsetHeight;
            }
            box.classList.toggle('collapsed', collapsed);
            contentEl.classList.toggle('content-hidden', collapsed);
            contentEl.style.display = collapsed ? 'none' : '';
            if (toggle) {
                toggle.textContent = collapsed ? '▲' : '▼';
                toggle.title = collapsed ? 'Expand' : 'Collapse';
            }
            if (collapsed) {
                const headerH = headerEl ? headerEl.offsetHeight : 0;
                const pad = 2 * parseFloat(getComputedStyle(box).paddingTop) || 0;
                const collapsedH = Math.max(28, headerH + pad + 4);
                const styled = parseInt(box.style.width, 10);
                const width = infoBoxExpandedWidth > 40 ? infoBoxExpandedWidth : (styled > 40 ? styled : 220);
                box.style.width = width + 'px';
                box.style.height = collapsedH + 'px';
                box.style.minHeight = collapsedH + 'px';
            } else {
                box.style.minHeight = '';
                if (infoBoxExpandedWidth != null && infoBoxExpandedHeight != null) {
                    box.style.width = infoBoxExpandedWidth + 'px';
                    box.style.height = infoBoxExpandedHeight + 'px';
                }
                requestAnimationFrame(() => {
                    if (typeof clampInfoBoxMinHeight === 'function') clampInfoBoxMinHeight();
                });
            }
        }
        function rememberInfoBoxCollapsed() {
            infoBoxStateKey = infoBoxPrefsKey();
        }
        function syncInfoBoxCollapsed() {
            const key = infoBoxPrefsKey();
            if (key === infoBoxStateKey) return;
            infoBoxStateKey = key;
            setInfoBoxCollapsed(currentChartPrefs.infoBoxCollapsed === true);
        }
        if (crosshairInfoBox) {
            crosshairInfoBox.addEventListener('click', (e) => {
                const toggle = e.target && e.target.closest ? e.target.closest('.crosshair-info-box-toggle') : null;
                if (!toggle) return;
                e.stopPropagation();
                e.preventDefault();
                try {
                    const box = document.getElementById('crosshair-info-box');
                    if (!box) return;
                    const collapsed = !box.classList.contains('collapsed');
                    if (collapsed) {
                        infoBoxExpandedWidth = box.offsetWidth;
                        infoBoxExpandedHeight = box.offsetHeight;
                    }
                    currentChartPrefs.infoBoxCollapsed = collapsed;
                    saveChartPrefs(ticker, currentChartDateStr || dateFromURL || '', currentChartPrefs);
                    rememberInfoBoxCollapsed();
                    setInfoBoxCollapsed(collapsed);
                } catch (err) {
                    console.error('[Chart] Error in collapse toggle:', err);
                }
            });
        }
        
        // Make info box draggable
        if (crosshairInfoHeader && crosshairInfoBox) {
            crosshairInfoHeader.addEventListener('mousedown', (e) => {
                if (e.target && e.target.classList && (e.target.classList.contains('crosshair-info-box-toggle') || e.target.classList.contains('crosshair-info-box-pin'))) return;
                try {
                    isDraggingInfoBox = true;
                    dragStartX = e.clientX;
                    dragStartY = e.clientY;
                    infoBoxStartX = crosshairInfoBox.offsetLeft;
                    infoBoxStartY = crosshairInfoBox.offsetTop;
                    crosshairInfoBox.classList.add('dragging');
                    e.preventDefault();
                } catch (err) {
                    console.error('[Chart] Error in mousedown handler for tooltip:', err);
                }
            });
            
            document.addEventListener('mousemove', (e) => {
                try {
                    if (isDraggingInfoBox && crosshairInfoBox) {
                        const deltaX = e.clientX - dragStartX;
                        const deltaY = e.clientY - dragStartY;
                        crosshairInfoBox.style.left = (infoBoxStartX + deltaX) + 'px';
                        crosshairInfoBox.style.top = (infoBoxStartY + deltaY) + 'px';
                        crosshairInfoBox.style.right = 'auto';
                        crosshairInfoBox.style.bottom = 'auto';
                    }
                } catch (err) {
                    console.error('[Chart] Error in mousemove handler for tooltip:', err);
                }
            });
            
            document.addEventListener('mouseup', () => {
                try {
                    if (isDraggingInfoBox && crosshairInfoBox) {
                        isDraggingInfoBox = false;
                        crosshairInfoBox.classList.remove('dragging');
                        
                        // Save position to localStorage
                        try {
                            if (typeof localStorage !== 'undefined' && localStorage !== null) {
                                const rect = crosshairInfoBox.getBoundingClientRect();
                                const left = Math.round(rect.left);
                                const top = Math.round(rect.top);
                                localStorage.setItem('chartTooltipLeft', left.toString());
                                localStorage.setItem('chartTooltipTop', top.toString());
                                console.log('[Chart] Saved tooltip position:', left, ',', top);
                            }
                        } catch (e) {
                            console.error('[Chart] Error saving tooltip position:', e);
                        }
                    }
                } catch (e) {
                    console.error('[Chart] Error saving tooltip position:', e);
                }
            });
        }
        
        var crosshairApi = installCrosshair(live);
        var updateCrosshairFromParam = crosshairApi.updateCrosshairFromParam;
        var clampInfoBoxMinHeight = crosshairApi.clampInfoBoxMinHeight;
        var updateCrosshairToLatest = crosshairApi.updateCrosshairToLatest;
        var openChartContextMenu = crosshairApi.openChartContextMenu;
        
        // Default color scheme (fallback if settings not available)
        const defaultColors = getDefaultChartColors();
        
        // Load colors from settings (will be populated when settings are loaded)
            let chartColors = { ...defaultColors };
        
        // Function to convert hex color to RGB
            function hexToRgb(hex) {
            const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
            return result ? {
                r: parseInt(result[1], 16),
                g: parseInt(result[2], 16),
                b: parseInt(result[3], 16)
            } : null;
        }
        
        // Function to convert hex to rgba string
            function hexToRgba(hex, alpha = 0.1) {
            const rgb = hexToRgb(hex);
            if (!rgb) return `rgba(128, 128, 128, ${alpha})`;
            return `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, ${alpha})`;
        }
        
        // Global settings storage for chart window
        let globalChartSettings = {
            UseMarketTime: false,
            Use24HourTime: true,
            HiddenPlots: [],
            ChartColorsOff: [],
            ChartZoomFilterPercent: 1.0,
            AutoFollowBufferPx: 16,
            PriceAxisLocation: 'left',
            ProfileSettings: { bar_width_percent: 22, profile_x_percent: 62, show_priors: true }
        };


        function isSeriesOffByColorSettings(endpoint) {
            if (globalChartSettings.ChartColorsOff.includes(endpoint)) return true;
            const color = chartColors[endpoint];
            return isChartColorOffValue(color);
        }
        
        // Function to load settings and update colors
            async function loadChartColors() {
            try {
                // Fetch settings via API endpoint
                const response = await fetch('/api/settings');
                if (response.ok) {
                    const settings = await response.json();
                    // Store global settings for time formatting, plot visibility, zoom filter, and auto-follow buffer
                    setChartDebug(!!settings.EnableDebug);
                    globalChartSettings.UseMarketTime = settings.UseMarketTime || false;
                    globalChartSettings.Use24HourTime = settings.Use24HourTime !== false;
                    globalChartSettings.HiddenPlots = settings.HiddenPlots || [];
                    globalChartSettings.ChartColorsOff = settings.ChartColorsOff || [];
                    globalChartSettings.ChartZoomFilterPercent = settings.ChartZoomFilterPercent || 1.0;
                    globalChartSettings.AutoFollowBufferPx = readBufferPx(settings.AutoFollowBufferPx);
                    globalChartSettings.PriceAxisLocation = (settings.PriceAxisLocation || 'left').toLowerCase() === 'right' ? 'right' : 'left';
                    globalChartSettings.ProfileSettings = settings.ProfileSettings || globalChartSettings.ProfileSettings;
                    if (live && typeof live.applyProfileSettings === 'function') live.applyProfileSettings();
                    if (settings && settings.ChartColors && typeof settings.ChartColors === 'object') {
                        chartColors = { ...defaultColors };
                        try {
                            const cc = settings.ChartColors;
                            for (const k of Object.keys(cc)) {
                                if (Object.prototype.hasOwnProperty.call(cc, k) && typeof cc[k] === 'string') chartColors[k] = cc[k];
                            }
                        } catch (e) {
                            await logToBackend('warn', `[Chart] ChartColors merge failed: ${e && e.message ? e.message : String(e)}`).catch(() => {});
                        }
                        await logToBackend('info', `[Chart] Loaded colors from settings (keys: ${Object.keys(chartColors).join(', ')})`);
                        await logToBackend('info', `[Chart] UseMarketTime: ${globalChartSettings.UseMarketTime}, HiddenPlots: ${JSON.stringify(globalChartSettings.HiddenPlots)}`);
                    }
                } else {
                    await logToBackend('info', '[Chart] Using default colors (settings not available or no ChartColors)');
                }
            } catch (error) {
                await logToBackend('warn', `[Chart] Could not load colors from settings, using defaults: ${error.message || error}`);
            }
            globalChartSettings.ChartColors = { ...chartColors };
            if (typeof chart !== 'undefined' && chart) {
                try {
                    applyPriceAxisSettings(chart);
                } catch (axisErr) {
                    await logToBackend('warn', `[Chart] applyPriceAxisSettings failed: ${axisErr && axisErr.message ? axisErr.message : String(axisErr)}`).catch(() => {});
                }
                try {
                    chart.applyOptions({
                        timeScale: {
                            tickMarkFormatter: getTickMarkFormatter(chart)
                        }
                    });
                } catch (e) {}
            }
        }
        
        function applyPriceAxisSettings(ch) {
            if (!ch) return;
            const id = globalChartSettings.PriceAxisLocation === 'left' ? 'left' : 'right';
            const other = id === 'left' ? 'right' : 'left';
            try {
                const spotColor = typeof chartColors.spot === 'string' ? chartColors.spot : (typeof defaultColors.spot === 'string' ? defaultColors.spot : '#4CAF50');
                const axisColor = spotColor.startsWith('#') ? spotColor : '#' + spotColor;
                ch.priceScale(id).applyOptions({
                    visible: true,
                    ticksVisible: false,
                    borderColor: axisColor,
                    autoScale: false,
                    scaleMargins: { top: 0.1, bottom: 0.1 }
                });
                ch.priceScale(other).applyOptions({ visible: false });
            } catch (e) {}
            lwSeriesList.forEach((row) => {
                try { row.series.applyOptions({ priceScaleId: id }); } catch (e) {}
            });
        }
        
        // Return Intl options for time-scale labels based on zoom: hide seconds when visible range is wide (e.g. 5-min ticks).
        function getTimeScaleFormatOptions(ch) {
            const base = { hour12: !(typeof globalChartSettings !== 'undefined' && globalChartSettings.Use24HourTime), hour: '2-digit', minute: '2-digit' };
            if (typeof globalChartSettings !== 'undefined' && globalChartSettings.UseMarketTime) base.timeZone = 'America/New_York';
            if (!ch || typeof ch.timeScale !== 'function') return base;
            try {
                const range = ch.timeScale().getVisibleRange();
                if (!range || range.from == null || range.to == null) return base;
                const fromSec = typeof range.from === 'number' ? range.from : (range.from && typeof range.from.timestamp === 'number' ? range.from.timestamp : 0);
                const toSec = typeof range.to === 'number' ? range.to : (range.to && typeof range.to.timestamp === 'number' ? range.to.timestamp : 0);
                const spanSec = toSec - fromSec;
                // Show seconds only when zoomed in so they're relevant (under ~1 minute visible).
                if (Number.isFinite(spanSec) && spanSec > 0 && spanSec <= 60) base.second = '2-digit';
            } catch (e) {}
            return base;
        }

        // Shared tick mark formatter so we can re-apply it when visible range changes (axis labels then update).
        function getTickMarkFormatter(ch) {
            return function(time, tickMarkType, locale) {
                const t = typeof time === 'number' ? time : (time && typeof time === 'object' && 'timestamp' in time ? time.timestamp : (time && typeof time === 'object' && 'year' in time ? undefined : Number(time)));
                if (t === undefined || t === null || !Number.isFinite(t)) return null;
                const date = new Date(t * 1000);
                const options = getTimeScaleFormatOptions(ch);
                return date.toLocaleTimeString('en-US', options);
            };
        }

        // Format time for display based on UseMarketTime and Use24HourTime settings
        function formatChartTime(timestamp) {
            const date = timestamp instanceof Date ? timestamp : new Date(timestamp);
            const options = {
                hour12: !globalChartSettings.Use24HourTime,
                hour: '2-digit',
                minute: '2-digit',
                second: '2-digit'
            };
            if (globalChartSettings.UseMarketTime) {
                options.timeZone = 'America/New_York';
            }
            return date.toLocaleTimeString('en-US', options);
        }
        
        // Build datasetColors from chartColors
            function getDatasetColors() {
            const datasetColors = {};
            Object.keys(defaultColors).forEach(key => {
                const color = chartColors[key] || defaultColors[key];
                const rgb = hexToRgb(color);
                if (rgb) {
                    datasetColors[key] = {
                        border: `rgb(${rgb.r}, ${rgb.g}, ${rgb.b})`,
                        fill: hexToRgba(color, 0.1)
                    };
                } else {
                    datasetColors[key] = {
                        border: 'rgb(128, 128, 128)',
                        fill: 'rgba(128, 128, 128, 0.1)'
                    };
                }
            });
            return datasetColors;
        }
        
        const datasetLabels = datasetLabelsFromMetadata();
        
        // Replace 0 values with the last previously known non-zero (forward-fill). Used so y-axis and labels never show 0 when we have a prior value.
            function forwardFillZeros(values) {
                if (!values || values.length === 0) return values;
                const out = values.slice(0);
                let lastNonZero = null;
                for (let i = 0; i < out.length; i++) {
                    const v = out[i];
                    const num = v === null || v === undefined ? NaN : (typeof v === 'number' ? v : (typeof v === 'object' && v != null && 'value' in v ? Number(v.value) : Number(v)));
                    if (typeof num === 'number' && !isNaN(num) && isFinite(num) && num !== 0) {
                        lastNonZero = num;
                    } else if (lastNonZero != null && typeof num === 'number' && num === 0) {
                        out[i] = lastNonZero;
                    }
                }
                return out;
            }

            const LEVEL_HISTORY_ENDPOINTS = { zero_gamma: true, major_call_gamma: true, major_put_gamma: true };

            function levelHistoryStyle() {
                const saved = currentChartPrefs && currentChartPrefs.levelHistoryStyle;
                if (saved === 'scatter' || saved === 'average') return saved;
                try { return localStorage.getItem('level-history-style') === 'scatter' ? 'scatter' : 'average'; }
                catch (err) { return 'average'; }
            }

            // Drop the moving average's warm-up ramp: the first windowSize-1 samples
            // average over a partial window and are not yet a real average.
            function levelHistoryWarmupDrop() {
                return !!(currentChartPrefs && currentChartPrefs.levelHistoryWarmupDrop);
            }

            function movingAverage(values, windowSize, dropWarmup) {
                const out = new Array(values.length);
                const window = [];
                let sum = 0;
                let last = null;
                for (let i = 0; i < values.length; i++) {
                    const raw = values[i];
                    let n = NaN;
                    if (typeof raw === 'number') n = raw;
                    else if (raw != null && typeof raw === 'object' && typeof raw.value === 'number') n = raw.value;
                    else if (raw != null && raw !== '') n = Number(raw);
                    // Older sessions leave zero gamma empty on many rows. Hold the last
                    // level through those gaps so the average is not restarted each time.
                    if (typeof n !== 'number' || !isFinite(n)) {
                        if (last == null) {
                            out[i] = null;
                            continue;
                        }
                        n = last;
                    } else {
                        last = n;
                    }
                    window.push(n);
                    sum += n;
                    if (window.length > windowSize) sum -= window.shift();
                    out[i] = (dropWarmup && window.length < windowSize) ? null : (sum / window.length);
                }
                return out;
            }

            const LEVEL_HISTORY_NAMES = ['zero_gamma', 'major_call_gamma', 'major_put_gamma'];

            function levelHistoryPoints(endpoint, scatter) {
                if (!chartDataCache || !Array.isArray(chartDataCache.timestamp)) return [];
                const times = chartDataCache.timestamp;
                const raw = chartDataCache[endpoint];
                if (!Array.isArray(raw) || raw.length === 0) return [];
                const filled = forwardFillZeros(raw.slice(0, times.length));
                const values = scatter ? filled : movingAverage(filled, 300, levelHistoryWarmupDrop());
                const byTime = new Map();
                const n = Math.min(times.length, values.length);
                for (let i = 0; i < n; i++) {
                    const rawVal = values[i];
                    if (rawVal === null || rawVal === undefined) continue;
                    const num = Number(rawVal);
                    if (!Number.isFinite(num)) continue;
                    const ts = typeof times[i] === 'number' ? times[i] : parseFloat(times[i]);
                    if (!Number.isFinite(ts)) continue;
                    const sec = ts < 1e12 ? Math.floor(ts) : Math.floor(ts / 1000);
                    byTime.set(sec, num);
                }
                const series = lwSeriesByEndpoint[endpoint];
                let plotted = null;
                try { plotted = series && typeof series.data === 'function' ? series.data() : null; } catch (e) {}
                if (plotted && plotted.length) {
                    const kept = [];
                    for (const point of plotted) {
                        if (!point || !Number.isFinite(point.time)) continue;
                        const value = byTime.get(point.time);
                        if (Number.isFinite(value)) kept.push({ time: point.time, value: value });
                    }
                    if (kept.length) return kept;
                }
                const out = [];
                byTime.forEach(function (value, time) { out.push({ time: time, value: value }); });
                out.sort(function (a, b) { return a.time - b.time; });
                return out;
            }

            function paintLevelHistory() {
                const style = levelHistoryStyle();
                const scatter = style === 'scatter';
                const colors = getDatasetColors();
                for (const endpoint of LEVEL_HISTORY_NAMES) {
                    const series = lwSeriesByEndpoint[endpoint];
                    if (!series) continue;
                    const points = levelHistoryPoints(endpoint, scatter);
                    let visible = true;
                    try {
                        const opts = typeof series.options === 'function' ? series.options() : null;
                        if (opts && opts.visible === false) visible = false;
                    } catch (e) {}
                    try { series.applyOptions({ lineVisible: !scatter, lineWidth: scatter ? 0 : 2, priceLineVisible: false }); } catch (e) {}
                    if (segmentPrimitive) {
                        if (scatter) {
                            const color = (colors[endpoint] && colors[endpoint].border) || 'rgb(128, 128, 128)';
                            segmentPrimitive.setScatterData(endpoint, points, color, visible);
                        } else {
                            segmentPrimitive.clearScatter(endpoint);
                        }
                    }
                    if (points.length) {
                        try { series.setData(points); } catch (e) {}
                    }
                }
                appliedLevelHistoryStyle = style;
            }

        // Transform data into horizontal segments (like Python's _plot_horizontal_segments)
        // Each value is held constant until the next timestamp, with no vertical connections
        // Uses null separators between segments (like Python uses NaN) to break connections
            function createHorizontalSegments(timestamps, values) {
            if (timestamps.length < 2 || values.length < 2) {
                // Not enough data - return as-is
                return timestamps.map((ts, idx) => ({
                    x: ts,
                    y: values[idx]
                })).filter(point => point.y !== null && point.y !== undefined && 
                    !(typeof point.y === 'number' && (isNaN(point.y) || !isFinite(point.y))));
            }
            
            const segments = [];
            function readValue(raw) {
                if (raw === null || raw === undefined) return null;
                let currentValue;
                if (typeof raw === 'number') currentValue = raw;
                else if (typeof raw === 'string') currentValue = Number(raw);
                else if (raw && typeof raw === 'object' && !Array.isArray(raw) && (raw.value != null || raw.y != null)) currentValue = Number(raw.value != null ? raw.value : raw.y);
                else currentValue = Number(raw);
                if (typeof currentValue !== 'number' || isNaN(currentValue) || !isFinite(currentValue)) return null;
                return currentValue;
            }

            // One horizontal run per value change: start, end, then a gap.
            let runStart = null;
            let runValue = null;
            for (let i = 0; i < timestamps.length; i++) {
                const currentValue = readValue(values[i]);
                if (currentValue === null) {
                    if (runStart != null) {
                        segments.push({ x: runStart, y: runValue });
                        segments.push({ x: timestamps[i - 1], y: runValue });
                        segments.push({ x: NaN, y: NaN });
                        runStart = null;
                        runValue = null;
                    }
                    continue;
                }
                if (runStart == null) {
                    runStart = timestamps[i];
                    runValue = currentValue;
                    continue;
                }
                if (currentValue !== runValue) {
                    segments.push({ x: runStart, y: runValue });
                    segments.push({ x: timestamps[i], y: runValue });
                    segments.push({ x: NaN, y: NaN });
                    runStart = timestamps[i];
                    runValue = currentValue;
                }
            }
            if (runStart != null) {
                segments.push({ x: runStart, y: runValue });
                segments.push({ x: timestamps[timestamps.length - 1], y: runValue });
            }
            return segments;
        }
        
        
        // Incremental chart data: cache and last timestamp for same ticker/date (avoid full rescan each poll)
        let chartDataCache = null;
        let lastChartTimestamp = null;
        const CHART_CACHE_CAP = 30000;
        // How many points we've already applied to the chart (after filter+optimize). Append-only path only pushes new points.
        let chartAppliedPointCount = 0;
        
        // Function to update chart with data
            async function updateChart() {
            const myUpdateId = ++chartUpdateId;
            try {
                statusEl.textContent = 'Loading data...';
                statusEl.className = '';
                
                // Get market date from backend
                const dateStr = await getMarketDate(dateFromURL);
                if (myUpdateId !== chartUpdateId) return;
                if (currentChartDateStr && currentChartDateStr !== dateStr) {
                    chartDataCache = null;
                    lastChartTimestamp = null;
                    chartAppliedPointCount = 0;
                    initialZoomSet = false;
                    lwSeriesList.forEach((e) => { try { chart.removeSeries(e.series); } catch (err) {} });
                    lwSeriesList = [];
                    lwSeriesByEndpoint = {};
                    lastValueByEndpoint = {};
                }
                currentChartDateStr = dateStr;
                currentChartPrefs = loadChartPrefs(ticker, dateStr);
                syncInfoBoxCollapsed();
                if (live && typeof live.applyChartProfilePrefs === 'function') live.applyChartProfilePrefs();
                if (!chartLiveResolved) {
                    chartIsLive = await resolveChartIsLive(dateStr);
                    chartLiveResolved = true;
                    if (!chartIsLive) {
                        autoFollowEnabled = false;
                        syncAutoFollowButton();
                    }
                }
                let url = `/api/chart-data/${ticker}/${dateStr}`;
                if (chartDataCache && chartDataCache.ticker === ticker && chartDataCache.dateStr === dateStr && lastChartTimestamp != null) {
                    url += `?since=${lastChartTimestamp}`;
                    await logToBackend('info', `[Chart] Incremental fetch: ${url} (market date: ${dateStr})`);
                } else {
                    await logToBackend('info', `[Chart] Fetching data from: ${url} (market date: ${dateStr})`);
                }
                
                const response = await fetch(url);
                
                await logToBackend('info', `[Chart] Response status: ${response.status} ${response.statusText}`);
                
                if (!response.ok) {
                    const errorText = await response.text();
                    await logToBackend('error', `[Chart] HTTP error: ${response.status} - ${errorText}`);
                    statusEl.textContent = `Error: HTTP ${response.status} - ${response.statusText}`;
                    statusEl.className = 'error';
                    throw new Error(`HTTP ${response.status}: ${response.statusText}`);
                }
                
                let data = await response.json();
                if (myUpdateId !== chartUpdateId) return;
                await logToBackend('info', `[Chart] Received data: timestamp=${data?.timestamp?.length || 0}, spot=${data?.spot?.length || 0}, zero_gamma=${data?.zero_gamma?.length || 0}, major_pos_vol=${data?.major_pos_vol?.length || 0}, major_neg_vol=${data?.major_neg_vol?.length || 0}, dateStr=${data?.dateStr || 'N/A'}, incremental=${!!data?.incremental}`);
                
                // Chart data is READ-ONLY: the API only returns data from the database; we never write back.
                // Incremental merge: append new rows to in-memory cache only; cap size by dropping oldest.
                if (data && data.incremental === true && chartDataCache && chartDataCache.ticker === ticker && chartDataCache.dateStr === dateStr) {
                    if (!Array.isArray(data.timestamp) || data.timestamp.length === 0) {
                        // No new rows; keep existing chart and cache, but still apply a style toggle.
                        paintLevelHistory();
                        if (chartDataCache.timestamp && chartDataCache.timestamp.length > 0) {
                            statusEl.textContent = `Loaded ${chartDataCache.timestamp.length} data points (no new data)`;
                            statusEl.className = '';
                        }
                        return;
                    }
                    const keys = ['timestamp', 'spot', 'zero_gamma', 'major_pos_vol', 'major_neg_vol', 'major_call_gamma', 'major_put_gamma', 'major_long_gamma', 'major_short_gamma', 'major_positive', 'major_negative', 'major_pos_oi', 'major_neg_oi', 'net'];
                    if (Number(data.timestamp[0]) === Number(lastChartTimestamp)) {
                        for (const key of keys) {
                            if (Array.isArray(chartDataCache[key]) && chartDataCache[key].length > 0) {
                                chartDataCache[key].pop();
                            }
                        }
                    }
                    for (const key of keys) {
                        if (Array.isArray(chartDataCache[key]) && Array.isArray(data[key])) {
                            for (let i = 0; i < data[key].length; i++) chartDataCache[key].push(data[key][i]);
                        }
                    }
                    if (chartDataCache.timestamp.length > CHART_CACHE_CAP) {
                        const drop = chartDataCache.timestamp.length - CHART_CACHE_CAP;
                        for (const key of keys) {
                            if (Array.isArray(chartDataCache[key])) chartDataCache[key] = chartDataCache[key].slice(drop);
                        }
                    }
                    lastChartTimestamp = chartDataCache.timestamp[chartDataCache.timestamp.length - 1];
                    data = chartDataCache;
                    await logToBackend('info', `[Chart] Merged incremental: ${data.timestamp.length} points, lastTs=${lastChartTimestamp}`);
                } else if (data && data.incremental !== true && data.timestamp && Array.isArray(data.timestamp) && data.timestamp.length > 0) {
                    chartDataCache = { ticker, dateStr, timestamp: [...data.timestamp], spot: data.spot ? [...data.spot] : [], zero_gamma: data.zero_gamma ? [...data.zero_gamma] : [], major_pos_vol: data.major_pos_vol ? [...data.major_pos_vol] : [], major_neg_vol: data.major_neg_vol ? [...data.major_neg_vol] : [], major_call_gamma: data.major_call_gamma ? [...data.major_call_gamma] : [], major_put_gamma: data.major_put_gamma ? [...data.major_put_gamma] : [], major_long_gamma: data.major_long_gamma ? [...data.major_long_gamma] : [], major_short_gamma: data.major_short_gamma ? [...data.major_short_gamma] : [], major_positive: data.major_positive ? [...data.major_positive] : [], major_negative: data.major_negative ? [...data.major_negative] : [], major_pos_oi: data.major_pos_oi ? [...data.major_pos_oi] : [], major_neg_oi: data.major_neg_oi ? [...data.major_neg_oi] : [], net: [] };
                    lastChartTimestamp = data.timestamp[data.timestamp.length - 1];
                }
                computeNetSeries(data);
                if (chartDataCache && chartDataCache.ticker === ticker && chartDataCache.dateStr === dateStr) {
                    computeNetSeries(chartDataCache);
                }
                if (typeof live.noteChartClock === 'function') live.noteChartClock();
                
                if (!data || !data.timestamp || data.timestamp.length === 0) {
                    chartDataCache = null;
                    lastChartTimestamp = null;
                    await logToBackend('warn', `[Chart] No data available: data=${!!data}, timestamp=${!!(data && data.timestamp)}, length=${data && data.timestamp ? data.timestamp.length : 0}`);
                    statusEl.textContent = 'No data available';
                    statusEl.className = '';
                    
                    // Still set x-axis to market hours even with no data
                    const marketHoursLocal = await getMarketHoursLocal();
                    await logToBackend('info', `[Chart] Market hours from API: ${marketHoursLocal.open} - ${marketHoursLocal.close}`);
                    
                    // Create date at midnight in local timezone
                    // dateStr is in format "YYYY-MM-DD", so we append "T00:00:00" to create local midnight
                    let chartDate = new Date(dateStr + 'T00:00:00');
                    if (isNaN(chartDate.getTime())) {
                        await logToBackend('warn', `[Chart] Failed to parse dateStr: ${dateStr}, using current date`);
                        chartDate = new Date();
                    }
                    chartDate.setHours(0, 0, 0, 0);
                    await logToBackend('info', `[Chart] Chart date (local midnight): ${chartDate.toISOString()} (local: ${chartDate.toLocaleString()})`);
                    
                    const [openHour, openMin] = marketHoursLocal.open.split(':').map(Number);
                    const [closeHour, closeMin] = marketHoursLocal.close.split(':').map(Number);
                    const xAxisMin = new Date(chartDate);
                    xAxisMin.setHours(openHour, openMin, 0, 0);
                    const xAxisMax = new Date(chartDate);
                    xAxisMax.setHours(closeHour, closeMin, 0, 0);
                    
                    await logToBackend('info', `[Chart] Setting x-axis for empty data: ${xAxisMin.toLocaleTimeString()} - ${xAxisMax.toLocaleTimeString()}`);
                    await logToBackend('info', `[Chart] X-axis min ISO: ${xAxisMin.toISOString()}, max ISO: ${xAxisMax.toISOString()}`);
                    await logToBackend('info', `[Chart] X-axis min local: ${xAxisMin.toLocaleString()}, max local: ${xAxisMax.toLocaleString()}`);
                    
                    if (myUpdateId !== chartUpdateId) return;
                    // Remove all series (Lightweight Charts)
                    lwSeriesList.forEach((e) => { try { chart.removeSeries(e.series); } catch (err) {} });
                    lwSeriesList = [];
                    lwSeriesByEndpoint = {};
                    lastValueByEndpoint = {};
                    if (segmentPrimitive) { try { segmentPrimitive.clearAll(); } catch (e) {} }
                    if (!segmentPrimitiveAttachedToPane) useSegmentPrimitive = false;
                    const fromT = dateToLWTime(xAxisMin);
                    const toT = dateToLWTime(xAxisMax);
                    chartDataMinTime = fromT;
                    chartDataMaxTime = toT;
                    chartDataLastLogicalIndex = null;
                    markProgrammaticRange();
                    try { chart.timeScale().setVisibleRange({ from: fromT, to: toT }); } catch (e) {}
                    await logToBackend('info', '[Chart] Chart updated with empty data');
                    return;
                }
                
                if (myUpdateId !== chartUpdateId) return;
                // Convert timestamps to Date objects (in local time)
                const labels = data.timestamp.map(ts => {
                    // Timestamp might be in seconds or milliseconds
                    const timestamp = typeof ts === 'number' ? ts : parseFloat(ts);
                    // If timestamp is less than 1e12, assume seconds, otherwise milliseconds
                    return new Date(timestamp < 1e12 ? timestamp * 1000 : timestamp);
                });
                
                // Get market hours in local timezone for filtering
                const marketHoursLocal = await getMarketHoursLocal();
                await logToBackend('info', `[Chart] Market hours (local): ${marketHoursLocal.open} - ${marketHoursLocal.close}`);
                
                // Debug: Log first and last timestamp to diagnose timezone issues
                if (labels.length > 0) {
                    const firstTs = labels[0];
                    const lastTs = labels[labels.length - 1];
                    await logToBackend('info', `[Chart] Timestamp range: first=${firstTs.toISOString()} (${firstTs.toLocaleString()}), last=${lastTs.toISOString()} (${lastTs.toLocaleString()})`);
                    await logToBackend('info', `[Chart] Raw timestamp values: first=${data.timestamp[0]}, last=${data.timestamp[data.timestamp.length - 1]}`);
                }
                
                // Filter data to market hours (9:30 AM - 4:00 PM ET in local time)
                // Pass dateStr to ensure filtering uses the requested date, not the first timestamp
                const filtered = await filterToMarketHours(labels, data, marketHoursLocal, dateStr);
                let filteredLabels = filtered.labels;
                let filteredData = filtered.data;
                
                await logToBackend('info', `[Chart] After market hours filter: ${filteredLabels.length} points (from ${labels.length})`);
                
                // Optimize data if too many points (for performance)
                const originalPointCount = filteredLabels.length;
                const optimizedData = await optimizeDataPoints(filteredData, 30000);
                
                // Recreate labels from optimized timestamp data to ensure alignment
                const optimizedLabels = optimizedData.timestamp.map(ts => {
                    const timestamp = typeof ts === 'number' ? ts : parseFloat(ts);
                    return new Date(timestamp < 1e12 ? timestamp * 1000 : timestamp);
                });
                
                await logToBackend('info', `[Chart] After optimization: ${optimizedLabels.length} points (from ${originalPointCount})`);
                await logToBackend('info', `[Chart] Optimized data keys: ${Object.keys(optimizedData).join(', ')}`);
                await logToBackend('info', `[Chart] Optimized data lengths: ${Object.keys(optimizedData).map(key => `${key}: ${optimizedData[key]?.length || 0}`).join(', ')}`);
                
                // Calculate market hours range for x-axis
                // Always use the market date from the API, not the current date
                // Parse dateStr (format: "YYYY-MM-DD") and create a date at midnight in local timezone
                await logToBackend('info', `[Chart] Market hours from API: ${marketHoursLocal.open} - ${marketHoursLocal.close}`);
                let chartDate = new Date(dateStr + 'T00:00:00');
                // If dateStr parsing fails, fall back to first label's date, or current date as last resort
                if (isNaN(chartDate.getTime())) {
                    await logToBackend('warn', `[Chart] Failed to parse dateStr: ${dateStr}, using fallback`);
                    if (optimizedLabels.length > 0 && optimizedLabels[0]) {
                        chartDate = new Date(optimizedLabels[0]);
                        chartDate.setHours(0, 0, 0, 0);
                    } else {
                        chartDate = new Date();
                        chartDate.setHours(0, 0, 0, 0);
                    }
                } else {
                    // Ensure we're using local midnight, not UTC
                    chartDate.setHours(0, 0, 0, 0);
                }
                
                await logToBackend('info', `[Chart] Chart date (local midnight): ${chartDate.toISOString()} (local: ${chartDate.toLocaleString()})`);
                
                const [openHour, openMin] = marketHoursLocal.open.split(':').map(Number);
                const [closeHour, closeMin] = marketHoursLocal.close.split(':').map(Number);
                const xAxisMin = new Date(chartDate);
                xAxisMin.setHours(openHour, openMin, 0, 0);
                const xAxisMax = new Date(chartDate);
                xAxisMax.setHours(closeHour, closeMin, 0, 0);
                
                await logToBackend('info', `[Chart] Setting x-axis range: ${xAxisMin.toLocaleTimeString()} - ${xAxisMax.toLocaleTimeString()} (market hours) for date ${dateStr}`);
                await logToBackend('info', `[Chart] X-axis min ISO: ${xAxisMin.toISOString()}, max ISO: ${xAxisMax.toISOString()}`);
                await logToBackend('info', `[Chart] X-axis min local: ${xAxisMin.toLocaleString()}, max local: ${xAxisMax.toLocaleString()}`);
                
                // Update chart x-axis limits to market hours only (even if no data after filtering)
                // Only set initial zoom on first load, not on subsequent updates if user has zoomed
                // Capture last known value per endpoint before clearing (for forward-fill when an endpoint is missing from this update)
                const previousLastValueByEndpoint = { ...lastValueByEndpoint };
                
                if (myUpdateId !== chartUpdateId) return;
                // In-place update: when we already have series for this ticker/date, update data only instead of
                // removing and re-adding all series. This avoids the chart blinking and scale jumping on every poll.
                const canUpdateInPlace = lwSeriesList.length > 0 && optimizedLabels.length > 0;
                let savedVisibleLogicalRange = null;
                if (canUpdateInPlace) {
                    try {
                        const logical = chart.timeScale().getVisibleLogicalRange();
                        if (logical && typeof logical.from === 'number' && typeof logical.to === 'number') {
                            savedVisibleLogicalRange = { from: logical.from, to: logical.to };
                        }
                    } catch (e) {}
                }
                
                if (!canUpdateInPlace) {
                    lwSeriesList.forEach((e) => { try { chart.removeSeries(e.series); } catch (err) {} });
                    lwSeriesList = [];
                    lwSeriesByEndpoint = {};
                    lastValueByEndpoint = {};
                    if (segmentPrimitive) { try { segmentPrimitive.clearAll(); } catch (e) {} }
                    if (!segmentPrimitiveAttachedToPane) useSegmentPrimitive = false;
                    chartAppliedPointCount = 0;
                }
                const appendOnly = canUpdateInPlace && optimizedLabels.length > chartAppliedPointCount;
                const styleNow = levelHistoryStyle();
                const levelStyleChanged = appliedLevelHistoryStyle !== styleNow;
                appliedLevelHistoryStyle = styleNow;
                if (levelStyleChanged && canUpdateInPlace) {
                    const levelNames = ['zero_gamma', 'major_call_gamma', 'major_put_gamma'];
                    for (const endpoint of levelNames) {
                        const series = lwSeriesByEndpoint[endpoint];
                        if (!series) continue;
                        try { chart.removeSeries(series); } catch (e) {}
                        delete lwSeriesByEndpoint[endpoint];
                        if (segmentPrimitive && typeof segmentPrimitive.clearScatter === 'function') segmentPrimitive.clearScatter(endpoint);
                    }
                    lwSeriesList = lwSeriesList.filter((entry) => levelNames.indexOf(entry.endpoint) < 0);
                }
                if (optimizedLabels.length === 0) {
                    await logToBackend('warn', `[Chart] No data after filtering/optimization for ${ticker} on ${dateStr}`);
                    try {
                        chartDataMinTime = dateToLWTime(xAxisMin);
                        chartDataMaxTime = dateToLWTime(xAxisMax);
                        chartDataLastLogicalIndex = null;
                        markProgrammaticRange();
                        chart.timeScale().setVisibleRange({ from: chartDataMinTime, to: chartDataMaxTime });
                    } catch (e) {}
                    return;
                }
                if (canUpdateInPlace && optimizedLabels.length === chartAppliedPointCount && !levelStyleChanged) {
                    statusEl.textContent = `Loaded ${optimizedLabels.length} data points (no new data)`;
                    statusEl.className = '';
                    if (initialZoomSet) firstChartLoadDone = true;
                    return;
                }

                chartDataMinTime = dateToLWTime(optimizedLabels[0]);
                chartDataMaxTime = dateToLWTime(optimizedLabels[optimizedLabels.length - 1]);
                chartDataLastLogicalIndex = optimizedLabels.length - 1;

                // Add one line series per endpoint
                const endpoints = [
                    'spot',
                    'zero_gamma',
                    'major_pos_vol',    // Positive gamma from live classic snapshots
                    'major_neg_vol',    // Negative gamma from live classic snapshots
                    'major_call_gamma', // Historical call gamma from the Ninja majors feed
                    'major_put_gamma',  // Historical put gamma from the Ninja majors feed
                    'net',              // Net gamma (computed)
                    'major_long_gamma', // Long gamma
                    'major_short_gamma', // Short gamma
                    'major_positive',   // Major positive strike
                    'major_negative',   // Major negative strike
                    'major_pos_oi',     // Major positive OI
                    'major_neg_oi'      // Major negative OI
                ];
                
                let datasetsAdded = 0;
                const batchPromises = [];
                for (const endpoint of endpoints) {
                    if (optimizedData[endpoint] && Array.isArray(optimizedData[endpoint]) && optimizedData[endpoint].length > 0) {
                        const valuesLength = optimizedData[endpoint].length;
                        const labelsLength = optimizedLabels.length;
                        const minLength = Math.min(valuesLength, labelsLength);
                        const labelsToUse = optimizedLabels.slice(0, minLength);
                        const valuesForEndpoint = forwardFillZeros(optimizedData[endpoint].slice(0, minLength));
                        if (valuesLength !== labelsLength) {
                            await logToBackend('warn', `[Chart] Data length mismatch for ${endpoint}: ${valuesLength} values vs ${labelsLength} labels`);
                        }
                        
                        const isSpot = endpoint === 'spot';
                        const isLevelHistory = !!LEVEL_HISTORY_ENDPOINTS[endpoint];
                        const historyScatter = isLevelHistory && levelHistoryStyle() === 'scatter';
                        let dataPoints;
                        
                        if (isSpot || isLevelHistory) {
                            const rawSpot = isLevelHistory && !historyScatter ? movingAverage(valuesForEndpoint, 300, levelHistoryWarmupDrop()) : valuesForEndpoint;
                            dataPoints = rawSpot
                                .map((value, idx) => {
                                    if (value === null || value === undefined) return null;
                                    let num;
                                    if (typeof value === 'number') num = value;
                                    else if (typeof value === 'string') num = Number(value);
                                    else if (value && typeof value === 'object' && typeof value.value === 'number') num = value.value;
                                    else if (value && typeof value === 'object' && !Array.isArray(value)) num = Number(value.value != null ? value.value : value.y != null ? value.y : value);
                                    else num = Number(value);
                                    if (typeof num !== 'number' || isNaN(num) || !isFinite(num)) return null;
                                    return { x: labelsToUse[idx], y: num };
                                })
                                .filter(point => point !== null);
                            if (dataPoints.length === 0 && rawSpot.length > 0) {
                                const first = rawSpot[0];
                                logToBackend('warn', `[Chart] spot first value type=${typeof first}, value=${first === null || first === undefined ? 'null' : (typeof first === 'object' ? JSON.stringify(first).slice(0, 80) : String(first))}`).catch(() => {});
                            }
                        } else {
                            dataPoints = createHorizontalSegments(labelsToUse, valuesForEndpoint);
                        }
                        
                        if (dataPoints.length === 0) {
                            await logToBackend('warn', `[Chart] No valid data points for ${endpoint} after filtering`);
                            continue;
                        }
                        
                        const datasetColors = getDatasetColors();
                        const colors = datasetColors[endpoint] || { border: 'rgb(128, 128, 128)', fill: 'rgba(128, 128, 128, 0.1)' };
                        const isHiddenByDefault = globalChartSettings.HiddenPlots.includes(endpoint);
                        const isHiddenOnChart = currentChartPrefs.hiddenSeries.includes(endpoint);
                        const isOffByColor = isSeriesOffByColorSettings(endpoint);
                        const showSeriesName = currentChartPrefs.seriesTitleVisible[endpoint] !== false;
                        const seriesVisible = !isHiddenByDefault && !isHiddenOnChart && !isOffByColor;
                        const seriesTitle = showSeriesName ? priceScaleTag(datasetLabels[endpoint]) : '';
                        const showValueOnScale = seriesVisible;
                        
                        if (isSpot || isLevelHistory) {
                            const lwData = dataPoints
                                .filter((p) => p != null && (typeof p.x !== 'number' || !isNaN(p.x)) && typeof p.y === 'number' && !isNaN(p.y) && isFinite(p.y))
                                .map((p) => ({ time: dateToLWTime(p.x), value: Number(p.y) }));
                            if (lwData.length === 0) continue;
                            let series = canUpdateInPlace ? lwSeriesByEndpoint[endpoint] : null;
                            if (!series) {
                                await logToBackend('info', `[Chart] Adding series ${endpoint} with ${lwData.length} data points`);
                                const LineSeriesType = (typeof LightweightCharts !== 'undefined' && LightweightCharts.LineSeries) ? LightweightCharts.LineSeries : null;
                                series = LineSeriesType
                                    ? chart.addSeries(LineSeriesType, {
                                        color: colors.border,
                                        title: seriesTitle,
                                        visible: seriesVisible,
                                        lineVisible: !historyScatter,
                                        lineWidth: historyScatter ? 0 : 2,
                                        priceLineVisible: false,
                                        lastValueVisible: showValueOnScale,
                                        crosshairMarkerVisible: false,
                                        pointMarkersVisible: false,
                                        priceScaleId: globalChartSettings.PriceAxisLocation === 'left' ? 'left' : 'right'
                                    })
                                    : chart.addLineSeries({
                                        color: colors.border,
                                        title: seriesTitle,
                                        visible: seriesVisible,
                                        lineVisible: !historyScatter,
                                        lineWidth: historyScatter ? 0 : 2,
                                        priceLineVisible: false,
                                        lastValueVisible: showValueOnScale,
                                        crosshairMarkerVisible: false,
                                        pointMarkersVisible: false,
                                        priceScaleId: globalChartSettings.PriceAxisLocation === 'left' ? 'left' : 'right'
                                    });
                                lwSeriesByEndpoint[endpoint] = series;
                                if (segmentPrimitive) {
                                    if (!segmentPrimitiveAttachedToPane && !useSegmentPrimitive && typeof series.attachPrimitive === 'function') {
                                        try { series.attachPrimitive(segmentPrimitive); useSegmentPrimitive = true; } catch (e) {}
                                    }
                                    if (isSpot || !segmentPrimitive.hasPriceSeries()) segmentPrimitive.setPriceSeries(series);
                                } else if (typeof series.attachPrimitive === 'function') {
                                    segmentPrimitive = new SegmentPrimitive();
                                    try { series.attachPrimitive(segmentPrimitive); useSegmentPrimitive = true; segmentPrimitive.setPriceSeries(series); } catch (e) {}
                                }
                                lwSeriesList.push({ endpoint, series, label: datasetLabels[endpoint], color: colors.border, lastTime: null });
                            } else {
                                series.applyOptions({ color: colors.border, title: seriesTitle, visible: seriesVisible, lineVisible: !historyScatter, lineWidth: historyScatter ? 0 : 2, lastValueVisible: showValueOnScale });
                            }
                            if (appendOnly && series && !(isLevelHistory && levelStyleChanged)) {
                                var firstNewTimeSpot = chartAppliedPointCount < optimizedLabels.length ? dateToLWTime(optimizedLabels[chartAppliedPointCount]) : null;
                                var startIdxSpot = 0;
                                if (firstNewTimeSpot != null) {
                                    for (var si = 0; si < lwData.length; si++) {
                                        if (lwData[si].time >= firstNewTimeSpot) { startIdxSpot = si; break; }
                                    }
                                }
                                for (var i = startIdxSpot; i < lwData.length; i++) {
                                    try { series.update({ time: lwData[i].time, value: lwData[i].value }); } catch (e) {}
                                }
                            } else {
                                series.setData(lwData.map(function (p) { return { time: p.time, value: p.value }; }));
                            }
                            series.applyOptions({ priceLineVisible: false, lineVisible: !historyScatter, lineWidth: historyScatter ? 0 : 2 });
                            if (isLevelHistory && segmentPrimitive) {
                                if (historyScatter) segmentPrimitive.setScatterData(endpoint, lwData, colors.border, seriesVisible);
                                else segmentPrimitive.clearScatter(endpoint);
                            }
                            const lastP = lwData[lwData.length - 1];
                            if (lastP.value !== 0) lastValueByEndpoint[endpoint] = lastP.value;
                            const listEntry = lwSeriesList.find((e) => e.endpoint === endpoint);
                            if (listEntry) listEntry.lastTime = lwTimeToDate(lastP.time);
                            else lwSeriesList.push({ endpoint, series, label: datasetLabels[endpoint], color: colors.border, lastTime: lwTimeToDate(lastP.time) });
                            datasetsAdded++;
                        } else {
                            const segmentArrays = splitSegmentsToLwArrays(dataPoints, dateToLWTime);
                            if (segmentArrays.length === 0) continue;
                            const segmentPairs = segmentArrays.flatMap(function (seg) { return seg; });
                            if (segmentPairs.length < 2) continue;
                            let series = canUpdateInPlace ? lwSeriesByEndpoint[endpoint] : null;
                            if (!series) {
                                await logToBackend('info', `[Chart] Adding ${endpoint} horizontal-segments series (${segmentArrays.length} segments)`);
                                series = chart.addCustomSeries(new HorizontalSegmentsPaneView(chart.timeScale()), {
                                    color: colors.border,
                                    title: seriesTitle,
                                    visible: seriesVisible,
                                    lineWidth: 2,
                                    priceLineVisible: false,
                                    lastValueVisible: showValueOnScale,
                                    crosshairMarkerVisible: false
                                });
                                lwSeriesByEndpoint[endpoint] = series;
                                lwSeriesList.push({ endpoint, series, label: datasetLabels[endpoint], color: colors.border, lastTime: null });
                            } else {
                                series.applyOptions({ color: colors.border, title: seriesTitle, visible: seriesVisible, lastValueVisible: showValueOnScale });
                            }
                            if (appendOnly && series && chartAppliedPointCount < labelsToUse.length) {
                                const firstNewTime = dateToLWTime(labelsToUse[chartAppliedPointCount]);
                                let startIdx = 0;
                                for (let k = 0; k < segmentPairs.length; k++) {
                                    if (segmentPairs[k].time >= firstNewTime) { startIdx = k; break; }
                                }
                                let appendOk = true;
                                for (let k = startIdx; k < segmentPairs.length; k++) {
                                    try { series.update({ time: segmentPairs[k].time, value: segmentPairs[k].value }); } catch (e) { appendOk = false; break; }
                                }
                                if (!appendOk) {
                                    // Do NOT fall back to setData: that replaces all data and redraws history. Skip this series this cycle.
                                }
                            } else {
                                series.setData(segmentPairs.map(function (p) { return { time: p.time, value: p.value }; }));
                            }
                            series.applyOptions({ priceLineVisible: false });
                            const lastP = segmentPairs[segmentPairs.length - 1];
                            if (lastP.value !== 0) lastValueByEndpoint[endpoint] = lastP.value;
                            const listEntry = lwSeriesList.find((e) => e.endpoint === endpoint);
                            if (listEntry) listEntry.lastTime = lastP ? lwTimeToDate(lastP.time) : null;
                            else lwSeriesList.push({ endpoint, series, label: datasetLabels[endpoint], color: colors.border, lastTime: lastP ? lwTimeToDate(lastP.time) : null });
                            datasetsAdded++;
                        }
                    } else {
                        const lastValue = previousLastValueByEndpoint[endpoint];
                        if (lastValue != null && optimizedLabels.length > 0) {
                            const isSpot = endpoint === 'spot';
                            const isLevelHistoryFwd = !!LEVEL_HISTORY_ENDPOINTS[endpoint];
                            const values = optimizedLabels.map(() => lastValue);
                            let dataPointsFwd;
                            if (isSpot || isLevelHistoryFwd) {
                                dataPointsFwd = optimizedLabels.map((t, idx) => ({ x: t, y: values[idx] }));
                            } else {
                                dataPointsFwd = createHorizontalSegments(optimizedLabels, values);
                            }
                            const datasetColorsFwd = getDatasetColors();
                            const colorsFwd = datasetColorsFwd[endpoint] || { border: 'rgb(128, 128, 128)' };
                            const isHiddenFwd = globalChartSettings.HiddenPlots.includes(endpoint);
                            const isHiddenOnChartFwd = currentChartPrefs.hiddenSeries.includes(endpoint);
                            const isOffByColorFwd = isSeriesOffByColorSettings(endpoint);
                            const showSeriesNameFwd = currentChartPrefs.seriesTitleVisible[endpoint] !== false;
                            const seriesTitleFwd = showSeriesNameFwd ? priceScaleTag(datasetLabels[endpoint]) : '';
                            const showValueOnScaleFwd = !isHiddenFwd && !isHiddenOnChartFwd && !isOffByColorFwd;
                            const seriesVisibleFwd = !isHiddenFwd && !isHiddenOnChartFwd && !isOffByColorFwd;
                            if (isSpot || isLevelHistoryFwd) {
                                const lwDataFwd = dataPointsFwd
                                    .filter((p) => p != null && (typeof p.x !== 'number' || !isNaN(p.x)) && typeof p.y === 'number' && !isNaN(p.y) && isFinite(p.y))
                                    .map((p) => ({ time: dateToLWTime(p.x), value: Number(p.y) }));
                                if (lwDataFwd.length > 0) {
                                    let seriesFwd = canUpdateInPlace ? lwSeriesByEndpoint[endpoint] : null;
                                    if (!seriesFwd) {
                                        const LineSeriesTypeFwd = (typeof LightweightCharts !== 'undefined' && LightweightCharts.LineSeries) ? LightweightCharts.LineSeries : null;
                                        seriesFwd = LineSeriesTypeFwd
                                            ? chart.addSeries(LineSeriesTypeFwd, {
                                                color: colorsFwd.border,
                                                title: seriesTitleFwd,
                                                visible: seriesVisibleFwd,
                                                lineWidth: 2,
                                                priceLineVisible: false,
                                                lastValueVisible: showValueOnScaleFwd,
                                                crosshairMarkerVisible: false,
                                                pointMarkersVisible: false,
                                                priceScaleId: globalChartSettings.PriceAxisLocation === 'left' ? 'left' : 'right'
                                            })
                                            : chart.addLineSeries({
                                                color: colorsFwd.border,
                                                title: seriesTitleFwd,
                                                visible: seriesVisibleFwd,
                                                lineWidth: 2,
                                                priceLineVisible: false,
                                                lastValueVisible: showValueOnScaleFwd,
                                                crosshairMarkerVisible: false,
                                                pointMarkersVisible: false,
                                                priceScaleId: globalChartSettings.PriceAxisLocation === 'left' ? 'left' : 'right'
                                            });
                                        lwSeriesByEndpoint[endpoint] = seriesFwd;
                                        if (segmentPrimitive && (isSpot || !segmentPrimitive.hasPriceSeries())) segmentPrimitive.setPriceSeries(seriesFwd);
                                        lwSeriesList.push({ endpoint, series: seriesFwd, label: datasetLabels[endpoint], color: colorsFwd.border, lastTime: null });
                                    } else {
                                        seriesFwd.applyOptions({ color: colorsFwd.border, title: seriesTitleFwd, visible: seriesVisibleFwd, lastValueVisible: showValueOnScaleFwd });
                                    }
                                    if (appendOnly && seriesFwd && chartAppliedPointCount < optimizedLabels.length) {
                                        var firstNewTimeFwd = dateToLWTime(optimizedLabels[chartAppliedPointCount]);
                                        var startIdxFwd = 0;
                                        for (var fi = 0; fi < lwDataFwd.length; fi++) {
                                            if (lwDataFwd[fi].time >= firstNewTimeFwd) { startIdxFwd = fi; break; }
                                        }
                                        for (var i = startIdxFwd; i < lwDataFwd.length; i++) {
                                            try { seriesFwd.update({ time: lwDataFwd[i].time, value: lwDataFwd[i].value }); } catch (e) {}
                                        }
                                    } else {
                                        seriesFwd.setData(lwDataFwd.map(function (p) { return { time: p.time, value: p.value }; }));
                                    }
                                    seriesFwd.applyOptions({ priceLineVisible: false, lineVisible: !(isLevelHistoryFwd && styleNow === 'scatter'), lineWidth: (isLevelHistoryFwd && styleNow === 'scatter') ? 0 : 2 });
                                    if (isLevelHistoryFwd && segmentPrimitive) {
                                        if (styleNow === 'scatter') segmentPrimitive.setScatterData(endpoint, lwDataFwd, colorsFwd.border, seriesVisibleFwd);
                                        else segmentPrimitive.clearScatter(endpoint);
                                    }
                                    const lastPf = lwDataFwd[lwDataFwd.length - 1];
                                    if (lastPf.value !== 0) lastValueByEndpoint[endpoint] = lastPf.value;
                                    const listEntryFwd = lwSeriesList.find((e) => e.endpoint === endpoint);
                                    if (listEntryFwd) listEntryFwd.lastTime = lwTimeToDate(lastPf.time);
                                    datasetsAdded++;
                                }
                            } else {
                                const segmentArraysFwd = splitSegmentsToLwArrays(dataPointsFwd, dateToLWTime);
                                if (segmentArraysFwd.length > 0) {
                                    const segmentPairsFwd = segmentArraysFwd.flatMap(function (seg) { return seg; });
                                    if (segmentPairsFwd.length >= 2) {
                                        let seriesFwd = canUpdateInPlace ? lwSeriesByEndpoint[endpoint] : null;
                                        if (!seriesFwd) {
                                            seriesFwd = chart.addCustomSeries(new HorizontalSegmentsPaneView(chart.timeScale()), {
                                                color: colorsFwd.border,
                                                title: seriesTitleFwd,
                                                visible: seriesVisibleFwd,
                                                lineWidth: 2,
                                                priceLineVisible: false,
                                                lastValueVisible: showValueOnScaleFwd,
                                                crosshairMarkerVisible: false
                                            });
                                            lwSeriesByEndpoint[endpoint] = seriesFwd;
                                            lwSeriesList.push({ endpoint, series: seriesFwd, label: datasetLabels[endpoint], color: colorsFwd.border, lastTime: null });
                                        } else {
                                            seriesFwd.applyOptions({ color: colorsFwd.border, title: seriesTitleFwd, visible: seriesVisibleFwd, lastValueVisible: showValueOnScaleFwd });
                                        }
                                        if (appendOnly && seriesFwd && chartAppliedPointCount < optimizedLabels.length) {
                                            const firstNewTime = dateToLWTime(optimizedLabels[chartAppliedPointCount]);
                                            let startIdx = 0;
                                            for (let k = 0; k < segmentPairsFwd.length; k++) {
                                                if (segmentPairsFwd[k].time >= firstNewTime) { startIdx = k; break; }
                                            }
                                            let appendOk = true;
                                            for (let k = startIdx; k < segmentPairsFwd.length; k++) {
                                                try { seriesFwd.update({ time: segmentPairsFwd[k].time, value: segmentPairsFwd[k].value }); } catch (e) { appendOk = false; break; }
                                            }
                                            if (!appendOk) {
                                                // Do NOT fall back to setData: that would replace all data and redraw history.
                                            }
                                        } else {
                                            seriesFwd.setData(segmentPairsFwd.map(function (p) { return { time: p.time, value: p.value }; }));
                                        }
                                        seriesFwd.applyOptions({ priceLineVisible: false });
                                        const lastPf = segmentPairsFwd[segmentPairsFwd.length - 1];
                                        if (lastPf && lastPf.value !== 0) lastValueByEndpoint[endpoint] = lastPf.value;
                                        const listEntryFwd = lwSeriesList.find((e) => e.endpoint === endpoint);
                                        if (listEntryFwd) listEntryFwd.lastTime = lastPf ? lwTimeToDate(lastPf.time) : null;
                                        datasetsAdded++;
                                    }
                                }
                            }
                            await logToBackend('info', `[Chart] Forwarded ${endpoint} with previous value ${lastValue} (no new data)`);
                        } else {
                            await logToBackend('info', `[Chart] Skipping ${endpoint}: no data or empty array`);
                        }
                    }
                }
                
                await Promise.all(batchPromises);

                paintLevelHistory();
                
                chartAppliedPointCount = optimizedLabels.length;

                if (!initialZoomSet) {
                    fitSessionInset();
                    userHasZoomed = false;
                    savedZoomState = null;
                    initialZoomSet = true;
                } else if (autoFollowEnabled) {
                    commitFollow();
                } else if (!appendOnly && savedVisibleLogicalRange) {
                    writeTimeRange(savedVisibleLogicalRange);
                }
                if (initialZoomSet && typeof zoomHelpers.followSpotPrice === 'function') {
                    zoomHelpers.followSpotPrice();
                }
                
                if (!isMouseOverChart) updateCrosshairToLatest();
                
                await logToBackend('info', `[Chart] Chart updated with ${lwSeriesList.length} series and ${optimizedLabels.length} labels`);
                
                const dataPoints = optimizedLabels.length;
                const optimizationNote = originalPointCount > dataPoints ? ` (optimized from ${originalPointCount})` : '';
                statusEl.textContent = `Loaded ${dataPoints} data points${optimizationNote}`;
                statusEl.className = '';
                if (live && typeof live.redrawProfile === 'function') live.redrawProfile();
                if (live && typeof live.noteLevelTouches === 'function') live.noteLevelTouches();
                if (initialZoomSet) firstChartLoadDone = true;
                updateHeaderDate();
                updateSpotReadout();
                
            } catch (error) {
                await logToBackend('error', `[Chart] Error updating chart: ${error.message || error}`);
                await logToBackend('error', `[Chart] Error details: name=${error.name}, message=${error.message}, stack=${error.stack || 'N/A'}`);
                statusEl.textContent = `Error: ${error.message || 'Failed to load data'}`;
                statusEl.className = 'error';
            }
        }
        
            // ---- Header readouts & series legend ----
            let marketHoursLocal = null;
            let lastSpotValue = null;
            let lastSpotChange = null;

            function formatCountdown(ms) {
                if (!Number.isFinite(ms) || ms < 0) return '';
                const total = Math.max(0, Math.floor(ms / 1000));
                const h = Math.floor(total / 3600);
                const m = Math.floor((total % 3600) / 60);
                const s = total % 60;
                const mm = String(m).padStart(2, '0');
                const ss = String(s).padStart(2, '0');
                return h > 0 ? h + ':' + mm + ':' + ss : m + ':' + ss;
            }

            function updateHeaderDate() {
                const el = document.getElementById('chart-date');
                if (el) el.textContent = currentChartDateStr || dateFromURL || 'Live';
            }

            function updateMarketPill() {
                const pill = document.getElementById('market-state-pill');
                if (!pill) return;
                if (!marketHoursLocal || !marketHoursLocal.open || !marketHoursLocal.close) {
                    pill.className = 'pill';
                    pill.textContent = '—';
                    return;
                }
                const now = new Date();
                const openParts = marketHoursLocal.open.split(':').map(Number);
                const closeParts = marketHoursLocal.close.split(':').map(Number);
                const open = new Date(now); open.setHours(openParts[0], openParts[1], 0, 0);
                const close = new Date(now); close.setHours(closeParts[0], closeParts[1], 0, 0);
                if (close.getTime() <= open.getTime()) close.setDate(close.getDate() + 1);
                if (now < open) {
                    pill.className = 'pill closed';
                    pill.textContent = 'Closed · opens in ' + formatCountdown(open - now);
                } else if (now < close) {
                    pill.className = 'pill open';
                    pill.textContent = 'Open · ' + formatCountdown(close - now) + ' to close';
                } else {
                    pill.className = 'pill closed';
                    pill.textContent = 'Closed';
                }
            }

            function updateSpotReadout() {
                const wrap = document.getElementById('spot-readout');
                if (!wrap) return;
                const priceEl = wrap.querySelector('.spot-price');
                const chgEl = wrap.querySelector('.spot-chg');
                const spot = lastValueByEndpoint.spot;
                if (spot == null || typeof spot !== 'number' || !isFinite(spot)) {
                    if (priceEl) priceEl.textContent = '—';
                    if (chgEl) chgEl.textContent = '';
                    return;
                }
                if (lastSpotValue == null) lastSpotValue = spot;
                const delta = spot - lastSpotValue;
                if (delta !== 0) lastSpotChange = delta;
                if (priceEl) priceEl.textContent = spot.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
                if (chgEl) {
                    if (lastSpotChange == null) {
                        chgEl.textContent = '';
                    } else {
                        const change = lastSpotChange;
                        const sign = change > 0 ? '+' : '';
                        const pct = lastSpotValue ? (change / lastSpotValue) * 100 : 0;
                        chgEl.textContent = sign + change.toFixed(2) + ' (' + sign + pct.toFixed(2) + '%)';
                    }
                }
                wrap.classList.toggle('up', (lastSpotChange || 0) > 0);
                wrap.classList.toggle('down', (lastSpotChange || 0) < 0);
                lastSpotValue = spot;
            }

            async function pollRequestCount() {
                try {
                    const res = await fetch('/api/request-count', { cache: 'no-store' });
                    if (res.ok) {
                        const d = await res.json();
                        const b = document.querySelector('#chart-req-counter b');
                        if (b) b.textContent = ((d && d.count) || 0).toLocaleString('en-US');
                    }
                } catch (e) {}
            }

            function setupHeaderReadouts() {
                getMarketHoursLocal().then((mh) => {
                    marketHoursLocal = mh;
                    updateMarketPill();
                    setInterval(updateMarketPill, 1000);
                }).catch(() => {});
                pollRequestCount();
                setInterval(pollRequestCount, 5000);
                const pinBtn = document.getElementById('pin-btn');
                if (pinBtn) {
                    pinBtn.addEventListener('click', async () => {
                        const on = !pinBtn.classList.contains('active');
                        pinBtn.classList.toggle('active', on);
                        pinBtn.title = on ? 'Unpin (always on top)' : 'Keep this window on top';
                        try {
                            const mod = await import('/bindings/market-terminal/app.js');
                            if (typeof mod.SetAlwaysOnTop === 'function') await mod.SetAlwaysOnTop(ticker, on);
                        } catch (err) {
                            console.warn('[Chart] SetAlwaysOnTop failed:', err);
                            pinBtn.classList.toggle('active', !on);
                            pinBtn.title = 'Keep this window on top';
                        }
                    });
                }
            }

            // Show or hide a series on the plot, the right-scale label, and the segment overlay.
            function applySeriesAppearance(endpoint, opts) {
                const series = lwSeriesByEndpoint[endpoint];
                if (series && typeof series.applyOptions === 'function') {
                    const patch = {};
                    if (opts.visible !== undefined) {
                        patch.visible = !!opts.visible;
                        patch.lastValueVisible = !!opts.visible;
                    }
                    if (opts.title !== undefined) patch.title = opts.title;
                    try { series.applyOptions(patch); } catch (e) {}
                }
                if (opts.visible !== undefined && segmentPrimitive && typeof segmentPrimitive.setSegmentVisible === 'function') {
                    segmentPrimitive.setSegmentVisible(endpoint, !!opts.visible);
                }
                if (opts.visible !== undefined) {
                    const row = document.querySelector('.crosshair-series-row[data-endpoint="' + endpoint + '"]');
                    if (row) row.classList.toggle('series-hidden', !opts.visible);
                }
            }

            function readBufferPx(value) {
                var px = Number(value);
                if (!Number.isFinite(px) || px < 0) return 16;
                return px;
            }

            function plotWidthPx() {
                var plotWidth = 0;
                try {
                    var rangeWidth = chart.timeScale().width();
                    if (typeof rangeWidth === 'number' && rangeWidth > 1) plotWidth = rangeWidth;
                } catch (e) {}
                if (!plotWidth) {
                    var div = document.getElementById('chart-div');
                    plotWidth = div && div.clientWidth ? div.clientWidth : 800;
                }
                return plotWidth;
            }

            function followBufferPx() {
                return readBufferPx(globalChartSettings.AutoFollowBufferPx);
            }

            // Bars of empty space that come out to the saved pixel gap at the current width.
            function bufferBarsFor(visibleBars) {
                var width = plotWidthPx();
                var px = Math.min(followBufferPx(), width * 0.45);
                var bars = visibleBars > 0 ? visibleBars : 1;
                if (!(px > 0) || !(width > 0)) return 0;
                return px * bars / width;
            }

            // One writer for the time axis. Hold does not move the range. Follow keeps the
            // current bar span and leaves the pixel gap after the last bar. Reset shows the session.
            let followFrame = 0;
            function logicalRange() {
                try {
                    var logical = chart.timeScale().getVisibleLogicalRange();
                    if (logical && Number.isFinite(logical.from) && Number.isFinite(logical.to) && logical.to > logical.from) {
                        return { from: logical.from, to: logical.to };
                    }
                } catch (e) {}
                return null;
            }
            function writeTimeRange(range) {
                if (!range || !Number.isFinite(range.from) || !Number.isFinite(range.to) || !(range.to > range.from)) return;
                var current = logicalRange();
                if (current && Math.abs(current.from - range.from) < 0.25 && Math.abs(current.to - range.to) < 0.25) return;
                markProgrammaticRange();
                try { chart.timeScale().setVisibleLogicalRange({ from: range.from, to: range.to }); } catch (e) {}
            }
            function followRange() {
                var lastIdx = chartDataLastLogicalIndex;
                if (lastIdx == null || typeof lastIdx !== 'number' || lastIdx < 0) return null;
                var logical = logicalRange();
                var span = logical ? (logical.to - logical.from) : (lastIdx + 1);
                if (!(span > 0)) span = lastIdx + 1;
                var gap = bufferBarsFor(span);
                return { from: lastIdx + gap - span, to: lastIdx + gap };
            }
            function commitFollow() {
                if (!autoFollowEnabled) return;
                writeTimeRange(followRange());
            }
            function scheduleFollow() {
                if (!autoFollowEnabled) return;
                var token = ++followFrame;
                requestAnimationFrame(function () {
                    if (token !== followFrame || !autoFollowEnabled) return;
                    commitFollow();
                });
            }
            function resizeChart(w, h) {
                var held = autoFollowEnabled ? null : logicalRange();
                try {
                    chart.applyOptions({ autoSize: false });
                    if (typeof chart.resize === 'function') chart.resize(Math.round(w), Math.round(h), true);
                    else chart.applyOptions({ width: Math.round(w), height: Math.round(h) });
                } catch (e) {}
                if (autoFollowEnabled) scheduleFollow();
                else if (held) writeTimeRange(held);
            }

            // Price range first, then one time inset after the price scale has taken its width.
            let fitToken = 0;
            function applyTimeInset() {
                var lastIdx = chartDataLastLogicalIndex;
                if (lastIdx == null || typeof lastIdx !== 'number' || lastIdx < 0) {
                    markProgrammaticRange();
                    try { chart.timeScale().fitContent(); } catch (e) {}
                    return;
                }
                var plotWidth = plotWidthPx();
                var padPx = Math.min(followBufferPx(), plotWidth * 0.45);
                if (!(padPx > 0)) {
                    writeTimeRange({ from: 0, to: lastIdx });
                    return;
                }
                var bars = lastIdx + 1;
                var inset = bars * (padPx / (plotWidth - padPx * 2));
                writeTimeRange({ from: -inset, to: lastIdx + inset });
            }
            function fitSessionInset() {
                const token = ++fitToken;
                try { if (live && typeof live.resetChartProfileX === 'function') live.resetChartProfileX(); } catch (e) {}
                try { setPriceScaleFromSpotOnly(); } catch (e) {}
                requestAnimationFrame(function () {
                    if (token !== fitToken) return;
                    try { applyTimeInset(); } catch (e) {}
                    try { setPriceScaleFromSpotOnly(); } catch (e) {}
                    if (live && typeof live.relinkProfilePrice === 'function') live.relinkProfilePrice();
                });
            }

            async function resolveChartIsLive(dateStr) {
                if (!dateFromURL) return true;
                try {
                    const response = await fetch('/api/market-date');
                    if (!response.ok) return false;
                    const data = await response.json();
                    const marketDate = data && (data.date || data.Date);
                    return marketDate === dateStr;
                } catch (e) {
                    return false;
                }
            }

            function syncAutoFollowButton() {
                const autoFollowBtn = document.getElementById('auto-follow-btn');
                if (!autoFollowBtn) return;
                autoFollowBtn.textContent = `Auto-Follow: ${autoFollowEnabled ? 'ON' : 'OFF'}`;
                autoFollowBtn.classList.toggle('active', autoFollowEnabled);
            }

            function startLivePolling() {
                if (!chartIsLive || updateInterval) return;
                updateInterval = setInterval(updateChart, 1500);
            }

            // Reset view: show the whole session inside the plot.
            async function applyResetWithBuffer() {
                fitSessionInset();
            }
            
            // Setup reset zoom button
            const resetZoomBtn = document.getElementById('reset-zoom-btn');
            if (resetZoomBtn) {
                resetZoomBtn.addEventListener('click', async () => {
                    try {
                        await applyResetWithBuffer();
                        userHasZoomed = false;
                        savedZoomState = null;
                        await logToBackend('info', '[Chart] Reset zoom to default filter').catch(() => {});
                    } catch (error) {
                        console.error('[Chart] Error resetting zoom:', error);
                        await logToBackend('error', `[Chart] Error resetting zoom: ${error.message || error}`).catch(() => {});
                    }
                });
            }
            
            // Setup auto-follow button
            const autoFollowBtn = document.getElementById('auto-follow-btn');
            if (autoFollowBtn) {
                // Set initial state (default ON)
                autoFollowBtn.textContent = `Auto-Follow: ${autoFollowEnabled ? 'ON' : 'OFF'}`;
                autoFollowBtn.classList.toggle('active', autoFollowEnabled);
                
                autoFollowBtn.addEventListener('click', () => {
                    autoFollowEnabled = !autoFollowEnabled;
                    autoFollowBtn.textContent = `Auto-Follow: ${autoFollowEnabled ? 'ON' : 'OFF'}`;
                    autoFollowBtn.classList.toggle('active', autoFollowEnabled);
                    if (autoFollowEnabled) scheduleFollow();
                    logToBackend('info', `[Chart] Auto-follow ${autoFollowEnabled ? 'enabled' : 'disabled'}`).catch(() => {});
                });
            }
            
            const screenshotBtn = document.getElementById('chart-screenshot-btn');
            if (screenshotBtn) {
                screenshotBtn.addEventListener('click', function onScreenshotClick() {
                    requestAnimationFrame(function () {
                        try {
                            var shot = captureWindow();
                            shot.toBlob(function (blob) {
                                if (!blob) {
                                    logToBackend('warn', '[Chart] Screenshot: toBlob returned null').catch(function () {});
                                    return;
                                }
                                var url = URL.createObjectURL(blob);
                                var a = document.createElement('a');
                                var safeTicker = String(ticker).replace(/[^\w.-]+/g, '_');
                                var datePart = String(currentChartDateStr || dateFromURL || '').replace(/[^\w.-]+/g, '_') || 'nodate';
                                var ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
                                a.href = url;
                                a.download = safeTicker + '_' + datePart + '_' + ts + '.png';
                                document.body.appendChild(a);
                                a.click();
                                document.body.removeChild(a);
                                URL.revokeObjectURL(url);
                                logToBackend('info', '[Chart] Screenshot saved: ' + a.download).catch(function () {});
                            }, 'image/png');
                        } catch (err) {
                            console.error('[Chart] Screenshot error:', err);
                            logToBackend('error', '[Chart] Screenshot error: ' + (err && err.message ? err.message : String(err))).catch(function () {});
                        }
                    });
                });
            }
            
            // Load colors from settings, then initialize chart
            await logToBackend('info', '[Chart] About to load chart colors...');
            let didInitialLoad = false;
            loadChartColors().then(async () => {
                await logToBackend('info', '[Chart] Colors loaded, initializing chart...');
                await logToBackend('info', `[Chart] Chart container: ${document.getElementById('chart-div') ? 'found' : 'NOT FOUND'}`);
                await logToBackend('info', `[Chart] Chart object: ${chart ? 'created' : 'NOT CREATED'}`);
                await updateChart();
                didInitialLoad = true;
                // Show latest data in crosshair info box on initial load
                updateCrosshairToLatest();
                startLivePolling();
            }).catch(async (err) => {
                await logToBackend('error', `[Chart] Error loading colors: ${err.message || err}`);
                if (!didInitialLoad) {
                    await updateChart();
                    didInitialLoad = true;
                }
                try { updateCrosshairToLatest(); } catch (e) {}
                startLivePolling();
            });
            
            // Live dates poll every 1.5s. A finished session loads once.
            window.addEventListener('beforeunload', () => {
                if (updateInterval) clearInterval(updateInterval);
            });
            
            // Listen for settings updates from the main window to refresh colors
            try {
                const settingsChannel = new BroadcastChannel('market-terminal-settings');
                settingsChannel.onmessage = async (event) => {
                    if (event.data && event.data.type === 'settings-updated') {
                        const zoomBefore = globalChartSettings.ChartZoomFilterPercent;
                        await logToBackend('info', '[Chart] Received settings update broadcast, refreshing colors...');
                        
                        // Update global settings
                        if (event.data.settings) {
                            const s = event.data.settings;
                            setChartDebug(!!s.EnableDebug);
                            globalChartSettings.UseMarketTime = s.UseMarketTime || false;
                            globalChartSettings.Use24HourTime = s.Use24HourTime !== false;
                            globalChartSettings.HiddenPlots = s.HiddenPlots || [];
                            globalChartSettings.ChartColorsOff = s.ChartColorsOff || [];
                            globalChartSettings.ChartZoomFilterPercent = s.ChartZoomFilterPercent || 1.0;
                            globalChartSettings.AutoFollowBufferPx = readBufferPx(s.AutoFollowBufferPx);
                            globalChartSettings.PriceAxisLocation = (s.PriceAxisLocation || 'left').toLowerCase() === 'right' ? 'right' : 'left';
                            globalChartSettings.ProfileSettings = s.ProfileSettings || globalChartSettings.ProfileSettings;
                            if (live && typeof live.applyProfileSettings === 'function') live.applyProfileSettings();
                        }
                        
                        await loadChartColors();
                        if (chart) applyPriceAxisSettings(chart);
                        const datasetColors = getDatasetColors();
                        lwSeriesList.forEach((e) => {
                            if (datasetColors[e.endpoint]) {
                                e.series.applyOptions({ color: datasetColors[e.endpoint].border });
                                e.color = datasetColors[e.endpoint].border;
                            }
                            const isHidden = globalChartSettings.HiddenPlots.includes(e.endpoint);
                            const isHiddenOnChart = currentChartPrefs.hiddenSeries.includes(e.endpoint);
                            const isOffByColor = isSeriesOffByColorSettings(e.endpoint);
                            const visible = !isHidden && !isHiddenOnChart && !isOffByColor;
                            e.series.applyOptions({ visible, lastValueVisible: visible });
                        });
                        await logToBackend('info', '[Chart] Colors and visibility refreshed from settings update');
                        if (globalChartSettings.ChartZoomFilterPercent !== zoomBefore) {
                            try { setPriceScaleFromSpotOnly(); } catch (e) {}
                        }
                        try { scheduleFollow(); } catch (e) {}
                        if (live && typeof live.redrawProfile === 'function') live.redrawProfile();
                        paintLevelHistory();
                    }
                };
                
                // Clean up channel on window close
                window.addEventListener('beforeunload', () => {
                    settingsChannel.close();
                });
            } catch (e) {
                console.warn('[Chart] Failed to set up settings broadcast listener:', e);
            }

            window.addEventListener('storage', function (event) {
                if (!event) return;
                const prefsKey = chartPrefsKey(ticker);
                if (event.key === prefsKey) {
                    currentChartPrefs = loadChartPrefs(ticker, currentChartDateStr || dateFromURL || '');
                    infoBoxStateKey = '';
                    syncInfoBoxCollapsed();
                    if (live && typeof live.applyChartProfilePrefs === 'function') live.applyChartProfilePrefs();
                }
                if (event.key === prefsKey) paintLevelHistory();
                if (event.key !== null && event.key !== 'profile-view') return;
                if ((event.key === null || event.key === 'profile-view') && live && typeof live.applyStoredProfileView === 'function') {
                    live.applyStoredProfileView();
                }
            });
            
            } catch (error) {
                // Log any errors that occur during initialization
                try {
                    fetch('/api/frontend-log', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ level: 'error', message: `[Chart] CRITICAL ERROR in async IIFE: ${error.message || error} - Stack: ${error.stack || 'N/A'}` })
                    }).catch(() => {});
                } catch (e) {
                    // Ignore
                }
                console.error('[Chart] Error:', error);
            }
        })(); // End of async IIFE
