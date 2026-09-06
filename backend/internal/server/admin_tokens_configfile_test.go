package server_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"freebuff-proxy/backend/internal/config"
	"freebuff-proxy/backend/internal/pool"
	"freebuff-proxy/backend/internal/registry"
	"freebuff-proxy/backend/internal/server"
	"freebuff-proxy/backend/internal/session"
	"freebuff-proxy/backend/internal/testutil"
	"freebuff-proxy/backend/internal/upstream"
)

// TestDashboardTokenAddUpdatesConfigFile verifies that a token added via the
// dashboard lands in the -config JSON file (regression: "token add does not
// update /app/config.json").
func TestDashboardTokenAddUpdatesConfigFile(t *testing.T) {
	t.Chdir(t.TempDir())
	mock := testutil.NewMock()
	defer mock.Close()

	cfgContent := "{\n  \"UPSTREAM_BASE_URL\": \"" + mock.URL() + "\",\n  \"AUTH_TOKENS\": [\"tok-0\"],\n  \"SAFE_MODE\": true,\n  \"ADMIN_TOKEN\": \"secret\"\n}\n"
	if err := os.WriteFile("config.json", []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AuthTokens: []string{"tok-0"}, RotationInterval: time.Hour,
		RequestTimeout: 15 * time.Minute, SessionCallTimeout: 5 * time.Second,
		RegistryRefresh: 6 * time.Hour, UpstreamBaseURL: mock.URL(),
		AdminToken: "secret", DashboardEnabled: true,
	}
	clientCfg := *cfg
	clientCfg.UpstreamBaseURL = mock.URL()
	client, err := upstream.New(cfg.AuthTokens[0], &clientCfg)
	if err != nil {
		t.Fatal(err)
	}
	sessions := []*session.Manager{session.NewManager(client)}
	reg := registry.New(cfg, nil)
	reg.LoadFallback()
	p, err := pool.New(cfg, []*upstream.Client{client}, sessions, reg)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(cfg, p, reg, nil, nil, "config.json")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cookie := authedCookie(t, ts)

	resp := postJSON(t, ts.URL, cookie, "/admin/tokens/add", `{"token":"tok-new"}`)
	body := bodyOf(t, resp)
	if !strings.Contains(body, "Token added at index 1") {
		t.Fatalf("add response = %q, want success", body)
	}

	cfgBytes, err := os.ReadFile("config.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(cfgBytes, &doc); err != nil {
		t.Fatalf("config.json invalid after add: %v\n%s", err, cfgBytes)
	}
	if tok := string(doc["AUTH_TOKENS"]); tok != `["tok-0","tok-new"]` {
		t.Errorf("AUTH_TOKENS = %s, want [\"tok-0\",\"tok-new\"]", tok)
	}
	if !strings.Contains(string(cfgBytes), `"SAFE_MODE"`) || !strings.Contains(string(cfgBytes), mock.URL()) {
		t.Errorf("config.json lost unrelated keys:\n%s", cfgBytes)
	}

	envBytes, _ := os.ReadFile(".env")
	if !strings.Contains(string(envBytes), "tok-new") {
		t.Errorf(".env missing tok-new:\n%s", envBytes)
	}

	// Remove the new token and verify config.json is back to one token.
	resp = postJSON(t, ts.URL, cookie, "/admin/tokens/remove", `{"index":1}`)
	if !strings.Contains(bodyOf(t, resp), "Token removed") {
		t.Fatalf("remove failed")
	}
	cfgBytes, _ = os.ReadFile("config.json")
	var doc2 map[string]json.RawMessage
	json.Unmarshal(cfgBytes, &doc2)
	if tok := string(doc2["AUTH_TOKENS"]); tok != `["tok-0"]` {
		t.Errorf("after remove AUTH_TOKENS = %s, want [\"tok-0\"]", tok)
	}
}

// TestDashboardTokenAddUpdatesConfigFileBridgeMode starts from a bridge-mode
// config.json (no AUTH_TOKENS key) and adds the first token via dashboard.
func TestDashboardTokenAddUpdatesConfigFileBridgeMode(t *testing.T) {
	t.Chdir(t.TempDir())
	mock := testutil.NewMock()
	defer mock.Close()

	cfgContent := "{\n  \"UPSTREAM_BASE_URL\": \"" + mock.URL() + "\",\n  \"SAFE_MODE\": true,\n  \"ADMIN_TOKEN\": \"secret\"\n}\n"
	if err := os.WriteFile("config.json", []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AuthTokens: nil, RotationInterval: time.Hour,
		RequestTimeout: 15 * time.Minute, SessionCallTimeout: 5 * time.Second,
		RegistryRefresh: 6 * time.Hour, UpstreamBaseURL: mock.URL(),
		AdminToken: "secret", DashboardEnabled: true,
	}
	reg := registry.New(cfg, nil)
	reg.LoadFallback()
	p, err := pool.New(cfg, nil, nil, reg)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(cfg, p, reg, nil, nil, "config.json")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	cookie := authedCookie(t, ts)

	resp := postJSON(t, ts.URL, cookie, "/admin/tokens/add", `{"token":"tok-first"}`)
	if !strings.Contains(bodyOf(t, resp), "Token added at index 0") {
		t.Fatalf("add failed: %s", bodyOf(t, resp))
	}

	cfgBytes, _ := os.ReadFile("config.json")
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(cfgBytes, &doc); err != nil {
		t.Fatalf("config.json invalid after add:\n%s\n%v", cfgBytes, err)
	}
	if tok := string(doc["AUTH_TOKENS"]); tok != `["tok-first"]` {
		t.Errorf("AUTH_TOKENS = %s, want [\"tok-first\"]", tok)
	}
	if !strings.Contains(string(cfgBytes), `"SAFE_MODE"`) {
		t.Errorf("config.json lost SAFE_MODE:\n%s", cfgBytes)
	}
}
