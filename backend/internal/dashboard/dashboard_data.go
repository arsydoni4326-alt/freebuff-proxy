package dashboard

import (
	"net"
	"net/http"
	"time"

	"freebuff-proxy/backend/internal/config"
	"freebuff-proxy/backend/internal/pool"
	"freebuff-proxy/backend/internal/stealth"
)

// --- overview ---

// riskBreakdown is the current passive ban-risk engine verdict exposed on
// the dashboard overview (Phase 3.5).
type riskBreakdown struct {
	Score   int      `json:"score"`
	Level   string   `json:"level"`
	Reasons []string `json:"reasons,omitempty"`
	Samples int      `json:"samples"`
}

// circuitBreakerInfo surfaces the bridge circuit breaker state on the
// dashboard Overview (ROADMAP §2.2). Only meaningful in bridge mode when
// BRIDGE_CIRCUIT_BREAKER_FAILURES > 0; zero values are emitted otherwise.
type circuitBreakerInfo struct {
	Enabled           bool    `json:"enabled"`
	Open              bool    `json:"open"`
	FailureCount      int     `json:"failure_count"`
	FailuresRemaining int     `json:"failures_remaining"`
	CooldownRemaining float64 `json:"cooldown_remaining"`
	Threshold         int     `json:"threshold"`
	Window            string  `json:"window"`
	Cooldown          string  `json:"cooldown"`
}

type overviewData struct {
	BaseURL              string            `json:"base_url"`
	Mode                 string            `json:"mode"`
	InBridge             bool              `json:"in_bridge"`
	ShowBridge           bool              `json:"show_bridge"`
	BridgeTokens         int               `json:"bridge_tokens"`
	BridgeTokenCards     []bridgeTokenCard `json:"bridge_token_cards,omitempty"`
	Models               []string          `json:"models"`
	ModelCount           int               `json:"model_count"`
	Uptime               string            `json:"uptime"`
	SafeMode             bool              `json:"safe_mode"`
	MaxMessagesPerDay    int               `json:"max_messages_per_day"`
	TransientRetries     int64             `json:"transient_retries"`
	FingerprintRotations int64             `json:"fingerprint_rotations"`
	Tokens               []tokenCard       `json:"tokens"`
	HasTokens            bool              `json:"has_tokens"`
	IsDefaultAdminToken  bool              `json:"is_default_admin_token"`
	// Registry freshness (Phase 4.2): RegistryFallback is true when the
	// current model catalog is the offline fallback rather than a live
	// upstream refresh; RegistryLastRefresh is a human-readable "time ago"
	// string ("" when the registry has never refreshed live). Surfaced on
	// the overview so operators can spot a stale offline path at a glance.
	RegistryFallback    bool   `json:"registry_fallback"`
	RegistryLastRefresh string `json:"registry_last_refresh"`
	// Risk (Phase 3.5): passive ban-risk engine verdict (read-only — the
	// engine warns but never modifies routing).  Available on the overview
	// so operators can judge ban exposure at a glance.
	Risk           riskBreakdown      `json:"risk"`
	CircuitBreaker circuitBreakerInfo `json:"circuit_breaker"`
	RequireLogin   bool               `json:"require_login"`
	UpstreamSync   *upstreamSync      `json:"upstream_sync,omitempty"`
}

// upstreamSync is the dashboard-friendly view of the embedded
// backend/internal/dashboard/data/upstream_drift.json. Computed once at request
// time; cheap.
type upstreamSync struct {
	UpstreamSHA  string         `json:"upstream_sha"`            // short SHA, "(not yet reported)" before first CI run
	CheckedAt    string         `json:"checked_at"`              // RFC3339
	HasDrift     bool           `json:"has_drift"`               // any non-SAME file
	HasRegistry  bool           `json:"has_registry_drift"`      // 5 pinned files
	HasWire      bool           `json:"has_wire_drift"`          // wire files MISSING_UPSTREAM
	DriftedFiles []upstreamFile `json:"drifted_files,omitempty"` // the actual changes
	ReleasesURL  string         `json:"releases_url"`            // where to update
}

type upstreamFile struct {
	Group     string `json:"group"` // "registry" | "wire"
	File      string `json:"file"`
	PinnedSHA string `json:"pinned_sha"`
	VendorSHA string `json:"vendor_sha"`
	Status    string `json:"status"` // DRIFT | MISSING_UPSTREAM | SAME
}
type tokenCard struct {
	Index         int    `json:"index"`
	TokenValue    string `json:"token_value,omitempty"` // actual token value for removal
	Email         string `json:"email,omitempty"`
	AccountID     string `json:"account_id,omitempty"`
	SessionStatus string `json:"session_status"`
	QueuePosition int    `json:"queue_position"`
	QueueDepth    int    `json:"queue_depth"`
	ActiveRuns    int    `json:"active_runs"`
	Requests      int    `json:"requests"`
	Messages24h   int    `json:"messages_24h"`
	DailyLimit    int    `json:"daily_limit"`
	UsagePct      int    `json:"usage_pct"`
	// Per-token request limits (issue: RPD/RPM): live counters + configured
	// caps (0 = unlimited) + time until the next Pacific midnight (the
	// official daily reset) in seconds, so the dashboard can show usage and
	// lock state per account.
	RequestsPerMinute      int     `json:"requests_per_minute"`
	RequestsPerDay         int     `json:"requests_per_day"`
	RequestsPerMinuteLimit int     `json:"requests_per_minute_limit"`
	RequestsPerDayLimit    int     `json:"requests_per_day_limit"`
	RequestsPerDayResetIn  int     `json:"requests_per_day_reset_in"` // seconds
	RiskLevel              string  `json:"risk_level"`
	CooldownActive         bool    `json:"cooldown_active"`
	CooldownUntil          string  `json:"cooldown_until"`
	Locked                 bool    `json:"locked"`
	BanType                string  `json:"ban_type,omitempty"`
	BannedUntil            string  `json:"banned_until,omitempty"`
	TransientRetries       int64   `json:"transient_retries"`
	AllowlistSkips         int64   `json:"allowlist_skips,omitempty"`
	HasStanding            bool    `json:"has_standing"`
	StandingLevel          string  `json:"standing_level"`
	StandingLabel          string  `json:"standing_label"`
	StandingScore          float64 `json:"standing_score"`
	StandingNextLevel      string  `json:"standing_next_level"`
	StandingNextLevelAt    string  `json:"standing_next_level_at"`
	// Standing cap + earn-back hints (issue #140, FreebuffStandingInfo):
	// cappedBy/cappedReason name the trust cap holding the level, blurb is
	// upstream's human explanation, nextSteps the suggested actions.
	StandingCappedBy     string             `json:"standing_capped_by,omitempty"`
	StandingCappedReason string             `json:"standing_capped_reason,omitempty"`
	StandingBlurb        string             `json:"standing_blurb,omitempty"`
	StandingNextSteps    []standingStepCard `json:"standing_next_steps,omitempty"`
	// Referral (FreebuffReferralInfo): invite program state for this token.
	HasReferral            bool   `json:"has_referral"`
	ReferralCode           string `json:"referral_code,omitempty"`
	ReferralQualifiedCount int    `json:"referral_qualified_count"`
	ReferralSessionsLeft   int    `json:"referral_sessions_left"`
	ReferralGithubLinked   bool   `json:"referral_github_linked"`
	ReferralResetAt        string `json:"referral_reset_at,omitempty"`
	// Freebucks (issue #232): balance + daily/weekly/monthly windows +
	// bindingWindow + prices. Nil when the session has not reported it.
	Freebucks *freebucksCard `json:"freebucks,omitempty"`
	// FreeWindows (issue #319): free-tier session-pool day/week/month
	// windows. Display-only; nil when the session has not reported it.
	FreeWindows *freeWindowsCard `json:"free_windows,omitempty"`
	// Subscription (issue #319): subscriber usage rings + provider spend,
	// rollout-audience only; nil otherwise.
	Subscription *subscriptionCard `json:"subscription,omitempty"`
	// AllowedModels is the slot's MODEL_LOCKS allowlist (issue #325); nil
	// when unlocked. Config-static: rides the full fetch, cached by the SPA.
	AllowedModels   []string `json:"allowed_models,omitempty"`
	Streak          int      `json:"streak,omitempty"`
	TodayUsed       bool     `json:"today_used,omitempty"`
	LastUsage       string   `json:"last_usage,omitempty"`
	StreakUpdatedAt string   `json:"streak_updated_at,omitempty"`
	// Maturity is the streak-maturity automation view (nil until maturity
	// is first enabled for the token).
	Maturity *maturityCard `json:"maturity,omitempty"`
}

// maturityCard is the dashboard view of pool.MaturitySnapshot: automation
// toggle + streak target + touch mode + badge + today's slot + last touch
// (time, action, result, advance) + non-advance warning. Nil when the token
// never opted in.
type maturityCard struct {
	Enabled       bool   `json:"enabled"`
	Target        int    `json:"target"`
	Mode          string `json:"mode"`
	Badge         string `json:"badge,omitempty"`
	Slot          string `json:"slot,omitempty"`
	LastTouch     string `json:"last_touch,omitempty"`
	LastAction    string `json:"last_action,omitempty"`
	LastResult    string `json:"last_result,omitempty"`
	LastAdvanced  string `json:"last_advanced,omitempty"`
	Warn          bool   `json:"warn,omitempty"`
	NoAdvanceDays int    `json:"no_advance_days,omitempty"`
}

// freebucksWindowCard is one window of the Freebucks allowance (issue #232):
// limit/spent/remaining + reset_at (RFC3339 string; empty when zero) +
// percent_used (spent/limit*100, 0 when limit==0). Mirrors
// upstream.FreebucksWindow but with string times and snake_case JSON for the
// dashboard API (daily/weekly/monthly).
type freebucksWindowCard struct {
	Limit       float64 `json:"limit"`
	Spent       float64 `json:"spent"`
	Remaining   float64 `json:"remaining"`
	ResetAt     string  `json:"reset_at,omitempty"`
	PercentUsed float64 `json:"percent_used"`
}

// freebucksCard is the dashboard view of upstream.FreebucksInfo (issue #232,
// shape issue #321): balance + the daily pool window + the never-expiring
// wallet + the USD spend ceiling + the plan id + per-model prices.
// Nil when the session has not reported Freebucks (nil-safe callers check).
// Exposed alongside premium_quota, not replacing it.
type freebucksCard struct {
	Balance float64             `json:"balance"`
	Daily   freebucksWindowCard `json:"daily"`
	Wallet  freebucksWalletCard `json:"wallet"`
	Spend   freebucksSpendCard  `json:"spend"`
	// Monthly is the monthly dollar allowance (wire drift 2026-09-04,
	// issue #330). Nil when the server predates it — the SPA renders
	// nothing rather than a zero that would read as "spent".
	Monthly      *freebucksWindowCard `json:"monthly,omitempty"`
	PlanID       string               `json:"plan_id,omitempty"`
	Prices       map[string]float64   `json:"prices,omitempty"`
	PriceNotices map[string]string    `json:"price_notices,omitempty"`
	// QuotaExempt is the server-authorized quota exemption (wire drift
	// 2026-09-05, issue #350): new sessions stay usable at zero balance.
	QuotaExempt bool `json:"quota_exempt,omitempty"`
}

// freebucksWalletCard is the dashboard view of the never-expiring Freebucks
// wallet: spendable balance, monthly plan bonus (0 on free), and the ISO
// instant the next plan bonus lands (empty when absent).
type freebucksWalletCard struct {
	Balance      float64 `json:"balance"`
	MonthlyBonus float64 `json:"monthly_bonus"`
	NextBonusAt  string  `json:"next_bonus_at,omitempty"`
}

// freebucksSpendCard is the dashboard view of the Freebucks USD spend
// ceiling: the cap plus the ISO instant the day rolls.
type freebucksSpendCard struct {
	LimitUsd float64 `json:"limit_usd"`
	ResetAt  string  `json:"reset_at,omitempty"`
}

// freeWindowsCard is the dashboard view of upstream.FreeWindowsInfo
// (issue #319): free-tier session-pool day/week/month used/limit windows.
type freeWindowsCard struct {
	DayUsed      float64 `json:"day_used"`
	DayLimit     float64 `json:"day_limit"`
	WeekUsed     float64 `json:"week_used"`
	WeekLimit    float64 `json:"week_limit"`
	MonthUsed    float64 `json:"month_used"`
	MonthLimit   float64 `json:"month_limit"`
	DayResetAt   string  `json:"day_reset_at,omitempty"`
	MonthResetAt string  `json:"month_reset_at,omitempty"`
}

// subscriptionCard is the dashboard view of upstream.SubscriptionInfo
// (issue #319): subscriber day / five-day / month usage rings + provider
// spend USD. Rollout-audience only.
type subscriptionCard struct {
	DayUsed            float64  `json:"day_used"`
	DayLimit           float64  `json:"day_limit"`
	FiveDayUsed        float64  `json:"five_day_used"`
	FiveDayLimit       float64  `json:"five_day_limit"`
	MonthUsed          float64  `json:"month_used"`
	MonthLimit         float64  `json:"month_limit"`
	DayPremiumUsed     float64  `json:"day_premium_used"`
	DayPremiumLimit    float64  `json:"day_premium_limit"`
	DayResetAt         string   `json:"day_reset_at,omitempty"`
	PeriodEndsAt       string   `json:"period_ends_at,omitempty"`
	MonthSpendUsd      float64  `json:"month_spend_usd"`
	MonthSpendLimitUsd float64  `json:"month_spend_limit_usd"`
	FreeDayUsed        *float64 `json:"free_day_used,omitempty"`
	FreeDayLimit       *float64 `json:"free_day_limit,omitempty"`
}

// standingStepCard is one dashboard-ready earn-back action
// (FreebuffTrustNextStep).
type standingStepCard struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Detail string  `json:"detail,omitempty"`
	Points float64 `json:"points"`
	Href   string  `json:"href,omitempty"`
}

// bridgeQuotaRow is one model's quota snapshot for the bridge quota dashboard.
type bridgeQuotaRow struct {
	Model       string  `json:"model"`
	Limit       float64 `json:"limit"`
	RecentCount float64 `json:"recent"`
	Period      string  `json:"period"`
	ResetAt     string  `json:"reset_at,omitempty"`
}

// bridgeTokenCard is a dashboard-ready view of one bridge entry (#187).

// bridgeTokenCard is a dashboard-ready view of one bridge entry (#187).
type bridgeTokenCard struct {
	Key             string                     `json:"key"`    // masked hash prefix
	Status          string                     `json:"status"` // active|cooldown|locked|dead
	Model           string                     `json:"model"`
	ActiveRuns      int                        `json:"active_runs"`
	Requests        int                        `json:"requests"`
	Locked          bool                       `json:"locked"`
	CooldownUntil   string                     `json:"cooldown_until"`
	SessionActive   bool                       `json:"session_active"`
	SpendDay        float64                    `json:"spend_day"`
	SpendPct        int                        `json:"spend_pct"`
	SpendLimit      int64                      `json:"spend_limit"`
	BanType         string                     `json:"ban_type,omitempty"`
	BannedUntil     string                     `json:"banned_until,omitempty"`
	PremiumQuota    *pool.PremiumQuotaSnapshot `json:"premium_quota,omitempty"`
	Quota           []bridgeQuotaRow           `json:"quota,omitempty"`
	RateLimitHits   int64                      `json:"rate_limit_hits"`
	RateLimitMisses int64                      `json:"rate_limit_misses"`
	RateLimitRate   float64                    `json:"rate_limit_rate"`
	Freebucks       *freebucksCard             `json:"freebucks,omitempty"`
}

func bridgeCardFromSnapshot(snap pool.BridgeTokenSnapshot, spendLimit int64) bridgeTokenCard {
	status := "active"
	if snap.DeadToken {
		status = "dead"
	} else if snap.Locked {
		status = "locked"
	} else if snap.CooldownUntil.After(time.Now()) {
		status = "cooldown"
	}
	bannedUntil := ""
	if !snap.BannedUntil.IsZero() && snap.BanType == "temporary" {
		bannedUntil = snap.BannedUntil.Format(time.RFC3339)
	}
	var quota []bridgeQuotaRow
	for _, q := range snap.QuotaByModel {
		row := bridgeQuotaRow{
			Model:       q.Model,
			Limit:       q.Limit,
			RecentCount: q.RecentCount,
			Period:      q.Period,
		}
		if !q.ResetAt.IsZero() {
			row.ResetAt = q.ResetAt.Format(time.RFC3339)
		}
		quota = append(quota, row)
	}
	return bridgeTokenCard{
		Key:             shortKey(snap.Key),
		Status:          status,
		Model:           snap.Model,
		ActiveRuns:      snap.ActiveRuns,
		Requests:        snap.Requests,
		Locked:          snap.Locked,
		CooldownUntil:   shortTime(snap.CooldownUntil),
		SessionActive:   snap.SessionActive,
		SpendDay:        snap.SpendDay,
		SpendPct:        snap.SpendPct,
		SpendLimit:      spendLimit,
		BanType:         snap.BanType,
		BannedUntil:     bannedUntil,
		PremiumQuota:    snap.PremiumQuota,
		Quota:           quota,
		RateLimitHits:   snap.RateLimitHits,
		RateLimitMisses: snap.RateLimitMisses,
		RateLimitRate:   snap.RateLimitRate,
		Freebucks:       freebucksCardFromInfo(snap.Freebucks),
	}
}

type configData struct {
	EnvContent string     `json:"env_content"`
	HasEnvFile bool       `json:"has_env_file"`
	Effective  []configKV `json:"effective"`
}

type configKV struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

func (d *Dashboard) configData() configData {
	cfg := d.cfg()
	cd := configData{}
	if _, raw, exists, err := config.EnvFileInfo(); err == nil && exists {
		cd.HasEnvFile = true
		cd.EnvContent = string(raw)
	} else {
		cd.EnvContent = config.DefaultEnvTemplate()
	}
	// Effective values come from the config package's own catalog-driven
	// rendering (issue #288): one key map, no per-key switch here.
	for _, entry := range cfg.Data() {
		cd.Effective = append(cd.Effective, configKV{
			Key:    entry.Key,
			Value:  entry.Value,
			Secret: entry.Secret,
		})
	}
	return cd
}

// baseURLForRequest computes the dynamic API base URL (/v1) for dashboard views.
// It prioritizes the incoming request's Host / X-Forwarded headers so that operators
// accessing the dashboard via VPS IP, domain, VPN, or reverse proxy see the exact
// URL their AI coding clients should dial. Falls back to LISTEN_ADDR when r is nil.
func baseURLForRequest(cfg *config.Config, r *http.Request) string {
	scheme := "http"
	host := ""
	if r != nil {
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		} else if r.TLS != nil {
			scheme = "https"
		}
		if fHost := r.Header.Get("X-Forwarded-Host"); fHost != "" {
			host = fHost
		} else if r.Host != "" {
			host = r.Host
		}
	}
	if host == "" {
		host = "127.0.0.1:3457"
		if cfg != nil && cfg.ListenAddr != "" {
			h, p, err := net.SplitHostPort(cfg.ListenAddr)
			if err == nil {
				if h == "" || h == "0.0.0.0" || h == "::" {
					h = "127.0.0.1"
				}
				host = net.JoinHostPort(h, p)
			} else {
				host = cfg.ListenAddr
			}
		}
	}
	return scheme + "://" + host + "/v1"
}

func (d *Dashboard) overviewData(r *http.Request) overviewData {
	cfg := d.cfg()
	ps := d.pool.PoolSnapshot()
	mode := cfg.EffectiveMode()
	// Phase 3.5: read the shared passive ban-risk engine verdict for the
	// dashboard overview.
	rs := stealth.DefaultRiskEngine.Score()
	od := overviewData{
		BaseURL:              baseURLForRequest(cfg, r),
		Mode:                 mode,
		InBridge:             mode == "bridge",
		ShowBridge:           mode == "bridge" || mode == "hybrid",
		Models:               servedModels(d.reg),
		ModelCount:           len(servedModels(d.reg)),
		Uptime:               humanDuration(time.Since(d.started)),
		SafeMode:             cfg.SafeMode,
		MaxMessagesPerDay:    cfg.MaxMessagesPerDay,
		TransientRetries:     ps.TransientRetries,
		FingerprintRotations: ps.FingerprintRotations,
		BridgeTokens:         d.pool.BridgeCount(),
		IsDefaultAdminToken:  cfg.IsDefaultAdminToken(),
		RequireLogin:       cfg.RequireLogin(),
		// Registry freshness (Phase 4.2): reflect the live vs fallback state
		// and the last successful refresh time on the overview so a stale
		// offline path is visible without leaving the dashboard.
		// Risk (Phase 3.5): passive ban-risk engine verdict (read-only — the
		// engine warns but never modifies routing).  Available on the overview
		// so operators can judge ban exposure at a glance.
		RegistryFallback:    d.reg.UsingFallback(),
		RegistryLastRefresh: shortTime(d.reg.LastRefreshAt()),
		Risk: riskBreakdown{
			Score:   rs.Score,
			Level:   string(rs.Level),
			Reasons: rs.Reasons,
			Samples: rs.Samples,
		},
	}
	// Circuit breaker state (ROADMAP §2.2): surface the bridge-mode
	// circuit breaker on the Overview so operators can see at a glance
	// whether upstream is being protected from cascading failures.
	bs := d.pool.BreakerSnapshot()
	od.CircuitBreaker = circuitBreakerInfo{
		Enabled:           bs.Enabled,
		Open:              bs.Open,
		FailureCount:      bs.FailureCount,
		FailuresRemaining: bs.FailuresRemaining,
		CooldownRemaining: bs.CooldownRemaining,
		Threshold:         bs.Threshold,
		Window:            bs.Window,
		Cooldown:          bs.Cooldown,
	}
	for _, t := range ps.Tokens {
		od.Tokens = append(od.Tokens, cardFromSnapshot(t))
	}
	// Regression guard (#200): df7a16a dropped this line, leaving
	// has_tokens permanently false so pooled operators saw "No upstream
	// tokens configured" on Overview while the Tokens tab worked.
	od.HasTokens = len(od.Tokens) > 0
	// Bridge token cards (#187): live snapshots of bridge-mode entries.
	spendLimit := cfg.MaxSpendPerDay
	if od.ShowBridge {
		for _, snap := range d.pool.BridgeSnapshot() {
			od.BridgeTokenCards = append(od.BridgeTokenCards, bridgeCardFromSnapshot(snap, spendLimit))
		}
	}
	od.UpstreamSync = parseUpstreamSync(upstreamDriftJSON)
	return od
}

// overviewLiveData is the hot-poll subset of overviewData (issue #322):
// live numbers only. Restart/deploy-only fields (base_url, mode, models,
// safe_mode, max_messages_per_day, transient_retries, upstream_sync) and
// account-stable card fields ride the once-per-mount full fetch; the SPA
// merges them back over this shape.
type overviewLiveData struct {
	Uptime           string            `json:"uptime"`
	Tokens           []tokenLiveCard   `json:"tokens"`
	HasTokens        bool              `json:"has_tokens"`
	BridgeTokens     int               `json:"bridge_tokens"`
	BridgeTokenCards []bridgeTokenCard `json:"bridge_token_cards,omitempty"`
}

// overviewLiveData builds the 15s hot-poll payload: uptime, per-token live
// cards, and bridge relay state. Uptime is string-formatted like the full
// view; bridge cards are live snapshots, identical to the full shape.
func (d *Dashboard) overviewLiveData() overviewLiveData {
	ps := d.pool.PoolSnapshot()
	od := overviewLiveData{
		Uptime:       humanDuration(time.Since(d.started)),
		BridgeTokens: d.pool.BridgeCount(),
	}
	for _, t := range ps.Tokens {
		od.Tokens = append(od.Tokens, liveCardFromSnapshot(t))
	}
	od.HasTokens = len(od.Tokens) > 0
	if mode := d.cfg().EffectiveMode(); mode == "bridge" || mode == "hybrid" {
		spendLimit := d.cfg().MaxSpendPerDay
		for _, snap := range d.pool.BridgeSnapshot() {
			od.BridgeTokenCards = append(od.BridgeTokenCards, bridgeCardFromSnapshot(snap, spendLimit))
		}
	}
	return od
}
