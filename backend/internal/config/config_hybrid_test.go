package config

import (
	"testing"
	"time"
)

// TestBridgeEnabledDefaultTrue pins the hybrid default: with AUTH_TOKENS
// set and BRIDGE_ENABLED unset, the proxy runs in hybrid mode (pooled +
// bridge) and BRIDGE_IDLE_EVICT defaults to 72h.
func TestBridgeEnabledDefaultTrue(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_TOKENS", "tok-1,tok-2")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.BridgeEnabled {
		t.Error("BridgeEnabled = false, want true (hybrid is the default)")
	}
	if !cfg.HybridBridgeMode() {
		t.Error("HybridBridgeMode() = false, want true with AUTH_TOKENS set and bridge enabled")
	}
	if cfg.BridgeMode() {
		t.Error("BridgeMode() = true, want false (tokens configured)")
	}
	if got := cfg.EffectiveMode(); got != "hybrid" {
		t.Errorf("EffectiveMode() = %q, want hybrid", got)
	}
	if cfg.BridgeIdleEvict != 72*time.Hour {
		t.Errorf("BridgeIdleEvict = %v, want 72h default", cfg.BridgeIdleEvict)
	}
}

// TestBridgeEnabledZeroDisablesBridge pins the lockdown switch: with
// BRIDGE_ENABLED=0 the instance is pure pooled even though AUTH_TOKENS are
// configured.
func TestBridgeEnabledZeroDisablesBridge(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_TOKENS", "tok-1")
	t.Setenv("BRIDGE_ENABLED", "0")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BridgeEnabled {
		t.Error("BridgeEnabled = true, want false after BRIDGE_ENABLED=0")
	}
	if cfg.HybridBridgeMode() {
		t.Error("HybridBridgeMode() = true, want false with BRIDGE_ENABLED=0")
	}
	if got := cfg.EffectiveMode(); got != "pooled" {
		t.Errorf("EffectiveMode() = %q, want pooled", got)
	}
}

// TestBridgeIdleEvictParse verifies BRIDGE_IDLE_EVICT parsing and its
// zero-tolerant 72h fallback (a zero TTL would evict every bridge entry on
// the first idle pass).
func TestBridgeIdleEvictParse(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_TOKENS", "tok-1")

	t.Setenv("BRIDGE_IDLE_EVICT", "48h")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BridgeIdleEvict != 48*time.Hour {
		t.Errorf("BridgeIdleEvict = %v, want 48h", cfg.BridgeIdleEvict)
	}

	t.Setenv("BRIDGE_IDLE_EVICT", "0")
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BridgeIdleEvict != 72*time.Hour {
		t.Errorf("BridgeIdleEvict = %v after 0, want 72h fallback", cfg.BridgeIdleEvict)
	}
}

// TestBridgeEnabledIgnoredWithoutTokens: without AUTH_TOKENS the instance
// is pure bridge regardless of BRIDGE_ENABLED.
func TestBridgeEnabledIgnoredWithoutTokens(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.BridgeMode() {
		t.Error("BridgeMode() = false, want true with no AUTH_TOKENS")
	}
	if cfg.HybridBridgeMode() {
		t.Error("HybridBridgeMode() = true, want false with no AUTH_TOKENS")
	}
	if got := cfg.EffectiveMode(); got != "bridge" {
		t.Errorf("EffectiveMode() = %q, want bridge", got)
	}
}
