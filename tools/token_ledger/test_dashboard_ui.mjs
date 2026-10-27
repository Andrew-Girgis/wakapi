import test from 'node:test'
import assert from 'node:assert/strict'

globalThis.dashboardTimeZone = 'America/Toronto'
globalThis.dashboardTokenSource = 'ledger'
globalThis.PetiteVue = { reactive: value => value }
globalThis.window = { location: { search: '', hash: '' }, addEventListener() {}, dispatchEvent() {} }
globalThis.document = { getElementById: () => null }
globalThis.CustomEvent = class { constructor(type, options) { this.type = type; this.detail = options.detail } }
const { app, state, rangeQuery } = await import('../../static/assets/js/dashboard/app.js')

test('scope includes model; comparison uses explicitly legacy tokens', () => {
 state.model = 'claude-opus-4-6'; state.project = 'wakapi'; state.machine = 'Pythia'
 assert.equal(new URLSearchParams(rangeQuery()).get('model'), state.model)
 app.panels.overview.data = { tokens: { total: 4323534508 }, legacy_tokens: { total: 576000000 } }
 assert.equal(app.ledgerDelta, 3747534508)
})

test('token shares use tokens, not heartbeat durations', () => {
 app.activityMode = 'tokens'
 app.panels.activity.data = { groups: ['Pi', 'Codex'], days: [{ tokens: { Pi: 900, Codex: 100 } }], share: [{ group: 'Pi', seconds: 1, percent: 1 }] }
 assert.equal(app.shareRows[0].percent, 90)
 assert.equal(app.shareRows[1].percent, 10)
})

test('older request cannot replace newer data or errors', async () => {
 let resolve
 globalThis.fetch = () => new Promise(r => { resolve = r })
 const older = app.load('overview')
 globalThis.fetch = async () => ({ ok: true, json: async () => ({ token_source: 'ledger', tokens: { total: 7 } }) })
 await app.load('overview')
 resolve({ ok: false, status: 500 })
 await older
 assert.equal(app.ov.tokens.total, 7)
 assert.equal(app.panels.overview.error, null)
 assert.equal(app.panels.overview.loading, false)
})

test('accounting mismatch is unavailable, never silent heartbeat fallback', async () => {
 globalThis.fetch = async () => ({ ok: true, json: async () => ({ token_source: 'heartbeats', tokens: { total: 576000000 } }) })
 const old = console.error; console.error = () => {}
 try { await app.load('overview') } finally { console.error = old }
 assert.equal(app.ov, null)
 assert.match(app.panels.overview.error, /Accounting mode changed/)
})

test('agent filter is part of the scope', () => {
 state.agent = 'Codex'
 assert.equal(new URLSearchParams(rangeQuery()).get('agent'), 'Codex')
 state.agent = ''
 assert.equal(new URLSearchParams(rangeQuery()).has('agent'), false)
})

test('a tab loads its own panels once, other tabs stay unloaded', async () => {
 const urls = []
 globalThis.fetch = async url => { urls.push(url.split('?')[0]); return { ok: true, json: async () => ({ token_source: 'ledger', projects: [], machines: [], warnings: [] }) } }
 for (const p of Object.values(app.panels)) { p.url = undefined; p.data = null; p.error = null }
 app.setTab('agents')
 await new Promise(r => setTimeout(r, 0))
 assert.deepEqual([...urls].sort(), ['api/dashboard/activity', 'api/dashboard/agents', 'api/dashboard/machines', 'api/dashboard/options', 'api/dashboard/overview'])
 urls.length = 0
 app.setTab('agents')
 await new Promise(r => setTimeout(r, 0))
 assert.deepEqual(urls, []) // already loaded for this scope
 app.setTab('projects')
 await new Promise(r => setTimeout(r, 0))
 assert.deepEqual(urls, ['api/dashboard/projects'])
})

test('projects tab sorts every row by the chosen column', () => {
 app.tab = 'projects'
 app.panels.projects.data = { projects: Array.from({ length: 8 }, (_, i) => ({ name: 'p' + i, seconds: i, tokens: 8 - i, cost: 0 })) }
 assert.equal(app.projectRows.length, 8)
 assert.equal(app.projectRows[0].name, 'p7') // default: time, descending
 app.sortProjects('tokens')
 assert.equal(app.projectRows[0].name, 'p0')
 app.sortProjects('tokens')
 assert.equal(app.projectRows[0].name, 'p7') // second click flips
 app.tab = 'overview'
 assert.equal(app.projectRows.length, 6)
})

test('value per agent hour divides by agent time only', () => {
 app.panels.overview.data = { time: { agent_seconds: 7200 }, cost: { available: true, estimated_api_value: 30 } }
 assert.equal(app.costPerAgentHour, 15)
 app.panels.overview.data = { time: { agent_seconds: 0 }, cost: { available: true, estimated_api_value: 30 } }
 assert.equal(app.costPerAgentHour, null)
})
