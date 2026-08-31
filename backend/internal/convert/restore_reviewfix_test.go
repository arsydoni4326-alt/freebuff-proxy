package convert

import (
	"encoding/json"
	"testing"
	"time"

	"freebuff-proxy/backend/internal/reasoningcache"
)

// Regression for the review's P2-5 wire path (devdocs/review-2026-08-31.md):
// the toolID reasoning-restore lookup in normalizeMessages presents the
// message's own content as a binding, so a per-conversation sequential
// tool_call_id ("call_1") cannot restore another conversation's reasoning.
// The cache rejects bound mismatches (reasoningcache.Get); these tests pin
// that the wire lookup actually supplies the binding.
func TestReasoningRestoreBindsContent(t *testing.T) {
	cache := reasoningcache.New(100, time.Hour)
	defer SetReasoningLookup(nil)
	SetReasoningLookup(cache.Get)

	restoreRequest := func(t *testing.T, content string) map[string]any {
		t.Helper()
		payload := map[string]any{
			"model": "test-model",
			"messages": []any{map[string]any{
				"role":    "assistant",
				"content": content,
				"tool_calls": []any{map[string]any{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      "ls",
						"arguments": "{}",
					},
				}},
			}},
		}
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		out, err := NormalizeRequest(b, "")
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		msgs, _ := got["messages"].([]any)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		m, _ := msgs[0].(map[string]any)
		if m == nil {
			t.Fatal("expected assistant message object")
		}
		return m
	}

	t.Run("foreign content under colliding tool_call_id restores nothing", func(t *testing.T) {
		cache.Put([]string{"call_1"}, "conversation A assistant text", "", "reasoning A", "", "m")
		m := restoreRequest(t, "conversation B assistant text")
		if rc, exists := m["reasoning_content"]; exists {
			if s, isStr := rc.(string); isStr && s != "" {
				t.Fatalf("expected no reasoning restore for foreign content, got %q", s)
			}
		}
	})

	t.Run("own content restores reasoning", func(t *testing.T) {
		cache.Put([]string{"call_1"}, "conversation C assistant text", "", "reasoning C", "", "m")
		m := restoreRequest(t, "conversation C assistant text")
		if rc, _ := m["reasoning_content"].(string); rc != "reasoning C" {
			t.Fatalf("expected own-content restore, got %q", rc)
		}
	})
}
