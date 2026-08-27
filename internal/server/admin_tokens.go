package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"freebuff-proxy/internal/config"
	"freebuff-proxy/internal/upstream"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func tokenActionID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 0 {
		return 0, errors.New("invalid token id")
	}
	return id, nil
}

func (s *Server) handleTokenUnlock(w http.ResponseWriter, r *http.Request) {
	id, err := tokenActionID(r)
	if err == nil {
		err = s.pool.UnlockToken(id)
	}
	if err != nil {
		s.dash.RenderConfigResult(w, r, false, "Unlock failed: "+err.Error())
		return
	}
	s.logger.Info("dashboard token unlocked", "token", id)
	s.dash.RenderConfigResult(w, r, true, "Token "+strconv.Itoa(id)+" unlocked — no cooldown or ban window remains.")
}

func (s *Server) handleTokenLock(w http.ResponseWriter, r *http.Request) {
	id, err := tokenActionID(r)
	if err == nil {
		err = s.pool.LockToken(id)
	}
	if err != nil {
		s.dash.RenderConfigResult(w, r, false, "Lock failed: "+err.Error())
		return
	}
	s.logger.Info("dashboard token locked", "token", id)
	s.dash.RenderConfigResult(w, r, true, "Token "+strconv.Itoa(id)+" locked — it will not be used for new requests.")
}

func (s *Server) handleTokenUnlockLock(w http.ResponseWriter, r *http.Request) {
	id, err := tokenActionID(r)
	if err == nil {
		err = s.pool.UnlockLockToken(id)
	}
	if err != nil {
		s.dash.RenderConfigResult(w, r, false, "Unlock failed: "+err.Error())
		return
	}
	s.logger.Info("dashboard token unlocked (admin)", "token", id)
	s.dash.RenderConfigResult(w, r, true, "Token "+strconv.Itoa(id)+" unlocked — it is available for requests again.")
}

func (s *Server) handleTokenFinish(w http.ResponseWriter, r *http.Request) {
	id, err := tokenActionID(r)
	if err == nil {
		err = s.pool.FinishTokenRuns(r.Context(), id)
	}
	if err != nil {
		s.dash.RenderConfigResult(w, r, false, "Finish failed: "+err.Error())
		return
	}
	s.logger.Info("dashboard token runs finished", "token", id)
	s.dash.RenderConfigResult(w, r, true, "Token "+strconv.Itoa(id)+" runs finished.")
}

func (s *Server) handleTokenTest(w http.ResponseWriter, r *http.Request) {
	id, err := tokenActionID(r)
	var state *upstream.SessionState
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		state, err = s.pool.ProbeToken(ctx, id)
	}
	if err != nil {
		if errors.Is(err, upstream.ErrNoActiveSession) {
			s.logger.Info("dashboard token probe ok (no active session)", "token", id)
			s.dash.RenderConfigResult(w, r, true, "Token "+strconv.Itoa(id)+" OK — zero-cost probe succeeded (no active session).")
			return
		}
		s.logger.Warn("dashboard token probe failed", "token", id, "err", err)
		s.dash.RenderConfigResult(w, r, false, "Token "+strconv.Itoa(id)+" test failed: "+err.Error())
		return
	}
	msg := "Token " + strconv.Itoa(id) + " OK — zero-cost probe succeeded"
	if q := quotaSummary(state); q != "" {
		msg += " (" + q + ")"
	}
	msg += "."
	s.logger.Info("dashboard token probe ok", "token", id)
	s.dash.RenderConfigResult(w, r, true, msg)
}

func (s *Server) handleTokenTestAll(w http.ResponseWriter, r *http.Request) {
	count := 0
	for _, snap := range s.pool.PoolSnapshot().Tokens {
		i := snap.Token
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		state, err := s.pool.ProbeToken(ctx, i)
		cancel()
		ok := err == nil || errors.Is(err, upstream.ErrNoActiveSession)
		msg := "ok"
		switch {
		case errors.Is(err, upstream.ErrNoActiveSession):
			msg = "ok (no active session)"
		case err != nil:
			msg = err.Error()
		default:
			if q := quotaSummary(state); q != "" {
				msg = "ok (" + q + ")"
			}
		}
		s.dash.RenderTestResult(w, r, i, ok, msg, "")
		count++
	}
	if count == 0 {
		s.dash.RenderConfigResult(w, r, false, "No tokens to test (bridge mode has no fixed AUTH_TOKENS).")
	}
}

func (s *Server) addTokenPersist(ctx context.Context, token string) (int, error) {
	// Tier gate (mirrors handleTokenAdd): a banned/country-blocked token
	// minted from a datacenter IP must never enter the pool — it would fail
	// every request with 403 and amplify the ban (issue #140).
	if _, err := s.probeTokenGate(ctx, token); err != nil {
		return 0, fmt.Errorf("token rejected by probe: %w", err)
	}
	cfg := s.cfg.Load()
	existing := cfg.AuthTokens
	if len(existing) > 0 {
		idx, err := s.pool.AddToken(token)
		if err != nil {
			return 0, fmt.Errorf("add token to pool: %w", err)
		}
		// Persist the runtime list (pool may have bridge additions too, but
		// AUTH_TOKENS is the fixed set — append only when not already there).
		tokens := append([]string(nil), existing...)
		seen := false
		for _, t := range tokens {
			if t == token {
				seen = true
				break
			}
		}
		if !seen {
			tokens = append(tokens, token)
		}
		if err := s.syncTokensAfterMutation(tokens); err != nil {
			return 0, err
		}
		return idx, nil
	}
	// Bridge mode (no fixed tokens): the first wizard token switches to
	// pooled mode, exactly like handleTokenAdd.
	idx, err := s.pool.AddToken(token)
	if err != nil {
		return 0, fmt.Errorf("add token to pool: %w", err)
	}
	if err := s.syncTokensAfterMutation([]string{token}); err != nil {
		return 0, err
	}
	return idx, nil
}

func shortFlowID(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}

func (s *Server) syncTokensAfterMutation(tokens []string) error {
	// When tokenDB is active, persist to the database instead of .env.
	if s.tokenDB != nil {
		// Sync database to match the desired token list.
		existing, err := s.tokenDB.List()
		if err != nil {
			return fmt.Errorf("tokendb list: %w", err)
		}
		existingSet := make(map[string]struct{}, len(existing))
		for _, t := range existing {
			existingSet[t] = struct{}{}
		}
		desiredSet := make(map[string]struct{}, len(tokens))
		for _, t := range tokens {
			desiredSet[t] = struct{}{}
		}
		// Remove tokens no longer in the desired list.
		for _, t := range existing {
			if _, ok := desiredSet[t]; !ok {
				if _, err := s.tokenDB.Remove(t); err != nil {
					return fmt.Errorf("tokendb remove: %w", err)
				}
			}
		}
		// Add tokens not yet in the database.
		for _, t := range tokens {
			if _, ok := existingSet[t]; !ok {
				if _, err := s.tokenDB.Add(t); err != nil {
					return fmt.Errorf("tokendb add: %w", err)
				}
			}
		}
		// Update the in-memory config directly (no .env rewrite needed).
		cfg := s.cfg.Load()
		cfg.AuthTokens = append([]string(nil), tokens...)
		s.cfg.Store(cfg)
		s.reg.SetConfig(cfg)
		s.pool.SetConfig(cfg)
		return nil
	}

	// Legacy path: persist to .env file.
	old, oldErr := os.ReadFile(".env")
	if _, err := updateAuthTokensEnv(tokens); err != nil {
		return fmt.Errorf("persist AUTH_TOKENS: %w", err)
	}
	newCfg, err := config.Load(s.configPath)
	if err != nil {
		restoreEnvFile(old, oldErr)
		return fmt.Errorf("reload config: %w", err)
	}
	if !reflect.DeepEqual(newCfg.AuthTokens, tokens) {
		restoreEnvFile(old, oldErr)
		return fmt.Errorf("AUTH_TOKENS overridden by environment or -config JSON (%d effective vs %d requested) — persisted to .env but NOT activated; clear it there or restart without env_file, then retry", len(newCfg.AuthTokens), len(tokens))
	}
	s.cfg.Store(&newCfg)
	s.reg.SetConfig(&newCfg)
	s.pool.SetConfig(&newCfg)
	s.rateLimiter.SetRate(newCfg.RateLimitPerIP, newCfg.RateLimitBurst)
	return nil
}

func (s *Server) probeTokenGate(ctx context.Context, token string) (*upstream.SessionState, error) {
	state, err := s.pool.ProbeNewToken(ctx, token)
	if err != nil {
		if errors.Is(err, upstream.ErrNoActiveSession) {
			// No active session is fine: the pool will create one on first
			// use. Treat as usable.
			return state, nil
		}
		return nil, err
	}
	if state != nil {
		switch state.Status {
		case "banned":
			return nil, fmt.Errorf("token is banned upstream (status banned): %w", upstream.ErrBanned)
		case "country_blocked":
			return nil, fmt.Errorf("token is country-blocked upstream: %w", upstream.ErrCountryBlocked)
		}
	}
	return state, nil
}

func (s *Server) handleTokenAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	req.Token = strings.TrimSpace(r.FormValue("token"))
	if req.Token == "" {
		// JSON fallback for programmatic clients.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
		if err != nil {
			s.dash.RenderConfigResult(w, r, false, "Failed to read request: "+err.Error())
			return
		}
		if err := json.Unmarshal(body, &req); err != nil {
			s.dash.RenderConfigResult(w, r, false, "Invalid request: "+err.Error())
			return
		}
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" || strings.HasPrefix(strings.ToLower(req.Token), "bearer ") {
		s.dash.RenderConfigResult(w, r, false, "Invalid token (must not start with 'Bearer ').")
		return
	}

	// adminSaveMu serializes the pool mutation + persist + reload with the
	// other .env writers (config editor, token remove, mode switch) so a
	// concurrent save cannot interleave and lose a token from .env.
	s.adminSaveMu.Lock()
	defer s.adminSaveMu.Unlock()

	cfg := s.cfg.Load()
	// Divergence guard (mirrors handleTokenRemove): a config-editor
	// AUTH_TOKENS edit or /admin/reload can diverge cfg.AuthTokens from the
	// live pool. Adding to a stale list would persist cfg.AuthTokens+new to
	// .env while the pool holds its own list, leaving pool/.env/cfg
	// permanently divergent — and the next remove is rejected by the same
	// guard, stranding the operator until restart.
	if len(cfg.AuthTokens) != s.pool.TokenCount() {
		s.dash.RenderConfigResult(w, r, false, "AUTH_TOKENS in .env differs from the live pool — reconcile in the Config editor or restart.")
		return
	}
	// Tier gate: reject dead accounts before they enter the pool. The probe
	// is zero-cost (no session slot claimed); a banned/country-blocked/
	// auth-rejected token is refused with a clear message instead of being
	// added and failing every request with 403 (the ban amplifier).
	_, err := s.probeTokenGate(r.Context(), req.Token)
	if err != nil {
		s.dash.RenderConfigResult(w, r, false, "Token rejected by probe: "+err.Error())
		return
	}
	idx, err := s.pool.AddToken(req.Token)
	if err != nil {
		s.dash.RenderConfigResult(w, r, false, err.Error())
		return
	}
	// Build the persist list from cfg (the fixed AUTH_TOKENS set) plus the
	// new token, skipping any token already present: a duplicate add must
	// not write `tok,cb,cb` to .env — splitList would collapse it on reload
	// and the strict reload check would reject the add and roll back.
	tokens := append([]string{}, cfg.AuthTokens...)
	seen := false
	for _, t := range tokens {
		if t == req.Token {
			seen = true
			break
		}
	}
	if !seen {
		tokens = append(tokens, req.Token)
	}
	if err := s.syncTokensAfterMutation(tokens); err != nil {
		_ = s.pool.RemoveLastToken()
		s.logger.Warn("dashboard token add rolled back", "remote", remoteHost(r), "err", err)
		s.dash.RenderConfigResult(w, r, false, err.Error())
		return
	}
	s.logger.Info("dashboard token added", "remote", remoteHost(r), "index", idx)
	s.dash.RenderConfigResult(w, r, true, "Token added at index "+strconv.Itoa(idx)+" and persisted to .env.")
}

func (s *Server) handleTokenRemove(w http.ResponseWriter, r *http.Request) {
	// adminSaveMu serializes the pool mutation + persist + reload with the
	// other .env writers, exactly like handleTokenAdd.
	s.adminSaveMu.Lock()
	defer s.adminSaveMu.Unlock()

	cfg := s.cfg.Load()
	// A config-editor AUTH_TOKENS edit or /admin/reload can diverge
	// cfg.AuthTokens from the live pool; removing "the last token" from a
	// stale list would persist the wrong .env and leave pool/.env/cfg
	// permanently inconsistent.
	if len(cfg.AuthTokens) != s.pool.TokenCount() {
		s.dash.RenderConfigResult(w, r, false, "AUTH_TOKENS in .env differs from the live pool — reconcile in the Config editor or restart.")
		return
	}
	removed := ""
	if len(cfg.AuthTokens) > 0 {
		removed = cfg.AuthTokens[len(cfg.AuthTokens)-1]
	}
	if err := s.pool.RemoveLastToken(); err != nil {
		s.dash.RenderConfigResult(w, r, false, err.Error())
		return
	}
	tokens := cfg.AuthTokens
	if len(tokens) > 0 {
		tokens = tokens[:len(tokens)-1]
	}
	if err := s.syncTokensAfterMutation(tokens); err != nil {
		// Roll the pool back so a failed persist does not leave the token
		// removed from the pool but still listed in .env/cfg (mirrors
		// handleTokenAdd's rollback).
		if removed != "" {
			if _, addErr := s.pool.AddToken(removed); addErr != nil {
				s.logger.Warn("dashboard token remove rollback re-add failed", "remote", remoteHost(r), "err", addErr)
			}
		}
		s.logger.Warn("dashboard token remove rolled back", "remote", remoteHost(r), "err", err)
		s.dash.RenderConfigResult(w, r, false, err.Error())
		return
	}
	s.logger.Info("dashboard token removed", "remote", remoteHost(r))
	s.dash.RenderConfigResult(w, r, true, "Last token removed and persisted to .env.")
}

func (s *Server) handleModeSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	req.Mode = r.FormValue("mode")
	if req.Mode == "" {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
		if err != nil {
			s.dash.RenderConfigResult(w, r, false, "Failed to read request: "+err.Error())
			return
		}
		if err := json.Unmarshal(body, &req); err != nil {
			s.dash.RenderConfigResult(w, r, false, "Invalid request: "+err.Error())
			return
		}
	}
	cfg := s.cfg.Load()
	switch strings.ToLower(strings.TrimSpace(req.Mode)) {
	case "bridge":
		if cfg.BridgeMode() {
			s.dash.RenderConfigResult(w, r, false, "Already in bridge mode.")
			return
		}
		s.adminSaveMu.Lock()
		defer s.adminSaveMu.Unlock()

		// When tokenDB is active, clear the database directly.
		if s.tokenDB != nil {
			if err := s.tokenDB.RemoveAll(); err != nil {
				s.dash.RenderConfigResult(w, r, false, "Failed to clear token database: "+err.Error())
				return
			}
			cfg.AuthTokens = nil
			s.cfg.Store(cfg)
			s.reg.SetConfig(cfg)
			s.pool.SetConfig(cfg)
			s.pool.RemoveAllTokens(r.Context())
			s.logger.Info("dashboard switched to bridge mode (tokenDB)")
			s.dash.RenderConfigResult(w, r, true, "Switched to bridge mode — tokens cleared; clients now send their own token.")
			return
		}

		// Legacy path: persist AUTH_TOKENS= (explicit empty) to .env.
		old, oldErr := os.ReadFile(".env")
		if _, err := updateEnvKeys([]envUpdate{{Key: "AUTH_TOKENS", Value: ""}}); err != nil {
			s.dash.RenderConfigResult(w, r, false, "Failed to persist .env: "+err.Error())
			return
		}
		newCfg, err := config.Load(s.configPath)
		if err != nil {
			restoreEnvFile(old, oldErr)
			s.dash.RenderConfigResult(w, r, false, "Reload rejected: "+err.Error())
			return
		}
		if !newCfg.BridgeMode() {
			restoreEnvFile(old, oldErr)
			s.dash.RenderConfigResult(w, r, false, "Could not switch to bridge mode: AUTH_TOKENS is still set by a -config JSON file or the environment, which overrides .env. Clear it there, or run without -config, then retry.")
			return
		}
		s.cfg.Store(&newCfg)
		s.reg.SetConfig(&newCfg)
		s.pool.SetConfig(&newCfg)
		s.rateLimiter.SetRate(newCfg.RateLimitPerIP, newCfg.RateLimitBurst)
		s.pool.RemoveAllTokens(r.Context())
		s.logger.Info("dashboard switched to bridge mode")
		s.dash.RenderConfigResult(w, r, true, "Switched to bridge mode — AUTH_TOKENS cleared; clients now send their own token.")
	case "pooled":
		if !cfg.BridgeMode() {
			s.dash.RenderConfigResult(w, r, false, "Already in pooled mode.")
			return
		}
		s.dash.RenderConfigResult(w, r, false, "Pooled mode needs tokens — add one via the Add-token form first.")
		return
	default:
		s.dash.RenderConfigResult(w, r, false, "Mode must be 'bridge' or 'pooled'.")
	}
}

// handleBridgeTokenLock locks a bridge token by its key hash (#187).
func (s *Server) handleBridgeTokenLock(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	if err := s.pool.LockBridgeEntry(key); err != nil {
		s.dash.RenderConfigResult(w, r, false, "Lock failed: "+err.Error())
		return
	}
	s.logger.Info("bridge token locked", "key", key)
	s.dash.RenderConfigResult(w, r, true, "Bridge token "+shortKey(key)+" locked.")
}

// handleBridgeTokenUnlock clears the admin lock on a bridge token (#187).
func (s *Server) handleBridgeTokenUnlock(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	if err := s.pool.UnlockBridgeEntry(key); err != nil {
		s.dash.RenderConfigResult(w, r, false, "Unlock failed: "+err.Error())
		return
	}
	s.logger.Info("bridge token unlocked", "key", key)
	s.dash.RenderConfigResult(w, r, true, "Bridge token "+shortKey(key)+" unlocked.")
}

// shortKey returns the first 8 chars of a bridge key hash for display.
func shortKey(key string) string {
	if len(key) > 8 {
		return key[:8] + "…"
	}
	return key
}

// handleTokenRemoveSpecific removes a specific token by its value (not just
// the last one).  This enables the UI to remove any token from the list.
func (s *Server) handleTokenRemoveSpecific(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	req.Token = strings.TrimSpace(r.FormValue("token"))
	if req.Token == "" {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
		if err != nil {
			s.dash.RenderConfigResult(w, r, false, "Failed to read request: "+err.Error())
			return
		}
		if err := json.Unmarshal(body, &req); err != nil {
			s.dash.RenderConfigResult(w, r, false, "Invalid request: "+err.Error())
			return
		}
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		s.dash.RenderConfigResult(w, r, false, "Token value is required.")
		return
	}

	s.adminSaveMu.Lock()
	defer s.adminSaveMu.Unlock()

	cfg := s.cfg.Load()
	// Find the token in the current list.
	found := false
	for _, t := range cfg.AuthTokens {
		if t == req.Token {
			found = true
			break
		}
	}
	if !found {
		s.dash.RenderConfigResult(w, r, false, "Token not found in the active token list.")
		return
	}

	// Find the pool index for this token and remove it.
	var poolIdx int = -1
	for i, t := range cfg.AuthTokens {
		if t == req.Token {
			poolIdx = i
			break
		}
	}
	if poolIdx < 0 {
		s.dash.RenderConfigResult(w, r, false, "Token not found.")
		return
	}

	// Remove from pool by index.
	if err := s.pool.RemoveTokenByIndex(poolIdx); err != nil {
		s.dash.RenderConfigResult(w, r, false, "Failed to remove from pool: "+err.Error())
		return
	}

	// Build the new token list without the removed token.
	tokens := make([]string, 0, len(cfg.AuthTokens)-1)
	for _, t := range cfg.AuthTokens {
		if t != req.Token {
			tokens = append(tokens, t)
		}
	}
	if err := s.syncTokensAfterMutation(tokens); err != nil {
		// Roll back: re-add the token to the pool.
		if _, addErr := s.pool.AddToken(req.Token); addErr != nil {
			s.logger.Warn("dashboard token remove-specific rollback re-add failed", "remote", remoteHost(r), "err", addErr)
		}
		s.logger.Warn("dashboard token remove-specific rolled back", "remote", remoteHost(r), "err", err)
		s.dash.RenderConfigResult(w, r, false, err.Error())
		return
	}
	s.logger.Info("dashboard token removed (specific)", "remote", remoteHost(r))
	s.dash.RenderConfigResult(w, r, true, "Token removed successfully.")
}

// handleTokenList returns all tokens from the database or config.
func (s *Server) handleTokenList(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Load()
	tokens := cfg.AuthTokens

	// If tokenDB is available, prefer its view.
	if s.tokenDB != nil {
		if dbTokens, err := s.tokenDB.List(); err == nil {
			tokens = dbTokens
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tokens": tokens,
		"count":  len(tokens),
	})
}
