# Specification — freebuff-proxy API & Behavior Contract

> Defines the proxy's public API surface, request/response contracts, and behavioral rules.
> Integrators implement against this spec.

---

## Table of Contents

1. [HTTP API Surface](#http-api-surface)
2. [OpenAI Chat Completions](#1-openai-chat-completions-post-v1chatcompletions)
3. [OpenAI Responses API](#2-openai-responses-api-post-v1responses)
4. [OpenAI Models API](#3-openai-models-api)
5. [Anthropic Messages API](#4-anthropic-messages-api)
6. [Anthropic Count Tokens](#5-anthropic-count-tokens)
7. [Embeddings](#6-embeddings-post-v1embeddings)
8. [Admin Dashboard Routes](#7-admin-dashboard-routes)
9. [Auth & Middleware](#8-auth--middleware)
10. [Error Contract](#9-error-contract)
11. [Streaming Protocol](#10-streaming-protocol)
12. [Configuration Reference](#11-configuration-reference)
13. [Behavioral Rules](#12-behavioral-rules)

---

## HTTP API Surface

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/healthz` | None | Health check, uptime, model count, token snapshots |
| `GET` | `/metrics` | None | Prometheus metrics endpoint |
| `GET` | `/v1/models` | `requireAuth` | List all served models |
| `GET` | `/v1/models/{model}` | `requireAuth` | Retrieve a single model |
| `POST` | `/v1/chat/completions` | `requireAuth` | OpenAI chat completions (stream + non-stream) |
| `POST` | `/v1/responses` | `requireAuth` | OpenAI Responses API |
| `POST` | `/v1/messages` | `requireAuth` | Anthropic Messages API |
| `POST` | `/v1/messages/count_tokens` | `requireAuth` | Anthropic token count (local estimation) |
| `POST` | `/v1/embeddings` | `requireAuth` | Embeddings proxy (returns `400 unsupported_endpoint`) |
| `POST` | `/admin/reload` | `requireAdminToken` | Hot-reload configuration |
| `GET` | `/admin/*` | Dashboard auth | Svelte 5 SPA dashboard |
| `POST` | `/admin/login` | Dashboard auth | Login (consumes per-IP budget) |
| `POST` | `/admin/config` | Dashboard auth | Save `.env` configuration |
| `POST` | `/admin/tokens/*` | Dashboard auth | Token management (add, remove, test, unlock) |
| `POST` | `/admin/bridge-tokens/*` | Dashboard auth | Bridge token lock/unlock |

All request bodies are limited to 32 MB. SSE line buffer is 16 MB.
---

## 1. OpenAI Chat Completions (`POST /v1/chat/completions`)

### Request Schema

Standard OpenAI chat completions request body with extensions:

| Field | Type | Required | Notes |
|---|---|---|---|
| `model` | string | Yes | Model ID from `/v1/models` |
| `messages` | array | Yes | Standard message array |
| `stream` | bool | No (default false) | Enable SSE streaming |
| `max_tokens` | int | No | Generation budget |
| `temperature` | float | No | Sampling temperature |
| `top_p` | float | No | Nucleus sampling |
| `stop` | string/array | No | Stop sequences |
| `tools` | array | No | Tool definitions (converted to upstream schema) |
| `tool_choice` | string/object | No | Tool selection mode |
| `functions` | array | No | **Legacy** — auto-converted to `tools` |
| `function_call` | string/object | No | **Legacy** — auto-converted to `tool_choice` |
| `reasoning_effort` | string | No | Ladder: `minimal`, `low`, `medium`, `high`, `xhigh` |
| `user` | string | No | Client identifier |

### Streaming Response (SSE)

```
data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":...,"model":"deepseek/deepseek-v4-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"..."},"finish_reason":null,"logprobs":null,"refusal":null}]}

data: [DONE]
```

**Properties:**
- `refusal: null` and `logprobs: null` for OpenAPI 3.1 compliance.
- Tool calls extracted from upstream XML (`<tool_call>`, `<codebuff_tool_call>`, etc.) into native `tool_calls` deltas with synthetic indices.
- `finish_reason`: `"tool_calls"` for function calls, `"stop"` for normal completion.

### Non-Streaming Response

```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "created": 1735689600,
  "model": "deepseek/deepseek-v4-flash",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "..."
    },
    "finish_reason": "stop",
    "logprobs": null,
    "refusal": null
  }],
  "usage": {
    "prompt_tokens": 100,
    "completion_tokens": 200,
    "total_tokens": 300
  }
}
---

## 2. OpenAI Responses API (`POST /v1/responses`)

The `/v1/responses` endpoint supports the OpenAI Responses API format. The proxy translates
Responses API requests into its internal Chat Completions pipeline.

Supported features:
- `input` array (translated to `messages`)
- `model` selection
- `text` format configuration
- `tools` (web search, file search, code interpreter maps to upstream tool schema)
- `reasoning.effort` normalized to same ladder as chat completions
- Streaming SSE events: `response.output_text.delta` and `response.completed`

---

## 3. OpenAI Models API

### GET /v1/models

Returns the full model catalog. Response is OpenAI-compatible:

```json
{
  "object": "list",
  "data": [
    {
      "id": "deepseek/deepseek-v4-flash",
      "object": "model",
      "created": 1735689600,
      "owned_by": "freebuff-proxy"
    }
  ]
}
```

**Filters:**
- `ModelsHideUnavailable=true` prunes models marked unavailable (region, quota, lock).
- `ModelsAllow` (comma-separated allowlist) restricts catalog to specified IDs only.

### GET /v1/models/{model}

Retrieve a single model. Supports slash-delimited model names (e.g. `deepseek/deepseek-v4-flash`).

```json
{
  "id": "deepseek/deepseek-v4-flash",
  "object": "model",
  "created": 1735689600,
  "owned_by": "freebuff-proxy"
}
```

Returns `404` for unknown models.

---

## 4. Anthropic Messages API (`POST /v1/messages`)

### Request Schema

Standard Anthropic Messages API format:

```json
{
  "model": "claude-sonnet-4-20250514",
  "messages": [{"role": "user", "content": "Hello"}],
  "system": "Optional system prompt",
  "max_tokens": 8192,
  "stream": true,
  "tools": [...],
  "tool_choice": {"type": "auto"}
}
```

**Version header:** `anthropic-version: 2023-06-01` is recognized and validated.

### Streaming Response (SSE)

| Event | Payload Shape |
|---|---|
| `message_start` | `{"type":"message_start","message":{"id":"msg_xxx","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":100,"output_tokens":1}}}` |
| `content_block_start` | `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"...","signature":""}}` |
| `content_block_delta` | `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"..."}}` |
| `content_block_stop` | `{"type":"content_block_stop","index":0}` |
| `message_delta` | `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":200}}` |
| `message_stop` | `{"type":"message_stop"}` |
| `ping` | `{"type":"ping"}` (keepalive) |

**Contract:**
- Thinking blocks include `"signature": ""` for TypeScript/Zod schema compliance.
- Tool calls use `input_json_delta` and `signature_delta` deltas.
- Proxy-injected `end_turn` tools are stripped before emitting.
- Content blocks are strictly sequential: `start → delta(s) → stop`.

### Non-Streaming Response

```json
{
  "id": "msg_xxx",
  "type": "message",
  "role": "assistant",
  "content": [
    {"type": "text", "text": "Hello!"}
  ],
  "model": "claude-sonnet-4-20250514",
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {
    "input_tokens": 100,
    "output_tokens": 200
  }
}
```

### Error Format

```json
{
  "type": "error",
  "error": {
    "type": "authentication_error",
    "message": "Upstream auth rejected"
  }
}
```

Error types: `authentication_error`, `rate_limit_error`, `overloaded_error`, `api_error`,
`invalid_request_error`.
---

## 5. Anthropic Count Tokens (`POST /v1/messages/count_tokens`)

Local deterministic token estimation using `o200k_base` BPE tokenizer.

### Request

```json
{
  "model": "deepseek/deepseek-v4-flash",
  "messages": [{"role": "user", "content": "Hello"}],
  "system": "Optional system prompt"
}
```

### Response

```json
{
  "input_tokens": 12,
  "output_tokens": 0,
  "counts": {
    "role_counts": {"user": 8, "system": 4},
    "total": 12
  }
}
```

**Notes:**
- Model must be in the served registry or `400` is returned.
- Malformed JSON returns `400` with error detail.
- Body over 32 MB returns `413 Payload Too Large`.
- Supports document/image content types (base64-encoded) for accurate token counting.

---

## 6. Embeddings (`POST /v1/embeddings`)

Returns `400` with an `unsupported_endpoint` error message. The upstream FreeBuff service
does not provide embeddings as a separable API.

---

## 7. Admin Dashboard Routes

All under `/admin/`. Cookie-authenticated via HMAC-signed `fb_admin` session cookie.

| Route | Method | Auth | Description |
|---|---|---|---|
| `/admin/login` | GET | None | Login page (SPA) |
| `/admin/login` | POST | CSRF | JSON login (submits `ADMIN_TOKEN`) |
| `/admin/logout` | GET/POST | Cookie | Clear session |
| `/admin/` | GET | Cookie | Dashboard SPA shell |
| `/admin/api/overview` | GET | Cookie + `adminSensitive` | Pool overview, KPIs |
| `/admin/api/tokens` | GET | Cookie | Token list and quotas |
| `/admin/api/models` | GET | Cookie | Model catalog |
| `/admin/api/config` | GET | Cookie + `adminSensitive` | Effective config (secrets redacted) |
| `/admin/api/logs` | GET | Cookie + `adminSensitive` | Live log stream |
| `/admin/api/setup` | GET | Cookie | Client setup snippets |
| `/admin/api/metrics` | GET | Cookie | Metrics data |
| `/admin/config` | POST | Cookie + CSRF + `adminSensitive` | Save `.env` |
| `/admin/tokens/add` | POST | Cookie + CSRF + `adminSensitive` | Add token |
| `/admin/tokens/remove` | POST | Cookie + CSRF + `adminSensitive` | Remove token |
| `/admin/tokens/{id}/test` | POST | Cookie + CSRF + `adminSensitive` | Test token validity |
| `/admin/tokens/{id}/unlock` | POST | Cookie + CSRF + `adminSensitive` | Clear cooldown |
| `/admin/reload` | POST | Bearer `ADMIN_TOKEN` | Hot-reload config |

**Access control:**
- `ADMIN_TOKEN` set: password required; `HttpOnly` + `SameSite=Strict` cookie (24h expiry).
- `ADMIN_TOKEN` unset: open when accessed from loopback; forbidden from remote IPs.
- Failed login attempts rate-limited per IP (5 failures → 1-minute lockout).

---

## 8. Auth & Middleware

### Client Authentication (`requireAuth`)

Checked in order:

1. **Authorization: Bearer <key>** — matched against configured `API_KEYS` (constant-time).
2. **x-api-key: <key>** — same matching as above.
3. **Bridge mode** (when `AUTH_TOKENS` empty): the bearer token or `x-api-key` is treated as a
   FreeBuff token for dynamic session leasing.

Returns `401 Unauthorized` if no matching key is found and not in bridge mode.

### Admin Authentication

- `POST /admin/reload` requires `Authorization: Bearer <admin_token>` (exact match).
- Dashboard SPA uses HMAC-signed `fb_admin` session cookie (stateless, per-process key).
- CSRF protection via double-submit cookie pattern on mutating admin routes.

### CORS

- `Access-Control-Allow-Origin` set to `CORSAllowedOrigin` (default `*`).
- Standard preflight headers.

### Access Logging

- Per-request `slog` lines (enabled by `LOG_ACCESS`, default on).
- Logged fields: method, path, status, duration, client IP, model, token hint.

---

## 9. Error Contract

### OpenAI Error Shapes

```json
{
  "error": {
    "message": "Upstream auth rejected",
    "type": "upstream_auth_rejected",
    "code": 502
  }
}
```

### Anthropic Error Shapes

```json
{
  "type": "error",
  "error": {
    "type": "authentication_error",
    "message": "Upstream auth rejected"
  }
}
```

### Error Mapping Table

| Upstream Condition | OpenAI (status + body) | Anthropic (error type) | Retry-After |
|---|---|---|---|
| Auth token rejected | `502 upstream_auth_rejected` | `authentication_error` | 30 min cooldown |
| Rate limited (429) | `503 rate_limited` | `rate_limit_error` | Yes (from upstream) |
| Waiting room queued | `503 waiting_room_queued` | `overloaded_error` | Yes (from upstream) |
| All tokens exhausted | `502 pool_exhausted` | `api_error` | None |
| Account banned | `502 account_banned` | `authentication_error` | Resume time if temp |
| Model not found | `404 model_not_found` | `invalid_request_error` | None |
| Retired Luna agent | `502 retired_luna_agent` | `api_error` | Auto-retry with fresh session |
| Body too large | `413 Payload Too Large` | `invalid_request_error` | None |

**Retry behavior:** Session-invalid and run-invalid errors are automatically retried once
with a fresh session before returning an error to the client.
---

## 10. Streaming Protocol

### SSE Format

Standard Server-Sent Events format:

```
event: <event_type>
data: <json_payload>

```

Terminal event for OpenAI streaming: `data: [DONE]` (no event type).

### OpenAI Chat Completions

- `event:` line is omitted (raw `data:` lines).
- Each `data:` line is a `chat.completion.chunk` JSON object.
- Stream ends with `data: [DONE]`.

### Anthropic Messages

- Named events: `message_start`, `content_block_start`, `content_block_delta`,
  `content_block_stop`, `message_delta`, `message_stop`, `ping`.
- `event: ping` keepalive sent periodically.
- Content blocks are strictly sequential: a block must reach `content_block_stop`
  before the next `content_block_start`.

### OpenAI Responses API

- Named events: `response.output_text.delta`, `response.completed`.
- Maps upstream SSE chunks to Responses API event format.

### Client Disconnect

- When the client disconnects, the request context is cancelled.
- The upstream body reader returns promptly.
- Active runs are terminated with `FINISH`.

---

## 11. Configuration Reference

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `127.0.0.1:3457` | HTTP listen address |
| `UPSTREAM_BASE_URL` | `https://codebuff.com` | Upstream FreeBuff URL |
| `AUTH_TOKENS` | `` | Comma-separated FreeBuff tokens (pooled mode) |
| `API_KEYS` | `` | Comma-separated client auth keys |
| `ADMIN_TOKEN` | `` | Dashboard/bearer admin password |
| `SAFE_MODE` | `true` | Apply anti-ban defaults |
| `TLS_FINGERPRINT` | `auto` | utls profile for egress |
| `HTTP2_UPSTREAM` | `false` | Negotiate HTTP/2 with upstream |
| `REQUEST_TIMEOUT` | `15m` | Upstream request timeout |
| `SESSION_CALL_TIMEOUT` | `30s` | Session call timeout |
| `REGISTRY_REFRESH` | `6h` | Model catalog refresh interval |
| `ROTATION_INTERVAL` | `6h` | Token rotation interval |
| `IDLE_ROTATION_TIMEOUT` | `0` (disabled) | Pause rotation after idle |
| `SESSION_IDLE_END` | `0` (disabled) | End sessions after idle |
| `MAX_MESSAGES_PER_DAY` | `0` (unlimited) | Per-token daily chat cap |
| `BRIDGE_DAILY_LIMIT` | `0` (unlimited) | Global bridge daily chat cap |
| `MAX_SPEND_PER_DAY` | `0` (unlimited) | Advisory spend ceiling |
| `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `LOG_FORMAT` | `text` | Log format (text, json) |
| `LOG_ACCESS` | `true` | Per-request access logging |
| `LOG_RING_SIZE` | `500` | Dashboard log ring buffer (50-5000) |
| `LOG_FILE` | `` | Optional log file path |
| `DEBUG_DUMP` | `false` | Dump request/response bodies |
| `CORS_ALLOWED_ORIGIN` | `*` | CORS origin |
| `REQUEST_JITTER` | `0` | Random delay before upstream calls |
| `TRANSIENT_RETRIES` | `1` | Max transport failure retries |
| `MODELS_HIDE_UNAVAILABLE` | `false` | Prune unavailable models |
| `MODELS_ALLOW` | `` | Model allowlist |
| `MODEL_ALIASES` | `` | Model alias map |
| `CLI_VERSION` | `0.10.7` | Upstream CLI version string |
| `SESSION_PERSIST` | `false` | Persist sessions to disk |
| `SESSION_STATE_FILE` | `.freebuff-session-state.json` | Session state file path |
| `COST_MODE` | `free` | A/B pending |
| `ACTING_USER_ID` | `` | FreeBuff account id (DANGER: must match token) |
| `DASHBOARD_ENABLED` | `true` | Enable admin dashboard |

### Configuration Precedence

1. `.env` file (auto-discovered in working directory and parent directories)
2. JSON config file (`-config` flag)
3. Environment variables (always win over file values)
---

## 12. Behavioral Rules

### Pooled Mode Rules

- Tokens consumed in hot-session-first order (round-robin start, failover on cooldown/quota).
- On `429` (rate limit), token locked locally for upstream `Retry-After` period.
- On auth rejection, token enters 30-minute cooldown.
- Pacific-midnight quota resets via embedded IANA tzdata.
- `SAFE_MODE=true` enables: TLS fingerprinting, header sanitization, request jitter,
  idle rotation, conservative routing.

### Bridge Mode Rules

- No pre-configured tokens; each request authenticates itself.
- Sessions lazily created and LRU-cached per client token.
- Bridge entries have own cooldown, quota, and health tracking.
- Bridge health visible in `GET /healthz`.

### Anti-Ban Protocol

- **Session POST**: sends `x-freebuff-model` and `x-freebuff-instance-id`.
- **Chat POST**: carries **NO** model header (critical stealth rule).
- **User-Agent**: pinned to `ai-sdk` on all chat requests.
- **Run termination**: honest `FINISH` sent on every run completion.
- **TLS fingerprint**: configurable via `TLS_FINGERPRINT`; `auto` picks best match.
- **Header sanitization**: automation-revealing headers stripped from egress.
- **Request jitter**: random delay `[0, RequestJitter)` before upstream calls.
- **Idle rotation**: `IDLE_ROTATION_TIMEOUT` pauses rotation during idle periods.

### Tool Handling

- `end_turn` pseudo-tool injected into upstream schema requests.
- `end_turn` always stripped from downstream responses.
- Tool schemas normalized: `$ref` references inlined via `schemacache`.
- Legacy `functions`/`function_call` auto-converted to `tools`/`tool_choice`.
- Streaming XML tool calls extracted into native `tool_calls` deltas.

### Reasoning Effort Ladder

| Input Value | Behavior |
|---|---|
| `minimal` | No thinking/reasoning |
| `low` | Short thinking budget |
| `medium` | Default thinking budget |
| `high` | Extended thinking budget |
| `xhigh` | Maximum thinking budget |
| ` `` (empty)` | No thinking (default) |

Think tags (` thinking... `) in upstream responses stripped and converted to
`thinking` content blocks in Anthropic format.

### Model Selection

- Model IDs follow upstream convention: `provider/model-name`.
- `MODEL_ALIASES` maps custom names to real model IDs.
- `MODELS_ALLOW` restricts catalog to operator-defined allowlist.
- Limited-tier accounts: all model requests coerced to `mimo/mimo-v2.5` upstream.
- `/v1/models` returns live catalog synced from upstream at `REGISTRY_REFRESH`.

---

## 13. Related Documentation

- [Architecture](ARCHITECTURE.md) — System architecture, components, data flow
- [Roadmap](ROADMAP.md) — Current status, planned work, known limitations
- [README](README.md) — Overview, quick start, configuration reference
- [Getting Started](docs/getting-started.md) — 5-minute setup walkthrough
- [Client Integration](docs/client-integration.md) — OpenAI-compatible client configs
- [Dashboard Guide](docs/dashboard.md) — Admin web UI reference
```