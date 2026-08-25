# Architecture — freebuff-proxy

> Living architecture document. Reflects the actual implementation in `main`.

---

## Overview

`freebuff-proxy` is a single-binary Go gateway that translates standard OpenAI and Anthropic API
requests into the FreeBuff/Codebuff CLI wire protocol and relays SSE responses back in native
format. A Svelte 5 SPA admin dashboard is embedded via `go:embed` and served under `/admin`.

```
┌────────────────────┐    OpenAI / Anthropic API    ┌────────────────────┐    FreeBuff CLI Wire    ┌──────────────┐
│   AI Client        │ ───▶ /v1/chat/completions ──▶│   freebuff-proxy   │ ───▶ Session Envelope ──▶│  FreeBuff    │
│   (OpenCode, Cline,│ ◀─── SSE chat.chunk ────────│   (Go binary)      │ ◀─── SSE relay ─────────│  Upstream    │
│    Cursor, 9router)│                              │   :3457            │                          │  codebuff.com│
└────────────────────┘                              └────────────────────┘                          └──────────────┘
                                                              │
                                                    ┌─────────▼──────────┐
                                                    │  Admin Dashboard   │
                                                    │  /admin (Svelte 5) │
                                                    └────────────────────┘
```

---

## Design Principles

1. **Single binary** — zero runtime dependencies; dashboard assets baked in at compile time.
2. **Stdlib-only HTTP** — no third-party router or framework; `net/http.ServeMux` (Go 1.22+).
3. **Composition over inheritance** — small focused packages with clear responsibilities.
4. **Zero-downtime config** — hot-reload via `atomic.Pointer[config.Config]`; in-flight requests never interrupted.
5. **Anti-ban discipline** — egress must match a real browser's TLS fingerprint, header set, and timing.

---

## Operating Modes

### Pooled Mode (default when `AUTH_TOKENS` is set)

The proxy owns a fixed pool of upstream FreeBuff tokens:

- Maintains one persistent session per token.
- Hot-session-first routing with failover on rate limits or cooldowns.
- Pacific-midnight quota resets, per-token daily caps, country-block fallback.
- Authenticates downstream clients via optional `API_KEYS` (constant-time comparison).

### Bridge Mode (active when `AUTH_TOKENS` is unset/empty)

Zero-storage relay. Each request carries the client's FreeBuff token in
`Authorization: Bearer <token>`, `x-api-key: <token>`, or `anthropic-api-key: <token>`:

- Lazily leases and caches upstream sessions per client token (LRU).
- Tracks cooldowns, health, and quota per bridge entry.
- Exposes bridge entry counts and health via `GET /healthz`.
---

## System Components

### cmd/freebuff-proxy

Entrypoint. Handles CLI flag parsing (`-doctor`, `-test-token`, `-version`, `-config`, `-setup`,
`-update`, `-install-service`), config loading, registry bootstrap, HTTP server lifecycle, and
graceful SIGINT/SIGTERM shutdown with drain.

### internal/config

Typed configuration loader. Supports `.env` files (comma-separated lists) and optional JSON
config file. Environment variables always override JSON values. Configuration held behind
`atomic.Pointer[config.Config]` for lock-free hot-reload on `POST /admin/reload`.

### internal/registry

Model catalog synchronized from upstream. Alias resolution, model validation, `ServedModels` gate
for `/v1/models` output. Background refresh on `RegistryRefresh` interval.

### internal/convert

Pure conversion logic (no I/O):

- `convert.go` — request normalization, role rewriting, legacy function normalization
- `accumulator.go` — non-streaming response assembler, XML tool call extraction, `Finish()` builder
- `effort.go` — reasoning effort extraction, thinking budget scaling, think tag stripping
- `schemacache.go` — `$ref` inlining, schema caching, `end_turn` tool injection
- `sse.go` — SSE frame encoding/decoding, chunk sanitization, end-turn stripping
- `streamxml.go` — incremental XML stream parser

### internal/server

HTTP router and handlers on `net/http.ServeMux`:

- `server.go` — `Server` struct, `Handler()` route registration, lifecycle
- `openai.go` — `/v1/chat/completions`, `/v1/responses`, `/v1/embeddings`, `/v1/models`
- `anthropic.go` — `/v1/messages`, `/v1/messages/count_tokens`
- `engine.go` / `engine_sse.go` — SSE relay abstraction and read loop
- `errors.go` — protocol-aware error formatting (OpenAI vs Anthropic)
- `middleware.go` — auth, CORS, access logging
- `admin*.go` — dashboard auth, CSRF, config editor, token management API

### internal/pool

Token lifecycle: `acquire.go` (hot-session-first lease), `bridge.go` (LRU bridge cache),
`cooldown.go` (auth-rejection cooldowns), `quota.go` (Pacific midnight windows),
`spend.go` (advisory spending), `unfit.go` (token fitness).

### internal/session / upstream / stealth

Session manager (admission, persistence, rotation), FreeBuff wire client (chat relay, rate-limit
parsing), TLS fingerprinting (utls profiles: chrome120, safari17, firefox120, etc.).

### internal/tokenestimate / runs / reasoningcache / ratelimit

Local `o200k_base` BPE tokenizer, run lifecycle (START/FINISH), multi-turn reasoning cache,
per-IP token bucket rate limiter.

### internal/telemetry / logring / dashboard

Prometheus metrics, circular log ring buffer (500 entries), embedded Svelte 5 SPA dashboard.

### internal/notify / phasetiming / egress / updatecheck / testutil

Notifications, request phase timing, egress tracking, GitHub release checker, shared test helpers.

---

## Request Flows

### Chat Completions (OpenAI)

```
Client POST → Middleware → convert.Convert() → pool.Acquire() → upstream.Chat() → relay SSE
```

### Messages (Anthropic)

```
Client POST → Middleware → convert → pool.Acquire() → upstream → relay SSE
   (message_start → content_block_start → deltas → stop → message_delta → message_stop)
```

---

## Error Mapping

| Condition | OpenAI | Anthropic |
|---|---|---|
| Auth rejected | `502 upstream_auth_rejected` | `authentication_error` |
| Rate limited | `503 Retry-After` | `rate_limit_error` |
| Waiting room | `503 Retry-After` | `overloaded_error` |
| Pool exhausted | `502 pool_exhausted` | `api_error` |
| Account banned | `502 account_banned` | `authentication_error` |
| Model not found | `404 model_not_found` | `invalid_request_error` |

---
## Load-Bearing Invariants

1. **Hermetic test suite** — always unset `AUTH_TOKENS` and `ADMIN_TOKEN`:
   ```bash
   env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...
   ```
2. **Anti-ban contract**:
   - Session POST sends `x-freebuff-model` and `x-freebuff-instance-id`.
   - Chat POST carries **NO** model header.
   - Pinned `ai-sdk` User-Agent on all chat requests.
   - Honest `FINISH` on run termination.
3. **Tool stripping parity**:
   - Proxy injects `end_turn` to satisfy upstream schema requirements.
   - `end_turn` MUST be stripped before emitting downstream.
4. **Sequential SSE content blocks**:
   - Anthropic streaming requires strictly sequential block lifecycles.
   - Never interleave unclosed blocks.

---

## Technology Stack

| Layer | Technology |
|---|---|
| Language | Go 1.26 |
| HTTP | `net/http` stdlib (no framework) |
| TLS Fingerprinting | `refraction-networking/utls` |
| Tokenizer | `tiktoken-go/tokenizer` (o200k_base) |
| Compression | `klauspost/compress`, `andybalholm/brotli` |
| Dashboard | Svelte 5 + Tailwind CSS v4 (embedded via `go:embed`) |
| Typography | IBM Plex Sans + IBM Plex Mono (self-hosted) |
| Observability | Prometheus `/metrics`, slog structured logging |
| CI | GitHub Actions |

---

## Related Documentation

- [README](README.md) — Overview, quick start, configuration reference
- [Specification](SPECIFICATION.md) — API contracts, endpoint schemas, behavioral rules
- [Roadmap](ROADMAP.md) — Current status, planned work, known limitations
- [Design System](DESIGN.md) — Dashboard design system (typography, layout, components)
- [Getting Started](docs/getting-started.md) — 5-minute setup walkthrough
- [Client Integration](docs/client-integration.md) — OpenAI-compatible client configs
- [Dashboard Guide](docs/dashboard.md) — Admin web UI reference
