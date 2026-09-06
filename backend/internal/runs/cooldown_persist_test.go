package runs

import (
	"encoding/json"
	"testing"
	"time"

	"freebuff-proxy/backend/internal/upstream"
)

func TestCooldownStateRoundTripAuth(t *testing.T) {
	m := NewRunManager(nil, nil, time.Hour)
	m.Cooldown(DefaultCooldown)
	st := m.CooldownPersistState()
	if st.CooldownUntil.IsZero() {
		t.Fatal("auth cooldown not captured in CooldownPersistState")
	}

	// Restore into a fresh manager.
	m2 := NewRunManager(nil, nil, time.Hour)
	m2.RestoreCooldownState(st)
	if m2.CooldownUntil().IsZero() || !m2.CooldownUntil().Equal(st.CooldownUntil) {
		t.Errorf("auth cooldown not restored: got %v, want %v", m2.CooldownUntil(), st.CooldownUntil)
	}
}

func TestCooldownStateRoundTripRateLimit(t *testing.T) {
	m := NewRunManager(nil, nil, time.Hour)
	rle := &upstream.RateLimitError{Status: "rate_limited", RetryAfter: 10 * time.Minute, Limit: 5, RecentCount: 5}
	m.CooldownRateLimit(rle)
	st := m.CooldownPersistState()
	if st.RateLimit == nil || st.RateLimit.RetryAfter != rle.RetryAfter {
		t.Fatalf("rate-limit not captured: %+v", st.RateLimit)
	}

	// JSON round-trip (pool stores as opaque blob).
	data, _ := json.Marshal(st)
	var st2 CooldownState
	if err := json.Unmarshal(data, &st2); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}

	m2 := NewRunManager(nil, nil, time.Hour)
	m2.RestoreCooldownState(st2)
	got := m2.RateLimitError()
	if got == nil || got.RetryAfter != rle.RetryAfter {
		t.Errorf("rate-limit not restored: %+v (want RetryAfter=%v)", got, rle.RetryAfter)
	}
}

func TestCooldownStateRoundTripIpCapped(t *testing.T) {
	m := NewRunManager(nil, nil, time.Hour)
	ice := &upstream.IpCappedError{ActiveUsersForIP: 3, Limit: 5, RetryAfter: 30 * time.Second}
	m.CooldownIpCapped(ice)
	st := m.CooldownPersistState()
	if st.IpCapped == nil || st.IpCappedUntil.IsZero() {
		t.Fatalf("ip-cap not captured: %+v", st.IpCapped)
	}

	m2 := NewRunManager(nil, nil, time.Hour)
	m2.RestoreCooldownState(st)
	if got := m2.IpCappedError(); got == nil || got.ActiveUsersForIP != ice.ActiveUsersForIP {
		t.Errorf("ip-cap not restored: %+v", got)
	}
}

func TestCooldownStateRoundTripBan(t *testing.T) {
	m := NewRunManager(nil, nil, time.Hour)
	be := &upstream.BanError{Body: "banned", ResumesAt: time.Now().Add(2 * time.Hour)}
	m.CooldownBan(be)
	st := m.CooldownPersistState()
	if st.Ban == nil || st.BanUntil.IsZero() {
		t.Fatalf("ban not captured: %+v", st.Ban)
	}

	m2 := NewRunManager(nil, nil, time.Hour)
	m2.RestoreCooldownState(st)
	if got := m2.BanError(); got == nil || got.Body != be.Body {
		t.Errorf("ban not restored: %+v", got)
	}
}

func TestCooldownStateRoundTripCountryBlock(t *testing.T) {
	m := NewRunManager(nil, nil, time.Hour)
	cbe := &upstream.CountryBlockedError{CountryCode: "ID", CountryBlockReason: "blocked"}
	m.CooldownCountryBlocked(cbe)
	st := m.CooldownPersistState()
	if st.CountryBlock == nil || st.CountryUntil.IsZero() {
		t.Fatalf("country-block not captured: %+v", st.CountryBlock)
	}

	m2 := NewRunManager(nil, nil, time.Hour)
	m2.RestoreCooldownState(st)
	if got := m2.CountryBlockedError(); got == nil || got.CountryCode != cbe.CountryCode {
		t.Errorf("country-block not restored: %+v", got)
	}
}

func TestCooldownStateClampsFarFuture(t *testing.T) {
	ceiling := 7 * 24 * time.Hour
	m := NewRunManager(nil, nil, time.Hour)
	farFuture := time.Now().Add(100 * 24 * time.Hour)
	m.mu.Lock()
	m.cooldownUntil = farFuture
	m.mu.Unlock()

	st := m.CooldownPersistState()
	m2 := NewRunManager(nil, nil, time.Hour)
	m2.RestoreCooldownState(st)

	got := m2.CooldownUntil()
	if got.IsZero() || got.After(time.Now().Add(ceiling+time.Hour)) {
		t.Errorf("far-future deadline not clamped: %v (now+ceiling=%v)", got, time.Now().Add(ceiling))
	}
}

func TestCooldownStateExpiredNotRestored(t *testing.T) {
	m := NewRunManager(nil, nil, time.Hour)
	// A rate-limit that has already expired.
	past := time.Now().Add(-10 * time.Minute)
	m.mu.Lock()
	m.cooldownUntil = past
	m.rateLimit = &upstream.RateLimitError{RetryAfter: time.Minute}
	m.mu.Unlock()

	st := m.CooldownPersistState()
	m2 := NewRunManager(nil, nil, time.Hour)
	m2.RestoreCooldownState(st)

	// The accessor returns nil because the deadline is in the past.
	if got := m2.RateLimitError(); got != nil {
		t.Error("expired rate-limit should not surface after restore")
	}
}

func TestCooldownStateRoundTripHardBan(t *testing.T) {
	m := NewRunManager(nil, nil, time.Hour)
	be := &upstream.BanError{Body: "hard banned"} // no ResumesAt = permanent
	m.CooldownBan(be)
	st := m.CooldownPersistState()
	if !st.BanPermanent || st.Ban == nil {
		t.Fatalf("hard ban not captured: BanPermanent=%v, Ban=%+v", st.BanPermanent, st.Ban)
	}

	m2 := NewRunManager(nil, nil, time.Hour)
	m2.RestoreCooldownState(st)
	if got := m2.BanError(); got == nil || got.Body != be.Body {
		t.Errorf("hard ban not restored: %+v", got)
	}
}
