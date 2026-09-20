package updatecheck

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.9.3", "v0.9.3", 0},
		{"v0.9.3", "v0.10.0", -1},
		{"v0.10.0", "v0.9.3", 1},
		{"1.2.3", "v1.2.3", 0},
		{"v1.2", "v1.2.0", 0},        // missing component counts as 0
		{"v1.10.0", "v1.9.9", 1},     // numeric, not lexicographic
		{"v0.9.3", "dev", 0},         // unparseable → 0
		{"", "v0.9.3", 0},            // unparseable → 0
		{"v0.9.3-rc.1", "v0.9.3", 0}, // suffix ignored
		{"v2.0.0", "v1.99.99", 1},
		{"v0.0.9", "v0.0.10", -1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestUpdateAvailable(t *testing.T) {
	if UpdateAvailable("v0.9.3", "v0.9.3") {
		t.Error("same version → update available")
	}
	if !UpdateAvailable("v0.9.3", "v0.9.4") {
		t.Error("newer release → update available")
	}
	if UpdateAvailable("v0.9.4", "v0.9.3") {
		t.Error("older release → update available")
	}
	if UpdateAvailable("dev", "v0.9.4") {
		t.Error("dev build → update available (cannot compare)")
	}
	if UpdateAvailable("", "v0.9.4") {
		t.Error("empty current → update available")
	}
	if UpdateAvailable("v0.9.3", "") {
		t.Error("empty latest → update available")
	}
}

func TestLatestFetchesAndCaches(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch {
		case strings.Contains(r.URL.Path, "/releases/latest"):
			_, _ = w.Write([]byte(`{"tag_name":"v0.9.4","body":"- fix A"}`))
		case strings.Contains(r.URL.Path, "/commits/"):
			_, _ = w.Write([]byte(`{"sha":"0123456789abcdef0123456789abcdef01234567"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	// The checker builds the github.com URL itself; to observe the fetch we
	// need a transport-level redirect hook: wrap the client so the request
	// goes to the test server instead.
	tr := &rewriteTransport{target: srv.URL}
	c := New(DefaultRepo, &http.Client{Transport: tr, Timeout: fetchTimeout})

	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Tag != "v0.9.4" {
		t.Errorf("tag = %q, want v0.9.4", info.Tag)
	}
	if info.Commit != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("commit = %q, want the mocked sha", info.Commit)
	}
	if info.Notes != "- fix A" {
		t.Errorf("notes = %q, want the release body", info.Notes)
	}
	if hits != 2 {
		t.Errorf("fetches = %d, want 2 (releases/latest + commits/<tag>)", hits)
	}

	// Cached: a second call within CacheTTL must not hit the network.
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("fetches = %d after cache reuse, want still 2", hits)
	}

	// Latest stays the tag-only view over the same cache.
	tag, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.9.4" {
		t.Errorf("Latest = %q, want v0.9.4", tag)
	}
	if hits != 2 {
		t.Errorf("fetches = %d after Latest cache reuse, want still 2", hits)
	}
}

// TestInfoCommitLookupIsBestEffort: a failing commit endpoint must not fail
// the update answer — the tag and notes survive with an empty Commit.
func TestInfoCommitLookupIsBestEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/commits/") {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3","body":"notes"}`))
	}))
	defer srv.Close()
	c := New(DefaultRepo, &http.Client{Transport: &rewriteTransport{target: srv.URL}, Timeout: fetchTimeout})

	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatalf("Info = %v; want success despite commit failure", err)
	}
	if info.Tag != "v1.2.3" || info.Notes != "notes" {
		t.Errorf("info = %+v, want tag v1.2.3 + notes", info)
	}
	if info.Commit != "" {
		t.Errorf("commit = %q, want empty on commit-lookup failure", info.Commit)
	}
}

// TestInfoReleaseFailureSkipsCommitLookup: a releases/latest failure aborts
// before the commit endpoint is contacted (cheap failure path).
func TestInfoReleaseFailureSkipsCommitLookup(t *testing.T) {
	var commitHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/commits/") {
			commitHits++
			_, _ = w.Write([]byte(`{"sha":"x"}`))
			return
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := New(DefaultRepo, &http.Client{Transport: &rewriteTransport{target: srv.URL}, Timeout: fetchTimeout})

	if info, err := c.Info(context.Background()); err == nil || info.Tag != "" {
		t.Fatalf("Info = %+v, %v; want empty + error on releases failure", info, err)
	}
	if commitHits != 0 {
		t.Errorf("commit endpoint hits = %d, want 0 (releases failure must short-circuit)", commitHits)
	}
}

func TestTruncateNotes(t *testing.T) {
	if got := truncateNotes("  hi  "); got != "hi" {
		t.Errorf("truncateNotes trims: got %q", got)
	}
	big := strings.Repeat("x", notesLimit+100)
	if got := truncateNotes(big); len(got) != notesLimit {
		t.Errorf("truncateNotes cap: len = %d, want %d", len(got), notesLimit)
	}
}

func TestLatestFetchFailureReturnsprev(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	tr := &rewriteTransport{target: srv.URL}
	c := New(DefaultRepo, &http.Client{Transport: tr, Timeout: fetchTimeout})

	if latest, err := c.Latest(context.Background()); latest != "" || err == nil {
		t.Fatalf("Latest = %q, %v; want empty + error on first failure", latest, err)
		return
	}
}

// TestLatestFirstFetchFailureBacksOffForTTL verifies that a failed first
// fetch still starts the CacheTTL backoff window: the attempt is stamped
// fetched (see the comment in Latest), so a second call well
// inside CacheTTL must reuse the recorded failure window instead of
// hitting the network again.
func TestLatestFirstFetchFailureBacksOffForTTL(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(500)
	}))
	defer srv.Close()
	tr := &rewriteTransport{target: srv.URL}
	c := New(DefaultRepo, &http.Client{Transport: tr, Timeout: fetchTimeout})

	if latest, err := c.Latest(context.Background()); latest != "" || err == nil {
		t.Fatalf("first Latest = %q, %v; want empty + error", latest, err)
		return
	}
	if hits != 1 {
		t.Fatalf("network hits after first call = %d, want 1", hits)
	}

	// Second call, immediately (well inside CacheTTL): must NOT re-fetch,
	// and the backoff hit surfaces as an empty tag with no error.
	latest2, err2 := c.Latest(context.Background())
	if latest2 != "" || err2 != nil {
		t.Errorf("second Latest = %q, %v; want empty tag + nil error (backoff hit)", latest2, err2)
	}
	if hits != 1 {
		t.Errorf("network hits after second call = %d, want 1 (first-ever failure must back off for CacheTTL)", hits)
	}
}

// rewriteTransport sends every request to target (a test seam: the checker
// hardcodes the github.com URL, which tests must not contact).
type rewriteTransport struct {
	target string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(t.target, "http://")
	return http.DefaultTransport.RoundTrip(clone)
}

var _ = time.Second // keep the time import for future cache-age assertions

// TestLatestLogsDecision verifies T18: each Latest() lookup logs a Debug
// line with the decision (fetched|cached|failed) and the lookup duration.
func TestLatestLogsDecision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	}))
	defer srv.Close()

	var sink bytes.Buffer
	c := New(DefaultRepo, &http.Client{Transport: &rewriteTransport{target: srv.URL}, Timeout: fetchTimeout})
	c.SetLogger(slog.New(slog.NewTextHandler(&sink, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// First lookup fetches → decision=fetched with ms.
	if _, err := c.Latest(context.Background()); err != nil {
		t.Fatal(err)
	}
	logs := sink.String()
	for _, want := range []string{"update check decision", "decision=fetched", "ms="} {
		if !strings.Contains(logs, want) {
			t.Errorf("fetched lookup log missing %q: %s", want, logs)
		}
	}

	// Second lookup within CacheTTL → decision=cached, no new fetch.
	if _, err := c.Latest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(sink.String(), "decision=cached"); got != 1 {
		t.Errorf("cached decision lines = %d, want 1", got)
	}

	// A failing source → decision=failed.
	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvFail.Close()
	var sinkFail bytes.Buffer
	c2 := New(DefaultRepo, &http.Client{Transport: &rewriteTransport{target: srvFail.URL}, Timeout: fetchTimeout})
	c2.SetLogger(slog.New(slog.NewTextHandler(&sinkFail, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if _, err := c2.Latest(context.Background()); err == nil {
		t.Fatal("Latest against a 500 source succeeded, want error")
		return
	}
	if !strings.Contains(sinkFail.String(), "decision=failed") {
		t.Errorf("failed lookup log missing decision=failed: %s", sinkFail.String())
	}
}
