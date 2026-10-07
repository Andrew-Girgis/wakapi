# Dashboard rebuild — plan and build instructions

Tracking issue: [#1](https://github.com/Andrew-Girgis/wakapi/issues/1). Reference design: [`mockup.png`](mockup.png).

Goal: replace `/summary` (e.g. `https://omphalos.tail626874.ts.net:8443/summary?interval=any`) with the
"Activity" dashboard in the mockup. The old page stays available at `/summary/classic` until the new one
has parity, then remains as a fallback.

## 1. Decisions

| Topic | Decision | Why |
| --- | --- | --- |
| Where | Inside this fork, new route `/dashboard`; `/summary` redirects to it at the end (phase 7) | One service, one login, data already here. A separate SPA would add a second deploy and auth. |
| Rendering | Go template page shell + one JSON endpoint per panel under `/api/dashboard/*`, fetched by petite-vue | Panels load independently and fail independently; endpoints are testable in Go; the shell paints instantly. |
| Auth | Existing `AuthenticateMiddleware` (already accepts the web session cookie and API keys) | No new auth code. |
| CSS | Tailwind 3 (existing `yarn build:tailwind`), dark only for this page, `tabular-nums` for figures | Already in the build; mockup is dark. |
| Interactivity | petite-vue (already vendored) | Tabs, dropdowns, toggles, fetch + render. No bundler. |
| Charts | Chart.js 4 (already vendored) — stacked bars, floating horizontal bars for the timeline | Every chart in the mockup is a bar or a sparkline. |
| Sparklines | Inline SVG `<polyline>` built in JS (~20 lines) | Dozens of tiny charts; cheaper and crisper than Chart.js instances. |
| Icons | Iconify (already vendored) | — |
| Data | Time computed from heartbeats (not Wakapi durations); new heartbeat queries for tokens, sessions, liveness | Upstream durations drop agent "thinking time" heartbeats (`type=app`, `category=ai coding`, upstream #964), which are most of an agent-driven week: classic page shows 4 h 47 m for 7 days where WakaTime shows 23 h 38 m. |
| Cost | Local price file, no network lookups | Self-hosted rule. |
| Project status | Wakapi project labels (`status:active`, `status:paused`, `status:done`); optional Mnemosyne integration behind a config flag | Keeps the fork self-contained (goal rule, 7 Oct 2026). |

Rejected: React/Svelte SPA (second toolchain, diverges from upstream style), D3 (not needed for bars),
Grafana (cannot read Wakapi's model without a datasource plugin; harder to make it look like the mockup).

## 2. Panel → data map

All panels respect the header filters: range (Today / 7 days / 30 days / All time / custom), project, machine.

| Panel (mockup) | Endpoint | Computation | Source |
| --- | --- | --- | --- |
| Coding time + "vs previous" | `GET /api/dashboard/overview` | union of all durations in range; same for the previous range of equal length | DurationService |
| AI coding %, You, Agents, overlap | `overview` | `agents` = union of durations with category `ai coding`; `you` = union of the rest; `ai%` = agents ∪ / total ∪; overlap = you ∩ agents | DurationService |
| Tokens, % cached | `overview` | `SUM(ai_input_tokens)`, `SUM(ai_cached_input_tokens)`, `SUM(ai_output_tokens)` | new `HeartbeatRepository.GetTokenStats` |
| Estimated API value | `overview` | tokens × price per (model, version) | new `PricingService` + `ai_prices.yml` |
| "Subscription usage · not billed spend" | `overview` | split by `ai_subscription_plan` | **needs that field stored first** |
| Activity this week (Time / Tokens toggle) | `GET /api/dashboard/activity` | per day × agent group: union of durations (time) or token sums | DurationService, GetTokenStats |
| Agent share bars | `activity` | share of total time per agent group | same |
| Sessions / day, median session | `activity` | distinct `ai_session` per day; session length = sum of heartbeat gaps ≤ user timeout (not first-to-last) | new `GetSessionStats` |
| Today's timeline | `GET /api/dashboard/timeline?date=` | durations for one day, rows = project, colour = agent group, window = first→last activity rounded to hours | DurationService |
| Peak concurrency | `timeline` | sweep line over `ai coding` durations | DashboardService |
| Machines + "N machines live" | `GET /api/dashboard/machines` | last heartbeat per (machine, editor); live = < 5 min | new `GetLastSeen` |
| Silent-agent warning | `machines` | (machine, agent) with ≥ 20 heartbeats in last 30 days and none in last 3 days | same |
| Projects table | `GET /api/dashboard/projects` | per project: time (union), est. cost, daily time series for sparkline, status | DurationService, GetTokenStats, Mnemosyne |

Agent groups (config, default): `Claude`, `Claude code` → **Claude Code**; `Codex`, `Codex-cli`, `Codex-vscode` → **Codex**;
`Pi`, `Pi-coding-agent` → **Pi**; `Opencode`, `Opencode cli` → **OpenCode**; everything else → **Other**.
Machine names are mapped with Wakapi's existing machine aliases
(`Mac`, `Mac.localdomain`, `Pythia.local`, `Andrews-Macbook-Air.local` → `Pythia`).

## 3. File layout

```
routes/dashboard.go                      page handler: GET /dashboard (+ /summary redirect in phase 7)
routes/api/dashboard.go                  JSON: overview, activity, timeline, machines, projects
services/dashboard.go                    DashboardService: assembles panels from the services below
services/dashboard_intervals.go          union / intersection / peak concurrency of durations (pure functions)
services/pricing.go                      PricingService: loads ai_prices.yml, cost(model, version, tokens)
repositories/heartbeat.go                + GetTokenStats, GetSessionStats, GetLastSeen
models/view/dashboard.go                 response structs (one per endpoint)
config/config.go                         + dashboard.agent_groups, dashboard.mnemosyne_url, pricing file path
views/dashboard.tpl.html                 page shell: header, tabs, panel containers
static/assets/js/dashboard/app.js        petite-vue root: filters, tabs, fetch
static/assets/js/dashboard/charts.js     Chart.js builders (stacked bars, timeline), SVG sparkline
etc/ai_prices.example.yml                price file example (operator fills real prices)
```

## 4. Phases (each = one branch + PR into fork `master`, tests + screenshot before merge)

**Phase 0 — prerequisites**
1. Deploy `7683e5f` (model version) and `cbe6f83` (unknown fields) to Omphalos.
2. Keep the WakaTime relay on until parity (decided). Strip `wakatime_api_key` from every DB copy used for testing.
3. Store `ai_subscription_plan` (same pattern as `ai_cached_input_tokens`).
4. Machine aliases for Pythia's hostnames.
5. `ai_prices.yml` with prices for the model versions in the data (`SELECT DISTINCT ai_model, ai_model_version`).

**Phase 1 — backend**: interval maths, token/session/last-seen queries, pricing, five endpoints.
Acceptance: unit tests for union/intersection/concurrency/session length/cost; repository tests on SQLite;
each endpoint < 300 ms for 7 days on the live data copy; JSON shapes documented in `models/view/dashboard.go`.

**Phase 2 — shell**: `/dashboard` page, header (title, date range, live dot), tabs (only Overview active),
project/machine filters, KPI row. Acceptance: KPI numbers match `overview` JSON; filters change the URL and
survive reload.

**Phase 3 — Activity this week**: stacked bars per agent group, Time/Tokens toggle, share bars, sessions.

**Phase 4 — Today's timeline**: floating horizontal bars, legend, peak concurrency, date stepper.

**Phase 5 — Machines**: cards, live dots, "last seen" ages, silent-agent warning.

**Phase 6 — Projects**: table, status from Mnemosyne (optional), cost, sparklines, "View all".

**Phase 7 — switch**: `/summary` → `/dashboard`, old page at `/summary/classic`, link in the menu.
Acceptance: `?interval=any` and existing query params still work.

**Phase 8 — remaining tabs**: Tokens & cost, Agents, Projects, Machines (detail views of the same endpoints).

## 5. Dev loop and verification

- Go: `docker run --rm -v "$PWD":/src -w /src -v wakapi-gomod:/go/pkg/mod -v wakapi-gocache:/root/.cache/go-build golang:1.27-alpine go test ./...`
  (the upstream `TestLoginHandlerTestSuite` fails on pristine upstream too; ignore it).
- CSS: `docker run --rm -v "$PWD":/src -w /src node:22-alpine sh -c "yarn && yarn build"`.
- Run: copy the live DB, **clear `users.wakatime_api_key` in the copy**, start the fork image on `127.0.0.1:3199`
  with `WAKAPI_ENV=dev` (templates reload on each request) and `views/`, `static/` mounted.
- Visual check: screenshot `/dashboard` at 1586 px and 390 px wide (Claude in Chrome) and compare with `mockup.png`.
- Deploy: `systemctl --user start mnemosyne-backup.service`, then
  `bin/compose up -d --no-deps --build wakapi` in `~/Projects/mnemosyne`.

## 6. Known data caveats to show on the page

- Cached tokens are missing between 6 Aug 2026 and the `c4e5b37` deploy (7 Oct 2026 01:24). Before 6 Aug they
  are inside `ai_input_tokens`. A transcript backfill (separate task) fixes this; until then show a coverage note.
- Cost is an estimate at API prices; subscription use is not billed per token.
- "You" vs "Agents" overlap: the two numbers add up to more than the total on purpose.

## 7. Decisions from review (7 Oct 2026)

1. **WakaTime relay:** keep it on until this page reaches parity with the WakaTime dashboard, then turn it off.
2. **Project status:** superseded by the build goal: Wakapi project labels by default, Mnemosyne optional behind a config flag.
3. **Agent groups:** Claude Code, Codex, Pi, OpenCode, Other — each with its own colour.
4. **Prices:** filled once by hand from the providers' public price pages into `ai_prices.yml`, checked by the user.
