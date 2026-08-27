# Circuit Breaker Observability

> **Status:** Phase 1 — Implementation Complete
> **Last Updated:** 2026-08-27

---

## Overview

The bridge circuit breaker protects upstream FreeBuff from being hammered when it experiences a batch outage (5xx responses or network failures). This document describes how operators can **observe** the breaker's state — through `/healthz`, Prometheus metrics, and the admin dashboard — to detect, diagnose, and respond to upstream instability.

---

## How the Circuit Breaker Works (Recap)

| Setting | Env Var | Default | Description |
|---|---|---|---|
| **Failures** | `BRIDGE_CIRCUIT_BREAKER_FAILURES` | `0` (disabled) | Number of transient failures within the window that trips the breaker |
| **Window** | `BRIDGE_CIRCUIT_BREAKER_WINDOW` | `30s` | Sliding window in which failures are counted |
| **Cooldown** | `BRIDGE_CIRCUIT_BREAKER_COOLDOWN` | `10s` | How long the breaker stays open after tripping |

**What trips the breaker:** Only genuine transient upstream outages — HTTP 5xx responses or raw network/transport errors. Classified errors (auth, rate-limit, ban, country-blocked, ip-capped, session-invalid, etc.) **never** trip the breaker.

**What happens when open:** All bridge-mode requests immediately return a `503 upstream_retryable` error with a `Retry-After` header, instead of attempting to contact a batch-down upstream.

---

## Observability Surfaces

### 1. `/healthz` — Operational Snapshot

The health endpoint now includes a `circuit_breaker` object:

```json
GET /healthz
{
  "uptime": "2h15m",
  "bridge_tokens": 3,
  "circuit_breaker": {
    "enabled": true,
    "open": false,
    "failure_count": 1,
    "failures_remaining": 4,
    "cooldown_remaining_seconds": 0,
    "until": null,
    "config": {
      "failures_threshold": 5,
      "window": "30s",
      "cooldown": "10s"
    }
  }
}
```

| Field | Type | Description |
|---|---|---|
| `enabled` | `bool` | `true` when `BRIDGE_CIRCUIT_BREAKER_FAILURES > 0` |
| `open` | `bool` | `true` when the breaker is currently blocking all bridge requests |
| `failure_count` | `int` | Current number of transient failures in the sliding window |
| `failures_remaining` | `int` | Failures remaining before the breaker trips (clamped to 0) |
| `cooldown_remaining_seconds` | `float` | Seconds left in the current cooldown period (0 when closed) |

### 2. Prometheus `/metrics` — Time-Series Monitoring

Two new gauge metrics are exported:

```
# HELP freebuff_proxy_bridge_breaker_open 1 when the circuit breaker is blocking requests, 0 otherwise
# TYPE freebuff_proxy_bridge_breaker_open gauge
freebuff_proxy_bridge_breaker_open 0

# HELP freebuff_proxy_bridge_breaker_failures Current number of transient failures in the circuit breaker sliding window
# TYPE freebuff_proxy_bridge_breaker_failures gauge
freebuff_proxy_bridge_breaker_failures 1
```

| Metric | Type | Values | Description |
|---|---|---|---|
| `freebuff_proxy_bridge_breaker_open` | gauge | `0` or `1` | `1` when the breaker is blocking; `0` when closed or disabled |
| `freebuff_proxy_bridge_breaker_failures` | gauge | `≥ 0` | Current failure count in the sliding window |

**PromQL examples:**

```promql
# Alert when breaker is open
freebuff_proxy_bridge_breaker_open == 1

# Track failure trend
rate(freebuff_proxy_bridge_breaker_failures[5m])
```

**Zero-value defaults:** When bridge mode is inactive or the breaker is disabled, both metrics emit `0` so pre-provisioned dashboards and alerts receive consistent data.

### 3. Admin Dashboard — Overview Page

The dashboard Overview page surfaces breaker state as part of the bridge-mode KPIs:

- **Breaker Status Badge:** Green ("Closed") or Red ("Open") indicator
- **Failure Count:** Current sliding-window count / threshold
- **Cooldown Timer:** Countdown when open

This provides at-a-glance awareness without needing to check `/healthz` or Prometheus directly.

---

## Log Events

The circuit breaker emits structured log events at `WARN` level when state changes:

```
pool: bridge circuit breaker opened
  failures=5 window=30s cooldown=10s until=2026-08-27T12:00:10Z

pool: bridge circuit breaker re-opened after cooldown expiry
  failures=3 until=2026-08-27T12:00:20Z
```

Set `LOG_LEVEL=debug` for verbose admission logs; the breaker state is always logged at `warn` when it trips or re-opens.

---

## Operational Patterns

### Detecting an Upstream Outage

### Tuning the Breaker

| Scenario | Recommendation |
|---|---|
| **False trips during slow upstream** | Increase `BRIDGE_CIRCUIT_BREAKER_WINDOW` (e.g., `60s`) |
| **Breaker never trips during real outage** | Decrease `BRIDGE_CIRCUIT_BREAKER_FAILURES` (e.g., `3`) |
| **Breaker stays open too long** | Decrease `BRIDGE_CIRCUIT_BREAKER_COOLDOWN` (e.g., `5s`) |
| **Breaker not needed** | Set `BRIDGE_CIRCUIT_BREAKER_FAILURES=0` (default) |

### Recovery After Trip

The breaker automatically recovers:

1. After `BRIDGE_CIRCUIT_BREAKER_COOLDOWN` elapses, the breaker closes
2. If failures in the sliding window still meet the threshold, the breaker re-opens immediately
3. Once failures age out of the window, the breaker stays closed

No manual intervention is required unless the upstream outage persists beyond the cooldown.

---

## Testing

Circuit breaker observability is covered by:

- **`TestHealthzCircuitBreaker`** — Verifies `/healthz` includes correct breaker fields in all states
- **`TestMetricsCircuitBreaker`** — Verifies Prometheus metrics export correct values in all states
- **`TestBridgeBreakerOpen`** — End-to-end test: breaker trips, requests fail fast, cooldown expires
- **`TestBridgeHardenedBreaker`** — Sliding window behavior, re-open after cooldown, transient-only trips

Run the full test suite to validate:

```bash
env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...
```

---

## Related Documentation

- [Bridge Mode](bridge-mode.md) — Bridge-mode architecture, invariants B1–B8, security
- [Ban-Avoidance & Signature Research](ban-avoidance.md) — Upstream detection and countermeasures
- [Upstream Drift Tracking](upstream-drift-tracking.md) — Registry freshness and sync
- [Architecture](../ARCHITECTURE.md) — System components and data flow
- [Roadmap](../ROADMAP.md) — Feature checklist and future plans


1. **Dashboard:** Red "Breaker Open" badge on Overview page
2. **Prometheus alert:** `freebuff_proxy_bridge_breaker_open == 1` fires
3. **Logs:** `pool: bridge circuit breaker opened` WARN line
4. **Client impact:** Bridge requests return `503 upstream_retryable` with `Retry-After`

### Diagnosing the Cause

When the breaker trips, the underlying issue is upstream instability (5xx/network):

1. Check upstream service status (if known)
2. Review recent `freebuff_proxy_bridge_requests_total` for error patterns
3. Check if the failures are from specific tokens or all bridge entries
4. Review the `failure_count` trend to determine if failures are accumulating or burst

| `until` | `string\|null` | RFC 3339 timestamp when the breaker will close (null when closed or disabled) |
| `config.failures_threshold` | `int` | The configured `BRIDGE_CIRCUIT_BREAKER_FAILURES` value |
| `config.window` | `string` | The configured sliding window duration |
| `config.cooldown` | `string` | The configured cooldown duration |

**Reading the output:**
- `enabled: false` → breaker not configured; no protection active.
- `enabled: true, open: false, failure_count < threshold` → healthy; some failures but not enough to trip.
- `enabled: true, open: true` → breaker tripped; all bridge requests blocked. Wait for `cooldown_remaining_seconds` to reach 0.
