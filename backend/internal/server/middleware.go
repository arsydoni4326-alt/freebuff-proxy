package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// statusWriter captures the response status for access logging. It forwards
// Flusher/Hijacker/Pusher so streaming and similar protocols keep working
// through the access-log wrapper.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

func (w *statusWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

// corsOrigin returns the configured Access-Control-Allow-Origin, treating an
// empty value as the "*" default (an empty .env line must not disable CORS).
func (s *Server) corsOrigin() string {
	origin := strings.TrimSpace(s.cfg.Load().CORSAllowedOrigin)
	if origin == "" {
		return "*"
	}
	return origin
}

// corsMiddleware answers CORS preflights on the /v1/* API surface and stamps
// the allow headers on /v1/* responses. An OPTIONS request for any /v1/*
// path is answered with 204 before the route table sees it (so unknown
// /v1/* subpaths still get a clean preflight, matching the reference
// proxy-freebuff OPTIONS → 204). Admin routes pass through untouched.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			h := w.Header()
			origin := s.corsOrigin()
			h.Set("Access-Control-Allow-Origin", origin)
			// When the origin is pinned (not "*"), vary on Origin so caches
			// never serve the pinned header to a different requester.
			if origin != "*" {
				h.Add("Vary", "Origin")
			}
			// The allow-list must cover every credential/header the auth path
			// accepts (x-api-key, anthropic-api-key) and the Anthropic beta
			// header real clients carry — a browser preflight fails silently
			// otherwise ("header not allowed") and the real request never
			// fires.
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-api-key, anthropic-version, anthropic-beta")
			h.Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// quietAccessPath reports whether path is a poll/fire-and-forget endpoint
// whose access lines are rate-limited (T17): /healthz, /metrics, and CORS
// OPTIONS preflights. Unknown paths (which 404) are gated the same way by
// the access wrapper — an arbitrary-path client must not flood the log.
func quietAccessPath(method, path string) bool {
	return path == "/healthz" || path == "/metrics" || method == http.MethodOptions
}

// accessQuietWindow is the quiet-endpoint access gate window: at most one
// access line per path per window (T17). A var so tests can shrink it.
var accessQuietWindow = 60 * time.Second

// accessLogGate is the per-process quiet-path access gate: map[path]lastLog
// plus a mutex (T17). The map is CAPPED: OPTIONS preflights and unknown
// paths arrive for arbitrary distinct paths, so unbounded membership would
// leak memory; accessLogDue evicts the oldest entry past the cap.
var accessLogGate = struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
}{lastSeen: make(map[string]time.Time)}

// maxAccessGateEntries bounds accessLogGate.lastSeen. The gate's quiet
// candidates are paths, and a client can mint unlimited distinct paths
// (OPTIONS preflights, unknown-path 404s), so the map must never grow past
// this cap: when full the OLDEST entry is evicted before a new one is
// recorded.
const maxAccessGateEntries = 512

// accessLogDue reports whether an access line may fire for path now,
// recording the current attempt. The first request for a path and any
// request at least accessQuietWindow after the last line fire; requests
// inside the window are suppressed. The map stays bounded: past
// maxAccessGateEntries the oldest entry is evicted.
func accessLogDue(path string, now time.Time) bool {
	accessLogGate.mu.Lock()
	defer accessLogGate.mu.Unlock()
	last, ok := accessLogGate.lastSeen[path]
	if !ok || now.Sub(last) >= accessQuietWindow {
		accessLogGate.lastSeen[path] = now
		// Bound the gate: arbitrary distinct paths must not grow the
		// process-global map without limit.
		if len(accessLogGate.lastSeen) > maxAccessGateEntries {
			oldestPath, oldest := "", time.Time{}
			for p, t := range accessLogGate.lastSeen {
				if oldestPath == "" || t.Before(oldest) {
					oldestPath, oldest = p, t
				}
			}
			delete(accessLogGate.lastSeen, oldestPath)
		}
		return true
	}
	return false
}

// accessQuietBudget is the per-process cap on quiet-class access lines
// (healthz/metrics/OPTIONS preflights/unknown-path 404s): per-path gating
// alone lets a flood of DISTINCT paths emit one line each, so a global
// budget bounds the total quiet lines per accessQuietWindow.
var accessQuietBudget = struct {
	mu          sync.Mutex
	windowStart time.Time
	lines       int
}{}

// maxQuietAccessLines is the budget of quiet-class access lines per
// accessQuietWindow. Distinct quiet paths beyond it are silent; the window
// rolls the budget over.
const maxQuietAccessLines = 60

// accessQuietBudgetDue reports whether the quiet-class line budget remains
// and charges one line. The window rolls on first use after expiry.
func accessQuietBudgetDue(now time.Time) bool {
	accessQuietBudget.mu.Lock()
	defer accessQuietBudget.mu.Unlock()
	if accessQuietBudget.windowStart.IsZero() || now.Sub(accessQuietBudget.windowStart) >= accessQuietWindow {
		accessQuietBudget.windowStart = now
		accessQuietBudget.lines = 0
	}
	if accessQuietBudget.lines >= maxQuietAccessLines {
		return false
	}
	accessQuietBudget.lines++
	return true
}

// resetAccessLogGate clears the quiet-path access gate and budget (test hook).
func resetAccessLogGate() {
	accessLogGate.mu.Lock()
	clear(accessLogGate.lastSeen)
	accessLogGate.mu.Unlock()
	accessQuietBudget.mu.Lock()
	accessQuietBudget.windowStart = time.Time{}
	accessQuietBudget.lines = 0
	accessQuietBudget.mu.Unlock()
}

// mustSubFS returns the named subtree of an embed.FS. The directory is
// embedded at compile time, so a missing subtree is an invariant violation,
// not a runtime condition.
func mustSubFS(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("dashboard: embedded subtree missing: " + err.Error())
	}
	return sub
}

// noDirListing rejects directory requests so FileServerFS never renders an
// index listing of the embedded assets.
func noDirListing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// remoteHost returns the client host without the port.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- Auth Middleware & Helpers ---

// requireAuth wraps a handler with client-auth enforcement. When no API keys
// are configured the handler passes through untouched; /healthz is always
// exempt (the caller wires it without requireAuth). Bridge mode (no
// AUTH_TOKENS) also passes through: the Authorization header IS the upstream
// token there, and API_KEYS is meaningless.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.cfg.Load()
		// Hybrid mode (AUTH_TOKENS + BRIDGE_ENABLED) passes through too:
		// the per-request decision — pooled vs bridge — happens in
		// chatCore, where a credential matching API_KEYS uses the pool and
		// any other credential is relayed as a bridge token.
		if len(cfg.APIKeys) == 0 || cfg.BridgeMode() || cfg.HybridBridgeMode() {
			next(w, r)
			return
		}
		if !s.authorized(r) {
			s.writeJSONError(w, http.StatusUnauthorized,
				"Invalid API key", "invalid_request_error", "invalid_api_key", 0)
			return
		}
		next(w, r)
	}
}

// extractBearerToken extracts the token from an Authorization header if it has
// a case-insensitive "Bearer " prefix (per RFC 7235 / RFC 6750). Returns the
// trimmed token and true if the prefix matches, or ("", false) otherwise.
func extractBearerToken(authHeader string) (string, bool) {
	authHeader = strings.TrimSpace(authHeader)
	if len(authHeader) >= 7 && strings.EqualFold(authHeader[:7], "bearer ") {
		return strings.TrimSpace(authHeader[7:]), true
	}
	return "", false
}

// authorized reports whether the request carries a configured API key,
// either as "Authorization: Bearer <key>", "x-api-key: <key>", or
// "anthropic-api-key: <key>". Comparison is constant-time against every
// configured key.
func (s *Server) authorized(r *http.Request) bool {
	cfg := s.cfg.Load()
	provided := ""
	if tok, ok := extractBearerToken(r.Header.Get("Authorization")); ok {
		provided = tok
	} else if h := r.Header.Get("x-api-key"); h != "" {
		provided = h
	} else if h := r.Header.Get("anthropic-api-key"); h != "" {
		provided = h
	}
	if provided == "" {
		return false
	}
	for _, key := range cfg.APIKeys {
		if subtle.ConstantTimeCompare([]byte(provided), []byte(key)) == 1 {
			return true
		}
	}
	return false
}

// requireAdminToken guards POST /admin/reload when ADMIN_TOKEN is set: the
// request must present it as "Authorization: Bearer <token>" (constant-time
// compare). When ADMIN_TOKEN is unset the handler passes through untouched —
// the loopback gate (adminSensitive, wired between requireAdminToken and
// requireAuth) and the legacy API_KEYS gate then apply, and main.go logs a
// startup warning for the open (default) case.
func (s *Server) requireAdminToken(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.cfg.Load()
		if cfg.AdminToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		provided := ""
		if tok, ok := extractBearerToken(r.Header.Get("Authorization")); ok {
			provided = tok
		}
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.AdminToken)) != 1 {
			s.writeJSONError(w, http.StatusUnauthorized,
				"Invalid admin token", "invalid_request_error", "invalid_admin_token", 0)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// clientToken returns the request's bearer token (Authorization: Bearer,
// x-api-key, or anthropic-api-key), trimmed. Empty when the request carries
// none. In bridge mode this token IS the client's FreeBuff token relayed
// upstream.
func clientToken(r *http.Request) string {
	provided := ""
	if tok, ok := extractBearerToken(r.Header.Get("Authorization")); ok {
		provided = tok
	} else if h := r.Header.Get("x-api-key"); h != "" {
		provided = h
	} else if h := r.Header.Get("anthropic-api-key"); h != "" {
		provided = h
	}
	return strings.TrimSpace(provided)
}

// bearerToken returns only the Authorization: Bearer token (the
// Authorization header value without the "Bearer " prefix). Returns "" if
// no Bearer token is present.
func bearerToken(r *http.Request) string {
	if tok, ok := extractBearerToken(r.Header.Get("Authorization")); ok {
		return tok
	}
	return ""
}

// --- Correlation IDs ---

// reqIDKey carries the per-request correlation id (req_id) through the
// request context. The key type is unexported so only this package can
// read/write it; the upstream client threads the same id a second way (via
// ChatOptions.RequestID) for its do()/retry log lines.
type reqIDKey struct{}

// reqIDFrom returns the request's correlation id, or "" when the request
// did not pass through the access wrapper (direct handler calls in tests).
func reqIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(reqIDKey{}).(string)
	return id
}

// originalBodyKey carries the client's raw request body (issue #140):
// handlers normalize+rename tools into a separate buffer, and chatCore needs
// the ORIGINAL names to build the response-side restore map.
type originalBodyKey struct{}

func withOriginalBody(ctx context.Context, body []byte) context.Context {
	return context.WithValue(ctx, originalBodyKey{}, body)
}

func originalBodyFromContext(ctx context.Context) []byte {
	b, _ := ctx.Value(originalBodyKey{}).([]byte)
	return b
}

// newReqID mints a UUIDv4 correlation id from crypto/rand (RFC 4122 §4.4:
// 122 random bits, version 4, variant 1). A rand failure is unrecoverable
// in practice; fall back to a time-seeded hex id rather than failing the
// request.
func newReqID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// clientRequestID sanitizes the client's X-Request-Id header for logging:
// trimmed, printable ASCII only (0x20-0x7e), max 64 runes. Returns "" when
// the header is absent or fails the checks — the field is then omitted from
// log lines (the proxy never trusts a client-supplied id as its correlation
// key, D1).
func clientRequestID(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("X-Request-Id"))
	if v == "" || utf8.RuneCountInString(v) > 64 {
		return ""
	}
	for _, b := range []byte(v) {
		if b < 0x20 || b > 0x7e {
			return ""
		}
	}
	return v
}
