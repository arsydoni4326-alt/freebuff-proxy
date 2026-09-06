package session

// Phase 4 of the SQLite state-persistence program: a pluggable durable
// backend for Store. The default backend is the JSON file (unchanged); when
// the SQLite token DB is active, cli.Serve wires a backend over it instead,
// so SESSION_PERSIST state survives without writing any file — the same
// no-file-writes contract as the token/state persistence phases.
//
// The backend carries opaque JSON blobs keyed by token hash (the same key
// space as the file store); the blob format is the kvBlob below, which wraps
// the token's session state and its per-agent runs so one SQLite row holds
// everything a restart needs to resume that token.

import (
	"encoding/json"
)

// StateBackend is the durable backing for a Store. Implemented by the file
// store internally (nil backend) and, via a thin adapter, by the SQLite
// token DB when it is active.
type StateBackend interface {
	// LoadState returns the persisted blob for key, or nil when absent.
	LoadState(key string) ([]byte, error)
	// LoadAll returns every persisted blob keyed by token hash.
	LoadAll() (map[string][]byte, error)
	// SaveState upserts the blob for key; a nil blob removes the key.
	SaveState(key string, blob []byte) error
}

// kvBlob is the per-key blob shape persisted through a StateBackend: the
// token's cached session plus its per-agent runs, so one row holds everything
// a restart needs for that token. Additive-friendly: unknown fields decode as
// zero values.
type kvBlob struct {
	Session *persistedState         `json:"session,omitempty"`
	Runs    map[string]PersistedRun `json:"runs,omitempty"`
}

func marshalKvBlob(b kvBlob) ([]byte, error) {
	return json.Marshal(b)
}

func unmarshalKvBlob(data []byte) (kvBlob, error) {
	var b kvBlob
	err := json.Unmarshal(data, &b)
	return b, err
}
