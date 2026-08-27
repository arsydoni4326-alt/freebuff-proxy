// health_test.go — tests for token health score computation (Phase 5.1).
package pool

import (
	"testing"
	"time"

	"freebuff-proxy/internal/session"
)

func TestComputeHealthScore_HealthyToken(t *testing.T) {
	// Fresh token: full quota, no cooldown, no spend, no errors, full session.
	in := healthScoreInput{
		quotaKnown: true, quotaTotal: 100, quotaRemain: 100,
		spendLimit: 0, totalRateLimitEvents: 0,
		sessionRemainingSeconds: 21600, // full 6h rotation interval
		rotationInterval:        6 * time.Hour,
	}
	score, label := ComputeHealthScore(in)
	if score != 100 {
		t.Errorf("score = %d, want 100", score)
	}
	if label != "active" {
		t.Errorf("label = %q, want active", label)
	}
}

func TestComputeHealthScore_ExhaustedQuota(t *testing.T) {
	in := healthScoreInput{
		quotaKnown: true, quotaTotal: 100, quotaRemain: 0,
		spendLimit: 0, totalRateLimitEvents: 0,
		sessionRemainingSeconds: 21600, rotationInterval: 6 * time.Hour,
	}
	score, label := ComputeHealthScore(in)
	// Lost 40 points for quota = 0; freshness is 21600/21600 = 100% → 10 pts.
	if score != 60 {
		t.Errorf("score = %d, want 60", score)
	}
	if label != "degraded" {
		t.Errorf("label = %q, want degraded", label)
	}
}

func TestComputeHealthScore_ActiveCooldown(t *testing.T) {
	in := healthScoreInput{
		cooldownUntil: time.Now().Add(30 * time.Minute),
		spendLimit: 0, totalRateLimitEvents: 0,
		sessionRemainingSeconds: 3600, rotationInterval: 6 * time.Hour,
	}
	score, _ := ComputeHealthScore(in)
	if score > 75 || score < 50 {
		t.Errorf("score = %d, want 50-75 (cooldown penalty)", score)
	}
}

func TestComputeHealthScore_HighErrorRate(t *testing.T) {
	in := healthScoreInput{
		quotaKnown: true, quotaTotal: 100, quotaRemain: 100,
		spendLimit: 0, totalRateLimitEvents: 50,
		sessionRemainingSeconds: 21600, rotationInterval: 6 * time.Hour,
	}
	score, label := ComputeHealthScore(in)
	// Lost 10 points for max error rate.
	if score != 90 {
		t.Errorf("score = %d, want 90", score)
	}
	if label != "active" {
		t.Errorf("label = %q, want active", label)
	}
}

func TestComputeHealthScore_SpendExhausted(t *testing.T) {
	in := healthScoreInput{
		quotaKnown: true, quotaTotal: 100, quotaRemain: 100,
		spendDay: 100, spendLimit: 100, totalRateLimitEvents: 0,
		sessionRemainingSeconds: 21600, rotationInterval: 6 * time.Hour,
	}
	score, _ := ComputeHealthScore(in)
	// Lost 15 points for spend = 100%.
	if score != 85 {
		t.Errorf("score = %d, want 85", score)
	}
}

func TestComputeHealthScore_NoSession(t *testing.T) {
	in := healthScoreInput{
		quotaKnown: true, quotaTotal: 100, quotaRemain: 100,
		spendLimit: 0, totalRateLimitEvents: 0,
		sessionRemainingSeconds: 0, rotationInterval: 6 * time.Hour,
	}
	score, _ := ComputeHealthScore(in)
	if score != 90 {
		t.Errorf("score = %d, want 90", score)
	}
}

func TestComputeHealthScore_ExhaustedToken(t *testing.T) {
	in := healthScoreInput{
		quotaKnown: true, quotaTotal: 100, quotaRemain: 0,
		spendDay: 200, spendLimit: 100, totalRateLimitEvents: 50,
		sessionRemainingSeconds: 0, rotationInterval: 6 * time.Hour,
	}
	score, label := ComputeHealthScore(in)
	if score > 30 {
		t.Errorf("score = %d, want <= 30", score)
	}
	if label != "exhausted" && label != "critical" {
		t.Errorf("label = %q, want exhausted or critical", label)
	}
}

func TestComputeHealthScore_UnknownQuota(t *testing.T) {
	in := healthScoreInput{
		quotaKnown: false, spendLimit: 0, totalRateLimitEvents: 0,
		sessionRemainingSeconds: 21600, rotationInterval: 6 * time.Hour,
	}
	score, _ := ComputeHealthScore(in)
	// Full credit: 40 (quota) + 25 (cooldown) + 15 (spend) + 10 (errors) + 10 (freshness) = 100.
	if score != 100 {
		t.Errorf("score = %d, want 100", score)
	}
}

func TestHealthScoreLabel(t *testing.T) {
	tests := []struct {
		score int
		want  string
	}{
		{100, "active"}, {80, "active"}, {79, "degraded"}, {50, "degraded"},
		{49, "critical"}, {20, "critical"}, {19, "exhausted"}, {0, "exhausted"},
	}
	for _, tt := range tests {
		if got := HealthScoreLabel(tt.score); got != tt.want {
			t.Errorf("HealthScoreLabel(%d) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestCountRateLimitEvents(t *testing.T) {
	events := map[string]int64{"rate_limited": 5, "ip_capped": 2, "spend_limited": 1}
	if got := countRateLimitEvents(events); got != 8 {
		t.Errorf("countRateLimitEvents = %d, want 8", got)
	}
	if got := countRateLimitEvents(nil); got != 0 {
		t.Errorf("countRateLimitEvents(nil) = %d, want 0", got)
	}
}

func TestBuildHealthScoreInput(t *testing.T) {
	quota := map[string]session.QuotaSnapshot{
		"deepseek/deepseek-v4-flash": {Limit: 5, RecentCount: 2},
		"mimo/mimo-v2.5":            {Limit: 3, RecentCount: 1},
	}
	in := buildHealthScoreInput(quota, time.Time{}, 10, 0, 3, 3600, 6*time.Hour)
	if !in.quotaKnown {
		t.Error("quotaKnown = false, want true")
	}
	if in.quotaTotal != 8 {
		t.Errorf("quotaTotal = %g, want 8", in.quotaTotal)
	}
	if in.quotaRemain != 5 {
		t.Errorf("quotaRemain = %g, want 5", in.quotaRemain)
	}
	score, label := ComputeHealthScore(in)
	if score < 60 || score > 100 {
		t.Errorf("score = %d, want 60-100", score)
	}
	if label != "active" && label != "degraded" {
		t.Errorf("label = %q, want active or degraded", label)
	}
}
