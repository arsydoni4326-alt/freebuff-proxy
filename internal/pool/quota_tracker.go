package pool

import (
	"sort"
	"time"

	"freebuff-proxy/internal/modelcat"
	"freebuff-proxy/internal/session"
)

// PremiumQuotaSnapshot is the per-token premium quota view (4/day pacific_day pool
// and 2/day glm_v53_flash lane) derived from session.QuotaSnapshot.
type PremiumQuotaSnapshot struct {
	Limit       int       `json:"limit"`
	Used        int       `json:"used"`
	Remaining   int       `json:"remaining"`
	Period      string    `json:"period"`
	ResetAt     time.Time `json:"reset_at"`
	PercentUsed int       `json:"percent_used"`
	Entitled    bool      `json:"entitled"`
	Capped      bool      `json:"capped"`
}

// premiumPoolModels is the shared daily premium pool (upstream
// FREEBUFF_PREMIUM_MODEL_IDS minus the per-model cap lanes the proxy tracks
// separately). Derived from modelcat so the set follows upstream on sync.
var premiumPoolModels = modelcat.SharedPremiumModels()

const glm53Model = modelcat.Glm53ModelID

// isPremiumModel reports whether model is part of the premium pool or the
// dedicated GLM 5.3 Flash lane. Kept for acquire-path gating and tests.
func isPremiumModel(model string) bool {
	return modelcat.IsPremium(model)
}

func buildPremiumSnapshot(q session.QuotaSnapshot) *PremiumQuotaSnapshot {
	return buildPremiumSnapshotAt(q, time.Now())
}

func buildPremiumSnapshotAt(q session.QuotaSnapshot, now time.Time) *PremiumQuotaSnapshot {
	limit := int(q.Limit)
	used := int(q.RecentCount)
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	percent := 0
	if limit > 0 {
		percent = (used * 100) / limit
		if percent > 100 {
			percent = 100
		}
		if percent < 0 {
			percent = 0
		}
	}
	capped := !q.ResetAt.IsZero() && q.ResetAt.After(now) && q.RecentCount >= q.Limit
	entitled := len(q.Entitlement) > 0 || q.Limit > 0
	return &PremiumQuotaSnapshot{Limit: limit, Used: used, Remaining: remaining, Period: q.Period, ResetAt: q.ResetAt, PercentUsed: percent, Entitled: entitled, Capped: capped}
}

func premiumSnapshotFromQuotaMap(m map[string]session.QuotaSnapshot) (*PremiumQuotaSnapshot, *PremiumQuotaSnapshot) {
	return premiumSnapshotFromQuotaMapAt(m, time.Now())
}

func premiumSnapshotFromQuotaMapAt(m map[string]session.QuotaSnapshot, now time.Time) (*PremiumQuotaSnapshot, *PremiumQuotaSnapshot) {
	if len(m) == 0 {
		return nil, nil
	}
	var premium *PremiumQuotaSnapshot
	if q, ok := m[premiumPoolModels[0]]; ok {
		premium = buildPremiumSnapshotAt(q, now)
	} else {
		var present []string
		for _, model := range premiumPoolModels {
			if _, ok := m[model]; ok {
				present = append(present, model)
			}
		}
		if len(present) > 0 {
			sort.Strings(present)
			premium = buildPremiumSnapshotAt(m[present[0]], now)
		}
	}
	var glm *PremiumQuotaSnapshot
	if q, ok := m[glm53Model]; ok {
		glm = buildPremiumSnapshotAt(q, now)
	}
	return premium, glm
}
func (p *Pool) PremiumQuotaForToken(idx int) *PremiumQuotaSnapshot {
	toks := p.toks.Load()
	if toks == nil || idx < 0 || idx >= len(*toks) {
		return nil
	}
	tok := (*toks)[idx]
	if tok == nil || tok.session == nil {
		return nil
	}
	premium, _ := premiumSnapshotFromQuotaMap(tok.session.Snapshot().QuotaByModel)
	return premium
}

// PremiumSnapshotForToken is an alias kept for backward compat.
func (p *Pool) PremiumSnapshotForToken(idx int) *PremiumQuotaSnapshot {
	return p.PremiumQuotaForToken(idx)
}

func (p *Pool) Glm53QuotaForToken(idx int) *PremiumQuotaSnapshot {
	toks := p.toks.Load()
	if toks == nil || idx < 0 || idx >= len(*toks) {
		return nil
	}
	tok := (*toks)[idx]
	if tok == nil || tok.session == nil {
		return nil
	}
	_, glm := premiumSnapshotFromQuotaMap(tok.session.Snapshot().QuotaByModel)
	return glm
}

func (p *Pool) PremiumQuotaForBridge(key string) *PremiumQuotaSnapshot {
	p.bridgeMu.RLock()
	entry, ok := p.bridge[key]
	if !ok {
		entry, ok = p.bridge[tokenKey(key)]
	}
	p.bridgeMu.RUnlock()
	if !ok || entry == nil || entry.session == nil {
		return nil
	}
	premium, _ := premiumSnapshotFromQuotaMap(entry.session.Snapshot().QuotaByModel)
	return premium
}

func (p *Pool) PremiumSnapshotForBridge(key string) *PremiumQuotaSnapshot {
	return p.PremiumQuotaForBridge(key)
}

func (p *Pool) Glm53QuotaForBridge(key string) *PremiumQuotaSnapshot {
	p.bridgeMu.RLock()
	entry, ok := p.bridge[key]
	if !ok {
		entry, ok = p.bridge[tokenKey(key)]
	}
	p.bridgeMu.RUnlock()
	if !ok || entry == nil || entry.session == nil {
		return nil
	}
	_, glm := premiumSnapshotFromQuotaMap(entry.session.Snapshot().QuotaByModel)
	return glm
}
