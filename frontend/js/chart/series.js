export function splitSegmentsToLwArrays(dataPoints, dateToLWTimeFn) {
    const out = [];
    let current = [];
    for (let i = 0; i < dataPoints.length; i++) {
        const p = dataPoints[i];
        if (p == null || (typeof p.x === 'number' && isNaN(p.x)) || typeof p.y !== 'number' || isNaN(p.y) || !isFinite(p.y)) {
            if (current.length >= 2) {
                out.push(current.map((pt) => ({ time: dateToLWTimeFn(pt.x), value: Number(pt.y) })));
            }
            current = [];
            continue;
        }
        current.push(p);
    }
    if (current.length >= 2) {
        out.push(current.map((pt) => ({ time: dateToLWTimeFn(pt.x), value: Number(pt.y) })));
    }
    return out;
}

// Pane primitive that draws segments using timeScale.timeToCoordinate at draw time so x matches the same frame (avoids LWC recalc jitter).
export function SegmentPrimitive() {
    var self = this;
    this._chart = null;
    this._priceSeries = null;
    this._segments = {};
    this._view = {
        renderer: function () {
            return {
                draw: function (target, drawingUtils) {
                    if (!self._chart || !self._priceSeries) return;
                    var ts = self._chart.timeScale();
                    var priceToCoord = self._priceSeries.priceToCoordinate.bind(self._priceSeries);
                    target.useBitmapCoordinateSpace(function (scope) {
                        var ctx = scope.context;
                        var hRatio = scope.horizontalPixelRatio || 1;
                        var vRatio = scope.verticalPixelRatio || 1;
                        var w = scope.bitmapSize ? scope.bitmapSize.width : 1e6;
                        var h = scope.bitmapSize ? scope.bitmapSize.height : 1e6;
                        var inset = Math.max(1, Math.floor(4 * hRatio));
                        var xMin = inset;
                        var xMax = w - inset;
                        if (xMax <= xMin) return;
                        ctx.save();
                        ctx.beginPath();
                        ctx.rect(0, 0, w, h);
                        ctx.clip();
                        for (var endpoint in self._segments) {
                            var seg = self._segments[endpoint];
                            if (!seg || !seg.visible) continue;
                            ctx.fillStyle = seg.color || 'rgb(128,128,128)';
                            ctx.strokeStyle = seg.color || 'rgb(128,128,128)';
                            if (seg.scatter) {
                                var radius = Math.max(1, 1.15 * hRatio);
                                var lastX = -1e9;
                                var lastY = -1e9;
                                var points = seg.points || [];
                                for (var p = 0; p < points.length; p++) {
                                    var point = points[p];
                                    if (!point) continue;
                                    var sx = ts.timeToCoordinate(point.time);
                                    if (sx == null && typeof ts.timeToIndex === 'function') {
                                        var sidx = ts.timeToIndex(point.time);
                                        if (sidx != null) sx = ts.logicalToCoordinate(sidx);
                                    }
                                    var sy = priceToCoord(point.value);
                                    if (sx == null || sy == null || !Number.isFinite(sy)) continue;
                                    var sxb = Math.round(sx * hRatio);
                                    var syb = Math.round(sy * vRatio);
                                    if (sxb < xMin || sxb > xMax) continue;
                                    if (Math.abs(sxb - lastX) < radius && Math.abs(syb - lastY) < radius) continue;
                                    lastX = sxb;
                                    lastY = syb;
                                    ctx.beginPath();
                                    ctx.arc(sxb, syb, radius, 0, Math.PI * 2);
                                    ctx.fill();
                                }
                                continue;
                            }
                            if (!seg.pairs || seg.pairs.length < 2) continue;
                            ctx.lineWidth = Math.max(1, Math.floor((seg.lineWidth || 2) * vRatio));
                            for (var i = 0; i < seg.pairs.length - 1; i += 2) {
                                var b0 = seg.pairs[i];
                                var b1 = seg.pairs[i + 1];
                                if (!b0 || !b1) continue;
                                var x0 = ts.timeToCoordinate(b0.time);
                                var x1 = ts.timeToCoordinate(b1.time);
                                if (x0 == null && typeof ts.timeToIndex === 'function') {
                                    var idx0 = ts.timeToIndex(b0.time);
                                    if (idx0 != null) x0 = ts.logicalToCoordinate(idx0);
                                }
                                if (x1 == null && typeof ts.timeToIndex === 'function') {
                                    var idx1 = ts.timeToIndex(b1.time);
                                    if (idx1 != null) x1 = ts.logicalToCoordinate(idx1);
                                }
                                var yCoord = priceToCoord(b0.value);
                                if (x0 == null || x1 == null || yCoord == null || !Number.isFinite(yCoord)) continue;
                                var x0b = Math.round(x0 * hRatio);
                                var x1b = Math.round(x1 * hRatio);
                                if (x1b <= xMin || x0b >= xMax) continue;
                                var xStart = Math.max(xMin, Math.min(xMax, x0b));
                                var xEnd = Math.max(xMin, Math.min(xMax, x1b));
                                if (xStart >= xEnd) continue;
                                var y = Math.round(yCoord * vRatio);
                                ctx.beginPath();
                                ctx.moveTo(xStart, y);
                                ctx.lineTo(xEnd, y);
                                ctx.stroke();
                            }
                        }
                        ctx.restore();
                    });
                }
            };
        }
    };
    this.attached = function (param) { self._chart = param && param.chart ? param.chart : null; };
    this.paneViews = function () { return [self._view]; };
    this.setPriceSeries = function (series) { self._priceSeries = series; };
    this.hasPriceSeries = function () { return !!self._priceSeries; };
    this.setSegmentData = function (endpoint, pairs, color, visible, lineWidth) {
        self._segments[endpoint] = { pairs: pairs || [], color: color, visible: visible !== false, lineWidth: lineWidth != null ? lineWidth : 2 };
    };
    this.setScatterData = function (endpoint, points, color, visible) {
        self._segments[endpoint] = { scatter: true, points: points || [], color: color, visible: visible !== false };
    };
    this.clearScatter = function (endpoint) {
        var seg = self._segments[endpoint];
        if (seg && seg.scatter) delete self._segments[endpoint];
    };
    this.setSegmentVisible = function (endpoint, visible) {
        var seg = self._segments[endpoint];
        if (seg) seg.visible = visible !== false;
    };
    this.clearAll = function () { self._segments = {}; };
}

// Convert segment pairs [p0,p1, p2,p3, ...] to line series data with whitespace gaps so the line
// draws only p0->p1, p2->p3, etc. (no connectors between segments). Uses same pipeline as spot line = no jitter.
export function segmentPairsToLineDataWithGaps(segmentPairs) {
    if (!segmentPairs || segmentPairs.length < 2) return [];
    const out = [];
    for (let i = 0; i < segmentPairs.length; i++) {
        out.push({ time: segmentPairs[i].time, value: segmentPairs[i].value });
        if (i % 2 === 1 && i < segmentPairs.length - 1) {
            var t1 = segmentPairs[i].time;
            var t2 = segmentPairs[i + 1].time;
            var gapTime = (typeof t1 === 'number' && typeof t2 === 'number') ? (t1 + t2) / 2 : t1;
            out.push({ time: gapTime });
        }
    }
    return out;
}

// Custom series: draws only horizontal segments (no vertical connectors). One series per endpoint.
// Data = pairs of points per segment: [ { time, value }, { time, value }, ... ]; renderer draws bar[i] -> bar[i+1] only.
// Uses bar.x from the library for positioning. (Using time scale at draw time broke visibility for non-spot series.)
export function HorizontalSegmentsPaneView(timeScaleApi, skipDraw) {
    const view = this;
    this._data = null;
    this._options = null;
    this._timeScale = timeScaleApi || null;
    this._skipDraw = !!skipDraw;
    this._renderer = {
        draw: function (target, priceToY) {
            if (view._skipDraw) return;
            const data = view._data;
            if (!data || !data.bars || data.bars.length < 2) return;
            const opts = view._options || {};
            const lineWidth = opts.lineWidth || 2;
            const color = opts.color || 'rgb(128, 128, 128)';
            var barSpacing = (data.barSpacing != null && Number.isFinite(data.barSpacing) && data.barSpacing > 0) ? data.barSpacing : null;
            function barToX(bar) {
                return (bar && typeof bar.x === 'number' && Number.isFinite(bar.x)) ? bar.x : NaN;
            }
            target.useBitmapCoordinateSpace(function (scope) {
                const ctx = scope.context;
                const hRatio = scope.horizontalPixelRatio || 1;
                const vRatio = scope.verticalPixelRatio || 1;
                const w = scope.bitmapSize ? scope.bitmapSize.width : 1e6;
                const mediaWidth = w / hRatio;
                var snapToBarGrid = function (xMedia) {
                    if (barSpacing == null || barSpacing <= 0 || !Number.isFinite(xMedia)) return xMedia;
                    var k = (mediaWidth - 1 - xMedia) / barSpacing - 0.5;
                    var kRounded = Math.round(k);
                    return mediaWidth - 1 - (kRounded + 0.5) * barSpacing;
                };
                const inset = Math.max(1, Math.floor(4 * hRatio));
                const xMin = inset;
                const xMax = w - inset;
                if (xMax <= xMin) return;
                ctx.save();
                ctx.beginPath();
                ctx.rect(0, 0, w, scope.bitmapSize ? scope.bitmapSize.height : 1e6);
                ctx.clip();
                ctx.strokeStyle = color;
                ctx.lineWidth = Math.max(1, Math.floor(lineWidth * vRatio));
                for (let i = 0; i < data.bars.length - 1; i += 2) {
                    const b0 = data.bars[i];
                    const b1 = data.bars[i + 1];
                    if (!b0 || !b1 || !b0.originalData || !b1.originalData) continue;
                    var x0 = barToX(b0);
                    var x1 = barToX(b1);
                    if (!Number.isFinite(x0) || !Number.isFinite(x1)) continue;
                    x0 = snapToBarGrid(x0);
                    x1 = snapToBarGrid(x1);
                    var x0b = Math.round(x0 * hRatio);
                    var x1b = Math.round(x1 * hRatio);
                    if (x1b <= xMin || x0b >= xMax) continue;
                    let xStart = Math.max(xMin, Math.min(xMax, x0b));
                    let xEnd = Math.max(xMin, Math.min(xMax, x1b));
                    const minSegmentPx = 1;
                    if (xEnd <= xStart) {
                        if (xStart > xMin && xEnd < xMax) xEnd = Math.min(xMax, xStart + minSegmentPx);
                        else continue;
                    }
                    if (xStart >= xEnd) continue;
                    const edgeBandPx = 3;
                    if (xEnd - xStart <= edgeBandPx && (xStart <= xMin + edgeBandPx || xEnd >= xMax - edgeBandPx)) continue;
                    const mediaY = priceToY(b0.originalData.value);
                    const y = Math.round(mediaY * vRatio);
                    ctx.beginPath();
                    ctx.moveTo(xStart, y);
                    ctx.lineTo(xEnd, y);
                    ctx.stroke();
                }
                ctx.restore();
            });
        }
    };
    this.renderer = function () { return view._renderer; };
    this.update = function (data, seriesOptions) {
        view._data = data;
        view._options = seriesOptions;
    };
    this.priceValueBuilder = function (data) {
        return [data.value, data.value, data.value];
    };
    this.isWhitespace = function () {
        return false;
    };
    this.destroy = function () {};
    this.defaultOptions = function () {
        return { lineWidth: 2, color: 'rgb(128, 128, 128)' };
    };
}
