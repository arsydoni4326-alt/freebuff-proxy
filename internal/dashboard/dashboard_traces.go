package dashboard

import (
	"freebuff-proxy/internal/phasetiming"
	"strconv"
	"strings"
)

// --- traces ---

type tracesData struct {
	Enabled bool         `json:"enabled"`
	Traces  []traceEntry `json:"traces"`
}

type traceEntry struct {
	Time   string    `json:"time"`
	Token  string    `json:"token"`
	Model  string    `json:"model"`
	Status string    `json:"status"`
	Ms     string    `json:"ms"`
	Error  string    `json:"error"`
	Phases []PhaseKV `json:"phases,omitempty"`
}

func (d *Dashboard) tracesData() tracesData {
	td := tracesData{Enabled: d.logs != nil}
	if d.logs == nil {
		return td
	}
	phaseNames := map[string]bool{
		phasetiming.AcquireMS:        true,
		phasetiming.SessionRefreshMS: true,
		phasetiming.RunAcquireMS:     true,
		phasetiming.UpstreamTTFBMS:   true,
		phasetiming.TotalMS:          true,
	}
	for _, e := range d.logs.Recent(200) {
		if e.Message != "chat trace" {
			continue
		}
		entry := traceEntry{Time: e.Time, Status: "ok"}
		var phaseMap map[string]int64
		for _, f := range e.Fields {
			key, value, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch key {
			case "token":
				entry.Token = value
			case "model":
				entry.Model = value
			case "status":
				entry.Status = value
			case "ms":
				entry.Ms = value + "ms"
			case "error":
				entry.Error = value
			default:
				if phaseNames[key] {
					if phaseMap == nil {
						phaseMap = make(map[string]int64, 5)
					}
					if v, err := strconv.ParseInt(value, 10, 64); err == nil {
						phaseMap[key] = v
					}
				}
			}
		}
		if phaseMap != nil {
			entry.Phases = PhaseList(phaseMap)
		}
		if entry.Token == "" {
			entry.Token = "—"
		}
		td.Traces = append(td.Traces, entry)
	}
	return td
}
