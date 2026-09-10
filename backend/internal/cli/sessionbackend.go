package cli

// Phase 4 of the SQLite state-persistence program: adapt the SQLite token
// DB's session_state table to session.StateBackend, so SESSION_PERSIST state
// is stored in SQLite instead of a JSON file — no bind-mounted-file writes.
// The adapter lives here because it is the only package allowed to import
// both tokendb and session (archtest matrix).

import (
	"encoding/json"
	"errors"

	"freebuff-proxy/backend/internal/store"
	"freebuff-proxy/backend/internal/tokendb"

	"freebuff-proxy/backend/internal/session"
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

// storeSessionBackend adapts *store.Store to session.StateBackend. It uses the
// store package's sessions_persist table (SaveSession/LoadSession/ListSessionKeys),
// keeping the blobs as KvBlob JSON (session + runs per key) — the same shape the
// session package marshals/unmarshals. This adapter is separate from
// sqliteSessionBackend because the store package is a different DB handle (the
// dashboard/settings DB).
type storeSessionBackend struct {
	st *store.Store
}

func (b storeSessionBackend) LoadState(key string) ([]byte, error) {
	sess, runs, found, err := b.st.LoadSession(key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	kb := session.KvBlob{
		Session: nil,
		Runs:    nil,
	}
	if sess != "" {
		var ps session.PersistedState
		if jsonErr := json.Unmarshal([]byte(sess), &ps); jsonErr == nil {
			kb.Session = &ps
		}
	}
	if runs != "" {
		if rawErr := json.Unmarshal([]byte(runs), &kb.Runs); rawErr != nil {
			kb.Runs = nil
		}
	}
	return session.MarshalKvBlob(kb)
}

func (b storeSessionBackend) LoadAll() (map[string][]byte, error) {
	keys, err := b.st.ListSessionKeys()
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(keys))
	for _, key := range keys {
		blob, err := b.LoadState(key)
		if err != nil {
			return nil, err
		}
		if blob != nil {
			out[key] = blob
		}
	}
	return out, nil
}

func (b storeSessionBackend) SaveState(key string, blob []byte) error {
	if key == "" {
		return errors.New("session: invalid key")
	}
	if blob == nil {
		return b.st.DeleteSession(key)
	}
	kb, uErr := session.UnmarshalKvBlob(blob)
	if uErr != nil {
		return uErr
	}
	sessJSON := ""
	if kb.Session != nil {
		sessJSONBytes, marshalErr := json.Marshal(kb.Session)
		if marshalErr != nil {
			return marshalErr
		}
		sessJSON = string(sessJSONBytes)
	}
	runsJSON := ""
	if kb.Runs != nil {
		runsJSONBytes, marshalErr := json.Marshal(kb.Runs)
		if marshalErr != nil {
			return marshalErr
		}
		runsJSON = string(runsJSONBytes)
	}
	return b.st.SaveSession(key, sessJSON, runsJSON)
}
