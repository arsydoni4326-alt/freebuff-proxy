package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetJSONObjectKeyReplacesMember pins the in-place AUTH_TOKENS splice:
// only the member's span changes, every other byte (key order, indentation,
// unrelated keys, trailing newline) survives.
func TestSetJSONObjectKeyReplacesMember(t *testing.T) {
	in := "{\n  \"LISTEN_ADDR\": \"127.0.0.1:3457\",\n  \"AUTH_TOKENS\": [\"cb_old\"],\n  \"SAFE_MODE\": true,\n  \"LOG_RING_SIZE\": 750\n}\n"
	val := []byte(`["cb_old","cb_new"]`)
	out, err := setJSONObjectKey([]byte(in), "AUTH_TOKENS", val)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"LISTEN_ADDR\": \"127.0.0.1:3457\",\n  \"AUTH_TOKENS\": [\"cb_old\",\"cb_new\"],\n  \"SAFE_MODE\": true,\n  \"LOG_RING_SIZE\": 750\n}\n"
	if string(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	if !json.Valid(out) {
		t.Errorf("output is not valid JSON: %s", out)
	}
}

// TestSetJSONObjectKeySingleLine covers the compact one-line document shape.
func TestSetJSONObjectKeySingleLine(t *testing.T) {
	out, err := setJSONObjectKey([]byte(`{"UPSTREAM_BASE_URL":"https://codebuff.com","AUTH_TOKENS":["a"]}`), "AUTH_TOKENS", []byte(`["a","b"]`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"UPSTREAM_BASE_URL":"https://codebuff.com","AUTH_TOKENS": ["a","b"]}`; string(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// TestSetJSONObjectKeyAppends covers the config file that does not yet carry
// an AUTH_TOKENS member (bridge-mode JSON configs): the member is appended
// before the closing brace and the document stays valid.
func TestSetJSONObjectKeyAppends(t *testing.T) {
	in := "{\n  \"SAFE_MODE\": true,\n  \"BRIDGE_ENABLED\": false\n}"
	out, err := setJSONObjectKey([]byte(in), "AUTH_TOKENS", []byte(`["cb_tok"]`))
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"SAFE_MODE\": true,\n  \"BRIDGE_ENABLED\": false,\n  \"AUTH_TOKENS\": [\"cb_tok\"]\n}"; string(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("appended output not valid JSON: %v\n%s", err, out)
	}
	if got := string(doc["AUTH_TOKENS"]); got != `["cb_tok"]` {
		t.Errorf("AUTH_TOKENS after append = %s, want [\"cb_tok\"]", got)
	}
}

// TestSetJSONObjectKeyEmptyObject covers appending into an empty JSON object.
func TestSetJSONObjectKeyEmptyObject(t *testing.T) {
	out, err := setJSONObjectKey([]byte(`{ }`), "AUTH_TOKENS", []byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("empty-object append not valid JSON: %v\n%s", err, out)
	}
	if got := string(doc["AUTH_TOKENS"]); got != `[]` {
		t.Errorf("AUTH_TOKENS = %s, want []", got)
	}
}

// TestSetJSONObjectKeyIgnoresNestedSameKey ensures only the top-level member
// is replaced: a nested object (e.g. under an unknown key) that contains the
// same key text must be left alone, and an absent top-level member appends.
func TestSetJSONObjectKeyIgnoresNestedSameKey(t *testing.T) {
	in := `{"nested":{"AUTH_TOKENS":["keep"]}}`
	out, err := setJSONObjectKey([]byte(in), "AUTH_TOKENS", []byte(`["top"]`))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if got := string(doc["AUTH_TOKENS"]); got != `["top"]` {
		t.Errorf("top-level AUTH_TOKENS = %s, want [\"top\"]", got)
	}
	if !strings.Contains(string(doc["nested"]), `"keep"`) {
		t.Errorf("nested AUTH_TOKENS was disturbed: %s", doc["nested"])
	}
}

// TestSetJSONObjectKeyLiteralValueBeforeKey ensures value scanning handles a
// boolean / number member before AUTH_TOKENS (no brace confusion).
func TestSetJSONObjectKeyLiteralValueBeforeKey(t *testing.T) {
	in := `{"enabled":true,"count":5,"AUTH_TOKENS":["a","b"]}`
	out, err := setJSONObjectKey([]byte(in), "AUTH_TOKENS", []byte(`["a"]`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"enabled":true,"count":5,"AUTH_TOKENS": ["a"]}`; string(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// TestSetJSONObjectKeyRejectsNonObject pins the guard: only JSON objects can
// carry an AUTH_TOKENS member.
func TestSetJSONObjectKeyRejectsNonObject(t *testing.T) {
	if _, err := setJSONObjectKey([]byte(`["not","an","object"]`), "AUTH_TOKENS", []byte(`[]`)); err == nil {
		t.Error("array document accepted, want error")
	}
}

// TestUpdateAuthTokensJSONFileRoundTrip exercises the full file write path:
// BOM stripping, unrelated-key preservation, and json-valid output that the
// config loader can read back.
func TestUpdateAuthTokensJSONFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	orig := "{\n  \"UPSTREAM_BASE_URL\": \"https://codebuff.com\",\n  \"AUTH_TOKENS\": [\"cb_a\", \"cb_b\"],\n  \"SAFE_MODE\": false\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateAuthTokensJSONFile(path, []string{"cb_a", "cb_b", "cb_c"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(got) {
		t.Fatalf("written config.json is not valid JSON:\n%s", got)
	}
	if !strings.Contains(string(got), `"cb_c"`) {
		t.Errorf("config.json missing the added token:\n%s", got)
	}
	if !strings.Contains(string(got), `"SAFE_MODE": false`) || !strings.Contains(string(got), `https://codebuff.com`) {
		t.Errorf("config.json lost unrelated members:\n%s", got)
	}
}

// TestUpdateAuthTokensJSONFileEmptyTokens ensures an explicit empty list is
// written as [] (never null), which the loader treats as set-but-empty.
func TestUpdateAuthTokensJSONFileEmptyTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"AUTH_TOKENS":["cb_a"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateAuthTokensJSONFile(path, []string{}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "null") {
		t.Errorf("empty list written as null:\n%s", got)
	}
	if !json.Valid(got) {
		t.Fatalf("written config.json not valid:\n%s", got)
	}
}

// TestUpdateAuthTokensJSONFileNilTokensAppendsKey covers a bridge-mode config
// file without an AUTH_TOKENS member: the first token add appends it.
func TestUpdateAuthTokensJSONFileNilTokensAppendsKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"BRIDGE_ENABLED": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateAuthTokensJSONFile(path, []string{"cb_first"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, got)
	}
	if tok := string(doc["AUTH_TOKENS"]); tok != `["cb_first"]` {
		t.Errorf("AUTH_TOKENS = %s, want [\"cb_first\"]", tok)
	}
}

// TestUpdateAuthTokensJSONFileNoopWithoutPath pins that no -config path means
// no file access at all (plain .env deployments stay untouched).
func TestUpdateAuthTokensJSONFileNoopWithoutPath(t *testing.T) {
	if err := updateAuthTokensJSONFile("", []string{"cb_x"}); err != nil {
		t.Fatalf("empty path must be a no-op, got %v", err)
	}
}

// TestConfigJSONSnapshotRestore pins snapshot/restore symmetry used by the
// persist → verify → rollback path.
func TestConfigJSONSnapshotRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	orig := []byte(`{"AUTH_TOKENS":["cb_orig"]}`)
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	old, oldErr := configJSONSnapshot(path)
	if oldErr != nil || string(old) != string(orig) {
		t.Fatalf("snapshot = (%q, %v), want original bytes", old, oldErr)
	}
	if err := updateAuthTokensJSONFile(path, []string{"cb_new"}); err != nil {
		t.Fatal(err)
	}
	restoreConfigJSONSnapshot(path, old, oldErr)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(orig) {
		t.Errorf("after restore = %q, want byte-exact original %q", got, orig)
	}
	// Empty path: snapshot/restore are no-ops.
	if old, err := configJSONSnapshot(""); old != nil || err != nil {
		t.Errorf("empty-path snapshot = (%v, %v), want (nil, nil)", old, err)
	}
	restoreConfigJSONSnapshot("", nil, nil) // must not panic
}
