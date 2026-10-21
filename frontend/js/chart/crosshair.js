import { logToBackend } from '../shared/logging.js';
import { priceScaleTag } from '../shared/series.js';
import { getMarketDate } from './data.js';
import { PROFILE_LEVELS } from './profile.js';

export function installCrosshair(live) {
// Update crosshair info box from Lightweight Charts crosshair event
function updateCrosshairFromParam(param) {
    if (!live.crosshairLine || !live.crosshairInfoBox || !live.crosshairInfoContent) return;
    if (!param || param.time === undefined) {
        live.crosshairLine.style.display = 'none';
        if (live.crosshairXMarker) live.crosshairXMarker.style.display = 'none';
        if (live.crosshairPulseDots) { live.crosshairPulseDots.style.display = 'none'; live.crosshairPulseDots.innerHTML = ''; }
        return;
    }
    const timestamp = live.lwTimeToDate(param.time);
    const timeStr = live.formatChartTime(timestamp);
    const tzIndicatorX = live.globalChartSettings.UseMarketTime ? ' ET' : '';
    const chartDivEl = document.getElementById('chart-div');
    if (chartDivEl && param.point) {
        const chartW = chartDivEl.clientWidth || 0;
        const chartH = chartDivEl.clientHeight || 0;
        const x = param.point.x;
        if (x < 0 || x > chartW) {
            live.crosshairLine.style.display = 'none';
            if (live.crosshairXMarker) live.crosshairXMarker.style.display = 'none';
            if (live.crosshairPulseDots) { live.crosshairPulseDots.style.display = 'none'; live.crosshairPulseDots.innerHTML = ''; }
        } else {
        live.crosshairLine.style.display = 'block';
        live.crosshairLine.style.left = x + 'px';
        if (live.crosshairXMarker) {
            live.crosshairXMarker.style.left = x + 'px';
            live.crosshairXMarker.style.display = 'flex';
            live.crosshairXMarker.setAttribute('aria-hidden', 'false');
            const xLabel = document.getElementById('crosshair-x-marker-label');
            if (xLabel) xLabel.textContent = timeStr + tzIndicatorX;
        }
        if (live.crosshairPulseDots && live.chart && param.seriesData && param.seriesData.size > 0) {
            live.crosshairPulseDots.innerHTML = '';
            live.crosshairPulseDots.style.display = 'block';
            const inset = 6;
            const yMin = inset;
            const yMax = chartH - inset;
            param.seriesData.forEach((value, series) => {
                const entry = live.lwSeriesList.find(e => e.series === series);
                if (!entry) return;
                const price = (value && typeof value === 'object' && 'value' in value) ? Number((value).value) : Number(value);
                if (typeof price !== 'number' || isNaN(price) || !isFinite(price)) return;
                let y = null;
                try { y = series.priceToCoordinate(price); } catch (e) { return; }
                if (y == null || typeof y !== 'number') return;
                y = Math.max(yMin, Math.min(yMax, y));
                const dot = document.createElement('div');
                dot.className = 'crosshair-pulse-dot';
                dot.style.left = x + 'px';
                dot.style.top = y + 'px';
                dot.style.background = entry.color || 'rgba(128, 128, 128, 0.8)';
                live.crosshairPulseDots.appendChild(dot);
            });
        } else if (live.crosshairPulseDots) {
            live.crosshairPulseDots.style.display = 'none';
            live.crosshairPulseDots.innerHTML = '';
        }
        }
    }
    const valueByEndpoint = new Map();
    if (param.seriesData && param.seriesData.size) {
        param.seriesData.forEach((value, series) => {
            const entry = live.lwSeriesList.find(e => e.series === series);
            if (entry && value !== undefined && value !== null && typeof value === 'object' && 'value' in value) {
                const num = Number((value).value);
                if (typeof num === 'number' && !isNaN(num) && isFinite(num) && num !== 0) {
                    valueByEndpoint.set(entry.endpoint, num);
                }
            }
        });
    }
    const headerTitle = document.getElementById('crosshair-info-box-header-title');
    if (headerTitle) headerTitle.textContent = `Time: ${timeStr}${tzIndicatorX}`;
    let contentHTML = '';
    live.lwSeriesList.forEach((entry) => {
        const val = valueByEndpoint.get(entry.endpoint) ?? live.lastValueByEndpoint[entry.endpoint];
        const hidden = live.currentChartPrefs.hiddenSeries.includes(entry.endpoint);
        const rowClass = 'crosshair-data-row crosshair-series-row' + (hidden ? ' series-hidden' : '');
        const displayVal = (val !== undefined && val !== null && typeof val === 'number')
            ? val.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
            : (hidden ? '(hidden)' : '—');
        contentHTML += `
            <div class="${rowClass}" data-endpoint="${entry.endpoint}" title="Click to show/hide on live.chart">
                <span class="crosshair-data-label">
                    <span class="crosshair-color-indicator" style="background-color: ${entry.color || '#888'};"></span>
                    ${entry.label}:
                </span>
                <span class="crosshair-data-value">${displayVal}</span>
            </div>
        `;
    });
    live.crosshairInfoContent.innerHTML = contentHTML || '<div style="color: #888;">No data at this time</div>';
    if (live.lwSeriesList.length > 0) live.crosshairInfoBox.style.display = 'block';
    if (live.crosshairInfoBox.classList.contains('collapsed')) {
        live.crosshairInfoContent.classList.add('content-hidden');
        live.crosshairInfoContent.style.display = 'none';
    }
    requestAnimationFrame(() => clampInfoBoxMinHeight());
    if (typeof live.syncProfile === 'function') live.syncProfile(timestamp.getTime() / 1000);
}

function clampInfoBoxMinHeight() {
    if (!live.crosshairInfoBox || !live.crosshairInfoContent) return;
    const header = document.getElementById('crosshair-info-box-header');
    const headerH = header ? header.offsetHeight : 0;
    const padding = 2 * parseFloat(getComputedStyle(live.crosshairInfoBox).paddingTop) || 0;
    if (live.crosshairInfoBox.classList.contains('collapsed')) {
        live.crosshairInfoBox.style.minHeight = (headerH + padding + 4) + 'px';
        return;
    }
    const contentH = live.crosshairInfoContent.scrollHeight;
    const buffer = 10;
    const minH = Math.max(100, headerH + contentH + padding + buffer);
    live.crosshairInfoBox.style.minHeight = minH + 'px';
    if (live.infoBoxAspectRatio == null && live.crosshairInfoBox.offsetWidth > 0) {
        live.infoBoxAspectRatio = live.crosshairInfoBox.offsetHeight / live.crosshairInfoBox.offsetWidth;
    }
}

function updateCrosshairToLatest() {
    if (!live.chart || !live.crosshairInfoBox || !live.crosshairInfoContent) return;
    if (live.lwSeriesList.length === 0) return;
    let latestTime = null;
    for (const entry of live.lwSeriesList) {
        if (!entry.lastTime) continue;
        const t = entry.lastTime instanceof Date ? entry.lastTime.getTime() : entry.lastTime;
        if (latestTime === null || t > latestTime) latestTime = t;
    }
    if (latestTime === null) return;
    const timestamp = new Date(latestTime);
    const timeStr = live.formatChartTime(timestamp);
    const tzIndicator = live.globalChartSettings.UseMarketTime ? ' ET' : '';
    const headerTitle = document.getElementById('crosshair-info-box-header-title');
    if (headerTitle) headerTitle.textContent = `Latest: ${timeStr}${tzIndicator}`;
    let contentHTML = '';
    live.lwSeriesList.forEach((entry) => {
        const v = live.lastValueByEndpoint[entry.endpoint];
        const hidden = live.currentChartPrefs.hiddenSeries.includes(entry.endpoint);
        const rowClass = 'crosshair-data-row crosshair-series-row' + (hidden ? ' series-hidden' : '');
        const displayVal = (v !== undefined && v !== null && typeof v === 'number')
            ? v.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
            : (hidden ? '(hidden)' : '—');
        contentHTML += `
            <div class="${rowClass}" data-endpoint="${entry.endpoint}" title="Click to show/hide on live.chart">
                <span class="crosshair-data-label">
                    <span class="crosshair-color-indicator" style="background-color: ${entry.color || '#888'};"></span>
                    ${entry.label}:
                </span>
                <span class="crosshair-data-value">${displayVal}</span>
            </div>
        `;
    });
    live.crosshairInfoContent.innerHTML = contentHTML || '<div style="color: #888;">No data available</div>';
    live.crosshairInfoBox.style.display = 'block';
    if (live.crosshairInfoBox.classList.contains('collapsed')) {
        live.crosshairInfoContent.classList.add('content-hidden');
        live.crosshairInfoContent.style.display = 'none';
    }
    if (live.crosshairLine) live.crosshairLine.style.display = 'none';
    if (live.crosshairXMarker) live.crosshairXMarker.style.display = 'none';
    if (live.crosshairPulseDots) { live.crosshairPulseDots.style.display = 'none'; live.crosshairPulseDots.innerHTML = ''; }
    requestAnimationFrame(() => clampInfoBoxMinHeight());
    if (typeof live.syncProfile === 'function') live.syncProfile(timestamp.getTime() / 1000);
}

live.chart.subscribeCrosshairMove(updateCrosshairFromParam);

if (live.crosshairInfoContent) {
    live.crosshairInfoContent.addEventListener('click', (e) => {
        const row = e.target && e.target.closest ? e.target.closest('.crosshair-series-row[data-endpoint]') : null;
        if (!row) return;
        const endpoint = row.getAttribute('data-endpoint');
        if (!endpoint || !live.lwSeriesByEndpoint[endpoint]) return;
        const idx = live.currentChartPrefs.hiddenSeries.indexOf(endpoint);
        const nowHidden = idx >= 0;
        if (nowHidden) {
            live.currentChartPrefs.hiddenSeries.splice(idx, 1);
        } else {
            live.currentChartPrefs.hiddenSeries.push(endpoint);
        }
        live.saveChartPrefs(live.ticker, live.currentChartDateStr, live.currentChartPrefs);
        live.applySeriesAppearance(endpoint, { visible: nowHidden });
    });
}

const chartContainerEl = document.getElementById('chart-container');
if (chartContainerEl) {
    chartContainerEl.addEventListener('mouseenter', () => { live.isMouseOverChart = true; });
    chartContainerEl.addEventListener('mouseleave', () => {
        live.isMouseOverChart = false;
        updateCrosshairToLatest();
    });
}

const chartContextMenu = document.getElementById('chart-context-menu');
    function closeChartContextMenu(ev) {
        if (chartContextMenu && ev && ev.target && chartContextMenu.contains(ev.target)) return;
        if (chartContextMenu) {
            chartContextMenu.style.display = 'none';
            chartContextMenu.removeAttribute('aria-hidden');
        }
        document.removeEventListener('click', closeChartContextMenuHandler);
    }
    function closeChartContextMenuHandler(ev) {
        closeChartContextMenu(ev);
    }
function openChartContextMenu(clientX, clientY, view) {
    if (!chartContextMenu) return;
    const onSide = view === 'side';
    (async function () {
        const dateStr = live.currentChartDateStr || await getMarketDate(live.dateFromURL);
        chartContextMenu.innerHTML = '';
        function clickRowToggles(item, box) {
            item.addEventListener('click', (e) => {
                if (e.target === box) return;
                if (e.target && e.target.closest && e.target.closest('button')) return;
                box.click();
            });
        }
        const prefsParent = document.createElement('div');
        prefsParent.className = 'ctx-item ctx-parent';
        prefsParent.innerHTML = '<span>Preferences</span><span class="ctx-arrow">▶</span>';
        const submenu = document.createElement('div');
        submenu.className = 'ctx-submenu';
        function clampContextMenuToViewport() {
            if (!chartContextMenu || chartContextMenu.style.display !== 'block') return;
            const pad = 8;
            const w = chartContextMenu.offsetWidth;
            const h = chartContextMenu.offsetHeight;
            const vw = window.innerWidth;
            const vh = window.innerHeight;
            let left = parseInt(chartContextMenu.style.left, 10);
            let top = parseInt(chartContextMenu.style.top, 10);
            if (isNaN(left)) left = 0;
            if (isNaN(top)) top = 0;
            if (left + w > vw - pad) left = Math.max(pad, vw - w - pad);
            if (left < pad) left = pad;
            if (top + h > vh - pad) top = Math.max(pad, vh - h - pad);
            if (top < pad) top = pad;
            chartContextMenu.style.left = left + 'px';
            chartContextMenu.style.top = top + 'px';
        }
        prefsParent.addEventListener('click', (e) => {
            e.stopPropagation();
            submenu.classList.toggle('expanded');
            prefsParent.querySelector('.ctx-arrow').textContent = submenu.classList.contains('expanded') ? '▼' : '▶';
            if (submenu.classList.contains('expanded')) {
                requestAnimationFrame(() => clampContextMenuToViewport());
            }
        });
        if (!onSide) {
            chartContextMenu.appendChild(prefsParent);
            chartContextMenu.appendChild(submenu);
        }
        const resetItem = document.createElement('div');
        resetItem.className = 'ctx-item';
        resetItem.textContent = 'Reset view';
        resetItem.title = 'Reset zoom and price scale to default';
        resetItem.addEventListener('click', async (e) => {
            e.stopPropagation();
            closeChartContextMenu(e);
            try {
                if (typeof live.fitSessionInset === 'function') {
                    live.fitSessionInset();
                } else {
                    try { live.chart.timeScale().fitContent(); live.setPriceScaleFromSpotOnly(); } catch (err) {}
                }
                live.userHasZoomed = false;
                live.autoPrice = 'spot';
                live.savedZoomState = null;
                await logToBackend('info', '[Chart] Reset view from context menu').catch(() => {});
            } catch (error) {
                console.error('[Chart] Error resetting view:', error);
                await logToBackend('error', `[Chart] Error resetting view: ${error.message || error}`).catch(() => {});
            }
        });
        if (!onSide) chartContextMenu.appendChild(resetItem);
        const autoFollowItem = document.createElement('div');
        autoFollowItem.className = 'ctx-item';
        const autoFollowCb = document.createElement('input');
        autoFollowCb.type = 'checkbox';
        autoFollowCb.checked = live.autoFollowEnabled;
        autoFollowCb.id = 'ctx-auto-follow';
        autoFollowCb.addEventListener('change', (e) => { e.stopPropagation(); });
        autoFollowCb.addEventListener('click', (e) => {
            e.stopPropagation();
            live.autoFollowEnabled = !live.autoFollowEnabled;
            const btn = document.getElementById('auto-follow-btn');
            if (btn) {
                btn.textContent = `Auto-Follow: ${live.autoFollowEnabled ? 'ON' : 'OFF'}`;
                btn.classList.toggle('active', live.autoFollowEnabled);
            }
            logToBackend('info', `[Chart] Auto-follow ${live.autoFollowEnabled ? 'enabled' : 'disabled'}`).catch(() => {});
        });
        autoFollowItem.appendChild(autoFollowCb);
        const autoFollowLabel = document.createElement('span');
        autoFollowLabel.textContent = 'Auto follow';
        autoFollowItem.appendChild(autoFollowLabel);
        clickRowToggles(autoFollowItem, autoFollowCb);
        if (!onSide) chartContextMenu.appendChild(autoFollowItem);
        const profileShown = typeof live.profileShown === 'function' ? live.profileShown() : true;
        const dotsItem = document.createElement('div');
        dotsItem.className = 'ctx-item';
        const dotsCb = document.createElement('input');
        dotsCb.type = 'checkbox';
        dotsCb.checked = typeof live.profileDotsOn === 'function' ? live.profileDotsOn() : true;
        dotsCb.id = 'ctx-profile-dots';
        dotsCb.addEventListener('change', (e) => { e.stopPropagation(); });
        dotsCb.addEventListener('click', (e) => {
            e.stopPropagation();
            if (typeof live.setProfileDots === 'function') live.setProfileDots(dotsCb.checked);
        });
        dotsItem.appendChild(dotsCb);
        const dotsLabel = document.createElement('span');
        dotsLabel.textContent = 'Lookback dots';
        dotsItem.appendChild(dotsLabel);
        clickRowToggles(dotsItem, dotsCb);
        if (profileShown) chartContextMenu.appendChild(dotsItem);
        const infoItem = document.createElement('div');
        infoItem.className = 'ctx-item';
        const infoCb = document.createElement('input');
        infoCb.type = 'checkbox';
        infoCb.checked = typeof live.metricsVisible === 'function' ? live.metricsVisible() : true;
        infoCb.id = 'ctx-profile-info';
        infoCb.addEventListener('change', (e) => { e.stopPropagation(); });
        infoCb.addEventListener('click', (e) => {
            e.stopPropagation();
            if (typeof live.setMetricsVisible === 'function') live.setMetricsVisible(infoCb.checked);
        });
        infoItem.appendChild(infoCb);
        const infoLabel = document.createElement('span');
        infoLabel.textContent = 'Information';
        infoItem.appendChild(infoLabel);
        const infoSides = document.createElement('span');
        infoSides.className = 'ctx-side-toggle';
        function infoSideButton(letter, value) {
            const button = document.createElement('button');
            button.type = 'button';
            button.textContent = letter;
            const current = typeof live.metricsSide === 'function' ? live.metricsSide() : 'right';
            button.className = 'ctx-side-btn' + (current === value ? ' is-on' : '');
            button.addEventListener('click', (e) => {
                e.stopPropagation();
                if (typeof live.setMetricsSide === 'function') live.setMetricsSide(value);
                infoSides.querySelectorAll('button').forEach((node) => {
                    node.classList.toggle('is-on', node === button);
                });
            });
            return button;
        }
        infoSides.appendChild(infoSideButton('L', 'left'));
        infoSides.appendChild(infoSideButton('R', 'right'));
        infoItem.appendChild(infoSides);
        clickRowToggles(infoItem, infoCb);
        if (profileShown) chartContextMenu.appendChild(infoItem);

        const alertParent = document.createElement('div');
        alertParent.className = 'ctx-item ctx-parent';
        alertParent.innerHTML = '<span>Alerts</span><span class="ctx-arrow">▶</span>';
        const alertMenu = document.createElement('div');
        alertMenu.className = 'ctx-submenu';
        [
            ['call', 'Major call'],
            ['put', 'Major put'],
            ['long', 'Major long'],
            ['short', 'Major short'],
        ].forEach((pair) => {
            const item = document.createElement('div');
            item.className = 'ctx-item ctx-item-indent';
            const box = document.createElement('input');
            box.type = 'checkbox';
            box.checked = typeof live.alertOn === 'function' ? live.alertOn(pair[0]) : false;
            box.addEventListener('click', (e) => {
                e.stopPropagation();
                if (typeof live.setAlert === 'function') live.setAlert(pair[0], box.checked);
            });
            item.appendChild(box);
            const span = document.createElement('span');
            span.textContent = pair[1];
            item.appendChild(span);
            clickRowToggles(item, box);
            alertMenu.appendChild(item);
        });
        alertParent.addEventListener('click', (e) => {
            e.stopPropagation();
            alertMenu.classList.toggle('expanded');
            alertParent.querySelector('.ctx-arrow').textContent = alertMenu.classList.contains('expanded') ? '▼' : '▶';
            if (alertMenu.classList.contains('expanded')) requestAnimationFrame(() => clampContextMenuToViewport());
        });
        chartContextMenu.appendChild(alertParent);
        chartContextMenu.appendChild(alertMenu);

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
        textRange.value = String(typeof live.levelLabelX === 'function' ? live.levelLabelX() : 0);
        textRange.addEventListener('mousedown', (e) => e.stopPropagation());
        textRange.addEventListener('click', (e) => e.stopPropagation());
        textRange.addEventListener('input', (e) => {
            e.stopPropagation();
            if (typeof live.setLevelLabel === 'function') live.setLevelLabel(Number(textRange.value));
        });
        textItem.appendChild(textSpan);
        textItem.appendChild(textRange);
        levelMenu.appendChild(textItem);
        PROFILE_LEVELS.forEach((level) => {
            const item = document.createElement('div');
            item.className = 'ctx-item ctx-item-indent';
            const box = document.createElement('input');
            box.type = 'checkbox';
            box.checked = typeof live.profileLevelOn === 'function' ? live.profileLevelOn(level.id) : true;
            box.addEventListener('click', (e) => {
                e.stopPropagation();
                if (typeof live.setProfileLevel === 'function') live.setProfileLevel(level.id, box.checked);
            });
            item.appendChild(box);
            const span = document.createElement('span');
            span.textContent = level.label;
            item.appendChild(span);
            clickRowToggles(item, box);
            levelMenu.appendChild(item);
        });
        levelParent.addEventListener('click', (e) => {
            e.stopPropagation();
            levelMenu.classList.toggle('expanded');
            levelParent.querySelector('.ctx-arrow').textContent = levelMenu.classList.contains('expanded') ? '▼' : '▶';
            if (levelMenu.classList.contains('expanded')) requestAnimationFrame(() => clampContextMenuToViewport());
        });
        if (profileShown) {
            chartContextMenu.appendChild(levelParent);
            chartContextMenu.appendChild(levelMenu);
        }

        const layout = (live.globalChartSettings && live.globalChartSettings.ProfileSettings) || {};
        function layoutControl(labelText, control) {
            const item = document.createElement('div');
            item.className = 'ctx-item';
            const span = document.createElement('span');
            span.textContent = labelText;
            control.addEventListener('mousedown', (e) => e.stopPropagation());
            control.addEventListener('click', (e) => e.stopPropagation());
            item.appendChild(span);
            item.appendChild(control);
            chartContextMenu.appendChild(item);
            return item;
        }
        function saveLayout(partial) {
            if (typeof live.saveProfileLayout === 'function') live.saveProfileLayout(partial);
        }
        if (onSide) {
            const layoutSection = document.createElement('div');
            layoutSection.className = 'ctx-section';
            layoutSection.textContent = 'Side profile';
            chartContextMenu.appendChild(layoutSection);
            const dockItem = document.createElement('div');
            dockItem.className = 'ctx-item';
            const docked = typeof live.profileDocked === 'function' ? live.profileDocked() : true;
            dockItem.textContent = docked ? 'Detach side profile' : 'Attach side profile';
            dockItem.addEventListener('click', (e) => {
                e.stopPropagation();
                closeChartContextMenu(e);
                if (docked) {
                    if (typeof live.detachProfile === 'function') live.detachProfile();
                } else if (typeof live.attachProfile === 'function') {
                    live.attachProfile();
                }
            });
            chartContextMenu.appendChild(dockItem);
            const sideSelect = document.createElement('select');
            sideSelect.innerHTML = '<option value="right">Right</option><option value="left">Left</option>';
            sideSelect.value = layout.profile_side === 'left' ? 'left' : 'right';
            sideSelect.addEventListener('change', () => saveLayout({ profile_side: sideSelect.value === 'left' ? 'left' : 'right' }));
            layoutControl('Side', sideSelect);
            const tickSelect = document.createElement('select');
            tickSelect.innerHTML = '<option value="aligned">5 / 10</option><option value="true">Strikes</option>';
            tickSelect.value = typeof live.priceTickMode === 'function' && live.priceTickMode() === 'true' ? 'true' : 'aligned';
            tickSelect.addEventListener('change', () => {
                if (typeof live.setPriceTickMode === 'function') live.setPriceTickMode(tickSelect.value);
            });
            layoutControl('Prices', tickSelect);
            const axisSelect = document.createElement('select');
            axisSelect.innerHTML = '<option value="off">Off</option><option value="left">Left</option><option value="right">Right</option>';
            const axisNow = typeof live.profileAxis === 'function' ? live.profileAxis() : 'left';
            axisSelect.value = axisNow === 'off' || axisNow === 'right' ? axisNow : 'left';
            axisSelect.addEventListener('change', () => {
                if (typeof live.setProfileAxis === 'function') live.setProfileAxis(axisSelect.value);
            });
            layoutControl('Y axis', axisSelect);
        }

        const historySection = document.createElement('div');
        historySection.className = 'ctx-section';
        historySection.textContent = 'Level history';
        chartContextMenu.appendChild(historySection);
        const historySelect = document.createElement('select');
        historySelect.innerHTML = '<option value="average">Average</option><option value="scatter">Scatter</option>';
        historySelect.value = typeof live.levelHistoryStyle === 'function' && live.levelHistoryStyle() === 'scatter' ? 'scatter' : 'average';
        historySelect.addEventListener('change', () => {
            if (typeof live.setLevelHistoryStyle === 'function') live.setLevelHistoryStyle(historySelect.value);
        });
        layoutControl('Average or Scatter', historySelect);

        if (!onSide && profileShown) {
            const ladderSection = document.createElement('div');
            ladderSection.className = 'ctx-section';
            ladderSection.textContent = 'Ladder';
            chartContextMenu.appendChild(ladderSection);
            const alignSelect = document.createElement('select');
            alignSelect.innerHTML = '<option value="left">Left</option><option value="center">Center</option><option value="right">Right</option>';
            const alignNow = typeof live.ladderAlign === 'function' ? live.ladderAlign() : 'center';
            alignSelect.value = alignNow === 'left' || alignNow === 'right' ? alignNow : 'center';
            alignSelect.addEventListener('change', () => {
                if (typeof live.setLadderAlign === 'function') live.setLadderAlign(alignSelect.value);
            });
            layoutControl('Align', alignSelect);
            const widthInput = document.createElement('input');
            widthInput.type = 'number';
            widthInput.min = '8';
            widthInput.max = '70';
            widthInput.step = '1';
            const storedWidth = Number(layout.bar_width_percent);
            widthInput.value = String(Number.isFinite(storedWidth) && storedWidth > 0 ? Math.round(storedWidth) : 22);
            widthInput.addEventListener('change', () => {
                let value = Number(widthInput.value);
                if (!Number.isFinite(value)) value = 22;
                value = Math.max(8, Math.min(70, Math.round(value)));
                widthInput.value = String(value);
                saveLayout({ bar_width_percent: value });
            });
            const widthItem = layoutControl('Width %', widthInput);
            const centerInput = document.createElement('input');
            centerInput.type = 'number';
            centerInput.min = '0';
            centerInput.max = '100';
            centerInput.step = '1';
            const storedCenter = Number(layout.profile_x_percent);
            centerInput.value = String(Number.isFinite(storedCenter) ? Math.round(storedCenter) : 62);
            centerInput.addEventListener('change', () => {
                let value = Number(centerInput.value);
                if (!Number.isFinite(value)) value = 62;
                value = Math.max(0, Math.min(100, Math.round(value)));
                centerInput.value = String(value);
                saveLayout({ profile_x_percent: value });
            });
            const centerItem = layoutControl('Center %', centerInput);
            function syncCenterControls() {
                const show = alignSelect.value === 'center';
                widthItem.style.display = show ? '' : 'none';
                centerItem.style.display = show ? '' : 'none';
            }
            syncCenterControls();
            alignSelect.addEventListener('change', syncCenterControls);
            const orderSelect = document.createElement('select');
            orderSelect.innerHTML = '<option value="over">Over plots</option><option value="under">Under plots</option>';
            orderSelect.value = typeof live.profileOver === 'function' && live.profileOver() === false ? 'under' : 'over';
            orderSelect.addEventListener('change', () => {
                if (typeof live.setProfileOver === 'function') live.setProfileOver(orderSelect.value !== 'under');
            });
            layoutControl('Draw', orderSelect);
        }

        if (!onSide && live.lwSeriesList.length > 0) {
            const section1 = document.createElement('div');
            section1.className = 'ctx-section';
            section1.textContent = 'Series name (text left of value on scale)';
            submenu.appendChild(section1);
            const allShown = live.lwSeriesList.every((entry) => live.currentChartPrefs.seriesTitleVisible[entry.endpoint] !== false);
            const allItem = document.createElement('div');
            allItem.className = 'ctx-item';
            const allCb = document.createElement('input');
            allCb.type = 'checkbox';
            allCb.checked = allShown;
            allCb.id = 'ctx-title-all';
            allCb.addEventListener('change', (e) => { e.stopPropagation(); });
            allCb.addEventListener('click', (e) => {
                e.stopPropagation();
                const showAll = allCb.checked;
                live.lwSeriesList.forEach((entry) => {
                    live.currentChartPrefs.seriesTitleVisible[entry.endpoint] = showAll;
                    live.applySeriesAppearance(entry.endpoint, { title: showAll ? priceScaleTag(entry.label) : '' });
                });
                live.saveChartPrefs(live.ticker, live.currentChartDateStr || dateStr, live.currentChartPrefs);
                live.lwSeriesList.forEach((entry) => {
                    const subCb = document.getElementById('ctx-title-' + entry.endpoint);
                    if (subCb) subCb.checked = showAll;
                });
            });
            allItem.appendChild(allCb);
            const allLabel = document.createElement('span');
            allLabel.textContent = 'Show all';
            allItem.appendChild(allLabel);
            clickRowToggles(allItem, allCb);
            submenu.appendChild(allItem);
            live.lwSeriesList.forEach((entry) => {
                const show = live.currentChartPrefs.seriesTitleVisible[entry.endpoint] !== false;
                const item = document.createElement('div');
                item.className = 'ctx-item ctx-item-indent';
                const cb = document.createElement('input');
                cb.type = 'checkbox';
                cb.checked = show;
                cb.id = 'ctx-title-' + entry.endpoint;
                cb.addEventListener('change', (e) => { e.stopPropagation(); });
                cb.addEventListener('click', () => {
                    live.currentChartPrefs.seriesTitleVisible[entry.endpoint] = cb.checked;
                    live.saveChartPrefs(live.ticker, live.currentChartDateStr || dateStr, live.currentChartPrefs);
                    live.applySeriesAppearance(entry.endpoint, { title: cb.checked ? priceScaleTag(entry.label) : '' });
                    const anyShown = live.lwSeriesList.some((e) => live.currentChartPrefs.seriesTitleVisible[e.endpoint] !== false);
                    if (allCb) allCb.checked = anyShown;
                });
                item.appendChild(cb);
                const span = document.createElement('span');
                span.style.display = 'flex';
                span.style.alignItems = 'center';
                span.style.gap = '6px';
                span.innerHTML = `<span class="crosshair-color-indicator" style="background-color:${entry.color || '#888'};width:8px;height:8px;"></span> ${entry.label}`;
                item.appendChild(span);
                clickRowToggles(item, cb);
                submenu.appendChild(item);
            });
            const section2 = document.createElement('div');
            section2.className = 'ctx-section';
            section2.textContent = 'Labels follow crosshair';
            submenu.appendChild(section2);
            const followItem = document.createElement('div');
            followItem.className = 'ctx-item';
            const followCb = document.createElement('input');
            followCb.type = 'checkbox';
            followCb.checked = live.currentChartPrefs.priceLabelsFollowCrosshair;
            followCb.id = 'ctx-follow-crosshair';
            followCb.addEventListener('change', (e) => { e.stopPropagation(); });
            followCb.addEventListener('click', () => {
                live.currentChartPrefs.priceLabelsFollowCrosshair = followCb.checked;
                live.saveChartPrefs(live.ticker, live.currentChartDateStr || dateStr, live.currentChartPrefs);
                try {
                    live.chart.applyOptions({ priceScaleLabelsSource: followCb.checked ? 'crosshair' : 'lastVisible' });
                } catch (e) {}
            });
            followItem.appendChild(followCb);
            const followLabel = document.createElement('span');
            followLabel.textContent = 'Show value at crosshair (else newest)';
            followItem.appendChild(followLabel);
            clickRowToggles(followItem, followCb);
            submenu.appendChild(followItem);
        }
        
        chartContextMenu.style.display = 'block';
        chartContextMenu.style.left = clientX + 'px';
        chartContextMenu.style.top = clientY + 'px';
        chartContextMenu.setAttribute('aria-hidden', 'false');
        requestAnimationFrame(() => {
            clampContextMenuToViewport();
            document.addEventListener('click', closeChartContextMenuHandler);
        });
    })();
}
if (chartContainerEl && chartContextMenu) {
    chartContainerEl.addEventListener('contextmenu', (e) => {
        e.preventDefault();
        e.stopPropagation();
        const onProfile = e.target && e.target.closest && e.target.closest('#profile-pane');
        openChartContextMenu(e.clientX, e.clientY, onProfile ? 'side' : 'chart');
    });
}

    return { updateCrosshairFromParam, clampInfoBoxMinHeight, updateCrosshairToLatest, openChartContextMenu };
}