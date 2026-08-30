package dashboard

import (
	"freebuff-proxy/backend/internal/modelcat"
	"freebuff-proxy/backend/internal/registry"
	"sort"
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

// quotaFor returns the daily session-quota label for a model row: the
// shared premium pool (luna, solar-pro4) shows the 5/day premium
// entitlement, GLM 5.2 is the referral reward (+1/day), and every other
// served row is unmetered. These strings are the displayed copy and stay
// in sync with the upstream catalog facts (modelcat).
func quotaFor(id string) string {
	if modelcat.IsPremium(id) {
		return "5/day shared premium"
	}
	if id == modelcat.Glm52ModelID {
		return "referral +1/day"
	}
	return "unmetered"
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
		row.Quota = quotaFor(id)
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
