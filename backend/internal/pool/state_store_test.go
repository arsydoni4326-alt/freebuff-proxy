package pool

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"freebuff-proxy/backend/internal/runs"
	"freebuff-proxy/backend/internal/testutil"
	"freebuff-proxy/backend/internal/upstream"
)

// memoryStateStore is a thread-safe in-memory TokenStateStore test double.
type memoryStateStore struct {
	mu     sync.Mutex
	states map[string][]byte
}

func newMemoryStateStore() *memoryStateStore {
	return &memoryStateStore{states: make(map[string][]byte)}
}

func (m *memoryStateStore) LoadTokenStates() (map[string][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]byte, len(m.states))
	for k, v := range m.states {
		out[k] = append([]byte(nil), v...)
	}
	return out, nil
}

func (m *memoryStateStore) SaveTokenState(token string, state []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[token] = append([]byte(nil), state...)
	return nil
}

func (m *memoryStateStore) ClearTokenState(token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, token)
	return nil
}

// decoded returns the store's persisted tokenState for token (zero value when
// absent), failing the test on a corrupt blob.
func (m *memoryStateStore) decoded(t *testing.T, token string) tokenState {
	t.Helper()
	blob, ok := m.states[token]
	if !ok {
		return tokenState{}
	}
	var st tokenState
	if err := json.Unmarshal(blob, &st); err != nil {
		t.Fatalf("store blob for %s is not a tokenState: %v", token, err)
	}
	return st
}

func TestLockTokenPersistsAndRestores(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	p.SetTokenStateStore(store)

	if err := p.LockToken(0); err != nil {
		t.Fatal(err)
	}
	if !store.decoded(t, "tok-0").Locked {
		t.Error("LockToken did not persist locked=true")
	}

	// A fresh pool restores the lock from the store.
	p2 := newTestPool(t, mock)
	p2.SetTokenStateStore(store)
	p2.RestoreTokenState()
	if got := (*p2.roster.Load())[0].locked.Load(); !got {
		t.Error("RestoreTokenState did not re-apply the persisted lock")
	}

	// Unlocking persists locked=false and a fresh restore leaves it unlocked.
	if err := p.UnlockLockToken(0); err != nil {
		t.Fatal(err)
	}
	if store.decoded(t, "tok-0").Locked {
		t.Error("UnlockLockToken did not persist locked=false")
	}
	p3 := newTestPool(t, mock)
	p3.SetTokenStateStore(store)
	p3.RestoreTokenState()
	if got := (*p3.roster.Load())[0].locked.Load(); got {
		t.Error("restored pool kept a lock that was released")
	}
}

func TestQuarantinePersistsAndRestores(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	p.SetTokenStateStore(store)

	// A hard ban quarantines the token terminally (anti-ban contract).
	p.CooldownTokenBan(0, &upstream.BanError{Body: "banned"})
	st := store.decoded(t, "tok-0")
	if !st.Quarantined || st.QuarantineReason != "banned" {
		t.Fatalf("ban did not persist quarantine: %+v", st)
	}

	// A fresh pool restores the quarantine so the dead account is never
	// re-admitted after a restart.
	p2 := newTestPool(t, mock)
	p2.SetTokenStateStore(store)
	p2.RestoreTokenState()
	q := (*p2.roster.Load())[0].quarantine.Load()
	if q == nil || q.reason != "banned" {
		t.Fatalf("restored quarantine = %+v, want banned", q)
	}

	// UnlockToken clears the quarantine and persists the cleared state.
	if err := p.UnlockToken(0); err != nil {
		t.Fatal(err)
	}
	if st := store.decoded(t, "tok-0"); st.Quarantined {
		t.Error("UnlockToken did not persist the cleared quarantine")
	}
}

func TestQuarantinePersistsLiftAt(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	p.SetTokenStateStore(store)

	lift := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	p.CooldownTokenBan(0, &upstream.BanError{Body: "temporary", ResumesAt: lift})
	st := store.decoded(t, "tok-0")
	if !st.Quarantined || !st.QuarantineLiftAt.Equal(lift) {
		t.Fatalf("temporary ban did not persist liftAt: %+v (want %v)", st, lift)
	}
}

func TestRestoreTokenStateIgnoresUnknownTokens(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	store.states["ghost"] = []byte(`{"Locked":true,"Quarantined":true,"QuarantineReason":"banned"}`)
	p.SetTokenStateStore(store)

	p.RestoreTokenState() // must not panic or apply the unknown state
	e := (*p.roster.Load())[0]
	if e.locked.Load() || e.quarantine.Load() != nil {
		t.Error("state for an unknown token was applied to the roster")
	}
}

func TestStateStoreDisabledIsNoop(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock) // no SetTokenStateStore: persistence disabled

	if err := p.LockToken(0); err != nil {
		t.Fatal(err)
	}
	p.CooldownTokenBan(0, &upstream.BanError{Body: "banned"})
	p.RestoreTokenState() // nil store: no-op
	e := (*p.roster.Load())[0]
	if !e.locked.Load() {
		t.Error("lock must still apply in-memory without a store")
	}
}

func TestRateLimitCooldownPersistsAndRestores(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	p.SetTokenStateStore(store)

	rle := &upstream.RateLimitError{Status: "rate_limited", RetryAfter: 10 * time.Minute, Limit: 5, RecentCount: 5}
	p.CooldownTokenRateLimit(0, rle)
	st := store.decoded(t, "tok-0")
	if st.Cooldown.RateLimit == nil || st.Cooldown.RateLimit.RetryAfter != rle.RetryAfter {
		t.Fatalf("rate-limit not persisted: Cooldown=%+v", st.Cooldown)
	}

	p2 := newTestPool(t, mock)
	p2.SetTokenStateStore(store)
	p2.RestoreTokenState()
	e := (*p2.roster.Load())[0]
	if got := e.runs.RateLimitError(); got == nil || got.RetryAfter != rle.RetryAfter {
		t.Errorf("rate-limit not restored: got %+v, want RetryAfter=%v", got, rle.RetryAfter)
	}
}

func TestIpCappedCooldownPersistsAndRestores(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	p.SetTokenStateStore(store)

	ice := &upstream.IpCappedError{ActiveUsersForIP: 3, Limit: 5, RetryAfter: 30 * time.Second}
	p.CooldownTokenIpCapped(0, ice)
	st := store.decoded(t, "tok-0")
	if st.Cooldown.IpCapped == nil || st.Cooldown.IpCappedUntil.IsZero() {
		t.Fatalf("ip-cap not persisted: Cooldown=%+v", st.Cooldown)
	}

	p2 := newTestPool(t, mock)
	p2.SetTokenStateStore(store)
	p2.RestoreTokenState()
	e := (*p2.roster.Load())[0]
	if got := e.runs.IpCappedError(); got == nil || got.ActiveUsersForIP != 3 {
		t.Errorf("ip-cap not restored: %+v", got)
	}
}

func TestAuthCooldownPersistsAndRestores(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	p.SetTokenStateStore(store)

	p.CooldownToken(0, runs.DefaultCooldown)
	st := store.decoded(t, "tok-0")
	if st.Cooldown.CooldownUntil.IsZero() {
		t.Fatal("auth cooldown not persisted")
	}

	p2 := newTestPool(t, mock)
	p2.SetTokenStateStore(store)
	p2.RestoreTokenState()
	e := (*p2.roster.Load())[0]
	if e.runs.CooldownUntil().IsZero() {
		t.Error("auth cooldown not restored")
	}
}

// --- Phase 3: spend + usage/request ledger persistence ---

func TestSpendLedgerPersistsAndRestores(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	p.SetTokenStateStore(store)

	// Record spend through the pool (fires the persist hook), then read the
	// persisted snapshot.
	p.recordSpend(0, 500)
	st := store.decoded(t, "tok-0")
	if st.Spend.DayUsed != 500 {
		t.Fatalf("spend not persisted: DayUsed = %d, want 500", st.Spend.DayUsed)
	}
	if st.Spend.DayStart == 0 || st.Spend.WeekStart == 0 || st.Spend.MonthStart == 0 {
		t.Errorf("period starts not persisted: %+v", st.Spend)
	}
	if len(st.Spend.Rolling) == 0 {
		t.Error("rolling 24h window not persisted")
	}

	// A fresh pool restores the spend buckets.
	p2 := newTestPool(t, mock)
	p2.SetTokenStateStore(store)
	p2.RestoreTokenState()
	view := p2.spendSnapshot(0)
	if view.Day != 500 {
		t.Errorf("restored Day = %d, want 500", view.Day)
	}
	if view.DayStart.IsZero() || view.DayStart.Unix() != st.Spend.DayStart {
		t.Errorf("restored DayStart = %v, want unix %d", view.DayStart, st.Spend.DayStart)
	}
	if view.Rolling24h != 500 {
		t.Errorf("restored Rolling24h = %d, want 500", view.Rolling24h)
	}
}

func TestSpendLimitedCounterPersistsAndRestores(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	p.SetTokenStateStore(store)

	p.recordSpendLimited(0)
	st := store.decoded(t, "tok-0")
	if st.Spend.SpendLimited != 1 {
		t.Fatalf("spend_limited counter not persisted: %d, want 1", st.Spend.SpendLimited)
	}

	p2 := newTestPool(t, mock)
	p2.SetTokenStateStore(store)
	p2.RestoreTokenState()
	if got := p2.spendSnapshot(0).SpendLimited; got != 1 {
		t.Errorf("restored SpendLimited = %d, want 1", got)
	}
}

func TestUsageLedgerPersistsAndRestores(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	p.SetTokenStateStore(store)

	p.recordChat(0)
	p.recordChat(0)
	st := store.decoded(t, "tok-0")
	if len(st.Account.Usage) != 2 {
		t.Fatalf("usage timestamps not persisted: %d entries, want 2", len(st.Account.Usage))
	}
	if st.Account.DayCnt != 0 {
		t.Errorf("DayCnt should stay 0 on recordChat (success-day counter is recorded elsewhere): %d", st.Account.DayCnt)
	}

	p2 := newTestPool(t, mock)
	p2.SetTokenStateStore(store)
	p2.RestoreTokenState()
	if got := p2.usageCount(0); got != 2 {
		t.Errorf("restored usageCount = %d, want 2", got)
	}
	if got := p2.usageResetIn(0); got <= 0 {
		t.Errorf("restored usageResetIn = %v, want > 0 (within the 24h window)", got)
	}
}

func TestDayRequestCounterPersistsAndRestores(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newTestPool(t, mock)
	store := newMemoryStateStore()
	p.SetTokenStateStore(store)

	// Drive a chat through the lease success path (recordChatEntry also
	// bumps the Pacific-day request counter via the entry ledger).
	p.recordChat(0)
	// Bump the day counter directly through the roster (the acquire path is
	// upstream-bound; the counter rides recordDayRequest).
	toks := p.roster.Load()
	(*toks)[0].ledger.recordDayRequest(time.Now())
	p.persistTokenIndex(0)

	st := store.decoded(t, "tok-0")
	if st.Account.DayCnt != 1 {
		t.Fatalf("day request counter not persisted: %d, want 1", st.Account.DayCnt)
	}

	p2 := newTestPool(t, mock)
	p2.SetTokenStateStore(store)
	p2.RestoreTokenState()
	if got := p2.dayRequestCount(0); got != 1 {
		t.Errorf("restored dayRequestCount = %d, want 1", got)
	}
}

func TestZeroLedgerStateIsNotApplied(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	store := newMemoryStateStore()
	store.states["tok-0"] = []byte(`{"Locked":false}`) // Phase-1-era blob: no Spend/Account

	p := newTestPool(t, mock)
	p.SetTokenStateStore(store)
	p.RestoreTokenState()

	view := p.spendSnapshot(0)
	if view.Day != 0 || len(store.decoded(t, "tok-0").Spend.Rolling) != 0 {
		t.Errorf("zero spend state mutated the live ledger: %+v", view)
	}
	if got := p.usageCount(0); got != 0 {
		t.Errorf("zero account state mutated usage: %d", got)
	}
}
