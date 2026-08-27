// health.go — token health score computation for automated rotation (Phase 5.1).
//
// The health score is a composite 0–100 integer derived from five independent
// signals already available in the pool's snapshot/cooldown/spend/quota data.
// It is purely advisory — it never gates Acquire or failover — but surfaces
// in /healthz and /metrics so operators can reason about per-token vitality.
//
// Score thresholds (from docs/automated-token-rotation.md):
//
//	≥ 80 : Active   (green)
//	50–79: Degraded  (yellow)
//	20–49: Critical  (orange)
//	< 20 : Exhausted (red)
package pool

import (
	"time"

	"freebuff-proxy/internal/session"
)

// Health score component weights. They sum to 100.
const (
	healthWeightQuota     = 40
	healthWeightCooldown  = 25
	healthWeightSpend     = 15
	healthWeightErrorRate = 10
	healthWeightFreshness = 10
)

// HealthScoreLabel returns the human-readable tier for a numeric score.
func HealthScoreLabel(score int) string {
	switch {
	case score >= 80:
		return "active"
	case score >= 50:
		return "degraded"
	case score >= 20:
		return "critical"
	default:
		return "exhausted"
	}
}

// healthScoreInput bundles every raw signal the scorer needs.
type healthScoreInput struct {
	quotaKnown              bool
	quotaTotal              float64
	quotaRemain             float64
	cooldownUntil           time.Time
	spendDay                int64
	spendLimit              int64
	totalRateLimitEvents    int64
	sessionRemainingSeconds int64
	rotationInterval        time.Duration
}

// ComputeHealthScore returns a 0–100 integer and its label from raw inputs.
func ComputeHealthScore(in healthScoreInput) (int, string) {
	s := 0

	// Quota remaining (40 points).
	if in.quotaKnown && in.quotaTotal > 0 {
		pct := in.quotaRemain / in.quotaTotal
		if pct < 0 {
			pct = 0
		}
		if pct > 1 {
			pct = 1
		}
		s += int(pct * healthWeightQuota)
	} else {
		s += healthWeightQuota
	}

	// Cooldown status (25 points).
	if in.cooldownUntil.IsZero() || !time.Now().Before(in.cooldownUntil) {
		s += healthWeightCooldown
	} else {
		remaining := time.Until(in.cooldownUntil)
		totalCooldown := in.cooldownUntil.Sub(time.Now().Add(-remaining))
		if totalCooldown > 0 {
			elapsedPct := 1 - (remaining.Seconds() / totalCooldown.Seconds())
			if elapsedPct < 0 {
				elapsedPct = 0
			}
			if elapsedPct > 1 {
				elapsedPct = 1
			}
			if elapsedPct > 0.8 {
				credit := (elapsedPct - 0.8) / 0.2
				s += int(credit * healthWeightCooldown)
			}
		}
	}

	// Spend headroom (15 points).
	if in.spendLimit > 0 {
		pct := float64(in.spendDay) / float64(in.spendLimit)
		if pct < 0 {
			pct = 0
		}
		if pct > 1 {
			pct = 1
		}
		s += int((1 - pct) * healthWeightSpend)
	} else {
		s += healthWeightSpend
	}

	// Error rate (10 points): soft ceiling at 50 events.
	const errorRateCeiling int64 = 50
	errRate := float64(in.totalRateLimitEvents) / float64(errorRateCeiling)
	if errRate > 1 {
		errRate = 1
	}
	s += int((1 - errRate) * healthWeightErrorRate)

	// Session freshness (10 points).
	if in.sessionRemainingSeconds > 0 && in.rotationInterval > 0 {
		freshness := float64(in.sessionRemainingSeconds) / in.rotationInterval.Seconds()
		if freshness < 0 {
			freshness = 0
		}
		if freshness > 1 {
			freshness = 1
		}
		s += int(freshness * healthWeightFreshness)
	} else if in.rotationInterval > 0 {
		// No active session: 0 freshness credit.
	} else {
		s += healthWeightFreshness
	}

	if s < 0 {
		s = 0
	}
	if s > 100 {
		s = 100
	}
	return s, HealthScoreLabel(s)
}

// buildHealthScoreInput constructs a healthScoreInput from token-level data.
func buildHealthScoreInput(
	quotaByModel map[string]session.QuotaSnapshot,
	cooldownUntil time.Time,
	spendDay int64,
	spendLimit int64,
	totalRateLimitEvents int64,
	sessionRemainingSeconds int64,
	rotationInterval time.Duration,
) healthScoreInput {
	in := healthScoreInput{
		cooldownUntil:           cooldownUntil,
		spendDay:                spendDay,
		spendLimit:              spendLimit,
		totalRateLimitEvents:    totalRateLimitEvents,
		sessionRemainingSeconds: sessionRemainingSeconds,
		rotationInterval:        rotationInterval,
	}
	if len(quotaByModel) > 0 {
		in.quotaKnown = true
		for _, q := range quotaByModel {
			if q.Limit > 0 {
				in.quotaTotal += q.Limit
				rem := q.Limit - q.RecentCount
				if rem < 0 {
					rem = 0
				}
				in.quotaRemain += rem
			}
		}
	}
	return in
}

// countRateLimitEvents sums all rate-limit event counters.
func countRateLimitEvents(events map[string]int64) int64 {
	var total int64
	for _, n := range events {
		total += n
	}
	return total
}
