// Protocol drift detection regression tests (Phase 4.3)
//
// Pins the known upstream wire protocol shapes so upstream schema/
// code changes are caught by CI before any real traffic is affected.
//
// ALL tests here use only local helpers (classifyError, parseFlexTime) —
// no network, no mock server.
//
// Adding a regression case is mandatory when:
//   - upstream introduces a new error code or body shape
//   - a new status-to-error-type mapping is added
//   - a known SSE event lifecycle changes
//   - a new admission response status string is observed
package upstream

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 1. Upstream error classification matrix
// ---------------------------------------------------------------------------

func TestProtocolRegressionErrorMatrix(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		wantIs error
		wantAs any
		denyIs error
	}{
		{"401 auth rejected", 401, `{"error":"Invalid API key"}`, ErrAuthRejected, nil, nil},
		{"403 banned bare", 403, `{"status":"banned"}`, nil, &BanError{}, nil},
		{"403 banned with resumes_at", 403, `{"status":"banned","resumes_at":"2026-08-12T08:00:00Z"}`, nil, &BanError{}, nil},
		{"403 account_suspended", 403, `{"error":"account_suspended","message":"billing"}`, nil, &BanError{}, nil},
		{"403 country_blocked", 403, `{"status":"country_blocked","countryCode":"CN","countryBlockReason":"region"}`, nil, &CountryBlockedError{}, nil},
		{"403 free_mode_cli_required", 403, `{"error":"free_mode_cli_required"}`, ErrFreeModeCLIRequired, nil, nil},
		{"402 out of credits", 402, `{"error":"Out of credits"}`, nil, &CreditsError{}, nil},
		{"409 session_superseded", 409, `{"error":"session_superseded"}`, nil, &SessionSupersededError{}, nil},
		{"409 session_limit_reached NOT session-invalid", 409, `{"error":{"code":"session_limit_reached"}}`, nil, &SessionLimitError{}, ErrSessionInvalid},
		{"409 model_locked", 409, `{"error":"model_locked"}`, ErrSessionInvalid, nil, nil},
		{"428 waiting_room_required", 428, `{"error":"waiting_room_required"}`, nil, &WaitingRoomRequiredError{}, nil},
		{"503 waiting room bare", 503, `{}`, nil, &WaitingRoomError{}, nil},
		{"429 ip_capped", 429, `{"status":"ip_capped","activeUsersForIp":5,"limit":4,"retryAfterMs":30000}`, ErrIpCapped, &IpCappedError{}, nil},
		{"429 rate_limited pacific_day", 429, `{"status":"rate_limited","recentCount":6,"limit":6,"period":"pacific_day","resetAt":"2026-08-12T07:00:00Z"}`, ErrRateLimited, &RateLimitError{}, nil},
		{"429 spend_limited", 429, `{"status":"spend_limited","message":"budget reached","retryAfterMs":60000}`, nil, &RateLimitError{}, nil},
		{"429 free_mode_rate_limited", 429, `{"error":"free_mode_rate_limited","message":"30 minutes limit"}`, ErrRateLimited, &RateLimitError{}, nil},
		{"429 insufficient_quota", 429, `{"error":"insufficient_quota"}`, nil, &RateLimitError{}, nil},
		{"429 limit_burst_rate", 429, `{"error":"limit_burst_rate"}`, nil, &RateLimitError{}, nil},
		{"429 peak hours", 429, `{"error":"rate_limited","message":"peak hours"}`, nil, &RateLimitError{}, nil},
		{"429 capacity_deferred", 429, `{"error":{"code":"free_mode_capacity_deferred"}}`, ErrCapacityDeferred, &CapacityDeferredError{}, nil},
		{"429 waiting_room_queued NOT session-invalid", 429, `{"error":{"code":"waiting_room_queued"}}`, nil, &WaitingRoomError{}, ErrSessionInvalid},
		{"502 session_expired", 502, `{"error":"session_expired"}`, ErrSessionInvalid, nil, nil},
		{"502 session_model_mismatch", 502, `{"error":"session_model_mismatch"}`, ErrSessionInvalid, nil, nil},
		{"409 limited_ip", 409, `{"error":"session_model_mismatch","message":"Model not available. limited"}`, ErrModelIPLimited, &LimitedIpError{}, nil},
		{"502 freebuff_update_required", 502, `{"error":"freebuff_update_required"}`, ErrSessionInvalid, nil, nil},
		{"400 runId not found", 400, `{"error":"runid not found"}`, ErrRunInvalid, nil, nil},
		{"500 generic upstream error", 500, `{"error":"internal server error"}`, nil, nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyError(tt.status, tt.body, http.Header{})
			if err == nil {
				t.Fatal("classifyError returned nil")
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("errors.Is(%v) = false; got %v", tt.wantIs, err)
			}
			if tt.denyIs != nil && errors.Is(err, tt.denyIs) {
				t.Errorf("errors.Is(%v) should be false, but got true", tt.denyIs)
			}
			if tt.wantAs != nil {
				assertConcreteType(t, err, tt.wantAs)
			}
		})
	}
}

func assertConcreteType(t *testing.T, err error, target any) {
	t.Helper()
	switch target.(type) {
	case *BanError:
		if !errors.As(err, new(*BanError)) {
			t.Errorf("want *BanError, got %T: %v", err, err)
		}
	case *CountryBlockedError:
		if !errors.As(err, new(*CountryBlockedError)) {
			t.Errorf("want *CountryBlockedError, got %T: %v", err, err)
		}
	case *IpCappedError:
		if !errors.As(err, new(*IpCappedError)) {
			t.Errorf("want *IpCappedError, got %T: %v", err, err)
		}
	case *RateLimitError:
		if !errors.As(err, new(*RateLimitError)) {
			t.Errorf("want *RateLimitError, got %T: %v", err, err)
		}
	case *CreditsError:
		if !errors.As(err, new(*CreditsError)) {
			t.Errorf("want *CreditsError, got %T: %v", err, err)
		}
	case *WaitingRoomError:
		if !errors.As(err, new(*WaitingRoomError)) {
			t.Errorf("want *WaitingRoomError, got %T: %v", err, err)
		}
	case *WaitingRoomRequiredError:
		if !errors.As(err, new(*WaitingRoomRequiredError)) {
			t.Errorf("want *WaitingRoomRequiredError, got %T: %v", err, err)
		}
	case *SessionSupersededError:
		if !errors.As(err, new(*SessionSupersededError)) {
			t.Errorf("want *SessionSupersededError, got %T: %v", err, err)
		}
	case *SessionLimitError:
		if !errors.As(err, new(*SessionLimitError)) {
			t.Errorf("want *SessionLimitError, got %T: %v", err, err)
		}
	case *CapacityDeferredError:
		if !errors.As(err, new(*CapacityDeferredError)) {
			t.Errorf("want *CapacityDeferredError, got %T: %v", err, err)
		}
	case *LimitedIpError:
		if !errors.As(err, new(*LimitedIpError)) {
			t.Errorf("want *LimitedIpError, got %T: %v", err, err)
		}
	default:
		t.Fatalf("unhandled target type %T", target)
	}
}

// ---------------------------------------------------------------------------
// 2. parseFlexTime contract — upstream timestamps
// ---------------------------------------------------------------------------

func TestProtocolRegressionParseFlexTime(t *testing.T) {
	epoch2026 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		input   any
		want    time.Time
		wantErr bool
	}{
		{"RFC3339", "2026-01-01T00:00:00Z", epoch2026, false},
		{"unix seconds string", "1767225600", epoch2026, false},
		{"unix seconds float64", float64(1767225600), epoch2026, false},
		{"unix millis float64", float64(1767225600123), time.Date(2026, 1, 1, 0, 0, 0, 123000000, time.UTC), false},
		{"nil", nil, time.Time{}, true},
		{"empty string", "", time.Time{}, true},
		{"unparseable", "not-a-date", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFlexTime(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseFlexTime(%v) succeeded, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlexTime(%v) error: %v", tt.input, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("parseFlexTime(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. RateLimitError field extraction — full quota payload
// ---------------------------------------------------------------------------

func TestProtocolRegressionRateLimitFields(t *testing.T) {
	body := `{"status":"rate_limited","model":"deepseek/deepseek-v4-flash","limit":6,"recentCount":5,"period":"pacific_day","resetAt":"2026-08-12T07:00:00Z","retryAfterMs":3600000}`
	err := classifyError(429, body, http.Header{})
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("want *RateLimitError, got %T: %v", err, err)
	}
	if rle.Status != "rate_limited" {
		t.Errorf("Status = %q", rle.Status)
	}
	if rle.Model != "deepseek/deepseek-v4-flash" {
		t.Errorf("Model = %q", rle.Model)
	}
	if rle.Limit != 6 {
		t.Errorf("Limit = %v", rle.Limit)
	}
	if rle.RecentCount != 5 {
		t.Errorf("RecentCount = %v", rle.RecentCount)
	}
	if rle.Period != "pacific_day" {
		t.Errorf("Period = %q", rle.Period)
	}
	if rle.ResetAt.IsZero() {
		t.Error("ResetAt is zero")
	}
	if rle.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v", rle.RetryAfter)
	}
}

// ---------------------------------------------------------------------------
// 4. IpCappedError field extraction
// ---------------------------------------------------------------------------

func TestProtocolRegressionIpCappedFields(t *testing.T) {
	body := `{"status":"ip_capped","activeUsersForIp":3,"limit":2,"retryAfterMs":45000}`
	err := classifyError(429, body, http.Header{})
	var ice *IpCappedError
	if !errors.As(err, &ice) {
		t.Fatalf("want *IpCappedError, got %T: %v", err, err)
	}
	if ice.ActiveUsersForIP != 3 {
		t.Errorf("ActiveUsersForIP = %d", ice.ActiveUsersForIP)
	}
	if ice.Limit != 2 {
		t.Errorf("Limit = %v", ice.Limit)
	}
	if ice.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v", ice.RetryAfter)
	}
}

// ---------------------------------------------------------------------------
// 5. CountryBlockedError field extraction
// ---------------------------------------------------------------------------

func TestProtocolRegressionCountryBlockedFields(t *testing.T) {
	body := `{"status":"country_blocked","countryCode":"IN","countryBlockReason":"region_restricted"}`
	err := classifyError(403, body, http.Header{})
	var cbe *CountryBlockedError
	if !errors.As(err, &cbe) {
		t.Fatalf("want *CountryBlockedError, got %T", err)
	}
	if cbe.CountryCode != "IN" {
		t.Errorf("CountryCode = %q", cbe.CountryCode)
	}
	if cbe.CountryBlockReason != "region_restricted" {
		t.Errorf("CountryBlockReason = %q", cbe.CountryBlockReason)
	}
}

// ---------------------------------------------------------------------------
// 6. SSE contract documentation (compile-time anchor)
//
// The canonical Anthropic SSE lifecycle:
//   message_start → content_block_start(s) → content_block_delta(s)
//     → content_block_stop(s) → message_delta → message_stop
//
// Thinking blocks MUST include "signature": "".
// OpenAI chunks MUST include logprobs:null, refusal:null.
//
// Runtime verification lives in harness_compatibility_test.go.
// ---------------------------------------------------------------------------

const anthropicLifecycle = "message_start → content_block_start → content_block_delta → content_block_stop → message_delta → message_stop"
