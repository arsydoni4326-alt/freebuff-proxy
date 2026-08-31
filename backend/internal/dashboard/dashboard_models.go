package dashboard

import (
	"fmt"
	"sort"
	"strconv"

	"freebuff-proxy/backend/internal/modelcat"
	"freebuff-proxy/backend/internal/registry"
)

// --- models ---

type modelsData struct {
	Models     []modelRow `json:"models"`
	Count      int        `json:"count"`
	Agents     int        `json:"agents"`
	Aliases    []aliasRow `json:"aliases"`
	HasAliases bool       `json:"has_aliases"`
}

type modelRow struct {
	ID    string `json:"id"`
	Agent string `json:"agent"`
	Quota string `json:"quota"`
}

// servedModels returns the registry ids that pass the strict ServedModels
// gate (issue #189 set): the vendor catalog also carries god-only/eval rows
// (luna-es) that must never appear as servable in dashboard/setup views.
func servedModels(reg *registry.Registry) []string {
	out := make([]string, 0, 8)
	for _, id := range reg.Models() {
		if registry.IsServedModel(id) {
			out = append(out, id)
		}
	}
	return out
}

type aliasRow struct {
	Alias string `json:"alias"`
	Real  string `json:"real"`
}

// quotaFor returns the daily session-quota label for a model row. The label
// prefers the LIVE wire snapshot (rateLimitsByModel mirrored per token) —
// the server-computed limit moves with trust-level/streak/referral bonuses,
// so a static number goes stale (the old copy said 5/day while the wire
// limit is base 4 plus bonuses). With no live data it falls back to catalog
// copy: shared premium pool (luna, solar-pro4), the referral reward (GLM
// 5.2), else unmetered.
func (d *Dashboard) quotaFor(id string) string {
	if d.pool != nil {
		if live := d.liveQuotaLabel(id); live != "" {
			return live
		}
	}
	if modelcat.IsPremium(id) {
		return "shared premium pool"
	}
	if id == modelcat.Glm52ModelID {
		return "referral +1/day"
	}
	return "unmetered"
}

// liveQuotaLabel renders "used of limit" from the first token quota snapshot
// carrying an entry for the model ("1.6 of 5 used" — the CLI's fractional
// unit display). "" when no token has live data for the model.
func (d *Dashboard) liveQuotaLabel(id string) string {
	for _, t := range d.pool.Snapshot() {
		if q, ok := t.QuotaByModel[id]; ok && q.Limit > 0 {
			return fmt.Sprintf("%s of %s used", formatSessionUnits(q.RecentCount), formatSessionUnits(q.Limit))
		}
	}
	return ""
}

// formatSessionUnits mirrors the CLI's unit display
// (format-session-units.ts): integers render bare, fractionals to one
// decimal — a long run can consume 1.3 sessions and billing floors at 0.1.
func formatSessionUnits(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func (d *Dashboard) modelsData() modelsData {
	md := modelsData{Count: d.reg.ModelCount(), Agents: len(d.reg.AgentIDs())}
	// Served gate: the dashboard shows the models this proxy actually
	// serves (issue #189 strict set), not the raw upstream registry — the
	// vendor catalog now carries god-only/eval rows (e.g. luna-es) that
	// must never be presented as servable.
	for _, id := range d.reg.Models() {
		if !registry.IsServedModel(id) {
			continue
		}
		row := modelRow{ID: id}
		if agent, err := d.reg.AgentForModel(id); err == nil {
			row.Agent = agent
		}
		row.Quota = d.quotaFor(id)
		md.Models = append(md.Models, row)
	}
	md.Count = len(md.Models)
	cfg := d.cfg()
	for alias, real := range cfg.ModelAliases {
		md.Aliases = append(md.Aliases, aliasRow{Alias: alias, Real: real})
	}
	sort.Slice(md.Aliases, func(i, j int) bool { return md.Aliases[i].Alias < md.Aliases[j].Alias })
	md.HasAliases = len(md.Aliases) > 0
	return md
}
