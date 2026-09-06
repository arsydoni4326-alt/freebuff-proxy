package pool

// SQLite token-state persistence (Phases 1-3 of the state-persistence
// program): the pool serializes a per-token JSON blob (lock, terminal
// quarantine, cooldown windows, spend + usage/request ledgers) into the
// TokenStateStore — the SQLite token DB's token_state table when active.
// The store only carries opaque bytes, so pool stays a leaf above it.

import (
	"encoding/json"
	"time"

	"freebuff-proxy/backend/internal/runs"
)

// TokenStateStore persists per-token operational state across restarts. A nil
// store disables persistence.
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
// must survive restarts. Phase 1: the administrative lock and the terminal
// quarantine (banned / country_blocked / invalid). Phase 2: the cooldown/ban/
// country/ip-cap windows (runs.CooldownState), so 429/403 windows survive
// restarts too. Phase 3: the spend and usage/request ledgers so quota
// accounting survives restarts.
type tokenState struct {
	Locked           bool
	Quarantined      bool
	QuarantineReason string
	QuarantineDetail string
	QuarantineLiftAt time.Time
	// Cooldown carries the entry's cooldown/ban/country/ip-cap windows.
	// Zero-valued unless a window is live at persist time.
	Cooldown runs.CooldownState
	// Spend carries the spend ledger: rolling 24h window, Pacific day/week/
	// month buckets, and the spend_limited refusal counter (#122).
	Spend SpendPersistState
	// Account carries the usage + request ledger: rolling 24h successful-chat
	// timestamps, rolling 60s admitted-request timestamps, and the Pacific-day
	// request counter (#263).
	Account AccountPersistState
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
	idx := p.roster.indexOf(e)
	st := tokenState{
		Locked:   e.locked.Load(),
		Cooldown: e.runs.CooldownPersistState(),
		Spend:    p.roster.spendState(idx),
		Account:  p.roster.accountState(idx),
	}
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

// persistTokenIndex persists the durable state of the roster entry at idx
// (convenience for the index-based ledger mutation paths).
func (p *Pool) persistTokenIndex(idx int) {
	toks := p.roster.Load()
	if idx < 0 || idx >= len(*toks) {
		return
	}
	p.persistTokenState((*toks)[idx])
}

// RestoreTokenState re-applies the persisted operational state (locks,
// quarantines, cooldown windows, spend + usage/request ledgers) onto the
// roster entries by token value. Called once at startup after the roster is
// reconciled with the token database, so a locked or dead account stays
// locked/quarantined and quota accounting continues where it left off.
// States for tokens not in the roster are ignored; a lifted temporary-ban
// quarantine self-heals on the next maintain pass (clearLiftedQuarantine).
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
	for idx, e := range *toks {
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
		// Phase 2: restore cooldown/ban/country/ip-cap windows so 429/403
		// deadlines survive restarts. The runs accessors self-time-out if the
		// deadline has already passed, so an expired window silently drops.
		// A zero-valued Cooldown (from a Phase-1-only state blob) is harmless.
		e.runs.RestoreCooldownState(st.Cooldown)

		// Phase 3: restore spend ledger (rolling 24h window, Pacific
		// day/week/month buckets, spend_limited counter).
		if !st.Spend.IsZero() {
			p.roster.applySpend(idx, st.Spend)
		}
		// Phase 3: restore usage/request ledger (rolling 24h chat timestamps,
		// rolling 60s request timestamps, Pacific-day request counter).
		if !st.Account.IsZero() {
			p.roster.applyAccount(idx, st.Account)
		}
	}
}
