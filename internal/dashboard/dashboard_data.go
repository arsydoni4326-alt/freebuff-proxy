package dashboard

import (
	"fmt"
	"freebuff-proxy/internal/pool"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// --- overview ---

type overviewData struct {
	BaseURL              string            `json:"base_url"`
	Mode                 string            `json:"mode"`
	InBridge             bool              `json:"in_bridge"`
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
	// UpstreamSync summarises the latest .github/workflows/upstream-drift
	// run (compiled into the binary). Users on an out-of-date build see
	// HasDrift=true + DriftedFiles and know to update.
	UpstreamSync *upstreamSync `json:"upstream_sync,omitempty"`
}

// upstreamSync is the dashboard-friendly view of the embedded
// internal/dashboard/data/upstream_drift.json. Computed once at request
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
	Index               int     `json:"index"`
	SessionStatus       string  `json:"session_status"`
	QueuePosition       int     `json:"queue_position"`
	QueueDepth          int     `json:"queue_depth"`
	ActiveRuns          int     `json:"active_runs"`
	Requests            int     `json:"requests"`
	Messages24h         int     `json:"messages_24h"`
	DailyLimit          int     `json:"daily_limit"`
	UsagePct            int     `json:"usage_pct"`
	RiskLevel           string  `json:"risk_level"`
	CooldownActive      bool    `json:"cooldown_active"`
	CooldownUntil       string  `json:"cooldown_until"`
	Locked              bool    `json:"locked"`
	BanType             string  `json:"ban_type,omitempty"`
	BannedUntil         string  `json:"banned_until,omitempty"`
	TransientRetries    int64   `json:"transient_retries"`
	HasStanding         bool    `json:"has_standing"`
	StandingLevel       string  `json:"standing_level"`
	StandingLabel       string  `json:"standing_label"`
	StandingScore       float64 `json:"standing_score"`
	StandingNextLevel   string  `json:"standing_next_level"`
	StandingNextLevelAt string  `json:"standing_next_level_at"`
	// Standing cap + earn-back hints (issue #140 P3d, FreebuffStandingInfo):
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

// bridgeTokenCard is a dashboard-ready view of one bridge entry (#187).
type bridgeTokenCard struct {
	Key             string                     `json:"key"`    // masked hash prefix
	Status          string                     `json:"status"` // active|cooldown|locked
	Model           string                     `json:"model"`
	ActiveRuns      int                        `json:"active_runs"`
	Requests        int                        `json:"requests"`
	Locked          bool                       `json:"locked"`
	CooldownUntil   string                     `json:"cooldown_until"`
	SessionActive   bool                       `json:"session_active"`
	SpendDay        float64                    `json:"spend_day"`
	SpendPct        int                        `json:"spend_pct"`
	BanType         string                     `json:"ban_type,omitempty"`
	BannedUntil     string                     `json:"banned_until,omitempty"`
	PremiumQuota    *pool.PremiumQuotaSnapshot `json:"premium_quota,omitempty"`
	Glm53FlashQuota *pool.PremiumQuotaSnapshot `json:"glm53flash_quota,omitempty"`
}

func bridgeCardFromSnapshot(snap pool.BridgeTokenSnapshot) bridgeTokenCard {
	status := "active"
	if snap.Locked {
		status = "locked"
	} else if snap.CooldownUntil.After(time.Now()) {
		status = "cooldown"
	}
	bannedUntil := ""
	if !snap.BannedUntil.IsZero() && snap.BanType == "temporary" {
		bannedUntil = snap.BannedUntil.Format(time.RFC3339)
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
		BanType:         snap.BanType,
		BannedUntil:     bannedUntil,
		PremiumQuota:    snap.PremiumQuota,
		Glm53FlashQuota: snap.Glm53FlashQuota,
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
	if raw, err := os.ReadFile(".env"); err == nil {
		cd.HasEnvFile = true
		cd.EnvContent = string(raw)
	} else {
		cd.EnvContent = defaultEnvTemplate
	}
	cd.Effective = []configKV{
		{Key: "LISTEN_ADDR", Value: cfg.ListenAddr},
		{Key: "UPSTREAM_BASE_URL", Value: cfg.UpstreamBaseURL},
		{Key: "AUTH_TOKENS", Value: fmt.Sprintf("%d token(s)", len(cfg.AuthTokens)), Secret: true},
		{Key: "API_KEYS", Value: fmt.Sprintf("%d key(s)", len(cfg.APIKeys)), Secret: true},
		{Key: "ADMIN_TOKEN", Value: boolWord(cfg.AdminToken != ""), Secret: true},
		{Key: "ROTATION_INTERVAL", Value: cfg.RotationInterval.String()},
		{Key: "REQUEST_TIMEOUT", Value: cfg.RequestTimeout.String()},
		{Key: "SESSION_CALL_TIMEOUT", Value: cfg.SessionCallTimeout.String()},
		{Key: "COST_MODE", Value: cfg.CostMode},
		{Key: "TLS_FINGERPRINT", Value: cfg.TLSFingerprint},
		{Key: "REGISTRY_REFRESH", Value: cfg.RegistryRefresh.String()},
		{Key: "DEBUG_DUMP", Value: strconv.FormatBool(cfg.DebugDump)},
		{Key: "LOG_FILE", Value: cfg.LogFile},
		{Key: "LOG_LEVEL", Value: cfg.LogLevel},
		{Key: "MAX_MESSAGES_PER_DAY", Value: strconv.Itoa(cfg.MaxMessagesPerDay)},
		{Key: "IDLE_ROTATION_TIMEOUT", Value: cfg.IdleRotationTimeout.String()},
		{Key: "SAFE_MODE", Value: strconv.FormatBool(cfg.SafeMode)},
		{Key: "REQUEST_JITTER", Value: cfg.RequestJitter.String()},
		{Key: "CLI_VERSION", Value: cfg.CLIVersion},
		{Key: "MODEL_ALIASES", Value: fmt.Sprintf("%d alias(es)", len(cfg.ModelAliases)), Secret: true},
		{Key: "MODELS_ALLOW", Value: strings.Join(cfg.ModelsAllow, ",")},
		{Key: "TRANSIENT_RETRIES", Value: strconv.Itoa(cfg.TransientRetries)},
	}
	return cd
}

func boolWord(v bool) string {
	if v {
		return "set"
	}
	return "unset"
}

const defaultEnvTemplate = `# freebuff-proxy configuration (.env)
# Keys mirror the environment variables; leave commented to keep the default.
# See the README and docs/guides for the full reference.

#LISTEN_ADDR=127.0.0.1:3457
#UPSTREAM_BASE_URL=https://www.codebuff.com
#AUTH_TOKENS=token1,token2
#API_KEYS=sk-local-...
#ADMIN_TOKEN=change-me
#ROTATION_INTERVAL=6h
#REQUEST_TIMEOUT=15m
#SESSION_CALL_TIMEOUT=30s
#COST_MODE=free
#TLS_FINGERPRINT=chrome120
#REGISTRY_REFRESH=6h
#DEBUG_DUMP=false
#LOG_FILE=
#LOG_LEVEL=info
#MAX_MESSAGES_PER_DAY=0
#IDLE_ROTATION_TIMEOUT=0
#SAFE_MODE=true
#REQUEST_JITTER=0s
#CLI_VERSION=0.10.7
#MODEL_ALIASES=
#MODELS_ALLOW=
#TRANSIENT_RETRIES=1
`

func (d *Dashboard) overviewData() overviewData {
	cfg := d.cfg()
	ps := d.pool.PoolSnapshot()
	mode := cfg.EffectiveMode()
	host := "localhost"
	if h, _, err := net.SplitHostPort(cfg.ListenAddr); err == nil && h != "" && h != "0.0.0.0" && h != "::" {
		host = h
	}
	od := overviewData{
		BaseURL:              "http://" + host + "/v1",
		Mode:                 mode,
		InBridge:             mode == "bridge",
		Models:               servedModels(d.reg),
		ModelCount:           len(servedModels(d.reg)),
		SafeMode:             cfg.SafeMode,
		MaxMessagesPerDay:    cfg.MaxMessagesPerDay,
		TransientRetries:     ps.TransientRetries,
		FingerprintRotations: ps.FingerprintRotations,
		BridgeTokens:         d.pool.BridgeCount(),
		IsDefaultAdminToken:  cfg.IsDefaultAdminToken(),
	}
	for _, t := range ps.Tokens {
		od.Tokens = append(od.Tokens, cardFromSnapshot(t))
	}
	// Regression guard (#200): df7a16a dropped this line, leaving
	// has_tokens permanently false so pooled operators saw "No upstream
	// tokens configured" on Overview while the Tokens tab worked.
	od.HasTokens = len(od.Tokens) > 0
	// Bridge token cards (#187): live snapshots of bridge-mode entries.
	if od.InBridge {
		for _, snap := range d.pool.BridgeSnapshot() {
			od.BridgeTokenCards = append(od.BridgeTokenCards, bridgeCardFromSnapshot(snap))
		}
	}
	od.UpstreamSync = parseUpstreamSync(upstreamDriftJSON)
	return od
}
