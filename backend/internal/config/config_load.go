package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LoadOptions configures LoadOpts. DiscoverCLIToken, when non-nil, sources
// an empty AUTH_TOKENS pool from the official CLI login files (issue #283);
// the cmd entrypoint wires clicreds.DiscoverToken here. A nil value keeps
// Load product-agnostic: the config package never reads product-specific
// credential files.
type LoadOptions struct {
	DiscoverCLIToken func() (token, email, path string, ok bool)
	// Overlay is the DB settings overlay (ADR-0019): canonical KEY -> raw
	// VALUE pairs applied after the .env file and before the process
	// environment, so UI-persisted knobs beat the file without rewriting
	// it while explicit process env keeps winning. Since the env-to-DB
	// migration every catalog key is overlay-addressable, secrets included
	// (the DB file holds them at mode 0600), except the env-only keys
	// (SettingsBlockedKeys: SESSION_STATE_FILE, SESSION_PERSIST, LOG_FILE,
	// HTTP_READ_TIMEOUT, AUTO_DISCOVER_TOKEN), whose rows are inert and
	// filtered before they apply; AUTH_TOKENS applies with
	// presence semantics (an empty row pins bridge mode and suppresses CLI
	// auto-discovery, mirroring the .env tier). Nil or empty behaves like
	// Load.
	Overlay map[string]string
}

// Load resolves configuration from the optional JSON file at configPath
// ("" skips the file), the optional ./.env file (when present), and
// environment overrides, then validates it. Precedence, lowest to highest:
// built-in defaults < JSON file (-config) < ./.env < real environment
// (.env is an environment file, so it follows the README rule that the
// environment overrides the JSON config). Load never performs CLI credential
// auto-discovery (issue #283): use LoadOpts with DiscoverCLIToken to source
// an empty AUTH_TOKENS pool from the official CLI login files.
func Load(configPath string) (Config, error) {
	return LoadOpts(configPath, LoadOptions{})
}

// LoadOpts is Load with additional load-time options (issue #283).
func LoadOpts(configPath string, opts LoadOptions) (Config, error) {
	raw, err := loadRaw(configPath)
	if err != nil {
		return Config{}, err
	}
	envFileUsed := ResolveEnvFile()
	if err := applyDotenv(&raw, envFileUsed); err != nil {
		return Config{}, err
	}
	// DB settings overlay (ADR-0019): beats the file, loses to explicit
	// process env (applied below).
	applySettingsOverlay(&raw, opts.Overlay)

	overrideString(&raw.ListenAddr, "LISTEN_ADDR")
	overrideString(&raw.UpstreamBaseURL, "UPSTREAM_BASE_URL")
	// AUTH_TOKENS is presence-sensitive: an empty value in the real
	// environment is an explicit bridge-mode choice (systemd/Docker unit
	// files set AUTH_TOKENS= to force bridge mode). Unlike other keys, an
	// empty value must not be skipped — it records presence so CLI
	// auto-discovery cannot refill the pool, mirroring applyDotenv's
	// AUTH_TOKENS handling for .env. When the variable is absent, the
	// JSON/.env value (if any) stands unchanged.
	if v, ok := os.LookupEnv("AUTH_TOKENS"); ok {
		raw.AuthTokens = splitList(v)
		raw.AuthTokensSet = true
	}
	overrideString(&raw.RotationInterval, "ROTATION_INTERVAL")
	overrideString(&raw.RequestTimeout, "REQUEST_TIMEOUT")
	overrideString(&raw.HTTPReadTimeout, "HTTP_READ_TIMEOUT")
	overrideString(&raw.SessionCallTimeout, "SESSION_CALL_TIMEOUT")
	overrideCSV(&raw.APIKeys, "API_KEYS")
	overrideString(&raw.AdminToken, "ADMIN_TOKEN")
	overrideString(&raw.CostMode, "COST_MODE")
	// ACTING_USER_ID / legacy USER_ID (#126): the alias is read from the
	// SAME env source as the primary, so a real-environment USER_ID beats a
	// lower-precedence .env/JSON ACTING_USER_ID instead of being silently
	// dropped; ACTING_USER_ID wins when both are set in one source.
	overrideStringAlias(&raw.ActingUserID, os.Getenv, "ACTING_USER_ID", "USER_ID")
	overrideString(&raw.TLSFingerprint, "TLS_FINGERPRINT")
	overrideString(&raw.RegistryRefresh, "REGISTRY_REFRESH")
	overrideBool(&raw.DebugDump, "DEBUG_DUMP")
	overrideBool(&raw.DevToolsEnabled, "DEVTOOLS_ENABLED")
	overrideString(&raw.LogFile, "LOG_FILE")
	overrideString(&raw.LogLevel, "LOG_LEVEL")
	overrideString(&raw.LogFormat, "LOG_FORMAT")
	overrideBool(&raw.LogAccess, "LOG_ACCESS")
	overrideBool(&raw.BridgeEnabled, "BRIDGE_ENABLED")
	overrideString(&raw.BridgeIdleEvict, "BRIDGE_IDLE_EVICT")
	overrideString(&raw.IdleRotationTimeout, "IDLE_ROTATION_TIMEOUT")
	overrideBool(&raw.SafeMode, "SAFE_MODE")
	overrideBool(&raw.ModelsHideUnavailable, "MODELS_HIDE_UNAVAILABLE")
	overrideString((*string)(&raw.ModelsAllow), "MODELS_ALLOW")
	overrideString(&raw.CORSAllowedOrigin, "CORS_ALLOWED_ORIGIN")
	overrideString(&raw.RequestJitter, "REQUEST_JITTER")
	overrideString(&raw.CLIVersion, "CLI_VERSION")
	overrideInt(&raw.TransientRetries, "TRANSIENT_RETRIES")
	overrideBool(&raw.SessionPersist, "SESSION_PERSIST")
	overrideString(&raw.SessionStateFile, "SESSION_STATE_FILE")
	overrideBool(&raw.HTTP2Upstream, "HTTP2_UPSTREAM")
	overrideInt(&raw.RunFinishQueueSize, "RUN_FINISH_QUEUE_SIZE")
	overrideString(&raw.RunFinishInlineTimeout, "RUN_FINISH_INLINE_TIMEOUT")
	overrideInt(&raw.RunsDrainQueueCap, "RUNS_DRAIN_QUEUE_CAP")
	overrideString(&raw.RunsDrainTTL, "RUNS_DRAIN_TTL")
	overrideString(&raw.SessionReAdmitLead, "SESSION_RE_ADMIT_LEAD")
	overrideString(&raw.SessionProbeCacheTTL, "SESSION_PROBE_CACHE_TTL")
	overrideString(&raw.ModelUnavailableCacheTTL, "MODEL_UNAVAILABLE_CACHE_TTL")
	overrideString(&raw.WebhookURL, "WEBHOOK_URL")
	overrideBool(&raw.AdoptCLISession, "ADOPT_CLI_SESSION")
	overrideInt(&raw.SlotsPerAccount, "SLOTS_PER_ACCOUNT")
	overrideString(&raw.QueueWait, "QUEUE_WAIT")
	overrideInt(&raw.QueueDepth, "QUEUE_DEPTH")
	overrideInt(&raw.MaxSpillAccounts, "MAX_SPILL_ACCOUNTS")
	// Cooldown / session-park tuning overrides (our lineage): integer
	// millisecond knobs, zero-tolerant in Load via msVal.
	overrideInt(&raw.CooldownDefaultMs, "COOLDOWN_DEFAULT_MS")
	overrideInt(&raw.CooldownCountryBlockMs, "COOLDOWN_COUNTRY_BLOCK_MS")
	overrideInt(&raw.CooldownCeilingMs, "COOLDOWN_CEILING_MS")
	overrideInt(&raw.CooldownFanoutMs, "COOLDOWN_FANOUT_MS")
	overrideInt(&raw.CooldownInvalidModelMs, "COOLDOWN_INVALID_MODEL_MS")
	overrideInt(&raw.CooldownOpaqueMs, "COOLDOWN_OPAQUE_MS")
	overrideInt(&raw.CooldownLoadShedMs, "COOLDOWN_LOADSHED_MS")
	overrideInt(&raw.CooldownPeakHoursMs, "COOLDOWN_PEAK_HOURS_MS")
	overrideInt(&raw.CooldownIPMaxReadmits, "COOLDOWN_IP_MAX_READMITS")
	overrideFloat(&raw.CooldownIPJitterRatio, "COOLDOWN_IP_JITTER_RATIO")
	overrideBool(&raw.SessionParkEnabled, "SESSION_PARK_ENABLED")
	overrideInt(&raw.SessionParkThresholdMs, "SESSION_PARK_THRESHOLD_MS")
	overrideInt(&raw.SessionPollMaxMs, "SESSION_POLL_MAX_MS")
	overrideInt(&raw.SmartProbeBackoffMaxMs, "SMART_PROBE_BACKOFF_MAX_MS")
	overrideInt(&raw.MaturityBackoffMs, "MATURITY_BACKOFF_MS")
	overrideBool(&raw.WaitingRoomChain, "WAITING_ROOM_CHAIN")
	overrideFloat(&raw.RateLimitPerIP, "RATE_LIMIT_PER_IP")
	overrideInt(&raw.RateLimitBurst, "RATE_LIMIT_BURST")
	overrideFloat(&raw.BridgeRateLimitPerToken, "BRIDGE_RATE_LIMIT_PER_TOKEN")
	overrideInt(&raw.BridgeCircuitBreakerFailures, "BRIDGE_CIRCUIT_BREAKER_FAILURES")
	overrideString(&raw.BridgeCircuitBreakerWindow, "BRIDGE_CIRCUIT_BREAKER_WINDOW")
	overrideString(&raw.BridgeCircuitBreakerCooldown, "BRIDGE_CIRCUIT_BREAKER_COOLDOWN")
	overrideString(&raw.TokenRotation, "TOKEN_ROTATION")
	overrideBoolPtr(&raw.RateLimitFailover, "RATE_LIMIT_FAILOVER")
	overrideString(&raw.ModelLocks, "MODEL_LOCKS")
	overrideString(&raw.PinModel, "PIN_MODEL")
	overrideBool(&raw.DashboardEnabled, "DASHBOARD_ENABLED")
	overrideBool(&raw.AutoRotateOnExhaustion, "AUTO_ROTATE_ON_EXHAUSTION")
	overrideString(&raw.ExhaustionWarningThreshold, "EXHAUSTION_WARNING_THRESHOLD")
	overrideBool(&raw.HealthScoreEnabled, "HEALTH_SCORE_ENABLED")
	overrideBool(&raw.TokenHealthProbes, "TOKEN_HEALTH_PROBES")
	overrideString(&raw.TokenProbeInterval, "TOKEN_PROBE_INTERVAL")
	overrideBool(&raw.DashboardRequireLogin, "DASHBOARD_REQUIRE_LOGIN")
	overrideBool(&raw.MaturityEnabled, "MATURITY_ENABLED")
	overrideString(&raw.MaturityTouchModel, "MATURITY_TOUCH_MODEL")
	overrideInt(&raw.MaturityTargetDays, "MATURITY_TARGET_DAYS")
	overrideBool(&raw.SmartProbeEnabled, "SMART_PROBE_ENABLED")
	overrideString(&raw.SmartProbeBackoffMax, "SMART_PROBE_BACKOFF_MAX")
	// Convert feature-translation modes (issue #277): COMPRESS_PROMPT,
	// CACHE_CONTROL_INJECTION and REASONING_IN_CONTENT are resolved once
	// here (so the dashboard config form and /admin/reload swaps apply) and
	// handed to convert.Options at request time.
	overrideString(&raw.CompressPrompt, "COMPRESS_PROMPT")
	overrideString(&raw.CacheControlInjection, "CACHE_CONTROL_INJECTION")
	overrideString(&raw.ReasoningInContent, "REASONING_IN_CONTENT")

	parseDuration := func(raw, name string) (time.Duration, error) {
		d, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return 0, fmt.Errorf("parse %s: %w", name, err)
		}
		return d, nil
	}

	rotationInterval, err := parseDuration(raw.RotationInterval, "ROTATION_INTERVAL")
	if err != nil {
		return Config{}, err
	}
	requestTimeout, err := parseDuration(raw.RequestTimeout, "REQUEST_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	sessionCallTimeout, err := parseDuration(raw.SessionCallTimeout, "SESSION_CALL_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	httpReadTimeout, err := parseDuration(raw.HTTPReadTimeout, "HTTP_READ_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	registryRefresh, err := parseDuration(raw.RegistryRefresh, "REGISTRY_REFRESH")
	if err != nil {
		return Config{}, err
	}
	// RUN_FINISH_INLINE_TIMEOUT / RUNS_DRAIN_TTL / SESSION_RE_ADMIT_LEAD /
	// SESSION_PROBE_CACHE_TTL / MODEL_UNAVAILABLE_CACHE_TTL are zero-tolerant
	// durations: "" or "0" fall back to the documented default (a zero inline
	// timeout would make the inline fallback useless; a zero re-admit lead
	// would spin a re-admit on every request).
	runFinishInlineTimeout := 250 * time.Millisecond
	if v := strings.TrimSpace(raw.RunFinishInlineTimeout); v != "" {
		runFinishInlineTimeout, err = parseDuration(v, "RUN_FINISH_INLINE_TIMEOUT")
		if err != nil {
			return Config{}, err
		}
		if runFinishInlineTimeout <= 0 {
			runFinishInlineTimeout = 250 * time.Millisecond
		}
	}
	runsDrainTTL := 10 * time.Minute
	if v := strings.TrimSpace(raw.RunsDrainTTL); v != "" {
		runsDrainTTL, err = parseDuration(v, "RUNS_DRAIN_TTL")
		if err != nil {
			return Config{}, err
		}
		if runsDrainTTL <= 0 {
			runsDrainTTL = 10 * time.Minute
		}
	}
	sessionReAdmitLead := 60 * time.Second
	if v := strings.TrimSpace(raw.SessionReAdmitLead); v != "" {
		sessionReAdmitLead, err = parseDuration(v, "SESSION_RE_ADMIT_LEAD")
		if err != nil {
			return Config{}, err
		}
		if sessionReAdmitLead <= 0 {
			sessionReAdmitLead = 60 * time.Second
		}
	}
	sessionProbeCacheTTL := 15 * time.Second
	if v := strings.TrimSpace(raw.SessionProbeCacheTTL); v != "" {
		sessionProbeCacheTTL, err = parseDuration(v, "SESSION_PROBE_CACHE_TTL")
		if err != nil {
			return Config{}, err
		}
		if sessionProbeCacheTTL <= 0 {
			sessionProbeCacheTTL = 15 * time.Second
		}
	}
	// MODEL_UNAVAILABLE_CACHE_TTL is zero-tolerant like the other session
	// knobs: "" or "0" fall back to the documented 1h default.
	modelUnavailableCacheTTL := time.Hour
	if v := strings.TrimSpace(raw.ModelUnavailableCacheTTL); v != "" {
		modelUnavailableCacheTTL, err = parseDuration(v, "MODEL_UNAVAILABLE_CACHE_TTL")
		if err != nil {
			return Config{}, err
		}
		if modelUnavailableCacheTTL <= 0 {
			modelUnavailableCacheTTL = time.Hour
		}
	}
	// msVal resolves one integer-millisecond knob (COOLDOWN_*_MS /
	// SESSION_*_MS / SMART_PROBE_BACKOFF_MAX_MS / MATURITY_BACKOFF_MS)
	// with msToDuration (cooldown.go): nil or non-positive values fall
	// back to the Contract defaults, so a blank row can never zero-out a
	// backoff. Absurd values saturate instead of wrapping (consumers clamp
	// to the ceiling at use); negative readmits/jitter are rejected in
	// Validate.
	msVal := func(raw *int, fallback int) time.Duration {
		if raw == nil {
			return msToDuration(0, fallback)
		}
		return msToDuration(*raw, fallback)
	}
	cooldownDefault := msVal(raw.CooldownDefaultMs, defaultCooldownDefaultMs)
	cooldownCountryBlock := msVal(raw.CooldownCountryBlockMs, defaultCooldownCountryBlockMs)
	cooldownCeiling := msVal(raw.CooldownCeilingMs, defaultCooldownCeilingMs)
	cooldownFanout := msVal(raw.CooldownFanoutMs, defaultCooldownFanoutMs)
	cooldownInvalidModel := msVal(raw.CooldownInvalidModelMs, defaultCooldownInvalidModelMs)
	cooldownOpaque := msVal(raw.CooldownOpaqueMs, defaultCooldownOpaqueMs)
	cooldownLoadShed := msVal(raw.CooldownLoadShedMs, defaultCooldownLoadShedMs)
	cooldownPeakHours := msVal(raw.CooldownPeakHoursMs, defaultCooldownPeakHoursMs)
	sessionParkThreshold := msVal(raw.SessionParkThresholdMs, defaultSessionParkThresholdMs)
	sessionPollMax := msVal(raw.SessionPollMaxMs, defaultSessionPollMaxMs)
	maturityBackoff := msVal(raw.MaturityBackoffMs, defaultMaturityBackoffMs)
	// Zero readmits falls back to the default like the ms knobs above (an
	// explicit 0 can never mean "no re-admits": runs would still enforce
	// the old global). A negative value passes through so Validate
	// rejects it.
	cooldownIPMaxReadmits := defaultCooldownIPMaxReadmits
	if raw.CooldownIPMaxReadmits != nil && *raw.CooldownIPMaxReadmits != 0 {
		cooldownIPMaxReadmits = *raw.CooldownIPMaxReadmits
	}
	cooldownIPJitterRatio := defaultCooldownIPJitterRatio
	if raw.CooldownIPJitterRatio != nil {
		cooldownIPJitterRatio = *raw.CooldownIPJitterRatio
	}
	// SMART_PROBE_BACKOFF_MAX is zero-tolerant like the other session
	// knobs: "" or "0" fall back to the documented 30m default (a zero cap
	// would pin every probe-429 retry to immediacy, defeating the backoff).
	// SMART_PROBE_BACKOFF_MAX_MS (our lineage, integer milliseconds) is the
	// fallback source when the string knob is unset — both spellings feed
	// the same Config field so either knob stays live.
	smartProbeBackoffMax := 30 * time.Minute
	if v := strings.TrimSpace(raw.SmartProbeBackoffMax); v != "" {
		smartProbeBackoffMax, err = parseDuration(v, "SMART_PROBE_BACKOFF_MAX")
		if err != nil {
			return Config{}, err
		}
		if smartProbeBackoffMax <= 0 {
			smartProbeBackoffMax = 30 * time.Minute
		}
	} else if raw.SmartProbeBackoffMaxMs != nil {
		smartProbeBackoffMax = msVal(raw.SmartProbeBackoffMaxMs, defaultSmartProbeBackoffMaxMs)
	}
	runFinishQueueSize := 64
	if raw.RunFinishQueueSize != nil {
		runFinishQueueSize = *raw.RunFinishQueueSize
	}
	runsDrainQueueCap := 64
	if raw.RunsDrainQueueCap != nil {
		runsDrainQueueCap = *raw.RunsDrainQueueCap
	}
	// IDLE_ROTATION_TIMEOUT is zero-tolerant: "" or "0" both mean disabled.
	// idleRotationSet distinguishes "explicitly disabled" from "not
	// configured" so the SafeMode preset only fills truly unset knobs.
	idleRotationSet := strings.TrimSpace(raw.IdleRotationTimeout) != ""
	idleRotationTimeout := time.Duration(0)
	if idleRotationSet && strings.TrimSpace(raw.IdleRotationTimeout) != "0" {
		idleRotationTimeout, err = parseDuration(raw.IdleRotationTimeout, "IDLE_ROTATION_TIMEOUT")
		if err != nil {
			return Config{}, err
		}
	}
	// SESSION_IDLE_END is zero-tolerant like IDLE_ROTATION_TIMEOUT: "" or "0"
	// both mean disabled (opt-in knob — ending a session costs a fresh
	// daily-slot admission when traffic resumes).
	sessionIdleEnd := time.Duration(0)
	if strings.TrimSpace(raw.SessionIdleEnd) != "" && strings.TrimSpace(raw.SessionIdleEnd) != "0" {
		sessionIdleEnd, err = parseDuration(raw.SessionIdleEnd, "SESSION_IDLE_END")
		if err != nil {
			return Config{}, err
		}
	}
	requestJitterSet := strings.TrimSpace(raw.RequestJitter) != ""
	requestJitter := time.Duration(0)
	if requestJitterSet {
		requestJitter, err = parseDuration(raw.RequestJitter, "REQUEST_JITTER")
		if err != nil {
			return Config{}, err
		}
	}
	upstreamBaseURL, err := normalizeUpstreamBaseURL(raw.UpstreamBaseURL)
	if err != nil {
		return Config{}, err
	}

	// BRIDGE_IDLE_EVICT is zero-tolerant: "" or "0" fall back to the 72h
	// default (a zero TTL would evict every bridge entry on the first idle
	// pass, defeating the cache).
	bridgeIdleEvict := 72 * time.Hour
	if v := strings.TrimSpace(raw.BridgeIdleEvict); v != "" {
		bridgeIdleEvict, err = parseDuration(v, "BRIDGE_IDLE_EVICT")
		if err != nil {
			return Config{}, err
		}
		if bridgeIdleEvict <= 0 {
			bridgeIdleEvict = 72 * time.Hour
		}
	}

	// TRANSIENT_RETRIES: nil defaults to 1 (one additional attempt after a
	// transient transport failure); an explicit 0 disables retries.
	transientRetries := 1
	if raw.TransientRetries != nil {
		transientRetries = *raw.TransientRetries
	}

	// RATE_LIMIT_PER_IP / RATE_LIMIT_BURST (issue #137): per-source-IP rate
	// limiter to protect upstream from bursts and spam. 0 = disabled.
	rateLimitPerIP := 0.0
	if raw.RateLimitPerIP != nil {
		rateLimitPerIP = *raw.RateLimitPerIP
	}
	rateLimitBurst := 0
	if raw.RateLimitBurst != nil {
		rateLimitBurst = *raw.RateLimitBurst
	}
	// MAX_SPEND_PER_DAY (issue #122): ADVISORY per-token Pacific-day spend
	// ceiling in ledger units. 0 (default) = unlimited; never enforced —
	// surfaced as SpendLimit/SpendPct on /healthz only.
	maxSpendPerDay := int64(0)
	if raw.MaxSpendPerDay != nil {
		maxSpendPerDay = int64(*raw.MaxSpendPerDay)
	}
	// BRIDGE_RATE_LIMIT_PER_TOKEN (security hardening): per-client-token
	// rate limit in req/s for bridge mode. 0 = unlimited. Independent of
	// the per-IP rate limiter.
	bridgeRateLimitPerToken := 0.0
	if raw.BridgeRateLimitPerToken != nil {
		bridgeRateLimitPerToken = *raw.BridgeRateLimitPerToken
	}
	// BRIDGE_CIRCUIT_BREAKER_FAILURES: 0 = disabled. When >0, a burst of
	// transient upstream 5xx/network failures within the sliding window
	// opens the breaker, short-circuiting bridge admission to 503 for the
	// cooldown period. Classified errors (auth/rate-limit/ban/country/ip_capped)
	// never trip it.
	bridgeCircuitBreakerFailures := 0
	if raw.BridgeCircuitBreakerFailures != nil {
		bridgeCircuitBreakerFailures = *raw.BridgeCircuitBreakerFailures
	}
	bridgeCircuitBreakerWindow := 30 * time.Second
	if v := strings.TrimSpace(raw.BridgeCircuitBreakerWindow); v != "" {
		bridgeCircuitBreakerWindow, err = parseDuration(v, "BRIDGE_CIRCUIT_BREAKER_WINDOW")
		if err != nil {
			return Config{}, err
		}
		if bridgeCircuitBreakerWindow <= 0 {
			bridgeCircuitBreakerWindow = 30 * time.Second
		}
	}
	bridgeCircuitBreakerCooldown := 10 * time.Second
	if v := strings.TrimSpace(raw.BridgeCircuitBreakerCooldown); v != "" {
		bridgeCircuitBreakerCooldown, err = parseDuration(v, "BRIDGE_CIRCUIT_BREAKER_COOLDOWN")
		if err != nil {
			return Config{}, err
		}
		if bridgeCircuitBreakerCooldown <= 0 {
			bridgeCircuitBreakerCooldown = 10 * time.Second
		}
	}
	// EXHAUSTION_WARNING_THRESHOLD: how far ahead to predict quota exhaustion.
	// "0" or "" disables predictive warnings (default 10m).
	exhaustionWarningThreshold := 10 * time.Minute
	if v := strings.TrimSpace(raw.ExhaustionWarningThreshold); v != "" && v != "0" {
		exhaustionWarningThreshold, err = parseDuration(v, "EXHAUSTION_WARNING_THRESHOLD")
		if err != nil {
			return Config{}, err
		}
	}
	// HEALTH_SCORE_ENABLED: defaults to true (health score on by default).
	healthScoreEnabled := true
	// TOKEN_PROBE_INTERVAL: how often to probe each token. Default 30m.
	tokenProbeInterval := 30 * time.Minute
	if v := strings.TrimSpace(raw.TokenProbeInterval); v != "" && v != "0" {
		tokenProbeInterval, err = parseDuration(v, "TOKEN_PROBE_INTERVAL")
		if err != nil {
			return Config{}, err
		}
		if tokenProbeInterval <= 0 {
			tokenProbeInterval = 30 * time.Minute
		}
	}

	// Model fallback and the log-surface knobs are excised: saved
	// MODEL_ALIASES / FALLBACK_AFTER_MS / FALLBACK_MODEL /
	// QUOTA_FALLBACK_MODELS / LOG_RING_SIZE / LOG_CONSOLE_WINDOW /
	// LOG_TABLE_RETENTION values are tolerated as unknown keys and ignored.

	// Backward-compat (#126): a JSON config carrying the pre-rename USER_ID
	// key still works when no ACTING_USER_ID source (env/.env/JSON) set a
	// value. Weakest source — env and .env override it via the aliases above.
	if raw.ActingUserID == "" {
		raw.ActingUserID = raw.LegacyActingUserID
	}

	// LOG_FORMAT default: empty means the text format (the historic output).
	logFormat := strings.TrimSpace(raw.LogFormat)
	if logFormat == "" {
		logFormat = "text"
	}

	dashboardRequireLogin := raw.DashboardRequireLogin
	adminToken := strings.TrimSpace(raw.AdminToken)
	if !dashboardRequireLogin || strings.EqualFold(adminToken, "none") || strings.EqualFold(adminToken, "off") || strings.EqualFold(adminToken, "false") {
		dashboardRequireLogin = false
		adminToken = ""
	} else if adminToken == "" {
		adminToken = DefaultAdminToken
	}

	pinModel, err := parsePinModel(raw.PinModel)
	if err != nil {
		return Config{}, err
	}
	// SLOTS_PER_ACCOUNT defaults to 3: a ~5h live run at 3 turns per
	// account-model lane drew no upstream flag (2026-09-22), so 3 ships and
	// the old 2 is the conservative posture (1 = bunker, fully sequential).
	// 0 = unlimited: no live-turn slot gating applies at all. Negative
	// values floor to 0 instead of failing the load.
	slotsPerAccount := 3
	if raw.SlotsPerAccount != nil {
		slotsPerAccount = *raw.SlotsPerAccount
	}
	if slotsPerAccount < 0 {
		slotsPerAccount = 0
	}
	tokenRotation := strings.ToLower(strings.TrimSpace(raw.TokenRotation))
	switch tokenRotation {
	case "", "drain":
		tokenRotation = "drain"
	case "round_robin", "roundrobin", "rr":
		tokenRotation = "round_robin"
	case "least_used", "leastused":
		tokenRotation = "least_used"
	case "random", "rand":
		tokenRotation = "random"
	default:
		return Config{}, fmt.Errorf("invalid TOKEN_ROTATION: %q (must be drain, round_robin, least_used, or random)", raw.TokenRotation)
	}

	modelLocks, err := parseModelLocks(raw.ModelLocks)
	if err != nil {
		return Config{}, err
	}
	// MATURITY_TARGET_DAYS defaults to 7 (one full streak interval); an
	// explicit value is range-checked in Validate (1..28).
	maturityTargetDays := 7
	if raw.MaturityTargetDays != nil {
		maturityTargetDays = *raw.MaturityTargetDays
	}
	// MATURITY_TOUCH_MODEL defaults to "" (= auto): the cheapest served
	// unmetered row per token. "auto" is accepted as an explicit alias;
	// an explicit provider/model id overrides auto.
	maturityTouchModel := strings.TrimSpace(raw.MaturityTouchModel)
	// Probe cadences are zero-tolerant: "" falls back to
	// the documented default, and an explicit non-positive value falls back
	// the same way (a zero cadence would probe on every tick).
	quotaProbeActiveInterval := 60 * time.Second
	if v := strings.TrimSpace(raw.QuotaProbeActiveInterval); v != "" {
		quotaProbeActiveInterval, err = parseDuration(v, "QUOTA_PROBE_ACTIVE_INTERVAL")
		if err != nil {
			return Config{}, err
		}
		if quotaProbeActiveInterval <= 0 {
			quotaProbeActiveInterval = 60 * time.Second
		}
	}
	quotaProbeIdleHeartbeat := 30 * time.Minute
	if v := strings.TrimSpace(raw.QuotaProbeIdleHeartbeat); v != "" {
		quotaProbeIdleHeartbeat, err = parseDuration(v, "QUOTA_PROBE_IDLE_HEARTBEAT")
		if err != nil {
			return Config{}, err
		}
		if quotaProbeIdleHeartbeat <= 0 {
			quotaProbeIdleHeartbeat = 30 * time.Minute
		}
	}
	// TOKEN_MAX_CONCURRENT defaults to 2 (the approved anti-ban pacing).
	// 0 = unlimited: no live-turn slot gating applies at all. Negative
	// values floor to 0 instead of failing the load.
	tokenMaxConcurrent := 2
	if raw.TokenMaxConcurrent != nil {
		tokenMaxConcurrent = *raw.TokenMaxConcurrent
	}
	if tokenMaxConcurrent < 0 {
		tokenMaxConcurrent = 0
	}
	// QUEUE_WAIT is zero-tolerant: "" falls back to the
	// 30s default, and an explicit non-positive value falls back the same
	// way (a zero wait would never park, defeating the FIFO queue).
	queueWait := 30 * time.Second
	if v := strings.TrimSpace(raw.QueueWait); v != "" {
		queueWait, err = parseDuration(v, "QUEUE_WAIT")
		if err != nil {
			return Config{}, err
		}
		if queueWait <= 0 {
			queueWait = 30 * time.Second
		}
	}
	// QUEUE_DEPTH defaults to 16; 0 disables queueing (fail over at once
	// when no live-turn slot is free); negative is rejected in Validate.
	queueDepth := 16
	if raw.QueueDepth != nil {
		queueDepth = *raw.QueueDepth
	}
	// MAX_SPILL_ACCOUNTS defaults to 0 (unbounded spill chain); negative
	// is rejected in Validate.
	maxSpillAccounts := 0
	if raw.MaxSpillAccounts != nil {
		maxSpillAccounts = *raw.MaxSpillAccounts
	}
	cfg := Config{
		ListenAddr:                   strings.TrimSpace(raw.ListenAddr),
		UpstreamBaseURL:              upstreamBaseURL,
		AuthTokens:                   dedupeStrings(raw.AuthTokens),
		RotationInterval:             rotationInterval,
		RequestTimeout:               requestTimeout,
		HTTPReadTimeout:              httpReadTimeout,
		SessionCallTimeout:           sessionCallTimeout,
		TokenRotation:                tokenRotation,
		ModelLocks:                   modelLocks,
		PinModel:                     pinModel,
		APIKeys:                      dedupeStrings(raw.APIKeys),
		AdminToken:                   adminToken,
		DashboardRequireLogin:        dashboardRequireLogin,
		HTTP2Upstream:                raw.HTTP2Upstream,
		CostMode:                     strings.TrimSpace(raw.CostMode),
		ActingUserID:                 strings.TrimSpace(raw.ActingUserID),
		TLSFingerprint:               strings.TrimSpace(raw.TLSFingerprint),
		RegistryRefresh:              registryRefresh,
		DebugDump:                    raw.DebugDump,
		DevToolsEnabled:              raw.DevToolsEnabled,
		LogFile:                      strings.TrimSpace(raw.LogFile),
		LogLevel:                     strings.TrimSpace(raw.LogLevel),
		LogFormat:                    logFormat,
		LogAccess:                    raw.LogAccess,
		BridgeEnabled:                raw.BridgeEnabled,
		BridgeIdleEvict:              bridgeIdleEvict,
		IdleRotationTimeout:          idleRotationTimeout,
		SessionIdleEnd:               sessionIdleEnd,
		SafeMode:                     raw.SafeMode,
		ModelsHideUnavailable:        raw.ModelsHideUnavailable,
		ModelsAllow:                  splitList(string(raw.ModelsAllow)),
		CORSAllowedOrigin:            strings.TrimSpace(raw.CORSAllowedOrigin),
		RequestJitter:                requestJitter,
		CLIVersion:                   strings.TrimSpace(raw.CLIVersion),
		TransientRetries:             transientRetries,
		SessionPersist:               raw.SessionPersist,
		SessionStateFile:             strings.TrimSpace(raw.SessionStateFile),
		RunFinishQueueSize:           runFinishQueueSize,
		RunFinishInlineTimeout:       runFinishInlineTimeout,
		RunsDrainQueueCap:            runsDrainQueueCap,
		RunsDrainTTL:                 runsDrainTTL,
		SessionReAdmitLead:           sessionReAdmitLead,
		SessionProbeCacheTTL:         sessionProbeCacheTTL,
		ModelUnavailableCacheTTL:     modelUnavailableCacheTTL,
		WebhookURL:                   strings.TrimSpace(raw.WebhookURL),
		AdoptCLISession:              raw.AdoptCLISession,
		MaturityEnabled:              raw.MaturityEnabled,
		MaturityTouchModel:           maturityTouchModel,
		MaturityTargetDays:           maturityTargetDays,
		QuotaAutoProbe:               raw.QuotaAutoProbe,
		QuotaProbeActiveInterval:     quotaProbeActiveInterval,
		QuotaProbeIdleHeartbeat:      quotaProbeIdleHeartbeat,
		RoutingSmart:                 raw.RoutingSmart,
		TokenMaxConcurrent:           tokenMaxConcurrent,
		SlotsPerAccount:              slotsPerAccount,
		QueueWait:                    queueWait,
		QueueDepth:                   queueDepth,
		MaxSpillAccounts:             maxSpillAccounts,
		WaitingRoomChain:             raw.WaitingRoomChain,
		RateLimitPerIP:               rateLimitPerIP,
		RateLimitBurst:               rateLimitBurst,
		DashboardEnabled:             raw.DashboardEnabled,
		EnvFile:                      envFileUsed,
		CompressPrompt:               parseCompressPrompt(raw.CompressPrompt),
		CacheControlInjection:        parseCacheControlInjection(raw.CacheControlInjection),
		ReasoningInContent:           parseReasoningInContent(raw.ReasoningInContent),
		RateLimitFailover:            raw.RateLimitFailover == nil || *raw.RateLimitFailover,
		MaxSpendPerDay:               maxSpendPerDay,
		BridgeRateLimitPerToken:      bridgeRateLimitPerToken,
		BridgeCircuitBreakerFailures: bridgeCircuitBreakerFailures,
		BridgeCircuitBreakerWindow:   bridgeCircuitBreakerWindow,
		BridgeCircuitBreakerCooldown: bridgeCircuitBreakerCooldown,
		AutoRotateOnExhaustion:       raw.AutoRotateOnExhaustion,
		ExhaustionWarningThreshold:   exhaustionWarningThreshold,
		HealthScoreEnabled:           healthScoreEnabled,
		TokenHealthProbes:            raw.TokenHealthProbes,
		TokenProbeInterval:           tokenProbeInterval,
		SmartProbeEnabled:            raw.SmartProbeEnabled,
		SmartProbeBackoffMax:         smartProbeBackoffMax,
		CooldownDefault:              cooldownDefault,
		CooldownCountryBlock:         cooldownCountryBlock,
		CooldownCeiling:              cooldownCeiling,
		CooldownFanout:               cooldownFanout,
		CooldownInvalidModel:         cooldownInvalidModel,
		CooldownOpaque:               cooldownOpaque,
		CooldownLoadShed:             cooldownLoadShed,
		CooldownPeakHours:            cooldownPeakHours,
		CooldownIPMaxReadmits:        cooldownIPMaxReadmits,
		CooldownIPJitterRatio:        cooldownIPJitterRatio,
		SessionParkEnabledFlag:       raw.SessionParkEnabled,
		SessionParkThreshold:         sessionParkThreshold,
		SessionPollMax:               sessionPollMax,
		MaturityBackoff:              maturityBackoff,
	}
	// Auto-discover CLI token if a discovery hook was wired (LoadOpts,
	// issue #283) AND no AUTH_TOKENS were explicitly configured AND
	// AUTO_DISCOVER_TOKEN is not disabled. ADOPT_CLI_SESSION (issue #97)
	// also opts into discovery: the operator explicitly asked to run like
	// the CLI, so AUTO_DISCOVER_TOKEN=false must not silently leave the
	// pool empty.
	// AUTO_DISCOVER_TOKEN is env-only (data-architecture decision): the
	// process environment alone decides, defaulting to true when unset. A
	// DB overlay row is inert (SettingsBlockedKeys) — behavior change from
	// the migrated era, when the overlay applied beneath the environment.
	// It records on the config so the dashboard and the env-to-DB migration
	// export the effective value instead of hardcoding it.
	autoDiscover := true
	if v, ok := os.LookupEnv("AUTO_DISCOVER_TOKEN"); ok {
		autoDiscover = !isFalseWord(v)
	}
	cfg.AutoDiscoverToken = autoDiscover
	if opts.DiscoverCLIToken != nil {
		if (autoDiscover || cfg.AdoptCLISession) && len(cfg.AuthTokens) == 0 && !raw.AuthTokensSet {
			if token, email, srcPath, ok := opts.DiscoverCLIToken(); ok {
				cfg.AuthTokens = []string{token}
				cfg.DiscoveredSource = srcPath
				cfg.DiscoveredEmail = email
				// An operator running without AUTH_TOKENS intends bridge
				// mode; auto-discovery silently flipping to pooled mode is
				// surprising, so warn loudly and name the off switch.
				slog.Warn("auto-discovery filled empty AUTH_TOKENS from CLI login: bridge mode switched to pooled mode",
					"file", srcPath,
					"email", email,
					"hint", "set AUTO_DISCOVER_TOKEN=false to disable auto-discovery")
			}
		}
	}

	// SafeMode presets: when SAFE_MODE=true, apply recommended defaults for
	// account-safety knobs that were NOT explicitly configured. Explicit
	// "0"/disabled values always win (IDLE_ROTATION_TIMEOUT=0 or
	// REQUEST_JITTER=0 stay disabled).
	if cfg.SafeMode {
		if !idleRotationSet && cfg.IdleRotationTimeout == 0 {
			cfg.IdleRotationTimeout = 30 * time.Minute
		}
		if !requestJitterSet && cfg.RequestJitter == 0 {
			cfg.RequestJitter = 200 * time.Millisecond
		}
		// TLS is CLI-faithful by default (plain Go/Bun baseline, no browser
		// JA3 spoofing). Browser-evasion (TLS_FINGERPRINT=auto/chrome...) is
		// opt-in for datacenter WAF evasion, not CLI parity.
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func loadRaw(configPath string) (rawConfig, error) {
	cfg := defaultRawConfig()

	if configPath != "" {
		path, err := filepath.Abs(configPath)
		if err != nil {
			return rawConfig{}, fmt.Errorf("resolve config path: %w", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return rawConfig{}, fmt.Errorf("read config file: %w", err)
		}
		// Strip a leading UTF-8 BOM (Windows editors/PowerShell writers add
		// one) or json.Unmarshal fails with "invalid character '\ufeff'".
		// Every other file reader in the package (discoverCLIToken,
		// parseDotenv) already strips it; this was the missed case (B3).
		data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
		if err := json.Unmarshal(data, &cfg); err != nil {
			return rawConfig{}, fmt.Errorf("parse config file: %w", err)
		}
		// A non-nil AuthTokens after unmarshal means the JSON key was present
		// ([] is an explicit empty list; absent leaves it nil).
		cfg.AuthTokensSet = cfg.AuthTokens != nil
	}

	return cfg, nil
}

// applyDotenv overlays KEY=VALUE pairs from the resolved .env file (when
// present) onto raw, so a local .env works like the JSON config file. path
// is the resolved env file ("" = none found, from ResolveEnvFile). A
// missing file is fine; any other read error fails the load. Real
// environment variables are applied afterwards and therefore always win.
func applyDotenv(raw *rawConfig, path string) error {
	if path == "" {
		return nil
	}
	vals, err := readDotenv(path)
	if err != nil || vals == nil {
		return err
	}
	get := func(name string) string { return vals[name] }
	// An empty AUTH_TOKENS= line in .env is an explicit bridge-mode choice
	// (the dashboard mode switch persists exactly this): record presence so
	// auto-discovery cannot refill it, AND clear whatever the JSON config
	// provided (the empty value must beat the JSON list). Unlike other keys,
	// AUTH_TOKENS must NOT skip empty overrides.
	if v, ok := vals["AUTH_TOKENS"]; ok {
		raw.AuthTokens = splitList(v)
		raw.AuthTokensSet = true
	}
	applyMappedValues(raw, get)
	return nil
}

// applyMappedValues overlays KEY=VALUE pairs from get onto raw. It is the
// single key list shared by applyDotenv (the .env file tier) and
// applySettingsOverlay (the DB overlay tier, ADR-0019), so every key the
// loader parses is overlay-addressable by construction — a new knob lands
// here once and both tiers learn it (pinned by
// TestCatalogCoversApplyDotenvKeys on the dotenv side and
// TestOverlayCoversCatalog on the overlay side).
func applyMappedValues(raw *rawConfig, get func(string) string) {
	overrideStringFrom(&raw.ListenAddr, get, "LISTEN_ADDR")
	overrideStringFrom(&raw.UpstreamBaseURL, get, "UPSTREAM_BASE_URL")
	overrideStringFrom(&raw.RotationInterval, get, "ROTATION_INTERVAL")
	overrideStringFrom(&raw.RequestTimeout, get, "REQUEST_TIMEOUT")
	overrideStringFrom(&raw.HTTPReadTimeout, get, "HTTP_READ_TIMEOUT")
	overrideStringFrom(&raw.SessionCallTimeout, get, "SESSION_CALL_TIMEOUT")
	overrideStringFrom(&raw.TokenRotation, get, "TOKEN_ROTATION")
	overrideBoolPtrFrom(&raw.RateLimitFailover, get, "RATE_LIMIT_FAILOVER")
	overrideStringFrom(&raw.ModelLocks, get, "MODEL_LOCKS")
	overrideStringFrom(&raw.PinModel, get, "PIN_MODEL")
	overrideCSVFrom(&raw.APIKeys, get, "API_KEYS")
	overrideStringFrom(&raw.AdminToken, get, "ADMIN_TOKEN")
	overrideStringFrom(&raw.CostMode, get, "COST_MODE")
	// A .env ACTING_USER_ID beats a JSON ACTING_USER_ID (dotenv outranks
	// JSON); ACTING_USER_ID wins when both are in the .env.
	overrideStringAlias(&raw.ActingUserID, get, "ACTING_USER_ID", "USER_ID")
	overrideStringFrom(&raw.TLSFingerprint, get, "TLS_FINGERPRINT")
	overrideStringFrom(&raw.RegistryRefresh, get, "REGISTRY_REFRESH")
	overrideBoolFrom(&raw.DebugDump, get, "DEBUG_DUMP")
	overrideBoolFrom(&raw.DevToolsEnabled, get, "DEVTOOLS_ENABLED")
	overrideStringFrom(&raw.LogFile, get, "LOG_FILE")
	overrideStringFrom(&raw.LogLevel, get, "LOG_LEVEL")
	overrideStringFrom(&raw.LogFormat, get, "LOG_FORMAT")
	overrideBoolFrom(&raw.LogAccess, get, "LOG_ACCESS")
	overrideBoolFrom(&raw.BridgeEnabled, get, "BRIDGE_ENABLED")
	overrideStringFrom(&raw.BridgeIdleEvict, get, "BRIDGE_IDLE_EVICT")
	overrideStringFrom(&raw.IdleRotationTimeout, get, "IDLE_ROTATION_TIMEOUT")
	overrideStringFrom(&raw.SessionIdleEnd, get, "SESSION_IDLE_END")
	// The remaining keys mirror the real-environment override set in Load.
	// AUTO_DISCOVER_TOKEN is intentionally env-only (it controls the .env
	// read itself, so honoring it from .env would be circular).
	overrideBoolFrom(&raw.SafeMode, get, "SAFE_MODE")
	overrideBoolFrom(&raw.ModelsHideUnavailable, get, "MODELS_HIDE_UNAVAILABLE")
	overrideStringFrom((*string)(&raw.ModelsAllow), get, "MODELS_ALLOW")
	overrideStringFrom(&raw.CORSAllowedOrigin, get, "CORS_ALLOWED_ORIGIN")
	overrideStringFrom(&raw.RequestJitter, get, "REQUEST_JITTER")
	overrideStringFrom(&raw.CLIVersion, get, "CLI_VERSION")
	overrideIntFrom(&raw.TransientRetries, get, "TRANSIENT_RETRIES")
	overrideBoolFrom(&raw.SessionPersist, get, "SESSION_PERSIST")
	overrideStringFrom(&raw.SessionStateFile, get, "SESSION_STATE_FILE")
	overrideBoolFrom(&raw.HTTP2Upstream, get, "HTTP2_UPSTREAM")
	overrideIntFrom(&raw.RunFinishQueueSize, get, "RUN_FINISH_QUEUE_SIZE")
	overrideStringFrom(&raw.RunFinishInlineTimeout, get, "RUN_FINISH_INLINE_TIMEOUT")
	overrideIntFrom(&raw.RunsDrainQueueCap, get, "RUNS_DRAIN_QUEUE_CAP")
	overrideStringFrom(&raw.RunsDrainTTL, get, "RUNS_DRAIN_TTL")
	overrideStringFrom(&raw.SessionReAdmitLead, get, "SESSION_RE_ADMIT_LEAD")
	overrideStringFrom(&raw.SessionProbeCacheTTL, get, "SESSION_PROBE_CACHE_TTL")
	overrideStringFrom(&raw.ModelUnavailableCacheTTL, get, "MODEL_UNAVAILABLE_CACHE_TTL")
	overrideStringFrom(&raw.WebhookURL, get, "WEBHOOK_URL")
	overrideBoolFrom(&raw.AdoptCLISession, get, "ADOPT_CLI_SESSION")
	overrideIntFrom(&raw.SlotsPerAccount, get, "SLOTS_PER_ACCOUNT")
	overrideStringFrom(&raw.QueueWait, get, "QUEUE_WAIT")
	overrideIntFrom(&raw.QueueDepth, get, "QUEUE_DEPTH")
	overrideIntFrom(&raw.MaxSpillAccounts, get, "MAX_SPILL_ACCOUNTS")
	overrideBoolFrom(&raw.QuotaAutoProbe, get, "QUOTA_AUTO_PROBE")
	overrideStringFrom(&raw.QuotaProbeActiveInterval, get, "QUOTA_PROBE_ACTIVE_INTERVAL")
	overrideStringFrom(&raw.QuotaProbeIdleHeartbeat, get, "QUOTA_PROBE_IDLE_HEARTBEAT")
	overrideBoolFrom(&raw.RoutingSmart, get, "ROUTING_SMART")
	overrideIntFrom(&raw.TokenMaxConcurrent, get, "TOKEN_MAX_CONCURRENT")
	overrideIntFrom(&raw.CooldownDefaultMs, get, "COOLDOWN_DEFAULT_MS")
	overrideIntFrom(&raw.CooldownCountryBlockMs, get, "COOLDOWN_COUNTRY_BLOCK_MS")
	overrideIntFrom(&raw.CooldownCeilingMs, get, "COOLDOWN_CEILING_MS")
	overrideIntFrom(&raw.CooldownFanoutMs, get, "COOLDOWN_FANOUT_MS")
	overrideIntFrom(&raw.CooldownInvalidModelMs, get, "COOLDOWN_INVALID_MODEL_MS")
	overrideIntFrom(&raw.CooldownOpaqueMs, get, "COOLDOWN_OPAQUE_MS")
	overrideIntFrom(&raw.CooldownLoadShedMs, get, "COOLDOWN_LOADSHED_MS")
	overrideIntFrom(&raw.CooldownPeakHoursMs, get, "COOLDOWN_PEAK_HOURS_MS")
	overrideIntFrom(&raw.CooldownIPMaxReadmits, get, "COOLDOWN_IP_MAX_READMITS")
	overrideFloatFrom(&raw.CooldownIPJitterRatio, get, "COOLDOWN_IP_JITTER_RATIO")
	overrideBoolFrom(&raw.SessionParkEnabled, get, "SESSION_PARK_ENABLED")
	overrideIntFrom(&raw.SessionParkThresholdMs, get, "SESSION_PARK_THRESHOLD_MS")
	overrideIntFrom(&raw.SessionPollMaxMs, get, "SESSION_POLL_MAX_MS")
	overrideIntFrom(&raw.SmartProbeBackoffMaxMs, get, "SMART_PROBE_BACKOFF_MAX_MS")
	overrideIntFrom(&raw.MaturityBackoffMs, get, "MATURITY_BACKOFF_MS")
	overrideBoolFrom(&raw.WaitingRoomChain, get, "WAITING_ROOM_CHAIN")
	overrideFloatFrom(&raw.RateLimitPerIP, get, "RATE_LIMIT_PER_IP")
	overrideIntFrom(&raw.RateLimitBurst, get, "RATE_LIMIT_BURST")
	overrideBoolFrom(&raw.DashboardEnabled, get, "DASHBOARD_ENABLED")
	overrideBoolFrom(&raw.DashboardRequireLogin, get, "DASHBOARD_REQUIRE_LOGIN")
	overrideBoolFrom(&raw.MaturityEnabled, get, "MATURITY_ENABLED")
	overrideStringFrom(&raw.MaturityTouchModel, get, "MATURITY_TOUCH_MODEL")
	overrideIntFrom(&raw.MaturityTargetDays, get, "MATURITY_TARGET_DAYS")
	overrideBoolFrom(&raw.SmartProbeEnabled, get, "SMART_PROBE_ENABLED")
	overrideStringFrom(&raw.SmartProbeBackoffMax, get, "SMART_PROBE_BACKOFF_MAX")
	// Convert feature-translation modes (issue #277), mirroring Load.
	overrideStringFrom(&raw.CompressPrompt, get, "COMPRESS_PROMPT")
	overrideStringFrom(&raw.CacheControlInjection, get, "CACHE_CONTROL_INJECTION")
	overrideStringFrom(&raw.ReasoningInContent, get, "REASONING_IN_CONTENT")
}

// override applies envName from get to target through parse. An unset or
// unparseable value leaves the file/default value untouched, so a single
// generic helper replaces the five typed override methods (issue #282).
func override[T any](target *T, get func(string) string, envName string, parse func(string) (T, bool)) {
	if value := strings.TrimSpace(get(envName)); value != "" {
		if parsed, ok := parse(value); ok {
			*target = parsed
		}
	}
}

// parseString returns the trimmed value unchanged (used by overrideString).
func parseString(s string) (string, bool) { return s, true }

// parseBool accepts "1"/"true"/"yes"/"on" and "0"/"false"/"no"/"off".
func parseBool(s string) (bool, bool) {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// isFalseWord reports whether s disables a flag in the AUTO_DISCOVER_TOKEN
// convention: "false"/"0"/"off"/"no" (case-insensitive, trimmed). Anything
// else — including blank — leaves the flag enabled, matching the loader's
// long-standing reading of the variable.
func isFalseWord(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "false", "0", "off", "no":
		return true
	}
	return false
}

// parseCSV splits a comma-separated value via splitList.
func parseCSV(s string) ([]string, bool) { return splitList(s), true }

// parseIntPtr parses an int; blank or unparseable values yield ok=false.
func parseIntPtr(s string) (*int, bool) {
	if parsed, err := strconv.Atoi(s); err == nil {
		return &parsed, true
	}
	return nil, false
}

// parseFloatPtr parses a float64; blank or unparseable values yield ok=false.
func parseFloatPtr(s string) (*float64, bool) {
	if parsed, err := strconv.ParseFloat(s, 64); err == nil {
		return &parsed, true
	}
	return nil, false
}

// parseCompressPrompt reports whether optional prompt & context compression
// is enabled (COMPRESS_PROMPT=true, default off), matching the convert
// package's historical env semantics (issue #277).
func parseCompressPrompt(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// parseCacheControlInjection reports whether DeepSeek prompt-cache
// cache_control injection is enabled (CACHE_CONTROL_INJECTION, default on;
// false disables), matching the convert package's default-on semantics.
func parseCacheControlInjection(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "0", "false", "off", "no", "disabled":
		return false
	}
	return true
}

// parseReasoningInContent returns the think-tag label used to fold reasoning
// into message content (REASONING_IN_CONTENT, default "" = off); an explicit
// tag word ("thinking") is returned verbatim, lowercased.
func parseReasoningInContent(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "", "0", "false", "off", "no", "disabled":
		return ""
	case "1", "true", "yes", "on":
		return "think"
	}
	return v
}

// overrideString sets target from a string env var.
func overrideString(target *string, envName string) {
	override(target, os.Getenv, envName, parseString)
}

func overrideStringFrom(target *string, get func(string) string, envName string) {
	override(target, get, envName, parseString)
}

// overrideStringAlias overrides target from source get, preferring the
// primary env name and falling back to the legacy alias name when the
// primary is empty at THIS source. Both names are read from the same
// source, so cross-source precedence (JSON < .env < env) holds for either
// name (#126): a higher-precedence USER_ID beats a lower-precedence
// ACTING_USER_ID instead of being silently dropped, while ACTING_USER_ID
// wins when both appear in one source.
func overrideStringAlias(target *string, get func(string) string, primary, alias string) {
	value := strings.TrimSpace(get(primary))
	if value == "" {
		value = strings.TrimSpace(get(alias))
	}
	if value != "" {
		*target = value
	}
}

// overrideCSV sets target from a comma-separated env var.
func overrideCSV(target *[]string, envName string) {
	override(target, os.Getenv, envName, parseCSV)
}

func overrideCSVFrom(target *[]string, get func(string) string, envName string) {
	override(target, get, envName, parseCSV)
}

// overrideBool sets target from DEBUG_DUMP-style env vars; unset or
// unrecognized values leave the file/default value untouched.
func overrideBool(target *bool, envName string) {
	override(target, os.Getenv, envName, parseBool)
}

func overrideBoolFrom(target *bool, get func(string) string, envName string) {
	override(target, get, envName, parseBool)
}

// overrideBoolPtr sets target from *bool env vars ("1"/"true" → true,
// "0"/"false" → false); unset or unrecognized values leave the
// file/default value untouched (nil keeps the load-time default).
func overrideBoolPtr(target **bool, envName string) {
	override(target, os.Getenv, envName, parseBoolPtr)
}

func overrideBoolPtrFrom(target **bool, get func(string) string, envName string) {
	override(target, get, envName, parseBoolPtr)
}

func parseBoolPtr(s string) (*bool, bool) {
	b, ok := parseBool(s)
	if !ok {
		return nil, false
	}
	return new(b), true
}

// overrideInt sets target from int env vars; unset or
// unparseable values leave the file/default value untouched.
func overrideInt(target **int, envName string) {
	override(target, os.Getenv, envName, parseIntPtr)
}

func overrideIntFrom(target **int, get func(string) string, envName string) {
	override(target, get, envName, parseIntPtr)
}

// overrideFloat sets target from RATE_LIMIT_PER_IP-style env vars; unset or
// unparseable values leave the file/default value untouched.
func overrideFloat(target **float64, envName string) {
	override(target, os.Getenv, envName, parseFloatPtr)
}

func overrideFloatFrom(target **float64, get func(string) string, envName string) {
	override(target, get, envName, parseFloatPtr)
}
