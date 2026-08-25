// bridge_metrics_test.go — bridge-specific Prometheus metrics and label
// hygiene: entries, cooling-down, dead-token, per-entry requests, and the
// guarantee that raw client tokens never appear as metric labels (#2x).
package server_test

import (
	"net/http"
	"strings"
	"testing"

	"freebuff-proxy/internal/testutil"
)

func TestBridgeMetricsEmitted(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	mock.ChatBody = testutil.SSEEvent(chunk("chatcmpl-bm", 1, `"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]`))
	ts, p := newBridgeTestServer(t, mock)

	// Make a bridged chat so a bridge entry exists with one request.
	resp, data := doJSON(t, http.MethodPost, ts.URL+"/v1/chat/completions", chatBody(modelA),
		map[string]string{"Authorization": "Bearer label-hygiene-tok"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d, want 200: %s", resp.StatusCode, data)
	}

	// Fetch metrics and verify the bridge metrics block.
	resp, data = doJSON(t, http.MethodGet, ts.URL+"/metrics", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200: %s", resp.StatusCode, data)
	}
	body := string(data)
	for _, want := range []string{
		"# HELP freebuff_proxy_bridge_entries_total",
		"# TYPE freebuff_proxy_bridge_entries_total gauge",
		"freebuff_proxy_bridge_entries_total 1",
		"# HELP freebuff_proxy_bridge_cooling_down_total",
		"freebuff_proxy_bridge_cooling_down_total 0",
		"# HELP freebuff_proxy_bridge_dead_tokens_total",
		"freebuff_proxy_bridge_dead_tokens_total 0",
		"# HELP freebuff_proxy_bridge_locked_total",
		"freebuff_proxy_bridge_locked_total 0",
		"# HELP freebuff_proxy_bridge_requests_total",
		`freebuff_proxy_bridge_requests_total{token_label=`,
		"# HELP freebuff_proxy_bridge_active_runs",
		"# HELP freebuff_proxy_bridge_quota_remaining",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q in:\n%s", want, body)
		}
	}

	// Only 1 bridge entry (may be +1 more if a second entry slipped in).
	_ = p
}

func TestBridgeMetricsNoRawTokenLabels(t *testing.T) {
	// The bridge metrics must never leak the raw client token as a label
	// value. The token_key label comes from the SHA-256 hash prefix only.
	mock := testutil.NewMock()
	defer mock.Close()
	mock.ChatBody = testutil.SSEEvent(chunk("chatcmpl-bm2", 1, `"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]`))
	ts, _ := newBridgeTestServer(t, mock)

	const rawToken = "cb_super_secret_token_xyz_12345"
	resp, data := doJSON(t, http.MethodPost, ts.URL+"/v1/chat/completions", chatBody(modelA),
		map[string]string{"Authorization": "Bearer " + rawToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d, want 200: %s", resp.StatusCode, data)
	}

	resp, data = doJSON(t, http.MethodGet, ts.URL+"/metrics", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200: %s", resp.StatusCode, data)
	}
	body := string(data)
	if strings.Contains(body, rawToken) {
		t.Errorf("metrics leak raw client token %q:\n%s", rawToken, body)
	}
	// Healthz must not leak the raw token either.
	resp, data = doJSON(t, http.MethodGet, ts.URL+"/healthz", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200: %s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), rawToken) {
		t.Errorf("healthz leaked raw client token %q:\n%s", rawToken, string(data))
	}
}
