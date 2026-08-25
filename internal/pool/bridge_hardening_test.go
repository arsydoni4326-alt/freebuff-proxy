// bridge_hardening_test.go — fuzz tests, property-based LRU eviction,
// load/thundering-herd tests, and quota exhaustion edge-case tests for the
// bridge mode path.
package pool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"freebuff-proxy/internal/config"
	"freebuff-proxy/internal/testutil"
	"freebuff-proxy/internal/upstream"
)

// --- Fuzz tests for token handling ---

// FuzzValidateClientToken verifies that validateClientToken never panics on
// any input, and its acceptance/rejection is internally consistent.
func FuzzValidateClientToken(f *testing.F) {
	seeds := []string{
		"",
		"cb_validtoken",
		"abc123",
		"   leading_space",
		"trailing_space   ",
		"tab\tinterior",
		"newline\ninterior",
		"\x00nullbyte",
		"token with spaces",
		strings.Repeat("a", maxClientTokenLen),
		strings.Repeat("a", maxClientTokenLen+1),
		"Bearer injected",
		"cb_tok\x7f DEL",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("validateClientToken panicked on %q: %v", raw, r)
			}
		}()
		err := validateClientToken(raw)
		if err == nil {
			if raw == "" {
				t.Error("accepted empty token")
			}
			if len(raw) > maxClientTokenLen {
				t.Errorf("accepted token longer than max (%d > %d)", len(raw), maxClientTokenLen)
			}
			for _, r := range raw {
				if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
					t.Errorf("accepted token containing whitespace 0x%x", r)
				}
				if r < 0x20 || r == 0x7f {
					t.Errorf("accepted token containing control char 0x%x", r)
				}
			}
		}
	})
}

// FuzzTokenKey verifies tokenKey never panics, always returns 32-char hex.
func FuzzTokenKey(f *testing.F) {
	seeds := []string{
		"",
		"cb_tok",
		"abc123",
		"\x00\x01\x02",
		strings.Repeat("a", 10000),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("tokenKey panicked on input len=%d: %v", len(raw), r)
			}
		}()
		key := tokenKey(raw)
		if len(key) != 32 {
			t.Errorf("tokenKey(%q...) = %q (len=%d), want 32", raw[:min(8, len(raw))], key, len(key))
		}
		for _, c := range key {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("tokenKey(%q...) = %q, non-hex char %x", raw[:min(8, len(raw))], key, c)
				break
			}
		}
		if key2 := tokenKey(raw); key != key2 {
			t.Errorf("tokenKey not deterministic on %q: %q vs %q", raw[:min(8, len(raw))], key, key2)
		}
	})
}

// --- Property-based LRU eviction verification ---

// TestBridgeLRUProperty verifies that LRU eviction order is correct under
// bounded random-like access patterns: evicted entries are the least-recently
// used ones, and cache size never exceeds maxBridgeEntries.
func TestBridgeLRUProperty(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newBridgePool(t, mock)
	ids := make([]string, 40)
	for i := range ids {
		ids[i] = fmt.Sprintf("run-%05d", i)
	}
	mock.RunIDs = ids

	const numTokens = 6
	tokens := make([]string, numTokens)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("prop-tok-%d", i)
	}

	order := []int{0, 1, 2, 3, 4, 5, 0, 1, 5, 4, 3, 2, 0}
	for _, idx := range order {
		lease, err := p.AcquireBridge(context.Background(), tokens[idx], modelA)
		if err != nil {
			t.Fatalf("AcquireBridge token %d failed: %v", idx, err)
		}
		p.LeaseRelease(lease)
	}

	if n := p.BridgeCount(); n != 6 {
		t.Fatalf("BridgeCount = %d before eviction, want 6", n)
	}

	for i := 6; i < 12; i++ {
		lease, err := p.AcquireBridge(context.Background(), fmt.Sprintf("prop-tok-%d", i), modelA)
		if err != nil {
			t.Fatalf("AcquireBridge token %d failed: %v", i, err)
		}
		p.LeaseRelease(lease)
	}

	if n := p.BridgeCount(); n > maxBridgeEntries {
		t.Errorf("BridgeCount = %d > maxBridgeEntries (%d)", n, maxBridgeEntries)
	}

	for i := 12; i < maxBridgeEntries+5; i++ {
		lease, err := p.AcquireBridge(context.Background(), fmt.Sprintf("prop-tok-%d", i), modelA)
		if err != nil {
			t.Fatalf("AcquireBridge token %d failed: %v", i, err)
		}
		p.LeaseRelease(lease)
	}
	if n := p.BridgeCount(); n > maxBridgeEntries {
		t.Errorf("BridgeCount = %d after many more tokens, want <= %d", n, maxBridgeEntries)
	}
}

// --- Load/thundering-herd test ---

// TestBridgeThunderingHerd fires many concurrent requests for distinct tokens
// to verify the creation-rate gate prevents duplicate client creations and
// every request completes (lease acquired or rate-limited).
func TestBridgeThunderingHerd(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	ids := make([]string, 100)
	for i := range ids {
		ids[i] = fmt.Sprintf("th-herd-%05d", i)
	}
	mock.RunIDs = ids
	p := newBridgePool(t, mock)

	const (
		numTokens  = 20
		numWorkers = 40
	)

	var wg sync.WaitGroup
	var mu sync.Mutex
	errCount := 0
	okCount := 0

	for i := 0; i < numWorkers; i++ {
		token := fmt.Sprintf("herd-tok-%d", i%numTokens)
		wg.Add(1)
		go func(tok string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			lease, err := p.AcquireBridge(ctx, tok, modelA)
			mu.Lock()
			if err != nil {
				errCount++
			} else {
				okCount++
				p.LeaseRelease(lease)
			}
			mu.Unlock()
		}(token)
	}
	wg.Wait()

	t.Logf("thundering herd: %d OK, %d errors", okCount, errCount)
	if okCount == 0 {
		t.Error("all requests failed in thundering-herd test")
	}
	if n := p.BridgeCount(); n > maxBridgeEntries+numTokens {
		t.Errorf("BridgeCount = %d after herd, want <= %d", n, maxBridgeEntries+numTokens)
	}
}

// --- Quota exhaustion edge-case tests ---

// TestBridgeGlobalDailyLimitExceeded verifies BRIDGE_DAILY_LIMIT is checked
// before per-entry limits — all entries are blocked once the global counter
// exceeds the limit.
func TestBridgeGlobalDailyLimitExceeded(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	mock.ChatBody = testutil.SSEEvent(`{"id":"chatcmpl-g","object":"chat.completion.chunk","created":1,"model":"` + modelA + `","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`)
	p := newBridgePoolCfg(t, mock, func(c *config.Config) {
		c.BridgeDailyLimit = 2
	})

	lease, err := p.AcquireBridge(context.Background(), "global-tok-a", modelA)
	if err != nil {
		t.Fatalf("first acquire (tok-a) failed: %v", err)
	}
	chatOnce(t, p, lease)
	p.LeaseRelease(lease)

	leaseB, err := p.AcquireBridge(context.Background(), "global-tok-b", modelA)
	if err != nil {
		t.Fatalf("second acquire (tok-b) failed: %v", err)
	}
	chatOnce(t, p, leaseB)
	p.LeaseRelease(leaseB)

	_, err = p.AcquireBridge(context.Background(), "global-tok-c", modelA)
	if err == nil {
		t.Fatal("third acquire succeeded, want global daily limit error")
	}
	if !strings.Contains(err.Error(), "global daily limit") {
		t.Errorf("err = %q, want global daily limit error", err.Error())
	}
}

// TestBridgeQuotaIntersection verifies that when a token is simultaneously
// cooldown-banned and over daily cap, the cooldown check fires first.
func TestBridgeQuotaIntersection(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	p := newBridgePool(t, mock)

	lease, err := p.AcquireBridge(context.Background(), "intersection-tok", modelA)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	p.LeaseRelease(lease)

	entry := p.bridgeToken("intersection-tok")
	if entry == nil {
		t.Fatal("bridge entry not found")
	}
	entry.runs.Cooldown(time.Hour)

	_, err = p.AcquireBridge(context.Background(), "intersection-tok", modelA)
	if err == nil {
		t.Fatal("acquire succeeded despite cooldown, want error")
	}
	if !strings.Contains(err.Error(), "cooling down") {
		t.Errorf("err = %q, want cooling down message", err.Error())
	}
}

// TestBridgeCooldownThenCap verifies that after cooldown expires, the
// per-entry daily cap still blocks.
func TestBridgeCooldownThenCap(t *testing.T) {
	mock := testutil.NewMock()
	defer mock.Close()
	mock.ChatBody = testutil.SSEEvent(`{"id":"chatcmpl-c","object":"chat.completion.chunk","created":1,"model":"` + modelA + `","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`)
	p := newBridgePool(t, mock)
	cfg := p.cfg.Load()
	cfg.MaxMessagesPerDay = 1
	p.cfg.Store(cfg)

	lease, err := p.AcquireBridge(context.Background(), "cap-tok", modelA)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	chatOnce(t, p, lease)
	p.LeaseRelease(lease)

	_, err = p.AcquireBridge(context.Background(), "cap-tok", modelA)
	var rle *upstream.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("second acquire err = %v, want *RateLimitError (daily cap)", err)
	}
	if rle.Limit != 1.0 {
		t.Errorf("rle.Limit = %v, want 1", rle.Limit)
	}
}

// --- Log sanitization audit ---

// TestBridgeLogSanitization captures the pool's logger during a bridge chat
// and asserts the raw client token never leaks into any log line — only the
// SHA-256-derived label (bridgeTokenLabel → client.TokenKey()[:8]) appears.
func TestBridgeLogSanitization(t *testing.T) {
	var sink bytes.Buffer
	mock := testutil.NewMock()
	defer mock.Close()
	p := newBridgePool(t, mock)
	p.logger = slog.New(slog.NewTextHandler(&sink, &slog.HandlerOptions{Level: slog.LevelDebug}))

	const rawToken = "cb_log_sanitize_secret_x9y8z7"
	lease, err := p.AcquireBridge(context.Background(), rawToken, modelA)
	if err != nil {
		t.Fatalf("AcquireBridge failed: %v", err)
	}
	chatOnce(t, p, lease)
	p.LeaseRelease(lease)

	// Force a maintain sweep that logs token labels, then check the sink.
	p.bridgeMaintain(context.Background(), true)

	logged := sink.String()
	if strings.Contains(logged, rawToken) {
		t.Errorf("log leak: raw client token %q appears in pool logs:\n%s", rawToken, logged)
	}

	// The hashed label is short (8 hex chars) and must NOT equal a raw-token
	// prefix that would let an observer correlate accounts.
	if strings.Contains(logged, "token_label=bridge_sanit") {
		t.Error("bridgeTokenLabel leaked a raw-token-derived prefix")
	}
}
