package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"freebuff-proxy/backend/internal/pool"
	"freebuff-proxy/backend/internal/runs"
	"freebuff-proxy/backend/internal/upstream"
)

// runChatAttemptRetry drives chatAttempt through one failed chat + the
// retry-once re-acquire, recording every upstream envelope and which
// invalidation/cooldown hooks fired. The pool stays nil: the synthetic
// leases carry no entry/bridge backing, so LeaseRelease is a no-op, and
// zero AcquiredAt keeps the success path off ClearModelUnfitBefore.
func runChatAttemptRetry(t *testing.T, firstLease, retryLease *pool.Lease, failFirst error) (captured []upstream.ChatOptions, invalidated []string) {
	t.Helper()
	acquires := 0
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	up, _, err := s.chatAttempt(context.Background(), "deepseek/deepseek-v4-flash", []byte(`{}`), &chatTraceState{reqID: "req-rf"},
		func(ctx context.Context, model string) (*pool.Lease, error) {
			acquires++
			if acquires == 1 {
				return firstLease, nil
			}
			return retryLease, nil
		},
		func(ctx context.Context, l *pool.Lease, opts upstream.ChatOptions, body []byte) (io.ReadCloser, error) {
			captured = append(captured, opts)
			if len(captured) == 1 {
				return nil, failFirst
			}
			return io.NopCloser(strings.NewReader("ok")), nil
		},
		func(l *pool.Lease) { t.Error("invalidateSession must not fire") },
		func(l *pool.Lease) { t.Error("invalidateSessionSuperseded must not fire") },
		func(l *pool.Lease, agentID string) { invalidated = append(invalidated, agentID) },
		func(l *pool.Lease) { t.Error("cooldownAuth must not fire") },
		func(l *pool.Lease, be *upstream.BanError) { t.Error("cooldownBan must not fire") },
		func(l *pool.Lease, rle *upstream.RateLimitError) { t.Error("cooldownRate must not fire") },
		func(l *pool.Lease, ice *upstream.IpCappedError) { t.Error("cooldownIpCapped must not fire") },
		func(l *pool.Lease, cbe *upstream.CountryBlockedError) { t.Error("cooldownCountry must not fire") },
	)
	if err != nil {
		t.Fatalf("chatAttempt returned error: %v", err)
	}
	if up == nil {
		t.Fatal("chatAttempt returned nil body on success")
	}
	_ = up.Close()
	if acquires != 2 {
		t.Errorf("acquires = %d, want 2 (one retry)", acquires)
	}
	if len(captured) != 2 {
		t.Fatalf("chat attempts captured = %d, want 2", len(captured))
	}
	return captured, invalidated
}

// TestChatAttemptRetryCarriesFreshRunIdentity pins the review-2026-08-31
// P3 fix: after a run-invalid retry re-acquires a lease on a NEW run, the
// upstream envelope carries the fresh run's TraceSessionID/ClientID/AgentID
// and its step counter — not the dead run's identity.
func TestChatAttemptRetryCarriesFreshRunIdentity(t *testing.T) {
	// Runs are constructed per subtest: StepCount persists on a shared
	// *runs.Run, and the identity assertions must not depend on test order.
	t.Run("fresh run refreshes trace identity", func(t *testing.T) {
		runA := &runs.Run{RunID: "run-1", TraceSessionID: "trace-1", ClientID: "client-1", AgentID: "agent-1"}
		runB := &runs.Run{RunID: "run-2", TraceSessionID: "trace-2", ClientID: "client-2", AgentID: "agent-2"}
		captured, invalidated := runChatAttemptRetry(t,
			&pool.Lease{Model: "deepseek/deepseek-v4-flash", AgentID: "agent-1", Run: runA, SessionInstanceID: "inst-1"},
			&pool.Lease{Model: "deepseek/deepseek-v4-flash", AgentID: "agent-2", Run: runB, SessionInstanceID: "inst-2"},
			upstream.ErrRunInvalid)

		if got := invalidated; len(got) != 1 || got[0] != "agent-1" {
			t.Errorf("invalidateRun calls = %v, want [agent-1]", got)
		}
		want := upstream.ChatOptions{
			Model:             "deepseek/deepseek-v4-flash",
			RunID:             "run-2",
			SessionInstanceID: "inst-2",
			TraceSessionID:    "trace-2",
			ClientID:          "client-2",
			AgentID:           "agent-2",
			RequestID:         "req-rf",
			StepNumber:        1, // the fresh run's first step
		}
		if captured[1] != want {
			t.Errorf("retry envelope = %+v, want %+v (fresh run identity)", captured[1], want)
		}
	})

	t.Run("same run keeps trace identity and step", func(t *testing.T) {
		// Same RunID on the retry lease (transient-error path): the
		// identity and the step number must stay untouched — the retry
		// repeats the SAME step of the SAME run.
		runA := &runs.Run{RunID: "run-1", TraceSessionID: "trace-1", ClientID: "client-1", AgentID: "agent-1"}
		runA2 := &runs.Run{RunID: "run-1", TraceSessionID: "trace-1", ClientID: "client-1", AgentID: "agent-1"}
		captured, invalidated := runChatAttemptRetry(t,
			&pool.Lease{Model: "deepseek/deepseek-v4-flash", AgentID: "agent-1", Run: runA, SessionInstanceID: "inst-1"},
			&pool.Lease{Model: "deepseek/deepseek-v4-flash", AgentID: "agent-1", Run: runA2, SessionInstanceID: "inst-1"},
			errors.New("transient transport failure"))

		if got := invalidated; len(got) != 0 {
			t.Errorf("invalidateRun calls = %v, want none on a transient error", got)
		}
		if captured[1] != captured[0] {
			t.Errorf("same-run retry envelope changed: %+v -> %+v (want identical)", captured[0], captured[1])
		}
	})
}
