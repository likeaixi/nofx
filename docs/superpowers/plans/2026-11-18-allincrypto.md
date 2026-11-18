# AllinCrypto Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** A selectable deterministic cross-exchange copy strategy for the specified Binance lead portfolio.
**Architecture:** Dedicated source adapter and persistent execution state feed a fixed strategy runner; existing exchange adapters execute orders after capability checks. UI exposes the fixed preset without AI configuration. Never run live orders during development.
**Tech Stack:** Go, GORM/SQLite, React/TypeScript, existing exchange SDKs.
**Spec:** docs/plans/allincrypto-copy-strategy-design.md

## Global Constraints
- Portfolio 5075281354358777856; marginBalance, not AUM, is denominator.
- Fixed 20x leverage; target fill price +/-2% stop; follow adds and proportional closes.
- First-start baseline, durable deduplication, no automatic replay of uncertain orders.
- Cross-exchange support, verified market identity and contract units; unsupported or ambiguous markets skip.
- No live enablement, deployment, or source data document changes in this task.

## Review Focus
- Two source rows can lack unique order IDs: ambiguous updates must not duplicate exposure.
- Partial fills/timeouts/restarts must not create unprotected or duplicated exposure.
- Local stops followed by source closes must not open reverse positions.
- Existing manual/other-strategy exposure must never be adopted.
- USDC/USDT conversion and 1000-unit contracts must not silently scale risk.

### Task 1: Complete fixed strategy vertical slice
**Files:** New focused files under provider/binancecopy/, store/, trader/; strategy schema/presets and strategy UI modules; tests beside implementation.
**Interfaces:** Define public source Snapshot with validated portfolio balance and ordered events, durable per-trader event/position state, and dedicated AllinCrypto execution entry selected by strategy_type. Document exact interfaces in implementation report before review.
- [ ] Inspect existing strategy serialization/defaults/UI and execution adapter semantics before edits.
- [ ] Write failing tests for source envelopes/pagination, baseline/dedup, sizing/precision, 2% stop on fills, proportional reductions and unknown-result blocking.
- [ ] Implement source client with timeouts, strict business response checks and bounded pagination. Read-only live response verification permitted; mock source and exchanges in tests.
- [ ] Implement additive strategy schema, persistence, runner and required market metadata checks. Make only justified explicit symbol mappings; fail closed on missing conversion or capability.
- [ ] Expose named fixed strategy AllinCrypto without AI deciding trades. Connect UI selection and API validation to runtime path.
- [ ] Run affected Go tests, frontend suite and TypeScript checks; record limitations honestly.
- [ ] Review whole diff against spec, fix safety defects, then hand over local changes without pushing or deploying.

## Execution rulings
- User's OK approves implementation of the updated design; proceed without another plan approval round.
- Work in existing dev-allin checkout, preserving unrelated untracked binance-copy-trade-public-api.md.
- Implementation may be a single integrated task because runtime/storage/schema/UI interfaces must agree. Independent review follows.
