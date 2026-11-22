# Binance request budget and stop confirmation implementation plan

Goal: reduce duplicate reads, reserve API capacity for protection, and avoid flattening on one transient stop lookup failure.
Architecture: shared host-scoped HTTP request budget with response-header telemetry and exchangeInfo limits; per-client short-lived config cache; persisted bounded stop-confirmation retry state. Never replay mutations automatically.
Spec: user approved four improvements in the conversation and requested direct implementation.
Constraints: preserve earlier diagnostic patch; no actual test trades; no cache for positions/orders; no credentials or upstream payloads in logs. Work in current checkout to preserve the already deployed uncommitted diagnostic changes.

- [x] TDD budget transport: concurrent shared budget, weight headers, cooldown/Retry-After, urgent reserve, safe HTTP errors; wire after client hooks.
- [x] TDD metadata cache: 30-second mode, 5-minute validated per-symbol fee, per-client isolation and failure invalidation; fresh account/position/order reads.
- [x] TDD confirmation: typed transient errors only, at most 3 lookups and 15 seconds, at least 5-second spacing or server backoff, persistent deadline across restart; terminal/malformed/identity failures rescue immediately. Never resubmit stop.
- [x] Whole-suite and race tests; independent review.
- Deployment: idle checkpoint guard, database backup, rollback image; verify health and exact revision. Results recorded in SERVER.md.

Review focus: late responses across minute boundaries; concurrent budget reservations; signed request expiry (reject locally rather than queue signed requests); timeout versus request cancellation; restart cannot reset deadline; unrelated account cache contamination.

Validation: final `go test ./...` and `go test -race ./trader/binance ./trader/allincrypto ./trader/execution` passed. Review findings fixed: context-aware cache gates and atomic invalidation, conservative cross-window reservations, pre-read durable backoff, sanitized HTTP errors with transient classification preserved. Stop ownership read is bounded to 3 seconds, submission to 5 seconds, each confirmation read to at most 3 seconds.
