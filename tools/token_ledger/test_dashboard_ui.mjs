import test from 'node:test'
import assert from 'node:assert/strict'

globalThis.dashboardTimeZone = 'America/Toronto'
globalThis.dashboardTokenSource = 'ledger'
globalThis.PetiteVue = { reactive: value => value }
globalThis.window = { location: { search: '', hash: '' }, addEventListener() {}, dispatchEvent() {} }
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
