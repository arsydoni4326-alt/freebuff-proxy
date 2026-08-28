package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"freebuff-proxy/internal/convert"
	"freebuff-proxy/internal/pool"
	"freebuff-proxy/internal/upstream"
)

// chatAttempt runs the retry-once recovery loop for one chat request: chat
// through the leased token; on session-invalid / run-invalid the lease is
// released, the cached session/run invalidated, and a fresh lease acquired
// once; on session_superseded the lease is released and the cached session
// invalidated (reason "superseded") but NEVER retried — it is terminal for
// this request (#159); on auth-reject / ban / rate-limit / ip-capped the
// token is cooled down (ip_capped bounded to its retryAfterMs — never the
// Pacific-midnight lock) and the error returned for writeError. The
// acquire/chat/invalidate/cooldown hooks are closures so the pooled
// (fixed-token) and bridge paths share the exact same recovery semantics.
// On success the returned body reader and final lease belong to the caller:
// close the body and release the lease via Pool.LeaseRelease when done.
func (s *Server) chatAttempt(
	ctx context.Context,
	model string,
	normalized []byte,
	st *chatTraceState,
	acquire func(context.Context, string) (*pool.Lease, error),
	chat func(context.Context, *pool.Lease, upstream.ChatOptions, []byte) (io.ReadCloser, error),
	invalidateSession func(*pool.Lease),
	invalidateSessionSuperseded func(*pool.Lease),
	invalidateRun func(*pool.Lease, string),
	cooldownAuth func(*pool.Lease),
	cooldownBan func(*pool.Lease, *upstream.BanError),
	cooldownRate func(*pool.Lease, *upstream.RateLimitError),
	cooldownIpCapped func(*pool.Lease, *upstream.IpCappedError),
	cooldownCountry func(*pool.Lease, *upstream.CountryBlockedError),
) (io.ReadCloser, *pool.Lease, error) {
	lease, err := acquire(ctx, model)
	if err != nil {
		return nil, nil, err
	}

	// The lease is the authoritative source for the model its session/run
	// are bound to: after a #100 fallback the acquire returned a lease for
	// the FALLBACK model while the caller still holds the requested model.
	// opts.Model, the body model and x-freebuff-model must all agree with
	// the lease (review P2 — previously the request went upstream labeled
	// with the requested model against the fallback session/run).
	effectiveModel := lease.Model
	if effectiveModel == "" {
		effectiveModel = model
	}
	if effectiveModel != model {
		if renormalized, nerr := convert.NormalizeRequest(normalized, effectiveModel); nerr == nil {
			normalized = renormalized
		}
	}

	opts := upstream.ChatOptions{
		Model:             effectiveModel,
		RunID:             lease.Run.RunID,
		SessionInstanceID: lease.SessionInstanceID,
		TraceSessionID:    lease.Run.TraceSessionID,
		// One client_id for the whole run: a fresh draw per call is the
		// free_mode_run_fanout shape (see injectEnvelope).
		ClientID: lease.Run.ClientID,
		// The run's root agent family selects the canonical system-prompt
		// opening: base3-free-* roots speak base3, others base2.
		AgentID: lease.Run.AgentID,
		// D1: the request's correlation id, threaded to the upstream
		// client so its do()/retry log lines share the server's req_id.
		RequestID: st.reqID,
		// Issue #113: stamp the run's 1-based per-chat step counter so
		// codebuff_metadata["llm_step_number"] matches the CLI (each chat
		// call is one agent step; run-agent-step.ts increments per step).
		// Incremented once per chatAttempt — the retry-once loop below
		// retries the SAME step.
		StepNumber: int(lease.Run.NextStepNumber()),
	}

	released := false
	release := func() {
		if !released {
			released = true
			if ctx.Err() != nil {
				// Issue #157: the downstream client is gone (context
				// canceled — 72 hits/5k logs as 60s harness timeouts and
				// Ctrl-C on long runs). Abandon the lease instead of a
				// plain release: the run is dropped from the active set
				// and FINISHed as "cancelled" through the bounded queue
				// (CLI DELETE-on-exit parity, issue #53) so upstream does
				// not keep an abandoned agent run alive until the 6h
				// rotation. Plain releasing here left the run active for
				// the full duration, then wasted it.
				s.pool.LeaseAbandon(lease)
				return
			}
			s.pool.LeaseRelease(lease)
		}
	}
	defer release()

	var up io.ReadCloser
	attempts := 0
	// failTime pins when the failed chat attempt returned; the measured
	// re-acquire wait below becomes the trace's backoff_ms.
	var failTime time.Time
	// transientErr remembers the default-branch chat error so the retry
	// announcement can log it AFTER the re-acquire (with a real backoff_ms).
	var transientErr error
	for {
		chatStart := time.Now()
		up, err = chat(ctx, lease, opts, normalized)
		attempts++
		st.attempts = attempts
		if err == nil {
			st.statuses = append(st.statuses, http.StatusOK)
			// Issue #74 P2: a successful chat is egress-level proof the
			// model is servable again — drop any (egress, model) unfit mark.
			// Only marks created before THIS lease's acquisition (a retry
			// re-acquires after the mark, so its success clears it; an
			// older in-flight chat succeeding must not erase a mark that
			// landed after its admission — that would reopen the
			// limited_ip re-admission burn).
			if !lease.AcquiredAt.IsZero() {
				s.pool.ClearModelUnfitBefore(effectiveModel, lease.AcquiredAt)
			}
			if attempts > 1 {
				// T13: the retry-once recovery landed — one Debug line that
				// greps the whole retry chain by req_id (ms = the retry
				// chat call's duration).
				s.logger.Debug("chat retry succeeded",
					"attempts", attempts, "req_id", st.reqID,
					"ms", time.Since(chatStart).Milliseconds())
			}
			released = true // Disarm deferred release: ownership transferred to caller
			return up, lease, nil
		}
		if s := attemptStatus(err); s != 0 {
			st.statuses = append(st.statuses, s)
		}
		failTime = time.Now()
		switch {
		case errors.Is(err, upstream.ErrModelIPLimited):
			// Issue #74 P2: the egress IP is limited for the requested
			// model. Mark (egress, model) unfit for ~5 min so new requests
			// refuse fast instead of re-admitting against a known-limited
			// gate (each admission burns a daily session slot). Retry once
			// through a fresh acquire — a different token may still
			// serve the model. The session is bound to
			// its admitted model and is NOT invalidated.
			var lie *upstream.LimitedIpError
			if errors.As(err, &lie) {
				s.pool.MarkModelUnfit(effectiveModel, lie)
			} else {
				s.pool.MarkModelUnfit(effectiveModel, nil)
			}
			release()
			if attempts > 1 {
				return nil, nil, err
			}
		case errors.Is(err, upstream.ErrSessionInvalid):
			release()
			invalidateSession(lease)
			if attempts > 1 {
				return nil, nil, err
			}
		case errors.Is(err, upstream.ErrWaitingRoomRequired):
			// #116: 428 waiting_room_required is session-ENDING
			// (endsTheSession:true — the seat is gone mid-chat;
			// reference/freebuff freebuff-session.ts FREEBUFF_GATE_CODES).
			// Drop the cached session and re-admit ONCE for this request
			// (mirror the ErrSessionInvalid budget: attempts > 1 surfaces
			// the error; the WAITING_ROOM_CHAIN fires before the next
			// create). Never loops — a single reacquire, then surface.
			release()
			invalidateSession(lease)
			if attempts > 1 {
				return nil, nil, err
			}
		case errors.Is(err, upstream.ErrSessionSuperseded):
			// #159: 409 session_superseded — another instance took over
			// the account; this session's row is GONE (endsTheSession:true
			// per FREEBUFF_GATE_CODES). TERMINAL for this request: drop the
			// cached session (reason "superseded" feeds the re-admit storm
			// detector) so the NEXT request re-admits fresh, and surface
			// the error immediately. NEVER retry on the dead instance — an
			// in-request re-admit burns a fresh daily session slot against
			// the superseding instance and risks ping-pong (the #119 retry
			// was observed as attempts=2 with the slot still wasted until
			// the client cancelled ~59s). Auto-takeover is the other
			// instance's to resolve; the next client request re-joins.
			release()
			invalidateSessionSuperseded(lease)
			return nil, nil, err
		case errors.Is(err, upstream.ErrRunInvalid):
			release()
			invalidateRun(lease, lease.AgentID)
			if attempts > 1 {
				return nil, nil, err
			}
		case errors.Is(err, upstream.ErrAuthRejected):
			cooldownAuth(lease)
			release()
			return nil, nil, err
		case errors.Is(err, upstream.ErrBanned):
			var be *upstream.BanError
			if errors.As(err, &be) {
				cooldownBan(lease, be)
			}
			release()
			return nil, nil, err
		case errors.Is(err, upstream.ErrRateLimited):
			var rle *upstream.RateLimitError
			if errors.As(err, &rle) {
				cooldownRate(lease, rle)
			}
			release()
			return nil, nil, err
		case errors.Is(err, upstream.ErrIpCapped):
			// ip_capped is admission-only (too many distinct users on the
			// egress IP), NOT a quota reset: cool the token via
			// cooldownIpCapped's bounded re-admission (#118) — full
			// retryAfterMs + jitter, capped per token per day (the 3rd hit
			// in a rolling window locks until Pacific midnight) — and never
			// invalidate the session (existing sessions keep running).
			var ice *upstream.IpCappedError
			if errors.As(err, &ice) {
				cooldownIpCapped(lease, ice)
			}
			release()
			return nil, nil, err
		case errors.Is(err, upstream.ErrCountryBlocked):
			// A chat-path country block cools the token like the admission
			// path does: without it the cached session stays "active" and
			// every request re-hits upstream run-start inside the window.
			var cbe *upstream.CountryBlockedError
			if errors.As(err, &cbe) {
				cooldownCountry(lease, cbe)
			}
			release()
			return nil, nil, err
		case errors.Is(err, upstream.ErrCredits):
			// #117: 402 is NEVER retried — the CLI throws immediately and
			// 402 is NOT in RETRYABLE_STATUS_CODES (reference/freebuff sdk
			// error-utils.ts line 16; run-agent-step.ts throws on 402). A
			// blind retry would burn a fresh lease against the same quota
			// wall (2 upstream chat POSTs). Surface for writeError, which
			// maps it to 402 out_of_credits.
			release()
			return nil, nil, err
		default:
			release()
			// Retryable UpstreamErrors (e.g. deployment_outside_hours) are
			// temporarily unavailable, not transient: a blind retry burns a
			// fresh lease against the same wall. Surface them for writeError
			// (503 upstream_retryable) instead.
			var ue *upstream.UpstreamError
			if errors.As(err, &ue) && ue.Retryable {
				return nil, nil, err
			}
			// T8: a retry cannot succeed on a canceled context (the log
			// watch showed `transient chat error, retrying once
			// err="context canceled"`) — surface the original error instead
			// of re-acquiring into a dead ctx.
			if attempts > 1 || ctx.Err() != nil {
				return nil, nil, err
			}
			transientErr = err
		}
		lease, err = acquire(ctx, effectiveModel)
		if err != nil {
			return nil, nil, err
		}
		released = false
		st.retried = true
		// The effective backoff before the retry: the re-acquire wait after
		// the failed attempt (a waiting-room/session gate can stall it).
		st.backoffMs = time.Since(failTime).Milliseconds()
		if transientErr != nil {
			// T13: logged here (not at the failure) so backoff_ms reflects
			// the real re-acquire wait before the retry attempt.
			s.logger.Debug("transient chat error, retrying once",
				"err", transientErr,
				"reason", chatErrClass(transientErr),
				"backoff_ms", st.backoffMs,
				"attempt", attempts,
				"req_id", st.reqID)
			transientErr = nil
		}
		// A fresh lease may bind a different model (fallback path): refresh
		// the effective model + body so opts.Model, the body and the
		// lease's session/run stay consistent.
		effectiveModel = lease.Model
		if effectiveModel == "" {
			effectiveModel = model
		}
		if effectiveModel != model {
			if renormalized, nerr := convert.NormalizeRequest(normalized, effectiveModel); nerr == nil {
				normalized = renormalized
			}
		}
		opts.Model = effectiveModel
		if lease.Run.RunID != opts.RunID {
			// The retry landed on a FRESH run (run-invalid path): the new
			// run's step counter starts at 1 — stamp its number so
			// llm_step_number stays per-run like the CLI.
			opts.StepNumber = int(lease.Run.NextStepNumber())
		}
		opts.RunID = lease.Run.RunID
		opts.SessionInstanceID = lease.SessionInstanceID
	}
}
