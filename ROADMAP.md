# Roadmap — freebuff-proxy

> Tracks current status, in-progress work, known limitations, and future plans.
> Maintainers: keep this in sync with the actual repository state.

---

## Current Status

**Version baseline:** Go 1.26 gateway, dual OpenAI + Anthropic protocol surface, embedded
Svelte 5 dashboard. Single unified trunk on `main`.

The proxy is feature-complete for its core mission (translate OpenAI/Anthropic requests into
FreeBuff's CLI wire protocol, pool/bridge FreeBuff tokens, stream native SSE responses).
Development is ongoing around the admin dashboard, ban-avoidance hardening, and upstream protocol
drift tracking.

---

## Feature Checklist (Implemented)

- [x] OpenAI Chat Completions (`/v1/chat/completions`) — stream + non-stream
- [x] OpenAI Responses API (`/v1/responses`)
- [x] OpenAI Models API (`/v1/models`, `/v1/models/{model}`)
- [x] Anthropic Messages API (`/v1/messages`) — stream + non-stream
- [x] Anthropic Count Tokens (`/v1/messages/count_tokens`)
- [x] Embeddings proxy (`/v1/embeddings`) — returns `unsupported_endpoint`
- [x] Pooled mode with multi-token failover and cooldowns
- [x] Bridge mode with per-token lazy session leasing (LRU)
- [x] TLS fingerprinting (utls) + header sanitization + request jitter
- [x] Pacific-midnight quota reset, daily spend caps, country-block handling
- [x] Local `o200k_base` tokenizer for Anthropic token counting
- [x] Streaming XML tool-call extraction (multiple tool XML dialects)
- [x] Legacy `functions`/`function_call` normalization
- [x] Reasoning effort ladders and think-tag extraction
- [x] `end_turn` injection (upstream schema) + stripping (downstream)
- [x] Run lifecycle START/FINISH with honest termination
- [x] Multi-turn reasoning cache
- [x] Prometheus `/metrics`
- [x] Per-IP rate limiting
- [x] Session persistence across restarts
- [x] Admin dashboard (Svelte 5): Overview, Tokens, Models, Config, Logs, Setup
- [x] Hot config reload (`/admin/reload`, config studio)
- [x] CLI diagnostics: `-doctor`, `-test-token`, `-setup`, `-version`, `-update`
- [x] Service install (`-install-service`) for systemd/launchd/Task Scheduler
- [x] OAuth device-code login wizard for token minting
- [x] Offline model registry + upstream drift sync scripts
- [x] Docker + systemd + launchd + install scripts

---

## Known Limitations & Risks

Tracking items; some are upstream constraints that cannot be fixed in this repo.

| Item | Nature | Notes |
|---|---|---|
| Upstream protocol drift | External risk | FreeBuff/Codebuff wire protocol is undocumented and changes; proxy re-implements ~99% CLI parity and can break until adapted. Mitigation: drift sync scripts, pinned fixtures, CI check. |
| Terms-of-service risk | External risk | Using FreeBuff tokens outside the official CLI conflicts with ToS; abuse detection can suspend/ban accounts. Nothing guarantees immunity. |
| Model coercion on limited-tier IPs | Upstream behavior | All requests from non-Tier-1 IPs coerced to `mimo/mimo-v2.5` server-side regardless of model sent. |
| MiniMax M3 temporary unavailability | Upstream behavior | Listed in catalog with `unavailable` annotation. |
| V4 Pro / Luna capped to 1 session/day | Upstream quota | Full-tier cap imposed upstream. |
| `ActingUserID` safety | Proxy gap | Header only honored for a token's own account id; auto-deriving each token's own id is deferred. |
| Admin dashboard sensitive gating | Security | When `ADMIN_TOKEN` unset, sensitive routes are blocked from non-loopback. |

---

## Roadmap: Current / In-Flight Work

Reflects current focus; items are not commitments.

### 1. Dashboard / Admin polish

Configuration Studio:
- [x] Add client-side diff preview showing exactly what changed before save
- [x] Enhance validation feedback with categorized error types (syntax, format, security)
- [x] Show changed keys count prominently with breakdown (added/removed/modified)

Effective Config Table:
- [x] Add search/filter input for the config table
- [x] Group config keys by category (core, auth, upstream, dashboard, performance)
- [x] Highlight recently changed keys with visual indicator

Token Quota Timeline:
- [x] Add model filter dropdown for quota breakdown
- [x] Visual quota usage bars with proportional fill
- [x] Color-code near-limit quotas (warn at 80%, critical at 95%)

UI/UX Polish:
- [x] Improve error/success banner auto-dismiss timing (5s success, 10s error)
- [x] Add loading spinners for async token actions (test, unlock, lock)
- [x] Consistent button spacing and icon alignment across pages

### 2. Bridge mode hardening

Documentation + planning:
- [x] Author `docs/bridge-mode.md` — architecture, invariants B1–B8, security notes, error surfaces, testing, hardening checklist
- [x] Track Phase 2 progress in `session.md`

Security:
- [x] Token format validation (`validateClientToken`: length bound + interior whitespace/control rejection; `cb_` prefix deliberately not hard-enforced)
- [x] Log sanitization audit — no raw tokens in logs/metrics (verified with pool-level log capture tests + metric label hygiene tests)
- [x] Per-client-token rate limiting (independent of per-IP) — `BridgeRateLimitPerToken` config, per-entry token bucket, tested
- [x] Header smuggling / injection tests (`Authorization`, `x-api-key`, `anthropic-api-key`) — precedence, no-concatenation, multiple-value, authorized gate parity, integration
- [x] Quota exhaustion edge-case tests — global daily limit, per-entry cap + cooldown intersection, cooldown then cap

Reliability:
- [x] Transient upstream failure retry (bounded, idempotent) — `TRANSIENT_RETRIES` config with `GetBody` replay, extensive client_retry_test coverage
- [x] Circuit breaker for batch upstream failures — `bridge_breaker.go` implemented: sliding window, transient-only trips (5xx/network), configurable failures/window/cooldown (`BridgeCircuitBreakerFailures/Window/Cooldown`), tested in `bridge_hardening_test.go`
- [x] Clear diagnostics for invalid/expired tokens — `errors.go` `remediationMessage()` has actionable gen-token/wait instructions per error code
- [x] Improved quota exhaustion messaging — `errors.go` `remediationMessage()` with reset time, Retry-After, and actionable hints per code

Code quality & tests:
- [x] Extract bridge admission/eviction into pure-unit-testable paths — `rateLimitAllow()` extracted, `newBridgePoolCfg` helper added
- [x] Fuzz tests for token handling — `FuzzValidateClientToken`, `FuzzTokenKey` (never panics, property-checked)
- [x] Property-based LRU eviction verification — `TestBridgeLRUProperty` (cache size ≤ max, random access patterns)
- [x] Load test for thundering-herd creation — `TestBridgeThunderingHerd` (40 concurrent requests, 20 distinct tokens, all complete)

Metrics & observability:
- [x] Bridge-specific Prometheus metrics (entries, evictions, cooldowns) — `freebuff_proxy_bridge_entries_total`, `cooling_down_total`, `dead_tokens_total`, `locked_total`, `requests_total`, `active_runs`, `quota_remaining`
- [x] Per-entry quota introspection in `/admin` without plaintext token exposure — `BridgeSnapshot` returns hashed key + quota/messages/spend/ban, no raw token
- [x] `/healthz` bridge indicators (entry count, cooling-down, dead-token) — exposed as `bridge_tokens` count and `bridge_entries[]` with `dead_token`, `cooldown_until`, `locked` per entry

### 3. Ban-avoidance & signature research

- [x] Document upstream detection mechanisms:
  - Per-request IP scoring and privacy signals
  - Per-account trust levels and sticky caps
  - IP-capping and daily spend ceilings
  - Mass sweeps against known farm patterns
  - Wire protocol fingerprinting vectors
- [x] Document proxy countermeasures:
  - Anti-ban contract invariants (A1–A5)
  - TLS fingerprinting profiles and rotation
  - Header sanitization and browser profile application
  - Request jitter and idle rotation
  - Cooldown/unfit registries and scarce model protection
  - Safe mode presets and operator hygiene rules
- [x] Document Risk Engine architecture:
  - Scoring rules and risk thresholds
  - Sample sources and aggregation
  - Dashboard exposure and read-only design
- [x] Create `docs/ban-avoidance.md` — comprehensive ban-avoidance & signature research documentation
- [ ] Phase 3.2: Header-sanitization refinements against real traffic
- [ ] Phase 3.3: Timing & behavioral analysis
- [ ] Phase 3.4: TLS fingerprint drift monitoring
- [ ] Phase 3.5: Risk engine improvements
- [ ] Phase 3.6: Egress probe enhancements

### 4. Upstream drift tracking

- [x] Pinned upstream snapshots — `internal/registry/testdata/upstream/` with 5 constant files
- [x] Hardcoded fallback tables — `fallbackAgents` and `fallbackRootByModel` in `internal/registry/registry.go`
- [x] Parity test — `TestFallbackParityWithPinnedUpstream` enforces sync
- [x] Drift detection — `scripts/check-upstream.sh` read-only hash comparison
- [x] Sync automation — `scripts/sync-upstream.sh` / `.ps1` fetch, copy, verify, test
- [x] CI integration — Weekly `upstream-drift` workflow (Monday 06:17 UTC)
- [x] Runtime self-healing — Live registry refresh (6h default)
- [x] Create `docs/upstream-drift-tracking.md` — comprehensive drift tracking documentation
- [x] Phase 4.2: Warning-level alerts (webhook, Prometheus, /healthz)
- [x] Phase 4.3: Protocol drift detection (error-classification + field + timestamp regression tests)
- [ ] Phase 4.4: Automated sync automation (auto-PR, auto-fallback)
- [ ] Phase 4.5: Egress behavior tracking (admission, quota windows)
- [x] Phase 4.6: Documentation & runbooks (troubleshooting, rollback)

---

## Planned / Backlog

Rough backlog; feasibility often depends on (undocumented) upstream behavior.

- [ ] Reasoning-effort mapping to newly added upstream reasoning engines.
- [ ] Proxy-side model gating improvements (per-tier, per-region) driven by `x-freebuff-model`.
- [ ] Multi-profile egress abstraction layered over `TLS_FINGERPRINT` for more browser shapes.
- [ ] Optional alert webhooks for ban detection / quota exhaustion via `internal/notify`.
- [ ] Document systemd/launchd/Task Scheduler status deeper in Getting Started.
- [ ] Bridge-mode quota introspection surfaced per entry in `/admin` without plaintext exposure.

---

## Deferred / Follow-Up (Long Term)

Consciously parked unless the upstream or a successor strategy removes the blocker.

- JSON schema validation of responses for stricter downstream clients.
- Consolidate configuration flow between the dashboard and `-setup` CLI.
- Reverse-engineer remaining upstream schema differences toward ~100% wire parity.
- Regional (China / datacenter) support within existing tiers, if upstream changes.

---

## Future Work Recommendations

Based on the current state of the project and recent merge, the following recommendations
are organized by priority and impact. These complement the existing roadmap items above.

### High Priority (Production Hardening)

1. **Circuit Breaker Observability**
   - [x] Expose circuit breaker state in `/healthz` (open/closed, failure count, cooldown remaining)
   - [x] Add Prometheus metrics: `freebuff_proxy_bridge_breaker_open`, `freebuff_proxy_bridge_breaker_failures`
   - [ ] Surface breaker state in dashboard Overview page

2. **Bridge Mode Quota Introspection Dashboard**
   - Per-entry quota visualization with model-level breakdown
   - Historical quota usage charts (Pacific-day window)
   - Rate limit hit/miss counters per entry

3. **Automated Token Rotation**
   - Detect tokens approaching quota exhaustion
   - Suggest or auto-rotate to backup tokens
   - Integrate with `-test-token` for health validation

4. **Enhanced Error Remediation**
   - Expand `remediationMessage()` with actionable steps for each error class
   - Link to relevant documentation sections in error responses
   - Add dashboard toast notifications for common fixable errors

### Medium Priority (Feature Completeness)

5. **Multi-Profile Egress Abstraction**
   - Support rotating between Chrome, Firefox, Safari profiles automatically
   - Profile selection based on request characteristics
   - CI testing against profile drift

6. **Request-Level Cost Tracking**
   - Per-request token usage logging
   - Aggregated cost dashboards per model/client
   - Integration with `MaxSpendPerDay` for budget enforcement

7. **Advanced Rate Limiting**
   - Per-model rate limits (not just per-token)
   - Burst allowance configuration
   - Adaptive rate limiting based on upstream feedback

8. **Session Persistence Improvements**
   - Cross-restart session continuity validation
   - Session state versioning for backward compatibility
   - Session migration tools for pool changes

### Low Priority (Nice-to-Have)

9. **Dashboard Enhancements**
   - Dark/light theme toggle (despite "instrument panel" philosophy)
   - Exportable configuration snapshots
   - Audit log for admin actions
   - WebSocket-based real-time updates (replacing polling)

10. **Developer Experience**
    - Plugin system for custom middleware
    - Webhook support for upstream events
    - GraphQL API for complex queries
    - gRPC transport option

11. **Advanced Ban Avoidance**
    - Machine learning-based risk scoring
    - Crowd-sourced IP reputation database
    - Automated IP rotation (with upstream consent)
    - Honeypot detection for upstream probes

12. **Multi-Tenant Support**
    - Per-user quotas and rate limits
    - Role-based access control
    - Usage billing integration
    - Tenant isolation guarantees

### Research & Exploration

13. **Upstream Protocol Analysis**
    - Formal protocol specification (reverse-engineered)
    - Protocol versioning detection
    - Backward compatibility layers
    - Forward compatibility negotiation

14. **Performance Optimization**
    - Connection pooling improvements
    - Request batching for upstream
    - Response caching for repeated queries
    - HTTP/3 support

15. **Security Hardening**
    - Zero-trust architecture for multi-user deployments
    - mTLS between proxy and upstream
    - Secret rotation automation
    - Compliance logging (SOC2, GDPR)

---

## Contributing to the Roadmap

See [CONTRIBUTING](CONTRIBUTING.md):

- Open an issue and discuss first, especially where behavior depends on the undocumented upstream.
- Keep docs in public places (`README.md`, `docs/`, plus these top-level docs).
- Route improvements are often better as configuration defaults; deliberate first.

---

## Related Documentation

- [Architecture](ARCHITECTURE.md) — system architecture, components, routing
- [Specification](SPECIFICATION.md) — API surface and behavior contracts
- [README](README.md) — overview, quick start, configuration
- [Design System](DESIGN.md) — dashboard design
- [docs/](docs/) — Getting Started, Client Integration, Testing, Dashboard, 9router

---

*Tracked by* maintainers and CI. Keep this roadmap accurate.

---

## Contributing to the Roadmap

See [CONTRIBUTING](CONTRIBUTING.md):

- Open an issue and discuss first, especially where behavior depends on the undocumented upstream.
- Keep docs in public places (`README.md`, `docs/`, plus these top-level docs).
- Route improvements are often better as configuration defaults; deliberate first.

---

## Related Documentation

- [Architecture](ARCHITECTURE.md) — system architecture, components, routing
- [Specification](SPECIFICATION.md) — API surface and behavior contracts
- [README](README.md) — overview, quick start, configuration
- [Design System](DESIGN.md) — dashboard design
- [docs/](docs/) — Getting Started, Client Integration, Testing, Dashboard, 9router

---

*Tracked by* maintainers and CI. Keep this roadmap accurate.