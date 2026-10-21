// One-snapshot profile ladders. The chart load never includes these blobs.
// On a chart, every visible layer shares that chart's price scale.
// A detached window keeps its own price scale and a horizontal scale the user can move.

const DTE_LABELS = { zero: '0 DTE', one: '1 DTE', full: '90 DTE' };

const SPOT_MARK = '#00FFFF';
const ZERO_MARK = '#FCB103';
const ZERO_HOVER = 'price level where GEX by strike is neutral';

const LAYER_SPECS = [
    { id: 'classic', label: 'Classic', pos: '#2BE05C', neg: '#FF4747', oiPos: '#00942A', oiNeg: '#940D0D', dots: true, greek: false },
    { id: 'state', label: 'State', pos: '#2BE05C', neg: '#FF0000', oiPos: '#00942A', oiNeg: '#940D0D', dots: true, greek: false },
    { id: 'gamma', label: 'Gamma', pos: '#00FFFF', neg: '#AE4AD5', dots: false, greek: true },
    { id: 'delta', label: 'Delta', pos: '#00FFFF', neg: '#AE4AD5', dots: false, greek: true },
    { id: 'vanna', label: 'Vanna', pos: '#00FFFF', neg: '#AE4AD5', dots: false, greek: true },
    { id: 'charm', label: 'Charm', pos: '#00FFFF', neg: '#AE4AD5', dots: false, greek: true },
];

// 1, 5, 10, 15, 30 minutes. One blue ramp, light to navy.
const DOT_COLORS = ['#ADD8E6', '#5DD6F5', '#00A2DE', '#006CC0', '#003494'];
const DOT_RADIUS = [5.2, 4.5, 3.9, 3.4, 2.9];
const DOT_LABELS = ['1 min', '5 min', '10 min', '15 min', '30 min'];
const hoverState = { hits: [], space: '' };
const PREF_KEY = 'profile-view';

export function readChartPrefs(ticker, dateStr) {
    try {
        const raw = localStorage.getItem('chartPrefs_' + (ticker || '') + '_' + (dateStr || ''));
        const parsed = raw ? JSON.parse(raw) : null;
        return parsed && typeof parsed === 'object' ? parsed : {};
    } catch (err) {
        return {};
    }
}

export function writeChartPrefs(ticker, dateStr, patch) {
    const next = Object.assign({}, readChartPrefs(ticker, dateStr), patch || {});
    try {
        localStorage.setItem('chartPrefs_' + (ticker || '') + '_' + (dateStr || ''), JSON.stringify(next));
    } catch (err) {}
    return next;
}

export function chartProfileFields(source) {
    const src = source && typeof source === 'object' ? source : {};
    return {
        ladderAlign: src.ladderAlign === 'left' || src.ladderAlign === 'right' || src.ladderAlign === 'center' ? src.ladderAlign : '',
        metricsVisible: src.metricsVisible === false ? false : true,
        metricsSide: src.metricsSide === 'left' ? 'left' : 'right',
        profileAxis: src.profileAxis === 'off' || src.profileAxis === 'left' || src.profileAxis === 'right' ? src.profileAxis : '',
        profileLevels: levelPrefs(src),
        levelLabelX: levelLabelPercent(src),
        profileDocked: src.profileDocked !== false,
        alerts: {
            call: !!(src.alerts && src.alerts.call === true),
            put: !!(src.alerts && src.alerts.put === true),
            long: !!(src.alerts && src.alerts.long === true),
            short: !!(src.alerts && src.alerts.short === true),
        },
    };
}

function defaultProfileLayers() {
    return {
        classic: true,
        state: false,
        gamma: false,
        delta: false,
        vanna: false,
        charm: false,
    };
}

function profileLayersFrom(source) {
    const layers = defaultProfileLayers();
    const raw = source && source.profileLayers;
    if (raw && typeof raw === 'object') {
        for (const spec of LAYER_SPECS) {
            if (typeof raw[spec.id] === 'boolean') layers[spec.id] = raw[spec.id];
        }
        return layers;
    }
    try {
        const shared = JSON.parse(localStorage.getItem(PREF_KEY) || '{}');
        if (shared.layers && typeof shared.layers === 'object') {
            for (const spec of LAYER_SPECS) {
                if (typeof shared.layers[spec.id] === 'boolean') layers[spec.id] = shared.layers[spec.id];
            }
        }
    } catch (err) {}
    return layers;
}

function readProfileLayers(ticker, dateStr) {
    return profileLayersFrom(readChartPrefs(ticker, dateStr));
}

function readProfileDte(ticker, dateStr) {
    const saved = readChartPrefs(ticker, dateStr).profileDte;
    if (saved === 'zero' || saved === 'one' || saved === 'full') return saved;
    try {
        const shared = JSON.parse(localStorage.getItem(PREF_KEY) || '{}');
        if (shared.dte === 'one' || shared.dte === 'full' || shared.dte === 'zero') return shared.dte;
    } catch (err) {}
    return 'zero';
}

function paintLayerChecks(box, layers) {
    if (!box) return;
    for (const input of box.querySelectorAll('input[data-layer]')) {
        input.checked = !!layers[input.dataset.layer];
    }
}

function copyProfileLayers(layers) {
    const next = {};
    for (const spec of LAYER_SPECS) next[spec.id] = !!layers[spec.id];
    return next;
}

function resolvedAxis(ticker, dateStr, paneSide) {
    const stored = readChartPrefs(ticker, dateStr).profileAxis;
    if (stored === 'off' || stored === 'left' || stored === 'right') return stored;
    return paneSide === 'left' ? 'right' : 'left';
}
const SAVED_LADDER = 'Saved ladder from before endpoints were stored separately.';

function publishSettings() {
    return fetch('/api/settings').then(function (res) {
        if (!res.ok) throw new Error('settings');
        return res.json();
    }).then(function (settings) {
        try {
            const channel = new BroadcastChannel('market-terminal-settings');
            channel.postMessage({ type: 'settings-updated', settings: settings });
            channel.close();
        } catch (err) {}
        return settings;
    });
}

function postProfile(url, body) {
    return fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
    }).then(function (res) {
        if (!res.ok) throw new Error(url);
        return publishSettings();
    }).catch(function () {});
}

export const PROFILE_LEVELS = [
    { id: 'spot', label: 'Spot', color: SPOT_MARK, field: 'spot' },
    { id: 'zero', label: 'Zero gamma', color: ZERO_MARK, field: 'zero_gamma' },
    { id: 'pos', label: 'Positive gamma', color: '#2196F3', field: 'major_pos_vol' },
    { id: 'neg', label: 'Negative gamma', color: '#F44336', field: 'major_neg_vol' },
    { id: 'long', label: 'Long gamma', color: '#9C27B0', field: 'major_long_gamma' },
    { id: 'short', label: 'Short gamma', color: '#00BCD4', field: 'major_short_gamma' },
    { id: 'majorPos', label: 'Major positive', color: '#8BC34A', field: 'major_positive' },
    { id: 'majorNeg', label: 'Major negative', color: '#FF5722', field: 'major_negative' },
    { id: 'posOi', label: 'Positive OI', color: '#3F51B5', field: 'major_pos_oi' },
    { id: 'negOi', label: 'Negative OI', color: '#E91E63', field: 'major_neg_oi' },
];

export function levelLabelPercent(source) {
    const n = Number(source && source.levelLabelX);
    if (!Number.isFinite(n)) return 0;
    return Math.max(0, Math.min(100, Math.round(n)));
}

function levelTextX(ctx, caption, left, right, percent) {
    const width = ctx.measureText(caption).width;
    const room = Math.max(0, (right - left) - width - 8);
    const t = (Number.isFinite(percent) ? Math.max(0, Math.min(100, percent)) : 0) / 100;
    return left + 4 + room * t;
}

function levelPrefs(source) {
    const bag = source && source.profileLevels;
    const out = {};
    for (const level of PROFILE_LEVELS) out[level.id] = !(bag && bag[level.id] === false);
    return out;
}

function levelPrice(data, field) {
    if (!data) return null;
    if (field === 'spot') {
        const spot = snapshotSpot(data);
        return spot != null && spot !== 0 ? spot : null;
    }
    if (field === 'zero_gamma') {
        const direct = numberOr(data.zero_gamma, null);
        if (direct != null && direct !== 0) return direct;
    }
    const profiles = data.profiles || {};
    const vol = field === 'major_pos_vol' || field === 'major_neg_vol' || field === 'major_pos_oi' || field === 'major_neg_oi' || field === 'zero_gamma';
    const prefer = vol
        ? ['classic_zero', 'classic_one', 'classic_full', 'state_zero', 'state_one', 'state_full']
        : ['gamma_zero', 'gamma_one', 'delta_zero', 'delta_one', 'vanna_zero', 'vanna_one', 'charm_zero', 'charm_one'];
    for (const key of prefer) {
        const price = priceField(profiles[key], field);
        if (price != null) return price;
    }
    for (const key of Object.keys(profiles)) {
        const price = priceField(profiles[key], field);
        if (price != null) return price;
    }
    return null;
}

function marksFor(data, ticker, dateStr) {
    const enabled = levelPrefs(readChartPrefs(ticker, dateStr));
    const marks = [];
    for (const level of PROFILE_LEVELS) {
        if (enabled[level.id] === false) continue;
        const price = levelPrice(data, level.field);
        if (price == null) continue;
        marks.push({ id: level.id, price: price, color: level.color, label: level.label });
    }
    return marks;
}

function joinLabels(names) {
    if (!names.length) return '';
    if (names.length === 1) return names[0];
    if (names.length === 2) return names[0] + ' and ' + names[1];
    return names.slice(0, -1).join(', ') + ', and ' + names[names.length - 1];
}

function fullGreekNote(dte, layerOn) {
    if (dte !== 'full') return '';
    const checked = [];
    for (const spec of LAYER_SPECS) {
        if (spec.greek && layerOn(spec.id)) checked.push(spec.label);
    }
    if (!checked.length) return '';
    return joinLabels(checked) + ' uses 0 DTE. No ladder was saved for that book.';
}

function ProfilePrimitive() {
    const self = this;
    this._requestUpdate = null;
    this._series = null;
    this._layers = [];
    this._onChart = false;
    this._active = false;
    this._band = { center: 0.62, width: 0.22 };
    this._side = null;
    this._spot = null;
    this._zero = null;
    this._zeroColor = ZERO_MARK;
    this._showDots = true;
    this._over = false;
    this._linked = true;
    this._shift = 0;
    this._span = null;
    this._margins = { top: 0.1, bottom: 0.1 };
    this._paneH = 0;
    this._axis = 'left';
    this._marks = [];
    this._labelX = 0;
    this._view = {
        zOrder: function () { return self._over === false ? 'bottom' : 'top'; },
        renderer: function () {
            return {
                draw: function (target) {
                    const series = self._series;
                    if (!series || typeof series.priceToCoordinate !== 'function') return;
                    const chartY = function (price) {
                        try {
                            const y = series.priceToCoordinate(price);
                            return (typeof y === 'number' && Number.isFinite(y)) ? y : null;
                        } catch (err) {
                            return null;
                        }
                    };
                    const priceY = function (price) {
                        if (!self._linked && self._span && self._paneH > 0) {
                            const top = self._margins.top || 0;
                            const bottom = self._margins.bottom || 0;
                            const innerTop = self._paneH * top + self._shift;
                            const innerH = Math.max(1, self._paneH * (1 - top - bottom));
                            return priceToY(price, self._span.lo, self._span.hi, innerTop, innerH);
                        }
                        const y = chartY(price);
                        return y == null ? null : y + self._shift;
                    };
                    if (!self._active) return;
                    try {
                        if (self._onChart) drawOnChart(target, self._layers, chartY, self._showDots, self._levels, self._priceSide, self._align, self._band);
                        else if (self._side) {
                            const frame = self._paneH > 0 ? { top: self._shift, bottom: self._shift + self._paneH } : null;
                            paintSide(self._side, self._layers, priceY, self._spot, self._zero, self._showDots, self._zeroColor, self._axis, self._marks, self._labelX, frame, self._levels && self._levels.moves);
                            drawMoveBounds(target, chartY, self._levels, self._priceSide);
                        }
                    } catch (err) {
                        console.error('Profile draw failed', err);
                    }
                }
            };
        }
    };
    this.attached = function (param) {
        self._requestUpdate = param && param.requestUpdate ? param.requestUpdate : null;
    };
    this.detached = function () {
        self._requestUpdate = null;
    };
    this.paneViews = function () { return [self._view]; };
    this.setSeries = function (series) { self._series = series; };
    this.setState = function (state) {
        self._active = !!state.active;
        self._onChart = !!state.onChart;
        self._layers = state.layers || [];
        self._band = state.band || self._band;
        self._side = state.side || null;
        self._paneSide = state.paneSide === 'left' ? 'left' : 'right';
        self._spot = state.spot;
        self._zero = state.zero;
        if (state.zeroColor) self._zeroColor = state.zeroColor;
        self._showDots = state.showDots !== false;
        self._levels = state.levels || null;
        self._priceSide = state.priceSide === 'left' ? 'left' : 'right';
        self._align = state.align === 'left' || state.align === 'right' ? state.align : 'center';
        if (state.over != null) self._over = state.over !== false;
        self._linked = state.linked !== false;
        self._shift = Number.isFinite(state.shift) ? state.shift : 0;
        self._span = state.span && state.span.hi > state.span.lo ? state.span : null;
        self._margins = state.margins || self._margins;
        self._paneH = Number.isFinite(state.paneH) ? state.paneH : 0;
        self._axis = state.axis === 'off' || state.axis === 'right' ? state.axis : 'left';
        self._marks = state.marks || [];
        self._labelX = Number.isFinite(state.labelX) ? state.labelX : 0;
    };
    this.requestUpdate = function () {
        if (typeof self._requestUpdate === 'function') self._requestUpdate();
    };
}

export function installProfile(live) {
    const btn = document.getElementById('profile-toggle-btn');
    const pane = document.getElementById('profile-pane');
    const dteSelect = document.getElementById('profile-dte');
    const layersBox = document.getElementById('profile-layers');
    const modeSelect = document.getElementById('profile-mode');
    const canvas = document.getElementById('profile-canvas');
    const note = document.getElementById('profile-note');
    const chartNote = document.getElementById('profile-chart-note');
    if (!btn || !pane || !dteSelect || !layersBox || !canvas || !note || !modeSelect) return;

    const prefs = loadPrefs();
    let preferredOpen = prefs.open;
    let open = false;
    let mode = prefs.mode;
    let docked = prefs.docked !== false;
    const savedDock = readChartPrefs(live.ticker, live.dateFromURL || '').profileDocked;
    if (savedDock === false || savedDock === true) docked = savedDock;
    try {
        const params = new URLSearchParams(window.location.search);
        const attachKey = 'profile-attach-' + (live.ticker || '');
        if (params.get('dock') === '1' || localStorage.getItem(attachKey) === '1') {
            localStorage.removeItem(attachKey);
            docked = true;
            mode = 'side';
            preferredOpen = true;
        }
    } catch (err) {}
    let dte = readProfileDte(live.ticker, live.dateFromURL || '');
    const layersOn = readProfileLayers(live.ticker, live.dateFromURL || '');
    let pendingAt = null;
    let followCrosshair = prefs.follow === true;
    let ladderAlign = prefs.align;
    const seededAlign = chartProfileFields(readChartPrefs(live.ticker, live.dateFromURL || '')).ladderAlign;
    if (seededAlign) ladderAlign = seededAlign;
    let sideLinked = true;
    let sidePriceLo = null;
    let sidePriceHi = null;
    let liveMode = true;
    let playing = false;
    let sliderIndex = 0;
    let lastLiveTs = null;
    let playTimer = 0;
    let timer = 0;
    let requestId = 0;
    let loadFlight = false;
    let loadQueued = false;
    let cached = null;
    const primitive = new ProfilePrimitive();

    dteSelect.value = dte;
    modeSelect.value = mode;
    paintLayerChecks(layersBox, layersOn);
    seedPriceTickMode(live.ticker, live.dateFromURL || '');

    function profileDate() {
        return live.currentChartDateStr || live.dateFromURL || '';
    }

    function layerOn(id) {
        return !!layersOn[id];
    }

    function wanted() {
        const names = [];
        for (const spec of LAYER_SPECS) {
            if (!layerOn(spec.id)) continue;
            const name = endpointFor(spec, dte);
            if (name) names.push(name);
        }
        return names;
    }

    function profileOn() {
        const stored = (live.globalChartSettings && live.globalChartSettings.ProfileSettings) || {};
        return stored.enabled === true || stored.enabled === 'true';
    }

    function savePrefs() {
        try {
            const saved = JSON.parse(localStorage.getItem(PREF_KEY) || '{}');
            saved.open = preferredOpen;
            saved.mode = mode;
            saved.follow = followCrosshair;
            localStorage.setItem(PREF_KEY, JSON.stringify(saved));
        } catch (err) {}
    }

    function saveLayerPrefs() {
        writeChartPrefs(live.ticker, profileDate(), { profileLayers: copyProfileLayers(layersOn) });
    }

    function syncLayersFromPrefs() {
        const next = readProfileLayers(live.ticker, profileDate());
        let changed = false;
        for (const spec of LAYER_SPECS) {
            if (!!layersOn[spec.id] !== !!next[spec.id]) changed = true;
            layersOn[spec.id] = !!next[spec.id];
        }
        paintLayerChecks(layersBox, layersOn);
        return changed;
    }

    function applyProfileSettings() {
        const on = profileOn();
        btn.hidden = !on;
        if (!on) open = false;
        else open = preferredOpen;
        applyChrome();
        if (on && open && docked) schedule();
        else if (!on || !docked) draw();
    }
    live.applyProfileSettings = applyProfileSettings;

    function band() {
        const view = windowProfileSettings();
        let width = view.barWidth;
        width = clamp(width, 8, 70) / 100;
        let center = view.center;
        center = clamp(center, 0, 100) / 100;
        return { width: width, center: center };
    }

    function zeroGammaColor() {
        return ZERO_MARK;
    }

    function showDots() {
        return windowProfileSettings().showPriors;
    }

    function setShowDots(on) {
        writeChartPrefs(live.ticker, profileDate(), { showPriors: !!on });
        draw();
    }
    live.profileDotsOn = showDots;
    live.setProfileDots = setShowDots;

    function showNote(text) {
        const onChart = mode === 'chart';
        note.textContent = onChart ? '' : (text || '');
        note.hidden = onChart || !text;
        if (chartNote) {
            chartNote.textContent = onChart ? (text || '') : '';
            chartNote.hidden = !onChart || !text;
        }
    }

    function clearCanvas(target) {
        const rect = target.getBoundingClientRect();
        if (rect.width < 2 || rect.height < 2) return;
        const dpr = window.devicePixelRatio || 1;
        target.width = Math.floor(rect.width * dpr);
        target.height = Math.floor(rect.height * dpr);
        const ctx = target.getContext('2d');
        ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        ctx.clearRect(0, 0, rect.width, rect.height);
        if (target === canvas) {
            ctx.fillStyle = '#161616';
            ctx.fillRect(0, 0, rect.width, rect.height);
        }
    }

    function ensurePrimitive() {
        const series = live.lwSeriesByEndpoint && live.lwSeriesByEndpoint.spot;
        if (!series || typeof series.attachPrimitive !== 'function') return false;
        if (primitive._series === series) return true;
        if (primitive._series) {
            try { primitive._series.detachPrimitive(primitive); } catch (err) {}
        }
        try {
            series.attachPrimitive(primitive);
        } catch (err) {
            return false;
        }
        primitive.setSeries(series);
        return true;
    }

    function publish(layers, spot, zero) {
        const shown = open && docked;
        const onChart = shown && mode === 'chart';
        const marks = marksFor(cached && cached.data, live.ticker, profileDate());
        const labelX = levelLabelPercent(readChartPrefs(live.ticker, profileDate()));
        const levels = chartLevelState(cached && cached.data, layers, dte);
        if (levels) {
            levels.marks = marks;
            levels.labelX = labelX;
        }
        primitive.setState({
            active: shown && layers.length > 0,
            onChart: onChart,
            layers: layers,
            band: band(),
            side: canvas,
            paneSide: sidePane().side,
            spot: spot,
            zero: zero,
            zeroColor: zeroGammaColor(),
            showDots: showDots(),
            levels: levels,
            marks: marks,
            labelX: labelX,
            priceSide: (live.globalChartSettings && live.globalChartSettings.PriceAxisLocation) === 'left' ? 'left' : 'right',
            align: ladderAlign,
            over: profileOver(),
            linked: sideLinked,
            shift: priceShift(),
            span: profileSpan(),
            margins: priceMargins(),
            paneH: plotHeight(),
            axis: axisSide(),
        });
        if (ensurePrimitive()) primitive.requestUpdate();
        else if (open && !onChart && docked) {
            const frameTop = priceShift();
            const paneH = plotHeight();
            paintSide(canvas, layers, priceY, spot, zero, showDots(), zeroGammaColor(), axisSide(), marks, labelX, paneH > 0 ? { top: frameTop, bottom: frameTop + paneH } : null, levels && levels.moves);
        }
    }

    function noteFor(data, layers) {
        if (data.legacy) {
            const bits = [];
            if (layers.some((layer) => layer.id === 'classic')) bits.push(SAVED_LADDER);
            if (layers.some((layer) => layer.savedGreek)) {
                bits.push('The saved greek row is included. This file does not split it into 0 DTE and 1 DTE.');
            }
            if (layerOn('state')) bits.push('State was not stored as its own ladder in this file.');
            if (bits.length) return bits.join(' ');
        }
        if (!layers.length) {
            const greekNote = fullGreekNote(dte, layerOn);
            if (greekNote) return greekNote;
            const any = LAYER_SPECS.some((spec) => layerOn(spec.id) && endpointFor(spec, dte));
            return any ? ('No ladder in this sample for ' + DTE_LABELS[dte] + '.') : 'No profile layers are turned on.';
        }
        const native = layers.filter((layer) => !layer.fallback).map((layer) => layer.label);
        const fallen = layers.filter((layer) => layer.fallback).map((layer) => layer.label);
        const bits = [];
        if (native.length) bits.push(native.join(', ') + ' · ' + DTE_LABELS[dte]);
        if (fallen.length) bits.push(joinLabels(fallen) + ' · 0 DTE (not in the 90 DTE book)');
        const spot = numberOr(data.spot, null);
        const zeroGamma = numberOr(data.zero_gamma, null);
        if (spot != null) bits.push('spot ' + formatNum(spot));
        if (zeroGamma != null) bits.push('zero gamma ' + formatNum(zeroGamma));
        return bits.join(' · ');
    }

    function draw() {
        if (!profileShown()) {
            hoverState.hits = [];
            hoverState.space = '';
            const closedTip = document.getElementById('profile-hover');
            if (closedTip) closedTip.hidden = true;
            pane.hidden = true;
            pane.classList.add('is-closed');
            primitive.setState({ active: false, onChart: false, layers: [], side: canvas });
            if (ensurePrimitive()) primitive.requestUpdate();
            showNote('');
            paintMetrics();
            return;
        }
        const onChart = mode === 'chart';
        pane.hidden = onChart || !docked;
        pane.classList.toggle('is-closed', pane.hidden);
        const popout = document.getElementById('profile-popout');
        if (popout) popout.hidden = pane.hidden;
        if (!cached || !cached.data || cached.data.missing) {
            hoverState.hits = [];
            hoverState.space = '';
            clearCanvas(canvas);
            publish([], null, null);
            showNote('No profile stored for this time.');
            paintMetrics();
            return;
        }
        const data = cached.data;
        const layers = buildLayers(data, dte, layerOn);
        if (!layers.length) {
            hoverState.hits = [];
            hoverState.space = '';
            clearCanvas(canvas);
            publish([], null, null);
            showNote(noteFor(data, layers));
            paintMetrics();
            return;
        }
        const spotNow = snapshotSpot(data);
        const zeroNow = numberOr(data.zero_gamma, null);
        publish(layers, spotNow, zeroNow);
        showNote(noteFor(data, layers));
        paintMetrics();
    }

    function plotHeight() {
        const chartDiv = document.getElementById('chart-div');
        if (!chartDiv) return 0;
        let timeH = 0;
        try {
            const h = live.chart && live.chart.timeScale().height();
            if (typeof h === 'number' && h > 0) timeH = h;
        } catch (err) {}
        return Math.max(1, chartDiv.getBoundingClientRect().height - timeH);
    }

    function rangeToY(price, range) {
        if (!range || !Number.isFinite(range.from) || !Number.isFinite(range.to) || range.from === range.to) return null;
        const top = Math.max(range.from, range.to);
        const bottom = Math.min(range.from, range.to);
        return ((top - price) / (top - bottom)) * plotHeight();
    }

    function visiblePriceRange() {
        const chart = live.chart;
        if (!chart || typeof chart.priceScale !== 'function') return null;
        const ranges = [];
        for (const id of ['right', 'left']) {
            try {
                const range = chart.priceScale(id).getVisibleRange();
                if (range && Number.isFinite(range.from) && Number.isFinite(range.to) && range.from !== range.to) ranges.push(range);
            } catch (err) {}
        }
        if (!ranges.length) return null;
        let spot = null;
        const cache = live.chartDataCache;
        if (cache && Array.isArray(cache.spot)) {
            for (let i = cache.spot.length - 1; i >= 0; i--) {
                const value = Number(cache.spot[i]);
                if (Number.isFinite(value) && value !== 0) {
                    spot = value;
                    break;
                }
            }
        }
        if (spot != null) {
            const containing = ranges.find((range) => {
                const low = Math.min(range.from, range.to);
                const high = Math.max(range.from, range.to);
                return spot >= low && spot <= high;
            });
            if (containing) return containing;
        }
        return ranges[0];
    }

    function chartSpan() {
        const range = visiblePriceRange();
        if (!range) return null;
        const lo = Math.min(range.from, range.to);
        const hi = Math.max(range.from, range.to);
        if (!(hi > lo)) return null;
        return { lo: lo, hi: hi };
    }

    function profileSpan() {
        if (!sideLinked && sidePriceHi > sidePriceLo) return { lo: sidePriceLo, hi: sidePriceHi };
        return chartSpan();
    }

    function priceMargins() {
        try {
            const id = (live.globalChartSettings && live.globalChartSettings.PriceAxisLocation) === 'left' ? 'left' : 'right';
            const opt = live.chart.priceScale(id).options();
            const margins = opt && opt.scaleMargins;
            if (margins && Number.isFinite(margins.top) && Number.isFinite(margins.bottom)) {
                return { top: margins.top, bottom: margins.bottom };
            }
        } catch (err) {}
        return { top: 0.1, bottom: 0.1 };
    }

    function priceShift() {
        try {
            const el = live.chart && typeof live.chart.chartElement === 'function' ? live.chart.chartElement() : null;
            if (!el) return 0;
            return el.getBoundingClientRect().top - canvas.getBoundingClientRect().top;
        } catch (err) {
            return 0;
        }
    }

    function axisSide() {
        return resolvedAxis(live.ticker, profileDate(), sidePane().side);
    }

    function marginPriceY(price, span) {
        if (!span) return null;
        const margins = priceMargins();
        const paneH = plotHeight();
        const innerTop = paneH * margins.top + priceShift();
        const innerH = Math.max(1, paneH * (1 - margins.top - margins.bottom));
        return priceToY(price, span.lo, span.hi, innerTop, innerH);
    }

    function priceY(price) {
        const span = profileSpan();
        if (!sideLinked) return marginPriceY(price, span);
        const series = live.lwSeriesByEndpoint && live.lwSeriesByEndpoint.spot;
        if (series && typeof series.priceToCoordinate === 'function') {
            try {
                const y = series.priceToCoordinate(price);
                if (typeof y === 'number' && Number.isFinite(y)) return y + priceShift();
            } catch (err) {}
        }
        return marginPriceY(price, span);
    }

    function priceAtPointer(y) {
        const span = profileSpan();
        if (!span) return null;
        if (sideLinked) {
            const series = live.lwSeriesByEndpoint && live.lwSeriesByEndpoint.spot;
            if (series && typeof series.coordinateToPrice === 'function') {
                try {
                    const price = series.coordinateToPrice(y - priceShift());
                    if (typeof price === 'number' && Number.isFinite(price)) return price;
                } catch (err) {}
            }
        }
        const margins = priceMargins();
        const paneH = plotHeight();
        return yToPrice(y - priceShift(), span.lo, span.hi, paneH * margins.top, Math.max(1, paneH * (1 - margins.top - margins.bottom)));
    }

    function zoomSidePrice(anchor, factor) {
        const span = profileSpan();
        if (!span || !Number.isFinite(anchor)) return;
        const next = zoomPrice(span.lo, span.hi, anchor, factor);
        sidePriceLo = next.lo;
        sidePriceHi = next.hi;
        sideLinked = false;
        draw();
    }

    function relinkProfilePrice() {
        sideLinked = true;
        sidePriceLo = null;
        sidePriceHi = null;
        if (open) draw();
    }
    live.relinkProfilePrice = relinkProfilePrice;

    async function load(at) {
        if (loadFlight) {
            loadQueued = true;
            return;
        }
        loadFlight = true;
        try {
            await loadNow(at);
        } finally {
            loadFlight = false;
            if (loadQueued) {
                loadQueued = false;
                const next = pendingAt;
                if (next !== at) load(next);
            }
        }
    }

    async function loadNow(at) {
        const date = profileDate();
        if (!date || !live.ticker) {
            showNote('Chart date is not ready yet.');
            return;
        }
        const sec = at == null || !Number.isFinite(at) ? null : Math.floor(at);
        const liveFetch = sec == null;
        const atKey = date + '|' + (sec == null ? 'latest' : String(sec));
        if (!liveFetch && cached && cached.atKey === atKey && cached.data && cached.data.legacy) {
            draw();
            return;
        }
        const names = wanted();
        if (!names.length) {
            if (!(cached && cached.atKey === atKey)) {
                cached = { key: atKey + '|', atKey: atKey, data: { profiles: {}, legacy: false, missing: false } };
            }
            draw();
            return;
        }
        const key = atKey + '|' + names.join(',');
        if (!liveFetch && cached && cached.key === key) {
            draw();
            return;
        }
        const mine = ++requestId;
        let url = '/api/profile/' + encodeURIComponent(live.ticker) + '/' + encodeURIComponent(date);
        const query = [];
        if (sec != null) query.push('at=' + sec);
        query.push('endpoints=' + encodeURIComponent(names.join(',')));
        url += '?' + query.join('&');
        try {
            const res = await fetch(url);
            if (!res.ok) throw new Error('profile ' + res.status);
            const data = await res.json();
            if (mine !== requestId) return;
            cached = { key: key, atKey: atKey, data: data };
            draw();
        } catch (err) {
            if (mine !== requestId) return;
            showNote('Profile could not be loaded.');
        }
    }

    function schedule() {
        if (!profileShown()) return;
        clearTimeout(timer);
        if (playing || pendingAt != null) {
            load(pendingAt);
            return;
        }
        timer = setTimeout(() => load(pendingAt), 80);
    }

    function placeSidePane() {
        const container = document.getElementById('chart-container');
        const info = document.getElementById('crosshair-info-box');
        const split = document.getElementById('profile-split');
        const showSide = open && mode !== 'chart' && docked;
        if (!showSide || !container) {
            pane.style.position = '';
            pane.style.left = '';
            pane.style.width = '';
            pane.style.flex = '';
            pane.style.order = '';
            pane.style.top = '';
            pane.style.bottom = '';
            pane.style.zIndex = '';
            if (split) {
                split.hidden = true;
                split.style.order = '';
            }
            if (info) info.style.right = '';
            return;
        }
        const spec = sidePane();
        pane.style.position = '';
        pane.style.left = '';
        pane.style.top = '';
        pane.style.bottom = '';
        pane.style.zIndex = '';
        pane.style.flex = '0 0 ' + spec.width + '%';
        pane.style.width = spec.width + '%';
        pane.classList.toggle('side-left', spec.side === 'left');
        pane.classList.toggle('side-right', spec.side !== 'left');
        if (spec.side === 'left') pane.style.order = '-2';
        else pane.style.order = '2';
        if (split) {
            split.hidden = false;
            split.style.order = spec.side === 'left' ? '-1' : '1';
        }
        if (info) info.style.right = spec.side === 'left' ? '' : 'calc(' + spec.width + '% + 12px)';
    }

    let lastPlotSize = '';
    function plotSize() {
        const el = document.getElementById('chart-div');
        const container = document.getElementById('chart-container');
        if (!el) return null;
        let w = el.clientWidth;
        let h = el.clientHeight;
        if (container) {
            let used = 0;
            const kids = container.children;
            for (let i = 0; i < kids.length; i++) {
                const child = kids[i];
                if (child === el) continue;
                const style = getComputedStyle(child);
                if (style.display === 'none' || style.position === 'absolute' || style.position === 'fixed') continue;
                used += child.offsetWidth;
            }
            const room = container.clientWidth - used;
            if (room > w) w = room;
            if (!(h > 1)) h = container.clientHeight;
        }
        return { w: w, h: h };
    }
    function nudgeChart() {
        const chart = live.chart;
        const size = plotSize();
        if (!chart || !size || typeof chart.resize !== 'function') return;
        if (size.w < 2 || size.h < 2) return;
        const key = Math.round(size.w) + 'x' + Math.round(size.h);
        if (key === lastPlotSize) return;
        lastPlotSize = key;
        try { chart.applyOptions({ autoSize: false }); } catch (err) {}
        try { chart.resize(Math.round(size.w), Math.round(size.h), true); } catch (err) {}
    }

    function settleChart() {
        lastPlotSize = '';
        nudgeChart();
        placeMarker();
        requestAnimationFrame(() => {
            lastPlotSize = '';
            nudgeChart();
            placeMarker();
            requestAnimationFrame(() => {
                lastPlotSize = '';
                nudgeChart();
            });
        });
    }

    function metricsVisible() {
        return readChartPrefs(live.ticker, profileDate()).metricsVisible !== false;
    }

    function metricsOnLeft() {
        return readChartPrefs(live.ticker, profileDate()).metricsSide === 'left';
    }

    function setMetricsVisible(on) {
        const next = writeChartPrefs(live.ticker, profileDate(), { metricsVisible: !!on });
        if (live.currentChartPrefs) Object.assign(live.currentChartPrefs, chartProfileFields(next));
        noteProfilePrefs();
        paintMetrics();
        lastPlotSize = '';
        settleChart();
    }

    function setMetricsSide(side) {
        const next = writeChartPrefs(live.ticker, profileDate(), { metricsSide: side === 'left' ? 'left' : 'right' });
        if (live.currentChartPrefs) Object.assign(live.currentChartPrefs, chartProfileFields(next));
        noteProfilePrefs();
        paintMetrics();
        lastPlotSize = '';
        settleChart();
    }

    let previewSideWidth = null;
    function windowProfileSettings() {
        const prefs = readChartPrefs(live.ticker, profileDate());
        const stored = (live.globalChartSettings && live.globalChartSettings.ProfileSettings) || {};
        const sideWidth = Number(prefs.sideWidthPercent);
        const barWidth = Number(prefs.barWidthPercent);
        const center = Number(prefs.profileXPercent);
        return {
            side: prefs.profileSide === 'left' || prefs.profileSide === 'right' ? prefs.profileSide : (stored.profile_side === 'left' ? 'left' : 'right'),
            sideWidth: Number.isFinite(sideWidth) && sideWidth > 0 ? sideWidth : (Number(stored.side_width_percent) > 0 ? Number(stored.side_width_percent) : 28),
            barWidth: Number.isFinite(barWidth) && barWidth > 0 ? barWidth : (Number(stored.bar_width_percent) > 0 ? Number(stored.bar_width_percent) : 22),
            center: Number.isFinite(center) ? center : (Number.isFinite(Number(stored.profile_x_percent)) ? Number(stored.profile_x_percent) : 62),
            showPriors: typeof prefs.showPriors === 'boolean' ? prefs.showPriors : !(stored.show_priors === false || stored.show_priors === 'false' || stored.show_priors === 0),
        };
    }
    live.profileLayout = function () {
        const view = windowProfileSettings();
        return {
            profile_side: view.side,
            side_width_percent: view.sideWidth,
            bar_width_percent: view.barWidth,
            profile_x_percent: view.center,
        };
    };

    function sidePane() {
        const view = windowProfileSettings();
        let width = previewSideWidth != null ? previewSideWidth : view.sideWidth;
        width = clamp(width, 12, 50);
        return { width: width, side: view.side };
    }

    let metricsWasShown = null;
    function paintMetrics() {
        const column = document.getElementById('profile-metrics');
        if (!column) return;
        const shown = open && docked && metricsVisible();
        occupyMetrics(column, shown, metricsOnLeft());
        if (metricsWasShown !== shown) {
            metricsWasShown = shown;
            lastPlotSize = '';
            settleChart();
        }
        column.onMetricsClose = () => setMetricsVisible(false);
        if (!shown) return;
        const data = cached && cached.data && !cached.data.missing ? cached.data : null;
        renderMetrics(column, data, dte, layerOn, profileDate());
    }

    function profileShown() {
        return open && docked;
    }

    function applyChrome() {
        const show = profileShown();
        dteSelect.hidden = !show;
        layersBox.hidden = !show;
        modeSelect.hidden = !show;
        placeSidePane();
        pane.hidden = !show || mode === 'chart';
        pane.classList.toggle('is-closed', pane.hidden);
        lastPlotSize = '';
        settleChart();
        const replay = document.getElementById('profile-replay');
        if (replay) replay.hidden = !show;
        const detachBtn = document.getElementById('profile-detach-btn');
        const attachBtn = document.getElementById('profile-attach-btn');
        if (detachBtn) detachBtn.hidden = !show || mode === 'chart';
        if (attachBtn) attachBtn.hidden = true;
        const popout = document.getElementById('profile-popout');
        if (popout) popout.hidden = pane.hidden;
        if (!show) stopPlay();
        btn.classList.toggle('active', show);
        if (!show) showNote('');
        paintMetrics();
    }

    function clock() {
        const cache = live.chartDataCache;
        if (!cache || !Array.isArray(cache.timestamp)) return [];
        return cache.timestamp;
    }

    function formatAt(sec) {
        if (typeof live.formatChartTime === 'function') return live.formatChartTime(new Date(sec * 1000));
        return new Date(sec * 1000).toLocaleTimeString();
    }

    function placeMarker() {
        const marker = document.getElementById('profile-replay-marker');
        if (!marker) return;
        const times = clock();
        const sec = times[sliderIndex];
        if (!open || liveMode || followCrosshair || sec == null || !live.chart) {
            marker.hidden = true;
            return;
        }
        let x = null;
        try { x = live.chart.timeScale().timeToCoordinate(Math.floor(Number(sec))); } catch (err) {}
        const chartBox = document.getElementById('chart-div');
        if (x == null || !chartBox) {
            marker.hidden = true;
            return;
        }
        const width = chartBox.clientWidth || 0;
        let origin = 0;
        try {
            if ((live.globalChartSettings && live.globalChartSettings.PriceAxisLocation) === 'left' && typeof live.chart.priceScale === 'function') {
                const scale = live.chart.priceScale('left');
                if (scale && typeof scale.width === 'function') origin = scale.width() || 0;
            }
        } catch (err) {}
        const left = origin + x;
        if (left < 0 || left > width) {
            marker.hidden = true;
            return;
        }
        marker.hidden = false;
        marker.style.left = left + 'px';
    }

    function paintTransport() {
        const range = document.getElementById('profile-replay-range');
        const label = document.getElementById('profile-replay-time');
        const liveBtn = document.getElementById('profile-replay-live');
        const playBtn = document.getElementById('profile-replay-play');
        const times = clock();
        const last = Math.max(0, times.length - 1);
        if (sliderIndex > last) sliderIndex = last;
        if (range) {
            range.max = String(last);
            range.value = String(liveMode ? last : sliderIndex);
            range.disabled = times.length < 2;
        }
        if (label) {
            if (liveMode) label.textContent = 'Live';
            else if (times[sliderIndex] != null) label.textContent = formatAt(Number(times[sliderIndex]));
            else label.textContent = '—';
        }
        if (liveBtn) liveBtn.classList.toggle('active', liveMode);
        if (playBtn) playBtn.textContent = playing ? 'Pause' : 'Play';
        placeMarker();
    }

    function stopPlay() {
        playing = false;
        clearInterval(playTimer);
        playTimer = 0;
        paintTransport();
    }

    function goLive() {
        stopPlay();
        liveMode = true;
        pendingAt = null;
        const times = clock();
        sliderIndex = Math.max(0, times.length - 1);
        lastLiveTs = times.length ? Number(times[times.length - 1]) : null;
        paintTransport();
        schedule();
    }

    function showSample(index) {
        const times = clock();
        if (!times.length) return;
        const idx = Math.max(0, Math.min(times.length - 1, index));
        if (idx >= times.length - 1) {
            goLive();
            return;
        }
        stopPlay();
        liveMode = false;
        followCrosshair = false;
        const follow = document.getElementById('profile-follow-crosshair');
        if (follow) follow.checked = false;
        savePrefs();
        sliderIndex = Math.max(0, Math.min(times.length - 1, index));
        pendingAt = Number(times[sliderIndex]);
        paintTransport();
        schedule();
    }

    function playStep() {
        const times = clock();
        if (times.length < 2) {
            stopPlay();
            return;
        }
        if (sliderIndex + 1 >= times.length - 1) {
            goLive();
            return;
        }
        sliderIndex += 1;
        pendingAt = Number(times[sliderIndex]);
        paintTransport();
        schedule();
    }

    function startPlay() {
        const times = clock();
        if (times.length < 2) return;
        if (liveMode || sliderIndex >= times.length - 1) sliderIndex = 0;
        liveMode = false;
        followCrosshair = false;
        const follow = document.getElementById('profile-follow-crosshair');
        if (follow) follow.checked = false;
        savePrefs();
        playing = true;
        pendingAt = Number(times[sliderIndex]);
        paintTransport();
        schedule();
        clearInterval(playTimer);
        const speed = Number((document.getElementById('profile-replay-speed') || {}).value) || 1;
        playTimer = setInterval(playStep, Math.max(16, Math.round(1000 / speed)));
    }

    const replayLive = document.getElementById('profile-replay-live');
    const replayPlay = document.getElementById('profile-replay-play');
    const replayBack = document.getElementById('profile-replay-back');
    const replayForward = document.getElementById('profile-replay-forward');
    const replayRange = document.getElementById('profile-replay-range');
    const replaySpeed = document.getElementById('profile-replay-speed');
    const followInput = document.getElementById('profile-follow-crosshair');
    if (followInput) followInput.checked = followCrosshair;
    if (replayLive) replayLive.addEventListener('click', goLive);
    if (replayPlay) replayPlay.addEventListener('click', () => { if (playing) stopPlay(); else startPlay(); });
    if (replayBack) replayBack.addEventListener('click', () => showSample(sliderIndex - 1));
    if (replayForward) replayForward.addEventListener('click', () => showSample(sliderIndex + 1));
    if (replayRange) replayRange.addEventListener('input', () => showSample(Number(replayRange.value)));
    if (replaySpeed) {
        replaySpeed.addEventListener('change', () => {
            if (!playing) return;
            clearInterval(playTimer);
            const speed = Number(replaySpeed.value) || 1;
            playTimer = setInterval(playStep, Math.max(16, Math.round(1000 / speed)));
        });
    }
    if (followInput) {
        followInput.addEventListener('change', () => {
            followCrosshair = followInput.checked;
            savePrefs();
            if (followCrosshair) {
                stopPlay();
                liveMode = false;
            }
            paintTransport();
        });
    }
    try {
        if (live.chart) live.chart.timeScale().subscribeVisibleLogicalRangeChange(() => placeMarker());
    } catch (err) {}

    live.syncProfile = function (unixSeconds) {
        if (!docked || !followCrosshair || playing) return;
        if (typeof unixSeconds !== 'number' || !Number.isFinite(unixSeconds)) return;
        liveMode = false;
        pendingAt = unixSeconds;
        const times = clock();
        let idx = 0;
        for (let i = 0; i < times.length; i++) {
            if (Number(times[i]) <= unixSeconds) idx = i;
            else break;
        }
        sliderIndex = idx;
        paintTransport();
        schedule();
    };
    live.noteChartClock = function () {
        const times = clock();
        const newest = times.length ? Number(times[times.length - 1]) : null;
        if (liveMode && profileShown() && newest != null && newest !== lastLiveTs) {
            lastLiveTs = newest;
            sliderIndex = times.length - 1;
            pendingAt = null;
            paintTransport();
            schedule();
            return;
        }
        paintTransport();
    };
    live.redrawProfile = function () {
        if (profileShown()) draw();
    };
    live.profileShown = profileShown;

    function rememberDock() {
        const next = writeChartPrefs(live.ticker, profileDate(), { profileDocked: docked });
        if (live.currentChartPrefs) Object.assign(live.currentChartPrefs, chartProfileFields(next));
        noteProfilePrefs();
    }

    function dockProfile() {
        docked = true;
        mode = 'side';
        modeSelect.value = 'side';
        preferredOpen = true;
        open = true;
        try { localStorage.removeItem('profile-attach-' + (live.ticker || '')); } catch (err) {}
        savePrefs();
        rememberDock();
        applyChrome();
        schedule();
    }

    async function profileApi() {
        return import('/bindings/market-terminal/app.js');
    }

    async function attachProfile() {
        dockProfile();
        try {
            const mod = await profileApi();
            await mod.CloseProfileWindow(live.ticker);
        } catch (err) {}
    }

    async function detachProfile() {
        docked = false;
        savePrefs();
        rememberDock();
        applyChrome();
        draw();
        try {
            const mod = await profileApi();
            await mod.OpenProfileWindow(live.ticker, profileDate());
        } catch (err) {
            showNote('Could not open the profile window.');
        }
    }

    function saveProfileLayout(partial) {
        const patch = {};
        if (partial.profile_side === 'left' || partial.profile_side === 'right') patch.profileSide = partial.profile_side;
        if (partial.side_width_percent != null) patch.sideWidthPercent = partial.side_width_percent;
        if (partial.bar_width_percent != null) patch.barWidthPercent = partial.bar_width_percent;
        if (partial.profile_x_percent != null) patch.profileXPercent = partial.profile_x_percent;
        writeChartPrefs(live.ticker, profileDate(), patch);
        if (live.currentChartPrefs) Object.assign(live.currentChartPrefs, patch);
        previewSideWidth = null;
        placeSidePane();
        if (open) draw();
    }

    const splitHandle = document.getElementById('profile-split');
    if (splitHandle) {
        let dragPercent = null;
        let dragFrame = 0;
        splitHandle.addEventListener('pointerdown', function (event) {
            if (splitHandle.hidden || event.button !== 0) return;
            event.preventDefault();
            event.stopPropagation();
            const spec = sidePane();
            dragPercent = spec.width;
            splitHandle.classList.add('is-dragging');
            document.body.style.cursor = 'col-resize';
            try { splitHandle.setPointerCapture(event.pointerId); } catch (err) {}
            function widthAt(clientX) {
                const container = document.getElementById('chart-container');
                if (!container) return dragPercent;
                const rect = container.getBoundingClientRect();
                const metrics = document.getElementById('profile-metrics');
                const metricsShown = metrics && !metrics.hidden;
                const metricsLeft = metricsShown && metrics.classList.contains('metrics-left');
                const metricsW = metricsShown && !metricsLeft ? metrics.getBoundingClientRect().width : 0;
                const metricsLeftW = metricsLeft ? metrics.getBoundingClientRect().width : 0;
                const splitW = splitHandle.getBoundingClientRect().width || 7;
                let panePx = spec.side === 'left'
                    ? clientX - rect.left - metricsLeftW - splitW / 2
                    : rect.right - metricsW - clientX - splitW / 2;
                if (!(rect.width > 0)) return dragPercent;
                return clamp(Math.round((panePx / rect.width) * 1000) / 10, 12, 50);
            }
            function paintDrag() {
                dragFrame = 0;
                previewSideWidth = dragPercent;
                placeSidePane();
                nudgeChart();
                try { primitive.requestUpdate(); } catch (err) {}
                placeMarker();
            }
            function onMove(ev) {
                dragPercent = widthAt(ev.clientX);
                if (!dragFrame) dragFrame = requestAnimationFrame(paintDrag);
            }
            function onUp(ev) {
                splitHandle.removeEventListener('pointermove', onMove);
                splitHandle.removeEventListener('pointerup', onUp);
                splitHandle.removeEventListener('pointercancel', onUp);
                splitHandle.classList.remove('is-dragging');
                document.body.style.cursor = '';
                if (dragFrame) {
                    cancelAnimationFrame(dragFrame);
                    dragFrame = 0;
                }
                const next = ev && ev.type !== 'pointercancel' && Number.isFinite(ev.clientX) ? widthAt(ev.clientX) : dragPercent;
                dragPercent = null;
                if (next != null) saveProfileLayout({ side_width_percent: next });
            }
            splitHandle.addEventListener('pointermove', onMove);
            splitHandle.addEventListener('pointerup', onUp);
            splitHandle.addEventListener('pointercancel', onUp);
        });
    }

    const alertPop = document.getElementById('level-alert-pop');
    let alertPrefs = chartProfileFields(readChartPrefs(live.ticker, live.dateFromURL || '')).alerts;
    let alertMark = null;

    function alertOn(key) {
        return !!alertPrefs[key];
    }

    function setAlert(key, on) {
        if (!Object.prototype.hasOwnProperty.call(alertPrefs, key)) return;
        alertPrefs[key] = !!on;
        const next = writeChartPrefs(live.ticker, profileDate(), { alerts: alertPrefs });
        if (live.currentChartPrefs) Object.assign(live.currentChartPrefs, chartProfileFields(next));
        noteProfilePrefs();
    }
    live.alertOn = alertOn;
    live.setAlert = setAlert;
    function profileLevelOn(id) {
        return levelPrefs(readChartPrefs(live.ticker, profileDate()))[id] !== false;
    }
    function setProfileLevel(id, on) {
        if (!PROFILE_LEVELS.some((level) => level.id === id)) return;
        const bag = levelPrefs(readChartPrefs(live.ticker, profileDate()));
        bag[id] = !!on;
        const next = writeChartPrefs(live.ticker, profileDate(), { profileLevels: bag });
        if (live.currentChartPrefs) Object.assign(live.currentChartPrefs, chartProfileFields(next));
        noteProfilePrefs();
        if (open) draw();
    }
    live.profileLevelOn = profileLevelOn;
    live.setProfileLevel = setProfileLevel;
    function setLevelLabel(percent) {
        const next = writeChartPrefs(live.ticker, profileDate(), { levelLabelX: levelLabelPercent({ levelLabelX: percent }) });
        if (live.currentChartPrefs) Object.assign(live.currentChartPrefs, chartProfileFields(next));
        noteProfilePrefs();
        if (open) draw();
    }
    live.levelLabelX = function () {
        return levelLabelPercent(readChartPrefs(live.ticker, profileDate()));
    };
    live.setLevelLabel = setLevelLabel;

    function chartLevel(cache, key, index) {
        const series = cache && cache[key];
        if (!Array.isArray(series) || index < 0 || index >= series.length) return null;
        return finitePrice(series[index]);
    }

    function volumeLevels(cache, index) {
        const call = chartLevel(cache, 'major_pos_vol', index);
        const put = chartLevel(cache, 'major_neg_vol', index);
        const data = cached && cached.data;
        const profiles = (data && data.profiles) || {};
        const classic = profiles[endpointFor(LAYER_SPECS[0], dte)];
        const stateSpec = LAYER_SPECS.find((item) => item.id === 'state');
        const state = stateSpec ? profiles[endpointFor(stateSpec, dte)] : null;
        return {
            call: call != null ? call : (priceField(classic, 'major_pos_vol') || priceField(state, 'major_pos_vol')),
            put: put != null ? put : (priceField(classic, 'major_neg_vol') || priceField(state, 'major_neg_vol')),
        };
    }

    function greekLevels() {
        const spec = LAYER_SPECS.find((item) => item.greek && layerOn(item.id));
        if (!spec) return null;
        const data = cached && cached.data;
        const payload = data && data.profiles ? data.profiles[endpointFor(spec, dte)] : null;
        if (!payload) return null;
        const longKey = spec.id === 'gamma' ? 'major_long_gamma' : 'major_positive';
        const shortKey = spec.id === 'gamma' ? 'major_short_gamma' : 'major_negative';
        return {
            long: priceField(payload, longKey) || priceField(payload, 'major_long_gamma'),
            short: priceField(payload, shortKey) || priceField(payload, 'major_short_gamma'),
        };
    }

    function spotCrossed(prev, next, level) {
        if (prev == null || next == null || level == null) return false;
        if (prev === level) return false;
        if (next === level) return true;
        return (prev < level && next > level) || (prev > level && next < level);
    }

    function pushLevelNotice(name, price) {
        if (!alertPop) return;
        alertPop.hidden = false;
        const item = document.createElement('button');
        item.type = 'button';
        item.textContent = name + '  ' + formatLevel(price);
        item.addEventListener('click', () => {
            item.remove();
            if (!alertPop.children.length) alertPop.hidden = true;
        });
        alertPop.appendChild(item);
        setTimeout(() => {
            if (!item.isConnected) return;
            item.remove();
            if (!alertPop.children.length) alertPop.hidden = true;
        }, 8000);
    }

    function noteLevelTouches() {
        const cache = live.chartDataCache;
        if (!cache || !Array.isArray(cache.timestamp) || !Array.isArray(cache.spot)) return;
        const times = cache.timestamp;
        const id = (cache.ticker || live.ticker || '') + '|' + (cache.dateStr || '');
        let last = -1;
        for (let i = times.length - 1; i >= 0; i--) {
            if (finitePrice(cache.spot[i]) != null) {
                last = i;
                break;
            }
        }
        if (last < 0) return;
        const lastTime = Number(times[last]);
        if (!alertMark || alertMark.id !== id) {
            alertMark = { id: id, time: lastTime };
            return;
        }
        let prev = null;
        let prevIndex = -1;
        for (let i = 0; i < times.length; i++) {
            const spot = finitePrice(cache.spot[i]);
            if (spot == null) continue;
            const time = Number(times[i]);
            if (!(time > alertMark.time)) {
                prev = spot;
                prevIndex = i;
                continue;
            }
            if (prev != null && prev !== spot) {
                const volume = volumeLevels(cache, i);
                const greek = greekLevels();
                if (alertOn('call') && spotCrossed(prev, spot, volume.call)) pushLevelNotice('Major call', volume.call);
                if (alertOn('put') && spotCrossed(prev, spot, volume.put)) pushLevelNotice('Major put', volume.put);
                if (greek && alertOn('long') && spotCrossed(prev, spot, greek.long)) pushLevelNotice('Major long', greek.long);
                if (greek && alertOn('short') && spotCrossed(prev, spot, greek.short)) pushLevelNotice('Major short', greek.short);
            }
            prev = spot;
            prevIndex = i;
        }
        if (prevIndex >= 0) alertMark = { id: id, time: Number(times[prevIndex]) };
    }

    live.noteLevelTouches = noteLevelTouches;
    live.metricsVisible = metricsVisible;
    live.setMetricsVisible = setMetricsVisible;
    live.metricsSide = function () { return metricsOnLeft() ? 'left' : 'right'; };
    live.setMetricsSide = setMetricsSide;
    live.profileAxis = axisSide;
    live.setProfileAxis = function (value) {
        const axis = value === 'off' || value === 'left' || value === 'right' ? value : '';
        const next = writeChartPrefs(live.ticker, profileDate(), { profileAxis: axis });
        if (live.currentChartPrefs) Object.assign(live.currentChartPrefs, chartProfileFields(next));
        noteProfilePrefs();
        if (open) draw();
    };
    window.addEventListener('storage', (event) => {
        if (!event || event.key !== 'chartPrefs_' + (live.ticker || '') + '_' + profileDate()) return;
        applyChartProfilePrefs();
    });
    live.priceTickMode = priceTickMode;
    live.setPriceTickMode = function (mode) {
        setPriceTickMode(mode);
        writeChartPrefs(live.ticker, profileDate(), { priceTickMode: priceTickMode() });
        if (open) draw();
    };
    live.profileDocked = function () { return docked; };
    live.detachProfile = detachProfile;
    live.attachProfile = attachProfile;
    live.saveProfileLayout = saveProfileLayout;
    let drawOver = null;
    function profileOver() {
        if (drawOver === true || drawOver === false) return drawOver;
        const saved = readChartPrefs(live.ticker, profileDate()).profileDrawOrder;
        if (saved === 'over' || saved === 'under') return saved === 'over';
        try { return localStorage.getItem('profile-draw-order') === 'over'; }
        catch (err) { return false; }
    }

    live.ladderAlign = function () { return ladderAlign; };
    live.profileOver = profileOver;
    live.setProfileOver = function (over) {
        drawOver = !!over;
        writeChartPrefs(live.ticker, profileDate(), { profileDrawOrder: drawOver ? 'over' : 'under' });
        if (primitive._series && typeof primitive._series.detachPrimitive === 'function') {
            try { primitive._series.detachPrimitive(primitive); } catch (err) {}
        }
        primitive._series = null;
        if (profileShown()) draw();
    };
    live.setLadderAlign = function (value) {
        ladderAlign = value === 'left' || value === 'right' ? value : 'center';
        const next = writeChartPrefs(live.ticker, profileDate(), { ladderAlign: ladderAlign });
        if (live.currentChartPrefs) Object.assign(live.currentChartPrefs, chartProfileFields(next));
        noteProfilePrefs();
        if (profileShown()) draw();
    };
    let appliedProfileKey = '';
    let appliedAlertKey = '';
    let appliedLevelKey = '';
    let appliedViewKey = '';
    function viewKeyNow() {
        seedPriceTickMode(live.ticker, profileDate());
        const order = readChartPrefs(live.ticker, profileDate()).profileDrawOrder;
        if (order === 'over' || order === 'under') drawOver = order === 'over';
        const view = windowProfileSettings();
        return [view.side, view.sideWidth, view.barWidth, view.center, view.showPriors ? '1' : '0', priceTickMode(), drawOver ? '1' : '0'].join('|');
    }
    function levelKeyOf(stored) {
        return PROFILE_LEVELS.map((level) => stored.profileLevels[level.id] ? '1' : '0').join('') + '|' + stored.levelLabelX;
    }
    function syncDteFromPrefs() {
        const next = readProfileDte(live.ticker, profileDate());
        if (next === dte) return false;
        dte = next;
        dteSelect.value = dte;
        return true;
    }

    function noteProfilePrefs() {
        const stored = chartProfileFields(readChartPrefs(live.ticker, profileDate()));
        appliedProfileKey = [stored.ladderAlign, stored.metricsVisible ? '1' : '0', stored.metricsSide, stored.profileAxis].join('|');
        appliedAlertKey = [stored.alerts.call, stored.alerts.put, stored.alerts.long, stored.alerts.short].map((on) => on ? '1' : '0').join('');
        appliedLevelKey = levelKeyOf(stored);
    }
    function applyChartProfilePrefs() {
        const layersChanged = syncLayersFromPrefs();
        const dteChanged = syncDteFromPrefs();
        const nextView = viewKeyNow();
        const viewChanged = nextView !== appliedViewKey;
        appliedViewKey = nextView;
        const stored = chartProfileFields(readChartPrefs(live.ticker, profileDate()));
        const key = [stored.ladderAlign, stored.metricsVisible ? '1' : '0', stored.metricsSide, stored.profileAxis].join('|');
        const alertKey = [stored.alerts.call, stored.alerts.put, stored.alerts.long, stored.alerts.short].map((on) => on ? '1' : '0').join('');
        const nextLevel = levelKeyOf(stored);
        const layoutChanged = key !== appliedProfileKey;
        const levelsChanged = nextLevel !== appliedLevelKey;
        const rawDock = readChartPrefs(live.ticker, profileDate()).profileDocked;
        const dockChanged = (rawDock === true || rawDock === false) && rawDock !== docked;
        if (!layoutChanged && alertKey === appliedAlertKey && !levelsChanged && !dockChanged) {
            if ((layersChanged || dteChanged) && profileShown()) schedule();
            else if (viewChanged && profileShown()) {
                placeSidePane();
                draw();
            }
            return;
        }
        appliedProfileKey = key;
        appliedAlertKey = alertKey;
        appliedLevelKey = nextLevel;
        alertPrefs = stored.alerts;
        if (dockChanged) docked = rawDock;
        if (!layoutChanged && !dockChanged) {
            if (levelsChanged && profileShown()) draw();
            return;
        }
        if (dockChanged) {
            applyChrome();
            if (profileShown()) schedule();
            else draw();
            return;
        }
        if (stored.ladderAlign) ladderAlign = stored.ladderAlign;
        paintMetrics();
        lastPlotSize = '';
        settleChart();
        if (profileShown()) draw();
    }
    live.applyChartProfilePrefs = applyChartProfilePrefs;
    // Same-window edits already call schedule() and setLadderAlign(). The storage
    // event only reaches other windows, so this must not write localStorage again.
    live.applyStoredProfileView = function () {};

    try {
        const dockChannel = new BroadcastChannel('market-terminal-profile');
        dockChannel.onmessage = function (event) {
            const msg = event.data;
            if (!msg || msg.ticker !== live.ticker) return;
            if (msg.type === 'attach') dockProfile();
        };
    } catch (err) {}

    btn.addEventListener('click', () => {
        if (!docked) {
            attachProfile();
            return;
        }
        preferredOpen = !preferredOpen;
        open = preferredOpen && profileOn();
        savePrefs();
        applyChrome();
        if (profileShown()) schedule();
        else draw();
    });
    dteSelect.addEventListener('change', () => {
        dte = dteSelect.value === 'one' || dteSelect.value === 'full' ? dteSelect.value : 'zero';
        writeChartPrefs(live.ticker, profileDate(), { profileDte: dte });
        schedule();
    });
    layersBox.addEventListener('change', (ev) => {
        const input = ev.target;
        if (!input || !input.dataset || !input.dataset.layer) return;
        layersOn[input.dataset.layer] = !!input.checked;
        saveLayerPrefs();
        schedule();
    });
    modeSelect.addEventListener('change', () => {
        mode = modeSelect.value === 'chart' ? 'chart' : 'side';
        if (mode === 'side' && !docked) {
            attachProfile();
            return;
        }
        savePrefs();
        applyChrome();
        draw();
    });
    const detachBtn = document.getElementById('profile-detach-btn');
    const attachBtn = document.getElementById('profile-attach-btn');
    if (detachBtn) detachBtn.addEventListener('click', () => detachProfile());
    const popoutBtn = document.getElementById('profile-popout');
    if (popoutBtn) popoutBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        detachProfile();
    });
    if (attachBtn) attachBtn.addEventListener('click', () => attachProfile());
    const chartFrame = document.getElementById('chart-container');
    if (chartFrame && typeof ResizeObserver !== 'undefined') {
        new ResizeObserver(() => {
            placeSidePane();
            requestAnimationFrame(() => {
                settleChart();
                placeMarker();
                if (!profileShown()) return;
                if (mode === 'chart') primitive.requestUpdate();
                else draw();
            });
        }).observe(chartFrame);
    }
    const chartDiv = document.getElementById('chart-div');
    const tip = document.getElementById('profile-hover');
    function hideTip() {
        if (tip) tip.hidden = true;
    }
    function showTip(ev, hit) {
        if (!tip) return;
        tip.textContent = formatHit(hit);
        tip.hidden = false;
        const pad = 8;
        let left = ev.clientX + 14;
        let top = ev.clientY + 16;
        const w = tip.offsetWidth;
        const h = tip.offsetHeight;
        if (left + w > window.innerWidth - pad) left = Math.max(pad, ev.clientX - w - 12);
        if (top + h > window.innerHeight - pad) top = Math.max(pad, ev.clientY - h - 12);
        tip.style.left = left + 'px';
        tip.style.top = top + 'px';
    }
    function onProfileMove(ev, space, origin) {
        if (!tip || hoverState.space !== space || !origin) {
            hideTip();
            return;
        }
        const rect = origin.getBoundingClientRect();
        const hit = hitAt(ev.clientX - rect.left, ev.clientY - rect.top, hoverState.hits);
        if (!hit) {
            hideTip();
            return;
        }
        showTip(ev, hit);
    }
    if (chartDiv) {
        chartDiv.addEventListener('mousemove', (ev) => onProfileMove(ev, 'chart', chartDiv));
        chartDiv.addEventListener('mouseleave', hideTip);
    }
    canvas.addEventListener('mousemove', (ev) => onProfileMove(ev, 'side', canvas));
    canvas.addEventListener('mouseleave', hideTip);
    let sideDrag = null;
    canvas.addEventListener('wheel', (ev) => {
        if (!open || mode === 'chart' || !docked) return;
        ev.preventDefault();
        const y = ev.clientY - canvas.getBoundingClientRect().top;
        zoomSidePrice(priceAtPointer(y), ev.deltaY > 0 ? 1.12 : 1 / 1.12);
    }, { passive: false });
    canvas.addEventListener('pointerdown', (ev) => {
        if (!open || mode === 'chart' || !docked || ev.button !== 0) return;
        const span = profileSpan();
        if (!span) return;
        sideDrag = { y: ev.clientY, lo: span.lo, hi: span.hi };
        try { canvas.setPointerCapture(ev.pointerId); } catch (err) {}
    });
    canvas.addEventListener('pointermove', (ev) => {
        if (!sideDrag) return;
        if (Math.abs(ev.clientY - sideDrag.y) < 3) return;
        const margins = priceMargins();
        const paneH = Math.max(1, plotHeight());
        const spanH = Math.max(1, paneH * (1 - margins.top - margins.bottom));
        const next = panPrice(sideDrag.lo, sideDrag.hi, ev.clientY - sideDrag.y, spanH);
        sidePriceLo = next.lo;
        sidePriceHi = next.hi;
        sideLinked = false;
        draw();
    });
    function endSideDrag() { sideDrag = null; }
    canvas.addEventListener('pointerup', endSideDrag);
    canvas.addEventListener('pointercancel', endSideDrag);
    if (typeof ResizeObserver !== 'undefined') {
        const observer = new ResizeObserver(() => {
            if (open) draw();
        });
        observer.observe(pane);
        if (chartDiv) observer.observe(chartDiv);
    }
}

function loadPrefs() {
    let saved = {};
    try {
        saved = JSON.parse(localStorage.getItem(PREF_KEY) || '{}');
    } catch (err) {}
    const layers = {
        classic: true,
        state: false,
        gamma: false,
        delta: false,
        vanna: false,
        charm: false,
    };
    if (saved.layers && typeof saved.layers === 'object') {
        for (const spec of LAYER_SPECS) {
            if (typeof saved.layers[spec.id] === 'boolean') layers[spec.id] = saved.layers[spec.id];
        }
    }
    const dte = saved.dte === 'one' || saved.dte === 'full' ? saved.dte : 'zero';
    return {
        open: saved.open === true,
        mode: saved.mode === 'chart' ? 'chart' : 'side',
        dte: dte,
        layers: layers,
        follow: saved.follow === true,
        docked: saved.docked !== false,
        align: saved.align === 'left' || saved.align === 'right' ? saved.align : 'center',
    };
}

function endpointFor(spec, dte) {
    if (spec.greek && dte === 'full') return spec.id + '_zero';
    return spec.id + '_' + dte;
}

function buildLayers(data, dte, layerOn) {
    if (data.legacy) {
        const layers = [];
        if (layerOn('classic')) {
            const ladder = savedStrikeRows(data.profiles);
            const spec = LAYER_SPECS[0];
            if (ladder.rows.length) layers.push(layerFrom(spec, ladder));
        }
        const greekSpec = LAYER_SPECS.find((spec) => spec.greek && layerOn(spec.id));
        if (greekSpec) {
            const rows = greekOf(data.profiles);
            if (rows.length) layers.push(layerFrom(greekSpec, Object.assign({ rows: rows }, sideWords(greekSpec))));
        }
        return layers;
    }
    const layers = [];
    const profiles = data.profiles || {};
    for (const spec of LAYER_SPECS) {
        if (!layerOn(spec.id)) continue;
        const name = endpointFor(spec, dte);
        if (!name || !profiles[name]) continue;
        const ladder = spec.greek
            ? Object.assign({ rows: greekOf(profiles[name]) }, sideWords(spec))
            : ladderOf(spec, profiles[name]);
        if (!ladder.rows.length) continue;
        layers.push(layerFrom(spec, ladder, spec.greek && dte === 'full'));
    }
    return layers;
}

function layerFrom(spec, ladder, fallback) {
    const rows = ladder.rows || [];
    return {
        id: spec.id,
        label: spec.label,
        measure: ladder.measure,
        posName: ladder.posName,
        negName: ladder.negName,
        majorPos: ladder.majorPos,
        majorNeg: ladder.majorNeg,
        pos: ladder.pos || spec.pos,
        neg: ladder.neg || spec.neg,
        dots: rows.some((row) => row.priors && row.priors.some((value) => Number.isFinite(value))),
        fallback: !!fallback,
        rows: rows,
    };
}

function sideWords(spec, by) {
    if (spec.greek) {
        if (spec.id === 'gamma') {
            return { measure: 'long / short gamma', posName: 'long gamma', negName: 'short gamma' };
        }
        const name = spec.label.toLowerCase();
        return {
            measure: 'positive / negative ' + name,
            posName: 'positive ' + name,
            negName: 'negative ' + name,
        };
    }
    const book = by === 'oi' ? 'OI' : 'volume';
    const majorBook = by === 'oi' ? ' by OI' : '';
    return {
        measure: 'call / put GEX by ' + book,
        posName: 'call GEX by ' + book,
        negName: 'put GEX by ' + book,
        majorPos: 'major call gamma' + majorBook + ' · call wall',
        majorNeg: 'major put gamma' + majorBook + ' · put wall',
    };
}

function clamp(value, min, max) {
    if (!Number.isFinite(value)) return min;
    return Math.min(max, Math.max(min, value));
}

function ladderOf(spec, payload) {
    const volume = strikeRows(payload, 1);
    let rows = volume;
    let by = 'volume';
    if (!volume.some((row) => row.gex !== 0)) {
        const oi = strikeRows(payload, 2);
        if (oi.some((row) => row.gex !== 0)) {
            rows = oi;
            by = 'oi';
        }
    }
    const words = sideWords(spec, by);
    return {
        rows: rows,
        measure: words.measure,
        posName: words.posName,
        negName: words.negName,
        majorPos: words.majorPos,
        majorNeg: words.majorNeg,
        pos: by === 'oi' && spec.oiPos ? spec.oiPos : spec.pos,
        neg: by === 'oi' && spec.oiNeg ? spec.oiNeg : spec.neg,
    };
}

function savedStrikeRows(payload) {
    return ladderOf(LAYER_SPECS[0], payload);
}

function strikeRows(payload, valueIndex) {
    const raw = payload && payload.strikes;
    if (!Array.isArray(raw)) return [];
    const rows = [];
    for (const row of raw) {
        if (!Array.isArray(row) || row.length <= valueIndex) continue;
        const strike = Number(row[0]);
        const gex = Number(row[valueIndex]);
        if (!Number.isFinite(strike) || !Number.isFinite(gex)) continue;
        rows.push({ strike: strike, gex: gex, priors: priorsOf(row[3]) });
    }
    rows.sort((a, b) => a.strike - b.strike);
    return rows;
}

function greekOf(payload) {
    const raw = payload && payload.mini_contracts;
    if (!Array.isArray(raw)) return [];
    const rows = [];
    for (const row of raw) {
        if (!Array.isArray(row) || row.length < 4) continue;
        const strike = Number(row[0]);
        const gex = Number(row[3]);
        if (!Number.isFinite(strike) || !Number.isFinite(gex)) continue;
        rows.push({ strike: strike, gex: gex, priors: priorsOf(row[4]) });
    }
    rows.sort((a, b) => a.strike - b.strike);
    return rows;
}

function priorsOf(raw) {
    if (!Array.isArray(raw) || !raw.length) return null;
    const nums = [];
    const count = Math.min(5, raw.length);
    let any = false;
    for (let i = 0; i < count; i++) {
        const value = Number(raw[i]);
        if (Number.isFinite(value)) {
            nums.push(value);
            any = true;
        } else {
            nums.push(null);
        }
    }
    return any ? nums : null;
}

function numberOr(primary, fallback) {
    const a = Number(primary);
    if (Number.isFinite(a)) return a;
    const b = Number(fallback);
    return Number.isFinite(b) ? b : null;
}

function formatNum(n) {
    return n.toLocaleString('en-US', { maximumFractionDigits: 2 });
}

function formatHit(hit) {
    const value = formatNum(hit.value);
    if (hit.detail) return hit.label + ' · ' + hit.detail + ' · ' + value;
    const what = hit.measure || hit.label;
    const strike = formatStrike(hit.strike);
    if (hit.minutes) return strike + ' · ' + what + ' · ' + hit.minutes + ' ago · ' + value;
    return strike + ' · ' + what + ' · ' + value;
}

function barExtremes(layer) {
    let maxPos = null;
    let minNeg = null;
    const rows = layer && layer.rows ? layer.rows : [];
    for (const row of rows) {
        if (row.gex > 0 && (maxPos == null || row.gex > maxPos)) maxPos = row.gex;
        if (row.gex < 0 && (minNeg == null || row.gex < minNeg)) minNeg = row.gex;
    }
    return { maxPos: maxPos, minNeg: minNeg };
}

function nameForBar(layer, gex, extremes) {
    if (extremes && gex > 0 && layer.majorPos && gex === extremes.maxPos) return layer.majorPos;
    if (extremes && gex < 0 && layer.majorNeg && gex === extremes.minNeg) return layer.majorNeg;
    if (gex >= 0 && layer.posName) return layer.posName;
    if (gex < 0 && layer.negName) return layer.negName;
    return layer.measure || layer.label;
}

function formatStrike(n) {
    if (!Number.isFinite(n)) return '';
    const rounded = Math.round(n);
    if (Math.abs(n - rounded) < 1e-6) return rounded.toLocaleString('en-US');
    return n.toLocaleString('en-US', { maximumFractionDigits: 2 });
}

function rowExtent(rows, includePriors) {
    let maxAbs = 0;
    for (const row of rows) {
        maxAbs = Math.max(maxAbs, Math.abs(row.gex) || 0);
        if (!includePriors || !row.priors) continue;
        for (let i = 0; i < row.priors.length; i++) {
            const prior = row.priors[i];
            if (Number.isFinite(prior)) maxAbs = Math.max(maxAbs, Math.abs(prior));
        }
    }
    return maxAbs;
}

// Room past the longest bar or lookback dot so the default scale ends inside the plot.
function ladderEdge(showDots) {
    return showDots ? DOT_RADIUS[0] + 5 : 8;
}

function placedRows(rows, yOf, top, bottom, includePriors) {
    // Scale to the whole ladder. A visible-only scale makes every bar jump
    // when a zoom brings one large strike into the pane.
    let maxAbs = rowExtent(rows, includePriors);
    const placed = [];
    for (const row of rows) {
        const y = yOf(row.strike);
        if (y == null || y < top || y > bottom) continue;
        placed.push({ y: y, gex: row.gex, strike: row.strike, priors: row.priors });
    }
    placed.sort((a, b) => a.y - b.y);
    const gaps = [];
    for (let i = 1; i < placed.length; i++) gaps.push(placed[i].y - placed[i - 1].y);
    gaps.sort((a, b) => a - b);
    const gap = gaps.length ? gaps[Math.floor(gaps.length / 2)] : 8;
    return { placed: placed, maxAbs: maxAbs, gap: gap, barH: Math.max(2, Math.min(9, gap * 0.55)) };
}

function xOf(center, half, maxAbs, value, dir) {
    if (!maxAbs || !Number.isFinite(value)) return center;
    const width = Math.min(half, (Math.abs(value) / maxAbs) * half);
    if (dir === 'left') return center + width;
    if (dir === 'right') return center - width;
    return value >= 0 ? center + width : center - width;
}

function hitAt(x, y, hits) {
    let best = null;
    let bestD = Infinity;
    for (const hit of hits) {
        let d = Infinity;
        if (hit.kind === 'bar') {
            if (x < hit.x0 - 3 || x > hit.x1 + 3) continue;
            if (Math.abs(y - hit.y) > hit.pad + 3) continue;
            d = Math.abs(y - hit.y);
        } else {
            d = Math.hypot(x - hit.x, y - hit.y);
            if (d > hit.pad) continue;
        }
        if (d < bestD) {
            best = hit;
            bestD = d;
        }
    }
    return best;
}

function paintLayers(ctx, layers, yOf, center, half, top, bottom, showDots, labelStrikes, fitPriors, wingDots, dotScale, dir) {
    ctx.strokeStyle = 'rgba(244,241,234,0.55)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(center, top);
    ctx.lineTo(center, bottom);
    ctx.stroke();
    const drawn = [];
    for (const layer of layers) {
        const placed = placedRows(layer.rows, yOf, top, bottom, fitPriors && showDots && layer.dots);
        if (placed.placed.length) drawn.push({ layer: layer, placed: placed });
    }
    const hits = [];
    if (!drawn.length) return hits;
    const bunch = bunchLayout(drawn);
    for (let i = 0; i < drawn.length; i++) {
        const item = drawn[i];
        const offset = bunch.origin + i * bunch.slot;
        drawBars(ctx, item.placed, center, half, offset, bunch.barH, item.layer, hits, wingDots, dir);
    }
    if (showDots) {
        for (let i = 0; i < drawn.length; i++) {
            const item = drawn[i];
            if (!item.layer.dots) continue;
            drawDots(ctx, item.placed, center, half, bunch.origin + i * bunch.slot, item.layer, hits, dotScale, dir);
        }
    }
    if (labelStrikes) {
        const labeled = drawn.find((item) => item.layer.id === 'classic') || drawn[0];
        labelStrikesOn(ctx, labeled.placed);
    }
    return hits;
}

function bunchLayout(drawn) {
    let gap = drawn[0].placed.gap;
    for (const item of drawn) gap = Math.min(gap, item.placed.gap);
    const count = drawn.length;
    if (count === 1) {
        return { origin: 0, slot: drawn[0].placed.barH, barH: drawn[0].placed.barH };
    }
    const budget = Math.min(gap * 0.82, 24);
    const slot = Math.max(2, budget / count);
    return { origin: -((count - 1) * slot) / 2, slot: slot, barH: Math.max(2, Math.min(7, slot * 0.72)) };
}

function drawBars(ctx, drawn, center, half, yOffset, barH, layer, hits, wingDots, dir) {
    const extremes = barExtremes(layer);
    for (const row of drawn.placed) {
        let width = drawn.maxAbs === 0 ? 0 : (Math.abs(row.gex) / drawn.maxAbs) * half;
        const y = row.y + yOffset;
        const name = nameForBar(layer, row.gex, extremes);
        ctx.fillStyle = row.gex >= 0 ? layer.pos : layer.neg;
        const sign = dir === 'left' ? 1 : (dir === 'right' ? -1 : (row.gex >= 0 ? 1 : -1));
        let x0;
        let x1;
        if (wingDots && width < 3) {
            const reach = Math.max(width, 0.5);
            const x = center + sign * reach;
            ctx.beginPath();
            ctx.arc(x, y, 2.1, 0, Math.PI * 2);
            ctx.fill();
            x0 = x - 3;
            x1 = x + 3;
        } else {
            if (row.gex !== 0 && width > 0 && width < 1) width = 1;
            const drawnW = Math.max(width, 1);
            x0 = sign >= 0 ? center : center - drawnW;
            ctx.fillRect(x0, y - barH / 2, drawnW, barH);
            x1 = x0 + drawnW;
        }
        if (hits) {
            hits.push({
                kind: 'bar',
                label: name,
                measure: name,
                strike: row.strike,
                value: row.gex,
                x0: x0,
                x1: x1,
                y: y,
                pad: Math.max(barH / 2, 4),
            });
        }
    }
}

function drawDots(ctx, drawn, center, half, yOffset, layer, hits, dotScale, dir) {
    const scale = Number.isFinite(dotScale) && dotScale > 0 ? dotScale : 1;
    ctx.lineWidth = 1;
    ctx.strokeStyle = '#101014';
    for (let i = 4; i >= 0; i--) {
        ctx.fillStyle = DOT_COLORS[i];
        const radius = DOT_RADIUS[i] * scale;
        for (const row of drawn.placed) {
            const prior = row.priors && row.priors[i];
            if (!Number.isFinite(prior)) continue;
            const x = xOf(center, half, drawn.maxAbs, prior, dir);
            const y = row.y + yOffset;
            const name = nameForBar(layer, prior, null);
            ctx.beginPath();
            ctx.arc(x, y, radius, 0, Math.PI * 2);
            ctx.fill();
            ctx.stroke();
            if (hits) {
                hits.push({
                    kind: 'dot',
                    label: name,
                    measure: name,
                    strike: row.strike,
                    value: prior,
                    minutes: DOT_LABELS[i],
                    x: x,
                    y: y,
                    pad: radius + 3,
                });
            }
        }
    }
}

function labelStrikesOn(ctx, drawn) {
    if (drawn.barH < 11) return;
    ctx.fillStyle = '#8a8a8a';
    ctx.font = '10px sans-serif';
    ctx.textBaseline = 'middle';
    for (const row of drawn.placed) ctx.fillText(String(row.strike), 4, row.y);
}

export function placeBand(frameWidth, paneLeft, spec) {
    const frameW = Math.max(1, Number(frameWidth) || 0);
    const widthFrac = spec && Number.isFinite(spec.width) ? spec.width : 0.22;
    const centerFrac = spec && Number.isFinite(spec.center) ? spec.center : 0.62;
    let widthPx = Math.max(48, widthFrac * frameW);
    if (widthPx > frameW) widthPx = frameW;
    let centerPx = centerFrac * frameW;
    const half = widthPx / 2;
    centerPx = Math.max(half, Math.min(frameW - half, centerPx));
    const frameLeft = centerPx - half;
    return {
        frameLeft: frameLeft,
        left: frameLeft - (Number(paneLeft) || 0),
        width: widthPx,
        center: centerPx,
    };
}

function paintSide(canvas, layers, yOf, spot, zeroGamma, showDots, zeroColor, axisSide, marks, labelX, frame, moves) {
    const cssW = canvas.clientWidth;
    const cssH = canvas.clientHeight;
    if (cssW < 2 || cssH < 2) return;
    const dpr = window.devicePixelRatio || 1;
    const bw = Math.max(1, Math.floor(cssW * dpr));
    const bh = Math.max(1, Math.floor(cssH * dpr));
    if (canvas.width !== bw || canvas.height !== bh) {
        canvas.width = bw;
        canvas.height = bh;
    }
    const ctx = canvas.getContext('2d');
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, cssW, cssH);
    ctx.fillStyle = '#161616';
    ctx.fillRect(0, 0, cssW, cssH);
    if (!layers.length) return;

    const frameTop = frame && Number.isFinite(frame.top) ? frame.top : 0;
    const frameBottom = frame && Number.isFinite(frame.bottom) ? frame.bottom : cssH;
    const axisTop = Math.max(0, Math.min(frameTop, cssH));
    const axisBottom = Math.max(axisTop + 1, Math.min(frameBottom, cssH));
    const showAxis = axisSide === 'left' || axisSide === 'right';
    const gutter = showAxis ? 58 : 8;
    const plotLeft = axisSide === 'left' ? gutter : 8;
    const plotWidth = Math.max(16, cssW - plotLeft - (axisSide === 'right' ? gutter : 8));
    const center = plotLeft + plotWidth / 2;
    const half = Math.max(8, plotWidth / 2 - ladderEdge(showDots));
    ctx.save();
    ctx.beginPath();
    ctx.rect(plotLeft, axisTop, plotWidth, axisBottom - axisTop);
    ctx.clip();
    hoverState.hits = paintLayers(ctx, layers, yOf, center, half, axisTop, axisBottom, showDots, false, true);
    ctx.restore();
    hoverState.space = 'side';
    ctx.save();
    ctx.beginPath();
    ctx.rect(0, axisTop, cssW, axisBottom - axisTop);
    ctx.clip();
    if (showAxis) {
        const ticks = sharedScaleTicks(layers, yOf, axisBottom, axisTop);
        ctx.font = '11px sans-serif';
        ctx.textBaseline = 'middle';
        ctx.textAlign = axisSide === 'right' ? 'left' : 'right';
        const textX = axisSide === 'right' ? plotLeft + plotWidth + 8 : gutter - 8;
        const edge = axisSide === 'right' ? plotLeft + plotWidth : plotLeft;
        for (const tick of ticks) {
            const y = yOf(tick);
            if (y == null || y < axisTop + 8 || y > axisBottom - 8) continue;
            ctx.strokeStyle = 'rgba(244,241,234,0.14)';
            ctx.beginPath();
            ctx.moveTo(plotLeft, y);
            ctx.lineTo(plotLeft + plotWidth, y);
            ctx.stroke();
            ctx.strokeStyle = '#f4f1ea';
            ctx.beginPath();
            ctx.moveTo(edge, y);
            ctx.lineTo(edge + (axisSide === 'right' ? 6 : -6), y);
            ctx.stroke();
            ctx.fillStyle = '#f4f1ea';
            ctx.fillText(formatStrike(tick), textX, y);
        }
    }
    paintMarks(ctx, yOf, plotLeft, plotLeft + plotWidth, marks, hoverState.hits, labelX);
    paintMoveBands(ctx, yOf, moves, plotLeft, plotLeft + plotWidth, axisTop, axisBottom, showAxis ? axisSide : 'right');
    ctx.restore();
}

function sharedScaleTicks(layers, yOf, height, top) {
    const edge = Number.isFinite(top) ? top : 0;
    const visible = [];
    for (const strike of collectStrikes(layers)) {
        const y = yOf(strike);
        if (y == null || y < edge || y > height) continue;
        visible.push(strike);
    }
    if (visible.length < 2) return [];
    let lo = Infinity;
    let hi = -Infinity;
    for (const strike of visible) {
        if (strike < lo) lo = strike;
        if (strike > hi) hi = strike;
    }
    return priceAxisTicks(visible, lo, hi, priceTickMode(), yOf, 16, edge + 8, height - 8);
}

function finitePrice(value) {
    const n = Number(value);
    if (!Number.isFinite(n) || n === 0) return null;
    return n;
}

function field(payload, key) {
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return null;
    const n = Number(payload[key]);
    return Number.isFinite(n) ? n : null;
}

function priceField(payload, key) {
    const n = field(payload, key);
    if (n == null || n === 0) return null;
    return n;
}

function snapshotSpot(data) {
    if (!data) return null;
    const direct = numberOr(data.spot, null);
    if (direct != null && direct !== 0) return direct;
    const profiles = data.profiles || {};
    for (const key of Object.keys(profiles)) {
        const spot = priceField(profiles[key], 'spot');
        if (spot != null) return spot;
    }
    return null;
}

function maxChanges(payload) {
    const rows = strikeRows(payload, 1);
    const moves = [];
    for (let i = 0; i < 5; i++) {
        let best = null;
        for (const row of rows) {
            if (!row.priors || !Number.isFinite(row.priors[i])) continue;
            const delta = row.gex - row.priors[i];
            if (!Number.isFinite(delta)) continue;
            if (!best || Math.abs(delta) > Math.abs(best.delta)) best = { strike: row.strike, delta: delta };
        }
        moves.push(best);
    }
    return moves;
}

function metricRow(label, value, color, title) {
    const row = document.createElement('div');
    row.className = 'metric-row';
    if (title) row.title = title;
    const name = document.createElement('span');
    name.textContent = label;
    const shown = document.createElement('b');
    shown.textContent = value == null ? '—' : value;
    if (color) shown.style.color = color;
    row.appendChild(name);
    row.appendChild(shown);
    return row;
}

function metricSection(title, titleAttr) {
    const section = document.createElement('section');
    const heading = document.createElement('h2');
    heading.textContent = title;
    if (titleAttr) heading.title = titleAttr;
    section.appendChild(heading);
    return section;
}

function appendChanges(section, payload, negColor) {
    const moves = maxChanges(payload);
    const labels = ['1 min', '5 min', '10 min', '15 min', '30 min'];
    for (let i = 0; i < labels.length; i++) {
        const row = document.createElement('div');
        row.className = 'metric-change';
        const when = document.createElement('span');
        when.textContent = labels[i];
        const strike = document.createElement('span');
        strike.className = 'metric-strike';
        const value = document.createElement('b');
        const move = moves[i];
        strike.textContent = move ? formatStrike(move.strike) : '—';
        if (!move) {
            value.textContent = '—';
            value.style.color = '#d6d6d6';
        } else {
            value.textContent = formatChange(move.delta);
            value.style.color = move.delta < 0 ? negColor : '#2BE05C';
        }
        row.appendChild(when);
        row.appendChild(strike);
        row.appendChild(value);
        section.appendChild(row);
    }
}

function occupyMetrics(column, shown, onLeft) {
    if (!column) return;
    column.hidden = !shown;
    column.classList.toggle('metrics-left', !!onLeft);
    column.style.display = shown ? '' : 'none';
    column.style.flex = shown ? '' : '0 0 0px';
    column.style.width = shown ? '' : '0px';
    column.style.minWidth = shown ? '' : '0px';
    column.style.padding = shown ? '' : '0px';
    column.style.border = shown ? '' : 'none';
}

function renderMetrics(column, data, dte, layerOn, dateStr) {
    if (!column) return;
    const scroll = column.scrollTop;
    const profiles = (data && data.profiles) || {};
    const head = document.createElement('div');
    head.className = 'metrics-head';
    const headLabel = document.createElement('span');
    headLabel.textContent = 'Information';
    const close = document.createElement('button');
    close.type = 'button';
    close.className = 'metrics-close';
    close.textContent = '\u00d7';
    close.title = 'Hide information';
    close.addEventListener('click', (event) => {
        event.preventDefault();
        event.stopPropagation();
        if (typeof column.onMetricsClose === 'function') column.onMetricsClose();
    });
    head.append(headLabel, close);
    const update = metricSection('update');
    update.appendChild(metricRow('date', formatMetricDate(dateStr), '#d6d6d6'));
    const stamp = data ? Number(data.timestamp) : NaN;
    update.appendChild(metricRow('time', Number.isFinite(stamp) ? formatMetricTime(stamp) : '—', '#d6d6d6'));
    const spot = snapshotSpot(data);
    update.appendChild(metricRow('spot', spot == null ? null : formatLevel(spot), SPOT_MARK));
    const blocks = [update];
    if (layerOn('classic')) {
        const payload = profiles[endpointFor(LAYER_SPECS[0], dte)] || null;
        const neg = '#FF4747';
        const section = metricSection('classic');
        const zeroRaw = data ? numberOr(data.zero_gamma, priceField(payload, 'zero_gamma')) : null;
        const zero = zeroRaw === 0 ? null : zeroRaw;
        section.appendChild(metricRow('zero gamma', zero == null ? null : formatLevel(zero), ZERO_MARK, 'price level where GEX by strike is neutral. gamma flip'));
        section.appendChild(metricRow('major call gamma', priceField(payload, 'major_pos_vol') == null ? null : formatLevel(priceField(payload, 'major_pos_vol')), '#2BE05C', 'peak call gamma by volume. call wall'));
        section.appendChild(metricRow('major put gamma', priceField(payload, 'major_neg_vol') == null ? null : formatLevel(priceField(payload, 'major_neg_vol')), neg, 'peak put gamma by volume. put wall'));
        const net = field(payload, 'sum_gex_vol');
        section.appendChild(metricRow('net gex', net == null ? null : formatNet(net), net != null && net < 0 ? neg : '#2BE05C', 'sum of all call and put GEX'));
        const oi = metricSection('open interest', 'Open interest is the morning print. It is calculated once near the open and stays fixed while volume keeps ticking.');
        oi.appendChild(metricRow('major call gamma', priceField(payload, 'major_pos_oi') == null ? null : formatLevel(priceField(payload, 'major_pos_oi')), '#00942A', 'peak call gamma by OI. call wall'));
        oi.appendChild(metricRow('major put gamma', priceField(payload, 'major_neg_oi') == null ? null : formatLevel(priceField(payload, 'major_neg_oi')), '#940D0D', 'peak put gamma by OI. put wall'));
        const oiNet = field(payload, 'sum_gex_oi');
        oi.appendChild(metricRow('net gex', oiNet == null ? null : formatNet(oiNet), oiNet != null && oiNet < 0 ? '#940D0D' : '#00942A', 'sum of all call and put GEX'));
        const change = metricSection('max change');
        appendChanges(change, payload, neg);
        blocks.push(section, oi, change);
    }
    if (layerOn('state')) {
        const spec = LAYER_SPECS.find((item) => item.id === 'state');
        const payload = profiles[endpointFor(spec, dte)] || null;
        const neg = '#FF0000';
        const section = metricSection('state');
        section.appendChild(metricRow('major call gamma', priceField(payload, 'major_pos_vol') == null ? null : formatLevel(priceField(payload, 'major_pos_vol')), '#2BE05C', 'peak call gamma by volume. call wall'));
        section.appendChild(metricRow('major put gamma', priceField(payload, 'major_neg_vol') == null ? null : formatLevel(priceField(payload, 'major_neg_vol')), neg, 'peak put gamma by volume. put wall'));
        const net = field(payload, 'sum_gex_vol');
        section.appendChild(metricRow('net gex', net == null ? null : formatNet(net), net != null && net < 0 ? neg : '#2BE05C', 'sum of all call and put GEX'));
        const change = metricSection('max change');
        appendChanges(change, payload, neg);
        blocks.push(section, change);
    }
    for (const spec of LAYER_SPECS) {
        if (!spec.greek || !layerOn(spec.id)) continue;
        const name = endpointFor(spec, dte);
        if (!name) continue;
        const payload = profiles[name] || null;
        const section = metricSection(spec.label.toLowerCase());
        const longSide = spec.id === 'gamma';
        const longLabel = longSide ? 'major long gamma' : 'major positive ' + spec.label.toLowerCase();
        const shortLabel = longSide ? 'major short gamma' : 'major negative ' + spec.label.toLowerCase();
        const longPrice = priceField(payload, longSide ? 'major_long_gamma' : 'major_positive');
        const shortPrice = priceField(payload, longSide ? 'major_short_gamma' : 'major_negative');
        const longShown = longPrice != null ? longPrice : priceField(payload, 'major_long_gamma');
        const shortShown = shortPrice != null ? shortPrice : priceField(payload, 'major_short_gamma');
        section.appendChild(metricRow(longLabel, longShown == null ? null : formatLevel(longShown), '#00FFFF'));
        section.appendChild(metricRow(shortLabel, shortShown == null ? null : formatLevel(shortShown), '#AE4AD5'));
        blocks.push(section);
    }
    column.replaceChildren.apply(column, [head].concat(blocks));
    column.scrollTop = scroll;
}

function setMetric(id, text, color) {
    const el = document.getElementById(id);
    if (!el) return;
    el.textContent = text;
    if (color) el.style.color = color;
}

function textOrDash(value, format) {
    return value == null ? '—' : format(value);
}

function formatMetricDate(iso) {
    const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(String(iso || ''));
    if (!match) return '—';
    return Number(match[2]) + '/' + Number(match[3]) + '/' + match[1];
}

function formatMetricTime(sec) {
    return new Date(sec * 1000).toLocaleTimeString('en-US', {
        timeZone: 'America/New_York',
        hour: 'numeric',
        minute: '2-digit',
        second: '2-digit',
    });
}

function formatLevel(n) {
    const cents = Math.round(n * 100) / 100;
    const digits = Math.abs(n - cents) > 0.0005 ? 3 : 2;
    return n.toLocaleString('en-US', { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

function formatNet(n) {
    return n.toLocaleString('en-US', { minimumFractionDigits: 4, maximumFractionDigits: 4 });
}

function formatChange(n) {
    return formatNet(n);
}

function pairIV(row) {
    if (!Array.isArray(row) || row.length < 3) return null;
    const call = Number(row[1]);
    const put = Number(row[2]);
    if (!(call > 0) || !(put > 0)) return null;
    return (call + put) / 2;
}

function weightedIV(raw, spot) {
    if (!Array.isArray(raw)) return null;
    const rows = [];
    for (const row of raw) {
        if (!Array.isArray(row) || row.length < 3) continue;
        const strike = Number(row[0]);
        if (!Number.isFinite(strike)) continue;
        rows.push(row);
    }
    rows.sort((a, b) => Number(a[0]) - Number(b[0]));
    if (rows.length < 3) return null;
    let atm = 0;
    let best = Infinity;
    for (let i = 0; i < rows.length; i++) {
        const dist = Math.abs(Number(rows[i][0]) - spot);
        if (dist < best) {
            best = dist;
            atm = i;
        }
    }
    if (atm < 1 || atm > rows.length - 2) return null;
    const below = pairIV(rows[atm - 1]);
    const mid = pairIV(rows[atm]);
    const above = pairIV(rows[atm + 1]);
    if (below == null || mid == null || above == null) return null;
    return mid * 0.5 + below * 0.25 + above * 0.25;
}

function secondsUntilClose(unixSeconds) {
    if (!Number.isFinite(unixSeconds)) return null;
    const parts = new Intl.DateTimeFormat('en-US', {
        timeZone: 'America/New_York',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hourCycle: 'h23',
    }).formatToParts(new Date(unixSeconds * 1000));
    const read = function (type) {
        const part = parts.find((item) => item.type === type);
        return part ? Number(part.value) : NaN;
    };
    let hour = read('hour');
    if (hour === 24) hour = 0;
    const minute = read('minute');
    const second = read('second');
    if (![hour, minute, second].every((part) => Number.isFinite(part))) return null;
    return Math.max(0, 16 * 3600 - (hour * 3600 + minute * 60 + second));
}

function dteDays(dte, payload, unixSeconds) {
    const left = secondsUntilClose(unixSeconds);
    if (left == null) return null;
    if (dte === 'one') {
        const sec = payload ? Number(payload.sec_min_dte) : NaN;
        if (Number.isFinite(sec) && sec > 3600) return sec / 86400;
        return 1 + left / 86400;
    }
    if (dte !== 'zero' || !(left > 0)) return null;
    return left / 86400;
}

function expectedBounds(data, layers, dte) {
    if (!data || dte === 'full') return null;
    const spec = LAYER_SPECS.find((item) => item.greek && (layers || []).some((layer) => layer.id === item.id));
    if (!spec) return null;
    const payload = (data.profiles || {})[endpointFor(spec, dte)];
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return null;
    const spot = finitePrice(numberOr(data.spot, payload.spot));
    if (spot == null) return null;
    const iv = weightedIV(payload.mini_contracts, spot);
    if (!(iv > 0)) return null;
    const days = dteDays(dte, payload, Number(data.timestamp));
    if (!(days > 0)) return null;
    const move = spot * iv * Math.sqrt(days / 365);
    if (!(move > 0)) return null;
    return [spot - move, spot + move];
}

function chartLevelState(data, layers, dte) {
    const profiles = (data && data.profiles) || {};
    const visible = {};
    for (const layer of layers || []) visible[layer.id] = true;
    const majors = [];
    let zero = null;
    for (const spec of LAYER_SPECS) {
        if (spec.greek || !visible[spec.id]) continue;
        const payload = profiles[endpointFor(spec, dte)];
        if (!payload || typeof payload !== 'object' || Array.isArray(payload)) continue;
        const pos = finitePrice(payload.major_pos_vol);
        const neg = finitePrice(payload.major_neg_vol);
        const posColor = spec.id === 'state' ? '#03FF35' : '#66FF00';
        if (pos != null) majors.push({ price: pos, color: posColor });
        if (neg != null) majors.push({ price: neg, color: '#B10000' });
        if (spec.id === 'classic') zero = numberOr(data.zero_gamma, finitePrice(payload.zero_gamma));
    }
    return {
        spot: data ? numberOr(data.spot, null) : null,
        zero: zero,
        majors: majors,
        moves: expectedBounds(data, layers, dte) || [],
    };
}

function layerScaleMax(layer, showDots) {
    return rowExtent(layer.rows, !!(showDots && layer.dots));
}

function formatMagnitude(n) {
    if (!Number.isFinite(n)) return '';
    return n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function drawMagnitudeRow(ctx, layer, center, half, y, plotW, showDots, baseline, dir) {
    const maxAbs = layerScaleMax(layer, showDots);
    if (!(maxAbs > 0)) return;
    ctx.textBaseline = baseline;
    if (dir === 'left' || dir === 'right') {
        const ticks = niceTicks(0, maxAbs, 4);
        for (const tick of ticks) {
            const x = dir === 'left' ? center + (tick / maxAbs) * half : center - (tick / maxAbs) * half;
            if (x < 2 || x > plotW - 2) continue;
            ctx.textAlign = tick === 0 ? (dir === 'left' ? 'left' : 'right') : 'center';
            ctx.fillText(formatMagnitude(Math.abs(tick)), x, y);
        }
        return;
    }
    const ticks = niceTicks(-maxAbs, maxAbs, 7);
    ctx.textAlign = 'center';
    for (const tick of ticks) {
        const x = center + (tick / maxAbs) * half;
        if (x < 28 || x > plotW - 28) continue;
        ctx.fillText(formatMagnitude(tick), x, y);
    }
}

function onChartPlacement(plotW, showDots, align, band) {
    const edge = ladderEdge(showDots) + 8;
    if (align === 'left' || align === 'right') {
        const room = Math.max(8, plotW - edge * 2);
        const reach = Math.min(room, Math.max(8, plotW * 0.36));
        const center = align === 'left' ? edge : Math.max(edge, plotW - edge);
        return { center: center, half: reach, dir: align };
    }
    const widthFrac = band && Number.isFinite(band.width) ? band.width : 0.22;
    let bandW = Math.max(48, widthFrac * plotW);
    bandW = Math.min(bandW, Math.max(48, plotW - edge * 2));
    const half = Math.max(8, bandW / 2);
    const frac = band && Number.isFinite(band.center) ? band.center : 0.62;
    let center = frac * plotW;
    center = Math.max(edge + half, Math.min(plotW - edge - half, center));
    return { center: center, half: half, dir: 'center' };
}

function paintChartMagnitude(ctx, layers, center, half, plotW, plotH, showDots, align) {
    const classic = layers.some((layer) => layer.id === 'classic');
    const state = layers.find((layer) => layer.id === 'state');
    const gamma = layers.find((layer) => layer.id === 'gamma');
    ctx.save();
    ctx.font = '11px sans-serif';
    ctx.fillStyle = 'rgba(244,241,234,0.82)';
    if (gamma) drawMagnitudeRow(ctx, gamma, center, half, 4, plotW, false, 'top', align);
    if (classic) {
        ctx.textBaseline = 'top';
        const y = gamma ? 18 : 4;
        if (align === 'left') {
            ctx.textAlign = 'left';
            ctx.fillText('0.00', center + 4, y);
        } else if (align === 'right') {
            ctx.textAlign = 'right';
            ctx.fillText('0.00', center - 4, y);
        } else {
            ctx.textAlign = 'center';
            ctx.fillText('0.00', center, y);
        }
    }
    if (state) drawMagnitudeRow(ctx, state, center, half, plotH - 4, plotW, showDots, 'bottom', align);
    ctx.restore();
}

function dashAcross(ctx, y, left, right, color) {
    ctx.save();
    ctx.strokeStyle = color;
    ctx.lineWidth = 1;
    ctx.setLineDash([4, 4]);
    ctx.beginPath();
    ctx.moveTo(left, y);
    ctx.lineTo(right, y);
    ctx.stroke();
    ctx.restore();
}

function drawEdgePills(ctx, pills, yOf, plotW, plotH, side) {
    const placed = [];
    ctx.save();
    ctx.font = '11px sans-serif';
    ctx.textBaseline = 'middle';
    ctx.textAlign = 'center';
    for (const pill of pills) {
        let y = yOf(pill.price);
        if (y == null || y < 10 || y > plotH - 10) continue;
        for (const other of placed) {
            if (Math.abs(other - y) < 16) y = other + 16;
        }
        if (y < 10 || y > plotH - 10) continue;
        placed.push(y);
        const w = Math.min(78, ctx.measureText(pill.text).width + 10);
        const x = side === 'left' ? 2 : plotW - w - 2;
        roundRect(ctx, x, y - 8, w, 16, 3);
        ctx.fillStyle = pill.bg;
        ctx.fill();
        ctx.fillStyle = textOn(pill.bg);
        ctx.fillText(pill.text, x + w / 2, y + 1);
    }
    ctx.restore();
}

function paintChartLevels(ctx, yOf, plotW, plotH, levels, priceSide, hits) {
    if (!levels) return;
    const pills = [];
    for (const mark of levels.marks || []) {
        const y = yOf(mark.price);
        if (y == null || y < 0 || y > plotH) continue;
        dashAcross(ctx, y, 0, plotW, mark.color);
        ctx.fillStyle = mark.color;
        ctx.font = '10px sans-serif';
        ctx.textAlign = 'left';
        ctx.textBaseline = 'bottom';
        const caption = mark.label === 'Zero gamma' ? '0 gamma' : mark.label;
        ctx.fillText(caption, levelTextX(ctx, caption, 0, plotW, levels.labelX), y - 1);
        pills.push({ price: mark.price, bg: mark.color, text: formatPrice(mark.price) });
        if (hits && mark.label === 'Zero gamma') {
            hits.push({
                kind: 'bar',
                label: '0 gamma',
                detail: ZERO_HOVER,
                strike: mark.price,
                value: mark.price,
                x0: 0,
                x1: plotW,
                y: y,
                pad: 6,
            });
        }
    }
    for (const price of levels.moves || []) {
        const y = yOf(price);
        if (y == null || y < 0 || y > plotH) continue;
        dashAcross(ctx, y, 0, plotW, '#888888');
        pills.push({ price: price, bg: '#888888', text: formatPrice(price) });
    }
    drawEdgePills(ctx, pills, yOf, plotW, plotH, priceSide);
}

function paintMoveBands(ctx, yOf, moves, left, right, top, bottom, side) {
    if (!moves || !moves.length || !(right > left) || !(bottom > top)) return;
    const pills = [];
    for (const price of moves) {
        const y = yOf(price);
        if (y == null || !Number.isFinite(y) || y < top || y > bottom) continue;
        dashAcross(ctx, y, left, right, '#888888');
        pills.push({ price: price, bg: '#888888', text: formatPrice(price) });
    }
    if (!pills.length) return;
    const placed = [];
    ctx.save();
    ctx.font = '11px sans-serif';
    ctx.textBaseline = 'middle';
    ctx.textAlign = 'center';
    for (const pill of pills) {
        let y = yOf(pill.price);
        if (y == null || y < top + 8 || y > bottom - 8) continue;
        for (const other of placed) {
            if (Math.abs(other - y) < 16) y = other + 16;
        }
        if (y < top + 8 || y > bottom - 8) continue;
        placed.push(y);
        const w = Math.min(78, ctx.measureText(pill.text).width + 10);
        const x = side === 'left' ? left + 2 : right - w - 2;
        roundRect(ctx, x, y - 8, w, 16, 3);
        ctx.fillStyle = pill.bg;
        ctx.fill();
        ctx.fillStyle = textOn('#888888');
        ctx.fillText(pill.text, x + w / 2, y + 1);
    }
    ctx.restore();
}

function drawMoveBounds(target, yOf, levels, priceSide) {
    const moves = levels && levels.moves;
    if (!moves || !moves.length || !target || typeof target.useBitmapCoordinateSpace !== 'function') return;
    target.useBitmapCoordinateSpace(function (scope) {
        const ctx = scope.context;
        const hRatio = scope.horizontalPixelRatio || 1;
        const vRatio = scope.verticalPixelRatio || 1;
        const bw = scope.bitmapSize ? scope.bitmapSize.width : 0;
        const bh = scope.bitmapSize ? scope.bitmapSize.height : 0;
        if (bw < 2 || bh < 2) return;
        const plotW = bw / hRatio;
        const plotH = bh / vRatio;
        ctx.save();
        ctx.setTransform(hRatio, 0, 0, vRatio, 0, 0);
        const pills = [];
        for (const price of moves) {
            const y = yOf(price);
            if (y == null || y < 0 || y > plotH) continue;
            dashAcross(ctx, y, 0, plotW, '#888888');
            pills.push({ price: price, bg: '#888888', text: formatPrice(price) });
        }
        drawEdgePills(ctx, pills, yOf, plotW, plotH, priceSide);
        ctx.restore();
    });
}

function shiftHits(hits, dx, dy) {
    for (const hit of hits) {
        if (hit.x0 != null) hit.x0 += dx;
        if (hit.x1 != null) hit.x1 += dx;
        if (hit.x != null) hit.x += dx;
        if (hit.y != null) hit.y += dy;
    }
}

function drawOnChart(target, layers, yOf, showDots, levels, priceSide, align, band) {
    if (!layers.length) return;
    target.useBitmapCoordinateSpace(function (scope) {
        const ctx = scope.context;
        const hRatio = scope.horizontalPixelRatio || 1;
        const vRatio = scope.verticalPixelRatio || 1;
        const bw = scope.bitmapSize ? scope.bitmapSize.width : 0;
        const bh = scope.bitmapSize ? scope.bitmapSize.height : 0;
        if (bw < 2 || bh < 2) return;
        const plotW = bw / hRatio;
        const plotH = bh / vRatio;
        const place = onChartPlacement(plotW, showDots, align, band);
        ctx.save();
        ctx.setTransform(hRatio, 0, 0, vRatio, 0, 0);
        const hits = paintLayers(ctx, layers, yOf, place.center, place.half, 0, plotH, showDots, false, true, true, 0.62, place.dir);
        paintChartMagnitude(ctx, layers, place.center, place.half, plotW, plotH, showDots, align);
        paintChartLevels(ctx, yOf, plotW, plotH, levels, priceSide, hits);
        const canvas = ctx.canvas;
        const frame = document.getElementById('chart-div');
        if (frame && canvas && typeof canvas.getBoundingClientRect === 'function') {
            const frameRect = frame.getBoundingClientRect();
            const canvasRect = canvas.getBoundingClientRect();
            shiftHits(hits, canvasRect.left - frameRect.left, canvasRect.top - frameRect.top);
        }
        hoverState.hits = hits;
        hoverState.space = 'chart';
        ctx.restore();
    });
}

function paintMarks(ctx, yOf, left, right, marks, hits, labelX) {
    for (const mark of marks || []) {
        markLevel(ctx, yOf, left, right, mark.price, mark.color, mark.label, hits, labelX);
    }
}

function markLevel(ctx, yOf, left, right, value, color, label, hits, labelX) {
    if (value == null) return;
    const y = yOf(value);
    if (y == null || !Number.isFinite(y)) return;
    ctx.strokeStyle = color;
    ctx.setLineDash([3, 3]);
    ctx.beginPath();
    ctx.moveTo(left, y);
    ctx.lineTo(right, y);
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = color;
    ctx.font = '10px sans-serif';
    ctx.textBaseline = 'bottom';
    const caption = label === '0 gamma' || label === 'Zero gamma' ? '0 gamma' : label + ' ' + formatPrice(value);
    ctx.fillText(caption, levelTextX(ctx, caption, left, right, labelX), y - 1);
    if (hits && (label === '0 gamma' || label === 'Zero gamma')) {
        hits.push({
            kind: 'bar',
            label: '0 gamma',
            detail: ZERO_HOVER,
            strike: value,
            value: value,
            x0: left,
            x1: right,
            y: y,
            pad: 6,
        });
    }
}

export function focusPriceRange(layers, spot, zero) {
    return fitPriceRange(collectPrices(layers, spot, zero));
}

export function fitPriceRange(values) {
    let lo = Infinity;
    let hi = -Infinity;
    for (const value of values) {
        if (!Number.isFinite(value)) continue;
        if (value < lo) lo = value;
        if (value > hi) hi = value;
    }
    if (!Number.isFinite(lo) || !(hi > lo)) {
        const mid = Number.isFinite(lo) ? lo : 0;
        return { lo: mid - 1, hi: mid + 1 };
    }
    const pad = (hi - lo) * 0.06;
    return { lo: lo - pad, hi: hi + pad };
}

export function priceToY(price, lo, hi, top, height) {
    if (!Number.isFinite(price) || !(hi > lo) || !(height > 0)) return null;
    return top + ((hi - price) / (hi - lo)) * height;
}

export function yToPrice(y, lo, hi, top, height) {
    if (!(height > 0) || !(hi > lo)) return lo;
    return hi - ((y - top) / height) * (hi - lo);
}

export function zoomPrice(lo, hi, anchor, factor) {
    if (!Number.isFinite(anchor) || !(factor > 0) || !(hi > lo)) return { lo: lo, hi: hi };
    const nextLo = anchor - (anchor - lo) * factor;
    const nextHi = anchor + (hi - anchor) * factor;
    if (!(nextHi > nextLo)) return { lo: lo, hi: hi };
    return { lo: nextLo, hi: nextHi };
}

export function panPrice(lo, hi, dy, height) {
    if (!(height > 0) || !(hi > lo)) return { lo: lo, hi: hi };
    const shift = (dy / height) * (hi - lo);
    return { lo: lo + shift, hi: hi + shift };
}

export function xGeometry(plotX, plotW, xOrigin, xZoom, maxAbs, edge) {
    const pad = Number.isFinite(edge) ? edge : 0;
    const zeroX = plotX + xOrigin * plotW;
    const half = Math.max(1, plotW / 2 - pad);
    const pxPerUnit = maxAbs ? (half * xZoom) / maxAbs : 0;
    return { zeroX: zeroX, half: half, pxPerUnit: pxPerUnit };
}

export function valueAtX(x, geom, maxAbs) {
    if (!geom || !geom.pxPerUnit || !maxAbs) return 0;
    return (x - geom.zeroX) / geom.pxPerUnit;
}

export function zoomXAround(xOrigin, xZoom, plotX, plotW, cursorX, factor, maxAbs, edge) {
    const before = xGeometry(plotX, plotW, xOrigin, xZoom, maxAbs, edge);
    const value = valueAtX(cursorX, before, maxAbs);
    const nextZoom = clamp(xZoom * factor, 0.15, 80);
    const half = xGeometry(plotX, plotW, xOrigin, nextZoom, maxAbs, edge).half;
    const zeroX = maxAbs ? cursorX - value * (half * nextZoom) / maxAbs : plotX + plotW / 2;
    const origin = plotW ? clamp((zeroX - plotX) / plotW, -1, 2) : 0.5;
    return { xOrigin: origin, xZoom: nextZoom };
}

export function moveX(xOrigin, dx, plotW) {
    if (!(plotW > 0)) return xOrigin;
    return clamp(xOrigin + dx / plotW, -1, 2);
}

export function niceTicks(lo, hi, count) {
    const span = hi - lo;
    if (!(span > 0)) return [lo];
    const rough = span / Math.max(2, count);
    const pow = Math.pow(10, Math.floor(Math.log10(rough)));
    const err = rough / pow;
    let step = 10 * pow;
    if (err <= 1) step = pow;
    else if (err <= 2) step = 2 * pow;
    else if (err <= 5) step = 5 * pow;
    const ticks = [];
    const start = Math.ceil((lo - step * 1e-9) / step) * step;
    for (let v = start; v <= hi + step * 0.001 && ticks.length < 12; v += step) {
        ticks.push(Number(v.toPrecision(12)));
    }
    return ticks.length ? ticks : [lo, hi];
}

function collectStrikes(layers) {
    const values = [];
    for (const layer of layers) {
        for (const row of layer.rows) values.push(row.strike);
    }
    return values;
}

export function strikeLabelStep(strikes) {
    const sorted = [];
    for (const strike of strikes) if (Number.isFinite(strike)) sorted.push(strike);
    sorted.sort((a, b) => a - b);
    let gap = 5;
    if (sorted.length > 1) {
        const gaps = [];
        for (let i = 1; i < sorted.length; i++) {
            const delta = sorted[i] - sorted[i - 1];
            if (delta > 0.01) gaps.push(delta);
        }
        gaps.sort((a, b) => a - b);
        if (gaps.length) gap = gaps[Math.floor(gaps.length / 2)];
    }
    return gap >= 10 ? 10 : 5;
}

export function strikeTicks(strikes, lo, hi) {
    if (!(hi > lo)) return [];
    const step = strikeLabelStep(strikes);
    const start = Math.ceil((lo - step * 1e-9) / step) * step;
    const ticks = [];
    for (let value = start; value <= hi + step * 1e-9; value += step) {
        ticks.push(Math.round(value * 1000) / 1000);
    }
    return ticks;
}

// Aligned ticks sit on 5 or 10. True ticks are the strike prices themselves.
// Either way, labels stay at least minGap pixels apart.
export function priceAxisTicks(strikes, lo, hi, mode, yOf, minGap, top, bottom) {
    if (!(hi > lo) || typeof yOf !== 'function') return [];
    const gap = Number.isFinite(minGap) && minGap > 0 ? minGap : 18;
    let candidates;
    if (mode === 'true') {
        const seen = new Set();
        candidates = [];
        for (const strike of strikes) {
            if (!Number.isFinite(strike) || strike < lo || strike > hi) continue;
            const price = Math.round(strike * 1000) / 1000;
            const key = String(price);
            if (seen.has(key)) continue;
            seen.add(key);
            candidates.push(price);
        }
        candidates.sort((a, b) => a - b);
    } else {
        candidates = strikeTicks(strikes, lo, hi);
    }
    const ticks = [];
    let lastY = null;
    for (const price of candidates) {
        const y = yOf(price);
        if (y == null || !Number.isFinite(y)) continue;
        if (top != null && (y < top || y > bottom)) continue;
        if (lastY != null && Math.abs(y - lastY) < gap) continue;
        ticks.push(price);
        lastY = y;
    }
    return ticks;
}

let windowPriceTickMode = '';

function priceTickMode() {
    return windowPriceTickMode === 'true' ? 'true' : 'aligned';
}

function setPriceTickMode(mode) {
    windowPriceTickMode = mode === 'true' ? 'true' : 'aligned';
}

function seedPriceTickMode(ticker, dateStr) {
    const saved = readChartPrefs(ticker, dateStr).priceTickMode;
    if (saved === 'true' || saved === 'aligned') {
        windowPriceTickMode = saved;
        return;
    }
    if (windowPriceTickMode === 'true' || windowPriceTickMode === 'aligned') return;
    try { windowPriceTickMode = localStorage.getItem('profile-price-ticks') === 'true' ? 'true' : 'aligned'; }
    catch (err) { windowPriceTickMode = 'aligned'; }
}

function collectPrices(layers, spot, zero) {
    const values = [];
    for (const layer of layers) {
        for (const row of layer.rows) values.push(row.strike);
    }
    if (spot != null) values.push(spot);
    if (zero != null) values.push(zero);
    return values;
}

function formatPrice(n) {
    if (!Number.isFinite(n)) return '';
    const digits = Math.abs(n) >= 1 ? 2 : 4;
    return n.toLocaleString('en-US', { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

function formatAxisValue(n) {
    if (!Number.isFinite(n)) return '';
    const abs = Math.abs(n);
    const sign = n < 0 ? '-' : '';
    if (abs >= 1000000) return sign + (abs / 1000000).toLocaleString('en-US', { maximumFractionDigits: 2 }) + 'M';
    if (abs >= 1000) return sign + (abs / 1000).toLocaleString('en-US', { maximumFractionDigits: 1 }) + 'k';
    if (abs >= 100) return n.toLocaleString('en-US', { maximumFractionDigits: 1 });
    return n.toLocaleString('en-US', { maximumFractionDigits: 2 });
}

function textOn(color) {
    const hex = String(color || '').replace('#', '');
    if (hex.length < 6) return '#161616';
    const r = parseInt(hex.slice(0, 2), 16);
    const g = parseInt(hex.slice(2, 4), 16);
    const b = parseInt(hex.slice(4, 6), 16);
    if (![r, g, b].every((part) => Number.isFinite(part))) return '#161616';
    return (r * 0.3 + g * 0.59 + b * 0.11) > 150 ? '#161616' : '#f4f1ea';
}

function pointIn(p, rect) {
    return !!p && !!rect && p.x >= rect.x && p.x <= rect.x + rect.w && p.y >= rect.y && p.y <= rect.y + rect.h;
}

function roundRect(ctx, x, y, w, h, r) {
    ctx.beginPath();
    if (typeof ctx.roundRect === 'function') {
        ctx.roundRect(x, y, w, h, r);
        return;
    }
    ctx.rect(x, y, w, h);
}

function referenceMax(layers, showDots) {
    const layer = layers.find((item) => item.id === 'classic') || layers[0];
    if (!layer) return 1;
    return rowExtent(layer.rows, showDots && layer.dots) || 1;
}

export function paintDetached(canvas, layers, spot, zeroGamma, showDots, zeroColor, view) {
    const cssW = canvas.clientWidth;
    const cssH = canvas.clientHeight;
    if (cssW < 2 || cssH < 2) return null;
    const dpr = window.devicePixelRatio || 1;
    const bw = Math.max(1, Math.floor(cssW * dpr));
    const bh = Math.max(1, Math.floor(cssH * dpr));
    if (canvas.width !== bw || canvas.height !== bh) {
        canvas.width = bw;
        canvas.height = bh;
    }
    const ctx = canvas.getContext('2d');
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, cssW, cssH);
    const side = view.side === 'left' ? 'left' : 'right';
    const axis = view.axis === 'off' ? 'off' : (view.axis === 'left' || view.axis === 'right' ? view.axis : side);
    const axisW = axis === 'off' ? 0 : 78;
    const rowH = 16;
    const xAxisH = 22 + Math.max(1, layers.length) * rowH;
    const plot = {
        x: axis === 'left' ? axisW : 0,
        y: 0,
        w: Math.max(10, cssW - axisW),
        h: Math.max(10, cssH - xAxisH),
    };
    const priceAxis = {
        x: axis === 'left' ? 0 : plot.x + plot.w,
        y: 0,
        w: axisW,
        h: plot.h,
    };
    const xAxis = { x: plot.x, y: plot.y + plot.h, w: plot.w, h: xAxisH };
    const ranged = view.priceLo != null && view.priceHi != null && view.priceHi > view.priceLo;
    let range = ranged
        ? { lo: view.priceLo, hi: view.priceHi }
        : focusPriceRange(layers, spot, zeroGamma);
    if (!ranged && view.moves && view.moves.length) {
        let lo = range.lo;
        let hi = range.hi;
        let grew = false;
        for (const price of view.moves) {
            if (!Number.isFinite(price)) continue;
            if (price < lo) { lo = price; grew = true; }
            if (price > hi) { hi = price; grew = true; }
        }
        if (grew) range = fitPriceRange([lo, hi]);
    }
    function yOf(price) {
        return priceToY(price, range.lo, range.hi, plot.y, plot.h);
    }

    ctx.fillStyle = '#161616';
    ctx.fillRect(0, 0, cssW, cssH);
    ctx.fillStyle = '#121212';
    ctx.fillRect(priceAxis.x, priceAxis.y, priceAxis.w, priceAxis.h);
    ctx.fillRect(xAxis.x, xAxis.y, xAxis.w, xAxis.h);

    const strikeTickList = priceAxisTicks(collectStrikes(layers), range.lo, range.hi, priceTickMode(), yOf, 18, plot.y + 10, plot.y + plot.h - 10);
    if (axis !== 'off') {
        ctx.save();
        ctx.beginPath();
        ctx.rect(priceAxis.x, priceAxis.y, priceAxis.w, priceAxis.h);
        ctx.clip();
        ctx.font = '12px sans-serif';
        ctx.fillStyle = '#f4f1ea';
        ctx.textBaseline = 'middle';
        ctx.textAlign = axis === 'left' ? 'right' : 'left';
        const tickX = axis === 'left' ? priceAxis.x + priceAxis.w - 10 : priceAxis.x + 10;
        for (const tick of strikeTickList) {
            const y = yOf(tick);
            if (y == null || y < plot.y + 10 || y > plot.y + plot.h - 10) continue;
            ctx.strokeStyle = '#f4f1ea';
            ctx.beginPath();
            const edge = axis === 'left' ? priceAxis.x + priceAxis.w : priceAxis.x;
            ctx.moveTo(edge, y);
            ctx.lineTo(edge + (axis === 'left' ? -8 : 8), y);
            ctx.stroke();
            ctx.fillText(formatStrike(tick), tickX, y);
        }
        ctx.restore();
    }

    const hits = [];
    const edge = ladderEdge(!!showDots);
    const scale = xGeometry(plot.x, plot.w, view.xOrigin, view.xZoom, 1, edge);
    const zeroX = scale.zeroX;
    const half = scale.half;
    ctx.save();
    ctx.beginPath();
    ctx.rect(plot.x, plot.y, plot.w, plot.h);
    ctx.clip();
    ctx.strokeStyle = 'rgba(244,241,234,0.45)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(zeroX, plot.y);
    ctx.lineTo(zeroX, plot.y + plot.h);
    ctx.stroke();
    ctx.strokeStyle = 'rgba(244,241,234,0.14)';
    for (const tick of strikeTickList) {
        const y = yOf(tick);
        if (y == null || y < plot.y || y > plot.y + plot.h) continue;
        ctx.beginPath();
        ctx.moveTo(plot.x, y);
        ctx.lineTo(plot.x + plot.w, y);
        ctx.stroke();
    }
    const drawn = [];
    for (const layer of layers) {
        const placed = placedRows(layer.rows, yOf, plot.y, plot.y + plot.h, showDots && layer.dots);
        if (placed.placed.length) drawn.push({ layer: layer, placed: placed });
    }
    const bunch = drawn.length ? bunchLayout(drawn) : null;
    if (bunch) {
        for (let i = 0; i < drawn.length; i++) {
            drawZoomedBars(ctx, drawn[i], zeroX, half, view.xZoom, bunch.origin + i * bunch.slot, bunch.barH, plot, hits);
        }
        if (showDots) {
            for (let i = 0; i < drawn.length; i++) {
                if (!drawn[i].layer.dots) continue;
                drawZoomedDots(ctx, drawn[i], zeroX, half, view.xZoom, bunch.origin + i * bunch.slot, plot, hits);
            }
        }
    }
    for (const mark of view.marks || []) {
        drawPlotLevel(ctx, yOf, plot, mark.price, mark.color, mark.label, hits, view.labelX);
    }
    for (const price of view.moves || []) {
        drawPlotLevel(ctx, yOf, plot, price, '#888888', formatPrice(price));
    }
    if (view.pointer && pointIn(view.pointer, plot)) {
        ctx.strokeStyle = 'rgba(244,241,234,0.35)';
        ctx.beginPath();
        ctx.moveTo(plot.x, view.pointer.y);
        ctx.lineTo(plot.x + plot.w, view.pointer.y);
        ctx.moveTo(view.pointer.x, plot.y);
        ctx.lineTo(view.pointer.x, plot.y + plot.h);
        ctx.stroke();
    }
    ctx.restore();

    ctx.save();
    ctx.beginPath();
    ctx.rect(xAxis.x, xAxis.y, xAxis.w, xAxis.h);
    ctx.clip();
    ctx.font = '11px sans-serif';
    ctx.textBaseline = 'middle';
    if (!drawn.length) {
        ctx.fillStyle = '#8a8a8a';
        ctx.textAlign = 'left';
        ctx.fillText('Wheel stretches · drag moves zero', xAxis.x + 8, xAxis.y + 14);
    }
    drawn.forEach(function (item, i) {
        const y = xAxis.y + 8 + i * rowH + 6;
        const geom = xGeometry(plot.x, plot.w, view.xOrigin, view.xZoom, item.placed.maxAbs, edge);
        const leftV = valueAtX(plot.x, geom, item.placed.maxAbs);
        const rightV = valueAtX(plot.x + plot.w, geom, item.placed.maxAbs);
        ctx.fillStyle = item.layer.pos;
        ctx.fillRect(xAxis.x + 6, y - 4, 8, 8);
        ctx.fillStyle = '#d6d6d6';
        ctx.textAlign = 'left';
        const measure = item.layer.measure || item.layer.label;
        ctx.fillText(measure, xAxis.x + 18, y);
        const valueX = xAxis.x + 26 + ctx.measureText(measure).width;
        if (valueX < xAxis.x + xAxis.w * 0.42) ctx.fillText(formatAxisValue(leftV), valueX, y);
        ctx.textAlign = 'right';
        ctx.fillText(formatAxisValue(rightV), xAxis.x + xAxis.w - 6, y);
        if (zeroX >= plot.x && zeroX <= plot.x + plot.w) {
            ctx.fillStyle = '#8a8a8a';
            ctx.textAlign = 'center';
            ctx.fillText('0', clamp(zeroX, xAxis.x + 130, xAxis.x + xAxis.w - 64), y);
        }
    });
    ctx.restore();

    if (view.priceTags) {
        const pills = [];
        if (spot != null) pills.push({ price: spot, bg: SPOT_MARK, fg: textOn(SPOT_MARK), text: formatPrice(spot) });
        if (zeroGamma != null) {
            const bg = zeroColor || ZERO_MARK;
            pills.push({ price: zeroGamma, bg: bg, fg: textOn(bg), text: formatPrice(zeroGamma) });
        }
        for (const price of view.moves || []) {
            if (!Number.isFinite(price)) continue;
            pills.push({ price: price, bg: '#888888', fg: textOn('#888888'), text: formatPrice(price) });
        }
        if (view.pointer && pointIn(view.pointer, plot)) {
            pills.push({
                price: yToPrice(view.pointer.y, range.lo, range.hi, plot.y, plot.h),
                bg: '#f4f1ea',
                fg: '#161616',
                text: formatPrice(yToPrice(view.pointer.y, range.lo, range.hi, plot.y, plot.h)),
                cross: true,
            });
        }
        if (axis !== 'off') drawPricePills(ctx, pills, yOf, priceAxis, axis);
    }

    if (view.pointer && pointIn(view.pointer, plot) && drawn.length) {
        const price = yToPrice(view.pointer.y, range.lo, range.hi, plot.y, plot.h);
        const bits = [formatPrice(price)];
        for (const item of drawn) {
            const geom = xGeometry(plot.x, plot.w, view.xOrigin, view.xZoom, item.placed.maxAbs, edge);
            bits.push((item.layer.measure || item.layer.label) + ' ' + formatAxisValue(valueAtX(view.pointer.x, geom, item.placed.maxAbs)));
        }
        const text = bits.join('   ');
        ctx.font = '12px sans-serif';
        const tw = ctx.measureText(text).width + 12;
        const boxW = Math.min(plot.w - 8, tw);
        ctx.fillStyle = 'rgba(16,16,20,0.88)';
        ctx.fillRect(plot.x + 4, plot.y + 4, boxW, 18);
        ctx.fillStyle = '#f4f1ea';
        ctx.textAlign = 'left';
        ctx.textBaseline = 'middle';
        ctx.fillText(text, plot.x + 10, plot.y + 13);
    }

    hoverState.hits = hits;
    hoverState.space = 'detached';
    return { plot: plot, priceAxis: priceAxis, xAxis: xAxis, range: range };
}

function drawZoomedBars(ctx, item, zeroX, half, xZoom, yOffset, barH, plot, hits) {
    const maxAbs = item.placed.maxAbs;
    const extremes = barExtremes(item.layer);
    for (const row of item.placed.placed) {
        const reach = !maxAbs ? 0 : (Math.abs(row.gex) / maxAbs) * half * xZoom;
        let x0 = row.gex >= 0 ? zeroX : zeroX - reach;
        let x1 = row.gex >= 0 ? zeroX + reach : zeroX;
        if (x1 < plot.x || x0 > plot.x + plot.w) continue;
        x0 = Math.max(plot.x, x0);
        x1 = Math.min(plot.x + plot.w, x1);
        const y = row.y + yOffset;
        ctx.fillStyle = row.gex >= 0 ? item.layer.pos : item.layer.neg;
        ctx.fillRect(x0, y - barH / 2, Math.max(1, x1 - x0), barH);
        const name = nameForBar(item.layer, row.gex, extremes);
        hits.push({
            kind: 'bar',
            label: name,
            measure: name,
            strike: row.strike,
            value: row.gex,
            x0: x0,
            x1: x0 + Math.max(1, x1 - x0),
            y: y,
            pad: Math.max(barH / 2, 4),
        });
    }
}

function drawZoomedDots(ctx, item, zeroX, half, xZoom, yOffset, plot, hits) {
    const maxAbs = item.placed.maxAbs;
    ctx.lineWidth = 1;
    ctx.strokeStyle = '#101014';
    for (let i = 4; i >= 0; i--) {
        ctx.fillStyle = DOT_COLORS[i];
        const radius = DOT_RADIUS[i];
        for (const row of item.placed.placed) {
            const prior = row.priors && row.priors[i];
            if (!Number.isFinite(prior) || !maxAbs) continue;
            const x = zeroX + (prior / maxAbs) * half * xZoom;
            if (x < plot.x || x > plot.x + plot.w) continue;
            const y = row.y + yOffset;
            ctx.beginPath();
            ctx.arc(x, y, radius, 0, Math.PI * 2);
            ctx.fill();
            ctx.stroke();
            const name = nameForBar(item.layer, prior, null);
            hits.push({
                kind: 'dot',
                label: name,
                measure: name,
                strike: row.strike,
                value: prior,
                minutes: DOT_LABELS[i],
                x: x,
                y: y,
                pad: radius + 3,
            });
        }
    }
}

function drawPlotLevel(ctx, yOf, plot, value, color, label, hits, labelX) {
    if (value == null) return;
    const y = yOf(value);
    if (y == null || y < plot.y || y > plot.y + plot.h) return;
    ctx.save();
    ctx.strokeStyle = color;
    ctx.setLineDash([3, 3]);
    ctx.beginPath();
    ctx.moveTo(plot.x, y);
    ctx.lineTo(plot.x + plot.w, y);
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = color;
    ctx.font = '10px sans-serif';
    ctx.textAlign = 'left';
    ctx.textBaseline = 'bottom';
    const caption = label === '0 gamma' || label === 'Zero gamma' ? '0 gamma' : label + ' ' + formatPrice(value);
    ctx.fillText(caption, levelTextX(ctx, caption, plot.x, plot.x + plot.w, labelX), y - 1);
    ctx.restore();
    if (hits && (label === '0 gamma' || label === 'Zero gamma')) {
        hits.push({
            kind: 'bar',
            label: '0 gamma',
            detail: ZERO_HOVER,
            strike: value,
            value: value,
            x0: plot.x,
            x1: plot.x + plot.w,
            y: y,
            pad: 6,
        });
    }
}

function drawPricePills(ctx, pills, yOf, axis, side) {
    const placed = [];
    ctx.font = '11px sans-serif';
    ctx.textBaseline = 'middle';
    ctx.textAlign = 'center';
    for (const pill of pills) {
        let y = yOf(pill.price);
        if (y == null) continue;
        for (const other of placed) {
            if (Math.abs(other - y) < 16) y = other + (pill.cross ? 16 : -16);
        }
        if (y < axis.y + 8 || y > axis.y + axis.h - 8) continue;
        placed.push(y);
        const w = Math.min(axis.w - 6, ctx.measureText(pill.text).width + 10);
        const x = side === 'left' ? axis.x + axis.w - w - 2 : axis.x + 2;
        roundRect(ctx, x, y - 8, w, 16, 3);
        ctx.fillStyle = pill.bg;
        ctx.fill();
        ctx.fillStyle = pill.fg;
        ctx.fillText(pill.text, x + w / 2, y + 1);
    }
}

export function installStandaloneProfile() {
    const canvas = document.getElementById('profile-canvas');
    const dteSelect = document.getElementById('profile-dte');
    const layersBox = document.getElementById('profile-layers');
    const note = document.getElementById('profile-note');
    const title = document.getElementById('profile-title');
    if (!canvas || !dteSelect || !layersBox || !note) return;

    const params = new URLSearchParams(window.location.search);
    const ticker = params.get('ticker') || '';
    const followSession = !params.get('date');
    let date = params.get('date') || '';
    const prefs = loadPrefs();
    let dte = readProfileDte(ticker, date);
    const layersOn = readProfileLayers(ticker, date);
    let pendingAt = null;
    let liveMode = true;
    let playing = false;
    let sliderIndex = 0;
    let lastLiveTs = null;
    let playTimer = 0;
    let timer = 0;
    let requestId = 0;
    let loadFlight = false;
    let loadQueued = false;
    let cached = null;
    let times = [];
    let settings = {};
    let priceLo = null;
    let priceHi = null;
    let xZoom = 1;
    let xOrigin = 0.5;
    let pointer = null;
    let drag = null;
    let lastLayout = null;
    let priceTags = true;
    priceTags = readChartPrefs(ticker, date).profileAxisPrice;
    if (priceTags !== true && priceTags !== false) {
        try { priceTags = localStorage.getItem('profile-axis-price') !== '0'; }
        catch (err) { priceTags = true; }
    }
    seedPriceTickMode(ticker, date);

    if (title) title.textContent = ticker ? (ticker + ' Profile') : 'Profile';
    document.title = title ? title.textContent : 'Profile';
    dteSelect.value = dte;
    paintLayerChecks(layersBox, layersOn);
    const priceToggle = document.getElementById('profile-axis-price');
    if (priceToggle) priceToggle.checked = priceTags;

    function sideSpec() {
        const prefs = readChartPrefs(ticker, date);
        const stored = settings.ProfileSettings || {};
        const prefWidth = Number(prefs.sideWidthPercent);
        let width = Number.isFinite(prefWidth) && prefWidth > 0 ? prefWidth : Number(stored.side_width_percent);
        if (!Number.isFinite(width) || width <= 0) width = 28;
        const side = prefs.profileSide === 'left' || prefs.profileSide === 'right' ? prefs.profileSide : (stored.profile_side === 'left' ? 'left' : 'right');
        return { width: clamp(width, 12, 50), side: side };
    }

    function showDots() {
        const saved = readChartPrefs(ticker, date).showPriors;
        if (saved === true || saved === false) return saved;
        const stored = settings.ProfileSettings || {};
        return stored.show_priors !== false && stored.show_priors !== 'false' && stored.show_priors !== 0;
    }

    function profileDate() {
        return date;
    }

    function zeroGammaColor() {
        return ZERO_MARK;
    }

    function showNote(text) {
        note.textContent = text || '';
        note.hidden = !text;
    }

    function formatAt(sec) {
        return new Date(sec * 1000).toLocaleTimeString();
    }

    function paintTransport() {
        const range = document.getElementById('profile-replay-range');
        const label = document.getElementById('profile-replay-time');
        const liveBtn = document.getElementById('profile-replay-live');
        const playBtn = document.getElementById('profile-replay-play');
        const last = Math.max(0, times.length - 1);
        if (sliderIndex > last) sliderIndex = last;
        if (range) {
            range.max = String(last);
            range.value = String(liveMode ? last : sliderIndex);
            range.disabled = times.length < 2;
        }
        if (label) {
            if (liveMode) label.textContent = 'Live';
            else if (times[sliderIndex] != null) label.textContent = formatAt(Number(times[sliderIndex]));
            else label.textContent = '—';
        }
        if (liveBtn) liveBtn.classList.toggle('active', liveMode);
        if (playBtn) playBtn.textContent = playing ? 'Pause' : 'Play';
    }

    function endpointNames() {
        const names = [];
        for (const spec of LAYER_SPECS) {
            if (!layersOn[spec.id]) continue;
            const name = endpointFor(spec, dte);
            if (name) names.push(name);
        }
        return names;
    }

    function draw() {
        const data = cached && cached.data;
        const layers = data ? buildLayers(data, dte, (id) => !!layersOn[id]) : [];
        const spot = data ? snapshotSpot(data) : null;
        const zero = data ? numberOr(data.zero_gamma, null) : null;
        const moves = (data && !data.missing) ? expectedBounds(data, layers, dte) : null;
        lastLayout = paintDetached(canvas, layers, spot, zero, showDots(), zeroGammaColor(), {
            priceLo: priceLo,
            priceHi: priceHi,
            xZoom: xZoom,
            xOrigin: xOrigin,
            pointer: pointer,
            priceTags: priceTags,
            side: sideSpec().side,
            axis: resolvedAxis(ticker, date, sideSpec().side),
            moves: moves || [],
            marks: data ? marksFor(data, ticker, date) : [],
            labelX: levelLabelPercent(readChartPrefs(ticker, date)),
        });
        if (data) showNote(noteForStandalone(data, layers, (id) => !!layersOn[id], dte));
        const column = document.getElementById('profile-metrics');
        if (!column) return;
        const chartPrefs = readChartPrefs(ticker, date);
        const showInfo = chartPrefs.metricsVisible !== false;
        occupyMetrics(column, showInfo, chartPrefs.metricsSide === 'left');
        column.onMetricsClose = () => {
            writeChartPrefs(ticker, date, { metricsVisible: false });
            occupyMetrics(column, false, chartPrefs.metricsSide === 'left');
            requestAnimationFrame(() => requestAnimationFrame(draw));
        };
        if (showInfo) renderMetrics(column, data && !data.missing ? data : null, dte, (id) => !!layersOn[id], date);
    }

    async function loadNow(at) {
        if (!ticker || !date) return;
        const names = endpointNames();
        const sec = at == null ? null : Math.round(Number(at));
        const liveFetch = sec == null;
        const atKey = sec == null ? 'latest' : String(sec);
        const key = [ticker, date, atKey, dte, names.join('|')].join(':');
        if (!names.length) {
            if (!cached || cached.key !== key) {
                cached = { key: key, data: { profiles: {}, legacy: false, missing: false } };
            }
            draw();
            return;
        }
        if (!liveFetch && cached && cached.key === key) {
            draw();
            return;
        }
        const mine = ++requestId;
        let url = '/api/profile/' + encodeURIComponent(ticker) + '/' + encodeURIComponent(date);
        const query = [];
        if (sec != null) query.push('at=' + sec);
        query.push('endpoints=' + encodeURIComponent(names.join(',')));
        url += '?' + query.join('&');
        try {
            const res = await fetch(url);
            if (!res.ok) throw new Error('profile ' + res.status);
            const data = await res.json();
            if (mine !== requestId) return;
            cached = { key: key, data: data };
            draw();
        } catch (err) {
            if (mine !== requestId) return;
            showNote('Profile could not be loaded.');
        }
    }

    async function load(at) {
        if (loadFlight) {
            loadQueued = true;
            return;
        }
        loadFlight = true;
        try {
            await loadNow(at);
        } finally {
            loadFlight = false;
            if (loadQueued) {
                loadQueued = false;
                const next = pendingAt;
                if (next !== at) load(next);
            }
        }
    }

    function schedule() {
        clearTimeout(timer);
        if (playing || pendingAt != null) {
            load(pendingAt);
            return;
        }
        timer = setTimeout(() => load(pendingAt), 80);
    }

    function stopPlay() {
        playing = false;
        clearInterval(playTimer);
        playTimer = 0;
        paintTransport();
    }

    function goLive() {
        stopPlay();
        liveMode = true;
        pendingAt = null;
        sliderIndex = Math.max(0, times.length - 1);
        lastLiveTs = times.length ? Number(times[times.length - 1]) : null;
        paintTransport();
        schedule();
    }

    function showSample(index) {
        if (!times.length) return;
        const idx = Math.max(0, Math.min(times.length - 1, index));
        if (idx >= times.length - 1) {
            goLive();
            return;
        }
        stopPlay();
        liveMode = false;
        sliderIndex = idx;
        pendingAt = Number(times[sliderIndex]);
        paintTransport();
        schedule();
    }

    function playStep() {
        if (times.length < 2) {
            stopPlay();
            return;
        }
        if (sliderIndex + 1 >= times.length - 1) {
            goLive();
            return;
        }
        sliderIndex += 1;
        pendingAt = Number(times[sliderIndex]);
        paintTransport();
        schedule();
    }

    function startPlay() {
        if (times.length < 2) return;
        if (liveMode || sliderIndex >= times.length - 1) sliderIndex = 0;
        liveMode = false;
        playing = true;
        pendingAt = Number(times[sliderIndex]);
        paintTransport();
        schedule();
        clearInterval(playTimer);
        const speed = Number((document.getElementById('profile-replay-speed') || {}).value) || 1;
        playTimer = setInterval(playStep, Math.max(16, Math.round(1000 / speed)));
    }

    async function refreshClock() {
        if (followSession) {
            try {
                const res = await fetch('/api/market-date');
                if (res.ok) {
                    const body = await res.json();
                    const next = typeof body === 'string' ? body : (body.date || body.market_date || '');
                    if (next && next !== date) {
                        date = next;
                        dte = readProfileDte(ticker, date);
                        dteSelect.value = dte;
                        seedPriceTickMode(ticker, date);
                        const tagged = readChartPrefs(ticker, date).profileAxisPrice;
                        if (tagged === true || tagged === false) {
                            priceTags = tagged;
                            if (priceToggle) priceToggle.checked = priceTags;
                        }
                        const nextLayers = readProfileLayers(ticker, date);
                        for (const spec of LAYER_SPECS) layersOn[spec.id] = !!nextLayers[spec.id];
                        paintLayerChecks(layersBox, layersOn);
                        times = [];
                        cached = null;
                        lastLiveTs = null;
                        pendingAt = null;
                    }
                }
            } catch (err) {}
        }
        if (!ticker || !date) return;
        const since = times.length ? String(times[times.length - 1]) : '';
        let url = '/api/chart-data/' + encodeURIComponent(ticker) + '/' + encodeURIComponent(date);
        if (since) url += '?since=' + encodeURIComponent(since);
        try {
            const res = await fetch(url);
            if (!res.ok) return;
            const data = await res.json();
            const incoming = Array.isArray(data.timestamp) ? data.timestamp : [];
            if (!times.length) times = incoming.map(Number);
            else {
                const have = new Set(times.map(Number));
                for (const stamp of incoming) {
                    const n = Number(stamp);
                    if (!have.has(n)) times.push(n);
                }
            }
            times.sort((a, b) => a - b);
            const newest = times.length ? Number(times[times.length - 1]) : null;
            if (liveMode && newest != null && newest !== lastLiveTs) {
                lastLiveTs = newest;
                sliderIndex = times.length - 1;
                pendingAt = null;
                paintTransport();
                schedule();
                return;
            }
            paintTransport();
        } catch (err) {}
    }

    function localPoint(ev) {
        const rect = canvas.getBoundingClientRect();
        return { x: ev.clientX - rect.left, y: ev.clientY - rect.top };
    }

    function currentLayers() {
        return cached && cached.data ? buildLayers(cached.data, dte, (id) => !!layersOn[id]) : [];
    }

    const tip = document.getElementById('profile-hover');
    function hideTip() {
        if (tip) tip.hidden = true;
    }
    function showTip(ev, hit) {
        if (!tip) return;
        tip.textContent = formatHit(hit);
        tip.hidden = false;
        let left = ev.clientX + 14;
        let top = ev.clientY + 16;
        const w = tip.offsetWidth;
        const h = tip.offsetHeight;
        if (left + w > window.innerWidth - 8) left = Math.max(8, ev.clientX - w - 12);
        if (top + h > window.innerHeight - 8) top = Math.max(8, ev.clientY - h - 12);
        tip.style.left = left + 'px';
        tip.style.top = top + 'px';
    }

    canvas.addEventListener('wheel', (ev) => {
        if (!lastLayout) return;
        ev.preventDefault();
        const p = localPoint(ev);
        if (pointIn(p, lastLayout.xAxis)) {
            const next = zoomXAround(xOrigin, xZoom, lastLayout.plot.x, lastLayout.plot.w, p.x, ev.deltaY < 0 ? 1.12 : 1 / 1.12, referenceMax(currentLayers(), showDots()), ladderEdge(showDots()));
            xOrigin = next.xOrigin;
            xZoom = next.xZoom;
        } else if (pointIn(p, lastLayout.priceAxis) || pointIn(p, lastLayout.plot)) {
            const anchor = yToPrice(p.y, lastLayout.range.lo, lastLayout.range.hi, lastLayout.plot.y, lastLayout.plot.h);
            const next = zoomPrice(lastLayout.range.lo, lastLayout.range.hi, anchor, ev.deltaY > 0 ? 1.12 : 1 / 1.12);
            priceLo = next.lo;
            priceHi = next.hi;
        }
        draw();
    }, { passive: false });

    canvas.addEventListener('pointerdown', (ev) => {
        if (!lastLayout) return;
        const p = localPoint(ev);
        pointer = p;
        if (pointIn(p, lastLayout.xAxis)) drag = { kind: 'x', x: p.x, origin: xOrigin };
        else if (pointIn(p, lastLayout.priceAxis) || pointIn(p, lastLayout.plot)) {
            drag = { kind: 'price', y: p.y, lo: lastLayout.range.lo, hi: lastLayout.range.hi };
        }
        try { canvas.setPointerCapture(ev.pointerId); } catch (err) {}
    });
    canvas.addEventListener('pointermove', (ev) => {
        const p = localPoint(ev);
        pointer = p;
        if (drag && lastLayout) {
            if (drag.kind === 'x') xOrigin = moveX(drag.origin, p.x - drag.x, lastLayout.plot.w);
            if (drag.kind === 'price') {
                const next = panPrice(drag.lo, drag.hi, p.y - drag.y, lastLayout.plot.h);
                priceLo = next.lo;
                priceHi = next.hi;
            }
        }
        if (lastLayout && pointIn(p, lastLayout.xAxis)) canvas.style.cursor = 'ew-resize';
        else if (lastLayout && pointIn(p, lastLayout.priceAxis)) canvas.style.cursor = 'ns-resize';
        else canvas.style.cursor = 'crosshair';
        draw();
        const hit = hitAt(p.x, p.y, hoverState.hits);
        if (hit) showTip(ev, hit);
        else hideTip();
    });
    function endDrag() {
        drag = null;
    }
    canvas.addEventListener('pointerup', endDrag);
    canvas.addEventListener('pointercancel', endDrag);
    canvas.addEventListener('pointerleave', () => {
        if (drag) return;
        pointer = null;
        hideTip();
        draw();
    });
    canvas.addEventListener('dblclick', (ev) => {
        if (!lastLayout) return;
        const p = localPoint(ev);
        if (pointIn(p, lastLayout.xAxis)) {
            xZoom = 1;
            xOrigin = 0.5;
        } else {
            priceLo = null;
            priceHi = null;
        }
        draw();
    });

    dteSelect.addEventListener('change', () => {
        dte = dteSelect.value === 'one' || dteSelect.value === 'full' ? dteSelect.value : 'zero';
        writeChartPrefs(ticker, profileDate(), { profileDte: dte });
        schedule();
    });
    layersBox.addEventListener('change', (ev) => {
        const input = ev.target;
        if (!input || !input.dataset || !input.dataset.layer) return;
        layersOn[input.dataset.layer] = !!input.checked;
        writeChartPrefs(ticker, profileDate(), { profileLayers: copyProfileLayers(layersOn) });
        schedule();
    });
    if (priceToggle) {
        priceToggle.addEventListener('change', () => {
            priceTags = !!priceToggle.checked;
            writeChartPrefs(ticker, profileDate(), { profileAxisPrice: priceTags });
            draw();
        });
    }
    const resetBtn = document.getElementById('profile-axis-reset');
    if (resetBtn) {
        resetBtn.addEventListener('click', () => {
            priceLo = null;
            priceHi = null;
            xZoom = 1;
            xOrigin = 0.5;
            draw();
        });
    }
    const replayLive = document.getElementById('profile-replay-live');
    const replayPlay = document.getElementById('profile-replay-play');
    const replayBack = document.getElementById('profile-replay-back');
    const replayForward = document.getElementById('profile-replay-forward');
    const replayRange = document.getElementById('profile-replay-range');
    const replaySpeed = document.getElementById('profile-replay-speed');
    if (replayLive) replayLive.addEventListener('click', goLive);
    if (replayPlay) replayPlay.addEventListener('click', () => { if (playing) stopPlay(); else startPlay(); });
    if (replayBack) replayBack.addEventListener('click', () => showSample(sliderIndex - 1));
    if (replayForward) replayForward.addEventListener('click', () => showSample(sliderIndex + 1));
    if (replayRange) replayRange.addEventListener('input', () => showSample(Number(replayRange.value)));
    if (replaySpeed) {
        replaySpeed.addEventListener('change', () => {
            if (!playing) return;
            clearInterval(playTimer);
            const speed = Number(replaySpeed.value) || 1;
            playTimer = setInterval(playStep, Math.max(16, Math.round(1000 / speed)));
        });
    }
    const attachBtn = document.getElementById('profile-attach-btn');
    if (attachBtn) {
        attachBtn.addEventListener('click', async () => {
            try { localStorage.setItem('profile-attach-' + ticker, '1'); } catch (err) {}
            try {
                const channel = new BroadcastChannel('market-terminal-profile');
                channel.postMessage({ type: 'attach', ticker: ticker });
                channel.close();
            } catch (err) {}
            try {
                const mod = await import('/bindings/market-terminal/app.js');
                await mod.AttachProfileToChart(ticker, profileDate());
                await mod.CloseProfileWindow(ticker);
            } catch (err) {
                showNote('Could not attach this profile to its chart.');
            }
        });
    }
    if (typeof ResizeObserver !== 'undefined') {
        new ResizeObserver(() => draw()).observe(canvas.parentElement || canvas);
    }
    window.addEventListener('storage', function (event) {
        if (!event) return;
        if (event.key === 'chartPrefs_' + (ticker || '') + '_' + (date || '')) {
            const next = readProfileLayers(ticker, date);
            let layersChanged = false;
            for (const spec of LAYER_SPECS) {
                if (!!layersOn[spec.id] !== !!next[spec.id]) layersChanged = true;
                layersOn[spec.id] = !!next[spec.id];
            }
            paintLayerChecks(layersBox, layersOn);
            const nextDte = readProfileDte(ticker, date);
            const dteChanged = nextDte !== dte;
            if (dteChanged) {
                dte = nextDte;
                dteSelect.value = dte;
            }
            seedPriceTickMode(ticker, date);
            const tagged = readChartPrefs(ticker, date).profileAxisPrice;
            if ((tagged === true || tagged === false) && tagged !== priceTags) {
                priceTags = tagged;
                if (priceToggle) priceToggle.checked = priceTags;
            }
            if (layersChanged || dteChanged) schedule();
            else draw();
            return;
        }
    });
    try {
        const settingsChannel = new BroadcastChannel('market-terminal-settings');
        settingsChannel.onmessage = (event) => {
            if (!event.data || event.data.type !== 'settings-updated' || !event.data.settings) return;
            settings = event.data.settings;
            draw();
        };
    } catch (err) {}

    const profileMenu = document.getElementById('chart-context-menu');
    function closeProfileMenu(ev) {
        if (profileMenu && ev && ev.target && profileMenu.contains(ev.target)) return;
        if (profileMenu) {
            profileMenu.style.display = 'none';
            profileMenu.removeAttribute('aria-hidden');
        }
        document.removeEventListener('click', closeProfileMenu);
    }
    function openProfileMenu(clientX, clientY) {
        if (!profileMenu) return;
        profileMenu.innerHTML = '';
        function rowToggle(item, box) {
            item.addEventListener('click', (e) => {
                if (e.target === box) return;
                box.click();
            });
        }
        function field(label, control) {
            const item = document.createElement('div');
            item.className = 'ctx-item';
            const span = document.createElement('span');
            span.textContent = label;
            control.addEventListener('mousedown', (e) => e.stopPropagation());
            control.addEventListener('click', (e) => e.stopPropagation());
            item.appendChild(span);
            item.appendChild(control);
            profileMenu.appendChild(item);
        }
        const section = document.createElement('div');
        section.className = 'ctx-section';
        section.textContent = 'Side profile';
        profileMenu.appendChild(section);
        const axisSelect = document.createElement('select');
        axisSelect.innerHTML = '<option value="off">Off</option><option value="left">Left</option><option value="right">Right</option>';
        const axisNow = resolvedAxis(ticker, date, sideSpec().side);
        axisSelect.value = axisNow === 'off' || axisNow === 'right' ? axisNow : 'left';
        axisSelect.addEventListener('change', () => {
            writeChartPrefs(ticker, date, { profileAxis: axisSelect.value });
            draw();
        });
        field('Y axis', axisSelect);
        const infoItem = document.createElement('div');
        infoItem.className = 'ctx-item';
        const infoBox = document.createElement('input');
        infoBox.type = 'checkbox';
        infoBox.checked = readChartPrefs(ticker, date).metricsVisible !== false;
        infoBox.addEventListener('click', (e) => {
            e.stopPropagation();
            writeChartPrefs(ticker, date, { metricsVisible: infoBox.checked });
            draw();
        });
        infoItem.appendChild(infoBox);
        const infoLabel = document.createElement('span');
        infoLabel.textContent = 'Information';
        infoItem.appendChild(infoLabel);
        const infoSides = document.createElement('span');
        infoSides.className = 'ctx-side-toggle';
        function infoSideButton(letter, value) {
            const button = document.createElement('button');
            button.type = 'button';
            button.textContent = letter;
            button.className = 'ctx-side-btn' + (readChartPrefs(ticker, date).metricsSide === value ? ' is-on' : '');
            button.addEventListener('click', (e) => {
                e.stopPropagation();
                writeChartPrefs(ticker, date, { metricsSide: value });
                draw();
                closeProfileMenu();
            });
            return button;
        }
        infoSides.appendChild(infoSideButton('L', 'left'));
        infoSides.appendChild(infoSideButton('R', 'right'));
        infoItem.appendChild(infoSides);
        infoItem.addEventListener('click', (e) => {
            if (e.target === infoBox || (e.target && e.target.closest && e.target.closest('button'))) return;
            infoBox.click();
        });
        profileMenu.appendChild(infoItem);
        const levelParent = document.createElement('div');
        levelParent.className = 'ctx-item ctx-parent';
        levelParent.innerHTML = '<span>Levels</span><span class="ctx-arrow">▶</span>';
        const levelMenu = document.createElement('div');
        levelMenu.className = 'ctx-submenu';
        const textItem = document.createElement('div');
        textItem.className = 'ctx-item ctx-item-indent';
        const textSpan = document.createElement('span');
        textSpan.textContent = 'Text';
        const textRange = document.createElement('input');
        textRange.type = 'range';
        textRange.min = '0';
        textRange.max = '100';
        textRange.value = String(levelLabelPercent(readChartPrefs(ticker, date)));
        textRange.addEventListener('mousedown', (e) => e.stopPropagation());
        textRange.addEventListener('click', (e) => e.stopPropagation());
        textRange.addEventListener('input', (e) => {
            e.stopPropagation();
            writeChartPrefs(ticker, date, { levelLabelX: levelLabelPercent({ levelLabelX: Number(textRange.value) }) });
            draw();
        });
        textItem.appendChild(textSpan);
        textItem.appendChild(textRange);
        levelMenu.appendChild(textItem);
        PROFILE_LEVELS.forEach((level) => {
            const item = document.createElement('div');
            item.className = 'ctx-item ctx-item-indent';
            const box = document.createElement('input');
            box.type = 'checkbox';
            box.checked = levelPrefs(readChartPrefs(ticker, date))[level.id] !== false;
            box.addEventListener('click', (e) => {
                e.stopPropagation();
                const bag = levelPrefs(readChartPrefs(ticker, date));
                bag[level.id] = box.checked;
                writeChartPrefs(ticker, date, { profileLevels: bag });
                draw();
            });
            item.appendChild(box);
            const span = document.createElement('span');
            span.textContent = level.label;
            item.appendChild(span);
            rowToggle(item, box);
            levelMenu.appendChild(item);
        });
        levelParent.addEventListener('click', (e) => {
            e.stopPropagation();
            levelMenu.classList.toggle('expanded');
            levelParent.querySelector('.ctx-arrow').textContent = levelMenu.classList.contains('expanded') ? '▼' : '▶';
        });
        profileMenu.appendChild(levelParent);
        profileMenu.appendChild(levelMenu);
        const tickSelect = document.createElement('select');
        tickSelect.innerHTML = '<option value="aligned">5 / 10</option><option value="true">Strikes</option>';
        tickSelect.value = priceTickMode();
        tickSelect.addEventListener('change', () => {
            setPriceTickMode(tickSelect.value);
            writeChartPrefs(ticker, date, { priceTickMode: priceTickMode() });
            draw();
        });
        field('Prices', tickSelect);
        const priceItem = document.createElement('div');
        priceItem.className = 'ctx-item';
        const priceBox = document.createElement('input');
        priceBox.type = 'checkbox';
        priceBox.checked = priceTags;
        priceBox.addEventListener('click', (e) => {
            e.stopPropagation();
            priceTags = priceBox.checked;
            if (priceToggle) priceToggle.checked = priceTags;
            writeChartPrefs(ticker, date, { profileAxisPrice: priceTags });
            draw();
        });
        priceItem.appendChild(priceBox);
        const priceLabel = document.createElement('span');
        priceLabel.textContent = 'Price labels';
        priceItem.appendChild(priceLabel);
        rowToggle(priceItem, priceBox);
        profileMenu.appendChild(priceItem);
        const dotsItem = document.createElement('div');
        dotsItem.className = 'ctx-item';
        const dotsBox = document.createElement('input');
        dotsBox.type = 'checkbox';
        dotsBox.checked = showDots();
        dotsBox.addEventListener('click', (e) => {
            e.stopPropagation();
            writeChartPrefs(ticker, date, { showPriors: dotsBox.checked });
            draw();
        });
        dotsItem.appendChild(dotsBox);
        const dotsLabel = document.createElement('span');
        dotsLabel.textContent = 'Lookback dots';
        dotsItem.appendChild(dotsLabel);
        rowToggle(dotsItem, dotsBox);
        profileMenu.appendChild(dotsItem);
        const resetItem = document.createElement('div');
        resetItem.className = 'ctx-item';
        resetItem.textContent = 'Reset axes';
        resetItem.addEventListener('click', () => {
            priceLo = null;
            priceHi = null;
            xZoom = 1;
            xOrigin = 0.5;
            closeProfileMenu();
            draw();
        });
        profileMenu.appendChild(resetItem);
        profileMenu.style.display = 'block';
        profileMenu.style.left = clientX + 'px';
        profileMenu.style.top = clientY + 'px';
        profileMenu.setAttribute('aria-hidden', 'false');
        requestAnimationFrame(() => document.addEventListener('click', closeProfileMenu));
    }
    const stage = document.getElementById('profile-stage');
    if (stage) {
        stage.addEventListener('contextmenu', (e) => {
            e.preventDefault();
            openProfileMenu(e.clientX, e.clientY);
        });
    }

    (async function start() {
        if (!date) {
            try {
                const res = await fetch('/api/market-date');
                if (res.ok) {
                    const body = await res.json();
                    date = typeof body === 'string' ? body : (body.date || body.market_date || '');
                }
            } catch (err) {}
        }
        try {
            const res = await fetch('/api/settings');
            if (res.ok) settings = await res.json();
        } catch (err) {}
        await refreshClock();
        schedule();
        setInterval(refreshClock, 2000);
    })();
}

function noteForStandalone(data, layers, layerOn, dte) {
    if (data.legacy) {
        const bits = [];
        if (layers.some((layer) => layer.id === 'classic')) bits.push(SAVED_LADDER);
        if (layers.some((layer) => layer.savedGreek)) bits.push('The saved greek row is included. This file does not split it into 0 DTE and 1 DTE.');
        if (layerOn('state')) bits.push('State was not stored as its own ladder in this file.');
        return bits.join(' ');
    }
    if (!layers.length) {
        const greekNote = fullGreekNote(dte, layerOn);
        if (greekNote) return greekNote;
        return 'No ladder in this sample.';
    }
    const fallen = layers.filter((layer) => layer.fallback).map((layer) => layer.label);
    if (!fallen.length) return '';
    return joinLabels(fallen) + ' · 0 DTE (not in the 90 DTE book)';
}
