# Dashboard accounting contract

- `WAKAPI_DASHBOARD_TOKEN_SOURCE=ledger|heartbeats` selects **all** token-derived
  dashboard panels server-side. Default is heartbeats until explicitly promoted.
  Invalid values fail startup; missing/unavailable ledger fails the panel, never
  silently falls back or displays zero. The page pins the selected mode and rejects
  responses from another mode during a deployment. Rollback selects heartbeats
  using the same image and database (a service restart is required).
- Ledger total = new input + cache read + cache write + unknown-input + output.
  Reasoning is already included in output; never add it again. Transcript session
  references suppress overlapping WakaTime fallback globally, BEFORE any display
  filters. Held observations are excluded. Copied requests count once.
- Tokens, model rows, daily token activity and project token totals share one
  counted-event projection and date/project/machine/model filters. Resolve aliases
  identically to heartbeat activity. Unassigned projects/models remain visible.
  WakaTime fallback has its own activity group: its original editor was not stored
  in the ledger, so do not guess tool attribution from a model name.
- All-time begins at the earliest heartbeat OR counted ledger event. Calendar-day
  bins use the user's timezone, including DST. Time, sessions, timeline and live
  machines continue using heartbeats; they are not reconstructed from token events.
- Primary ledger totals are labelled **provisional accounted usage**, not certified
  lifetime history. Display unknown cache splits, held observations and source
  revisions/coverage limitations. Copied-event machine/project attribution is
  first-observed history location, not certified execution origin.
- Initial ledger pricing was explicitly pending; verified partial pricing is now
  deployed. Old heartbeat estimates are never the price of a ledger total. Legacy
  comparison is labelled; unpriced buckets remain visible per model/project.
- Ledger pricing uses only exact configured model/rate matches and verified
  usage buckets. Unknown input, missing rates and cache-write TTL/rate ambiguity
  stay unpriced. No scaling of old estimates; no inference of subscription status
  from a CLI name. Currency is explicitly USD and values are estimated equivalent
  API list value at configured rates, **not bills or historical paid spend**.
  See [LEDGER_PRICING.md](LEDGER_PRICING.md) for rate/TTL rules and validation.
- Background source/queue/receipt reconciliation remains enabled independently of
  display promotion. Seven days is a reliability milestone, not a prerequisite
  for a clearly provisional display. A passing display test never certifies missing
  history. Deployment checks require a healthy actual image, authenticated API,
  known-cutoff reconciliation, unchanged heartbeat fields/schema, and browser checks.
