package server

// Output-layer completeness tests: first-chunk role injection on the OpenAI
// chat wire, Anthropic stop_reason mapping for content_filter (→ refusal)
// on both stream and JSON paths, the Responses function-call done event and
// its usage detail mapping. These drive the relays directly with scripted
// SSE readers — no network/timing flakiness.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOpenAIStreamFirstChunkRoleInjected pins the first-chunk role
// guarantee: the OpenAI spec stamps "role":"assistant" in the first chunk
// of a chat completion stream, but an upstream that omits it (e.g. when
// the first delta is pure content) would leave clients without a role.
func TestOpenAIStreamFirstChunkRoleInjected(t *testing.T) {
	t.Run("missing role injected on first chunk only", func(t *testing.T) {
		s := testRelayServer()
		rec := httptest.NewRecorder()
		ss := strings.Join([]string{
			testutilSSE(`{"id":"chatcmpl-r1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`),
			testutilSSE(`{"id":"chatcmpl-r1","choices":[{"index":0,"delta":{"content":" there"},"finish_reason":null}]}`),
			testutilSSE(`{"id":"chatcmpl-r1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		}, "")
		s.relayStream(context.Background(), rec, strings.NewReader(ss), &relayStats{}, time.Now())
		frames := collectSSEFrames(t, rec.Body.String())
		if len(frames) < 3 {
			t.Fatalf("frames = %d, want >= 3", len(frames))
		}
		first, _ := frames[0].data["choices"].([]any)
		firstChoice, _ := first[0].(map[string]any)
		firstDelta, _ := firstChoice["delta"].(map[string]any)
		if firstDelta["role"] != "assistant" {
			t.Errorf("first chunk delta.role = %v, want assistant", firstDelta["role"])
		}
		if firstDelta["content"] != "hi" {
			t.Errorf("first chunk content = %v, want 'hi'", firstDelta["content"])
		}
		// Later chunks must NOT get a role injected (and keep their bytes
		// otherwise).
		second, _ := frames[1].data["choices"].([]any)
		secondChoice, _ := second[0].(map[string]any)
		secondDelta, _ := secondChoice["delta"].(map[string]any)
		if _, has := secondDelta["role"]; has {
			t.Errorf("later chunk got role injected: %v", secondDelta)
		}
	})

	t.Run("existing role untouched", func(t *testing.T) {
		s := testRelayServer()
		rec := httptest.NewRecorder()
		ss := testutilSSE(`{"id":"chatcmpl-r2","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`)
		s.relayStream(context.Background(), rec, strings.NewReader(ss), &relayStats{}, time.Now())
		frames := collectSSEFrames(t, rec.Body.String())
		first, _ := frames[0].data["choices"].([]any)
		firstChoice, _ := first[0].(map[string]any)
		firstDelta, _ := firstChoice["delta"].(map[string]any)
		if firstDelta["role"] != "assistant" {
			t.Errorf("existing role clobbered: %v", firstDelta["role"])
		}
	})

	t.Run("usage-only first chunk does not consume the role slot", func(t *testing.T) {
		s := testRelayServer()
		rec := httptest.NewRecorder()
		ss := strings.Join([]string{
			testutilSSE(`{"id":"chatcmpl-r3","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
			testutilSSE(`{"id":"chatcmpl-r3","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`),
		}, "")
		s.relayStream(context.Background(), rec, strings.NewReader(ss), &relayStats{}, time.Now())
		frames := collectSSEFrames(t, rec.Body.String())
		if len(frames) != 2 {
			t.Fatalf("frames = %d, want 2", len(frames))
		}
		second, _ := frames[1].data["choices"].([]any)
		secondChoice, _ := second[0].(map[string]any)
		secondDelta, _ := secondChoice["delta"].(map[string]any)
		if secondDelta["role"] != "assistant" {
			t.Errorf("content chunk after usage-only frame missing role: %v", secondDelta)
		}
	})
}

// TestAnthropicStreamInBandErrorChunk pins the in-band upstream error
// path: an OpenAI-shaped error chunk mid-stream must surface as an
// Anthropic error event and terminate — the client must never see a
// successfully completed message for a failed upstream turn.
func TestAnthropicStreamInBandErrorChunk(t *testing.T) {
	s := testRelayServer()
	rec := httptest.NewRecorder()
	ss := strings.Join([]string{
		testutilSSE(`{"id":"chatcmpl-e1","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`),
		testutilSSE(`{"error":{"message":"upstream boom","type":"server_error","code":"e500"}}`),
		testutilSSE(`{"id":"chatcmpl-e1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
	}, "")
	s.relayAnthropicStream(context.Background(), rec, strings.NewReader(ss), &relayStats{}, time.Now(), "m", 0)
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"error"`) {
		t.Fatalf("stream missing error event: %q", truncateStr(body, 400))
	}
	if !strings.Contains(body, "upstream boom") {
		t.Errorf("error event missing upstream message: %q", truncateStr(body, 400))
	}
	// A terminated-by-error stream must not claim a completed message.
	if strings.Contains(body, `"type":"message_stop"`) {
		t.Errorf("message_stop emitted after error: %q", truncateStr(body, 400))
	}
	if strings.Contains(body, `"stop_reason":"end_turn"`) {
		t.Errorf("message_delta stop_reason end_turn after error: %q", truncateStr(body, 400))
	}
}

// TestAnthropicStreamContentFilterMapsToRefusal pins the stop_reason
// mapping: an upstream OpenAI "content_filter" finish reason must surface
// as the Anthropic "refusal" StopReason (there is no "content_filter" in
// the Anthropic vocabulary), with output_tokens usage still attached.
func TestAnthropicStreamContentFilterMapsToRefusal(t *testing.T) {
	s := testRelayServer()
	rec := httptest.NewRecorder()
	ss := strings.Join([]string{
		testutilSSE(`{"id":"chatcmpl-cf","choices":[{"index":0,"delta":{"content":"I can't help with that"},"finish_reason":null}]}`),
		testutilSSE(`{"id":"chatcmpl-cf","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`),
	}, "")
	s.relayAnthropicStream(context.Background(), rec, strings.NewReader(ss), &relayStats{}, time.Now(), "m", 0)
	var delta map[string]any
	found := false
	for _, ev := range collectSSEFrames(t, rec.Body.String()) {
		if ev.data["type"] == "message_delta" {
			delta, _ = ev.data["delta"].(map[string]any)
			found = true
		}
	}
	if !found {
		t.Fatal("message_delta not emitted")
	}
	if delta["stop_reason"] != "refusal" {
		t.Errorf("stop_reason = %v, want refusal", delta["stop_reason"])
	}
}

// TestAnthropicJSONContentFilterMapsToRefusal pins the same mapping on the
// non-streaming /v1/messages path.
func TestAnthropicJSONContentFilterMapsToRefusal(t *testing.T) {
	s := testRelayServer()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	ss := strings.Join([]string{
		testutilSSE(`{"id":"chatcmpl-cj","choices":[{"index":0,"delta":{"content":"I can't help with that"},"finish_reason":null}]}`),
		testutilSSE(`{"id":"chatcmpl-cj","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`),
	}, "")
	s.relayAnthropicJSON(context.Background(), rec, r, strings.NewReader(ss), &relayStats{}, time.Now(), "m")
	var msg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("response not JSON: %v: %s", err, truncateStr(rec.Body.String(), 400))
	}
	if msg["stop_reason"] != "refusal" {
		t.Errorf("stop_reason = %v, want refusal", msg["stop_reason"])
	}
}

// TestResponsesStreamFunctionCallArgumentsDoneSequence pins the spec's
// function-call output-item sequence: output_item.added,
// function_call_arguments.delta (per fragment), function_call_arguments.done
// with the final assembled arguments, then output_item.done.
func TestResponsesStreamFunctionCallArgumentsDoneSequence(t *testing.T) {
	s := testRelayServer()
	rec := httptest.NewRecorder()
	ss := strings.Join([]string{
		testutilSSE(`{"id":"chatcmpl-fc","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":"}}]},"finish_reason":null}]}`),
		testutilSSE(`{"id":"chatcmpl-fc","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"pwd\"}"}}]},"finish_reason":null}]}`),
		testutilSSE(`{"id":"chatcmpl-fc","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`),
	}, "")
	s.relayResponsesStream(context.Background(), rec, strings.NewReader(ss), &relayStats{}, time.Now(), "m", "resp_fc")
	var seq []string
	var done map[string]any
	for _, ev := range collectSSEFrames(t, rec.Body.String()) {
		seq = append(seq, ev.data["type"].(string))
		if ev.data["type"] == "response.function_call_arguments.done" {
			done = ev.data
		}
	}
	// The done event must land before the item's output_item.done.
	doneIdx, itemIdx := -1, -1
	for i, typ := range seq {
		if typ == "response.function_call_arguments.done" {
			doneIdx = i
		}
		if typ == "response.output_item.done" {
			itemIdx = i
		}
	}
	if doneIdx < 0 {
		t.Fatal("function_call_arguments.done not emitted")
	}
	if itemIdx < 0 || doneIdx > itemIdx {
		t.Errorf("args.done (idx %d) must precede output_item.done (idx %d)", doneIdx, itemIdx)
	}
	if done == nil || done["call_id"] != "call_1" || done["name"] != "bash" {
		t.Errorf("args.done = %v, want call_id call_1 name bash", done)
	}
	if done["arguments"] != `{"command":"pwd"}` {
		t.Errorf("args.done arguments = %v, want assembled %q", done["arguments"], `{"command":"pwd"}`)
	}
}

// TestResponsesJSONUsageCarriesDetails pins the usage translation on the
// non-streaming Responses path: OpenAI-shaped detail keys
// (prompt_tokens_details / completion_tokens_details) must surface in the
// Responses-native input/output details.
func TestResponsesJSONUsageCarriesDetails(t *testing.T) {
	s := testRelayServer()
	rec := httptest.NewRecorder()
	ss := strings.Join([]string{
		testutilSSE(`{"id":"chatcmpl-ud","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`),
		testutilSSE(`{"id":"chatcmpl-ud","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28,"prompt_tokens_details":{"cached_tokens":7},"completion_tokens_details":{"reasoning_tokens":3}}}`),
	}, "")
	s.relayResponsesJSON(context.Background(), rec, strings.NewReader(ss), &relayStats{}, time.Now(), "m", "resp_ud")
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	usage, _ := resp["usage"].(map[string]any)
	if usage == nil {
		t.Fatal("usage missing")
	}
	inDetails, _ := usage["input_tokens_details"].(map[string]any)
	if inDetails == nil || inDetails["cached_tokens"].(float64) != 7 {
		t.Errorf("input_tokens_details = %v, want cached_tokens 7", inDetails)
	}
	outDetails, _ := usage["output_tokens_details"].(map[string]any)
	if outDetails == nil || outDetails["reasoning_tokens"].(float64) != 3 {
		t.Errorf("output_tokens_details = %v, want reasoning_tokens 3", outDetails)
	}
	if usage["total_tokens"].(float64) != 28 {
		t.Errorf("total_tokens = %v, want 28", usage["total_tokens"])
	}
}
