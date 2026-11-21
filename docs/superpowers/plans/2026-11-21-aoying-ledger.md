# Aoying source ledger Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans. Execute the authorized repair inline.

**Goal:** Persist source fills from the user-confirmed flat baseline without replaying historical trades.
**Architecture:** Opt-in baseline on Runner; provider HistorySince verifies continuous history; atomic ledger and checkpoint writes; existing execution controls unchanged.
**Tech Stack:** Go, decimal, GORM/SQLite.
**Spec:** docs/superpowers/specs/2026-11-21-aoying-ledger-design.md

## Global Constraints
- Baseline 2026-11-20T00:00:00+08:00, fixed portfolio 5075281354358777856; source and actual holdings separate.
- Bootstrap never submits orders; migration refuses outstanding copy exposure.
- Preserve five-minute entry window, sizing, risk checks, and old default behavior.

## Review Focus
- Incomplete pagination/revisions must not commit partial history.
- Repeated poll/restart must not double count or replay.
- Skipped target orders must still update source balances.
- Excess reduction/unknown side/out-of-order fill must fail safely.
- Deployment must preserve running traders, credentials, actual orders.

### Task 1: Historical reader
- [x] Add failing provider HistorySince tests: boundary coverage, missing page, changed head, duplicate identity.
- [x] Implement HistorySince(ctx, baselineMillis) returning Snapshot; chronological validation and page recheck.
- [x] Run provider tests.

### Task 2: Durable ledger
- [x] Add runner tests for migration with historical skips, restart/dedup, target skip, future open and proportional reduction, excessive close.
- [x] Add source ledger rows and checkpoint baseline; bootstrap atomically with consumed historical signals.
- [x] Update ledger before target plan, persist with signal consumption; preserve legacy path without opt-in.
- [x] Wire NOFX_AOYING_FLAT_BASELINE in AutoTrader; runtime reports show source quantities.
- [x] Run focused and broad Go tests; review changes.

### Task 3: Deploy
- [ ] Backup live SQLite/config and tag previous image, transfer exact patch, build backend.
- [ ] Configure confirmed baseline, recreate backend only, verify health and all source ledgers; confirm no historical order submissions.
- [ ] Record deployed version and verification.


## Execution evidence
- Baseline provider and runner suites passed (httptest required localhost permission).
- Regression tests observed missing APIs initially; source/target decoupling test failed at source=109 vs114 before fix.
- Same-time ordering regression failed at source=119 vs129 before persisted sequence fix.
- Real 26-fill replay: MU long=0, ZEC short=727.875; zero historical orders.
- `go test ./...` passed after final implementation.
- Independent reviewer found migration ordering, source collection coupling and same-time execution ordering; all fixed and re-reviewed without remaining critical/important findings.
- Ruling: public API cannot expose arbitrary historical changes outside returned overlapping pages; explicitly document this pre-existing limitation instead of requiring broken old-page scans forever.
