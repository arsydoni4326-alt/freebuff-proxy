# Bridge Mode — Architecture, Invariants & Hardening Guide

> **Status:** Phase 2 Hardening — In Progress  
> **Last Updated:** 2026-08-25

---

## Overview

Bridge mode activates when `AUTH_TOKENS` is unset or empty. Instead of the proxy managing a fixed pool of upstream FreeBuff tokens, each client supplies its own token via the `Authorization: Bearer <token>`, `x-api-key: <token>`, or `anthropic-api-key: <token>` headers. The proxy acts as a zero-storage relay, dynamically leasing and caching upstream sessions per client token.

---

## Operating Model

### When Bridge Mode is Active

- `config.AuthTokens` is empty (no configured pool tokens).
- `config.BridgeMode()` returns `true`.
- Each request must carry a valid upstream FreeBuff token.

### When Bridge Mode is Inactive (Pooled Mode)

- `config.AuthTokens` contains at least one configured token.
- The proxy manages the pool, routing, failover, and cooldowns.
- Client-supplied `Authorization` headers are validated against `API_KEYS` if configured.

---

## Core Components

### `internal/pool/bridge.go`

- **`AcquireBridge(ctx, clientToken, model)`** — Main entry point for bridge-mode lease acquisition.
  - Validates the client token (non-empty).
  - Resolves the agent ID from the model via the registry.
  - Looks up or creates a `bridgeEntry` via `bridgeEntryFor()`.
  - Checks administrative lock, global daily limit, cooldowns, per-entry daily cap.
  - Performs upstream session admission and run creation.
  - Returns a `Lease` with `Token: -1` (bridge mode marker) and the `Bridge` field set.

- **`ProbeNewToken(ctx, token)`** — Validates a NOT-yet-added token with a zero-cost GET probe.
  - Used by dashboard token wizard and health checks.
  - Returns `SessionState` or classified error (`ErrAuthRejected`, `ErrBanned`, etc.).

### `internal/pool/bridge_cache.go`

- **LRU Cache:** `maxBridgeEntries = 32` (hard cap).
- **Idle Eviction:** `bridgeIdleEvict = 24h` (sliding TTL).
- **Token Key Hashing:** SHA-256 truncated to 32 hex chars (`tokenKey()`). Raw tokens are never stored as map keys.
- **Creation Rate Gate:** `bridgeCreateGate` (capacity 4) limits concurrent `upstream.New()` calls.
- **Double-Check Pattern:** After acquiring the gate, re-check the cache before creating a new entry.

---

## Invariants & Safety Contracts

### B1: Creation Rate Limiting

- `bridgeCreateGate` (capacity 4) caps concurrent `upstream.New()` calls.
- Prevents thundering-herd creation when many distinct tokens arrive simultaneously.
- Timeout: 5 seconds. Exceeding returns `"bridge: creation rate limit exceeded"`.

### B2: Client Creation Outside Lock

- `upstream.New()` is called **outside** `bridgeMu` to avoid blocking other bridge operations during DNS + TLS handshake.
- Double-check after acquiring the gate ensures no duplicate creation.

### B3: Token Key Hashing

- Raw client tokens are never stored as map keys.
- `tokenKey()` returns a 32-char hex string derived from SHA-256.
- The raw token is held in `bridgeEntry.token` for upstream client creation only.

### B4: LRU Eviction

- Cache capped at `maxBridgeEntries = 32`.
- Least-recently-used entries evicted when full.
- Idle entries (unused > 24h) evicted by `bridgeMaintain()`.

### B5: Global Bridge Daily Limit

- Configurable via `BridgeDailyLimit` config.
- Checked **before** per-entry limits.
- Best-effort TOCTOU: one extra request may slip past the limit.

### B6: Dead-Token Eviction Gate

- `bridgeEvictToken()` defers eviction if in-flight leases exist.
- Prevents orphaning live streams during dead-token cleanup.
- Entry stays cached (cooled down) until leases drain, then idle sweep drops it.

### B7: Cooldown Surfacing

- During cooldown, bridge entry returns the remembered error (`BanError`, `CountryBlockedError`, `RateLimitError`, `IpCappedError`) instead of re-hitting upstream.
- Prevents redundant upstream probes for known-bad tokens.

### B8: Inflight Tracking

- `entry.runs.InflightCount()` gates maintenance and eviction.
- Prevents session kicks or run kills during active streams.

---

## Security Considerations

### Token Handling

- Raw tokens are held in memory only (`bridgeEntry.token`).
- Never logged, never exposed in metrics, never stored in maps as keys.
- SHA-256 hashing for cache keys ensures non-reversibility.

### Client Isolation

- Each client token maps to its own `bridgeEntry` (upstream client + session + run manager).
- No cross-client token leakage or slot burning.
- Administrative lock (`entry.locked`) can freeze individual entries.

### Cooldown Protection

- Invalid/expired tokens are cooled down immediately.
- Repeated probes for cooled-down tokens return the cached error without upstream contact.

### Rate Limiting

- Per-IP rate limiting via `internal/ratelimit` applies to bridge mode.
- Global bridge daily limit caps total requests across all entries.

---

## Configuration Keys

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `BRIDGE_DAILY_LIMIT` | int | 0 (disabled) | Global daily request cap across all bridge entries |
| `MAX_MESSAGES_PER_DAY` | int | 0 (disabled) | Per-client-token daily request cap |
| `REQUEST_TIMEOUT` | duration | 15m | Timeout for upstream requests |
| `SESSION_CALL_TIMEOUT` | duration | 5s | Timeout for session admission calls |
| `AUTH_TOKENS` | string list | (empty) | When empty, bridge mode activates |

---

## Error Surfaces

| Error | Source | HTTP Status | Client Response |
|-------|--------|-------------|-----------------|
| Empty client token | `AcquireBridge()` | 401 | `missing_bearer_token` |
| Token cooling down | `AcquireBridge()` | 429/403 | Remembered error (ban/country-block/rate-limit) |
| Global daily limit reached | `AcquireBridge()` | 429 | `bridge: global daily limit N reached` |
| Per-entry daily cap reached | `AcquireBridge()` | 429 | `bridge: daily limit N reached for token` |
| Auth rejected | Upstream admission | 401 | `authentication_error` |
| Country blocked | Upstream admission | 403 | `country_blocked` |
| Banned | Upstream admission | 403 | `account_banned` |
| Rate limited | Upstream admission | 429 | `rate_limit_error` |
| Creation rate limit exceeded | `bridgeEntryFor()` | 503 | `bridge: creation rate limit exceeded` |
| Admin locked | `AcquireBridge()` | 403 | `bridge token locked by administrator` |

---

## Testing

### Hermetic Test Suite

```bash
env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...
```

### Bridge-Specific Test Files

| File | Coverage |
|------|----------|
| `internal/pool/pool_bridge_test.go` | LRU eviction, cooldowns, reuse, global limits |
| `internal/pool/bridge_singleflight_test.go` | Concurrent admission dedup |
| `internal/server/server_bridge_test.go` | End-to-end bridge mode relay |

### Test Patterns

- Use `testutil.NewMock()` for mock upstream.
- Use `newBridgePool(t, mock)` helper for pool-level setup.
- Use `newBridgeTestServer(t, mock)` helper for full server setup.

---

## Hardening Checklist (Phase 2)

> This is the tracking checklist for the current hardening phase. Each item becomes a committed change with tests and documentation updates.

### Security

- [x] **Token format validation** — Added `validateClientToken` in `internal/pool/bridge.go`: bounds length (`maxClientTokenLen = 4096`) and rejects interior whitespace / control characters before caching or upstream contact. A hard `cb_` prefix/charset check was deliberately deferred: existing bridge tests exercise non-`cb_` opaque tokens, so a strict prefix would be brittle. Covered by `TestValidateClientToken`.
- [x] **Log sanitization audit** — No raw tokens in logs or metric labels. `bridgeTokenLabel` uses the SHA-256 token key prefix; chat/access/request-failed lines use `token=bridge`, never the raw token. Verified by `TestBridgeLogSanitization` (pool log capture) and `TestBridgeMetricsNoRawTokenLabels` (metrics + healthz).
- [x] **Per-client-token rate limiting** — Added `rateLimitAllow()` (per-entry token bucket) on `bridgeEntry`, gated by `BridgeRateLimitPerToken` config. Independent of the per-IP limiter. Initialized at entry creation from config. Covered by `TestBridgeTokenRateLimit`, `TestBridgeTokenRateLimitUnlimited`, `TestBridgeRateLimitEntryIsIndependent`.
- [x] **Header smuggling / injection tests** — `header_injection_test.go`: `clientToken` precedence (Authz > x-api-key > anthropic-api-key), no concatenation across headers, multiple Authorization values (Header.Get reads first), and `authorized()` gate parity so auth and relay always agree.
- [x] **Quota exhaustion edge-case tests** — `TestBridgeGlobalDailyLimitExceeded`, `TestBridgeQuotaIntersection`, `TestBridgeCooldownThenCap` cover global cap, cooldown+cap intersection, and cooldown-then-cap ordering.
- [x] **Metrics label hygiene** — `TestBridgeMetricsNoRawTokenLabels` verifies raw client tokens never appear as Prometheus labels (only the SHA-256 prefix `token_label`).

### Reliability

- [x] **Transient failure retry** — Pre-existing `TRANSIENT_RETRIES` (default 1) in the upstream client replays the request body via `GetBody` on a fresh connection; bounded attempts, byte-identical replay (idempotent). Covered by extensive `client_retry_test.go`.
- [x] **Circuit breaker** — `bridge_breaker.go` implemented: sliding window, transient-only trips (5xx/network), configurable failures/window/cooldown (`BridgeCircuitBreakerFailures/Window/Cooldown`), tested in `bridge_hardening_test.go`.
- [x] **Diagnostics for invalid tokens** — `errors.go` `remediationMessage()` returns actionable per-code remediation (gen-token scripts for invalid/expired, reset time for quota, region routing for country block, etc.).
- [x] **Quota exhaustion messaging** — `errors.go` `remediationMessage()` adds reset-at, Retry-After, and precise hints for daily-cap / spend_limited / ip_capped / waiting-room states.

### Code Quality & Tests

- [x] **Refactor critical paths** — Extracted `bridgeEntry.rateLimitAllow()` and the `newBridgePoolCfg` test helper for pure unit testing.
- [x] **Fuzz tests for token handling** — `FuzzValidateClientToken`, `FuzzTokenKey` in `bridge_hardening_test.go`: never panic, outputs property-checked.
- [x] **Property-based LRU tests** — `TestBridgeLRUProperty`: bounded random access, cache size never exceeds `maxBridgeEntries`.
- [x] **Load test** — `TestBridgeThunderingHerd`: 40 concurrent requests across 20 distinct tokens; all complete, cache bounded.

### Documentation

- [x] Create `docs/bridge-mode.md` (this document).
- [x] Update `ROADMAP.md` — Phase 2 items marked done (except circuit breaker).
- [x] Update `session.md` — Phase 2 progress tracking.
- [ ] Update `README.md` with bridge mode security posture.
- [ ] Update `ARCHITECTURE.md` with hardening invariants B1–B8.
- [ ] Fold completed hardening items out of the ROADMAP current-work list.

### Metrics & Observability

- [x] **Bridge-specific Prometheus metrics** — `freebuff_proxy_bridge_entries_total`, `cooling_down_total`, `dead_tokens_total`, `locked_total`, `requests_total`, `active_runs`, `quota_remaining`. No raw token labels (SHA-256 prefix only).
- [x] **Per-entry quota introspection** — `BridgeSnapshot` returns hashed `Key` + `QuotaByModel`, `SpendDay/SpendPct`, `BanType/BannedUntil`, `DeadToken`; no plaintext token exposure.
- [x] **`/healthz` bridge indicators** — `bridge_tokens` count + `bridge_entries[]` with `dead_token`, `cooldown_until`, `locked`, `session_active`, `active_runs`, `requests`, `model`, `spend_*` per entry.
- [x] **Rate limit hit/miss counters** — Per-entry `rateLimitHits`/`rateLimitMisses` atomic counters surfaced via `BridgeTokenSnapshot` and dashboard cards.
- [x] **Dashboard Bridge Quota section** — Tokens page shows per-entry cards with model-level quota breakdown, spend progress bars, rate limit activity, and lock/unlock actions.

---

## Related Documentation

- [Architecture](../ARCHITECTURE.md) — System components and request flows
- [Bridge Quota Dashboard](bridge-quota-dashboard.md) — Quota introspection design and implementation
- [Specification](../SPECIFICATION.md) — API surface and error contracts
- [Roadmap](../ROADMAP.md) — Feature checklist and known limitations
- [Security Policy](../.github/SECURITY.md) — Vulnerability reporting and in-scope surfaces
