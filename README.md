# freebuff-proxy

Go wire gateway in front of FreeBuff, with OpenAI-compatible and Anthropic
endpoints plus an embedded Svelte dashboard.

## What it is

- Speaks OpenAI chat (`POST /v1/chat/completions`, `GET /v1/models`) and an
  Anthropic-compatible layer, then translates to the FreeBuff wire protocol.
- Runs in pooled, bridge, or hybrid mode (`EffectiveMode`):
  - **Pooled** — `AUTH_TOKENS` set + `BRIDGE_ENABLED=0`; pool only.
  - **Bridge** — `AUTH_TOKENS` empty; each request carries its own token.
  - **Hybrid** (default with `AUTH_TOKENS`) — `API_KEYS` credential uses the
    pool, any other credential relays upstream as a bridge token.
- Dashboard at `/admin` (Svelte SPA embedded in the binary).
- Freebucks metering follows the wire `prices` map: charged once per session-hour
  at session start, refunded on early `DELETE`, refilled on a Pacific-midnight
  cadence.

## Quickstart

```sh
cp .env.example .env   # then edit: AUTH_TOKENS, ADMIN_TOKEN, ...
go build ./backend/...
go run ./backend/cmd/freebuff-proxy
```

Then:

- `GET http://localhost:3457/healthz` → 200
- `GET http://localhost:3457/v1/models` → live model list
- `http://localhost:3457/admin` → dashboard

Defaults that matter (`.env.example`): `SAFE_MODE=true` (anti-ban preset),
`COST_MODE=free`, 30 req/min and 1500 req/day Pacific limits.

Configuration persistence: the first boot imports the effective config
(process env wins over `.env` over defaults) into the dashboard DB
(`data/freebuff.db`, mode `0600`) as `config:` overlay rows plus a
`config:migrated_env_v1` marker — later boots are no-ops via the marker.
The DB is then the persisted home the dashboard saves write to, secrets
included (`AUTH_TOKENS`, `ADMIN_TOKEN`, `API_KEYS`, `WEBHOOK_URL` rows);
keep its `0600` mode on copies/backups. Explicit process env still wins at
runtime, so a migrated row never overrides the environment.

## Layout

- `backend/` — gateway source.
- `frontend/` — dashboard SPA source.
- `scripts/` — upstream sync / drift tooling.
- `docs/` — agent workflow notes.

## Contributing

`freebuff-proxy` follows platform-standard paths, and it **finds its configuration automatically** — you never need to `cd` into a specific folder for the `.env` to resolve.

| | Linux | macOS | Windows |
|---|---|---|---|
| **Binary** | `~/.local/bin/freebuff-proxy` | `/usr/local/bin/freebuff-proxy` | `%LOCALAPPDATA%\Programs\freebuff-proxy\freebuff-proxy.exe` |
| **Config** (`.env`, mode `0600`) | `~/.config/freebuff-proxy/.env` | `~/Library/Application Support/freebuff-proxy/.env` | `%APPDATA%\freebuff-proxy\.env` |
| **Template** (`.env.example`) | `~/.local/share/freebuff-proxy/.env.example` | `/usr/local/share/freebuff-proxy/.env.example` | `%LOCALAPPDATA%\Programs\freebuff-proxy\.env.example` |

- The **`.env`** file holds your secrets (`AUTH_TOKENS`, `ADMIN_TOKEN`, …). It lives **only** in the platform config directory above (the installer creates it there, `chmod 600` on Linux/macOS) and is resolved automatically by the runtime. As a legacy convenience for power users and dev clones, a `./.env` in the working directory still wins when present.
- The **`.env.example`** is a **template** — a secrets-free starter shipped next to the install root. It seeds the real `.env` during setup; it is never read as live config.
- **Overrides:** `--prefix <dir>` / `--dir <dir>` (bash) and `-Dir <dir>` (PowerShell) relocate the install root; `--env-file <path>` (bash) and `-EnvFile <path>` (PowerShell) point `.env` at a specific file. `--dir <dir>` (kept for backward compatibility) and a dev-clone checkout preserve the legacy "`.env` in the current directory" behavior — the platform directories above are the default.

**One-command installer (Linux/macOS):**

```bash
curl -sSL https://raw.githubusercontent.com/trefeon/freebuff-proxy/main/scripts/install-freebuff-proxy.sh | bash
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/trefeon/freebuff-proxy/main/scripts/install-freebuff-proxy.ps1 | iex
```

The bash installer prompts for an install method (easy, manual binary, Docker Compose, bridge mode); both installers mint/read your token and write `.env` in your platform config directory.

**Alternatively**, run with Docker Compose (a dev clone — the installers place `.env` for you):

```bash
cp .env.example .env   # dev clone: seed the template next to the compose file, then set AUTH_TOKENS
git fetch --tags 2>/dev/null || true
VERSION=$(git describe --tags 2>/dev/null || echo dev) docker compose up -d --build
```

**Or** download a release binary from [Releases](https://github.com/trefeon/freebuff-proxy/releases) (Linux/macOS/Windows × amd64/arm64), unzip it, right-click the extracted folder → **Open in Terminal**, and run `./start-proxy.sh` (Windows: `.\start-proxy.cmd`; the `.cmd` wrappers bypass the PowerShell execution policy). `start-proxy.*` resolves `.env` from your platform config directory, so it works no matter which directory you launch it from. The bundled scripts also include a headless token generator (`gen-token.sh` / `gen-token.cmd`).

### 2. Obtain an Auth Token

Generate one headlessly (opens a browser OAuth login). Run with no flags for an interactive menu; the recommended default (Enter) appends the token to `.env`, auto-creating it from `.env.example` if missing:

**Windows (PowerShell / CMD):**

```powershell
.\scripts\gen-token.cmd            # menu; Enter = append to .env (auto-create)
```

**Linux / macOS (bash):**

```bash
./scripts/gen-token.sh             # menu; Enter = append to .env (auto-create)
```

`gen-token.*` also supports explicit modes that skip the menu: `--clipboard` / `-ToClipboard`, `--save` / `-Save` (store in the CLI credentials file), `--append` / `-Append` (add to `.env` `AUTH_TOKENS`), and `--env <path>` / `-EnvFile <path>`.

Alternatively, log in with the official CLI (`npm i -g freebuff && freebuff`): the proxy auto-discovers the token from its credentials file on startup.

### 3. Configure

The installers already wrote `.env` for you (in your platform config directory — see [Where the files are installed](#where-the-files-are-installed)). For a manual or dev-clone run, seed the template and set your token:

```bash
# defaults to the platform config dir; pass --env-file <path> to target a specific file
cp .env.example .env
# AUTH_TOKENS=cb_xxx        ← paste your token (comma-separate for pooling)
# SAFE_MODE=true            ← default (set false to disable)
```

Leave `AUTH_TOKENS=` empty for **bridge mode** (clients bring their own tokens). Set `AUTH_TOKENS` for **pooled mode** — and because bridge relay is **on by default** (`BRIDGE_ENABLED`), a token pool also accepts clients who bring their own tokens (**hybrid mode**); set `BRIDGE_ENABLED=0` for pooled-only. Not sure which to pick? One user with a few accounts → pooled/hybrid; a shared router serving many users → bridge mode. See [Key Concepts](#key-concepts). `config.example.json` shows the common keys in JSON form, loaded with `-config`; the [Configuration Reference](#configuration-reference) below documents every key. Its `cb_xxx`/`cb_yyy` auth placeholders are deliberately rejected by validation — edit the file with real token values before passing `-config`.

### 4. Run & Verify

```bash
./freebuff-proxy            # or: docker compose up -d
```

The binary reads `.env` from your platform config directory automatically — run it from anywhere.

Check health and run diagnostics:

```bash
curl http://127.0.0.1:3457/healthz
./freebuff-proxy -doctor        # config, port, DNS/TLS, registry, plus zero-cost per-token validity probes
./freebuff-proxy -test-token    # zero-cost probe on the first token (no session claimed); prints live quota, exit 0/1
```

---

## Command-Line Interface

Daily management lives in the dashboard (`/admin`; see [Admin Dashboard](#admin-dashboard) and the [Dashboard Guide](docs/dashboard.md)). The CLI stays fully working as the headless and bootstrap path: no flag was removed or renamed, and every exit code still scripts the same way. `-help` prints the same Serve / Advanced grouping shown here.

| Flag | Dashboard twin | Description |
|---|---|---|
| *(none)* | Overview, Tokens, Settings, Logs, Setup | Run the proxy; manage it from `/admin` |
| `-config <path>` | Settings page, raw `.env` editor, `POST /admin/reload` | Load an optional JSON config file (keys mirror env names) |
| `-v` | Logs viewer, Settings log level | Verbose (debug) logging |
| `-version` | Overview status line | Print version and exit |
| `-doctor` | `POST /admin/diag` (needs a running server) | Run configuration and environment diagnostics: config, port, DNS/TLS reachability, model registry, plus a zero-cost validity probe per token |
| `-test-token` | Tokens page per-token Test, `POST /admin/tokens/test-all` | Probe the first configured token with a zero-cost upstream GET probe (no session claimed); prints `token OK` and live quota, exits `0`, or exits `1` (for installers/scripts) |
| `-validate-tokens[=tok1,tok2]` | `POST /admin/tokens/test-all` (needs a running server) | Validate every configured token with non-mutating upstream probes, print a health report, and exit `0` (healthy) / `1` (banned, invalid, or disposable mailbox) / `2` (config error); a comma-separated list overrides `AUTH_TOKENS` |
| `-refresh-token N` | Tokens page login wizard (adds a pool token; it does not re-auth slot N in place) | Re-authenticate token #N in `.env` via the headless login flow and exit. Interactive: prints a login URL and polls. With `-yes` and `GITHUB_USER` / `GITHUB_PASSWORD` / `GITHUB_TOTP` set: protocol login |
| `-setup` | Setup page copy blocks (no file writes) | Interactive client setup (detects installed clients) |
| `-yes` | None (headless modifier for `-setup` and `-refresh-token`) | Auto-confirm prompts |
| `-update` | Overview update badge (release link plus restart; the dashboard never swaps the binary) | Self-update from the latest GitHub release (SHA-256 verified against `checksums.txt`) |
| `-install-service` | None (a browser tab cannot register OS services) | Register the current binary as a background service and start it: Task Scheduler on Windows (per-user, no admin), systemd `--user` unit on Linux, launchd LaunchAgent on macOS. Resolves `.env` from your platform config directory (a `./.env` in the working directory still wins), and auto-starts on logon/boot |
| `-uninstall-service` | None (a browser tab cannot remove OS services) | Stop and unregister the background service (idempotent) |
| `-service-status` | None (headless check for scripts) | Check whether the service is registered and running; exits `0` when registered, `1` when not (scriptable) |

---

## Configuration Reference

All keys can be set via environment variables or the JSON config file passed to `-config` (`AUTO_DISCOVER_TOKEN` is environment-only); a `.env` file (resolved automatically from your platform config directory, or `./.env` in the working directory when present) is also read, and for the keys it covers it behaves like the environment. Precedence, lowest to highest: **built-in defaults < JSON `-config` < `.env` < environment**. List values (`AUTH_TOKENS`, `API_KEYS`, `MODELS_ALLOW`) are comma-separated in env and arrays in JSON (`MODELS_ALLOW` also accepts a plain comma-separated JSON string).

**Where tokens and state live.** Token add/remove/swap/move mutations from `/admin` are applied live and written to the SQLite token database when it is active (`AUTH_TOKEN_DB_PATH`, default `data/auth_tokens.db`) and otherwise to `.env`. On startup the pool is reconciled to the database's authoritative token list, so tokens added via the dashboard survive container recreates and restarts **without any write to the `-config` file** — a bind-mounted `config.json` stays untouched (no resource-busy / EBUSY on the mounted file). The database also persists each token's operational state — administrative locks, terminal quarantines (banned / country-blocked / invalid accounts), cooldown/ban windows, and the spend/usage ledgers — so a locked or dead account stays locked/quarantined and quota accounting survives restarts (Phases 1-3 of the SQLite state-persistence program). With `SESSION_PERSIST=true`, active sessions and agent runs are stored in the same database too (Phase 4), so a restart resumes unexpired sessions without writing any JSON file.

| Environment Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `127.0.0.1:3457` | Host and port to bind (loopback; containers set `:3457`) |
| `UPSTREAM_BASE_URL` | `https://codebuff.com` | Upstream API endpoint (normalized to `www.codebuff.com`) |
| `AUTH_TOKENS` | `""` | Comma-separated upstream tokens (empty = bridge mode; set = pooled or hybrid) |
| `BRIDGE_ENABLED` | `true` | With `AUTH_TOKENS` set, accept bridge-mode clients (their own token relayed) alongside the pool — **hybrid mode**. `0` = locked-down pooled-only instance (the pre-hybrid behavior) |
| `BRIDGE_IDLE_EVICT` | `72h` | How long a bridge entry may sit unused before its runs are FINISHed and it is evicted from the cache (sliding TTL; zero or invalid → 72h) |
| `BRIDGE_DAILY_LIMIT` | `0` | Global daily chat cap across **all** bridge-mode entries (`0` = unlimited) |
| `MODELS_HIDE_UNAVAILABLE` | `false` | `/v1/models` prunes models marked unavailable (region/tier demotion, quota exhaustion) so picker clients cannot select them; off by default so a stale signal never hides a working model |
| `MODEL_UNAVAILABLE_CACHE_TTL` | `1h` | How long a `model_unavailable` admission refusal is remembered per model (off-window models short-circuit to the fallback within the TTL) |
| `MODELS_ALLOW` | `""` | Comma-separated model allowlist (JSON array or string). When set, only these model ids are served — `/v1/models` lists only them, and `chat/messages/responses` requests whose resolved model (after alias resolution) is not listed are rejected with `404 model_not_found` (`"model not allowed by MODELS_ALLOW"`). Empty = all models allowed |
| `AUTO_DISCOVER_TOKEN` | `true` | When `AUTH_TOKENS` is empty, read credentials from the official CLI login files (`false` disables) |
| `API_KEYS` | `""` | Comma-separated client keys required for `/v1/*` (empty = open; ignored in bridge mode). **In hybrid mode `API_KEYS` is the discriminator**: a credential matching an entry uses the pool, any other is relayed as a bridge token |
| `ADMIN_TOKEN` | `123456` | Login password for the [admin dashboard](#admin-dashboard) and the bearer token `POST /admin/reload` requires. Defaults to the factory password `123456` (a startup warning is logged and the dashboard shows a change banner until you rotate it): **change it before exposing the port** — while the factory default is active, sensitive dashboard routes (config editor, logs, token management, reload) additionally require a loopback client |
| `ROTATION_INTERVAL` | `6h` | Agent-run rotation interval |
| `REQUEST_TIMEOUT` | `15m` | Upstream request timeout |
| `SESSION_CALL_TIMEOUT` | `30s` | Session call timeout |
| `REGISTRY_REFRESH` | `6h` | Model catalog refresh interval |
| `COST_MODE` | `free` | `free` (default) or unset; any other value fails startup validation |
| `ACTING_USER_ID` | `""` | Optional FreeBuff account id; sent on every chat call as `x-freebuff-acting-user-id`. BAN RISK: only the token's own account id is safe (the CLI derives it from `GET /api/v1/me`; the server honors the header only for the FreeBuff Web service account) — any other value impersonates another user. Pre-rename name `USER_ID` still works. Empty = header omitted |
| `TLS_FINGERPRINT` | `""` | `""` (plain Go/Bun baseline, CLI-faithful), or `auto`, `chrome120`, `chrome126`, `safari17`, `safari18`, `firefox120`, `firefox128`, `edge126`, `random` for browser JA3 evasion |
| `DASHBOARD_ENABLED` | `true` | Serve the embedded admin dashboard at `/admin` (`false` disables all `/admin` routes with 404) |
| `DEVTOOLS_ENABLED` | `false` | Show the Dev Tools page (batch chat, session spawner) in the admin dashboard. Default **off** — it is a manual testing surface that hammers `/v1/*` and is not for public dashboards. |
| `LOG_FILE` | `""` | Append log lines to a file (e.g. `./logs/proxy.log`) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`, `trace` (trace = wire-level bodies) |
| `LOG_FORMAT` | `text` | `text` (key=value, colored) or `json` (one JSON object per line) |
| `LOG_ACCESS` | `true` | Log one `access` line per HTTP request (`false` disables; `/healthz`, `/metrics`, OPTIONS are rate-limited to 1/min regardless) |
| `LOG_RING_SIZE` | `500` | In-memory log ring for `/admin/logs` (50–5000) |
| `MAX_MESSAGES_PER_DAY` | `0` | Per-token daily cap on successful chats (`0` = unlimited, default; the upstream `429` lock is the real enforcement) |
| `MAX_SPEND_PER_DAY` | `0` | Advisory per-token Pacific-day spend ceiling in ledger units (`0` = unlimited). Never enforced — surfacing only, on `/healthz` |
| `IDLE_ROTATION_TIMEOUT` | `0` | Finish runs after this idle period (`0` = disabled; `SAFE_MODE` sets 30m when unset) |
| `SESSION_IDLE_END` | `0` | End upstream sessions after this idle period, releasing the token's daily admission slot while the proxy sits unused; the next request re-admits and consumes a fresh slot (`0` = disabled, opt-in) |
| `SCARCE_SESSION_MODELS` | `openai/gpt-5.6-luna` | 1-session/day models to keep alive for their full session (never idle-evict or DELETE on shutdown while active) |
| `QUOTA_FALLBACK_MODELS` | `flash→mimo, glm→flash, luna→flash` | Map model → fallback when its session quota is exhausted/unentitled. Defaults: `deepseek/deepseek-v4-flash=mimo/mimo-v2.5`, `z-ai/glm-5.2=deepseek/deepseek-v4-flash`, `openai/gpt-5.6-luna=deepseek/deepseek-v4-flash` (luna degrades the scarce premium session locally instead of hammering quota 429s; #203) |
| `SAFE_MODE` | `true` | Apply anti-ban presets (see below; set `false` to disable) |
| `REQUEST_JITTER` | `0s` | Random delay range `[0, REQUEST_JITTER)` before upstream calls (`SAFE_MODE` sets 200ms when unset; set `0` for instant TTFB) |
| `CLI_VERSION` | `0.10.7` | Informational only: parsed and shown on the admin dashboard (Configuration Studio). No wire impact — the chat UA is pinned to `ai-sdk/openai-compatible/1.0.0/codebuff`, the ads UA to `Freebuff-CLI/1.0.0`, and session/auth endpoints default to `Bun/1.3.14` |
| `MODEL_ALIASES` | `""` | Map aliases to real model IDs, e.g. `gpt-4o:openai/gpt-5.6-luna`. There are no built-in aliases (the old `deepseek-chat`/`gpt-4o`/`claude-3-5-sonnet` map was removed when `deepseek-v4-pro` was paused); clients must map aliases explicitly. |
| `TRANSIENT_RETRIES` | `1` | Max additional attempts after a transient transport failure; `0` disables |
| `SESSION_PERSIST` | `false` | Persist session state AND active agent runs so a restart resumes them instead of re-creating (new daily slot / re-START). When the SQLite token DB is active, state is stored in it (no JSON file); otherwise in `SESSION_STATE_FILE` |
| `SESSION_STATE_FILE` | `.freebuff-session-state.json` | Path of the session state file (used when `SESSION_PERSIST=true`; token-keyed, `0600`) |
| `SESSION_RE_ADMIT_LEAD` | `60s` | Re-admit a session pre-emptively when less than this remains: the request rides the old session while the refresh runs in the background |
| `SESSION_PROBE_CACHE_TTL` | `15s` | Reuse the last successful session state (skip redundant session poll GETs) within this window |
| `SESSION_CREATE_MAX_PARALLEL_GLOBAL` | `128` | Cap on concurrent in-flight session admissions (wait-or-503) |
| `SESSION_CREATE_MAX_PARALLEL_PER_MODEL` | `32` | Per-model cap on concurrent in-flight session admissions |
| `RUN_FINISH_QUEUE_SIZE` | `64` | Bounded deferred-FINISH worker queue for rotated/drained runs |
| `RUN_FINISH_INLINE_TIMEOUT` | `250ms` | Synchronous inline FINISH fallback bound when the finish queue is full |
| `RUNS_DRAIN_QUEUE_CAP` | `64` | Draining-runs list cap; older entries are force-dropped (FINISH is best-effort) |
| `RUNS_DRAIN_TTL` | `10m` | Draining-runs TTL eviction window |
| `HTTP2_UPSTREAM` | `true` | Negotiate HTTP/2 with the upstream so the ALPN matches real browsers; `false` forces HTTP/1.1 |
| `FALLBACK_MODEL` | `""` | Map `model1=fallback1,model2=fallback2` to re-route a request to the fallback model when its queue wait passes `FALLBACK_AFTER_MS` (queue-wait only — never on 429 quota exhaustion). When unset, the built-in default applies: `openai/gpt-5.6-luna` → `deepseek/deepseek-v4-flash` |
| `FALLBACK_AFTER_MS` | `10000` | Queue-wait threshold (ms) before falling back to `FALLBACK_MODEL` |
| `CORS_ALLOWED_ORIGIN` | `*` | `Access-Control-Allow-Origin` for `/v1/*` responses |
| `ADOPT_CLI_SESSION` | `false` | Adopt the upstream CLI's active session instead of creating a new one |
| `WAITING_ROOM_CHAIN` | `false` | After an upstream 428 `waiting_room_required`, fire the reference ad-chain (POST `/api/v1/ads` per provider) + GET `/api/v1/freebuff/streak` before the next session create — on both the pooled and bridge paths (issue #94(b), gated stub — best-effort, never blocks the request; not a queue-across-tokens mechanism) |
| `WEBHOOK_URL` | `""` | Best-effort alert POSTs for three events: `pool_exhausted` (all tokens rate-limited), `token_banned` (from chat **or** admission — including bridge tokens, sent with `token_index 0`), and `agent_model_mismatch_escalation` (3+ allowlist refusals in 60s on one token — issue #140; empty = disabled; at most one POST per event type per 5m, never blocks the request path) |
| `RATE_LIMIT_PER_IP` | `0` | Requests/second allowed per client IP (`0` = disabled; e.g. `20`) |
| `RATE_LIMIT_BURST` | `0` | Burst request capacity per client IP (`0` = default `2 * RATE_LIMIT_PER_IP`) |
| `BRIDGE_RATE_LIMIT_PER_TOKEN` | `0` | Per-client-token rate limit in req/s in bridge mode (`0` = unlimited). Independent of the per-IP limiter; each bridge token is throttled individually |
| `BRIDGE_DAILY_LIMIT` | `0` | Global daily chat cap across ALL bridge-mode tokens (`0` = unlimited). Checked before per-entry caps |
| `BRIDGE_CIRCUIT_BREAKER_FAILURES` | `0` | Number of transient upstream 5xx/network failures within the sliding window that trips the circuit breaker (`0` = disabled). Only genuine transient outages (5xx/network) trip the breaker; classified errors (auth/rate-limit/ban/country/ip_capped) never trip it |
| `BRIDGE_CIRCUIT_BREAKER_WINDOW` | `30s` | Sliding window in which the failure count is measured. The breaker opens when `BRIDGE_CIRCUIT_BREAKER_FAILURES` failures fall within this window, and stays open for `BRIDGE_CIRCUIT_BREAKER_COOLDOWN` |
| `BRIDGE_CIRCUIT_BREAKER_COOLDOWN` | `10s` | How long the circuit breaker stays open after tripping. Disabled when `BRIDGE_CIRCUIT_BREAKER_FAILURES` is 0 |
| `MAX_SPEND_PER_DAY` | `0` | Advisory per-token Pacific-day spend ceiling in ledger units (`0` = unlimited). Never blocks — informational only, surfaced on `/healthz` |
| `AUTO_ROTATE_ON_EXHAUSTION` | `false` | Deprioritise exhausted tokens in Acquire order — routes requests to healthier backup tokens when available (reactive, never proactive; exhausted token remains last-resort eligible so a single-token pool never deadlocks) |
| `EXHAUSTION_WARNING_THRESHOLD` | `10m` | Warn when a token's remaining quota at current usage rate would exhaust within this window (`0` disables predictive warnings) |
| `HEALTH_SCORE_ENABLED` | `true` | Enable composite 0–100 health score computation and `/healthz` / dashboard exposure |
| `TOKEN_HEALTH_PROBES` | `false` | Enable background zero-cost GET `/api/v1/freebuff/session` probes for each pooled token at `TOKEN_PROBE_INTERVAL` cadence (no session claimed, no daily slot burned) |
| `TOKEN_PROBE_INTERVAL` | `30m` | Interval between background health probes per token (only effective when `TOKEN_HEALTH_PROBES=true`) |
| `TOKEN_ROTATION` | `drain` | Token selection strategy: `drain` (exhaust a token's session before rotating), `round_robin`, `least_used`, or `random` |

The three request-translation knobs (`COMPRESS_PROMPT`, `CACHE_CONTROL_INJECTION`, `REASONING_IN_CONTENT`) are environment-only (never read from `-config` JSON or `.env` in a native install) and are documented in `.env.example` / `.env.full-example`.

When `SESSION_PERSIST=true`, the state file stores a SHA-256 hash of each
active token plus its session metadata (instance id, expiry, tier/country)
**and its active agent runs** (run id, agent, trace session id), including
bridge-mode client tokens, since every session manager shares the one store.
A restart adopts the persisted session and runs without re-creating them.
The raw token is never written, and the file is created with mode
`0600`. Leave `SESSION_PERSIST` unset (or `false`) to opt out entirely.

### Safe Mode & Zero-Spam Quota Handling

`SAFE_MODE=true` is the **default** for all setups (set `SAFE_MODE=false` to
opt out). It enables essential anti-ban protections and presets:

- **TLS: CLI-faithful** — plain Go/Bun baseline, no browser JA3 spoofing. What
  CLI serves (`Bun/1.3.14` for session, `ai-sdk` for chat) we serve. Set
  `TLS_FINGERPRINT=auto` (or `chrome126`/`safari18`...) only for browser JA3
  evasion on datacenter IPs (e.g. SG).
- **Proxy Header Sanitization**: Strips 25 proxy-identifying headers (`X-Forwarded-For`, `Via`, `CF-Connecting-IP`, etc.).
- **Request Jitter**: Injects randomized 0-200ms delay jitter to break robotic, machine-like cadence (set `REQUEST_JITTER=0` for instant, or `REQUEST_JITTER=100ms` for minimal jitter).
- **Idle Rotation**: Finishes runs after 30 minutes of inactivity.
- **Daily Cap** (optional): `MAX_MESSAGES_PER_DAY` defaults to `0` (unlimited). The upstream `429` lock is the real enforcement; see below.

### Key Hygiene & Ban Avoidance

- **Use one key until it is rate-limited.** The pool prefers the token that already holds a
  live session (hot-session-first) and only fails over when a token hits its quota or errors.
  It does **not** aggressively round-robin healthy keys. Letting one account run until its
  daily quota is natural usage; rotating many healthy keys in rapid succession looks like
  account farming and can trigger upstream ban detection.
- **Do not route through a VPN.** FreeBuff resolves access tier via Cloudflare TCP-layer GeoIP
  (not HTTP headers — `X-Forwarded-For`/`CF-Connecting-IP` spoofing is impossible at L4).
  VPN/datacenter IPs are detected via MaxMind/Spur Intelligence ASN databases
  (`ipPrivacySignals: ["vpn"]`) and placed in a restricted cohort with a **$0.50/day spend ceiling**.
  Commercial VPNs (NordVPN, ExpressVPN), datacenter VPS (AWS, DO, Hetzner), and Tor all trigger
  this detection. The proxy's stealth settings mask TLS fingerprints and proxy headers; they do
  **not** change your public IP. Use a normal residential connection.
- **Do not hammer many tokens at once from the same public IP.** Upstream caps how many
  distinct users can hold an active free session on one egress IP (`ip_capped`, 429), and
  accounts created from the same signup network (≥8 per /24) or mailbox (≥3) are permanently
  capped at lower trust levels. Documented ban cohorts include single-IP rings and same-day
  account mints. The pool already drains keys one at a time; do not add aggressive rotation
  on top.
- **Only request models your account's tier and region actually offers.** Out-of-tier picks
  are refused or downgraded (`model_unavailable`, `session_model_mismatch`).
  The requested model id is correlated with the egress IP's resolved geo, so a premium model request
  from a VPN/hosting IP is a suspicious, ToS-prohibited combination. On limited-tier accounts,
  `mimo/mimo-v2.5` is the supported active model (`deepseek/deepseek-v4-flash` is restricted on limited tier).
- **Know the difference between a quota and a ban.** `429` (quota, resets at
  Pacific midnight) is the normal end-of-day signal; the proxy locks the token locally and
  answers in `<1ms`, and routers fail over. `503` with `waiting_room` is the queued-waiting-room
  signal (also transient). Only `403` with `banned` / `country_blocked`
  means the account itself is gone: stop using it and move to a fresh established account.
- **For ~24h of continuous coding, budget 4-5 keys.** Each FreeBuff account has a daily session
  quota (premium 5/day, limited 3/day, trust-level ladder up to 7) and the CLI holds **one session
  at a time** (concurrent sessions are a Desktop multi-tab feature, not CLI).
  One key ≈ one day of moderate use. Configure `AUTH_TOKENS` with multiple tokens to pool session
  headroom across tokens and let the proxy drain them one at a time.
- **Register accounts with real email addresses** (e.g. Gmail). Disposable / temp-mail
  registrations are a documented ban cohort: 6,699 of 7,129 accounts on flagged domains were
  already banned when the blocklist was compiled. Accounts sharing one mailbox are capped at
  lower trust levels.

**Why `MAX_MESSAGES_PER_DAY` Defaults to `0` (Unlimited):**

- Unlimited is the **default**: no local cap throttles your free-tier allowance.
  The proxy never spams upstream: when an account reaches its daily quota, the
  upstream `429` lock kicks in (below), so an unlimited local cap is safe.
- **Zero-Spam Guarantee**: When an account reaches its daily quota or upstream capacity limit, the upstream returns a `429` with a Pacific midnight reset timestamp (`resetAt: 07:00:00Z`).
- The proxy parses this timestamp and **locks the token locally in memory**.
- Any subsequent request for that token returns `429` locally in `<1ms` without sending any network traffic upstream.
- Upstream routers (e.g. 9router) receive standard `429` + `Retry-After` headers and automatically rotate to your next available account without failing user prompts.

### HTTP Endpoints

| Endpoint | Auth | Description |
|---|---|---|
| `POST /v1/chat/completions` | `API_KEYS` (when set) | OpenAI-compatible chat, streaming and non-streaming |
| `GET /v1/models` | `API_KEYS` (when set) | Model catalog from the registry (fallback at boot + live refresh). Each row carries `available`/`status`/`current_access_tier`: models outside the limited-tier allowlist (`mimo-v2.5`) are marked `available:false, status:"region_limited"` when the token's egress region demotes it to the limited tier; `MODELS_HIDE_UNAVAILABLE=true` prunes them from the list; `MODELS_ALLOW` prunes every id not in the allowlist |
| `GET /healthz` | none | Liveness + pool snapshot — JSON: `status`, `uptime_seconds`, `models`, per-token snapshot (incl. per-model `quota` map when the last admission carried it), `bridge_tokens`. It does **not** probe upstream reachability, so the container stays healthy during an upstream outage |
| `GET /metrics` | none | Prometheus text format: uptime, model count, per-token 24h messages / requests / active runs / cooldown, per-model quota (`freebuff_proxy_quota_recent` / `freebuff_proxy_quota_limit`) |
| `POST /admin/reload` | `ADMIN_TOKEN` (when set) | Hot-reload configuration from disk without restart |
| `GET /admin` | session cookie (login via `ADMIN_TOKEN`) | Admin dashboard: overview, tokens, config, logs, metrics (see [Admin Dashboard](#admin-dashboard)) |
| `GET/POST /admin/login` | none | Dashboard login: constant-time `ADMIN_TOKEN` check, per-IP rate limit, `HttpOnly` + `SameSite=Strict` session cookie |
| `POST /admin/config` | session cookie | Validate and persist the `.env` file, then hot-reload the config (rolls back on rejection) |
| `POST /admin/smoke` | session cookie (loopback when `ADMIN_TOKEN` unset) | One real chat through the pool: reports model, token, latency, and a content preview (bridge mode needs a client token in the payload) |
| `POST /admin/diag` | session cookie (loopback when `ADMIN_TOKEN` unset) | Dashboard diagnostics (same checks as `-doctor`): config state, DNS + TCP reachability, registry count; zero-cost per-token validity probes run on every request |
| `POST /admin/mode` | session cookie (loopback when `ADMIN_TOKEN` unset) | Runtime mode switch: `{"mode":"hybrid"}` (pooled + bridge), `{"mode":"pooled"}` (bridge relay disabled, `BRIDGE_ENABLED=0`), `{"mode":"bridge"}` (empties `AUTH_TOKENS`). All changes persisted to `.env` |
| `POST /admin/tokens/...` | session cookie (loopback when `ADMIN_TOKEN` unset) | Runtime pool management: `/add`, `/remove` (last token), `/test-all`, and per-token `/test`, `/unlock`, `/finish`, persisted to `.env` |

## Admin Dashboard

The proxy ships with a built-in modern SPA web dashboard: single binary, no external dependencies, and zero runtime Node.js requirement (the Svelte 5 production build is compiled and embedded into the binary at build time). Open `http://127.0.0.1:3457/admin` (or your `LISTEN_ADDR`).

- **Login**: enter your `ADMIN_TOKEN` on the login page. It is the same value as the bearer token for `POST /admin/reload`. It defaults to the factory password `123456` — until you change it, sensitive routes (config editor, logs, token management, reload) require a loopback client even when logged in (a startup warning and a persistent dashboard banner prompt the rotation; `/admin/api/change-password` works from anywhere since it requires the current password). Failed logins are rate-limited per IP (5 fails → 1 minute lockout), and the session cookie is `HttpOnly` + `SameSite=Strict` (+ `Secure` when TLS or `X-Forwarded-Proto: https` is present).
- **Overview**: live relay state (pooled/bridge/**hybrid** mode, model count, uptime, safe mode), 6 KPI counters, and client-integration base URL with copy button.
- **Tokens**: pooled credentials as `Account #1, #2, …` (1-based pool order) with live cooldown countdowns, at-risk account cards, per-account **Move Up/Down** reorder, **Clear**/**Lock**/**Unlock**/**Remove**, expandable session drawer (**Drop Session**, model-lock pinning), rotation-policy radios, 429 auto-failover switch, **Add Token to Pool**, and the headless OAuth login wizard. Add/remove/swap/move persist to the SQLite token database when active, else `.env`; the `-config` JSON file is never rewritten (see [Configuration Reference](#configuration-reference)).
- **Quota Tracker**: per-account premium-pool bars and session-quota tables with reset countdowns (day granularity past 24h).
- **Models**: live served-model catalog with upstream agent mappings and 1-click model ID copy.
- **Logs**: console (live `/v1` traffic, 1s auto-refresh) plus a table view of the newest 200 ring entries with level select, message search, and pagination.
- **Metrics**: tabular stat cards with SVG sparklines and direct link to the raw `/metrics` Prometheus feed.
- **Traces**: recent chat requests and their routing outcome (token, model, status, duration, error class), the observability view for ban-avoidance debugging.
- **Settings**: intent-driven cards (Security password change, General, Pool, Upstream) that apply live on save, plus the collapsible raw `.env` editor with server-side validation and rollback.
- **Setup**: universal Base URL, client API key field with **Generate**/**Reset**, per-model copy buttons, and copy-paste snippets for major AI coding tools (OpenCode, Continue/Cline, aider, 9router, cURL).

See [Dashboard Guide](docs/dashboard.md) for access, Docker caveats, and hardening.

#### Dashboard development

Develop the SPA with the Vite dev server: run `task frontend:dev` (Vite on `http://127.0.0.1:5173/admin/`)
alongside a local gateway (`task dev`, `127.0.0.1:3457`). The dev server proxies `/admin/*` to that
gateway and redirects a `GET /admin/login` to the SPA hash route `/admin/#login`. For the embedded
production build (single binary), run `task frontend:build`.

---

## Deployment

- **Docker**: `docker-compose.yml` + `Dockerfile`, runs as an unprivileged user, healthchecked on `/healthz`, `LISTEN_ADDR=:3457` inside the container.
- **Systemd**: `scripts/freebuff-proxy.service` (Linux).
- **macOS launchd**: `scripts/com.freebuff-proxy.plist` (macOS).
- **Docker + 9router helper**: `scripts/setup-proxy-docker.sh`.

## Guides

- [Getting Started](docs/getting-started.md): 5-minute setup walkthrough
- [Client Integration](docs/client-integration.md): OpenCode, pi, 9router, LiteLLM, OpenAI SDKs
- [9router Integration](docs/9router-integration.md): router dashboard setup in bridge mode
- [Dashboard Guide](docs/dashboard.md): the admin web UI: access, pages, Docker caveats, hardening
- [Manual Testing](docs/testing.md): verify the proxy on Linux or Windows by hand, step by step
- [Bridge Mode](docs/bridge-mode.md): bridge-mode architecture, invariants, security notes, hardening checklist
- [Ban-Avoidance & Signature Research](docs/ban-avoidance.md): upstream detection landscape, countermeasures, risk engine, operator hygiene rules
- [Upstream Drift Tracking](docs/upstream-drift-tracking.md): pinned registry snapshots, drift detection/sync scripts, CI integration, response playbook
- [User Lifecycle](docs/user-lifecycle.md): install → first run → tokens → use → monitor → edit → rotate → quota → update
- [Version Stability & Ban Findings](docs/getting-started.md#access-tiers--workarounds): **read before upgrading** — why v0.11.2 bridge is the proven-stable deployment

## Documentation Set

- [Architecture](ARCHITECTURE.md): system components, request flows, operating modes, invariants
- [Specification](SPECIFICATION.md): full API surface, request/response schemas, error contract
- [Roadmap & Future Work](ROADMAP.md): feature checklist, known limitations, planned & deferred work

---

## Harness Compatibility

| Compatibility | Details |
|---|---|
| **Coding-agent harnesses** | **11/12 first-party surfaces supported**: opencode, codex, cline, roo-code, goose, aider, continue, qwen-code, pi, oh-my-pi, kilocode. **gemini-cli is not supported** (native Gemini only — point it at Vertex AI / AI Studio, or use opencode-go). Full per-harness matrix, config snippets, and known limits: [docs/harness-compatibility.md](docs/harness-compatibility.md); ready-to-edit templates in [`examples/harnesses/`](examples/harnesses/). |

---

## Contributing & Security

- [Contributing](CONTRIBUTING.md): filing issues, opening PRs, what to expect
- [Security](.github/SECURITY.md): supported versions and how to report a vulnerability

### Upstream Drift Tracking & Sync

The offline model registry pins five upstream constant files in `backend/internal/registry/testdata/upstream/`.

To automatically fetch upstream changes from `CodebuffAI/freebuff`, update the pinned definitions, verify hash parity, and run the test suite:

```bash
# Linux / macOS / Git Bash
bash scripts/sync-upstream.sh

# Windows (PowerShell / CMD)
.\scripts\sync-upstream.cmd
```

To check drift without writing files, pass `--check` / `-CheckOnly`. To run the full test suite after syncing, pass `--test-all` / `-TestAll`.

To run only the read-only hash parity check:

```bash
bash scripts/check-upstream.sh
```

CI runs the same check weekly (`upstream-drift` workflow) and goes red on drift. A live registry refresh self-heals at runtime; `sync-upstream` keeps the offline fallback in lockstep.

## Contact & Support

- **Questions, bugs, feature requests**: [GitHub Issues](https://github.com/trefeon/freebuff-proxy/issues)
- **Security reports**: [SECURITY.md](.github/SECURITY.md)
- **Contributing**: [CONTRIBUTING.md](CONTRIBUTING.md)

## License

[MIT](LICENSE)
