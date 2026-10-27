# Token usage ledger — rollout and validation log

**Status (13 Oct 2026): ledger is the primary dashboard token source, clearly
labelled provisional.** Collectors and verification schedules remain live on both
hosts. Heartbeat tracking, activity time and original data are unchanged. Display
promotion does not certify missing lifetime history or historical paid spend.

## Immutable baseline and restore point

Both hosts have private, SHA-256-manifested history snapshots under
`~/.local/share/wakapi-token-repair/20261011T230012Z/`. On Pythia,
`report/baseline.json` contains the schema fingerprint and the verified,
restorable Wakapi/Mnemosyne database copies. Omphalos has a separate fresh
online database snapshot in `db/latest/`. The Mac copy has **32,674 heartbeats,
467,898,128 heartbeat tokens** at its fixed snapshot time. These directories
contain private transcripts and real credentials; never check them in, publish
them, or pass them to an LLM. Check `PRAGMA integrity_check` on a restored copy.
Do not restore this historical production snapshot to roll back a dashboard
change: it would lose subsequent heartbeats.

## Additive implementation

- `tools/token_ledger/sources.py`: read-only adapters for Claude, Pi, Codex,
  OpenCode; cached/read/write/output semantics and stable source identities.
  `wakatime.py` reads a private historical export. It adds only per-session
  fallback observations when no transcript usage references that session.
- `tools/token_ledger/audit.py`: source audit and *offline-only*, explicit
  `*.rehearsal.sqlite3` ledger import; existing heartbeat rows left untouched.
- `tools/token_ledger/collect.py`: one-shot local persistent queue, including
  copied-session aliases; network delivery requires an explicit `--send`.
  Audited bulk delivery checkpoints only acknowledged batches. New five-minute
  collector jobs are installed without replacing the original tracking jobs.
- `models/token_usage.go`, `services/token_usage.go`,
  `routes/api/token_usage.go`: additive Go ledger model, idempotent ingest,
  conflict quarantine, and authenticated shadow summary. Main's startup
  migration creates new tables only. Running in production in shadow mode.
- The canonical total is **input + cache reads + cache writes + unknown input + output**;
  reasoning is metadata within output. Codex's inclusive input is split into
  uncached input and cache-read/write buckets. OpenCode's two reasoning
  conventions are selected using its own reported total. Input with an
  unverified cache split is recorded separately and must not be priced as
  new input. Copied Pi sessions use one counted event and multiple session
  references, preventing false WakaTime fallback on the copied session.

Go `POST /api/usage-write/events` requires a full-access Wakapi key and accepts
at most 200 events / 2 MiB. Go `GET /api/usage/summary?from_ms=...&to_ms=...`
is authenticated. Neither route changes ordinary heartbeats. Dashboard token
panels now select the ledger consistently; `/api/dashboard/ledger` supplies fresh
provenance/verification figures. Unknown input and the explicitly labelled legacy
heartbeat comparison remain visible. Verified [partial ledger pricing](LEDGER_PRICING.md) is now deployed after separate
bucket/rate reconciliation, never inferred from the old heartbeat estimate.

## Current v9 validation

- Fixed cutoff: `1791759696000` ms, applied to both transcripts and WakaTime.
  Frozen audited total: **4,323,534,508 tokens** = **4,188,596,128 transcript
  tokens + 134,938,380 attributed WakaTime fallback tokens**. This is accounted
  source usage, **not a guarantee that every lifetime request survives**.
- **Zero blocking numeric exceptions**, one retained OpenCode classification
  note: source total 57,554 is corroborated by its step-finish record, while
  output/reasoning classification differs by 74. Reasoning is never added twice.
- Codex interleaved counters reconstruct a complete parent-linked fork only
  with verified predecessors and one root. Newer per-response records must
  reconcile to each thread/turn running counter and cover the snapshot usage.
  Format selection is consistent even before a fork appears. Collector and
  server pin each session's accounting representation; changes stop delivery
  rather than count a second set of IDs. Live and archived files are combined.
- A **fresh isolated database**, initially containing only the old tables, was
  migrated and backfilled **through the authenticated HTTP API**, not by a
  live SQLite writer: **58,905 observations, 37,628 session references**;
  **37,993 counted events**. All **59,588 queued observation/alias receipts**
  matched the ledger with no missing records, mismatches, or conflicts. Its
  summary matched the independent audit exactly. Original schema and all
  heartbeat aggregates were unchanged; database integrity passed.
- `backfill.py` requires the passing audit's exact evidence digest, parser,
  source buckets and total. It defaults to queue-only; sending is explicit.
  Queue checkpoints are tied to one endpoint and HTTP redirects never carry
  authorization. `observe.py` checks source fingerprints and session aliases
  against local sent checkpoints, server receipts, and shadow-preview parity.
- Both live collectors and observers passed on the isolated server. No old
  heartbeat/sync job or tracking endpoint has been replaced. Tests include
  append-after-prefix, incompatible modes, missing records, authentication,
  body limits, backfill evidence mismatch, queue endpoint binding and the
  seven-day gate. The previously known full-Go login failures remain unrelated;
  targeted models/services/API tests pass.
- `install.py` generates **new** launchd/systemd jobs. `--send` and `--enable`
  must both be requested for unattended delivery. `daily.py` catches up fixed
  UTC windows after wake/restart. `gate.py` requires successful reports from
  both hosts on seven consecutive UTC days with one unchanged implementation,
  and at least one verified observation from each of the four tools during the
  period. Elapsed time or an inactive tool is not validation. It cannot switch
  the dashboard.

### Production shadow verification

- Fresh online pre-deployment backup: **33,446 heartbeats**, integrity passed.
  All pre-deployment heartbeat fields and original schema remained unchanged
  after migration; new tracking rows continued arriving.
- Historical API delivery: **59,588 queue rows**, no conflicts or pending rows;
  all receipts matched. Production frozen summary: **4,323,534,508 tokens**,
  **37,993 counted events**, **81,182,367 unknown-input tokens**, zero quarantined
  tokens. Evidence: private `report/production-backfill-v9-verification.json`.
- Both hosts' live-window observers passed with no missing source events,
  checkpoint mismatches, unsent rows, or receipt conflicts; preview/summary
  parity passed. Both new OS jobs were verified with successful exits. The old
  `wakatime-ai-sync.timer` remains enabled; Linux user-manager lingering is on.
  No original heartbeat job, endpoint, or authoritative total was replaced.
- Initial image startup failed because a protected build copy had mode 700,
  incompatible with the nonroot image user. The original image was immediately
  restored before any ledger migration/write. Packaging explicitly sets mode
  0755. `validate_image.py` now requires actual container health, authenticated
  ledger API success, created tables, heartbeat checksums and integrity on a
  new pre-ledger backup copy; a restart loop fails closed.
- Simultaneous collector/observer startup exposed lock contention. Observation
  jobs now retry only temporary lock contention (exit 75), never real accounting
  errors. Both host jobs then passed. **31 Python tests**, targeted Go tests,
  JavaScript syntax, and whitespace checks pass.

Private collector release on both hosts:
`~/.local/share/wakapi-token-ledger/releases/v9-c0987224c84c/` (read-only).
`release.json` pins its digest, interpreter, cutoff, and first observation day.
Queues, count-only reports, logs and saved previous jobs remain outside Git.

**Background reliability milestone (NOT a provisional-display gate):** seven
complete UTC days from **13 October 2026
00:00 UTC** through **20 October 2026 00:00 UTC**. Daily jobs run at 03:15 local
on Pythia and 07:15 UTC on omphalos. First completed-day reports are due
14 October; the first eligible gate evaluation is after both 20 October jobs.
If a tool is inactive, a window is missing, a discrepancy is unresolved, or an
implementation changes, reliability certification remains withheld. The operator
approved provisional promotion without seven elapsed days; the independent
checks continue. There is no lifetime-coverage certification from elapsed time alone.

Collect the private remote count-only reports and evaluate from the installed
release (not a changing working tree):

```bash
STATE="$HOME/.local/share/wakapi-token-ledger"
mkdir -p -m 700 "$STATE/observations/omphalos"
rsync -a girgie@omphalos:.local/share/wakapi-token-ledger/observations/ "$STATE/observations/omphalos/"
cd "$STATE/releases/v9-c0987224c84c"
python3 -m tools.token_ledger.gate --report-dir "$STATE/observations" --machine Pythia --machine omphalos --from-ms 1791849600000
```

Rollback without losing subsequent tracking: disable only the **new** collector
and observation jobs, tag `wakapi-fork:pre-token-ledger-v9` back to
`wakapi-fork:local`, and recreate only Wakapi using `bin/compose up -d --no-build
wakapi`. Retain the additive tables/queues and do **not** restore an old database.
The protected pre-shadow backup and image manifest are in `pre-shadow-v9/`.

Private current evidence: `report/audit-v9.json` and the `server-v9` isolated
copy. Bounded classification notes remain visible; input with unknown cache
split remains unpriced. Changing the parser or an accounting representation
requires a fresh rehearsal, an audited queue/ledger migration, and a restarted
observation window. Never silently requeue an old parser's sent records.

## Provisional primary-display promotion

Initial token-only image: `wakapi-fork:ledger-dashboard-v1`; current validated
image: `wakapi-fork:ledger-priced-v1` (see [pricing evidence](LEDGER_PRICING.md)). A fresh production backup
was retained under `pre-cutover-final/`; full schema and every pre-cutover
heartbeat field remained unchanged. Independent SQL and all-time/day/project/
machine/model API scopes reconciled on a current database copy. Frozen audit
cutoff still returns **4,323,534,508 tokens**. Browser verified the same image
against production API data: **4,500,011,093 accounted tokens** at the verification
time, provisional labels, **81,182,367 unknown-input tokens**, and no panel errors.
Costs were initially pending, not substituted with the legacy estimate.

Persistent non-secret mode: Mnemosyne `deploy/token-dashboard-source`.
`bin/compose` injects `WAKAPI_DASHBOARD_TOKEN_SOURCE` from that file. Both commands
were exercised on production with the **same image/database**, healthy startup,
and authenticated verification of the selected mode:

```bash
cd ~/Projects/mnemosyne
bin/dashboard-source heartbeats   # reversible legacy display
bin/dashboard-source ledger       # primary provisional ledger
```

Neither command rebuilds or restores a database; only Wakapi is recreated.
Failure restores the previous selection. The actual page pins the mode, rejects
mixed-mode responses, and never silently substitutes heartbeat or zero totals.
The isolated browser harness uses trusted-header auth only on a loopback copy;
production auth was not loosened and API keys never reach the browser.

## Earlier v7 rehearsal (superseded)

Frozen v7 report: **36,862 transcript events = 4,178,436,933 tokens**;
**23,014 distinct WakaTime observations = 2,157,490,590 raw tokens**. Of
these, **1,054 observations / 309,850,680 tokens** have missing or non-target
editor attribution and are excluded (not inferred to belong to one of the
four tools). The remaining 21,960 / 1,847,639,910 have target-tool
attribution; 1,712,701,530 of those tokens overlap sessions covered by
transcripts. **1,048 fallback observations / 134,938,380 tokens** are
provisionally counted, giving a **NOT-certified** ledger total of
**4,313,375,313**. Of the counted amount,
81,182,367 tokens have an unknown input/cache split and cannot be priced as
new input. Every counted fallback has a named target-tool editor; 88
unattributed export rows found in the earlier scan matched transcript sessions
and were already excluded by the session rule. The export and its
user-agent/machine metadata are in the private
Pythia `wakatime-export/`; the fixed UTC cutoff is `1791759696000` ms.

On a fresh, private `*.rehearsal.sqlite3` database copy, **58,822 events
and 37,545 session references** imported with no conflicts. Identical replay
inserted or updated **zero** events and references. Direct SQL excluding
transcript-overlap returned **4,313,375,313**. No session is
currently quarantined; the quarantine mechanism remains in place for a future
unmatched outlier. A prior v6 rehearsal independently proved that a suspect
WakaTime-only session is held in full, not partly counted.
The original schema, 32,674 heartbeat aggregate, and `PRAGMA integrity_check`
matched the snapshot. An isolated Go server on omphalos successfully migrated
a NEW writable v7 copy and returned identical all-time and machine-filtered
summary values via both authenticated APIs. Prior isolated copies also passed
project filtering and held-session totals. Its original schema and heartbeat
aggregate stayed unchanged. The original dashboard API still responded. Both
hosts' v7 collectors delivered 345 and 340 post-cutoff events respectively to
this **isolated server**, zero conflicts; replay after an intentionally lost ACK
left the total unchanged. **No delivery to production occurred.** Fifteen
Python tests, targeted Go model/service/API tests, and JS syntax passed. The
full Go suite still has the previously known `TestLoginHandlerTestSuite`
failures.

**V7 had three historical exceptions (resolved/bounded in v9 above):** two Codex
sessions with non-monotonic cumulative counters and one OpenCode record whose
reported total requires a 74-token output correction. They remain in the
private `report/rehearsal-v7.json`. One Codex exception session has 31,397,652 parsed
transcript tokens versus 79,842,597 WakaTime tokens; the other has 44,314,618
versus 33,946,052. These figures are **not interchangeable**, because
heartbeats may sample a different set of events; neither the difference nor
its sign proves missing usage. A single WakaTime observation in the first
Codex session is 24.4M tokens and may itself be cumulative rather than one
request. Another 36,962,754-token WakaTime session previously appeared
uncovered because a copied Claude transcript lost its second session key
inside the adapter. V7 retains both session references, excludes those 82
heartbeats from fallback, and verifies the corresponding original transcript
contains 417 assistant usage records. Codex's documented `total_token_usage` is
cumulative, while `last_token_usage` can be repeated on a rate-limit-only
update: do not blindly sum it. Resolve or explicitly bound the discrepancies
against independent per-turn evidence before backfill or production deploy.
See [Codex token accounting](https://github.com/openai/symphony/blob/main/elixir/docs/token_accounting.md)
and [the duplicate-last-usage issue](https://github.com/openai/codex/issues/14489).

The first isolated-server attempt failed on SQLite side-file permissions; the
new isolated writable copy resolved it. Production volume permissions were
never touched.

## Historical rollout checklist (superseded by provisional-display contract)

See [ACCOUNTING.md](ACCOUNTING.md). Numeric/replay/auth/schema/browser checks
remain mandatory; seven days is a background reliability milestone, not a wait
before a clearly provisional display.

1. **Accounting:** Independent per-source sampled totals, missing/zero
   distinction, resets, fork/cross-machine duplicate tests; resolve or
   explicitly bound every exception. Verify Mac+omphalos coverage by day.
2. **Test server (passed for v9):** A NEW private writable database copy,
   import disabled and local-only port passed migration, authenticated ingest,
   replay, revisions, conflicts, rejected input/auth, SQL reconciliation,
   and unchanged original schema and heartbeat aggregate. Repeat after any
   migration/API changes; never repair test permissions by changing the live
   production volume.
3. **Collector:** Test live-source formats, bounded cutover, queue persistence,
   lost ACK, auth failure, restart/sleep, source correction, concurrent lock,
   rate/backlog and both hosts. Run with `--send` against the isolated service
   first (v9 delivery and receipt verification passed on both hosts). Never put API keys in
   command lines or logs; `collect.py` reads the
   locally protected WakaTime config.
4. **Rollout:** Deploy only after backup and isolated tests; migrate additive
   ledger tables, turn on both collectors from an audited UTC cutoff, observe
   existing heartbeat traffic and relay. Backfill verified history with
   provenance; do not use a one-off live SQLite writer. Run the ledger preview
   in shadow mode with direct SQL reconciliation and measured coverage notices.
5. **Seven-day observation:** daily frozen-window reconciliation of source →
   queue → ledger → preview for every tool/machine; exercise inactive tools
   with controlled test usage; no unexplained discrepancy or tracking
   regression. Restart this window after significant collector/parser changes.
6. **Cutover:** after sign-off switch only token dashboard queries, never the
   heartbeat endpoints. Reversible feature flag back to heartbeat display;
   retain queued usage and original tables, do not restore an older production
   DB to undo a dashboard change.

## Test commands

From `wakapi/` on a machine with Python:

```sh
python3 -m unittest discover -s tools/token_ledger -t . -v
python3 -m tools.token_ledger.audit --source /private/snapshot/source --machine Pythia \
  --source /private/remote-source --machine omphalos \
  --wakatime-export /private/wakatime-export/heartbeats.download \
  --wakatime-user-agents /private/wakatime-export/user_agents.json \
  --wakatime-machines /private/wakatime-export/machine_names.json \
  --cutoff-ms 1791759696000 --report /private/audit.json
```

In an isolated copy of this fork with an existing Go module cache:

```sh
go test ./models ./services ./routes/api
```

The audit exits nonzero when there are unresolved source exceptions (including
quarantined fallback sessions) even though it still writes its report. Its
`--commit` option is restricted to existing,
explicitly named `*.rehearsal.sqlite3` copies; `--allow-incomplete` is for
rehearsal only. Its output files are private; keep them outside the repository.
