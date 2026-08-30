package convert

// Output-layer completeness tests: vendor canonical tool-call JSON
// (cb_tool_name keyed), partial-opener streaming extraction, and
// enrichment-key pass-through. These pin the output translation fixes.

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParseToolCallVendorCbToolName pins the vendor's canonical XML tool
// call format (reference common/src/tools/constants.ts toolNameParam): the
// codebuff_tool_call JSON is keyed by cb_tool_name and the remaining keys
// ARE the tool input (cb_easp is the stop sentinel and must never leak into
// arguments).
func TestParseToolCallVendorCbToolName(t *testing.T) {
	t.Run("codebuff_tool_call json format", func(t *testing.T) {
		raw := "Plan:\n<codebuff_tool_call>\n{\"cb_tool_name\":\"bash\",\"command\":\"pwd\",\"cb_easp\":true}\n</codebuff_tool_call>\nDone."
		cleaned, calls := extractXMLToolCalls(raw)
		if cleaned != "Plan:\n\nDone." {
			t.Errorf("cleaned = %q, want %q", cleaned, "Plan:\n\nDone.")
		}
		if len(calls) != 1 {
			t.Fatalf("calls len = %d, want 1", len(calls))
		}
		if calls[0].Function.Name != "bash" {
			t.Errorf("name = %q, want 'bash'", calls[0].Function.Name)
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
			t.Fatalf("arguments not JSON: %v", err)
		}
		if len(args) != 1 || args["command"] != "pwd" {
			t.Errorf("args = %v, want {command: pwd} only (no cb_tool_name/cb_easp)", args)
		}
	})

	t.Run("fenced json cb_tool_name", func(t *testing.T) {
		raw := "```json\n{\"cb_tool_name\":\"write_file\",\"path\":\"x.txt\",\"content\":\"hi\"}\n```"
		cleaned, calls := extractXMLToolCalls(raw)
		if cleaned != "" {
			t.Errorf("cleaned = %q, want empty", cleaned)
		}
		if len(calls) != 1 || calls[0].Function.Name != "write_file" {
			t.Fatalf("calls = %+v, want one write_file call", calls)
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
			t.Fatalf("arguments not JSON: %v", err)
		}
		if args["path"] != "x.txt" || args["content"] != "hi" {
			t.Errorf("args = %v, want {path, content}", args)
		}
	})

	t.Run("bare name format still works", func(t *testing.T) {
		cleaned, calls := extractXMLToolCalls("<tool_call>{\"name\":\"b\",\"arguments\":{\"k\":1}}</tool_call>")
		if cleaned != "" {
			t.Errorf("cleaned = %q, want empty", cleaned)
		}
		if len(calls) != 1 || calls[0].Function.Name != "b" {
			t.Fatalf("calls = %+v, want one b call", calls)
		}
	})
}

// TestXMLStreamExtractorPartialOpener pins partial-opener withholding: an
// opener tag split across fragments (e.g. "<tool_ca" then "ll>...") must
// still extract, and a refuted partial must be emitted as plain text (never
// silently dropped).
func TestXMLStreamExtractorPartialOpener(t *testing.T) {
	t.Run("split opener extracts", func(t *testing.T) {
		var x XMLToolCallExtractor
		tt, cc := x.Feed("intro <tool_ca")
		if tt != "intro " {
			t.Errorf("first Feed text = %q, want 'intro '", tt)
		}
		if len(cc) != 0 {
			t.Errorf("first Feed calls = %d, want 0", len(cc))
		}
		tt, cc = x.Feed("ll><function=bash><parameter=command>pwd</parameter></function></tool_call>")
		if tt != "" {
			t.Errorf("second Feed text = %q, want ''", tt)
		}
		if len(cc) != 1 || cc[0].Function.Name != "bash" {
			t.Fatalf("calls = %+v, want one bash call", cc)
		}
	})

	t.Run("split vendor opener extracts", func(t *testing.T) {
		var x XMLToolCallExtractor
		tt, _ := x.Feed("run: <codebuff_tool_ca")
		if tt != "run: " {
			t.Errorf("first Feed text = %q, want 'run: '", tt)
		}
		tt, cc := x.Feed("ll>{\"cb_tool_name\":\"bash\",\"command\":\"ls\"}</codebuff_tool_call>")
		if tt != "" {
			t.Errorf("second Feed text = %q, want ''", tt)
		}
		if len(cc) != 1 || cc[0].Function.Name != "bash" {
			t.Fatalf("calls = %+v, want one bash call", cc)
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(cc[0].Function.Arguments), &args); err != nil {
			t.Fatalf("arguments not JSON: %v", err)
		}
		if args["command"] != "ls" {
			t.Errorf("args = %v, want {command: ls}", args)
		}
	})

	t.Run("refuted partial emitted as text", func(t *testing.T) {
		var x XMLToolCallExtractor
		tt, cc := x.Feed("the <function_ca")
		if tt != "the " || len(cc) != 0 {
			t.Fatalf("first Feed = (%q, %d), want ('the ', 0)", tt, len(cc))
		}
		tt, cc = x.Feed("llery supports it")
		if tt != "<function_callery supports it" {
			t.Errorf("second Feed text = %q, want withheld text back", tt)
		}
		if len(cc) != 0 {
			t.Errorf("calls = %d, want 0", len(cc))
		}
	})

	t.Run("stream end releases partial as text", func(t *testing.T) {
		var x XMLToolCallExtractor
		_, _ = x.Feed("untagged <tool_ca")
		tt, cc := x.Flush()
		// The unresolved partial must surface as text, not be dropped
		// (lossless relay; the vendor drops silently — we choose to keep).
		if tt != "<tool_ca" {
			t.Errorf("Flush text = %q, want '<tool_ca'", tt)
		}
		if len(cc) != 0 {
			t.Errorf("Flush calls = %d, want 0", len(cc))
		}
	})
}

// TestXMLStreamExtractorClosedTooLargeBoundAndPartial guards the bounded
// hold: a pathological fragment stream that keeps matching a partial opener
// never grows the hold past the longest opener, and the guard's 64 KiB
// bound applies to real blocks only.
func TestXMLStreamExtractorPartialHoldBounded(t *testing.T) {
	var x XMLToolCallExtractor
	// Repeated "<" cannot re-hold forever: each Feed emits or advances.
	tt, _ := x.Feed("x <")
	if tt != "x " {
		t.Errorf("Feed text = %q, want 'x '", tt)
	}
	tt, _ = x.Feed("<")
	if tt != "<" {
		t.Errorf("Feed text = %q, want '<' (the first partial was refuted)", tt)
	}
}

// TestSanitizeChunkEnrichmentKeys pins the optional top-level enrichment
// keys (service_tier / obfuscation / moderation) passing through the
// sanitizer instead of being dropped as unknown keys, while genuinely
// unknown keys (e.g. a dialect-only field) stay dropped.
func TestSanitizeChunkEnrichmentKeys(t *testing.T) {
	in := `{"id":"chatcmpl-a","object":"chat.completion.chunk","created":1,"model":"m","service_tier":"default","obfuscation":"abc","moderation":{"id":"modr_1","status":"flagged"},"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}],"usage":null}`
	out, drop := SanitizeChunk([]byte(in))
	if drop {
		t.Fatal("chunk dropped")
	}
	var chunk map[string]any
	if err := json.Unmarshal(out, &chunk); err != nil {
		t.Fatalf("output not JSON: %v: %s", err, out)
	}
	if chunk["service_tier"] != "default" {
		t.Errorf("service_tier = %v, want 'default'", chunk["service_tier"])
	}
	if chunk["obfuscation"] != "abc" {
		t.Errorf("obfuscation = %v, want 'abc'", chunk["obfuscation"])
	}
	mod, _ := chunk["moderation"].(map[string]any)
	if mod == nil || mod["id"] != "modr_1" {
		t.Errorf("moderation = %v, want {id: modr_1}", chunk["moderation"])
	}
	if chunk["usage"] != nil {
		t.Errorf("null usage must be dropped, got %v", chunk["usage"])
	}

	// Unknown keys stay dropped.
	in2 := `{"id":"chatcmpl-b","object":"chat.completion.chunk","created":1,"model":"m","dialect_only":true,"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`
	out2, drop2 := SanitizeChunk([]byte(in2))
	if drop2 {
		t.Fatal("chunk dropped")
	}
	if strings.Contains(string(out2), "dialect_only") {
		t.Errorf("unknown top-level key leaked: %s", out2)
	}
}

// TestSanitizeChunkEnrichmentKeysFastPath pins the zero-allocation path:
// a chunk that satisfies every invariant (including the enrichment keys)
// is relayed verbatim.
func TestSanitizeChunkEnrichmentKeysFastPath(t *testing.T) {
	in := []byte(`{"id":"chatcmpl-f","object":"chat.completion.chunk","created":1,"model":"m","service_tier":"default","choices":[{"index":0,"delta":{"content":"hi","role":"assistant"},"finish_reason":null}]}`)
	out, drop := SanitizeChunk(in)
	if drop {
		t.Fatal("chunk dropped")
	}
	if string(out) != string(in) {
		t.Errorf("fast path re-encoded: %s", out)
	}
}
