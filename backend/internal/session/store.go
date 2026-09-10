package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"freebuff-proxy/backend/internal/upstream"
)

// storeVersion guards the on-disk format; bump it when the schema changes so
// a stale file is ignored instead of mis-parsed.
const storeVersion = 1

// maxStoreFileSize caps the on-disk store file; anything larger is treated
// as empty instead of being read into memory wholesale.
const maxStoreFileSize = 8 << 20 // 8 MiB

// PersistedState is the on-disk shape of one token's cached session. The
// instance id + expiry are the fields that matter for restart-resume; the
// rest are carried so a resumed session keeps its country/queue view.
type PersistedState struct {
	InstanceID         string                    `json:"instance_id"`
	Model              string                    `json:"model"`
	Status             string                    `json:"status"`
	ExpiresAt          time.Time                 `json:"expires_at"`
	GracePeriodEndsAt  time.Time                 `json:"grace_period_ends_at"`
	Position           int                       `json:"position"`
	QueueDepth         int                       `json:"queue_depth"`
	PollAt             time.Time                 `json:"poll_at"`
	CountryCode        string                    `json:"country_code"`
	CountryBlockReason string                    `json:"country_block_reason"`
	AccessTier         string                    `json:"access_tier,omitempty"`
	QuotaByModel       map[string]PersistedQuota `json:"quota_by_model,omitempty"`
	// GlmPromo is the raw upstream glmPromo block ({dailySessions,
	// endsAt}); "" when absent (issue #178).
	GlmPromo string `json:"glm_promo,omitempty"`
	// Account blocks persisted so a restart keeps the dashboard's
	// referral banner, Freebucks card and standing until the next full
	// admission refreshes them (rework 2026-09-05): compact polls never
	// carry them, so without this the UI goes blank on every restart.
	// All optional — old files load with nils.
	Referral  *upstream.SessionReferral `json:"referral,omitempty"`
	Freebucks *upstream.FreebucksInfo   `json:"freebucks,omitempty"`
	Standing  *upstream.SessionStanding `json:"standing,omitempty"`
}

// PersistedQuota is one model's live session quota persisted on disk.
type PersistedQuota struct {
	Model       string             `json:"model"`
	Limit       float64            `json:"limit"`
	RecentCount float64            `json:"recent_count"`
	ResetAt     time.Time          `json:"reset_at"`
	Period      string             `json:"period"`
	Entitlement map[string]float64 `json:"entitlement,omitempty"`
}

// PersistedRun is the on-disk shape of one token's active agent run
// (issue #40): a restart resumes the run id without re-START. Keyed per
// token (hash) and agent, alongside the session state.
type PersistedRun struct {
	RunID          string `json:"run_id"`
	AgentID        string `json:"agent_id"`
	TraceSessionID string `json:"trace_session_id"`
	// ClientID is the run's codebuff_metadata["client_id"]. Additive: an old
	// file parses with "" and the run manager mints a fresh id, which only
	// costs that resumed run one client-id change.
	ClientID  string    `json:"client_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
	Requests  int       `json:"requests"`
}

type storeFile struct {
	Version  int                       `json:"version"`
	Sessions map[string]PersistedState `json:"sessions"`
	// Runs maps token key → agent id → persisted run (issue #40). Additive
	// since version 1: old files parse with an empty runs map, and old
	// binaries ignore the extra field.
	Runs map[string]map[string]PersistedRun `json:"runs,omitempty"`
}

// Store persists cached session state to a single JSON file so a proxy
// restart can resume an unexpired upstream session instead of burning a new
// session slot. Keys are token hashes (upstream.Client.TokenKey), never raw
// tokens. All methods are safe for concurrent use; writes are atomic
// (temp file + rename) and the file is created with mode 0600.
type Store struct {
	path string

	// backend is the optional pluggable durable backend (Phase 4 of the
	// SQLite state-persistence program). When non-nil, persistence goes
	// through it instead of the JSON file — no bind-mounted-file writes.
	// The whole in-memory view is written back per key on each flush (the
	// token count is small, so the write amplification is negligible).
	backend     StateBackend
	backendKeys map[string]struct{} // keys known to the backend, for deletions

	mu     sync.Mutex
	data   map[string]PersistedState
	runs   map[string]map[string]PersistedRun // token key → agent id → run (issue #40)
	loaded bool
	// readFailed is set when a read of the on-disk file failed with a
	// non-NotExist error (chmod 000, transient EIO). While set, Save/Remove
	// keep their in-memory update but must NOT flush the partial map over the
	// file: that would destroy every other token's persisted entry. The
	// flag is cleared once a load succeeds (the on-disk content is then
	// established and a flush is safe again).
	readFailed bool
	// pending records mutations that could not be flushed while readFailed
	// was set (key → state written, nil for a removal). loadLocked merges
	// them back over the disk content on the next successful reload so the
	// in-window updates survive instead of being silently discarded by the
	// rebuild (a lost update would leave a restart unable to resume the
	// session, burning a daily slot). Guarded by mu.
	pending map[string]*PersistedState
}

// NewStore builds a store backed by path. The file is read lazily on the
// first Load; NewStore never fails (a missing/unreadable file is treated as
// empty and a later Save overwrites it).
func NewStore(path string) *Store {
	return &Store{path: path, pending: make(map[string]*PersistedState), runs: make(map[string]map[string]PersistedRun)}
}

// NewStoreWithBackend builds a store whose persistence is backed by the
// given StateBackend (Phase 4: the SQLite token DB's session_state table)
// instead of a JSON file. The path is unused (kept empty). Load semantics are
// identical: the backend is read lazily on first Load/Save, an unreadable
// backend retries on the next access.
func NewStoreWithBackend(backend StateBackend) *Store {
	return &Store{backend: backend, pending: make(map[string]*PersistedState), runs: make(map[string]map[string]PersistedRun), backendKeys: make(map[string]struct{})}
}

// NewStoreWithBackendPath is a backward-compatible variant that accepts a
// path for legacy file import tests. When path is non-empty, the store imports
// the legacy JSON file on first load (if the backend is a fakeSessionBackend
// or similar test double). Production code should use NewStoreWithBackend.
func NewStoreWithBackendPath(backend StateBackend, path string) *Store {
	s := &Store{backend: backend, pending: make(map[string]*PersistedState), runs: make(map[string]map[string]PersistedRun), backendKeys: make(map[string]struct{})}
	if path != "" {
		s.path = path
	}
	return s
}

func (s *Store) loadLocked() {
	if s.loaded {
		return
	}
	s.data = make(map[string]PersistedState)
	s.runs = make(map[string]map[string]PersistedRun)

	// Backend mode (Phase 4): read every persisted blob once and decode
	// each into the in-memory view. If a legacy session path is also set,
	// import that file into the backend first (import-only: the source
	// archives to .bak and the backend row wins on collision).
	if s.backend != nil {
		if s.path != "" {
			if err := s.importLegacyFileIntoBackend(); err != nil {
				slog.Warn("session store: legacy file import failed, proceeding with existing backend data", "path", s.path, "err", err)
			}
		}
		blobs, err := s.backend.LoadAll()
		if err != nil {
			s.readFailed = true
			slog.Warn("session store: backend read failed, will retry on next access", "err", err)
			return
		}
		for key, blob := range blobs {
			kb, err := UnmarshalKvBlob(blob)
			if err != nil {
				slog.Warn("session store: backend blob parse failed, ignoring", "err", err)
				continue
			}
			if kb.Session != nil {
				ps := *kb.Session
				if ps.Status == "active" && ps.InstanceID == "" {
					continue // cannot be resumed; drop like the file path
				}
				s.data[key] = ps
			}
			if len(kb.Runs) > 0 {
				runMap := make(map[string]PersistedRun, len(kb.Runs))
				for agentID, pr := range kb.Runs {
					if pr.RunID != "" {
						runMap[agentID] = pr
					}
				}
				if len(runMap) > 0 {
					s.runs[key] = runMap
				}
			}
			s.backendKeys[key] = struct{}{}
		}
		s.loaded = true
		s.readFailed = false
		s.applyPendingLocked()
		return
	}

	// Legacy file fallback: import-only when path is set and backend is nil.
	// The file is archived to .bak on first use and never written again.
	if s.path != "" {
		if err := s.importLegacyFileIntoBackend(); err != nil {
			slog.Warn("session store: legacy file import failed", "path", s.path, "err", err)
		}
		s.loaded = true
		s.readFailed = false
		s.applyPendingLocked()
		return
	}

	// Reject oversized files before reading them into memory.
	if fi, err := os.Stat(s.path); err == nil && fi.Size() > maxStoreFileSize {
		slog.Warn("session store: file too large, ignoring", "path", s.path, "bytes", fi.Size())
		s.loaded = true
		s.readFailed = false
		s.applyPendingLocked()
		return
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// First run: a missing file is a valid empty store.
			s.loaded = true
			s.readFailed = false
			s.applyPendingLocked()
		} else {
			// Leave loaded=false so the next Load (or Save) retries the
			// read instead of permanently freezing an empty view that a
			// later Save would flush over the on-disk file, destroying
			// other tokens' persisted sessions. Track readFailed so Save/
			// Remove skip the flush while the file stays unreadable.
			s.readFailed = true
			slog.Warn("session store: read failed, will retry on next access", "path", s.path, "err", err)
		}
		return
	}
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		// The file is genuinely bad: remember that so we stop re-parsing it
		// and proceed empty (a later Save replaces it).
		slog.Warn("session store: parse failed, ignoring", "path", s.path, "err", err)
		s.loaded = true
		s.readFailed = false
		s.applyPendingLocked()
		return
	}
	if file.Version != storeVersion {
		slog.Warn("session store: version mismatch, ignoring", "path", s.path, "version", file.Version)
		s.loaded = true
		s.readFailed = false
		s.applyPendingLocked()
		return
	}
	if file.Sessions != nil {
		for key, ps := range file.Sessions {
			// An "active" entry without an instance id cannot be resumed and
			// would poison the resume path; drop it on load.
			if ps.Status == "active" && ps.InstanceID == "" {
				slog.Warn("session store: dropping active entry with empty instance id", "path", s.path, "key", key)
				continue
			}
			s.data[key] = ps
		}
	}
	if file.Runs != nil {
		for key, agents := range file.Runs {
			if len(agents) == 0 {
				continue
			}
			runMap := make(map[string]PersistedRun, len(agents))
			for agentID, pr := range agents {
				// A run entry without an id cannot be resumed; drop it.
				if pr.RunID == "" {
					continue
				}
				runMap[agentID] = pr
			}
			if len(runMap) > 0 {
				s.runs[key] = runMap
			}
		}
	}
	s.loaded = true
	s.readFailed = false
	s.applyPendingLocked()
}

// Load returns the persisted cached state for key, or nil when absent or
// already expired beyond the grace window. Load never performs upstream
// calls; it only filters obviously-dead entries.
func (s *Store) Load(key string) *cachedState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	ps, ok := s.data[key]
	if !ok {
		return nil
	}
	// Drop entries whose grace window is already closed: resuming them is
	// impossible and keeping them only delays the inevitable re-create.
	if !ps.GracePeriodEndsAt.IsZero() && time.Now().After(ps.GracePeriodEndsAt) {
		delete(s.data, key)
		if s.flushLockedUnlessReadFailed() {
			s.recordPendingLocked(key, nil)
		}
		return nil
	}
	cs := &cachedState{
		status:             ps.Status,
		instanceID:         ps.InstanceID,
		model:              ps.Model,
		expiresAt:          ps.ExpiresAt,
		gracePeriodEndsAt:  ps.GracePeriodEndsAt,
		position:           ps.Position,
		queueDepth:         ps.QueueDepth,
		pollAt:             ps.PollAt,
		countryCode:        ps.CountryCode,
		countryBlockReason: ps.CountryBlockReason,
		accessTier:         ps.AccessTier,
		glmPromo:           ps.GlmPromo,
		referral:           ps.Referral,
		freebucks:          ps.Freebucks,
		standing:           ps.Standing,
	}
	if len(ps.QuotaByModel) > 0 {
		cs.quotaByModel = make(map[string]upstream.ModelQuota, len(ps.QuotaByModel))
		for k, q := range ps.QuotaByModel {
			mq := upstream.ModelQuota{
				Model:       q.Model,
				Limit:       q.Limit,
				RecentCount: q.RecentCount,
				ResetAt:     q.ResetAt,
				Period:      q.Period,
			}
			if len(q.Entitlement) > 0 {
				mq.Entitlement = make(map[string]float64, len(q.Entitlement))
				for ek, ev := range q.Entitlement {
					mq.Entitlement[ek] = ev
				}
			}
			cs.quotaByModel[k] = mq
		}
	}
	return cs
}

// Save persists cs under key. A nil cs removes the key. Disabled sessions
// (no instance id, no expiry) are not persisted: there is nothing to resume.
func (s *Store) Save(key string, cs *cachedState) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()

	if cs == nil || (cs.instanceID == "" && cs.status != "queued") {
		delete(s.data, key)
		if s.flushLockedUnlessReadFailed() {
			s.recordPendingLocked(key, nil)
		}
		return
	}
	s.data[key] = PersistedState{
		InstanceID:         cs.instanceID,
		Model:              cs.model,
		Status:             cs.status,
		ExpiresAt:          cs.expiresAt,
		GracePeriodEndsAt:  cs.gracePeriodEndsAt,
		Position:           cs.position,
		QueueDepth:         cs.queueDepth,
		PollAt:             cs.pollAt,
		CountryCode:        cs.countryCode,
		CountryBlockReason: cs.countryBlockReason,
		AccessTier:         cs.accessTier,
		GlmPromo:           cs.glmPromo,
	}
	if len(cs.quotaByModel) > 0 {
		ps := s.data[key]
		ps.QuotaByModel = make(map[string]PersistedQuota, len(cs.quotaByModel))
		for k, q := range cs.quotaByModel {
			pq := PersistedQuota{
				Model:       q.Model,
				Limit:       q.Limit,
				RecentCount: q.RecentCount,
				ResetAt:     q.ResetAt,
				Period:      q.Period,
			}
			if len(q.Entitlement) > 0 {
				pq.Entitlement = make(map[string]float64, len(q.Entitlement))
				for ek, ev := range q.Entitlement {
					pq.Entitlement[ek] = ev
				}
			}
			ps.QuotaByModel[k] = pq
		}
		s.data[key] = ps
	}
	// Account blocks: value-copy the scalar structs; deep-copy Freebucks
	// (its price maps are mutated in place by the schedule applier, so a
	// shared reference would race the live state).
	if cs.referral != nil {
		ps := s.data[key]
		r := *cs.referral
		ps.Referral = &r
		s.data[key] = ps
	}
	if cs.freebucks != nil {
		ps := s.data[key]
		ps.Freebucks = cloneFreebucksInfo(cs.freebucks)
		s.data[key] = ps
	}
	if cs.standing != nil {
		ps := s.data[key]
		st := *cs.standing
		if len(st.NextSteps) > 0 {
			st.NextSteps = append([]upstream.StandingNextStep(nil), st.NextSteps...)
		}
		ps.Standing = &st
		s.data[key] = ps
	}
	if s.flushLockedUnlessReadFailed() {
		ps := s.data[key]
		s.recordPendingLocked(key, &ps)
	}
}

// cloneFreebucksInfo deep-copies the map/slice fields ApplyFreebucksPriceChanges
// mutates in place, so the persisted snapshot cannot race live state.
func cloneFreebucksInfo(fb *upstream.FreebucksInfo) *upstream.FreebucksInfo {
	if fb == nil {
		return nil
	}
	out := *fb
	if len(fb.Prices) > 0 {
		out.Prices = make(map[string]float64, len(fb.Prices))
		for k, v := range fb.Prices {
			out.Prices[k] = v
		}
	}
	if len(fb.PriceNotices) > 0 {
		out.PriceNotices = make(map[string]string, len(fb.PriceNotices))
		for k, v := range fb.PriceNotices {
			out.PriceNotices[k] = v
		}
	}
	if len(fb.PriceChanges) > 0 {
		out.PriceChanges = append([]upstream.FreebucksPriceChange(nil), fb.PriceChanges...)
	}
	return &out
}

// Remove drops key from the store (session invalidated/ended at runtime).
// When expectedInstanceID is non-empty the entry is only removed if its
// stored instance id matches, so a stale invalidation cannot clobber a newer
// resumed session; an empty expectedInstanceID removes unconditionally.
func (s *Store) Remove(key, expectedInstanceID string) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()

	ps, ok := s.data[key]
	if !ok {
		return
	}
	if expectedInstanceID != "" && ps.InstanceID != expectedInstanceID {
		return
	}
	delete(s.data, key)
	if s.flushLockedUnlessReadFailed() {
		s.recordPendingLocked(key, nil)
	}
}

// SaveRun persists one active run for token key under agentID (issue #40).
// A run with an empty id is dropped. Best-effort: a skipped flush (file
// unreadable) is tolerated — the run is simply re-STARTed after a restart.
func (s *Store) SaveRun(key, agentID string, pr PersistedRun) {
	if key == "" || agentID == "" || pr.RunID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	agents := s.runs[key]
	if agents == nil {
		agents = make(map[string]PersistedRun)
		s.runs[key] = agents
	}
	agents[agentID] = pr
	s.flushLockedUnlessReadFailed()
}

// LoadRun returns the persisted run for token key + agentID, or nil when
// absent. The caller (runs manager) decides whether the run is fresh enough
// to adopt; LoadRun never performs upstream calls.
func (s *Store) LoadRun(key, agentID string) *PersistedRun {
	if key == "" || agentID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	pr, ok := s.runs[key][agentID]
	if !ok {
		return nil
	}
	copy := pr
	return &copy
}

// RemoveRun drops the persisted run for token key + agentID (FINISHed
// upstream or superseded by a fresh START).
func (s *Store) RemoveRun(key, agentID string) {
	if key == "" || agentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	agents, ok := s.runs[key]
	if !ok {
		return
	}
	if _, ok := agents[agentID]; !ok {
		return
	}
	delete(agents, agentID)
	if len(agents) == 0 {
		delete(s.runs, key)
	}
	s.flushLockedUnlessReadFailed()
}

// flushLockedUnlessReadFailed writes the current map atomically unless the
// on-disk file could not be read: flushing the partial in-memory view
// over an unreadable file would destroy every other token's persisted entry.
// The in-memory update is kept so the store stays consistent once the file
// becomes readable again; a warn log is the only signal that persistence was
// skipped. Returns true when the flush was skipped (file unreadable) so the
// caller records the mutation into pending — otherwise a later successful
// reload would rebuild s.data from disk and silently drop the update.
func (s *Store) flushLockedUnlessReadFailed() bool {
	if s.readFailed {
		slog.Warn("session store: file unreadable, skipping persist (in-memory update kept)", "path", s.path)
		return true
	}
	s.flushLocked()
	return false
}

// flushLocked writes the current map atomically. Caller holds s.mu.
func (s *Store) flushLocked() {
	// Backend mode (Phase 4): write every current key's blob and delete
	// keys that no longer exist. Failures are logged and retried on the
	// next flush (the in-memory view stays authoritative).
	if s.backend != nil {
		written := make(map[string]struct{}, len(s.data)+len(s.runs))
		for key := range s.data {
			written[key] = struct{}{}
		}
		for key, agents := range s.runs {
			written[key] = struct{}{}
			if _, ok := s.data[key]; ok {
				continue // the session pass below carries the runs too
			}
			blob, err := MarshalKvBlob(KvBlob{Runs: agents})
			if err == nil {
				err = s.backend.SaveState(key, blob)
			}
			if err != nil {
				slog.Warn("session store: backend save failed", "err", err)
			}
		}
		for key, ps := range s.data {
			kb := KvBlob{Session: &ps, Runs: s.runs[key]}
			blob, err := MarshalKvBlob(kb)
			if err == nil {
				err = s.backend.SaveState(key, blob)
			}
			if err != nil {
				slog.Warn("session store: backend save failed", "err", err)
			}
		}
		for key := range s.backendKeys {
			if _, ok := written[key]; !ok {
				if err := s.backend.SaveState(key, nil); err != nil {
					slog.Warn("session store: backend delete failed", "err", err)
					continue
				}
			}
		}
		s.backendKeys = written
		return
	}
	file := storeFile{Version: storeVersion, Sessions: s.data}
	if len(s.runs) > 0 {
		file.Runs = s.runs
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		slog.Warn("session store: marshal failed", "path", s.path, "err", err)
		return
	}

	dir := filepath.Dir(s.path)
	if dir == "" {
		dir = "."
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("session store: mkdir failed", "dir", dir, "err", err)
		return
	}

	// Write a temp file in the target directory, then rename it over the
	// target so a crash mid-write never leaves a truncated state file.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(s.path)+".tmp*")
	if err != nil {
		slog.Warn("session store: temp create failed", "dir", dir, "err", err)
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		slog.Warn("session store: write failed", "path", s.path, "err", err)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		slog.Warn("session store: close failed", "path", s.path, "err", err)
		return
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		if _, statErr := os.Stat(s.path); statErr == nil {
			// The target exists but rename-over-existing failed (e.g.
			// Windows without MOVEFILE_REPLACE_EXISTING): fall back to
			// removing the target first, then renaming.
			_ = os.Remove(s.path)
			if err := os.Rename(tmpName, s.path); err == nil {
				return
			}
		}
		_ = os.Remove(tmpName)
		slog.Warn("session store: rename failed", "path", s.path, "err", err)
	}
}

// recordPendingLocked remembers a mutation that could not be flushed because
// the file was unreadable (readFailed): the key with the state that was
// written, or nil for a removal. A later successful loadLocked merges
// pending back over the disk content so the in-window update survives the
// reload instead of being lost. Caller holds s.mu.
func (s *Store) recordPendingLocked(key string, ps *PersistedState) {
	if s.pending == nil {
		s.pending = make(map[string]*PersistedState)
	}
	s.pending[key] = ps
}

// applyPendingLocked merges mutations recorded while the file was unreadable
// back over the freshly-loaded disk view and persists the result: a nil
// value removes the key, a non-nil value restores the in-memory update that
// was never flushed. Without this, a Save/Remove made during the failure
// window would be silently discarded by the reload, and the following flush
// would persist WITHOUT it — a restart then fails to resume that session
// and burns a daily slot. The merged map is flushed immediately so the disk
// catches up. Caller holds s.mu.
func (s *Store) applyPendingLocked() {
	if len(s.pending) == 0 {
		return
	}
	for key, ps := range s.pending {
		if ps == nil {
			delete(s.data, key)
		} else {
			s.data[key] = *ps
		}
	}
	clear(s.pending)
	s.flushLocked()
}

// importLegacyFileIntoBackend imports a legacy .freebuff-session-state.json
// file into the backend (if set) or loads it directly into memory (if backend
// is nil). The source file is archived to .bak on first use and never written
// again. When the file doesn't exist, this is a no-op.
func (s *Store) importLegacyFileIntoBackend() error {
	if s.path == "" {
		return nil
	}

	// Read the legacy file.
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // No legacy file to import.
		}
		return fmt.Errorf("session store: read legacy file: %w", err)
	}

	// Parse the legacy file format.
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("session store: parse legacy file: %w", err)
	}

	if file.Version != storeVersion {
		return fmt.Errorf("session store: legacy file version mismatch: want %d, got %d", storeVersion, file.Version)
	}

	// Import sessions into backend or load directly.
	if s.backend != nil {
		// Backend mode: save each session to the backend.
		if file.Sessions != nil {
			for key, ps := range file.Sessions {
				// Skip entries that cannot be resumed.
				if ps.Status == "active" && ps.InstanceID == "" {
					continue
				}
				kb := KvBlob{Session: &ps, Runs: nil}
				if file.Runs != nil {
					if agents, ok := file.Runs[key]; ok && len(agents) > 0 {
						runMap := make(map[string]PersistedRun, len(agents))
						for agentID, pr := range agents {
							if pr.RunID != "" {
								runMap[agentID] = pr
							}
						}
						if len(runMap) > 0 {
							kb.Runs = runMap
						}
					}
				}
				blob, err := MarshalKvBlob(kb)
				if err != nil {
					return fmt.Errorf("session store: marshal legacy session %s: %w", key, err)
				}
				if err := s.backend.SaveState(key, blob); err != nil {
					return fmt.Errorf("session store: save legacy session %s to backend: %w", key, err)
				}
				s.backendKeys[key] = struct{}{}
			}
		}
		// Also import runs-only entries (no session data).
		if file.Runs != nil {
			for key, agents := range file.Runs {
				if _, ok := file.Sessions[key]; ok {
					continue // Already handled above.
				}
				if len(agents) == 0 {
					continue
				}
				runMap := make(map[string]PersistedRun, len(agents))
				for agentID, pr := range agents {
					if pr.RunID != "" {
						runMap[agentID] = pr
					}
				}
				if len(runMap) > 0 {
					kb := KvBlob{Session: nil, Runs: runMap}
					blob, err := MarshalKvBlob(kb)
					if err != nil {
						return fmt.Errorf("session store: marshal legacy runs %s: %w", key, err)
					}
					if err := s.backend.SaveState(key, blob); err != nil {
						return fmt.Errorf("session store: save legacy runs %s to backend: %w", key, err)
					}
					s.backendKeys[key] = struct{}{}
				}
			}
		}
	} else {
		// No backend: load directly into memory.
		if file.Sessions != nil {
			for key, ps := range file.Sessions {
				if ps.Status == "active" && ps.InstanceID == "" {
					continue
				}
				s.data[key] = ps
			}
		}
		if file.Runs != nil {
			for key, agents := range file.Runs {
				if len(agents) == 0 {
					continue
				}
				runMap := make(map[string]PersistedRun, len(agents))
				for agentID, pr := range agents {
					if pr.RunID != "" {
						runMap[agentID] = pr
					}
				}
				if len(runMap) > 0 {
					s.runs[key] = runMap
				}
			}
		}
	}

	// Archive the legacy file to .bak.
	bak := s.path + ".bak"
	_ = os.Remove(bak)
	if err := os.Rename(s.path, bak); err != nil {
		return fmt.Errorf("session store: archive legacy file: %w", err)
	}

	return nil
}
