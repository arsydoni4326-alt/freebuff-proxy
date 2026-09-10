package session

import (
	"sync"
	"testing"
	"time"

	"freebuff-proxy/backend/internal/upstream"
)

// memoryBackend is a thread-safe in-memory StateBackend test double.
type memoryBackend struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{blobs: make(map[string][]byte)}
}

func (m *memoryBackend) LoadState(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.blobs[key]; ok {
		return append([]byte(nil), b...), nil
	}
	return nil, nil
}

func (m *memoryBackend) LoadAll() (map[string][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]byte, len(m.blobs))
	for k, v := range m.blobs {
		out[k] = append([]byte(nil), v...)
	}
	return out, nil
}

func (m *memoryBackend) SaveState(key string, blob []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if blob == nil {
		delete(m.blobs, key)
		return nil
	}
	m.blobs[key] = append([]byte(nil), blob...)
	return nil
}

// TestBackendSessionRoundTrip verifies Save/Load through a StateBackend
// preserves every resumable field (mirrors the file-store round-trip test).
func TestBackendSessionRoundTrip(t *testing.T) {
	store := NewStoreWithBackend(newMemoryBackend())
	key := "hash-key"
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	grace := exp.Add(15 * time.Minute)
	cs := &cachedState{
		status:            "active",
		instanceID:        "inst-1",
		model:             "deepseek/deepseek-v4-flash",
		expiresAt:         exp,
		gracePeriodEndsAt: grace,
		countryCode:       "ID",
		quotaByModel: map[string]upstream.ModelQuota{
			"deepseek/deepseek-v4-flash": {Model: "deepseek/deepseek-v4-flash", Limit: 5, RecentCount: 2, ResetAt: exp.Add(2 * time.Hour)},
		},
	}
	store.Save(key, cs)

	// A fresh store over the same backend resumes the session.
	store2 := NewStoreWithBackend(newMemoryBackendLoadedFrom(t, store))
	got := store2.Load(key)
	if got == nil {
		t.Fatal("Load returned nil after Save through the backend")
	}
	if got.instanceID != "inst-1" || got.model != cs.model {
		t.Errorf("resumed session = %+v, want instance inst-1", got)
	}
	if q, ok := got.quotaByModel["deepseek/deepseek-v4-flash"]; !ok || q.RecentCount != 2 {
		t.Errorf("resumed quota = %+v, want RecentCount 2", got.quotaByModel)
	}
}

// newMemoryBackendLoadedFrom copies a source store's backend blobs, so the
// test constructs a "second process" view over the same durable storage.
func newMemoryBackendLoadedFrom(t *testing.T, src *Store) *memoryBackend {
	t.Helper()
	src.mu.Lock()
	defer src.mu.Unlock()
	blobs, err := src.backend.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	return &memoryBackend{blobs: blobs}
}

// TestBackendRunsRoundTrip verifies SaveRun/LoadRun/RemoveRun through a
// StateBackend, including a fresh-store resume view.
func TestBackendRunsRoundTrip(t *testing.T) {
	backend := newMemoryBackend()
	store := NewStoreWithBackend(backend)
	key, agent := "hash-key", "base2-free-deepseek-flash"
	pr := PersistedRun{RunID: "run-1", AgentID: agent, TraceSessionID: "trace-1", StartedAt: time.Now().Truncate(time.Second), Requests: 3}
	store.SaveRun(key, agent, pr)

	// Both stores share one backend: the second store's first access loads
	// the blob the first store saved (the "restart" view).
	store2 := NewStoreWithBackend(backend)
	got := store2.LoadRun(key, agent)
	if got == nil || got.RunID != "run-1" {
		t.Fatalf("resumed run = %+v, want run-1", got)
	}
	if got.Requests != 3 {
		t.Errorf("resumed run Requests = %d, want 3", got.Requests)
	}

	// Removing the run deletes the key's blob (session was never saved).
	store2.RemoveRun(key, agent)
	if got := store2.LoadRun(key, agent); got != nil {
		t.Error("run not removed")
	}
	if len(backend.blobs) != 0 {
		t.Errorf("backend blobs after removal = %d, want 0 (empty key must be deleted)", len(backend.blobs))
	}
}

// TestBackendRemoveSession verifies Remove through a StateBackend and that a
// stale instance id cannot clobber a newer session (file-parity).
func TestBackendRemoveSession(t *testing.T) {
	store := NewStoreWithBackend(newMemoryBackend())
	key := "hash-key"
	cs := &cachedState{
		status: "active", instanceID: "inst-1", model: "m",
		expiresAt: time.Now().Add(time.Hour),
	}
	store.Save(key, cs)

	// Stale instance id: removal is refused.
	store.Remove(key, "stale")
	if got := store.Load(key); got == nil {
		t.Fatal("stale-instance removal clobbered the live session")
	}

	// Matching instance id: removal persists as a delete.
	store.Remove(key, "inst-1")
	if got := store.Load(key); got != nil {
		t.Error("session not removed")
	}
}

// TestBackendActiveEntryWithoutInstanceIDDropped pins the file-parity guard:
// an "active" blob without an instance id cannot be resumed and is dropped
// on load instead of poisoning the resume path.
func TestBackendActiveEntryWithoutInstanceIDDropped(t *testing.T) {
	backend := newMemoryBackend()
	key := "hash-key"
	// Simulate a legacy/corrupt blob: active status but no instance id.
	blob, err := MarshalKvBlob(KvBlob{Session: &PersistedState{Status: "active", Model: "m", ExpiresAt: time.Now().Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.SaveState(key, blob); err != nil {
		t.Fatal(err)
	}

	store2 := NewStoreWithBackend(backend)
	if got := store2.Load(key); got != nil {
		t.Errorf("active entry without instance id was resumed: %+v", got)
	}
}
