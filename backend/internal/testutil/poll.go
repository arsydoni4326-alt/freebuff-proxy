package testutil

// Shared test-side synchronization helpers. WaitFor consolidates the
// inline poll-until-deadline loops that used to be copied across test
// packages; DrainStrayTempFiles defuses the Windows TempDir cleanup flake
// where a virus scanner holds a brief handle on freshly written files.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pollInterval is how often WaitFor re-evaluates its condition: short
// enough that second-scale timeouts stay tight, long enough that polling
// never busy-spinns.
const pollInterval = 10 * time.Millisecond

// WaitFor polls cond until it returns true or timeout elapses, then fails
// the test. The failure message is stable: the timeout always appears,
// followed by the caller's message (fmt.Sprint semantics — pre-format with
// fmt.Sprintf at the call site when values are needed; note values captured
// at the WaitFor call equal the value at timeout for monotonic conditions).
func WaitFor(t *testing.T, timeout time.Duration, cond func() bool, msgAndArgs ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if !time.Now().Before(deadline) {
			msg := "condition never became true"
			if len(msgAndArgs) > 0 {
				msg = fmt.Sprint(msgAndArgs...)
			}
			t.Fatalf("condition not met within %s: %s", timeout, msg)
		}
		time.Sleep(pollInterval)
	}
}

// DrainStrayTempFiles deletes stray *.tmp* / *.bak files that writeFileAtomic
// and similar temp+rename writers can leave behind when Windows (Defender)
// briefly holds a scan handle on a freshly written file and a single
// os.Remove fails. Register it via t.Cleanup by calling it AFTER
// t.TempDir()/t.Chdir: cleanups run LIFO, so this drain executes before
// TempDir's own RemoveAll and hands it a directory with no locked stragglers.
// Best-effort: residuals are tolerated (retried ~50x10ms, then given up on)
// and never fail the test.
func DrainStrayTempFiles(t *testing.T, dir string) {
	t.Helper()
	patterns := []string{
		filepath.Join(dir, "*.tmp*"),
		filepath.Join(dir, "*.bak"),
	}
	const attempts = 50
	for i := 0; i < attempts; i++ {
		stray := 0
		for _, pat := range patterns {
			matches, err := filepath.Glob(pat)
			if err != nil {
				continue
			}
			for _, m := range matches {
				if os.Remove(m) != nil {
					stray++
				}
			}
		}
		if stray == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
