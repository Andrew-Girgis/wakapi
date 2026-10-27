import { GROUP_COLORS, TOKEN_BUCKETS, renderActivity, renderTokenDays } from './charts.js?v=plan2-v1'

// Activity dashboard (/dashboard). Each panel fetches its own endpoint under api/dashboard/,
// so a slow or failing panel never blocks the others.

const PANELS = {
    overview: () => `api/dashboard/overview?${rangeQuery()}`,
    activity: () => `api/dashboard/activity?${rangeQuery()}`,
    timeline: () => `api/dashboard/timeline?${timelineQuery()}`,
    machines: () => 'api/dashboard/machines',
    projects: () => `api/dashboard/projects?${rangeQuery()}`,
    cost: () => `api/dashboard/cost?${rangeQuery()}`,
    ledger: () => `api/dashboard/ledger?${rangeQuery()}`,
    options: () => 'api/dashboard/options',
    breakdown: () => `api/dashboard/breakdown?${rangeQuery()}`,
    tokens: () => `api/dashboard/tokens?${rangeQuery()}`,
    agents: () => `api/dashboard/agents?${rangeQuery()}`,
}

// panels each tab shows; a tab's panels load when it is first shown and again after a filter change
const TAB_PANELS = {
    overview: ['overview', 'activity', 'timeline', 'projects', 'cost', 'ledger', 'breakdown'],
    tokens: ['overview', 'tokens', 'cost', 'ledger', 'projects'],
    agents: ['activity', 'agents'],
    projects: ['projects'],
    machines: [],
}
const ALWAYS = ['overview', 'machines', 'options'] // header: range label, live machines and filter options

const AGENTS = ['Claude Code', 'Codex', 'Pi', 'OpenCode']

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
    model: params.get('model') || '',
    agent: params.get('agent') || '',
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
    if (state.model) q.set('model', state.model)
    if (state.agent) q.set('agent', state.agent)
    return q.toString()
}

// today's date (YYYY-MM-DD) in the user's time zone
function todayISO() {
    return new Intl.DateTimeFormat('en-CA', { timeZone: dashboardTimeZone }).format(new Date())
}

function shiftISO(iso, days) {
    const d = new Date(iso + 'T12:00:00Z')
    d.setUTCDate(d.getUTCDate() + days)
    return d.toISOString().slice(0, 10)
}

const timelineState = PetiteVue.reactive({ date: params.get('date') || todayISO() })

function timelineQuery() {
    const q = new URLSearchParams({ date: timelineState.date })
    if (state.project) q.set('project', state.project)
    if (state.machine) q.set('machine', state.machine)
    if (state.agent) q.set('agent', state.agent)
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
    tab: TABS.some(t => '#' + t.id === window.location.hash) ? window.location.hash.slice(1) : 'overview',
    agentOptions: AGENTS,
    tokenBuckets: TOKEN_BUCKETS,
    timeZone: dashboardTimeZone,
    tokenSource: dashboardTokenSource,
    get ledgerMode() { return this.tokenSource === 'ledger' },
    get accountingLabel() { return this.ledgerMode ? 'Ledger · provisional accounted usage' : 'Legacy heartbeat accounting' },
    panels: Object.fromEntries(Object.keys(PANELS).map(k => [k, { loading: false, error: null, data: null }])),

    get range() { return state.range },
    set range(v) { state.range = v },
    get project() { return state.project },
    set project(v) { state.project = v },
    get machine() { return state.machine },
    set machine(v) { state.machine = v },
    get model() { return state.model },
    set model(v) { state.model = v },
    get agent() { return state.agent },
    set agent(v) { state.agent = v },
    get modelOptions() { const names = [...(this.panels.options.data?.models || [])]; if (state.model && !names.includes(state.model)) names.push(state.model); return names },

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
        let shares = this.act?.share || []
        if (this.activityMode === 'tokens' && this.act) {
            const totals = this.act.groups.map(group => ({ group, tokens: this.act.days.reduce((n, d) => n + (d.tokens[group] || 0), 0) }))
            const total = totals.reduce((n, d) => n + d.tokens, 0)
            shares = totals.filter(d => d.tokens > 0).map(d => ({ group: d.group, seconds: d.tokens, percent: total ? d.tokens / total * 100 : 0 }))
        }
        for (const s of shares) {
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
    get tl() { return this.panels.timeline.data },
    timelineExpanded: false,
    get timelineDate() { return timelineState.date },
    get timelineIsToday() { return timelineState.date === todayISO() },
    get timelineTitle() { return this.timelineIsToday ? "Today's timeline" : 'Timeline' },
    get timelineSubtitle() {
        const d = this.tl
        if (!d) return ''
        const day = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' }).format(new Date(d.date + 'T00:00:00Z'))
        return `${day} · ${this.clock(d.window_start)}–${this.clock(d.window_end)}`
    },
    get timelineRows() {
        const rows = this.tl?.rows || []
        return this.timelineExpanded ? rows : rows.slice(0, 6)
    },
    get timelineGroups() {
        const seen = new Set()
        for (const r of this.tl?.rows || []) for (const s of r.segments) seen.add(s.group)
        return (this.tl?.groups || []).filter(g => seen.has(g))
    },
    get timelineTicks() {
        const d = this.tl
        if (!d) return []
        const start = new Date(d.window_start).getTime(), end = new Date(d.window_end).getTime()
        const hours = Math.max(1, Math.round((end - start) / 3600e3))
        const step = hours <= 4 ? 1 : hours <= 12 ? 3 : 6
        const ticks = []
        for (let h = 0; h <= hours; h += step) {
            const t = start + h * 3600e3
            ticks.push({ left: ((t - start) / (end - start)) * 100, label: this.clock(new Date(t).toISOString()).slice(0, 2) })
        }
        return ticks
    },
    segmentStyle(seg) {
        const d = this.tl
        const start = new Date(d.window_start).getTime(), end = new Date(d.window_end).getTime()
        const a = (new Date(seg.start).getTime() - start) / (end - start) * 100
        const w = (new Date(seg.end).getTime() - new Date(seg.start).getTime()) / (end - start) * 100
        return { left: a + '%', width: Math.max(w, 0.4) + '%', background: GROUP_COLORS[seg.group] || GROUP_COLORS.Other }
    },
    segmentTitle(row, seg) {
        return `${row.project} · ${seg.group} · ${this.clock(seg.start)}–${this.clock(seg.end)}`
    },
    clock(iso) {
        return new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone: dashboardTimeZone }).format(new Date(iso))
    },
    stepTimeline(days) {
        const next = shiftISO(timelineState.date, days)
        if (next > todayISO()) return
        timelineState.date = next
        this.load('timeline')
    },
    get pj() { return this.panels.projects.data },
    projectsExpanded: false,
    projectSort: { key: 'seconds', desc: true },
    get projectRows() {
        const rows = this.pj?.projects || []
        if (this.tab !== 'projects') return this.projectsExpanded ? rows : rows.slice(0, 6)
        const { key, desc } = this.projectSort
        const value = p => key === 'name' ? (p.name || '').toLowerCase() : key === 'last_active' ? new Date(p.last_active).getTime() || 0 : p[key] || 0
        return [...rows].sort((a, b) => (value(a) < value(b) ? -1 : value(a) > value(b) ? 1 : 0) * (desc ? -1 : 1))
    },
    sortProjects(key) {
        this.projectSort = { key, desc: this.projectSort.key === key ? !this.projectSort.desc : key !== 'name' }
    },
    sortMark(key) { return this.projectSort.key === key ? (this.projectSort.desc ? ' ↓' : ' ↑') : '' },
    get projectsByCost() { return [...(this.pj?.projects || [])].filter(p => p.tokens > 0).sort((a, b) => b.cost - a.cost || b.tokens - a.tokens) },
    statusLabel(status) {
        return status ? status[0].toUpperCase() + status.slice(1) : '—'
    },
    // polyline points for a 280x28 sparkline of daily seconds
    sparkline(values) {
        const w = 280, h = 28, n = values.length
        if (!n) return ''
        const max = Math.max(...values, 1)
        return values.map((v, i) => `${n === 1 ? w / 2 : (i / (n - 1)) * w},${(h - 3 - (v / max) * (h - 6)).toFixed(1)}`).join(' ')
    },
    get cs() { return this.panels.cost.data },
    get lg() { return this.panels.ledger.data },
    get ledgerDelta() { return this.ov?.legacy_tokens ? this.ov.tokens.total - this.ov.legacy_tokens.total : (this.lg && this.ov ? this.lg.total_tokens - this.ov.tokens.total : null) },
    get tokenDetail() { const t = this.ov?.tokens; return t ? `${fmt.int(t.input)} new input · ${fmt.int(t.cached_input)} cache read · ${fmt.int(t.cache_write)} cache write · ${fmt.int(t.unknown_input)} unknown input · ${fmt.int(t.output)} output` : '' },
    modelsExpanded: false,
    get allCostRows() { return (this.cs?.cost.by_model || []).filter(m => m.tokens.total > 0) },
    get costRows() { return this.modelsExpanded || this.tab === 'tokens' ? this.allCostRows : this.allCostRows.slice(0, 8) },
    modelLabel(m) { return m.model ? `${m.model} ${m.version}`.trim() : '(no model)' },
    // ledger: the plan recorded on the session's heartbeats, never "pay per token"; heartbeats: legacy split
    planLabel(m) {
        if (this.cs.cost.attribution === 'recorded_plan') return m.recorded_plan || 'Plan unknown'
        return this.cs.cost.billing_attribution_known ? (m.subscription ? 'Subscription' : 'Pay per token') : 'Unknown'
    },

    get mc() { return this.panels.machines.data },
    pipelineFor(m) { return (this.mc?.pipelines || []).filter(p => p.machine === m.name) },

    get bd() { return this.panels.breakdown.data },
    get tk() { return this.panels.tokens.data },
    get ag() { return this.panels.agents.data },
    // API value per hour of agent time in the range (agent time is a union, so parallel agents count once)
    get costPerAgentHour() {
        const o = this.ov
        if (!o || !o.cost.available || !o.time.agent_seconds) return null
        return o.cost.estimated_api_value / (o.time.agent_seconds / 3600)
    },
    get rhythmMax() {
        const b = this.bd
        if (!b) return 1
        return Math.max(1, ...b.rhythm_agents.map((row, d) => Math.max(...row.map((v, h) => v + b.rhythm_you[d][h]))))
    },
    rhythmCell(d, h) {
        const b = this.bd, a = b.rhythm_agents[d][h], y = b.rhythm_you[d][h], total = a + y
        if (!total) return { background: 'rgb(255 255 255 / 0.03)' }
        const alpha = 0.15 + 0.85 * total / this.rhythmMax
        return { background: a >= y ? `rgb(139 124 248 / ${alpha})` : `rgb(96 165 250 / ${alpha})` }
    },
    rhythmTitle(d, h) {
        const b = this.bd
        return `${['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'][d]} ${String(h).padStart(2, '0')}:00 · agents ${this.fmtMinutes(b.rhythm_agents[d][h])} · you ${this.fmtMinutes(b.rhythm_you[d][h])}`
    },
    barRows(list) { return (list || []).slice(0, 8) },
    barWidth(list, row) { const max = (list || [])[0]?.seconds || 1; return Math.max(1, row.seconds / max * 100) + '%' },
    osName(os) {
        const o = (os || '').toLowerCase()
        return o === 'macos' || o === 'darwin' ? 'macOS' : o === 'linux' ? 'Linux' : o === 'windows' ? 'Windows' : os || 'unknown OS'
    },
    machineLine(m) {
        if (m.live && m.active_agent) return `${this.osName(m.os)} · ${m.active_agent} active`
        const last = (m.agents || [])[0]
        return last ? `${this.osName(m.os)} · last: ${last.editor}` : this.osName(m.os)
    },
    ago(iso) {
        const now = this.mc ? new Date(this.mc.now).getTime() : Date.now()
        const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000))
        if (s < 60) return `${s}s ago`
        if (s < 3600) return `${Math.floor(s / 60)}m ago`
        if (s < 86400) return `${Math.floor(s / 3600)}h ago`
        return `${Math.floor(s / 86400)}d ago`
    },
    fmtMinutes(seconds) {
        if (seconds > 0 && seconds < 30) return '<1m'
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
        const names = [...(this.panels.options.data?.machines || [])]
        if (state.machine && !names.includes(state.machine)) names.push(state.machine)
        return names
    },
    get projectOptions() {
        const names = [...(this.panels.options.data?.projects || [])]
        if (state.project && !names.includes(state.project)) names.unshift(state.project)
        return names
    },

    fmtDuration: fmt.duration,
    fmtTokens: fmt.tokens,
    fmtUSD: fmt.usd,
    fmtInt: fmt.int,

    setTab(id) {
        this.tab = id
        this.ensureTab()
        // charts on a hidden tab render at zero size; draw them again once their tab shows
        setTimeout(() => {
            if (this.act && (id === 'overview' || id === 'agents')) renderActivity(document.getElementById('activity-chart'), this.act, this.activityMode)
            if (this.tk && id === 'tokens') renderTokenDays(document.getElementById('token-days-chart'), this.tk)
        }, 0)
    },

    // load what the current tab shows, unless it is already loaded (or loading) for the current filters
    ensureTab(force = false) {
        for (const name of [...ALWAYS, ...(TAB_PANELS[this.tab] || [])]) {
            const panel = this.panels[name]
            if (force || (panel.url !== PANELS[name]() && !panel.loading) || panel.error) this.load(name)
        }
    },

    async load(name) {
        const panel = this.panels[name]
        const requestID = (panel.requestID || 0) + 1
        panel.requestID = requestID
        panel.loading = true
        panel.error = null
        const url = PANELS[name]()
        if (panel.url !== url) panel.data = null // never show old-scope data while loading
        panel.url = url
        try {
            const res = await fetch(url)
            if (!res.ok) throw new Error(`HTTP ${res.status}`)
            const data = await res.json()
            if (panel.requestID !== requestID) return
            if (['overview', 'activity', 'projects', 'cost', 'options', 'tokens', 'agents'].includes(name) && data.token_source !== this.tokenSource) throw new Error('Accounting mode changed; reload the page')
            panel.data = data
            window.dispatchEvent(new CustomEvent('dashboard:loaded', { detail: { name, data } }))
        } catch (e) {
            if (panel.requestID !== requestID) return
            panel.data = null
            console.error(`dashboard panel ${name} failed`, e)
            panel.error = String(e)
        } finally {
            if (panel.requestID === requestID) panel.loading = false
        }
    },

    reloadAll() {
        this.ensureTab(true) // in parallel, independently; other tabs reload when shown
    },

    applyFilters() {
        const q = new URLSearchParams(rangeQuery())
        history.replaceState(null, '', `dashboard?${q.toString()}${window.location.hash}`)
        this.reloadAll()
    },

    mounted() {
        window.addEventListener('dashboard:loaded', e => {
            if (e.detail.name === 'activity') setTimeout(() => renderActivity(document.getElementById('activity-chart'), e.detail.data, this.activityMode), 0)
            if (e.detail.name === 'tokens' && this.tab === 'tokens') setTimeout(() => renderTokenDays(document.getElementById('token-days-chart'), e.detail.data), 0)
        })
        window.addEventListener('hashchange', () => {
            const id = window.location.hash.slice(1)
            if (id !== this.tab && TABS.some(t => t.id === id)) this.setTab(id)
        })
        this.reloadAll()
        setInterval(() => this.load('machines'), 60_000) // keep "live" and "last seen" current
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
