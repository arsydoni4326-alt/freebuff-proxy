package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"freebuff-proxy/backend/internal/pool"
	"freebuff-proxy/backend/internal/telemetry"
)

// handleHealthz reports uptime, model count, the per-token snapshot, the
// cached bridge entries (bridge mode), and the effective routing mode.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	snaps := s.pool.Snapshot()
	probeResults := s.pool.ProbeSnapshot()
	cfg := s.cfg.Load()
	w.Header().Set("Content-Type", "application/json")
	tokens := make([]map[string]any, 0, len(snaps))
	for _, snap := range snaps {
		tok := map[string]any{
			"Token":                snap.Token,
			"CooldownUntil":        snap.CooldownUntil,
			"SessionStatus":        snap.SessionStatus,
			"SessionInstanceID":    snap.SessionInstanceID,
			"SessionQueuePosition": snap.SessionQueuePosition,
			"SessionQueueDepth":    snap.SessionQueueDepth,
			"ActiveRuns":           snap.ActiveRuns,
			"Requests":             snap.Requests,
			"Messages24h":          snap.Messages24h,
			"DailyLimit":           snap.DailyLimit,
			"UsagePct":             snap.UsagePct,
			// Spend ledger (issue #87/#122): Pacific-day/week/month buckets
			// plus the advisory MAX_SPEND_PER_DAY ceiling (SpendLimit/
			// SpendPct, informational — the upstream $ ceilings are
			// server-enforced) and the spend_limited refusal counter.
			"Spend24h":                  snap.Spend24h,
			"SpendDay":                  snap.SpendDay,
			"SpendWeek":                 snap.SpendWeek,
			"SpendMonth":                snap.SpendMonth,
			"SpendDayStart":             snap.SpendDayStart,
			"SpendLimit":                snap.SpendLimit,
			"SpendPct":                  snap.SpendPct,
			"SpendLimited":              snap.SpendLimited,
			"RiskLevel":                 snap.RiskLevel,
			"country":                   snap.CountryCode,
			"session_model":             snap.SessionModel,
			"session_remaining_seconds": snap.SessionRemainingSeconds,
			"health_score":              snap.HealthScore,
			"health_score_label":        snap.HealthScoreLabel,
			"quarantined":               snap.Quarantined,
			"quarantine_reason":         snap.QuarantineReason,
		}
		// Background health probe results (TOKEN_HEALTH_PROBES).
		if probeResults != nil {
			if pr, ok := probeResults[snap.Token]; ok {
				tok["probe_ok"] = pr.OK
				tok["probe_quota_ok"] = pr.QuotaOK
				if pr.Error != "" {
					tok["probe_error"] = pr.Error
				}
				if !pr.ProbedAt.IsZero() {
					tok["probe_at"] = pr.ProbedAt
				}
			}
		}
		if len(snap.QuotaByModel) > 0 {
			quota := make(map[string]any, len(snap.QuotaByModel))
			for model, q := range snap.QuotaByModel {
				entry := map[string]any{
					"limit":        q.Limit,
					"recent_count": q.RecentCount,
					"period":       q.Period,
				}
				if !q.ResetAt.IsZero() {
					entry["reset_at"] = q.ResetAt
				}
				if len(q.Entitlement) > 0 {
					entry["entitlement"] = q.Entitlement
				}
				quota[model] = entry
			}
			tok["quota"] = quota
		}
		if snap.PremiumQuota != nil {
			tok["premium_quota"] = premiumQuotaMap(snap.PremiumQuota)
		}
		if len(snap.Entitlement) > 0 {
			tok["entitlement"] = snap.Entitlement
		}
		tokens = append(tokens, tok)
	}
	// Bridge token snapshots (#187): per-entry data when in bridge mode.
	bridgeSnaps := s.pool.BridgeSnapshot()
	bridgeEntries := make([]map[string]any, 0, len(bridgeSnaps))
	for _, bs := range bridgeSnaps {
		entry := map[string]any{
			"key":            bs.Key[:min(8, len(bs.Key))] + "…",
			"locked":         bs.Locked,
			"session_active": bs.SessionActive,
			"active_runs":    bs.ActiveRuns,
			"requests":       bs.Requests,
			"model":          bs.Model,
			"spend_day":      bs.SpendDay,
			"spend_pct":      bs.SpendPct,
			"dead_token":     bs.DeadToken,
		}
		if bs.CooldownUntil.After(time.Now()) {
			entry["cooldown_until"] = bs.CooldownUntil
		}
		if len(bs.QuotaByModel) > 0 {
			quota := make(map[string]any, len(bs.QuotaByModel))
			for model, q := range bs.QuotaByModel {
				qEntry := map[string]any{
					"limit":        q.Limit,
					"recent_count": q.RecentCount,
					"period":       q.Period,
				}
				if !q.ResetAt.IsZero() {
					qEntry["reset_at"] = q.ResetAt
				}
				if len(q.Entitlement) > 0 {
					qEntry["entitlement"] = q.Entitlement
				}
				quota[model] = qEntry
			}
			entry["quota"] = quota
		}
		if bs.PremiumQuota != nil {
			entry["premium_quota"] = premiumQuotaMap(bs.PremiumQuota)
		}
		bridgeEntries = append(bridgeEntries, entry)
	}
	// Registry freshness (Phase 4.2): surface the last successful live
	// refresh timestamp and whether the current state is the offline
	// fallback so operators can detect a stale offline path from /healthz
	// without inspecting logs. A zero last-refresh means the registry has
	// never refreshed from upstream (fresh boot or fallback-only state).
	registryLastRefresh := s.reg.LastRefreshAt()
	registryFreshness := map[string]any{
		"fallback": s.reg.UsingFallback(),
	}
	if registryLastRefresh.IsZero() {
		registryFreshness["last_refresh"] = nil
		registryFreshness["age_seconds"] = nil
	} else {
		registryFreshness["last_refresh"] = registryLastRefresh
		registryFreshness["age_seconds"] = time.Since(registryLastRefresh).Seconds()
	}

	// Circuit breaker state (Phase: Circuit Breaker Observability).
	breakerSnap := s.pool.BreakerSnapshot()
	var breakerUntil any
	if breakerSnap.Until != nil {
		breakerUntil = breakerSnap.Until.Format(time.RFC3339)
	}
	circuitBreaker := map[string]any{
		"enabled":                    breakerSnap.Enabled,
		"open":                       breakerSnap.Open,
		"failure_count":              breakerSnap.FailureCount,
		"failures_remaining":         breakerSnap.FailuresRemaining,
		"cooldown_remaining_seconds": breakerSnap.CooldownRemaining,
		"until":                      breakerUntil,
		"config": map[string]any{
			"failures_threshold": breakerSnap.Threshold,
			"window":             breakerSnap.Window,
			"cooldown":           breakerSnap.Cooldown,
		},
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":          "ok",
		"mode":            cfg.EffectiveMode(),
		"uptime_seconds":  time.Since(s.started).Seconds(),
		"models":          s.servedModelCount(),
		"tokens":          tokens,
		"bridge_tokens":   s.pool.BridgeCount(),
		"bridge_entries":  bridgeEntries,
		"circuit_breaker": circuitBreaker,
		"registry":        registryFreshness,
	})
}

// premiumQuotaMap renders a PremiumQuotaSnapshot as the healthz JSON map.
// reset_at is RFC3339; model is fixed to the premium pool sentinel for
// clients that key off it.
func premiumQuotaMap(q *pool.PremiumQuotaSnapshot) map[string]any {
	m := map[string]any{
		"limit":        q.Limit,
		"used":         q.Used,
		"remaining":    q.Remaining,
		"period":       q.Period,
		"reset_at":     q.ResetAt.Format(time.RFC3339),
		"percent_used": q.PercentUsed,
		"entitled":     q.Entitled,
		"capped":       q.Capped,
		"model":        "_premium_pool",
	}
	return m
}

// escapeLabelValue escapes a Prometheus label value per the text exposition
// format: backslash, double quote, and newline are escaped; everything else
// passes through unchanged.
func escapeLabelValue(v string) string {
	if !strings.ContainsAny(v, `\"\n`) {
		return v
	}
	var sb strings.Builder
	for _, r := range v {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// handleMetrics exports Prometheus metrics (#24).
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var sb strings.Builder
	uptime := time.Since(s.started).Seconds()
	ps := s.pool.PoolSnapshot()
	snaps := ps.Tokens

	sb.WriteString("# HELP freebuff_proxy_uptime_seconds Process uptime in seconds\n")
	sb.WriteString("# TYPE freebuff_proxy_uptime_seconds gauge\n")
	fmt.Fprintf(&sb, "freebuff_proxy_uptime_seconds %.2f\n\n", uptime)

	sb.WriteString("# HELP freebuff_proxy_models_total Count of models available in registry\n")
	sb.WriteString("# TYPE freebuff_proxy_models_total gauge\n")
	fmt.Fprintf(&sb, "freebuff_proxy_models_total %d\n\n", s.servedModelCount())

	sb.WriteString("# HELP freebuff_proxy_tokens_total Count of configured tokens in pool\n")
	sb.WriteString("# TYPE freebuff_proxy_tokens_total gauge\n")
	fmt.Fprintf(&sb, "freebuff_proxy_tokens_total %d\n\n", len(snaps))

	sb.WriteString("# HELP freebuff_proxy_rate_limit_rejected_total Total client requests rejected by local rate limiter\n")
	sb.WriteString("# TYPE freebuff_proxy_rate_limit_rejected_total counter\n")
	fmt.Fprintf(&sb, "freebuff_proxy_rate_limit_rejected_total %d\n\n", s.rateLimitRejections.Load())
	sb.WriteString("# HELP freebuff_proxy_model_unavailable_skips_total Session admissions skipped via the model_unavailable window cache (issue #158)\n")
	sb.WriteString("# TYPE freebuff_proxy_model_unavailable_skips_total counter\n")
	fmt.Fprintf(&sb, "freebuff_proxy_model_unavailable_skips_total %d\n\n", telemetry.ModelUnavailableSkips.Load())
	sb.WriteString("# HELP freebuff_proxy_token_messages_24h Rolling 24h message count per token\n")
	sb.WriteString("# TYPE freebuff_proxy_token_messages_24h gauge\n")
	for _, snap := range snaps {
		fmt.Fprintf(&sb, "freebuff_proxy_token_messages_24h{token=\"%d\"} %d\n", snap.Token+1, snap.Messages24h)
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_token_requests_total Total requests served per token\n")
	sb.WriteString("# TYPE freebuff_proxy_token_requests_total counter\n")
	for _, snap := range snaps {
		fmt.Fprintf(&sb, "freebuff_proxy_token_requests_total{token=\"%d\"} %d\n", snap.Token+1, snap.Requests)
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_token_active_runs Active agent runs per token\n")
	sb.WriteString("# TYPE freebuff_proxy_token_active_runs gauge\n")
	for _, snap := range snaps {
		fmt.Fprintf(&sb, "freebuff_proxy_token_active_runs{token=\"%d\"} %d\n", snap.Token+1, snap.ActiveRuns)
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_token_cooldown_active Is token currently cooling down (1=yes, 0=no)\n")
	sb.WriteString("# TYPE freebuff_proxy_token_cooldown_active gauge\n")
	now := time.Now()
	for _, snap := range snaps {
		cd := 0
		if !snap.CooldownUntil.IsZero() && now.Before(snap.CooldownUntil) {
			cd = 1
		}
		fmt.Fprintf(&sb, "freebuff_proxy_token_cooldown_active{token=\"%d\"} %d\n", snap.Token+1, cd)
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_quota_recent Current usage toward the per-model quota window\n")
	sb.WriteString("# TYPE freebuff_proxy_quota_recent gauge\n")
	for _, snap := range snaps {
		for model, q := range snap.QuotaByModel {
			fmt.Fprintf(&sb, "freebuff_proxy_quota_recent{token=\"%d\",model=\"%s\",period=\"%s\"} %g\n",
				snap.Token+1, escapeLabelValue(model), escapeLabelValue(q.Period), q.RecentCount)
		}
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_quota_limit Per-model quota limit for the window\n")
	sb.WriteString("# TYPE freebuff_proxy_quota_limit gauge\n")
	for _, snap := range snaps {
		for model, q := range snap.QuotaByModel {
			fmt.Fprintf(&sb, "freebuff_proxy_quota_limit{token=\"%d\",model=\"%s\",period=\"%s\"} %g\n",
				snap.Token+1, escapeLabelValue(model), escapeLabelValue(q.Period), q.Limit)
		}
	}

	sb.WriteString("# HELP freebuff_proxy_quota_remaining Remaining allowance toward the per-model quota window\n")
	sb.WriteString("# TYPE freebuff_proxy_quota_remaining gauge\n")
	for _, snap := range snaps {
		for model, q := range snap.QuotaByModel {
			rem := float64(0)
			if q.Limit > 0 {
				rem = q.Limit - q.RecentCount
				if rem < 0 {
					rem = 0
				}
			}
			fmt.Fprintf(&sb, "freebuff_proxy_quota_remaining{token=\"%d\",model=\"%s\",period=\"%s\"} %g\n",
				snap.Token+1, escapeLabelValue(model), escapeLabelValue(q.Period), rem)
		}
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_session_remaining_seconds Remaining time in seconds for the active session\n")
	sb.WriteString("# TYPE freebuff_proxy_session_remaining_seconds gauge\n")
	for _, snap := range snaps {
		if snap.SessionModel != "" && snap.SessionRemainingSeconds > 0 {
			fmt.Fprintf(&sb, "freebuff_proxy_session_remaining_seconds{token=\"%d\",model=\"%s\"} %d\n",
				snap.Token+1, escapeLabelValue(snap.SessionModel), snap.SessionRemainingSeconds)
		}
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_transient_retries_total Transient transport failures retried per token\n")
	sb.WriteString("# TYPE freebuff_proxy_transient_retries_total counter\n")
	for _, snap := range snaps {
		if snap.TransientRetries > 0 {
			fmt.Fprintf(&sb, "freebuff_proxy_transient_retries_total{token=\"%d\"} %d\n", snap.Token+1, snap.TransientRetries)
		}
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_fingerprint_rotations_total TLS fingerprint rotations per token\n")
	sb.WriteString("# TYPE freebuff_proxy_fingerprint_rotations_total counter\n")
	for _, snap := range snaps {
		if snap.FingerprintRotations > 0 {
			fmt.Fprintf(&sb, "freebuff_proxy_fingerprint_rotations_total{token=\"%d\"} %d\n", snap.Token+1, snap.FingerprintRotations)
		}
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_rate_limit_events_total Upstream rate-limit classifications per token and code\n")
	sb.WriteString("# TYPE freebuff_proxy_rate_limit_events_total counter\n")
	for _, snap := range snaps {
		for code, n := range snap.RateLimitEvents {
			if n > 0 {
				fmt.Fprintf(&sb, "freebuff_proxy_rate_limit_events_total{token=\"%d\",code=\"%s\"} %d\n",
					snap.Token+1, escapeLabelValue(code), n)
			}
		}
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_model_locked_total Session releases on model lock, by model switch (from to)\n")
	sb.WriteString("# TYPE freebuff_proxy_model_locked_total counter\n")
	for _, snap := range snaps {
		for from, tos := range snap.ModelLocked {
			for to, n := range tos {
				if n > 0 {
					fmt.Fprintf(&sb, "freebuff_proxy_model_locked_total{token=\"%d\",from=\"%s\",to=\"%s\"} %d\n",
						snap.Token+1, escapeLabelValue(from), escapeLabelValue(to), n)
				}
			}
		}
	}
	sb.WriteString("\n")

	// Premium quota metrics (quota_tracker.go): one gauge family per field,
	// emitted only when the premium snapshot is present (nil means no data).
	sb.WriteString("# HELP freebuff_proxy_premium_quota_limit Premium quota limit (4 for pacific_day pool) per token\n")
	sb.WriteString("# TYPE freebuff_proxy_premium_quota_limit gauge\n")
	for _, snap := range snaps {
		if snap.PremiumQuota != nil {
			fmt.Fprintf(&sb, "freebuff_proxy_premium_quota_limit{token=\"%d\"} %d\n", snap.Token+1, snap.PremiumQuota.Limit)
		}
	}
	for _, bs := range s.pool.BridgeSnapshot() {
		if bs.PremiumQuota != nil {
			fmt.Fprintf(&sb, "freebuff_proxy_premium_quota_limit{token=\"bridge_%s\"} %d\n", escapeLabelValue(bs.Key), bs.PremiumQuota.Limit)
		}
	}
	sb.WriteString("\n")
	sb.WriteString("# HELP freebuff_proxy_premium_quota_used Premium quota used per token\n")
	sb.WriteString("# TYPE freebuff_proxy_premium_quota_used gauge\n")
	for _, snap := range snaps {
		if snap.PremiumQuota != nil {
			fmt.Fprintf(&sb, "freebuff_proxy_premium_quota_used{token=\"%d\"} %d\n", snap.Token+1, snap.PremiumQuota.Used)
		}
	}
	for _, bs := range s.pool.BridgeSnapshot() {
		if bs.PremiumQuota != nil {
			fmt.Fprintf(&sb, "freebuff_proxy_premium_quota_used{token=\"bridge_%s\"} %d\n", escapeLabelValue(bs.Key), bs.PremiumQuota.Used)
		}
	}
	sb.WriteString("\n")
	sb.WriteString("# HELP freebuff_proxy_premium_quota_remaining Premium quota remaining per token\n")
	sb.WriteString("# TYPE freebuff_proxy_premium_quota_remaining gauge\n")
	for _, snap := range snaps {
		if snap.PremiumQuota != nil {
			fmt.Fprintf(&sb, "freebuff_proxy_premium_quota_remaining{token=\"%d\"} %d\n", snap.Token+1, snap.PremiumQuota.Remaining)
		}
	}
	for _, bs := range s.pool.BridgeSnapshot() {
		if bs.PremiumQuota != nil {
			fmt.Fprintf(&sb, "freebuff_proxy_premium_quota_remaining{token=\"bridge_%s\"} %d\n", escapeLabelValue(bs.Key), bs.PremiumQuota.Remaining)
		}
	}
	sb.WriteString("\n")
	sb.WriteString("# HELP freebuff_proxy_premium_quota_percent Premium quota percent used per token\n")
	sb.WriteString("# TYPE freebuff_proxy_premium_quota_percent gauge\n")
	for _, snap := range snaps {
		if snap.PremiumQuota != nil {
			fmt.Fprintf(&sb, "freebuff_proxy_premium_quota_percent{token=\"%d\"} %d\n", snap.Token+1, snap.PremiumQuota.PercentUsed)
		}
	}
	for _, bs := range s.pool.BridgeSnapshot() {
		if bs.PremiumQuota != nil {
			fmt.Fprintf(&sb, "freebuff_proxy_premium_quota_percent{token=\"bridge_%s\"} %d\n", escapeLabelValue(bs.Key), bs.PremiumQuota.PercentUsed)
		}
	}
	sb.WriteString("\n")
	if s.logs != nil {
		// T20: handled-record counters from the dashboard log ring. The key
		// is logring's "level|msg" (level lowercased). msg is a free-form
		// operator message, so the label is escaped like every upstream-
		// derived label.
		sb.WriteString("# HELP freebuff_proxy_log_events_total Log records handled per level and message\n")
		sb.WriteString("# TYPE freebuff_proxy_log_events_total counter\n")
		for key, n := range s.logs.Counts() {
			level, msg, ok := strings.Cut(key, "|")
			if !ok {
				continue
			}
			fmt.Fprintf(&sb, "freebuff_proxy_log_events_total{level=\"%s\",msg=\"%s\"} %d\n",
				escapeLabelValue(level), escapeLabelValue(msg), n)
		}
		sb.WriteString("\n")
	}

	// Bridge-specific Prometheus metrics (entries, evictions, cooldowns).
	s.bridgeMetrics(&sb)

	// Registry freshness (Phase 4.2): emit a staleness gauge so dashboards
	// and alerting can detect a stale offline fallback path without parsing
	// logs. registry_age_seconds is the time since the last successful live
	// refresh (zero when the registry has never refreshed); registry_fallback
	// is 1 when the current state is the offline fallback, 0 when it is live.
	registryLastRefresh := s.reg.LastRefreshAt()
	registryAge := 0.0
	if !registryLastRefresh.IsZero() {
		registryAge = time.Since(registryLastRefresh).Seconds()
	}
	registryFallback := 0
	if s.reg.UsingFallback() {
		registryFallback = 1
	}
	sb.WriteString("# HELP freebuff_proxy_registry_age_seconds Seconds since the last successful live model registry refresh (0 = never refreshed)\n")
	sb.WriteString("# TYPE freebuff_proxy_registry_age_seconds gauge\n")
	fmt.Fprintf(&sb, "freebuff_proxy_registry_age_seconds %g\n\n", registryAge)
	sb.WriteString("# HELP freebuff_proxy_registry_fallback 1 when the registry is serving the offline hardcoded fallback, 0 when live-refreshed\n")
	sb.WriteString("# TYPE freebuff_proxy_registry_fallback gauge\n")
	fmt.Fprintf(&sb, "freebuff_proxy_registry_fallback %d\n\n", registryFallback)

	_, _ = w.Write([]byte(sb.String()))
}

// bridgeMetrics appends bridge-specific Prometheus metrics to sb.
// It reads bridge snapshots and emits zero-value defaults when bridge mode
// is inactive so dashboards with pre-provisioned queries get consistent data.
func (s *Server) bridgeMetrics(sb *strings.Builder) {
	now := time.Now()
	bridgeSnaps := s.pool.BridgeSnapshot()

	sb.WriteString("# HELP freebuff_proxy_bridge_entries_total Current bridge cache entries\n")
	sb.WriteString("# TYPE freebuff_proxy_bridge_entries_total gauge\n")
	fmt.Fprintf(sb, "freebuff_proxy_bridge_entries_total %d\n\n", len(bridgeSnaps))

	var coolingDown, deadTokens, locked int
	for _, snap := range bridgeSnaps {
		if !snap.CooldownUntil.IsZero() && now.Before(snap.CooldownUntil) {
			coolingDown++
		}
		if snap.DeadToken {
			deadTokens++
		}
		if snap.Locked {
			locked++
		}
	}

	sb.WriteString("# HELP freebuff_proxy_bridge_cooling_down_total Bridge entries currently cooling down\n")
	sb.WriteString("# TYPE freebuff_proxy_bridge_cooling_down_total gauge\n")
	fmt.Fprintf(sb, "freebuff_proxy_bridge_cooling_down_total %d\n\n", coolingDown)

	sb.WriteString("# HELP freebuff_proxy_bridge_dead_tokens_total Bridge entries with dead tokens\n")
	sb.WriteString("# TYPE freebuff_proxy_bridge_dead_tokens_total gauge\n")
	fmt.Fprintf(sb, "freebuff_proxy_bridge_dead_tokens_total %d\n\n", deadTokens)

	sb.WriteString("# HELP freebuff_proxy_bridge_locked_total Bridge entries locked by administrator\n")
	sb.WriteString("# TYPE freebuff_proxy_bridge_locked_total gauge\n")
	fmt.Fprintf(sb, "freebuff_proxy_bridge_locked_total %d\n\n", locked)

	sb.WriteString("# HELP freebuff_proxy_bridge_requests_total Total requests served through bridge entries\n")
	sb.WriteString("# TYPE freebuff_proxy_bridge_requests_total counter\n")
	for _, snap := range bridgeSnaps {
		fmt.Fprintf(sb, "freebuff_proxy_bridge_requests_total{token_label=\"%s\"} %d\n",
			escapeLabelValue(snap.Key[:min(8, len(snap.Key))]), snap.Requests)
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_bridge_active_runs Active agent runs per bridge entry\n")
	sb.WriteString("# TYPE freebuff_proxy_bridge_active_runs gauge\n")
	for _, snap := range bridgeSnaps {
		fmt.Fprintf(sb, "freebuff_proxy_bridge_active_runs{token_label=\"%s\"} %d\n",
			escapeLabelValue(snap.Key[:min(8, len(snap.Key))]), snap.ActiveRuns)
	}
	sb.WriteString("\n")

	sb.WriteString("# HELP freebuff_proxy_bridge_quota_remaining Quota remaining per bridge entry per model\n")
	sb.WriteString("# TYPE freebuff_proxy_bridge_quota_remaining gauge\n")
	for _, snap := range bridgeSnaps {
		for model, q := range snap.QuotaByModel {
			rem := float64(0)
			if q.Limit > 0 {
				rem = q.Limit - q.RecentCount
				if rem < 0 {
					rem = 0
				}
			}
			fmt.Fprintf(sb, "freebuff_proxy_bridge_quota_remaining{token_label=\"%s\",model=\"%s\"} %g\n",
				escapeLabelValue(snap.Key[:min(8, len(snap.Key))]), escapeLabelValue(model), rem)
		}
	}
	sb.WriteString("\n")

	// Circuit breaker metrics (Phase: Circuit Breaker Observability).
	breakerSnap := s.pool.BreakerSnapshot()
	openVal := 0
	if breakerSnap.Open {
		openVal = 1
	}
	sb.WriteString("# HELP freebuff_proxy_bridge_breaker_open 1 when the circuit breaker is blocking requests, 0 otherwise\n")
	sb.WriteString("# TYPE freebuff_proxy_bridge_breaker_open gauge\n")
	fmt.Fprintf(sb, "freebuff_proxy_bridge_breaker_open %d\n\n", openVal)

	sb.WriteString("# HELP freebuff_proxy_bridge_breaker_failures Current number of transient failures in the circuit breaker sliding window\n")
	sb.WriteString("# TYPE freebuff_proxy_bridge_breaker_failures gauge\n")
	fmt.Fprintf(sb, "freebuff_proxy_bridge_breaker_failures %d\n\n", breakerSnap.FailureCount)
}
