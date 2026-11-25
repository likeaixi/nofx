# Shadow Copy and Trade Ledger Implementation Plan

**Goal:** Configurable live Binance-source copy strategies, an explicit general strategy editor, and a trade ledger matching 1.png/2.png with legacy analysis in the account menu.
**Architecture:** Reuse allincrypto execution with a persisted copy_config (source_url and leverage), strict URL parsing and per-source sharing; preserve legacy defaults. Add a ledger view backed by existing authenticated reads, with honest unavailable fields and client-side CSV export. Keep original analytics route private.
**Tech Stack:** Go/GORM, React/TypeScript/SWR, existing Flow CSS.

## Constraints
- User confirmed live execution. Do not start new live traders as part of implementation.
- Preserve existing default source, 10x leverage and 4% stop; no retroactive orders.
- Accept Binance lead-details HTTPS URLs only; never fetch arbitrary submitted URLs.
- Source identity must be isolated in clients, checkpoints and donor-ledger reuse.
- Leverage 1–20; all submission, margin and ownership checks use the selected value.
- Existing source cannot be replaced on a checkpoint-bearing trader; use a new trader to establish a fresh baseline.
- No invented initial-risk/R, candidate, planned-order or runtime-start data where the current API has none.

## Tasks
- [x] Test copy config URL/range validation and JSON roundtrip; implement store config and API validation.
- [x] Test dynamic provider ID and checkpoint isolation; implement source registry and runtime configuration, dynamic leverage verification. Run targeted Go tests.
- [x] Add copy strategy form and general strategy entry. Preserve edited general parameters when saving or launching; distinguish configure from live start.
- [x] Move Trade Details to account menu; add Trade Ledger navigation with filters, stats, positions, close records, refresh and CSV. Add data-computation tests.
- [x] Run full Go/type/UI checks, request independent review, fix findings.
- [ ] Commit/push/build/deploy after checking live protection and backing up DB; verify health and record release notes.

## Review focus
- Source change on restart must not consume another lead's history.
- Legacy clients and checkpoints must retain original behavior.
- Margin and exchange verification must use the exact configured leverage.
- Pagination/filter statistics must disclose loaded scope, fees must not be double-subtracted.
- Switching traders must not mix histories or export stale records.

## Verification notes
- Full Go suite and targeted API tests passed; frontend type checking and 13 relevant tests passed.
- Independent backend/frontend review completed; fixed hidden Save controls, publication settings preservation and paginated history refresh gaps.
- Desktop and 390px mobile layouts checked with local synthetic data via Chrome; no live trade test performed.
- Strategy edits and starts share process-local locks; deployment runs one backend instance.
