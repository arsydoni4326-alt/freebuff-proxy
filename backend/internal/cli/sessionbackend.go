package cli

// Phase 4 of the SQLite state-persistence program: adapt the SQLite token
// DB's session_state table to session.StateBackend, so SESSION_PERSIST state
// is stored in SQLite instead of a JSON file — no bind-mounted-file writes.
// The adapter lives here because it is the only package allowed to import
// both tokendb and session (archtest matrix).

import (
	"freebuff-proxy/backend/internal/tokendb"
)

// sqliteSessionBackend adapts *tokendb.DB to session.StateBackend. The blob
// format (session + runs per key) is owned by the session package; this
// adapter only moves opaque bytes.
type sqliteSessionBackend struct {
	db *tokendb.DB
}

func (b sqliteSessionBackend) LoadState(key string) ([]byte, error) {
	return b.db.LoadSessionState(key)
}

func (b sqliteSessionBackend) LoadAll() (map[string][]byte, error) {
	return b.db.LoadAllSessionStates()
}

func (b sqliteSessionBackend) SaveState(key string, blob []byte) error {
	return b.db.SaveSessionState(key, blob)
}
