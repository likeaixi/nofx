# Copy source periods

User-approved design: reuse a trader across copy sources, preserve prior history, require no owned positions or unresolved orders before switching, and never replay historical entries.

## Implementation
1. Add a durable period archive keyed independently from the trader. On source mismatch at Poll, atomically archive the prior checkpoint and move its source ledger/signals under the archive key; replace the active checkpoint with a fresh period. Retain account identity and decision cycle numbering. Reject invalid, faulted or outstanding state. Keep revision monotonic to reject stale writers. Reconcile alone must never change source.
2. Keep existing stopped-trader and outstanding-work guards for configuration updates. Remove the blanket prohibition on changing a source merely because historical records exist.
3. Log source transitions and current period; do not display old-period signals as the current period's latest result. Preserve existing decisions, exchange orders and trade history under the original trader.
4. Add tests for archival, same-source restarts, switching back, stale writers, open positions/pending/waiting/faulted state and historical-entry suppression. Run affected API, provider and runner tests plus race checks.
5. Include the previously tested one-second shared public history request pacing. Commit, push and deploy with database backup and ownership checks; verify health and the affected running trader's bootstrap without manually submitting orders.

No account changes, historical position adoption, historical catch-up orders, or source mismatch bypass for outstanding exposure.
