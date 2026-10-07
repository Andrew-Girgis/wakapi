import { GROUP_COLORS, renderActivity } from './charts.js'

// Activity dashboard (/dashboard). Each panel fetches its own endpoint under api/dashboard/,
// so a slow or failing panel never blocks the others.

const PANELS = {
    overview: () => `api/dashboard/overview?${rangeQuery()}`,
    activity: () => `api/dashboard/activity?${rangeQuery()}`,
    machines: () => 'api/dashboard/machines',
    projects: () => `api/dashboard/projects?${rangeQuery()}`,
}

const TABS = [
    { id: 'overview', label: 'Overview' },
    { id: 'tokens', label: 'Tokens & cost' },
    { id: 'agents', label: 'Agents' },
    { id: 'projects', label: 'Projects' },
    { id: 'machines', label: 'Machines' },
]

const params = new URLSearchParams(window.location.search)

function initialRange() {
    if (params.has('days')) return `days:${params.get('days')}`
    if (params.has('interval')) return `interval:${params.get('interval')}`
    if (params.has('from') && params.has('to')) return `from:${params.get('from')}:${params.get('to')}`
    return 'days:7'
}

const state = PetiteVue.reactive({
    range: initialRange(),
    project: params.get('project') || '',
    machine: params.get('machine') || '',
})

// query string for the current range and filters, shared by all range-based panels
function rangeQuery() {
    const q = new URLSearchParams()
    const [kind, a, b] = state.range.split(':')
    if (kind === 'days') q.set('days', a)
    else if (kind === 'interval') q.set('interval', a)
    else if (kind === 'from') { q.set('from', a); q.set('to', b) }
    if (state.project) q.set('project', state.project)
    if (state.machine) q.set('machine', state.machine)
    return q.toString()
}

const dateFmt = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: dashboardTimeZone })
const yearFmt = new Intl.DateTimeFormat('en', { year: 'numeric', timeZone: dashboardTimeZone })

export const fmt = {
    duration(seconds) {
        const m = Math.round((seconds || 0) / 60)
        return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`
    },
    tokens(n) {
        n = n || 0
        if (n >= 1e9) return `${(n / 1e9).toFixed(n >= 1e10 ? 0 : 1)}B`
        if (n >= 1e6) return `${(n / 1e6).toFixed(n >= 1e8 ? 0 : 1)}M`
        if (n >= 1e3) return `${(n / 1e3).toFixed(n >= 1e5 ? 0 : 1)}K`
        return String(n)
    },
    usd(v) {
        return (v || 0).toLocaleString('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 2 })
    },
    int(n) {
        return (n || 0).toLocaleString('en-US')
    },
}

const app = {
    $delimiters: ['${', '}'],
    tabs: TABS,
    tab: (window.location.hash || '#overview').slice(1),
    timeZone: dashboardTimeZone,
    panels: Object.fromEntries(Object.keys(PANELS).map(k => [k, { loading: false, error: null, data: null }])),

    get range() { return state.range },
    set range(v) { state.range = v },
    get project() { return state.project },
    set project(v) { state.project = v },
    get machine() { return state.machine },
    set machine(v) { state.machine = v },

    get ov() { return this.panels.overview.data },
    get act() { return this.panels.activity.data },

    activityMode: 'time',
    groupColors: GROUP_COLORS,
    get activityTitle() {
        const [kind, a] = state.range.split(':')
        if (kind === 'days' && a === '7') return 'Activity this week'
        if (kind === 'days' && a === '1') return 'Activity today'
        if (kind === 'days') return `Activity, last ${a} days`
        return 'Activity'
    },
    get activityGroups() {
        const d = this.act
        if (!d) return []
        return d.groups.filter(g => d.days.some(x => (this.activityMode === 'tokens' ? x.tokens[g] : x.seconds[g]) > 0))
    },
    // groups under 1 % are folded into "Other", like the mockup
    get shareRows() {
        const rows = [], other = { group: 'Other', seconds: 0, percent: 0 }
        for (const s of this.act?.share || []) {
            if (s.group !== 'Other' && s.percent >= 1) rows.push(s)
            else { other.seconds += s.seconds; other.percent += s.percent }
        }
        if (other.seconds > 0) rows.push({ ...other, percent: Math.round(other.percent * 10) / 10 })
        return rows
    },
    setActivityMode(mode) {
        this.activityMode = mode
        renderActivity(document.getElementById('activity-chart'), this.act, mode)
    },
    fmtMinutes(seconds) {
        const m = Math.round((seconds || 0) / 60)
        return m >= 60 ? `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m` : `${m}m`
    },

    get customRange() { return state.range.startsWith('from:') || (state.range.startsWith('interval:') && state.range !== 'interval:any') ? state.range : null },
    get customRangeLabel() { return state.range.replace(/^(from|interval):/, '').replace(':', ' – ') },

    get rangeLabel() {
        const r = this.ov?.range
        if (!r) return ''
        const from = new Date(r.from), to = new Date(new Date(r.to).getTime() - 1)
        const a = dateFmt.format(from), b = dateFmt.format(to)
        const year = yearFmt.format(to)
        if (a === b) return `${a}, ${year}`
        const sameMonth = a.split(' ')[0] === b.split(' ')[0]
        return `${a} – ${sameMonth ? b.split(' ')[1] : b}, ${year}`
    },
    get periodName() {
        const [kind, a] = state.range.split(':')
        if (kind === 'days') return a === '1' ? 'day' : a === '7' ? 'week' : `${a} days`
        return 'period'
    },

    get liveCount() { return this.panels.machines.data?.live_count ?? 0 },
    get liveTitle() {
        const m = this.panels.machines.data?.machines || []
        return m.map(x => `${x.name}: ${x.live ? 'live' : 'last seen ' + new Date(x.last_seen).toLocaleString()}`).join('\n')
    },
    get machineOptions() {
        const names = (this.panels.machines.data?.machines || []).map(m => m.name)
        if (state.machine && !names.includes(state.machine)) names.push(state.machine)
        return names
    },
    get projectOptions() {
        const names = (this.panels.projects.data?.projects || []).map(p => p.name)
        if (state.project && !names.includes(state.project)) names.unshift(state.project)
        return names
    },

    fmtDuration: fmt.duration,
    fmtTokens: fmt.tokens,
    fmtUSD: fmt.usd,
    fmtInt: fmt.int,

    async load(name) {
        const panel = this.panels[name]
        panel.loading = true
        panel.error = null
        try {
            const res = await fetch(PANELS[name]())
            if (!res.ok) throw new Error(`HTTP ${res.status}`)
            panel.data = await res.json()
            window.dispatchEvent(new CustomEvent('dashboard:loaded', { detail: { name, data: panel.data } }))
        } catch (e) {
            console.error(`dashboard panel ${name} failed`, e)
            panel.error = String(e)
        } finally {
            panel.loading = false
        }
    },

    reloadAll() {
        Object.keys(PANELS).forEach(name => this.load(name)) // in parallel, independently
    },

    applyFilters() {
        const q = new URLSearchParams(rangeQuery())
        history.replaceState(null, '', `dashboard?${q.toString()}${window.location.hash}`)
        this.reloadAll()
    },

    mounted() {
        window.addEventListener('dashboard:loaded', e => {
            if (e.detail.name === 'activity') setTimeout(() => renderActivity(document.getElementById('activity-chart'), e.detail.data, this.activityMode), 0)
        })
        this.reloadAll()
    },
}

export function registerPanel(name, url, extra = {}) {
    PANELS[name] = url
    app.panels[name] = { loading: false, error: null, data: null }
    Object.assign(app, extra)
}

export { app, state, rangeQuery }

// panels register themselves in their own modules before the app mounts
window.addEventListener('DOMContentLoaded', () => PetiteVue.createApp(app).mount('#dashboard'))
