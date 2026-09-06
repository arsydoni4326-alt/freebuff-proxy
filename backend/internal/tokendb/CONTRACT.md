# Package Contract: `backend/internal/tokendb`

Task-local contract for agents modifying this package. Load before editing any file here.

## Purpose

Persistent SQLite-backed store for AUTH_TOKENS and, since Phase 1 of the SQLite
state-persistence program, the per-token operational state the pool must not
lose across restarts. The database is the authoritative token source: the pool
reads from it at startup and dashboard mutations (add/remove/mode switch)
persist through it instead of rewriting `.env`.

## Public API (stable surface)

- Tokens: `Open(path, log)`, `Close`, `List`, `Count`, `Add`, `Remove`,
  `RemoveLast`, `RemoveAll`, `MigrateFromEnv`, `Tokens` (sorted snapshot).
- State (opaque JSON blobs owned by the pool — this package never parses
  them): `SaveTokenState(token, blob)`, `LoadTokenStates() (map[string][]byte,
  error)` (JOINs `auth_tokens`, so a removed token's state is invisible),
  `ClearTokenState(token)`.
- Session state (Phase 4 of the state-persistence program, SESSION_PERSIST):
  `SaveSessionState(token, blob)` (nil blob deletes the row),
  `LoadSessionState(token)` (nil when absent — the token-hash key space is
  SEPARATE from auth_tokens, so no JOIN and a re-added token must resume its
  session), `LoadAllSessionStates()`. Consumed through the
  `session.StateBackend` adapter in `internal/cli/sessionbackend.go`.

## Allowed dependencies

None internal — a storage leaf (stdlib `database/sql` + `mattn/go-sqlite3`
only). Enforced by `archtest` (`internal/tokendb` has an empty allowlist).

## Critical invariants

- `auth_tokens.token` is UNIQUE; `Add` is `INSERT OR IGNORE`.
- `LoadTokenStates` only returns state whose token still exists in
  `auth_tokens` — a removed token's state row stays dormant and resurfaces if
  the same account is re-added (intentional: the account's terminal state is
  still true). `ClearTokenState` is the explicit operator override.
- The `state` column is an opaque BLOB: the pool owns the JSON schema, so the
  schema can evolve without a migration for every pool change. Corrupt blobs
  are ignored by the pool on restore, never fatal.
- All writes go through `d.mu` (`SetMaxOpenConns(1)` + WAL/busy-timeout DSN).

## Tests that protect it

`tokendb_test.go` (list/add/remove/duplicate/migrate/count/sorted + state
save/load/upsert/orphan-invisibility/clear).

## Safe modification patterns

- New per-token state: extend the pool's `tokenState` JSON, NOT the table
  shape. Only add columns when you need to index/query a field.
- Never import an internal package here (leaf).