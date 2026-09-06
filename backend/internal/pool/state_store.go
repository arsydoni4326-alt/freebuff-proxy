package pool

import (
	"encoding/json"
	"time"
)

// TokenStateStore persists per-token operational state across restarts. The
// pool serializes its own tokenState JSON; the store only carries opaque
// bytes, so it stays a dependency-free leaf (implemented by the custom SQLite
// token DB's token_state table when active). A nil store disables persistence.
type TokenStateStore interface {
	// LoadTokenStates returns every persisted state blob keyed by token
	// value; states whose token no longer exists are omitted.
	LoadTokenStates() (map[string][]byte, error)
	// SaveTokenState upserts the state blob for one token value.
	SaveTokenState(token string, state []byte) error
	// ClearTokenState removes the state blob for one token value.
	ClearTokenState(token string) error
}

// tokenState is the durable slice of a tokenEntry's operational state that
// must survive restarts for anti-ban correctness (Phase 1 of the SQLite state
// persistence program): the administrative lock and the terminal quarantine
// (banned / country_blocked / invalid). Cooldown windows (runs) and
// spend/quota ledgers are persisted in later phases (see session.md) — until
// then a restart forgets 429/cooldown windows but never re-admits a locked or
// dead account.
type tokenState struct {
	Locked           bool
	Quarantined      bool
	QuarantineReason string
	QuarantineDetail string
	QuarantineLiftAt time.Time
}

// SetTokenStateStore wires the durable per-token state store. Call once at
// startup before RestoreTokenState; nil disables persistence.
func (p *Pool) SetTokenStateStore(store TokenStateStore) {
	p.stateStore = store
}

// persistTokenState writes one entry's durable state to the store. Failures
// log a warning and never block or reject the mutation — the in-memory state
// stays authoritative for the running process and the next mutation retries
// the write.
func (p *Pool) persistTokenState(e *tokenEntry) {
	if e == nil || e.token == "" || p.stateStore == nil {
		return
	}
	st := tokenState{Locked: e.locked.Load()}
	if q := e.quarantine.Load(); q != nil {
		st.Quarantined = true
		st.QuarantineReason = q.reason
		st.QuarantineDetail = q.detail
		st.QuarantineLiftAt = q.liftAt
	}
	blob, err := json.Marshal(st)
	if err != nil {
		p.logger.Warn("pool: persist token state: marshal failed", "token_label", tokenEntryLabel(e), "err", err)
		return
	}
	if err := p.stateStore.SaveTokenState(e.token, blob); err != nil {
		p.logger.Warn("pool: persist token state failed", "token_label", tokenEntryLabel(e), "err", err)
	}
}

// RestoreTokenState re-applies the persisted administrative locks and terminal
// quarantines onto the roster entries by token value. Called once at startup
// after the roster is reconciled with the token database, so a locked or dead
// account stays locked/quarantined across restarts (anti-ban contract). States
// for tokens not in the roster are ignored; a lifted temporary-ban quarantine
// self-heals on the next maintain pass (clearLiftedQuarantine).
func (p *Pool) RestoreTokenState() {
	if p.stateStore == nil {
		return
	}
	states, err := p.stateStore.LoadTokenStates()
	if err != nil {
		p.logger.Warn("pool: restore token state failed", "err", err)
		return
	}
	if len(states) == 0 {
		return
	}
	toks := p.roster.Load()
	for _, e := range *toks {
		blob, ok := states[e.token]
		if !ok {
			continue
		}
		var st tokenState
		if err := json.Unmarshal(blob, &st); err != nil {
			p.logger.Warn("pool: restore token state: corrupt state ignored", "token_label", tokenEntryLabel(e), "err", err)
			continue
		}
		if st.Locked {
			e.locked.Store(true)
		}
		if st.Quarantined {
			e.quarantine.Store(&quarantineState{
				reason: st.QuarantineReason,
				detail: st.QuarantineDetail,
				liftAt: st.QuarantineLiftAt,
			})
			p.logger.Warn("pool: token state restored (terminal account state)",
				"token_label", tokenEntryLabel(e), "state", st.QuarantineReason)
		}
	}
}
