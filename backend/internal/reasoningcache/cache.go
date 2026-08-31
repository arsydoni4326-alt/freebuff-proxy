package reasoningcache

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxEntries is the default maximum number of entries held in cache.
	DefaultMaxEntries = 10000
	// DefaultTTL is the default time-to-live for cache entries.
	DefaultTTL = 2 * time.Hour
)

// Entry stores reasoning content, signature, and metadata for a tool call or completion.
type Entry struct {
	ReasoningContent string
	Signature        string
	Model            string
	CreatedAt        time.Time
}

type cacheNode struct {
	entry       *Entry
	toolCallIDs []string
	// hashKey is sha256(content+toolCallsJSON) as recorded at Put time. It
	// indexes byHash.
	hashKey string
	// bindKey is the canonical binding recorded at Put time:
	// sha256(content + "\x00" + canonicalToolKey), "" when the entry was
	// stored with neither content nor tool-call identity (no binding).
	// Get verifies it on toolID hits: it is symmetric across every Put
	// call site (streaming relays hold structured fields, non-streaming
	// ones raw JSON — both reduce to the same canonical key) while still
	// rejecting cross-conversation tool_call_id collisions whose content
	// or tool-call identity differs.
	bindKey string
	element *list.Element
}

// Cache is a thread-safe LRU/TTL cache for tool call reasoning content and signatures.
type Cache struct {
	mu         sync.RWMutex
	byToolID   map[string]*Entry
	byHash     map[string]*Entry
	nodes      map[*Entry]*cacheNode
	lru        *list.List
	maxEntries int
	ttl        time.Duration
}

// New creates a new Cache with the given maxEntries and ttl.
// If maxEntries <= 0, DefaultMaxEntries is used.
// If ttl <= 0, DefaultTTL is used.
func New(maxEntries int, ttl time.Duration) *Cache {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Cache{
		byToolID:   make(map[string]*Entry),
		byHash:     make(map[string]*Entry),
		nodes:      make(map[*Entry]*cacheNode),
		lru:        list.New(),
		maxEntries: maxEntries,
		ttl:        ttl,
	}
}

func hashKey(content, toolCallsJSON string) string {
	if content == "" && toolCallsJSON == "" {
		return ""
	}
	h := sha256.Sum256([]byte(content + toolCallsJSON))
	return hex.EncodeToString(h[:])
}

// Canonical tool-call identity encoding. The separators are ASCII control
// characters that never appear unescaped inside JSON string values, so the
// encoding is unambiguous without length-prefixing.
const (
	canonicalCallSep = "\x1e" // record separator between tool calls
	canonicalFldSep  = "\x1f" // unit separator between id, name, arguments
)

// CanonicalizeToolCallsJSON reduces a raw JSON tool_calls array to its
// canonical identity key. Each element may arrive in any of the wire
// shapes callers use — flat {"id","name","arguments"}, OpenAI
// {"id","type","function":{"name","arguments"}}, or Anthropic tool_use
// {"id","name","input"} — and all reduce to the same (id, name, arguments)
// triple. Elements without a usable id are skipped so the key only
// contains calls whose identity is actually known (mirroring the toolID
// filter in Put and the streaming accumulation sites). It returns "" when
// raw is empty, does not parse as a JSON array, or yields no usable
// elements.
func CanonicalizeToolCallsJSON(raw string) string {
	if raw == "" {
		return ""
	}
	var calls []map[string]any
	if err := json.Unmarshal([]byte(raw), &calls); err != nil {
		return ""
	}
	triples := make([][3]string, 0, len(calls))
	for _, tc := range calls {
		id, _ := tc["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		name, _ := tc["name"].(string)
		args := canonicalArgsString(tc["arguments"])
		if fn, ok := tc["function"].(map[string]any); ok {
			if name == "" {
				name, _ = fn["name"].(string)
			}
			if args == "" {
				args = canonicalArgsString(fn["arguments"])
			}
		}
		if args == "" {
			args = canonicalArgsString(tc["input"])
		}
		triples = append(triples, [3]string{id, name, args})
	}
	return CanonicalToolKey(triples)
}

// canonicalArgsString renders an arguments field for the canonical key.
// JSON strings are normalized to their compact canonical encoding (sorted
// keys, no whitespace) so the key is stable across wire surfaces: the
// OpenAI surface echoes arguments verbatim while the Anthropic surface
// round-trips them through a parsed input object (marshalJSONArgs). Any
// other JSON value (Anthropic tool_use input objects) marshals compactly;
// values that are not valid JSON stay verbatim.
func canonicalArgsString(v any) string {
	switch arg := v.(type) {
	case string:
		return canonicalizeArgsJSON(arg)
	case nil:
		return ""
	default:
		if b, err := json.Marshal(arg); err == nil {
			return string(b)
		}
		return ""
	}
}

// canonicalizeArgsJSON re-encodes a JSON arguments string compactly so
// both binding sides converge on the same encoding of the same arguments;
// non-JSON strings pass through unchanged.
func canonicalizeArgsJSON(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(b)
}

// CanonicalToolKey encodes (id, name, arguments) triples — the structured
// equivalent of CanonicalizeToolCallsJSON for call sites that accumulate
// tool calls field-by-field while streaming. Arguments strings are
// normalized exactly as in CanonicalizeToolCallsJSON, triples without a
// usable id are skipped, and it returns "" when nothing usable remains.
func CanonicalToolKey(calls [][3]string) string {
	parts := make([]string, 0, len(calls))
	for _, call := range calls {
		id := strings.TrimSpace(call[0])
		if id == "" {
			continue
		}
		parts = append(parts, id+canonicalFldSep+call[1]+canonicalFldSep+canonicalizeArgsJSON(call[2]))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, canonicalCallSep)
}

// bindKeyFor computes the canonical binding recorded on cacheNode and
// verified on toolID hits: sha256(content + "\x00" + canonicalToolKey).
// A binding exists whenever the entry carries content or tool-call
// identity; an entry with neither records no binding ("").
func bindKeyFor(content, canonical string) string {
	if content == "" && canonical == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(content + "\x00" + canonical))
	return hex.EncodeToString(sum[:])
}

// Put stores reasoning content and signature in the cache.
// If reasoning == "" && signature == "", Put does nothing.
// Stored entries are indexed by each non-empty toolCallID and by sha256(content + toolCallsJSON).
// A canonical binding — sha256(content + "\x00" + the canonical identity of
// toolCallsJSON) — is also recorded and verified on toolID hits, so a
// tool_call_id reused across conversations cannot restore another
// conversation's reasoning.
func (c *Cache) Put(toolCallIDs []string, content string, toolCallsJSON string, reasoning string, signature string, model string) {
	c.put(toolCallIDs, content, toolCallsJSON, CanonicalizeToolCallsJSON(toolCallsJSON), reasoning, signature, model)
}

// PutCanonical is Put for streaming sites that accumulate tool calls as
// structured fields instead of raw JSON: canonicalToolKey carries the
// precomputed identity (CanonicalToolKey), and no toolCallsJSON-derived
// byHash entry exists beyond the content-only one (hashKey(content, "")).
func (c *Cache) PutCanonical(toolCallIDs []string, content string, canonicalToolKey string, reasoning string, signature string, model string) {
	c.put(toolCallIDs, content, "", canonicalToolKey, reasoning, signature, model)
}

func (c *Cache) put(toolCallIDs []string, content string, toolCallsJSON string, canonical string, reasoning string, signature string, model string) {
	if reasoning == "" && signature == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	// 1. Evict expired entries from the back
	if c.ttl > 0 {
		for elem := c.lru.Back(); elem != nil; {
			prev := elem.Prev()
			node := elem.Value.(*cacheNode)
			if now.Sub(node.entry.CreatedAt) > c.ttl {
				c.evictNode(node)
				elem = prev
			} else {
				break
			}
		}
	}

	// 2. Evict oldest if capacity is reached
	for c.lru.Len() >= c.maxEntries && c.lru.Len() > 0 {
		back := c.lru.Back()
		if back == nil {
			break
		}
		c.evictNode(back.Value.(*cacheNode))
	}

	// 3. Filter non-empty unique toolCallIDs
	var validIDs []string
	seen := make(map[string]bool, len(toolCallIDs))
	for _, id := range toolCallIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			validIDs = append(validIDs, id)
		}
	}

	// 4. Compute hash key
	hKey := hashKey(content, toolCallsJSON)
	// Canonical binding for toolID-hit verification (see cacheNode.bindKey).
	bindKey := bindKeyFor(content, canonical)

	// 5. Create entry and node
	entry := &Entry{
		ReasoningContent: reasoning,
		Signature:        signature,
		Model:            model,
		CreatedAt:        now,
	}

	node := &cacheNode{
		entry:       entry,
		toolCallIDs: validIDs,
		hashKey:     hKey,
		bindKey:     bindKey,
	}

	node.element = c.lru.PushFront(node)
	c.nodes[entry] = node

	for _, id := range validIDs {
		c.byToolID[id] = entry
	}
	if hKey != "" {
		c.byHash[hKey] = entry
	}
}

func (c *Cache) evictNode(node *cacheNode) {
	if node == nil {
		return
	}
	if node.element != nil {
		c.lru.Remove(node.element)
		node.element = nil
	}
	for _, id := range node.toolCallIDs {
		if c.byToolID[id] == node.entry {
			delete(c.byToolID, id)
		}
	}
	if node.hashKey != "" {
		if c.byHash[node.hashKey] == node.entry {
			delete(c.byHash, node.hashKey)
		}
	}
	delete(c.nodes, node.entry)
}

// Get looks up reasoning content and signature by toolID first (if non-empty), and falls back to hash(content, toolCallsJSON).
//
// Every entry carries a canonical binding recorded at Put time: sha256 of
// the caller's content plus the canonical identity of its tool_calls. A
// toolID hit is only returned when it is consistent with the caller's
// context: callers that supply content or a tool_calls array must present
// the same identity, otherwise the hit is discarded and the lookup falls
// through to the hash index. This keeps per-conversation sequential
// tool_call_ids ("call_1", ...) from restoring another conversation's
// reasoning — including tool-only turns whose content is empty or null
// (the Claude Code / aider pattern), which are bound by tool-call identity
// alone. Entries stored with neither content nor tool-call identity cannot
// be verified against anything and keep the plain toolID-only behavior.
func (c *Cache) Get(toolID string, content, toolCallsJSON string) (reasoning, signature string, ok bool) {
	if c == nil {
		return "", "", false
	}
	if strings.TrimSpace(toolID) != "" {
		if r, s, found := c.getByToolIDBound(toolID, content, toolCallsJSON); found {
			return r, s, true
		}
	}
	if content != "" || toolCallsJSON != "" {
		return c.GetByHash(content, toolCallsJSON)
	}
	return "", "", false
}

// getByToolIDBound is Get's toolID step: it resolves the entry for toolID
// and enforces the canonical binding recorded at Put time when the caller
// presents one (content and/or a canonicalizable tool_calls array). A
// binding mismatch is reported as a miss so the caller falls through to
// the hash index instead of restoring foreign reasoning. Entries stored
// with no binding at all (neither content nor tool-call identity) have
// nothing to compare against and keep the legacy toolID-only behavior.
func (c *Cache) getByToolIDBound(toolID, content, raw string) (reasoning, signature string, ok bool) {
	entry, node, ok := c.lookupByToolID(toolID)
	if !ok {
		return "", "", false
	}
	want := bindKeyFor(content, CanonicalizeToolCallsJSON(raw))
	if want != "" && (node == nil || (node.bindKey != "" && node.bindKey != want)) {
		return "", "", false
	}
	return entry.ReasoningContent, entry.Signature, true
}

// lookupByToolID returns the live entry for toolID together with its node,
// evicting the entry first if it has expired. The entry and node are immutable
// once published, so they are safe to read after the lock is released.
func (c *Cache) lookupByToolID(toolID string) (*Entry, *cacheNode, bool) {
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		return nil, nil, false
	}

	c.mu.RLock()
	entry, found := c.byToolID[toolID]
	if !found {
		c.mu.RUnlock()
		return nil, nil, false
	}

	if c.ttl > 0 && time.Since(entry.CreatedAt) > c.ttl {
		c.mu.RUnlock()
		c.mu.Lock()
		if e, exists := c.byToolID[toolID]; exists && c.ttl > 0 && time.Since(e.CreatedAt) > c.ttl {
			if node, hasNode := c.nodes[e]; hasNode {
				c.evictNode(node)
			} else {
				delete(c.byToolID, toolID)
			}
		}
		c.mu.Unlock()
		return nil, nil, false
	}

	node := c.nodes[entry]
	c.mu.RUnlock()
	return entry, node, true
}

// GetByToolID looks up reasoning content and signature by tool_call_id.
// If the entry is expired, it is removed and ok is false.
func (c *Cache) GetByToolID(toolID string) (reasoning, signature string, ok bool) {
	entry, _, ok := c.lookupByToolID(toolID)
	if !ok {
		return "", "", false
	}
	return entry.ReasoningContent, entry.Signature, true
}

// GetByHash looks up reasoning content and signature by content and toolCallsJSON.
// If the entry is expired, it is removed and ok is false.
func (c *Cache) GetByHash(content, toolCallsJSON string) (reasoning, signature string, ok bool) {
	hKey := hashKey(content, toolCallsJSON)
	if hKey == "" {
		return "", "", false
	}

	c.mu.RLock()
	entry, found := c.byHash[hKey]
	if !found {
		c.mu.RUnlock()
		return "", "", false
	}

	if c.ttl > 0 && time.Since(entry.CreatedAt) > c.ttl {
		c.mu.RUnlock()
		c.mu.Lock()
		if e, exists := c.byHash[hKey]; exists && c.ttl > 0 && time.Since(e.CreatedAt) > c.ttl {
			if node, hasNode := c.nodes[e]; hasNode {
				c.evictNode(node)
			} else {
				delete(c.byHash, hKey)
			}
		}
		c.mu.Unlock()
		return "", "", false
	}

	r := entry.ReasoningContent
	s := entry.Signature
	c.mu.RUnlock()
	return r, s, true
}

// GetEntryByToolID returns a copy of the Entry for the given tool_call_id.
func (c *Cache) GetEntryByToolID(toolID string) (*Entry, bool) {
	entry, _, ok := c.lookupByToolID(toolID)
	if !ok {
		return nil, false
	}
	res := *entry
	return &res, true
}

// GetEntryByHash returns a copy of the Entry for the given content and toolCallsJSON.
func (c *Cache) GetEntryByHash(content, toolCallsJSON string) (*Entry, bool) {
	hKey := hashKey(content, toolCallsJSON)
	if hKey == "" {
		return nil, false
	}

	c.mu.RLock()
	entry, found := c.byHash[hKey]
	if !found {
		c.mu.RUnlock()
		return nil, false
	}

	if c.ttl > 0 && time.Since(entry.CreatedAt) > c.ttl {
		c.mu.RUnlock()
		c.mu.Lock()
		if e, exists := c.byHash[hKey]; exists && c.ttl > 0 && time.Since(e.CreatedAt) > c.ttl {
			if node, hasNode := c.nodes[e]; hasNode {
				c.evictNode(node)
			} else {
				delete(c.byHash, hKey)
			}
		}
		c.mu.Unlock()
		return nil, false
	}

	res := *entry
	c.mu.RUnlock()
	return &res, true
}

// Len returns the current number of cached entries.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lru.Len()
}

// Prune sweeps and removes all expired entries.
func (c *Cache) Prune() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ttl <= 0 {
		return
	}

	now := time.Now()
	for elem := c.lru.Back(); elem != nil; {
		prev := elem.Prev()
		node := elem.Value.(*cacheNode)
		if now.Sub(node.entry.CreatedAt) > c.ttl {
			c.evictNode(node)
			elem = prev
		} else {
			break
		}
	}
}
