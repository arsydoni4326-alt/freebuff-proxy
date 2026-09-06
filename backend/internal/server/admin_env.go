package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"freebuff-proxy/backend/internal/config"
	"freebuff-proxy/backend/internal/dashboard"
	"freebuff-proxy/backend/internal/upstream"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxEnvSize = 64 << 10

func readBounded(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	got, err := io.ReadFull(r, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:got], nil
}

// updateEnvKeys reads the resolved .env file, applies the line edits and
// writes it back atomically — the single read-modify-write contract shared
// with the dashboard (issue #234).
func updateEnvKeys(updates []config.EnvUpdate) ([]byte, error) {
	_, content, exists, err := config.EnvFileInfo()
	if err != nil {
		return nil, err
	}
	if !exists {
		content = nil
	}
	out, err := config.ApplyEnvUpdates(content, updates)
	if err != nil {
		return nil, err
	}
	if err := config.WriteEnvFile(out); err != nil {
		return nil, err
	}
	return out, nil
}

func updateAuthTokensEnv(tokens []string) ([]byte, error) {
	// AUTH_TOKENS is comma-joined in .env: a token carrying an
	// interior comma would split into two on the next reload, corrupting
	// the file the pool was built from. Reject the whole update — the
	// caller rolls its pool mutation back.
	for i, tok := range tokens {
		if strings.Contains(tok, ",") {
			return nil, fmt.Errorf("AUTH_TOKENS entry %d contains a comma (AUTH_TOKENS is comma-separated in .env)", i+1)
		}
	}
	return updateEnvKeys([]config.EnvUpdate{{Key: "AUTH_TOKENS", Value: strings.Join(tokens, ",")}})
}

func restoreEnvFile(old []byte, oldErr error) {
	path := config.EnvFileForWrite()
	switch {
	case oldErr == nil:
		_ = config.WriteFileAtomic(path, old)
	case errors.Is(oldErr, os.ErrNotExist):
		_ = os.Remove(path)
	}
}

// configJSONSnapshot returns the current bytes of the -config JSON file (path
// "" when the process was started without -config). Token mutations snapshot
// it alongside the .env so a failed persist → verify → rollback restores the
// operator's config file byte-exact too, never just the .env.
func configJSONSnapshot(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	return os.ReadFile(path)
}

// restoreConfigJSONSnapshot writes a configJSONSnapshot back, or removes the
// file when it did not exist before the mutation (mirrors restoreEnvFile).
func restoreConfigJSONSnapshot(path string, old []byte, oldErr error) {
	if path == "" {
		return
	}
	switch {
	case oldErr == nil:
		_ = config.WriteFileAtomic(path, old)
	case errors.Is(oldErr, os.ErrNotExist):
		_ = os.Remove(path)
	}
}

// updateAuthTokensJSONFile mirrors a new AUTH_TOKENS list into the -config
// JSON file — the path the process was started with via `-config`, "" = none
// (then it is a no-op). A deployment that keeps its tokens in that file (e.g.
// `-config /app/config.json` in a container) must see dashboard add/remove/
// swap/move mutations land there, not only in .env or the SQLite token DB, or
// a container recreate / restart silently drops the change.
//
// Only the top-level AUTH_TOKENS member is replaced (or appended when the file
// has none): key order, indentation, unknown keys, and the trailing newline
// survive byte-exact, unlike a full re-marshal. Atomic temp+rename write, 0600
// like the .env contract.
func updateAuthTokensJSONFile(path string, tokens []string) error {
	if path == "" {
		return nil
	}
	// A nil slice would marshal to `null` (valid JSON but not an array); an
	// explicitly empty list must stay an array so the loaded config records
	// AUTH_TOKENS as set (bridge mode) rather than absent.
	if tokens == nil {
		tokens = []string{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	// Mirror config.loadRaw: strip a leading UTF-8 BOM so the byte offsets of
	// the splice below match the parsed document.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	val, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("encode AUTH_TOKENS: %w", err)
	}
	out, err := setJSONObjectKey(data, "AUTH_TOKENS", val)
	if err != nil {
		return fmt.Errorf("update AUTH_TOKENS in %s: %w", path, err)
	}
	if err := config.WriteFileAtomic(path, out); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// setJSONObjectKey replaces (or appends) one top-level member of a JSON object
// document, preserving every other byte of the document. val must already be
// marshaled JSON. When the key is present, only its member span is replaced
// (the indentation before the key is kept); when absent, the member is
// appended before the closing brace. Nested objects/arrays that happen to
// contain the same key text are never touched (only top-level members count).
func setJSONObjectKey(data []byte, key string, val []byte) ([]byte, error) {
	i := skipJSONSpace(data, 0)
	if i >= len(data) || data[i] != '{' {
		return nil, errors.New("document is not a JSON object")
	}
	i++ // past '{'
	sawMember := false
	lastValEnd := 0
	for {
		i = skipJSONSpace(data, i)
		if i >= len(data) {
			return nil, errors.New("unterminated JSON object")
		}
		if data[i] == '}' {
			// Key absent: append before the closing brace. In a non-empty
			// object the comma lands right after the last member's value (the
			// whitespace before the closing brace is preserved); in an empty
			// object the member goes right after the opening brace.
			if !sawMember {
				out := make([]byte, 0, len(data)+len(key)+len(val)+4)
				out = append(out, data[:i]...)
				out = append(out, '"')
				out = append(out, key...)
				out = append(out, `": `...)
				out = append(out, val...)
				out = append(out, data[i:]...)
				return out, nil
			}
			out := make([]byte, 0, len(data)+len(key)+len(val)+8)
			out = append(out, data[:lastValEnd]...)
			out = append(out, ",\n  "...)
			out = append(out, '"')
			out = append(out, key...)
			out = append(out, `": `...)
			out = append(out, val...)
			out = append(out, data[lastValEnd:]...)
			return out, nil
		}
		if data[i] != '"' {
			return nil, errors.New("expected a JSON member key")
		}
		keyStart := i
		keyEnd, err := skipJSONString(data, i)
		if err != nil {
			return nil, err
		}
		j := skipJSONSpace(data, keyEnd)
		if j >= len(data) || data[j] != ':' {
			return nil, errors.New("expected ':' after JSON member key")
		}
		j = skipJSONSpace(data, j+1)
		valEnd, err := skipJSONValue(data, j)
		if err != nil {
			return nil, err
		}
		if keyEnd-keyStart == len(key)+2 && string(data[keyStart+1:keyEnd-1]) == key {
			// Found: replace this member's span. The whitespace before the key
			// (in data[:keyStart]) and everything after the value are kept.
			out := make([]byte, 0, len(data)-(valEnd-keyStart)+len(key)+len(val)+4)
			out = append(out, data[:keyStart]...)
			out = append(out, '"')
			out = append(out, key...)
			out = append(out, `": `...)
			out = append(out, val...)
			out = append(out, data[valEnd:]...)
			return out, nil
		}
		sawMember = true
		lastValEnd = valEnd
		i = valEnd
		if i < len(data) && data[i] == ',' {
			i++ // move past the member separator
		}
	}
}

// skipJSONSpace advances i past JSON whitespace (space, tab, CR, LF).
func skipJSONSpace(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// skipJSONString returns the index just past the closing quote of the JSON
// string that starts at data[i] (data[i] must be '"'). Backslash escapes are
// skipped so an escaped quote does not end the string.
func skipJSONString(data []byte, i int) (int, error) {
	if i >= len(data) || data[i] != '"' {
		return 0, errors.New("not a JSON string")
	}
	for j := i + 1; j < len(data); j++ {
		switch data[j] {
		case '\\':
			j++ // skip the escaped character
		case '"':
			return j + 1, nil
		}
	}
	return 0, errors.New("unterminated JSON string")
}

// skipJSONValue returns the index just past the JSON value that starts at
// data[i]: an object, array, string, number, or a true/false/null literal.
// Same-type bracket nesting is counted (strings are skipped), so nested
// containers of the other bracket kind do not disturb the depth.
func skipJSONValue(data []byte, i int) (int, error) {
	if i >= len(data) {
		return 0, errors.New("unexpected end of JSON value")
	}
	switch c := data[i]; {
	case c == '"':
		return skipJSONString(data, i)
	case c == '{' || c == '[':
		openCh, closeCh := c, byte('}')
		if c == '[' {
			closeCh = ']'
		}
		depth := 1
		for j := i + 1; j < len(data); j++ {
			switch data[j] {
			case '"':
				var err error
				if j, err = skipJSONString(data, j); err != nil {
					return 0, err
				}
				j-- // the loop's j++ lands past the string
			case openCh:
				depth++
			case closeCh:
				depth--
				if depth == 0 {
					return j + 1, nil
				}
			}
		}
		return 0, errors.New("unbalanced JSON value")
	default:
		// Number or literal: ends at the first delimiter (whitespace, comma,
		// or a closing brace/bracket).
		j := i
		for j < len(data) {
			switch data[j] {
			case ' ', '\t', '\r', '\n', ',', '}', ']':
				return j, nil
			}
			j++
		}
		return j, nil
	}
}

func dialTarget(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "443")
}

func (a *adminHandlers) handleDiag(w http.ResponseWriter, r *http.Request) {
	checks := []dashboard.DiagCheck{}

	cfg := a.cfgLoad()
	switch cfg.EffectiveMode() {
	case "bridge":
		checks = append(checks, dashboard.DiagCheck{OK: true, Message: "Configuration: bridge mode (clients relay their own token)"})
	case "hybrid":
		checks = append(checks, dashboard.DiagCheck{OK: true, Message: fmt.Sprintf("Configuration: hybrid mode, %d pooled token(s) + bridge relay", len(cfg.AuthTokens))})
	default:
		checks = append(checks, dashboard.DiagCheck{OK: true, Message: fmt.Sprintf("Configuration: pooled mode, %d token(s)", len(cfg.AuthTokens))})
	}

	// Upstream reachability: DNS + TLS to the configured base host. The DNS
	// lookup uses the bare host, not u.Host verbatim: "host:8443" would be
	// treated as a literal DNS name and NXDOMAIN, a false red row (the -doctor
	// tool strips the port the same way). The display and dial target keep the
	// port so the TCP row still connects to the real endpoint.
	targetHost := "www.codebuff.com"
	dnsHost := targetHost
	if u, err := url.Parse(cfg.UpstreamBaseURL); err == nil && u.Host != "" {
		targetHost = u.Host
		if h := u.Hostname(); h != "" {
			dnsHost = h
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := net.DefaultResolver.LookupHost(ctx, dnsHost); err != nil {
		checks = append(checks, dashboard.DiagCheck{Message: "DNS lookup failed for " + dnsHost + ": " + err.Error()})
	} else {
		checks = append(checks, dashboard.DiagCheck{OK: true, Message: "DNS resolves " + dnsHost})
	}
	hostForDial := dialTarget(targetHost)
	if conn, err := net.DialTimeout("tcp", hostForDial, 5*time.Second); err != nil {
		checks = append(checks, dashboard.DiagCheck{Message: "TCP connect to " + hostForDial + " failed: " + err.Error()})
	} else {
		_ = conn.Close()
		checks = append(checks, dashboard.DiagCheck{OK: true, Message: "TCP reachable " + hostForDial})
	}

	checks = append(checks, dashboard.DiagCheck{OK: true, Message: fmt.Sprintf("Model registry: %d models", a.reg.ModelCount())})

	// Per-token validity probes (pooled mode only). Each
	// probe is a zero-cost upstream GET /api/v1/freebuff/session (no session
	// claim, no model needed), so they always run; a token with no active
	// session still counts as valid.
	if !cfg.BridgeMode() {
		for _, snap := range a.pool.PoolSnapshot().Tokens {
			idx := snap.Token
			probeCtx, probeCancel := context.WithTimeout(r.Context(), 8*time.Second)
			state, err := a.pool.ProbeToken(probeCtx, idx)
			probeCancel()
			switch {
			case errors.Is(err, upstream.ErrNoActiveSession):
				checks = append(checks, dashboard.DiagCheck{OK: true, Message: fmt.Sprintf("Token #%d validity probe succeeded (no active session)", idx+1)})
			case err != nil:
				checks = append(checks, dashboard.DiagCheck{Message: fmt.Sprintf("Token #%d validity probe failed: %v", idx+1, err)})
			default:
				msg := fmt.Sprintf("Token #%d validity probe succeeded", idx+1)
				if q := quotaSummary(state); q != "" {
					msg += " (" + q + ")"
				}
				checks = append(checks, dashboard.DiagCheck{OK: true, Message: msg})
			}
		}
	} else {
		checks = append(checks, dashboard.DiagCheck{Warn: true, Message: "No pooled tokens to probe (the smoke test uses a client token)."})
	}

	a.dash.RenderDiag(w, r, checks)
}

func (a *adminHandlers) handleConfigSave(w http.ResponseWriter, r *http.Request) {
	// Resolve ONCE at write time: the dashboard must write the same file the
	// loader would read (cwd wins, else the platform config dir) — a stray
	// ./.env with the defaults template silently becomes authoritative
	// otherwise (issue #234).
	envPath := config.EnvFileForWrite()
	r.Body = http.MaxBytesReader(w, r.Body, maxEnvSize)

	// The dashboard textarea posts application/x-www-form-urlencoded
	// (name="content"); a raw urlencoded body written verbatim as .env would
	// become "content=KEY=VALUE..." and destroy the file. Programmatic
	// clients (text/plain) post the raw .env text and keep the raw path.
	var content []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			a.dash.RenderConfigResult(w, r, false, "Failed to read request form.")
			return
		}
		content = []byte(r.FormValue("content"))
	} else {
		var err error
		content, err = io.ReadAll(r.Body)
		if err != nil {
			a.dash.RenderConfigResult(w, r, false, "Failed to read request body.")
			return
		}
	}

	// Guard: an empty payload (urlencoded POST without content=, or an empty
	// text/plain body) must never write an empty .env. config.Load succeeds
	// on an empty file with built-in defaults, so the write would silently
	// wipe the operator's AUTH_TOKENS/ADMIN_TOKEN/API_KEYS/SAFE_MODE while
	// reporting a green "Saved and reloaded". Reject it and leave the file
	// untouched.
	if len(bytes.TrimSpace(content)) == 0 {
		a.dash.RenderConfigResult(w, r, false, "Configuration rejected: empty .env content — nothing to save.")
		return
	}

	a.adminSaveMu.Lock()
	defer a.adminSaveMu.Unlock()

	old, oldErr := os.ReadFile(envPath)
	if err := config.WriteFileAtomic(envPath, content); err != nil {
		a.dash.RenderConfigResult(w, r, false, "Failed to write .env: "+err.Error())
		return
	}
	newCfg, err := config.Load(a.configPath)
	if err != nil {
		switch {
		case oldErr == nil:
			_ = config.WriteFileAtomic(envPath, old)
		case errors.Is(oldErr, os.ErrNotExist):
			// The .env did not exist before the save: remove the rejected
			// write so the state matches.
			_ = os.Remove(envPath)
		default:
			// The previous .env existed but was unreadable (permissions, ACL):
			// deleting it would destroy the operator's file. Leave the newly
			// written content and warn — a restore is impossible without the
			// old bytes.
			a.logfunc().Warn("dashboard config save rejected; previous .env unreadable, not restored", "readErr", oldErr, "err", err)
		}
		a.logfunc().Warn("dashboard config save rejected", "err", err)
		a.dash.RenderConfigResult(w, r, false, "Configuration rejected: "+err.Error())
		return
	}
	oldCfg := a.cfgLoad()
	a.applyReloadedConfig(&newCfg)
	a.logfunc().Info("dashboard config saved and reloaded",
		"remote", remoteHost(r), "changed_keys", changedConfigKeys(oldCfg, &newCfg),
		"auth_tokens", len(newCfg.AuthTokens), "safe_mode", newCfg.SafeMode)
	if keys := envOverrideKeys(content); len(keys) > 0 {
		// The file was written and the config reloaded, but a real process
		// environment variable outranks the .env file (precedence
		// env > .env > JSON), so those values are in force from the
		// environment, NOT from the saved file. Report fail-loud but keep
		// the 200 status: the write itself succeeded and rolling back could
		// not beat the environment (matching the token-path semantics).
		message := fmt.Sprintf("saved, but these keys are overridden by the process environment and will only apply after restart: %s",
			strings.Join(keys, ", "))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": message})
		return
	}
	// Restart-only knobs: the save succeeded and the reload re-applied
	// everything the pool can move, but the listed keys need a restart to
	// take effect on the live clients (they are snapshotted when the
	// upstream clients, session managers, run managers, and notifier are
	// constructed at boot). Report them explicitly so the save response
	// cannot read as a full live update.
	if restartOnly := changedRestartOnlyKeys(oldCfg, &newCfg); len(restartOnly) > 0 {
		message := fmt.Sprintf("Saved and reloaded. These keys apply after restart only: %s",
			strings.Join(restartOnly, ", "))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": message, "restart_only": restartOnly})
		return
	}
	a.dash.RenderConfigResult(w, r, true, "Saved and reloaded — effective configuration updated.")
}

// restartOnlyConfigKeys lists knobs that are snapshotted when the upstream
// clients, session managers, run managers, and notifier are constructed at
// boot: a config save reloads the in-memory config, but the live objects
// keep the values they were built with until the process restarts.
var restartOnlyConfigKeys = []string{
	"UPSTREAM_BASE_URL",
	"REQUEST_TIMEOUT",
	"SESSION_CALL_TIMEOUT",
	"HTTP_READ_TIMEOUT",
	"REQUEST_JITTER",
	"COST_MODE",
	"TLS_FINGERPRINT",
	"TRANSIENT_RETRIES",
	"HTTP2_UPSTREAM",
	"DEBUG_DUMP",
	"ACTING_USER_ID",
	"SESSION_PERSIST",
	"SESSION_STATE_FILE",
	"ADOPT_CLI_SESSION",
	"WEBHOOK_URL",
	"ROTATION_INTERVAL",
	"RUN_FINISH_QUEUE_SIZE",
	"RUN_FINISH_INLINE_TIMEOUT",
	"RUNS_DRAIN_QUEUE_CAP",
	"RUNS_DRAIN_TTL",
	"DASHBOARD_ENABLED",
}

// changedRestartOnlyKeys returns the subset of restartOnlyConfigKeys whose
// effective value changed between oldCfg and newCfg.
func changedRestartOnlyKeys(oldCfg, newCfg *config.Config) []string {
	oldKV := effectiveConfigKV(oldCfg)
	newKV := effectiveConfigKV(newCfg)
	var changed []string
	for _, k := range restartOnlyConfigKeys {
		if oldKV[k] != newKV[k] {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return changed
}

// parseEnvContent is a lenient dotenv parser: the dashboard's textarea posts
// raw .env text, and we re-parse the SAME bytes just written to report which
// keys the process environment silently overrides. BOM, blank lines,
// comments, single/double quotes, and inline # comments are handled; export
// prefixes and interpolated vars (foo=$bar) are intentionally not resolved —
// a literal-of-a-variable is not an override worth reporting.
func parseEnvContent(data []byte) map[string]string {
	out := make(map[string]string)
	// Strip a leading UTF-8 BOM so the first key is not corrupted.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		quoted := false
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
			if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
				value = value[1 : 1+end]
				quoted = true
			}
		}
		if !quoted {
			if idx := strings.IndexByte(value, '#'); idx >= 0 {
				value = strings.TrimSpace(value[:idx])
			}
		}
		out[key] = value
	}
	return out
}

// envOverrideKeys reports which keys in just-written .env content are
// silently overridden by the actual process environment (precedence
// env > .env > JSON). A key counts as overridden only when the environment
// value differs from the written value — or, for AUTH_TOKENS, simply when a
// real env var exists at all (presence wins regardless of emptiness under
// the config loader).
func envOverrideKeys(content []byte) []string {
	written := parseEnvContent(content)
	keys := make([]string, 0, len(written))
	for key, writtenVal := range written {
		envVal, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		envVal = strings.TrimSpace(envVal)
		writtenVal = strings.TrimSpace(writtenVal)
		switch {
		case key == "AUTH_TOKENS":
			// Presence wins even with an empty value; equality means the
			// written list landed and there is nothing to report.
			if envVal != writtenVal {
				keys = append(keys, key)
			}
		case envVal != "" && envVal != writtenVal:
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func effectiveConfigKV(cfg *config.Config) map[string]string {
	return map[string]string{
		"LISTEN_ADDR":                 cfg.ListenAddr,
		"UPSTREAM_BASE_URL":           cfg.UpstreamBaseURL,
		"AUTH_TOKENS":                 strconv.Itoa(len(cfg.AuthTokens)),
		"API_KEYS":                    strconv.Itoa(len(cfg.APIKeys)),
		"ADMIN_TOKEN":                 boolWord(cfg.AdminToken != ""),
		"ROTATION_INTERVAL":           cfg.RotationInterval.String(),
		"REQUEST_TIMEOUT":             cfg.RequestTimeout.String(),
		"SESSION_CALL_TIMEOUT":        cfg.SessionCallTimeout.String(),
		"COST_MODE":                   cfg.CostMode,
		"TLS_FINGERPRINT":             cfg.TLSFingerprint,
		"REGISTRY_REFRESH":            cfg.RegistryRefresh.String(),
		"DEBUG_DUMP":                  strconv.FormatBool(cfg.DebugDump),
		"ACTING_USER_ID":              boolWord(cfg.ActingUserID != ""),
		"LOG_FILE":                    cfg.LogFile,
		"LOG_LEVEL":                   cfg.LogLevel,
		"LOG_FORMAT":                  cfg.LogFormat,
		"LOG_ACCESS":                  strconv.FormatBool(cfg.LogAccess),
		"LOG_RING_SIZE":               strconv.Itoa(cfg.LogRingSize),
		"MAX_MESSAGES_PER_DAY":        strconv.Itoa(cfg.MaxMessagesPerDay),
		"MAX_SPEND_PER_DAY":           strconv.FormatInt(cfg.MaxSpendPerDay, 10),
		"IDLE_ROTATION_TIMEOUT":       cfg.IdleRotationTimeout.String(),
		"SESSION_IDLE_END":            cfg.SessionIdleEnd.String(),
		"SAFE_MODE":                   strconv.FormatBool(cfg.SafeMode),
		"MODELS_HIDE_UNAVAILABLE":     strconv.FormatBool(cfg.ModelsHideUnavailable),
		"MODELS_ALLOW":                strings.Join(cfg.ModelsAllow, ","),
		"CORS_ALLOWED_ORIGIN":         cfg.CORSAllowedOrigin,
		"REQUEST_JITTER":              cfg.RequestJitter.String(),
		"CLI_VERSION":                 cfg.CLIVersion,
		"MODEL_ALIASES":               strconv.Itoa(len(cfg.ModelAliases)),
		"TRANSIENT_RETRIES":           strconv.Itoa(cfg.TransientRetries),
		"SESSION_PERSIST":             strconv.FormatBool(cfg.SessionPersist),
		"SESSION_STATE_FILE":          cfg.SessionStateFile,
		"HTTP2_UPSTREAM":              strconv.FormatBool(cfg.HTTP2Upstream),
		"DASHBOARD_ENABLED":           strconv.FormatBool(cfg.DashboardEnabled),
		"RUN_FINISH_QUEUE_SIZE":       strconv.Itoa(cfg.RunFinishQueueSize),
		"RUN_FINISH_INLINE_TIMEOUT":   cfg.RunFinishInlineTimeout.String(),
		"RUNS_DRAIN_QUEUE_CAP":        strconv.Itoa(cfg.RunsDrainQueueCap),
		"RUNS_DRAIN_TTL":              cfg.RunsDrainTTL.String(),
		"SESSION_RE_ADMIT_LEAD":       cfg.SessionReAdmitLead.String(),
		"SESSION_PROBE_CACHE_TTL":     cfg.SessionProbeCacheTTL.String(),
		"MODEL_UNAVAILABLE_CACHE_TTL": cfg.ModelUnavailableCacheTTL.String(),
		"WEBHOOK_URL":                 boolWord(cfg.WebhookURL != ""),
		"FALLBACK_AFTER_MS":           cfg.FallbackAfter.String(),
		"FALLBACK_MODEL":              strconv.Itoa(len(cfg.FallbackModels)),
		"ADOPT_CLI_SESSION":           strconv.FormatBool(cfg.AdoptCLISession),
		"WAITING_ROOM_CHAIN":          strconv.FormatBool(cfg.WaitingRoomChain),
		"QUOTA_FALLBACK_MODELS":       strconv.Itoa(len(cfg.QuotaFallbackModels)),
		"TOKEN_ROTATION":              cfg.TokenRotation,
		"RATE_LIMIT_FAILOVER":         strconv.FormatBool(cfg.RateLimitFailover),
		"BRIDGE_ENABLED":              strconv.FormatBool(cfg.BridgeEnabled),
		"BRIDGE_IDLE_EVICT":           cfg.BridgeIdleEvict.String(),
	}
}

func boolWord(v bool) string {
	if v {
		return "set"
	}
	return "unset"
}

func changedConfigKeys(oldCfg, newCfg *config.Config) []string {
	oldKV := effectiveConfigKV(oldCfg)
	newKV := effectiveConfigKV(newCfg)
	var changed []string
	for k, v := range newKV {
		if oldKV[k] != v {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return changed
}
