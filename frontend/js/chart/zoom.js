// Wheel zoom and saved price/time ranges. `live` reads the chart instance
// that chart-main.js owns so these helpers stay out of that file.
export function installZoom(live) {
    const WHEEL_FACTOR = 0.002;

    function activePriceScale() {
        const chart = live.chart;
        const id = typeof live.priceScaleId === 'function' ? live.priceScaleId() : 'right';
        return chart.priceScale(id === 'left' ? 'left' : 'right');
    }

    function applyPriceZoom(deltaY) {
        const chart = live.chart;
        try {
            const priceScale = activePriceScale();
            const range = priceScale.getVisibleRange();
            if (!range || typeof range.from !== 'number' || typeof range.to !== 'number') return;
            const center = (range.from + range.to) / 2;
            const span = range.to - range.from;
            const factor = 1 + (deltaY * WHEEL_FACTOR);
            const newSpan = Math.max(span * factor, 1e-6);
            const clampedSpan = Math.max(1e-12, Math.min(1e30, newSpan));
            live.autoPrice = 'user';
            priceScale.applyOptions({ autoScale: false });
            priceScale.setVisibleRange({ from: center - clampedSpan / 2, to: center + clampedSpan / 2 });
            if (typeof live.redrawProfile === 'function') live.redrawProfile();
        } catch (err) {}
    }

    function applyTimeZoom(deltaY) {
        const chart = live.chart;
        try {
            const range = chart.timeScale().getVisibleLogicalRange();
            if (!range || typeof range.from !== 'number' || typeof range.to !== 'number') return;
            const center = (range.from + range.to) / 2;
            const span = range.to - range.from;
            const factor = 1 + (deltaY * WHEEL_FACTOR);
            const newSpan = Math.max(span * factor, 0.5);
            const newFrom = center - newSpan / 2;
            const newTo = center + newSpan / 2;
            chart.timeScale().setVisibleLogicalRange({ from: newFrom, to: newTo });
            if (typeof live.redrawProfile === 'function') live.redrawProfile();
        } catch (err) {}
    }

    function saveZoomState() {
        const chart = live.chart;
        if (!chart || !live.userHasZoomed) return null;
        try {
            const range = chart.timeScale().getVisibleRange();
            if (!range) return null;
            return { from: range.from, to: range.to };
        } catch (e) { return null; }
    }

    function restoreZoomState(zoomState) {
        const chart = live.chart;
        if (!chart || !zoomState || zoomState.from === undefined || zoomState.to === undefined) return;
        if (typeof live.markProgrammaticRange === 'function') live.markProgrammaticRange();
        try {
            chart.timeScale().setVisibleRange({ from: zoomState.from, to: zoomState.to });
        } catch (e) { console.warn('[Chart] restoreZoomState:', e); }
    }

    function setVisiblePriceRange(lo, hi) {
        const chart = live.chart;
        if (!chart || !(hi > lo)) return;
        try {
            const scale = activePriceScale();
            scale.applyOptions({ autoScale: false });
            scale.setVisibleRange({ from: lo, to: hi });
            live.autoPrice = 'ladder';
        } catch (e) {}
    }

    function setPriceScaleFromSpotOnly() {
        const chart = live.chart;
        const chartDataCache = live.chartDataCache;
        if (!chart) return;
        let spot = null;
        let spotMin = null;
        let spotMax = null;
        if (chartDataCache && Array.isArray(chartDataCache.spot)) {
            for (let i = 0; i < chartDataCache.spot.length; i++) {
                const v = Number(chartDataCache.spot[i]);
                if (!Number.isFinite(v) || v === 0) continue;
                spot = v;
                if (spotMin == null || v < spotMin) spotMin = v;
                if (spotMax == null || v > spotMax) spotMax = v;
            }
        }
        if (spot == null || spotMin == null || spotMax == null) return;
        const settings = live.globalChartSettings || {};
        const pct = Number(settings.ChartZoomFilterPercent);
        const percent = Number.isFinite(pct) && pct > 0 ? pct : 1;
        // Percent of the current spot, above the session high and below the session low.
        // Profile strikes stay on this scale and do not widen it.
        const pad = Math.abs(spot) * (percent / 100) || 1;
        try {
            const scale = activePriceScale();
            scale.applyOptions({ autoScale: false, scaleMargins: { top: 0.1, bottom: 0.1 } });
            scale.setVisibleRange({ from: spotMin - pad, to: spotMax + pad });
            live.autoPrice = 'spot';
            if (typeof live.redrawProfile === 'function') live.redrawProfile();
        } catch (e) {}
    }

    // Keep the configured band around the latest spot until the price scale is dragged or wheeled.
    function followSpotPrice() {
        if (live.autoPrice === 'user' || live.autoPrice === 'ladder') return;
        setPriceScaleFromSpotOnly();
    }

    return { applyPriceZoom, applyTimeZoom, saveZoomState, restoreZoomState, setPriceScaleFromSpotOnly, followSpotPrice, setVisiblePriceRange };
}
