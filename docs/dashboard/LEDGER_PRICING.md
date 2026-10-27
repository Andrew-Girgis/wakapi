# Provisional ledger pricing

The dashboard estimates **partial API-equivalent value in USD at configured
standard-tier, short-context/global-routing rates**. It is not historical invoices,
paid spend, subscription charges, or a claim about actual region/tier/discounts.

The existing local `config/ai_prices.yml` rate book is retained. No new provider
rates were guessed. `WAKAPI_AI_PRICES_FILE` can supply an explicit replacement.

## Rules

- Match the complete model/version against configured entries, with explicit
  family/punctuation aliases only. Mini/fast/codex variants and dated versions
  never inherit another version's rate. CLI versions such as `Claude 2.1.150`
  and missing models remain unpriced. Conflicting duplicate rates fail closed.
- Base input/read/output rates must be finite and positive. Unknown input/cache
  splits are **always unpriced**, even when the model has a price.
- Count reasoning within output, not as an additional billable bucket.
- Cache writes require both an explicit rate and a complete numeric provider
  `cache_creation` split whose 5-minute + 1-hour counts exactly equal the recorded
  write count. Missing, fractional, inconsistent or ambiguous TTLs are unpriced.
- Optional rate-file keys: `cache_write_5m` and `cache_write_1h`, USD / 1M tokens.
  Missing keys never imply zero price or an inferred multiplier. The bundled
  book does not supply these keys, so its cache-write tokens remain unpriced.
- Each model and the overall cost reports `priced_tokens` + `unpriced_tokens`
  equal to the corresponding accounted total. Individual values are rounded to
  cents for display; summed unrounded values determine the overall estimate.
- Subscription/pay-per-token attribution is **unknown** in the ledger, not inferred
  from a CLI, model, machine, or absence of a plan. The old heartbeat estimate is
  accessible only through explicit heartbeats display mode.

## Validation and deployment (12–13 Oct 2026)

- Tests cover exact aliases, rejected variants/CLI versions, invalid/conflicting
  rates, unknown input, reasoning, TTL completeness and partial rate coverage.
- Independent Python Decimal arithmetic over the local rate book reconciled six
  isolated scopes. Snapshot: 4,494,204,237 accounted tokens; 3,726,023,389 priced,
  768,180,848 unpriced; partial equivalent value **USD 2,207.40**.
- Actual production API/browser verification after deployment:
  **4,507,638,967 accounted tokens**, **3,728,755,722 priced**,
  **778,883,245 unpriced**, **USD 2,210.44 partial equivalent value**.
  These are time-stamped verification figures, not fixed future headline values.
- Final checks at 13 Oct 2026 01:17 UTC: **4,513,225,167 accounted tokens**,
  **3,734,341,922 priced**, **778,883,245 unpriced**, **USD 2,212.90**.
  Frozen audited cutoff remains exactly 4,323,534,508; both host receipt/preview
  observers pass with no missing/mismatched/unsent/conflicting records. Full schema,
  pre-pricing heartbeat checksums and database integrity remain unchanged.
- Production image `wakapi-fork:ledger-priced-v1`; prior token-only image retained
  as `wakapi-fork:pre-ledger-pricing`. Fresh backup in private `pre-pricing/`.
  Source rollback remains `~/Projects/mnemosyne/bin/dashboard-source heartbeats`
  (same image/database; restart only). Tracking is never redirected or restored.
