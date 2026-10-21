// Canonical chart series metadata so plot names, legend names, and color settings stay aligned.
export const CHART_SERIES_METADATA = [
    { key: 'spot', label: 'Spot Price', defaultColor: '#4CAF50' },
    { key: 'zero_gamma', label: 'Zero Gamma', defaultColor: '#FF9800' },
    { key: 'major_pos_vol', label: 'Positive Gamma', defaultColor: '#2196F3' },
    { key: 'major_neg_vol', label: 'Negative Gamma', defaultColor: '#F44336' },
    { key: 'net', label: 'Net Gamma', defaultColor: '#00FFFF' },
    { key: 'major_long_gamma', label: 'Long Gamma', defaultColor: '#9C27B0' },
    { key: 'major_short_gamma', label: 'Short Gamma', defaultColor: '#00BCD4' },
    { key: 'major_positive', label: 'Major Positive Strike', defaultColor: '#8BC34A' },
    { key: 'major_negative', label: 'Major Negative Strike', defaultColor: '#FF5722' },
    { key: 'major_pos_oi', label: 'Major Positive OI', defaultColor: '#3F51B5' },
    { key: 'major_neg_oi', label: 'Major Negative OI', defaultColor: '#E91E63' }
];

export function getDefaultChartColors() {
    return CHART_SERIES_METADATA.reduce((acc, series) => {
        acc[series.key] = series.defaultColor;
        return acc;
    }, {});
}

export function datasetLabelsFromMetadata() {
    return CHART_SERIES_METADATA.reduce((acc, series) => {
        acc[series.key] = series.label;
        return acc;
    }, {});
}

// Price-scale tag: first letter of each word. "Long Gamma" is LG.
export function priceScaleTag(label) {
    const words = String(label || '').trim().split(/[^A-Za-z0-9]+/).filter(Boolean);
    if (!words.length) return '';
    return words.map((word) => word.charAt(0).toUpperCase()).join('');
}

const CHART_COLOR_OFF_VALUES = new Set(['off', 'none', 'hidden', 'transparent']);

export function isChartColorOffValue(color) {
    if (!color || typeof color !== 'string') return false;
    const normalized = color.trim().toLowerCase();
    return CHART_COLOR_OFF_VALUES.has(normalized) || normalized === '#00000000';
}

export function getChartColorsOffFromSettings(settings) {
    const off = new Set();
    if (settings && Array.isArray(settings.ChartColorsOff)) {
        settings.ChartColorsOff.forEach((key) => {
            if (typeof key === 'string' && key) off.add(key);
        });
    }
    if (settings && settings.ChartColors && typeof settings.ChartColors === 'object') {
        Object.entries(settings.ChartColors).forEach(([key, color]) => {
            if (isChartColorOffValue(color)) off.add(key);
        });
    }
    return off;
}

// Match NinjaScript Net plot level: spot + ((pos - abs(neg)) / 100)
export function computeNetGamma(spot, posGamma, negGamma) {
    if (typeof spot !== 'number' || typeof posGamma !== 'number' || typeof negGamma !== 'number') {
        return null;
    }
    if (!Number.isFinite(spot) || !Number.isFinite(posGamma) || !Number.isFinite(negGamma)) {
        return null;
    }
    return spot + ((posGamma - Math.abs(negGamma)) / 100);
}

export function computeNetSeries(dataObj) {
    if (!dataObj || !Array.isArray(dataObj.major_pos_vol) || !Array.isArray(dataObj.major_neg_vol) || !Array.isArray(dataObj.spot)) {
        return;
    }
    const len = Math.min(dataObj.major_pos_vol.length, dataObj.major_neg_vol.length, dataObj.spot.length);
    const net = new Array(len);
    for (let i = 0; i < len; i++) {
        const pos = Number(dataObj.major_pos_vol[i]);
        const neg = Number(dataObj.major_neg_vol[i]);
        const spot = Number(dataObj.spot[i]);
        net[i] = computeNetGamma(spot, pos, neg);
    }
    dataObj.net = net;
}
