package dashboard

import (
	"net"
)

// --- client setup ---

type setupData struct {
	BaseURL      string   `json:"base_url"`
	KeyHint      string   `json:"key_hint"`
	Model        string   `json:"model"`
	Models       []string `json:"models"`
	Mode         string   `json:"mode"`
	Bridge       bool     `json:"bridge"`
	BridgeTokens int      `json:"bridge_tokens"`
	TokenCount   int      `json:"token_count"`
	HasTokens    bool     `json:"has_tokens"`
}

func (d *Dashboard) setupData() setupData {
	cfg := d.cfg()
	host := "localhost"
	if h, _, err := net.SplitHostPort(cfg.ListenAddr); err == nil && h != "" && h != "0.0.0.0" && h != "::" {
		host = h
	}
	mode := cfg.EffectiveMode()
	sd := setupData{
		BaseURL:      "http://" + host + "/v1",
		Mode:         mode,
		Bridge:       mode == "bridge",
		BridgeTokens: d.pool.BridgeCount(),
		TokenCount:   d.pool.TokenCount(),
		Models:       servedModels(d.reg),
	}
	sd.HasTokens = sd.TokenCount > 0
	if len(sd.Models) > 0 {
		sd.Model = pickDefaultModel(sd.Models)
	}
	switch mode {
	case "bridge":
		sd.KeyHint = "your FreeBuff token (bridge mode: the client's Authorization header IS the upstream token)"
	default:
		sd.KeyHint = "sk-any (pooled mode; the proxy picks from AUTH_TOKENS)"
	}
	return sd
}
