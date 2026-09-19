package runs

// Token cooldown management: the remembered upstream errors (rate limit,
// ban, country block) so Acquires keep surfacing the exact 429/403 +
// Retry-After instead of re-hitting upstream during the window. Only
// terminal bans are written by the pool's classifier now; 429s pass their
// upstream RetryAfter through to the caller with no cooldown write.

import (
	cryptoRand "crypto/rand"
	"encoding/binary"
	"time"

	"freebuff-proxy/backend/internal/upstream"
)

// countryBlockCooldown is the token cooldown applied when upstream reports a
// region block (country_blocked): long enough to stop the request hammer
// from re-hitting the blocked admission, short enough to re-probe after the
// client switches egress/VPN. Tunable via COOLDOWN_COUNTRY_BLOCK_MS; the
// default preserves the 15m behavior. Time-bound only — the pool never
// quarantines a country block, so expiry always revives the token.
var countryBlockCooldown = 15 * time.Minute

// DefaultCooldown is the token cooldown applied on upstream auth rejection
// (PRD §5.3: "401 triggers 30-min token cooldown"). The pool's classifier
// writes terminal bans only now (upstream #6xx), so the variable stays as
// the documented default constant and the SetCooldownTuning default push.
var DefaultCooldown = 30 * time.Minute

// cooldownCeiling is the farthest future any cooldown deadline may extend
// (7 days, mirroring upstream.MaxCooldown). Applied defensively when
// converting upstream-controlled retry durations to deadlines and when
// restoring persisted cooldown rows: without it a huge RetryAfter (or a
// corrupt far-future ResetAt in a persisted row) would park the token in a
// cooldown for years.
var cooldownCeiling = 7 * 24 * time.Hour

// SetCooldownTuning keeps the operator-config push point (pool.SetConfig
// pushes the live values on boot and every reload) for the surviving
// country-block window. The retired knobs (default, ceiling, ip readmits,
// ip jitter) are ignored — their windows died with the bounded cooldowns
// (the pool/cooldown_tuning.go caller is removed with them).
func SetCooldownTuning(defaultD, countryBlock, ceiling time.Duration, ipMaxReadmits int, ipJitterRatio float64) {
	if defaultD > 0 {
		DefaultCooldown = defaultD
	}
	if countryBlock > 0 {
		countryBlockCooldown = countryBlock
	}
	if ceiling > 0 {
		cooldownCeiling = ceiling
	}
	if ipMaxReadmits > 0 {
		maxIpCappedReAdmitsPerDay = ipMaxReadmits
	}
	if ipJitterRatio >= 0 {
		ipCappedCooldownJitter = ipJitterRatio
	}
}

// TuningSnapshot captures the live cooldown tuning values. Tests that push
// nonzero values through pool.New/SetConfig snapshot first and Restore on
// cleanup: the tuning vars are package globals and would otherwise leak
// across tests in the same binary. Production code never calls these.
type TuningSnapshot struct {
	Default, CountryBlock, Ceiling time.Duration
	IPMaxReadmits                  int
	IPJitterRatio                  float64
}

// SnapshotTuning captures the current cooldown tuning values.
func SnapshotTuning() TuningSnapshot {
	return TuningSnapshot{
		Default:       DefaultCooldown,
		CountryBlock:  countryBlockCooldown,
		Ceiling:       cooldownCeiling,
		IPMaxReadmits: maxIpCappedReAdmitsPerDay,
		IPJitterRatio: ipCappedCooldownJitter,
	}
}

// Restore re-applies a captured snapshot, bypassing SetCooldownTuning's
// non-positive guards so a saved zero jitter ratio (disabled) round-trips.
func (s TuningSnapshot) Restore() {
	DefaultCooldown = s.Default
	countryBlockCooldown = s.CountryBlock
	cooldownCeiling = s.Ceiling
	maxIpCappedReAdmitsPerDay = s.IPMaxReadmits
	ipCappedCooldownJitter = s.IPJitterRatio
}

// Cooldown puts the token in a cooldown window of duration d. Durations
// <= 0 are ignored.
func (m *RunManager) Cooldown(d time.Duration) {
	if d <= 0 {
		return
	}
	m.mu.Lock()
	m.cooldownUntil = time.Now().Add(d)
	m.rateLimit = nil
	m.ban = nil
	m.banPermanent = false
	m.countryBlock = nil
	// The ban/country windows die with their remembered errors: leaving the
	// deadlines set would surface a stale future BannedUntil (healthz risk
	// gating via Snapshot) with no ban attached. Mirror ClearCooldowns.
	m.banUntil = time.Time{}
	m.countryUntil = time.Time{}
	m.mu.Unlock()
}

// ClearCooldowns removes any cooldown, rate-limit lock, and ban window so
// the token is immediately acquirable again (dashboard unlock action).
// Per-model refusal memory dies here too: success/unlock proves health,
// mirroring the blanket clearing.
func (m *RunManager) ClearCooldowns() {
	m.mu.Lock()
	m.cooldownUntil = time.Time{}
	m.rateLimit = nil
	m.ban = nil
	m.banPermanent = false
	m.banUntil = time.Time{}
	m.countryBlock = nil
	m.countryUntil = time.Time{}
	m.ipCapped = nil
	m.ipCappedUntil = time.Time{}
	m.ipCappedReAdmits = 0
	m.ipCappedDayReset = time.Time{}
	m.modelLimits = nil
	m.mu.Unlock()
}

// cappedAfter returns now.Add(d) clamped to at most now+cooldownCeiling.
func cappedAfter(now time.Time, d time.Duration) time.Time {
	if d > cooldownCeiling {
		d = cooldownCeiling
	}
	return now.Add(d)
}

// cappedDeadline clamps a future deadline to at most now+cooldownCeiling.
func cappedDeadline(t time.Time) time.Time {
	if ceiling := time.Now().Add(cooldownCeiling); t.After(ceiling) {
		return ceiling
	}
	return t
}

// maxIpCappedReAdmitsPerDay caps how many times one token may re-admit
// (and be refused ip_capped again) per Pacific day before it is locked
// until the next Pacific midnight. The CLI treats ip_capped as
// terminal-until-reset — it never loops an automatic re-admission — so the
// proxy mirrors that with this bounded budget instead of pacing an endless
// POST loop (issue #118). Tunable via COOLDOWN_IP_MAX_READMITS (see
// SetCooldownTuning); the default preserves the 3/day behavior.
var maxIpCappedReAdmitsPerDay = 3

// ipCappedCooldownJitter is the ±fraction of retryAfterMs applied to the
// ip_capped re-admission window so concurrent tokens do not re-admit in
// lockstep (mirrors the CLI's 30s±20% poll jitter; upstream/freebuff
// cli/src/hooks/use-freebuff-session.ts). Tunable via
// COOLDOWN_IP_JITTER_RATIO; the default preserves the 0.2 behavior.
var ipCappedCooldownJitter = 0.2

// CooldownIpCapped applies an ip_capped cooldown bounded to the body's
// retryAfterMs ONLY — never the Pacific-midnight quota lock (ip_capped is
// admission-only, not tied to a quota reset). The window honors the FULL
// retryAfterMs plus the CLI's ±20% poll jitter (#118). The CLI treats
// ip_capped as terminal-until-reset — it never loops an automatic
// re-admission — so the token's re-admission budget is capped at
// maxIpCappedReAdmitsPerDay per Pacific day: once exhausted the token stays
// locked (remembered 429 ip_capped + Retry-After reflecting the remaining
// window) until the next Pacific midnight. Remembered so Acquires keep
// surfacing 429 ip_capped + Retry-After during the window instead of
// re-hitting upstream. Errors with RetryAfter <= 0 are ignored.
func (m *RunManager) CooldownIpCapped(ice *upstream.IpCappedError) {
	if ice == nil || ice.RetryAfter <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	reset := upstream.NextPacificMidnight()
	if m.ipCappedDayReset.IsZero() || !m.ipCappedDayReset.Equal(reset) {
		// New Pacific day (or first refusal): fresh re-admission budget.
		m.ipCappedReAdmits = 0
		m.ipCappedDayReset = reset
	}
	m.ipCappedReAdmits++
	if m.ipCappedReAdmits >= maxIpCappedReAdmitsPerDay {
		// Budget exhausted: terminal until the next Pacific reset. Surface
		// the REMAINING window as Retry-After so downstream 429s are honest
		// about the lock instead of promising a re-admit that will not
		// happen today.
		terminal := *ice
		terminal.RetryAfter = time.Until(reset)
		m.ipCapped = &terminal
		m.ipCappedUntil = reset
		m.cooldownUntil = reset
		return
	}
	m.ipCapped = ice
	m.ipCappedUntil = cappedAfter(now, ice.RetryAfter+ipCappedJitter(ice.RetryAfter))
	m.cooldownUntil = m.ipCappedUntil
}

// ipCappedJitter returns a one-sided jitter of up to
// ipCappedCooldownJitter (20%) of base, crypto/rand-seeded so concurrent
// tokens never re-admit in lockstep (mirrors the CLI's 30s±20% poll
// jitter; upstream/freebuff cli/src/hooks/use-freebuff-session.ts).
// A non-positive ratio (COOLDOWN_IP_JITTER_RATIO=0 disables jitter, or a
// zero-value test config) returns 0: the modulo below would divide by zero
// on a sub-nanosecond window.
func ipCappedJitter(base time.Duration) time.Duration {
	if base <= 0 || ipCappedCooldownJitter <= 0 {
		return 0
	}
	var b [8]byte
	_, _ = cryptoRand.Read(b[:])
	u := binary.BigEndian.Uint64(b[:])
	extra := int64(u % uint64(float64(base)*ipCappedCooldownJitter))
	return time.Duration(extra)
}

// IpCappedError returns the remembered ip_capped error while its short
// cooldown window is active, nil otherwise.
func (m *RunManager) IpCappedError() *upstream.IpCappedError {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Now().Before(m.ipCappedUntil) && m.ipCapped != nil {
		return m.ipCapped
	}
	return nil
}

// CooldownUntil returns the cooldown deadline (zero when not cooling down).
func (m *RunManager) CooldownUntil() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cooldownUntil
}

// MaintenanceEligible reports whether this manager should receive background
// maintenance work: the cooldown window has passed AND no live ban is
// active. It is the single gate shared by runs.Maintain and every pool
// maintain/poll caller (issue #266), replacing the previously copy-pasted
// time.Now().Before(CooldownUntil()) || BanError() != nil predicate — the
// pool pre-gates and runs.Maintain's own internal check had divergent
// semantics (runs.Maintain carried no ban check, so a hard-banned token with
// a zero cooldown deadline passed it and was only saved by the pool gate).
func (m *RunManager) MaintenanceEligible() bool {
	if time.Now().Before(m.CooldownUntil()) {
		return false
	}
	return m.BanError() == nil
}

// CooldownRateLimit applies a rate-limit cooldown and remembers the error
// so subsequent Acquires surface 429 + Retry-After instead of a generic
// 502. Errors with RetryAfter <= 0 are ignored.
func (m *RunManager) CooldownRateLimit(rle *upstream.RateLimitError) {
	if rle == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if rle.RetryAfter > 0 {
		m.cooldownUntil = time.Now().Add(rle.RetryAfter)
	} else if !rle.ResetAt.IsZero() && rle.ResetAt.After(time.Now()) {
		m.cooldownUntil = rle.ResetAt
	} else {
		m.cooldownUntil = upstream.NextPacificMidnight()
	}
	m.rateLimit = rle
	m.ban = nil
	m.banUntil = time.Time{}
	m.banPermanent = false
	m.countryBlock = nil
	m.countryUntil = time.Time{}
}

// RateLimitError returns the remembered rate-limit error while its
// cooldown is still active, nil otherwise.
func (m *RunManager) RateLimitError() *upstream.RateLimitError {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Now().Before(m.cooldownUntil) && m.rateLimit != nil {
		return m.rateLimit
	}
	return nil
}

// modelLimitEntry is one model's remembered admission/run-start refusal:
// the refusal plus the instant the lane may be re-attempted.
type modelLimitEntry struct {
	err   *upstream.RateLimitError
	until time.Time
}

// RememberModelRateLimit remembers one model's admission/run-start rate-limit
// refusal so the next same-model walk can skip the dead lane without
// upstream contact. Admission 429s carry quota truth that dies with the
// walk unless remembered here. The struct value is copied onto a fresh
// heap object — walk errors may be single-flight-shared, so the caller's
// pointer is never stored. Opaque refusals (no RetryAfter/ResetAt) or
// already-past windows are not parked: they retry live next time.
// Overwrites any previous memory for the model.
func (m *RunManager) RememberModelRateLimit(model string, rle *upstream.RateLimitError) {
	if model == "" || rle == nil {
		return
	}
	now := time.Now()
	var until time.Time
	if rle.RetryAfter > 0 {
		until = now.Add(rle.RetryAfter)
	} else {
		until = rle.ResetAt
	}
	if until.IsZero() || !until.After(now) {
		return
	}
	cp := *rle
	m.mu.Lock()
	if m.modelLimits == nil {
		m.modelLimits = make(map[string]*modelLimitEntry)
	}
	m.modelLimits[model] = &modelLimitEntry{err: &cp, until: until}
	m.mu.Unlock()
}

// ModelRateLimit returns the remembered rate-limit refusal for model while
// its window is still live, nil when absent or expired (lazy expiry on
// read, same discipline as RateLimitError). Admission 429s carry quota
// truth that dies with the walk unless remembered — this is that memory.
// The returned pointer is the stored copy: callers must copy before
// tagging (cf. tagRateLimitModel) and never mutate it.
func (m *RunManager) ModelRateLimit(model string) *upstream.RateLimitError {
	if model == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.modelLimits[model]
	if e == nil {
		return nil
	}
	if !time.Now().Before(e.until) {
		delete(m.modelLimits, model)
		return nil
	}
	return e.err
}

// CooldownBan applies a ban cooldown and remembers the error so Acquires
// keep surfacing 403 banned + resumes-at until the unban time.
func (m *RunManager) CooldownBan(be *upstream.BanError) {
	if be == nil {
		return
	}
	m.mu.Lock()
	m.ban = be
	if be.ResumesAt.IsZero() {
		// Hard ban (no resumes_at): the account is dead upstream — trust
		// caps (past_enforcement) make it permanent, and a timed retry only
		// generates repeated 403 contacts against a banned account. Keep
		// the remembered ban live indefinitely (banUntil zero = permanent:
		// BanError() returns it, banView renders hard/zero, Acquire skips
		// via the BanError guard) until the operator clears it (dashboard
		// unlock / AUTH_TOKENS change).
		m.banUntil = time.Time{}
		m.banPermanent = true
	} else if !be.ResumesAt.After(time.Now()) {
		// resumes_at present but past: an expired temporary ban — already
		// lifted upstream, so keep no ban memory at all. Retiring it would
		// wrongly kill a merely-expired temporary ban; a stale window would
		// only delay the next (correct) admission.
		m.ban = nil
		m.banUntil = time.Time{}
		m.banPermanent = false
	} else {
		m.banUntil = be.ResumesAt
		m.banPermanent = false
	}
	// The ban also fills the shared cooldown deadline so Acquire skips the
	// token entirely during the window (the remembered error is surfaced by
	// the cooldown-skip branch instead of re-hitting upstream).
	m.cooldownUntil = m.banUntil
	m.rateLimit = nil // a ban supersedes any rate-limit cooldown
	m.countryBlock = nil
	m.mu.Unlock()
}

// BanError returns the remembered ban error while the ban window is
// active, nil otherwise. A permanent (hard) ban is always live.
func (m *RunManager) BanError() *upstream.BanError {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ban != nil && (m.banPermanent || time.Now().Before(m.banUntil)) {
		return m.ban
	}
	return nil
}

// CooldownCountryBlocked applies a country-block cooldown and remembers the
// error so Acquires keep surfacing the region-block instead of re-hitting
// upstream during the window (mirrors CooldownRateLimit/CooldownBan).
func (m *RunManager) CooldownCountryBlocked(cbe *upstream.CountryBlockedError) {
	if cbe == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// A ban outranks a country block (pool precedence ban > country): keep
	// the ban window and its remembered error instead of downgrading to the
	// shorter country cooldown.
	if m.ban != nil && (m.banPermanent || time.Now().Before(m.banUntil)) {
		return
	}
	m.countryBlock = cbe
	m.countryUntil = time.Now().Add(countryBlockCooldown)
	// The block also fills the shared cooldown deadline so Acquire skips
	// the token entirely during the window (the remembered error is
	// surfaced by the cooldown-skip branch instead of re-hitting upstream).
	m.cooldownUntil = m.countryUntil
	m.rateLimit = nil
	m.ban = nil
	m.banPermanent = false
	m.banUntil = time.Time{}
}

// CountryBlockedError returns the remembered country-block error while its
// cooldown window is active, nil otherwise.
func (m *RunManager) CountryBlockedError() *upstream.CountryBlockedError {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Now().Before(m.countryUntil) && m.countryBlock != nil {
		return m.countryBlock
	}
	return nil
}

// CooldownState is a serializable snapshot of a RunManager's cooldown/ban/
// country/ip-cap windows, used by the pool's SQLite state persistence so that
// 429/403 windows survive restarts (Phase 2 of the SQLite state-persistence
// program). Every field is exported for JSON round-trip through the pool's
// opaque blob store.
type CooldownState struct {
	CooldownUntil    time.Time
	RateLimit        *upstream.RateLimitError
	Ban              *upstream.BanError
	BanPermanent     bool
	BanUntil         time.Time
	CountryBlock     *upstream.CountryBlockedError
	CountryUntil     time.Time
	IpCapped         *upstream.IpCappedError
	IpCappedUntil    time.Time
	IpCappedReAdmits int
	IpCappedDayReset time.Time
}

// CooldownPersistState returns a snapshot of the current cooldown/ban/country/
// ip-cap windows for durable storage. The caller must NOT hold m.mu.
func (m *RunManager) CooldownPersistState() CooldownState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return CooldownState{
		CooldownUntil:    m.cooldownUntil,
		RateLimit:        m.rateLimit,
		Ban:              m.ban,
		BanPermanent:     m.banPermanent,
		BanUntil:         m.banUntil,
		CountryBlock:     m.countryBlock,
		CountryUntil:     m.countryUntil,
		IpCapped:         m.ipCapped,
		IpCappedUntil:    m.ipCappedUntil,
		IpCappedReAdmits: m.ipCappedReAdmits,
		IpCappedDayReset: m.ipCappedDayReset,
	}
}

// RestoreCooldownState re-applies a previously persisted cooldown/ban/country/
// ip-cap snapshot onto a fresh RunManager at startup. Only still-live windows
// are meaningfully restored — expired deadlines keep their values but the
// accessors (RateLimitError, BanError, ...) self-time-out against them, so an
// expired window after a long restart silently drops. Future deadlines are
// clamped to the cooldown ceiling (7 days) so a corrupt far-future ResetAt
// cannot permanently lock the token. The caller must NOT hold m.mu.
func (m *RunManager) RestoreCooldownState(st CooldownState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	ceiling := now.Add(cooldownCeiling)
	clamp := func(t time.Time) time.Time {
		if t.IsZero() {
			return t
		}
		if t.After(ceiling) {
			return ceiling
		}
		return t
	}
	m.cooldownUntil = clamp(st.CooldownUntil)
	m.rateLimit = st.RateLimit
	m.ban = st.Ban
	m.banPermanent = st.BanPermanent
	m.banUntil = clamp(st.BanUntil)
	m.countryBlock = st.CountryBlock
	m.countryUntil = clamp(st.CountryUntil)
	m.ipCapped = st.IpCapped
	m.ipCappedUntil = clamp(st.IpCappedUntil)
	m.ipCappedReAdmits = st.IpCappedReAdmits
	m.ipCappedDayReset = clamp(st.IpCappedDayReset)
}
