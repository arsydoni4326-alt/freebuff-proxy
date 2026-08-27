// token_probe.go — background health probe scheduler (Phase 5.2).
//
// When TOKEN_HEALTH_PROBES=true, the pool runs periodic zero-cost GET
// /api/v1/freebuff/session probes for each pooled token at TOKEN_PROBE_INTERVAL
// cadence. Probes validate token liveness without claiming session slots;
// failures surface in /healthz, dashboard, and logs.
package pool

import (
	"context"
	"errors"
	"sync"
	"time"

	"freebuff-proxy/internal/upstream"
)

// probeResult captures the outcome of a single background health probe.
type probeResult struct {
	OK       bool
	QuotaOK  bool
	Error    string
	ProbedAt time.Time
}

// probeState tracks the last probe result for each token.
type probeState struct {
	mu      sync.RWMutex
	results map[int]*probeResult
}

func newProbeState() *probeState {
	return &probeState{results: make(map[int]*probeResult)}
}

// Get returns the last probe result for the given token index, or nil if
// no probe has been run yet.
func (ps *probeState) Get(idx int) *probeResult {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.results[idx]
}

// Set stores a probe result for the given token index.
func (ps *probeState) Set(idx int, pr *probeResult) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.results[idx] = pr
}

// probeTokens runs a zero-cost health probe for each token in the pool.
// It is called periodically by the background probe goroutine.
func (p *Pool) probeTokens(ctx context.Context) {
	cfg := p.cfg.Load()
	if cfg == nil || cfg.BridgeMode() {
		return
	}
	toks := p.toks.Load()
	if toks == nil || len(*toks) == 0 {
		return
	}
	for i, tok := range *toks {
		if ctx.Err() != nil {
			return
		}
		// Skip tokens that are administratively locked.
		if tok.locked.Load() {
			continue
		}
		pr := p.probeSingleToken(ctx, tok, i)
		p.probeResults.Set(i, pr)
		if !pr.OK {
			p.logger.Warn("background health probe failed",
				"token", i+1,
				"error", pr.Error,
				"hint", "token may be expired or banned; rotate or re-authenticate")
		}
	}
}

// probeSingleToken probes one token with a zero-cost GET session probe
// (no session claimed, no daily slot consumed). Returns the probe result.
func (p *Pool) probeSingleToken(ctx context.Context, tok *tokenEntry, idx int) *probeResult {
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	st, err := tok.client.ProbeAccount(probeCtx)
	pr := &probeResult{ProbedAt: time.Now()}

	if err != nil {
		if errors.Is(err, upstream.ErrNoActiveSession) {
			// Token is valid but has no active session — this is OK.
			pr.OK = true
			return pr
		}
		pr.OK = false
		pr.Error = err.Error()
		return pr
	}

	pr.OK = true
	// Check if any model has positive quota remaining.
	if len(st.RateLimitsByModel) > 0 {
		for _, q := range st.RateLimitsByModel {
			remaining := q.Limit - q.RecentCount
			if remaining > 0 {
				pr.QuotaOK = true
				break
			}
		}
	} else {
		// No quota info available — assume OK (compact response).
		pr.QuotaOK = true
	}
	return pr
}

// startProbeLoop launches the background health probe goroutine. It runs
// at the configured TOKEN_PROBE_INTERVAL cadence and stops when ctx is
// cancelled. The goroutine is registered on the pool's WaitGroup so
// Shutdown waits for it to finish.
func (p *Pool) startProbeLoop(ctx context.Context) {
	cfg := p.cfg.Load()
	if cfg == nil || !cfg.TokenHealthProbes || cfg.BridgeMode() {
		return
	}
	interval := cfg.TokenProbeInterval
	if interval <= 0 {
		interval = 30 * time.Minute
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.logger.Info("background health probe started",
			"interval", interval.String(),
			"probe_type", "zero-cost GET /api/v1/freebuff/session")

		// Run an initial probe after a short delay (let the pool warm up).
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		p.probeTokens(ctx)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.probeTokens(ctx)
			}
		}
	}()
}
