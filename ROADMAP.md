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

- Extend Configuration Studio validation feedback and rollback ergonomics.
- Deepen effective-config table normalization and dedup.
- Add filtered per-model quota timelines beyond today's expandable rows.

### 2. Bridge mode hardening

- Improve cooldown/quota handling under a flood of distinct client tokens.
- Consider surfacing per-entry registry / quota info in bridge health without storing tokens.

### 3. Ban-avoidance & signature research

- Continue documenting upstream detection (per-request IP scoring, trust levels, sticky caps,
  daily ceilings, mass sweeps).
- Prototype header-sanitization refinements against real traffic signatures.

### 4. Upstream drift tracking

- Keep pinned upstream files under `internal/registry/testdata/upstream/` synced.
- Add warning-level alerts when drift is detected.

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