package pool

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

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
