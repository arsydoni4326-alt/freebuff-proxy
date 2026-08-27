# Bridge Mode Quota Introspection Dashboard

> **Status:** Phase 2 — Complete  
> **Last Updated:** 2026-08-27

---

## Overview

This document describes the Bridge Mode Quota Introspection Dashboard — a feature that
surfaces per-entry quota state, model-level breakdown, and rate-limit activity for
bridge-mode tokens. Operators can diagnose quota exhaustion, monitor token health,
and identify rate-limit bottlenecks without exposing raw client tokens.

---

## Motivation

In bridge mode, each client supplies its own upstream FreeBuff token. The proxy dynamically
leases and caches upstream sessions per client token (LRU, max 32 entries). Operators need
visibility into:

- **Per-entry quota state**: How much quota remains per model for each bridge entry?
- **Model-level breakdown**: Which models are consuming quota? Are any near exhaustion?
- **Rate-limit activity**: Are entries being rate-limited? How often?

Without this visibility, operators cannot diagnose sudden quota exhaustion, rate-limit
cascades, or cooldown patterns that indicate upstream detection.

---

## Architecture

### Data Flow

```
bridgeEntry
  ├── session.Snapshot().QuotaByModel  →  per-model quota (limit, recent, reset)
  ├── spendLedger                      →  Pacific-day spend (advisory)
  ├── rateLimitHits/Misses (NEW)       →  per-entry rate limit counters
  └── client.RateLimitEvents()         →  upstream rate-limit classifications
          │
          ▼
BridgeTokenSnapshot (enriched)
          │
          ▼
/admin/api/bridge-quota  →  JSON response
          │
          ▼
Tokens.svelte Bridge Quota tab  →  per-entry cards with model breakdown
```

### Backend Components

#### 1. Rate Limit Counters

Added to `bridgeEntry` in `internal/pool/pool.go`:

```go
rateLimitHits   atomic.Int64  // allowed requests
rateLimitMisses atomic.Int64  // denied requests (rate limited)
```

Incremented in `rateLimitAllow()` in `internal/pool/bridge_cache.go`.

#### 2. Enriched BridgeTokenSnapshot

Extended in `internal/pool/snapshot.go` with `RateLimitHits`, `RateLimitMisses`,
and `RateLimitRate` fields.

#### 3. Enriched bridgeTokenCard (Dashboard Go Struct)

Extended in `internal/dashboard/dashboard_data.go` with:
- `Quota []bridgeQuotaRow` — per-model quota rows
- `SpendLimit int64` — MAX_SPEND_PER_DAY
- `RateLimitHits int64` — allowed requests
- `RateLimitMisses int64` — denied requests
- `RateLimitRate float64` — configured rate

#### 4. Dashboard API Endpoint

New endpoint: `GET /admin/api/bridge-quota`

Returns enriched bridge entry data with per-model quota breakdown, rate limit counters,
and spend information. Requires dashboard authentication.

---

## Frontend Design

### Tokens Page — Bridge Quota Section

When in bridge mode, the Tokens page shows a dedicated Bridge Quota section. Each bridge
entry is rendered as a card with:

1. **Header**: Hashed key prefix, status badge (active/cooldown/locked/dead), model
2. **Spend Overview**: Spend/day vs limit, visual progress bar, color-coded
3. **Quota Breakdown**: Per-model quota rows with usage bar and reset times
4. **Rate Limit Activity**: Hits/misses counters, configured rate
5. **Actions**: Lock/Unlock buttons (existing)

### Visual Design

Follows the existing instrument panel design system (DESIGN.md):
- Mono instrumentation: All numbers in IBM Plex Mono with tabular-nums
- Status LEDs: 3px amber dot for active, pulsing for cooldown, red for dead
- Hairline borders: No soft shadows, defined edges only
- Color coding: Green (<80%), amber (80-95%), red (>95%) for quota usage

---

## Configuration

| Key | Default | Description |
|---|---|---|
| `BRIDGE_RATE_LIMIT_PER_TOKEN` | `0` | Per-client-token rate limit in req/s (0 = unlimited) |
| `BRIDGE_DAILY_LIMIT` | `0` | Global daily chat cap across all bridge tokens (0 = unlimited) |
| `MAX_SPEND_PER_DAY` | `0` | Advisory per-token Pacific-day spend ceiling (0 = unlimited) |

---

## Security & Privacy

- **No raw tokens**: All keys are SHA-256 truncated to 32 hex chars.
- **No token material in logs/metrics**: hash prefix only.
- **Dashboard authentication**: Bridge quota endpoint requires dashboard auth.

---

## Implementation Steps

1. ✅ Add `rateLimitHits`/`rateLimitMisses` atomic counters to `bridgeEntry`
2. ✅ Increment counters in `rateLimitAllow()`
3. ✅ Extend `BridgeTokenSnapshot` with rate limit fields
4. ✅ Extend `bridgeTokenCard` with `Quota` rows and rate limit stats
5. ✅ Bridge quota data included in existing tokens/overview API endpoints
6. ✅ Add Bridge Quota section to Tokens page
7. ✅ Surface circuit breaker state on Overview page (ROADMAP §2.2)
8. ✅ Tests: unit tests for counters, snapshot, API, dashboard
9. ✅ Documentation updates

---

## Testing

All dashboard tests pass with hermetic suite:

```bash
env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./internal/dashboard/... -count=1 -v
```

Key test coverage:
- `TestPageOverviewFull` — bridge mode overview includes circuit breaker data
- `TestPageOverviewFragment` — pooled mode overview works correctly
- `TestTokensPageQuotaRows` — per-model quota rows render in bridge quota section
- `TestTokensPagePureBridgeInBridgeCard` — bridge token cards display correctly
- `TestDashboardModelsViewsServeOnly` — served-models gate works across views

---

## Related Documentation

- [Architecture](../ARCHITECTURE.md)
- [Bridge Mode](bridge-mode.md)
- [Circuit Breaker Observability](circuit-breaker-observability.md)
- [Roadmap](../ROADMAP.md)
- [Design System](../DESIGN.md)
