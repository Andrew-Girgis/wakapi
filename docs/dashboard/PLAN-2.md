# Dashboard and token ledger — plan 2 (27 Oct 2026)

Follows [PLAN.md](PLAN.md) (phases 0–7, merged) and [TOKEN_LEDGER_ROLLOUT.md](TOKEN_LEDGER_ROLLOUT.md)
(ledger in production since 13 Oct, provisional). Tracking issue: #1.

## Findings that drive this plan (audit of 26 Oct 2026)

1. The whole token ledger is not in Git (Go ledger model/service/API, ledger pricing, `tools/token_ledger/`,
   three docs, edits to 10 tracked files). The working tree equals production: served JS hashes match,
   Python digest equals release `v9-c0987224c84c`. Mnemosyne's `bin/compose`, `compose.yaml`,
   `bin/dashboard-source`, `deploy/token-dashboard-source` are also uncommitted.
2. After the 23–24 Oct outage, `wakatime-cli --sync-ai-activity` stayed in backoff (`backoff_retries = 7`)
   and the parse cutoff stopped at 24 Oct 04:32 UTC, while the timer still exited 0. Heartbeats for today
   reached the server only after a manual run. Nothing alerted.
3. `wakapi-token-ledger-observation` runs at boot (`Persistent=true`) before DNS works and fails; no
   retry until the next day. Reports for 23–26 Oct were missing until re-run by hand.
4. `Pi-coding-agent` heartbeats have no `ai coding` category, so the dashboard counts them as "You"
   and never warns when Pi goes silent.
5. Pricing gaps (last 7 days): Claude cache writes (no `cache_write_5m` / `cache_write_1h` rates),
   `gpt-6.1-sol` (no rate), `codex-auto-review` (internal, no public rate).
6. Ledger billing attribution is "unknown", although heartbeats record `ai_subscription_plan` per
   `ai_session`, and ledger `session_key` equals heartbeat `ai_session` for Claude, Codex and Pi.
7. Phase 8 is not done: the tabs change only the highlight. Issue #1 sections not built: agent filter,
   daily average and streak, cost per hour of coding, reasoning effort, prompts per session, tokens per
   AI line change, rhythm heatmap, languages/categories/editors.

## Rules (unchanged from the ledger contract)

- Never change heartbeat ingestion, original tables or the tracking endpoints. Additive only.
- Never change files hashed by `gate.implementation_digest()` (`sources.py collect.py audit.py
  backfill.py observe.py daily.py gate.py`) in this plan: that restarts the reliability window.
  `install.py` is not hashed.
- No guessed prices. Rates come from the providers' public price pages and are confirmed by the user.
- No inference of billing from CLI/model names. Attribution only from recorded `ai_subscription_plan`.
- Each workstream: branch → tests → merge into `master` (same pattern as phases 1–7).

## W0 — Preserve the work (no behaviour change) — DONE locally

wakapi `ledger-v9` (4 commits) merged into `master`; mnemosyne token-source switch committed on `main`.
Before any push: scan the new commits for credentials and transcript-derived data.

## W1 — Reliability

1. **Observation job waits for DNS, no restart loop.** A failed observation writes a failed report that
   `gate.py` counts forever, so no `Restart=`. Instead `ExecStartPre` waits up to 10 min for the API host
   to resolve (`TimeoutStartSec=15min`); `daily.py` already catches up missed windows on the next run.
   Applied as a systemd **drop-in** (`…observation.service.d/10-wait-dns.conf`), so the generated units
   and the read-only v9 release stay untouched; `install.py` gets the same lines for future installs
   (+ test in `test_rollout.py`). Rollback = delete the drop-in.
2. **Pi heartbeats are agent time (Pi only).** `isAgentHeartbeat` = category `ai coding` OR
   `AgentGroup(editor) == Pi`. Before/after counts by (editor, category) recorded. Pi silent warnings
   start working. Unit test.
3. **Server-side stall detection** (after W1.2). `/api/dashboard/machines` gains per (machine, agent group)
   last agent heartbeat vs last counted ledger event (`occurred_ms`, machine via alias resolution),
   excluding `wakatime` fallback, held events and copied-alias events. Warning when the ledger is more
   than 60 min ahead. Pi excluded from the rule while wakatime-cli fails to parse Pi transcripts.
4. **Local watchdog** (mnemosyne `deploy/`, hourly user timer): if `ai_logs_last_parsed_at` is more than
   60 min older than the newest Claude/Codex transcript mtime, `systemctl --user start
   wakatime-ai-sync.service` (serialised with the timer, never wakatime-cli directly); if still stale on
   the next check, `notify-send` + journal. Reads, never edits, wakatime-cli's files.

## W2 — Pricing coverage (rates from the providers' pages, user-confirmed)

1. `cache_write_5m` / `cache_write_1h` per Anthropic model, as listed per model (not multipliers).
   Identical rates on both Fable 5 rows (`fable`, `claude-fable` normalise to the same key; conflicting
   duplicates fail closed) — test for it. Fix Sonnet 5.5 cache read 0.20 → 0.10 (price page).
2. `gpt-6.1-sol`: input 2.00, cached 0.10, output 10.00. `codex-auto-review` stays unpriced.
3. Pi/OpenCode cache writes without a TTL split stay unpriced (documented). Update LEDGER_PRICING.md.

## W3 — Recorded-plan attribution (needs contract amendment, user-approved)

1. `CountedEvents` selects `session_key`; session references of copied events are loaded too.
   `/usage/summary` and `/dashboard/ledger` JSON stay byte-identical (observer parity) — Go test.
2. Plan lookup: heartbeats by `user_id` (indexed) with `ai_session IN (…)` in chunks, **no time bound**
   (attribution never depends on the selected range), no new index (schema unchanged).
3. A session is "recorded plan X" only if all its heartbeats with that `ai_session` from the matching tool
   carry the same non-empty plan; otherwise "unknown". Copied events: all references must agree.
   WakaTime fallback: always unknown. Never "pay per token".
4. Cost rows split by (model, recorded plan). UI says "recorded plan: pro" and "plan unknown" — not
   "covered", not "pay per token" (template lines with that text rewritten). ACCOUNTING.md and
   LEDGER_PRICING.md amended.

## W4 — Phase 8 tabs and issue #1 sections

1. **Tabs.** `v-show` per tab section; panels fetch on first show (hash routing already exists).
2. **Agent filter** `agent=` over agent groups (Claude Code, Codex, Pi, OpenCode): heartbeats via
   `AgentGroup` for agent heartbeats only; ledger via `ledgerGroup` inside the existing loop. WakaTime
   fallback rows drop out under a filter; UI notes that filtered totals exclude fallback.
3. **Overview additions.** Daily average (active days in range), streak (looks back before the range up to
   365 days), rhythm heatmap (weekday × hour, agents vs you), compact bars for languages, categories,
   editors. Endpoint `/api/dashboard/breakdown`.
4. **Tokens & cost tab.** Daily stacked tokens by bucket; cache-hit ratio per day; cost by model (all rows)
   and by project; cost per hour of agent time; recorded-plan vs unknown.
5. **Agents tab.** Per agent group: time, tokens, sessions, median session, prompts per session
   (heartbeats with `ai_prompt_length > 0` per `ai_session`), tokens per AI line change (only when line
   changes > 0), time by model version × reasoning effort. Endpoint `/api/dashboard/agents`.
6. **Projects tab.** Full sortable table. **Machines tab.** Cards + per-agent table + W1.3 health.
7. Tests: Go unit tests per aggregation; `test_dashboard_ui.mjs` for tab switching; screenshots at
   1586 px and 390 px.

## W5 — Deploy and verify (stage 1: W1–W3, stage 2: W4)

Not within an hour of 07:15 UTC. `mnemosyne-backup.service` + `PRAGMA integrity_check` on the copy; tag
running image `pre-<stage>`; build; `validate_image.py`; `bin/compose up -d --no-deps --build wakapi`;
check the pinned display mode; authenticated API checks (frozen cutoff still 4,323,534,508;
`/usage/summary` == `/dashboard/ledger`); browser check of every tab; next observation report passes.
Rollback: `bin/dashboard-source heartbeats` (display only, same image) → retag `pre-<stage>` +
`bin/compose up -d --no-build wakapi`; W1 drop-in/watchdog removal.

## Not in this plan

- WakaTime relay switch-off (user decision after parity).
- Pythia: offline; collector/watchdog/gate run there later (commands documented).
- wakatime-cli's own Pi parse errors (upstream binary; the ledger is the token source).
