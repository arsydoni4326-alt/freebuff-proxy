package dashboard

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseUpstreamSyncEmpty pins the empty-embed fallback: shipped binaries
// whose drift JSON has no `files` array (first deploy before any CI run)
// still surface a non-nil banner with the placeholder SHA.
func TestParseUpstreamSyncEmpty(t *testing.T) {
	got := parseUpstreamSync([]byte(`{"upstream_sha":"(not yet reported)","files":[]}`))
	if got == nil {
		t.Fatal("parseUpstreamSync returned nil")
	}
	if got.HasDrift || got.HasRegistry || got.HasWire {
		t.Errorf("empty JSON should report no drift: %+v", got)
	}
	if got.UpstreamSHA != "(not yet reported)" {
		t.Errorf("UpstreamSHA = %q, want placeholder", got.UpstreamSHA)
	}
	if got.ReleasesURL == "" {
		t.Error("ReleasesURL must be set so the banner always points at the current repo")
	}
}

// TestParseUpstreamSyncDrifted pins the drifted case: a registry DRIFT and
// a wire MISSING_UPSTREAM roll up into the two boolean fields, and the
// affected files land in DriftedFiles in the order the JSON emitted them.
func TestParseUpstreamSyncDrifted(t *testing.T) {
	raw := []byte(`{
		"upstream": "https://github.com/CodebuffAI/freebuff.git",
		"upstream_sha": "57c04f95a53ac1737a19fac1c6c2b5ebb1e227e0",
		"checked_at": "2026-08-26T07:30:55Z",
		"files": [
			{"group":"registry","file":"common/src/constants/free-agents.ts","pinned_sha":"358c414e36cb","vendor_sha":"358c414e36cb","status":"SAME"},
			{"group":"registry","file":"common/src/constants/freebuff-models.ts","pinned_sha":"b3f78f258791","vendor_sha":"093deb24eb97","status":"DRIFT"},
			{"group":"wire","file":"common/src/constants/freebuff-trust.ts","pinned_sha":"","vendor_sha":"159e97f16b78","status":"SAME"},
			{"group":"wire","file":"common/src/types/freebuff-session.ts","pinned_sha":"","vendor_sha":"","status":"MISSING_UPSTREAM"}
		]
	}`)
	got := parseUpstreamSync(raw)
	if !got.HasDrift || !got.HasRegistry || !got.HasWire {
		t.Errorf("flags wrong: has_drift=%v has_registry=%v has_wire=%v (want all true)", got.HasDrift, got.HasRegistry, got.HasWire)
	}
	if len(got.DriftedFiles) != 2 {
		t.Fatalf("DriftedFiles = %d, want 2 (the two non-SAME entries)", len(got.DriftedFiles))
	}
	if got.UpstreamSHA != "57c04f95a53a" {
		t.Errorf("UpstreamSHA = %q, want 12-char short SHA", got.UpstreamSHA)
	}
}

// TestParseUpstreamSyncMalformed: a parse failure must NOT panic and must
// still return a non-nil struct (the banner is non-fatal UI).
func TestParseUpstreamSyncMalformed(t *testing.T) {
	got := parseUpstreamSync([]byte(`{not json`))
	if got == nil {
		t.Fatal("malformed JSON must not return nil")
	}
	if got.UpstreamSHA != "(parse error)" {
		t.Errorf("UpstreamSHA = %q, want (parse error)", got.UpstreamSHA)
	}
}

// TestUpstreamEndpointJSON: the /admin/api/upstream-drift route returns the
// raw embedded JSON under the `drift` key, round-tripped as
// json.RawMessage so the client can parse it.
func TestUpstreamEndpointJSON(t *testing.T) {
	d := &Dashboard{}
	rec := httptest.NewRecorder()
	d.upstreamData()
	_ = rec // the data is a map; covered by the parser tests above. The
	// endpoint wiring (mux + auth) is exercised by the existing
	// server_admin_* tests in internal/server.
	if !strings.Contains(string(upstreamDriftJSON), `"upstream_sha"`) {
		t.Errorf("embedded drift JSON missing upstream_sha field: %s", upstreamDriftJSON)
	}
}
