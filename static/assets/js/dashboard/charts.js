// Chart builders for the dashboard. Chart instances live here, outside petite-vue's reactive state.

export const GROUP_COLORS = {
    'Claude Code': '#8b7cf8',
    'Codex': '#2dd4bf',
    'Pi': '#f59e0b',
    'OpenCode': '#f472b6',
    'Other': '#64748b',
    'WakaTime fallback': '#64748b',
    'You': '#60a5fa',
}

const GRID = 'rgba(138, 144, 168, 0.12)'
const TICKS = 'rgb(138, 144, 168)'
const charts = {}

function upsert(key, canvas, config) {
    if (charts[key]) charts[key].destroy()
    charts[key] = new Chart(canvas.getContext('2d'), config)
}

// long ranges are summed into weeks (> 62 days) or months (> 366 days) so bars stay readable
export function bucketDays(days) {
    if (days.length <= 62) return { unit: 'day', buckets: days }
    const unit = days.length > 366 ? 'month' : 'week'
    const out = []
    for (const d of days) {
        const date = new Date(d.date + 'T00:00:00Z')
        let key
        if (unit === 'month') key = d.date.slice(0, 7) + '-01'
        else {
            const monday = new Date(date)
            monday.setUTCDate(date.getUTCDate() - ((date.getUTCDay() + 6) % 7))
            key = monday.toISOString().slice(0, 10)
        }
        let b = out[out.length - 1]
        if (!b || b.date !== key) out.push(b = { date: key, seconds: {}, tokens: {} })
        for (const [g, v] of Object.entries(d.seconds)) b.seconds[g] = (b.seconds[g] || 0) + v
        for (const [g, v] of Object.entries(d.tokens)) b.tokens[g] = (b.tokens[g] || 0) + v
    }
    // drop leading empty buckets
    while (out.length > 1 && !Object.keys(out[0].seconds).length && !Object.keys(out[0].tokens).length) out.shift()
    return { unit, buckets: out }
}

export function renderActivity(canvas, data, mode) {
    if (!canvas || !data) return
    const { unit, buckets } = bucketDays(data.days)
    const fmt = new Intl.DateTimeFormat('en', unit === 'month' ? { month: 'short', year: '2-digit', timeZone: 'UTC' } : { month: 'short', day: 'numeric', timeZone: 'UTC' })
    const labels = buckets.map(d => (unit === 'week' ? 'wk ' : '') + fmt.format(new Date(d.date + 'T00:00:00Z')))
    const groups = data.groups.filter(g => buckets.some(d => (mode === 'tokens' ? d.tokens[g] : d.seconds[g]) > 0))
    const datasets = groups.map(g => ({
        label: g,
        data: buckets.map(d => mode === 'tokens' ? (d.tokens[g] || 0) / 1e6 : (d.seconds[g] || 0) / 3600),
        backgroundColor: GROUP_COLORS[g] || GROUP_COLORS.Other,
        borderWidth: 0,
        borderRadius: 2,
        maxBarThickness: 76,
        categoryPercentage: 0.62,
        barPercentage: 1,
    }))

    upsert('activity', canvas, {
        type: 'bar',
        data: { labels, datasets },
        options: {
            maintainAspectRatio: false,
            animation: false,
            plugins: {
                legend: { display: false },
                tooltip: {
                    callbacks: {
                        label: ctx => mode === 'tokens'
                            ? `${ctx.dataset.label}: ${ctx.parsed.y.toFixed(1)}M tokens`
                            : `${ctx.dataset.label}: ${Math.floor(ctx.parsed.y)}h ${String(Math.round((ctx.parsed.y % 1) * 60)).padStart(2, '0')}m`,
                    },
                },
            },
            scales: {
                x: { stacked: true, grid: { display: false }, ticks: { color: TICKS, maxRotation: 0, autoSkip: true } },
                y: {
                    stacked: true,
                    beginAtZero: true,
                    grid: { color: GRID },
                    border: { display: false },
                    ticks: { color: TICKS, maxTicksLimit: 5, callback: v => mode === 'tokens' ? `${v}M` : `${v}h` },
                },
            },
        },
    })
}

// token buckets in stacking order; reasoning is inside output and never a separate bar
export const TOKEN_BUCKETS = [
    { key: 'cached_input', label: 'Cache read', color: '#8b7cf8' },
    { key: 'input', label: 'New input', color: '#60a5fa' },
    { key: 'cache_write', label: 'Cache write', color: '#f59e0b' },
    { key: 'unknown_input', label: 'Unknown input', color: '#64748b' },
    { key: 'output', label: 'Output', color: '#2dd4bf' },
]

// Tokens per day (or week/month) stacked by bucket, with the cache-read share as a line on the right axis.
export function renderTokenDays(canvas, data) {
    if (!canvas || !data) return
    const days = data.by_day.map(d => ({ date: d.date, seconds: {}, tokens: Object.fromEntries(TOKEN_BUCKETS.map(b => [b.key, d.tokens[b.key] || 0])) }))
    const { unit, buckets } = bucketDays(days)
    const fmt = new Intl.DateTimeFormat('en', unit === 'month' ? { month: 'short', year: '2-digit', timeZone: 'UTC' } : { month: 'short', day: 'numeric', timeZone: 'UTC' })
    const labels = buckets.map(d => (unit === 'week' ? 'wk ' : '') + fmt.format(new Date(d.date + 'T00:00:00Z')))
    const datasets = TOKEN_BUCKETS.filter(b => buckets.some(d => d.tokens[b.key] > 0)).map(b => ({
        type: 'bar', label: b.label, yAxisID: 'y',
        data: buckets.map(d => (d.tokens[b.key] || 0) / 1e6),
        backgroundColor: b.color, borderWidth: 0, borderRadius: 2, maxBarThickness: 76, categoryPercentage: 0.62, barPercentage: 1,
    }))
    datasets.push({
        type: 'line', label: 'Cache-read share', yAxisID: 'share', order: -1,
        data: buckets.map(d => {
            const input = d.tokens.input + d.tokens.cached_input + d.tokens.cache_write + d.tokens.unknown_input
            return input > 0 ? d.tokens.cached_input / input * 100 : null
        }),
        borderColor: 'rgb(230, 232, 242)', backgroundColor: 'rgb(230, 232, 242)', borderWidth: 1.5, pointRadius: 2, spanGaps: true,
    })
    upsert('tokens', canvas, {
        data: { labels, datasets },
        options: {
            maintainAspectRatio: false,
            animation: false,
            plugins: {
                legend: { display: false },
                tooltip: { callbacks: { label: ctx => ctx.dataset.yAxisID === 'share' ? `${ctx.dataset.label}: ${ctx.parsed.y.toFixed(1)}%` : `${ctx.dataset.label}: ${ctx.parsed.y.toFixed(1)}M` } },
            },
            scales: {
                x: { stacked: true, grid: { display: false }, ticks: { color: TICKS, maxRotation: 0, autoSkip: true } },
                y: { stacked: true, beginAtZero: true, grid: { color: GRID }, border: { display: false }, ticks: { color: TICKS, maxTicksLimit: 5, callback: v => `${v}M` } },
                share: { position: 'right', min: 0, max: 100, grid: { display: false }, border: { display: false }, ticks: { color: TICKS, maxTicksLimit: 5, callback: v => `${v}%` } },
            },
        },
    })
}
