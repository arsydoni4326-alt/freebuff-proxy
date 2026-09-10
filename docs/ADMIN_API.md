# Admin API — token maturity actions

> Maturity automation API surface for pool tokens. Authentication: session cookie
> (loopback when `ADMIN_TOKEN` unset) plus CSRF on POST.

## POST /admin/tokens/{id}/maturity/warn-reset

Clears one token's non-advance warning state (`warn-reset`). This is the dashboard
"Reset warning" lever.

- **What it changes:** only the warn loop — warning counter and day counters drop,
  and the daily touch re-arms.
- **What it does not touch:** `enabled`, `target`, `mode`, `touch_model`. It never
  locks, unlocks, or reconfigures the token.
- **Response:** `200` with a JSON result body on success.
- **Validation:** out-of-range token `id` returns `400`.

This endpoint is additive and idempotent when no warning is set.

## Related maturity endpoints

- `POST /admin/tokens/{id}/maturity` — enable/disable ticker-maturity automation and
  configure target, mode, touch_model. Enabling also applies the warming lock.
- `POST /admin/tokens/{id}/maturity/touch` — fire one manual maturity touch outside
  the daily slot (slot wait and 6h throttle bypassed; health gates, streak freshness,
  and todayUsed still apply).
