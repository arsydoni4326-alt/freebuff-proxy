package pool

// Phase 3 of the SQLite state-persistence program: durable snapshots of the
// per-token spend and usage/request ledgers (AccountLedger + its embedded
// spendLedger). The JSON types ride inside the opaque token_state blob (see
// state_store.go). Locking: the *Of snapshot helpers and the applyTo helpers
// assume the caller holds the owning guard — tokenRoster.mu for pooled
// entries (wrapped below), Pool.bridgeMu for bridge entries.

import "time"

// SpendPersistState is a JSON-round-trippable snapshot of a spendLedger: the
// rolling 24h spend entries, the Pacific day/week/month buckets with their
// period starts (unix seconds), and the spend_limited refusal counter (#122).
type SpendPersistState struct {
	Rolling      []SpendEntryPersistState `json:"rolling,omitempty"`
	DayUsed      int64                    `json:"day_used"`
	DayStart     int64                    `json:"day_start"`
	WeekUsed     int64                    `json:"week_used"`
	WeekStart    int64                    `json:"week_start"`
	MonthUsed    int64                    `json:"month_used"`
	MonthStart   int64                    `json:"month_start"`
	SpendLimited int                      `json:"spend_limited"`
}

// SpendEntryPersistState is one rolling-window spend record.
type SpendEntryPersistState struct {
	At     time.Time `json:"at"`
	Tokens int64     `json:"tokens"`
}

// IsZero reports whether the snapshot carries no spend state (a Phase-1/2-only
// blob decodes into this shape).
func (s SpendPersistState) IsZero() bool {
	return len(s.Rolling) == 0 && s.DayUsed == 0 && s.WeekUsed == 0 &&
		s.MonthUsed == 0 && s.SpendLimited == 0
}

// AccountPersistState is a JSON-round-trippable snapshot of an AccountLedger:
// the rolling 24h successful-chat timestamps (MAX_MESSAGES_PER_DAY), the
// rolling 60s admitted-request timestamps (MAX_REQUESTS_PER_MINUTE), and the
// Pacific-day request counter with its bucket start (MAX_REQUESTS_PER_DAY).
type AccountPersistState struct {
	Usage   []time.Time `json:"usage,omitempty"`
	Reqs    []time.Time `json:"reqs,omitempty"`
	DayCnt  int64       `json:"day_cnt"`
	DaySeen int64       `json:"day_start"`
}

// IsZero reports whether the snapshot carries no usage/request state.
func (a AccountPersistState) IsZero() bool {
	return len(a.Usage) == 0 && len(a.Reqs) == 0 && a.DayCnt == 0
}

// spendSnapshotOf copies a spendLedger's durable fields. Caller holds the
// owning guard (roster mu / bridgeMu).
func spendSnapshotOf(sp *spendLedger) SpendPersistState {
	if sp == nil {
		return SpendPersistState{}
	}
	out := SpendPersistState{
		DayUsed:      sp.dayUsed,
		DayStart:     sp.dayStart,
		WeekUsed:     sp.weekUsed,
		WeekStart:    sp.weekStart,
		MonthUsed:    sp.monthUsed,
		MonthStart:   sp.monthStart,
		SpendLimited: sp.spendLimited,
	}
	if len(sp.rolling) > 0 {
		out.Rolling = make([]SpendEntryPersistState, len(sp.rolling))
		for i, e := range sp.rolling {
			out.Rolling[i] = SpendEntryPersistState{At: e.at, Tokens: e.tokens}
		}
	}
	return out
}

// accountSnapshotOf copies an AccountLedger's durable fields. Caller holds
// the owning guard.
func accountSnapshotOf(l *AccountLedger) AccountPersistState {
	if l == nil {
		return AccountPersistState{}
	}
	return AccountPersistState{
		Usage:   append([]time.Time(nil), l.usage...),
		Reqs:    append([]time.Time(nil), l.requests...),
		DayCnt:  l.reqDayCount,
		DaySeen: l.reqDayStart,
	}
}

// applySpendTo overwrites a spendLedger with a persisted snapshot. Caller
// holds the owning guard.
func applySpendTo(sp *spendLedger, st SpendPersistState) {
	if sp == nil {
		return
	}
	sp.rolling = nil
	if len(st.Rolling) > 0 {
		sp.rolling = make([]spendEntry, len(st.Rolling))
		for i, e := range st.Rolling {
			sp.rolling[i] = spendEntry{at: e.At, tokens: e.Tokens}
		}
	}
	sp.dayUsed = st.DayUsed
	sp.dayStart = st.DayStart
	sp.weekUsed = st.WeekUsed
	sp.weekStart = st.WeekStart
	sp.monthUsed = st.MonthUsed
	sp.monthStart = st.MonthStart
	sp.spendLimited = st.SpendLimited
}

// applyAccountTo overwrites an AccountLedger with a persisted snapshot.
// Caller holds the owning guard.
func applyAccountTo(l *AccountLedger, st AccountPersistState) {
	if l == nil {
		return
	}
	l.usage = st.Usage
	l.requests = st.Reqs
	l.reqDayCount = st.DayCnt
	l.reqDayStart = st.DaySeen
}

// spendState snapshots the entry at token's spend ledger under the roster
// lock (zero value when out of range).
func (r *tokenRoster) spendState(token int) SpendPersistState {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := *r.toks.Load()
	if token < 0 || token >= len(cur) {
		return SpendPersistState{}
	}
	return spendSnapshotOf(cur[token].ledger.spend)
}

// accountState snapshots the entry at token's usage/request ledger under the
// roster lock (zero value when out of range).
func (r *tokenRoster) accountState(token int) AccountPersistState {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := *r.toks.Load()
	if token < 0 || token >= len(cur) {
		return AccountPersistState{}
	}
	return accountSnapshotOf(cur[token].ledger)
}

// applySpend overwrites the entry at token's spend ledger with a persisted
// snapshot under the roster lock (no-op when out of range).
func (r *tokenRoster) applySpend(token int, st SpendPersistState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := *r.toks.Load()
	if token < 0 || token >= len(cur) {
		return
	}
	applySpendTo(cur[token].ledger.spend, st)
}

// applyAccount overwrites the entry at token's usage/request ledger with a
// persisted snapshot under the roster lock (no-op when out of range).
func (r *tokenRoster) applyAccount(token int, st AccountPersistState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := *r.toks.Load()
	if token < 0 || token >= len(cur) {
		return
	}
	applyAccountTo(cur[token].ledger, st)
}

// indexOf resolves entry's current roster position (-1 when retired).
func (r *tokenRoster) indexOf(entry *tokenEntry) int {
	if entry == nil {
		return -1
	}
	cur := *r.toks.Load()
	for i, e := range cur {
		if e == entry {
			return i
		}
	}
	return -1
}
