package registry

import "testing"

// Luna's base2 root is retired upstream (free_mode_legacy_luna_agent); every
// path that resolves a root must hand out base3-free-luna instead.
func TestRetiredRootOverrideAppliesToBothPaths(t *testing.T) {
	// Offline fallback path.
	fallback := New(nil, nil)
	fallback.LoadFallback()
	got, err := fallback.AgentForModel("openai/gpt-5.6-luna")
	if err != nil {
		t.Fatalf("fallback AgentForModel: %v", err)
	}
	if got != "base3-free-luna" {
		t.Errorf("fallback root = %q, want base3-free-luna", got)
	}

	// Live-parse path: the parsed root map still names the retired agent.
	modelToAgent, _ := buildModelMapping(
		[]agentModels{{agent: "base2-free", models: []string{"openai/gpt-5.6-luna"}}},
		map[string]string{"openai/gpt-5.6-luna": "base2-free-luna"},
	)
	if modelToAgent["openai/gpt-5.6-luna"] != "base3-free-luna" {
		t.Errorf("parsed root = %q, want base3-free-luna", modelToAgent["openai/gpt-5.6-luna"])
	}

	// An override never invents a model the sources do not serve.
	modelToAgent, _ = buildModelMapping(nil, map[string]string{"mimo/mimo-v2.5": "base2-free-mimo"})
	if _, ok := modelToAgent["openai/gpt-5.6-luna"]; ok {
		t.Error("override invented a catalog row for an unserved model")
	}
}
