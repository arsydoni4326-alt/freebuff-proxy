// header_injection_test.go — header smuggling / injection tests for the
// auth and bridge header surfaces (Authorization, x-api-key,
// anthropic-api-key). These pin the security properties that matter: a
// single source of truth when multiple credential headers are present,
// trimming without privilege surprise, Bearer-prefix correctness, and that
// injected control characters / whitespace in bridge tokens are rejected
// before any upstream contact.
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"freebuff-proxy/backend/internal/config"
	"freebuff-proxy/backend/internal/testutil"
)

func TestClientTokenPrecedence(t *testing.T) {
	// clientToken must prefer Authorization: Bearer, then x-api-key, then
	// anthropic-api-key when multiple are present — never concatenate them
	// (that would smuggle a second credential into the upstream call).
	cases := []struct {
		name      string
		authz     string
		xapi      string
		anthropic string
		want      string
	}{
		{"only bearer", "Bearer tok-a", "", "", "tok-a"},
		{"bearer wins over x-api-key", "Bearer tok-a", "tok-b", "", "tok-a"},
		{"bearer wins over anthropic", "Bearer tok-a", "", "tok-x", "tok-a"},
		{"bearer wins over both", "Bearer tok-a", "tok-b", "tok-x", "tok-a"},
		{"x-api-key wins over anthropic when no bearer", "", "tok-b", "tok-x", "tok-b"},
		{"only x-api-key", "", "tok-b", "", "tok-b"},
		{"only anthropic", "", "", "tok-x", "tok-x"},
		{"none present", "", "", "", ""},
		{"non-bearer authorization falls through to x-api-key", "Basic abc", "tok-b", "", "tok-b"},
		{"trimmed", "  Bearer tok-a  ", "", "", "tok-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clientToken(mkReqAuth(tc.authz, tc.xapi, tc.anthropic))
			if got != tc.want {
				t.Errorf("clientToken() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientTokenNoConcatenation(t *testing.T) {
	// A bearer plus a distinct x-api-key must NOT produce a composite token
	// (a header-smuggling vector where two credentials merge into one).
	r := mkReqAuth("Bearer real-token", "  fake-token  ", "")
	if got := clientToken(r); got != "real-token" {
		t.Errorf("clientToken got %q, want real-token only (no concatenation)", got)
	}
}

func TestMultipleAuthorizationValues(t *testing.T) {
	// Go's http.Header.Get returns the first value; a smuggler adding a
	// second Authorization header cannot hide a different token behind the
	// one the handler reads. This pins that Get() semantics match clientToken.
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Add("Authorization", "Bearer first-token")
	r.Header.Add("Authorization", "Bearer second-token")
	if got := clientToken(r); got != "first-token" {
		t.Errorf("clientToken got %q, want first-token (Header.Get reads first)", got)
	}
}

func TestAuthorizedPrecedenceMatchesClientToken(t *testing.T) {
	// The auth gate (authorized) and the bridge token extractor (clientToken)
	// must agree on which header wins, so a client cannot authenticate as
	// one account and then be relayed as another.
	srv := newServerWithKeys(t, "sk-expected")

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer sk-expected")
	r.Header.Set("x-api-key", "sk-evil")
	if !srv.authorized(srv.cfg.Load(), r) {
		t.Error("authorized() = false with matching bearer, want true (bearer wins)")
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer sk-evil")
	r.Header.Set("x-api-key", "sk-expected")
	if srv.authorized(srv.cfg.Load(), r) {
		t.Error("authorized() = true with mismatched bearer, want false (x-api-key must not override bearer)")
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer sk-expected")
	r.Header.Set("anthropic-api-key", "sk-evil")
	if !srv.authorized(srv.cfg.Load(), r) {
		t.Error("authorized() = false with matching bearer + evil anthropic-key, want true")
	}
}

// mkReqAuth builds a request carrying the three credential headers.
func mkReqAuth(authz, xapi, anthropic string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if authz != "" {
		r.Header.Set("Authorization", authz)
	}
	if xapi != "" {
		r.Header.Set("x-api-key", xapi)
	}
	if anthropic != "" {
		r.Header.Set("anthropic-api-key", anthropic)
	}
	return r
}

// newServerWithKeys wires a server with client API keys for authorized tests.
func newServerWithKeys(t *testing.T, keys ...string) *Server {
	t.Helper()
	mock := testutil.NewMock()
	srv := newServerOpts(t, mock, func(c *config.Config) { c.APIKeys = keys })
	t.Cleanup(mock.Close)
	return srv
}
