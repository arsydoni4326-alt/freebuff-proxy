// health_warn.go — token health transition logging, backup-token suggestions,
// and reactive auto-rotation (Phase 5.2).
//
// When a token's health score transitions to "critical" or "exhausted", the
// pool emits a WARN log suggesting the operator add backup tokens. When
// AUTO_ROTATE_ON_EXHAUSTION is enabled, exhausted tokens are deprioritised
// during Acquire — requests route to healthier tokens instead. The exhausted
// token remains eligible as a last resort so a single-token pool never deadlocks.
package pool

import (
	"sync"
	"time"
)

// healthState tracks the last-known health label for each token so
// transitions are detected exactly once (avoiding log spam).
type healthState struct {
	mu     sync.Mutex
	labels map[int]string // token index → last label
}

func newHealthState() *healthState {
	return &healthState{labels: make(map[int]string)}
}

// checkTransition returns the new label when the token's health label has
// changed since the last call, and updates the stored state. Returns ""
// when the label is unchanged or first-seen (first snapshot is baseline).
func (hs *healthState) checkTransition(idx int, label string) string {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	prev, exists := hs.labels[idx]
	hs.labels[idx] = label
	if !exists {
		// First observation — baseline, no transition.
		return ""
	}
	if prev == label {
		return ""
	}
	return label
}

// isExhausted reports whether the label indicates an exhausted token.
func isExhausted(label string) bool {
	return label == "exhausted"
}

// isCriticalOrWorse reports whether the label is critical or exhausted.
func isCriticalOrWorse(label string) bool {
	return label == "critical" || label == "exhausted"
}

// logHealthTransition emits a WARN log when a token's health label changes
// to critical or exhausted, suggesting the operator add backup tokens.
func (p *Pool) logHealthTransition(idx int, label string, snaps []TokenSnapshot) {
	if !isCriticalOrWorse(label) {
		return
	}
	// Build a summary of healthy vs unhealthy tokens for context.
	var healthy, degraded, critical, exhausted int
	for _, s := range snaps {
		switch s.HealthScoreLabel {
		case "active":
			healthy++
		case "degraded":
			degraded++
		case "critical":
			critical++
		case "exhausted":
			exhausted++
		}
	}
	p.logger.Warn("token health degraded — consider adding backup tokens",
		"token", idx+1,
		"health_label", label,
		"healthy_tokens", healthy,
		"degraded_tokens", degraded,
		"critical_tokens", critical,
		"exhausted_tokens", exhausted,
		"hint", "add more AUTH_TOKENS for redundancy")
}

// checkHealthTransitions scans all tokens and logs transitions for critical
// and exhausted labels. Called after every Snapshot() computation.
func (p *Pool) checkHealthTransitions(snaps []TokenSnapshot) {
	if p.healthTracker == nil {
		return
	}
	for _, s := range snaps {
		if label := p.healthTracker.checkTransition(s.Token, s.HealthScoreLabel); label != "" {
			p.logHealthTransition(s.Token, label, snaps)
		}
	}
}

// predictExhaustion returns the estimated time until the token's quota
// exhausts at the current usage rate, and whether the prediction is
// within the warning threshold. Returns (eta, warned) where warned is
// true when the ETA is within the threshold (or quota is already gone).
func predictExhaustion(snap TokenSnapshot, threshold time.Duration) (time.Duration, bool) {
	if threshold <= 0 {
		return 0, false
	}
	// Find the model with the lowest remaining quota.
	var minRemain float64
	var minTotal float64
	for _, q := range snap.QuotaByModel {
		rem := q.Limit - q.RecentCount
		if rem < 0 {
			rem = 0
		}
		if minTotal == 0 || rem/minTotal < minRemain/minTotal {
			minRemain = rem
			minTotal = q.Limit
		}
	}
	if minTotal <= 0 {
		return 0, false
	}
	// Already exhausted?
	if minRemain <= 0 {
		return 0, true
	}
	// Estimate: we don't have a usage rate here, so we use the quota
	// remaining as a fraction of the rotation interval. This is a rough
	// heuristic — the real rate would require tracking usage over time.
	// For now, if remaining is < 10% of total, flag it.
	remainPct := minRemain / minTotal
	if remainPct < 0.1 {
		// Estimate ETA as proportion of rotation interval.
		eta := time.Duration(remainPct * float64(time.Hour))
		return eta, eta <= threshold
	}
	return 0, false
}
