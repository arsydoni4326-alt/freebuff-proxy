// Package updatecheck provides the dashboard's release-update indicator
// (issue #50b): a cached lookup of the latest GitHub release tag for the
// repo, plus a semver-ish comparison against the running version. The
// lookup is deliberately non-blocking for the dashboard — the first render
// after a 6h cache expiry (or after a failed attempt's backoff window)
// performs one bounded HTTP GET (3s timeout); a failure degrades to
// "no update" and backs off for the same 6h window instead of retrying on
// every render.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultRepo is the upstream repo whose releases the indicator checks.
// ARSYDONI UPDATE SOURCE (merge-guarded): this fork's repo — the dashboard
// update modal depends on it; see frontend/src/lib/README_ARSYDONI_UPDATE.md.
const DefaultRepo = "arsydoni4326-alt/freebuff-proxy"

// CacheTTL is how long a fetched latest-release tag is reused (issue #50:
// "cached 6h").
const CacheTTL = 6 * time.Hour

// HeadTTL is how long a fetched main-branch head commit is reused. It is
// deliberately much shorter than CacheTTL: the commit check is the primary
// update signal (every push to main counts, no release needed), so page
// refreshes should notice new commits within minutes while still keeping
// the GitHub API at background-poll cadence.
const HeadTTL = 10 * time.Minute

// fetchTimeout bounds one GitHub API call (the dashboard must never block
// on it).
const fetchTimeout = 3 * time.Second

// commitTimeout bounds the best-effort tag→commit lookup; a slow or missing
// commit answer degrades to an empty Commit, never an error.
const commitTimeout = 2 * time.Second

// notesLimit caps the stored release-notes body (the update modal renders a
// short "what's changed" digest, not the full notes page).
const notesLimit = 8 << 10

// Info is one update-check answer: the latest release tag, the commit that
// tag points at, and the release notes (that release's changelog only).
type Info struct {
	Tag    string
	Commit string
	Notes  string
}

// Checker is a concurrency-safe, in-memory-cached latest-release lookup.
// The zero value is not usable — use New.
type Checker struct {
	repo   string
	client *http.Client
	logger *slog.Logger // decision Debug sink (nil = slog.Default())

	mu       sync.Mutex
	info     Info
	fetched  time.Time
	fetching bool

	// head is the fork's default-branch (main) head commit, cached for
	// HeadTTL. It is the primary "is the running build outdated" signal:
	// comparing the running build's commit against main needs no release.
	head        string
	headFetched time.Time
	headFetch   bool
}

// New builds a checker for repo (owner/name). client is used for the
// GitHub API call; nil uses http.DefaultClient with fetchTimeout.
func New(repo string, client *http.Client) *Checker {
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}
	return &Checker{repo: repo, client: client, logger: slog.Default()}
}

// SetLogger replaces the checker's log sink (nil restores slog.Default).
// Used by tests and by hosts that want the decision Debug on a custom logger.
func (c *Checker) SetLogger(l *slog.Logger) {
	if l == nil {
		l = slog.Default()
	}
	c.logger = l
}

// Invalidate clears the cache timestamps so the next Latest/Info/Head call
// re-fetches.
func (c *Checker) Invalidate() {
	c.mu.Lock()
	c.fetched = time.Time{}
	c.headFetched = time.Time{}
	c.mu.Unlock()
}

// Head returns the fork's default-branch (main) head commit SHA, cached for
// HeadTTL with the same fail-open/backoff shape as Info: a failed fetch
// still stamps the attempt (so page refreshes never hammer the GitHub API)
// and returns the previously cached value ("" on first failure) with the
// error. Single-flight like the release lookup.
func (c *Checker) Head(ctx context.Context) (string, error) {
	start := time.Now()
	c.mu.Lock()
	if time.Since(c.headFetched) < HeadTTL {
		sha := c.head
		c.mu.Unlock()
		return sha, nil
	}
	if c.headFetch {
		// Another caller is mid-fetch: wait for it instead of stacking a
		// second GET (mirrors the Info single-flight waiter).
		for c.headFetch {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				c.mu.Lock()
				sha := c.head
				c.mu.Unlock()
				return sha, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
			c.mu.Lock()
		}
		sha := c.head
		c.mu.Unlock()
		return sha, nil
	}
	c.headFetch = true
	c.mu.Unlock()

	sha, err := c.fetchHead(ctx)

	c.mu.Lock()
	c.headFetch = false
	if err == nil && sha != "" {
		c.head = sha
		c.headFetched = time.Now()
	} else {
		// Stamp the attempt even on failure: the HeadTTL window covers
		// failed lookups too, so frequent page refreshes back off instead
		// of hammering api.github.com (same policy as the release cache).
		c.headFetched = time.Now()
	}
	got := c.head
	c.mu.Unlock()
	decision := "fetched"
	if err != nil || sha == "" {
		decision = "failed"
	}
	c.logger.Debug("update head decision", "decision", decision, "ms", time.Since(start).Milliseconds())
	return got, err
}

// fetchHead GETs https://api.github.com/repos/<repo>/commits/<branch> and
// extracts the head commit SHA of the default branch.
func (c *Checker) fetchHead(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+c.repo+"/commits/main", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "freebucks-proxy-updatecheck/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("github commits/main: status %d", resp.StatusCode)
	}
	var decoded struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return "", err
	}
	return strings.TrimSpace(decoded.SHA), nil
}

// Latest returns the latest release tag (e.g. "v0.9.3"). It is the
// tag-only view over Info — see Info for the fetch/cache/backoff semantics.
func (c *Checker) Latest(ctx context.Context) (string, error) {
	info, err := c.Info(ctx)
	return info.Tag, err
}

// Info returns the cached update answer (latest release tag, that tag's
// commit, and the release notes), fetching it when the last attempt —
// successful or failed — is older than CacheTTL. A fetch failure returns
// the previously cached answer (or zeros) with the error and still records
// the attempt, so subsequent calls back off for CacheTTL instead of
// re-fetching. The cache is refreshed single-flight so concurrent renders
// share one GET. Each lookup emits a Debug line with the decision
// (cached|fetched|failed) and the lookup duration (T18).
func (c *Checker) Info(ctx context.Context) (Info, error) {
	start := time.Now()
	c.mu.Lock()
	if time.Since(c.fetched) < CacheTTL {
		info := c.info
		c.mu.Unlock()
		c.logger.Debug("update check decision", "decision", "cached", "ms", time.Since(start).Milliseconds())
		return info, nil
	}
	if c.fetching {
		// Another render is mid-fetch: wait for it rather than stacking a
		// second GET. Honor ctx cancellation so a canceled waiter does not
		// spin on the in-flight fetch.
		for c.fetching {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				c.mu.Lock()
				info := c.info
				c.mu.Unlock()
				c.logger.Debug("update check decision", "decision", "cached", "ms", time.Since(start).Milliseconds())
				return info, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
			c.mu.Lock()
		}
		info := c.info
		c.mu.Unlock()
		c.logger.Debug("update check decision", "decision", "cached", "ms", time.Since(start).Milliseconds())
		return info, nil
	}
	c.fetching = true
	c.mu.Unlock()

	info, err := c.fetchInfo(ctx)

	c.mu.Lock()
	c.fetching = false
	if err == nil && info.Tag != "" {
		c.info = info
		c.fetched = time.Now()
	} else {
		// Keep the previous value and stamp the attempt: the CacheTTL window
		// now also covers failed lookups (first-ever failure included), so
		// the dashboard's frequent polls back off instead of hammering
		// api.github.com on every render (review P2).
		c.fetched = time.Now()
	}
	got := c.info
	c.mu.Unlock()
	decision := "fetched"
	if err != nil || info.Tag == "" {
		decision = "failed"
	}
	c.logger.Debug("update check decision", "decision", decision, "ms", time.Since(start).Milliseconds())
	return got, err
}

// fetchInfo resolves the latest release (tag + notes) and then — best
// effort — the commit that release tag points at. A releases failure aborts
// before the commit lookup; a commit failure degrades to an empty Commit
// without failing the whole answer.
func (c *Checker) fetchInfo(ctx context.Context) (Info, error) {
	tag, notes, err := c.fetchRelease(ctx)
	if err != nil {
		return Info{}, err
	}
	return Info{Tag: tag, Notes: notes, Commit: c.fetchTagCommit(ctx, tag)}, nil
}

// fetchRelease GETs https://api.github.com/repos/<repo>/releases/latest and
// extracts tag_name plus the release body — the notes for that one release,
// which is the changelog source for the dashboard's update modal.
func (c *Checker) fetchRelease(ctx context.Context) (tag string, notes string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+c.repo+"/releases/latest", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "freebuff-proxy-updatecheck/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", "", fmt.Errorf("github releases/latest: status %d", resp.StatusCode)
	}
	var decoded struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(decoded.TagName), truncateNotes(decoded.Body), nil
}

// fetchTagCommit resolves the release tag to its commit SHA (the short hash
// shown next to the version in the update modal). Best-effort: any failure,
// including the commitTimeout deadline, returns "".
func (c *Checker) fetchTagCommit(ctx context.Context, tag string) string {
	if tag == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, commitTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+c.repo+"/commits/"+url.PathEscape(tag), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "freebuff-proxy-updatecheck/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return ""
	}
	var decoded struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return ""
	}
	return strings.TrimSpace(decoded.SHA)
}

// truncateNotes caps the release notes at notesLimit so a pathological
// release body cannot bloat the cache or the version API answer.
func truncateNotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > notesLimit {
		s = s[:notesLimit]
	}
	return s
}

// UpdateAvailable reports whether latest is newer than current ("" current
// means dev builds — never "update available" for a dev build, since its
// version cannot be compared). Both tags are compared with CompareVersions.
func UpdateAvailable(current, latest string) bool {
	if current == "" || current == "dev" || latest == "" {
		return false
	}
	return CompareVersions(latest, current) > 0
}

// CommitOutdated is the ARSYDONI UPDATE SOURCE primary signal (merge-guarded
// — see frontend/src/lib/README_ARSYDONI_UPDATE.md): the running build is
// outdated when its embedded commit hash differs from the repo's main-branch
// head — no release required, every push to main counts. Unknown inputs are
// never outdated: runningCommit "" (dev builds without a stamped commit) or
// an empty head (GitHub unreachable) degrade to no-update.
func CommitOutdated(runningCommit, headCommit string) bool {
	runningCommit = strings.TrimSpace(runningCommit)
	headCommit = strings.TrimSpace(headCommit)
	if runningCommit == "" || headCommit == "" {
		return false
	}
	// A running build may embed the short hash while the API answers with
	// the full SHA (and vice versa): outdated only when the short forms
	// clearly differ.
	n := len(runningCommit)
	if len(headCommit) < n {
		n = len(headCommit)
	}
	return runningCommit[:n] != headCommit[:n]
}

// CompareVersions compares two version strings ("v0.9.3", "0.10.1",
// "1.2.3-beta.1") numerically on their numeric dot-components; a missing
// component counts as 0 (v0.9 == v0.9.0). Non-numeric suffixes after the
// third component are ignored for ordering (a beta is treated equal to its
// release — the dashboard only needs to detect NEWER releases, and GitHub
// tags the release with a plain vX.Y.Z). Returns -1/0/1; malformed input
// compares as 0 against anything (the indicator degrades to no-update).
func CompareVersions(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return 0
	}
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// parseVersion splits "v1.2.3-rc.1" into [1,2,3] (numeric dot-components
// only; the suffix and any non-numeric trailing parts are dropped).
func parseVersion(v string) ([]int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	// Cut any pre-release/build suffix (e.g. "-rc.1", "+build").
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
