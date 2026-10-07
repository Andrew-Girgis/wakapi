// Chart builders for the dashboard. Chart instances live here, outside petite-vue's reactive state.

export const GROUP_COLORS = {
    'Claude Code': '#8b7cf8',
    'Codex': '#2dd4bf',
    'Pi': '#f59e0b',
    'OpenCode': '#f472b6',
    'Other': '#64748b',
    'You': '#60a5fa',
}

const GRID = 'rgba(138, 144, 168, 0.12)'
const TICKS = 'rgb(138, 144, 168)'
const charts = {}

function upsert(key, canvas, config) {
    if (charts[key]) charts[key].destroy()
    charts[key] = new Chart(canvas.getContext('2d'), config)
}

export function renderActivity(canvas, data, mode, tz) {
    if (!canvas || !data) return
    const fmtDay = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
    const labels = data.days.map(d => fmtDay.format(new Date(d.date + 'T00:00:00Z')))
    const groups = data.groups.filter(g => data.days.some(d => (mode === 'tokens' ? d.tokens[g] : d.seconds[g]) > 0))
    const datasets = groups.map(g => ({
        label: g,
        data: data.days.map(d => mode === 'tokens' ? (d.tokens[g] || 0) / 1e6 : (d.seconds[g] || 0) / 3600),
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
