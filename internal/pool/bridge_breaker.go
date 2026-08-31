// bridge_breaker.go — circuit breaker for batch upstream failures.
//
// When BRIDGE_CIRCUIT_BREAKER_FAILURES > 0, a burst of transient upstream
// 5xx/network failures within the sliding window opens the breaker. While
// open, AcquireBridge short-circuits to a 503 upstream_retryable error
// with Retry-After instead of hammering a batch-down upstream.
//
// Only genuine transient outages (UpstreamError with Retryable, or raw
// network/transport errors) trip the breaker. Classified errors — auth,
// rate-limit, ban, country_blocked, ip_capped, session-invalid, etc. —
// never trip it.
package pool

import (
	"errors"
	"fmt"
	"time"

	"freebuff-proxy/backend/internal/config"
	"freebuff-proxy/backend/internal/upstream"
)

// isBridgeTransientFailure reports whether err is a genuine transient
// upstream outage (5xx/network) rather than a classified refusal that
// should never trip the breaker.
func isBridgeTransientFailure(err error) bool {
	if err == nil {
		return false
	}
	// Classified errors never trip the breaker.
	if asRateLimit(err) != nil || asIpCapped(err) != nil || asBan(err) != nil ||
		asCountryBlocked(err) != nil || errors.Is(err, upstream.ErrAuthRejected) ||
		errors.Is(err, upstream.ErrWaitingRoomRequired) || errors.Is(err, upstream.ErrWaitingRoom) ||
		errors.Is(err, upstream.ErrSessionInvalid) || errors.Is(err, upstream.ErrSessionSuperseded) ||
		errors.Is(err, upstream.ErrCredits) || errors.Is(err, upstream.ErrFreeModeCLIRequired) ||
		errors.Is(err, upstream.ErrCapacityDeferred) || errors.Is(err, upstream.ErrModelIPLimited) ||
		errors.Is(err, upstream.ErrRunInvalid) || errors.Is(err, upstream.ErrNoActiveSession) {
		return false
	}
	// A Retryable UpstreamError or a status ≥500 is a transient upstream outage.
	var ue *upstream.UpstreamError
	if errors.As(err, &ue) {
		return ue.Retryable || ue.Status >= 500
	}
	// Any other error that isn't classified is treated as transient
	// (e.g. raw network/transport error, context deadline from upstream).
	return true
}

// breakerRecord records one transient upstream failure in the sliding
// window and opens the breaker if the threshold is reached. Called with
// bridgeMu held.
func (p *Pool) breakerRecordLocked(cfg *config.Config) {
	if cfg.BridgeCircuitBreakerFailures <= 0 {
		return
	}
	now := time.Now()
	window := cfg.BridgeCircuitBreakerWindow
	cutoff := now.Add(-window)

	// Reap old entries.
	n := 0
	for _, f := range p.breakerFailures {
		if f.After(cutoff) {
			break
		}
		n++
	}
	p.breakerFailures = p.breakerFailures[n:]

	// Record the new failure.
	p.breakerFailures = append(p.breakerFailures, now)

	// Check the threshold.
	if len(p.breakerFailures) >= cfg.BridgeCircuitBreakerFailures {
		p.breakerUntil = now.Add(cfg.BridgeCircuitBreakerCooldown)
		p.breakerFailures = nil // reset window on trip
		p.logger.Warn("pool: bridge circuit breaker opened",
			"failures", cfg.BridgeCircuitBreakerFailures,
			"window", cfg.BridgeCircuitBreakerWindow.String(),
			"cooldown", cfg.BridgeCircuitBreakerCooldown.String(),
			"until", p.breakerUntil.Format(time.RFC3339))
	}
}

// breakerOpenLocked reports whether the circuit breaker is currently open
// (calculated from the sliding window, not the cached breakerUntil, so the
// very first access after a trip also counts against the threshold). Called
// with bridgeMu held.
func (p *Pool) breakerOpenLocked(cfg *config.Config) bool {
	if cfg.BridgeCircuitBreakerFailures <= 0 {
		return false
	}
	now := time.Now()

	// If the breakerUntil timestamp is still in the future, it's open.
	if !p.breakerUntil.IsZero() && now.Before(p.breakerUntil) {
		return true
	}

	// Cooldown elapsed — check whether the WINDOW still has enough
	// failures to re-open (counted from recent timestamps, not the cached
	// breakerUntil). This handles the case where N-1 failures accumulated,
	// the breaker opened briefly, cooldown expired, then the last failure
	// arrived — the window should still be open.
	if !p.breakerUntil.IsZero() {
		// Cooldown expired: re-close and fall through to check the window.
		p.breakerUntil = time.Time{}
		p.breakerFailures = nil
	}

	cutoff := now.Add(-cfg.BridgeCircuitBreakerWindow)
	n := 0
	for _, f := range p.breakerFailures {
		if f.After(cutoff) {
			break
		}
		n++
	}
	if n > 0 {
		p.breakerFailures = p.breakerFailures[n:]
	}

	if len(p.breakerFailures) >= cfg.BridgeCircuitBreakerFailures {
		p.breakerUntil = now.Add(cfg.BridgeCircuitBreakerCooldown)
		p.breakerFailures = nil
		p.logger.Warn("pool: bridge circuit breaker re-opened after cooldown expiry",
			"failures", len(p.breakerFailures),
			"until", p.breakerUntil.Format(time.RFC3339))
		return true
	}
	return false
}

// breakerFailWithCause records a transient upstream failure and returns a
// 503 upstream_retryable error suitable for the server's error mapper.
func (p *Pool) breakerFailWithCause(cfg *config.Config) *upstream.UpstreamError {
	p.bridgeMu.Lock()
	p.breakerRecordLocked(cfg)
	remaining := time.Until(p.breakerUntil)
	p.bridgeMu.Unlock()

	if remaining <= 0 {
		remaining = cfg.BridgeCircuitBreakerCooldown
	}
	return &upstream.UpstreamError{
		Status:     503,
		Body:       fmt.Sprintf("bridge circuit breaker open: upstream unavailable (retry after %s)", remaining.Round(time.Second)),
		RetryAfter: remaining,
		Retryable:  true,
	}
}

// breakerRecordFailureClass records a transient upstream failure in the
// circuit breaker if the error is a genuine transient outage. Returns
// true when the error was recorded (for callers that want to emit
// metrics/logs). Called with bridgeMu NOT held.
func (p *Pool) breakerRecordFailureClass(cfg *config.Config, err error) bool {
	if !isBridgeTransientFailure(err) {
		return false
	}
	p.bridgeMu.Lock()
	p.breakerRecordLocked(cfg)
	p.bridgeMu.Unlock()
	return true
}
