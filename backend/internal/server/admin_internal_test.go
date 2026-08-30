package server

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testIP(n int) string {
	// Distinct, fresh per-IP hosts for the global-budget tests.
	return "10." + string(rune('0'+n/1000%10)) + "." + string(rune('0'+n/100%10)) + "." + string(rune('0'+n%100/10)) + string(rune('0'+n%10))
}

// TestAdminAuthGlobalBudget pins the process-wide failed-login budget
// : failures from DISTINCT source IPs never trip the per-IP lockout,
// but crossing loginGlobalFailMax inside one window locks every source, and
// each subsequent breach escalates the lockout by doubling up to the cap.
func TestAdminAuthGlobalBudget(t *testing.T) {
	a := newAdminAuth()
	for i := range loginGlobalFailMax {
		a.recordFail(testIP(i))
	}
	if a.allow(testIP(9999)) {
		t.Fatal("allow() = true after the process-wide budget was crossed, want global lockout")
	}
	if a.globalLevel != 1 {
		t.Errorf("globalLevel = %d, want 1 after first breach", a.globalLevel)
	}
	if until := time.Until(a.globalUntil); until < loginGlobalLockout-time.Second || until > loginGlobalLockout+time.Second {
		t.Errorf("first global lockout = %v, want ~%v", until, loginGlobalLockout)
	}

	// While the global lockout is active the budget is not re-armed.
	for i := range loginGlobalFailMax {
		a.recordFail(testIP(10000 + i))
	}
	if a.globalLevel != 1 {
		t.Errorf("globalLevel = %d during active lockout, want still 1", a.globalLevel)
	}

	// After expiry, a fresh breach doubles the lockout.
	a.globalUntil = time.Now().Add(-time.Second)
	a.globalWindow = time.Now().Add(-2 * loginGlobalWindow)
	a.globalFails = 0
	for i := range loginGlobalFailMax {
		a.recordFail(testIP(20000 + i))
	}
	if a.globalLevel != 2 {
		t.Errorf("globalLevel = %d after second breach, want 2", a.globalLevel)
	}
	if until := time.Until(a.globalUntil); until < 2*loginGlobalLockout-time.Second || until > 2*loginGlobalLockout+time.Second {
		t.Errorf("second global lockout = %v, want ~%v (doubled)", until, 2*loginGlobalLockout)
	}

	// The lockout duration is capped at loginGlobalLockoutMax.
	a.globalUntil = time.Now().Add(-time.Second)
	a.globalWindow = time.Now().Add(-2 * loginGlobalWindow)
	a.globalFails = 0
	for i := range 10 {
		a.globalUntil = time.Now().Add(-time.Second)
		a.globalWindow = time.Now().Add(-2 * loginGlobalWindow)
		a.globalFails = 0
		for range loginGlobalFailMax {
			a.recordFail(testIP(30000 + i))
		}
	}
	if until := a.globalUntil.Sub(time.Now()); until > loginGlobalLockoutMax {
		t.Errorf("global lockout = %v exceeds the cap %v", until, loginGlobalLockoutMax)
	}
}

// TestAdminAuthLoginSlotBound pins the concurrent-login semaphore:
// the bounded slots reject overflow and hand the slot back on release.
func TestAdminAuthLoginSlotBound(t *testing.T) {
	a := newAdminAuth()
	for range loginConcurrencyMax {
		if !a.tryLogin() {
			t.Fatal("tryLogin = false below the concurrency bound")
		}
	}
	if a.tryLogin() {
		t.Fatal("tryLogin = true above the concurrency bound")
	}
	a.releaseLogin()
	if !a.tryLogin() {
		t.Fatal("tryLogin = false after one release")
	}
	// Drain every slot, releasing exactly once per acquire.
	for range loginConcurrencyMax {
		a.releaseLogin()
	}
	if len(a.loginSlots) != 0 {
		t.Errorf("%d slots held after balanced acquire/release, want 0", len(a.loginSlots))
	}
}

// TestWriteFileAtomicRestoresBackupOnRenameFailure pins the backup-first safety
// pattern: when rename-over-existing keeps failing (Windows transient
// antivirus lock), writeFileAtomic moves the target to .bak first, retries,
// and restores the original on every failure — the target is never removed
// without a recoverable copy.
func TestWriteFileAtomicRestoresBackupOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	realRename := osRename
	defer func() { osRename = realRename }()
	// Fail every rename whose SOURCE is the temp file writeFileAtomic
	// mints (".env.tmp*"); the .bak aside/restore renames still run real.
	osRename = func(old, new string) error {
		if strings.Contains(filepath.Base(old), ".env.tmp") {
			return errors.New("injected rename failure")
		}
		return realRename(old, new)
	}

	if err := writeFileAtomic(path, []byte("NEW\n")); err == nil {
		t.Fatal("writeFileAtomic succeeded under injected rename failures, want error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("target missing after failed write (data-loss window): %v", err)
	}
	if string(got) != "OLD\n" {
		t.Errorf("target content after failed write = %q, want %q", got, "OLD\n")
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error(".bak left behind after the original was restored")
	}
	assertNoTmpFiles(t, dir, ".env")
}

// TestWriteFileAtomicPreservesBackupWhenRestoreFails pins the
// no-data-loss invariant: if BOTH the temp rename and the .bak restore fail,
// the old content must still exist in .bak and the error must say so.
func TestWriteFileAtomicPreservesBackupWhenRestoreFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	realRename := osRename
	defer func() { osRename = realRename }()
	osRename = func(old, new string) error {
		if strings.Contains(filepath.Base(old), ".env.tmp") || new == path {
			return errors.New("injected rename failure")
		}
		return realRename(old, new)
	}

	err := writeFileAtomic(path, []byte("NEW\n"))
	if err == nil {
		t.Fatal("writeFileAtomic succeeded under injected rename failures, want error")
	}
	if !strings.Contains(err.Error(), "restore") {
		t.Errorf("error = %v, want it to mention the .bak restore failure", err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf(".bak missing after total rename failure: %v", err)
	}
	if string(bak) != "OLD\n" {
		t.Errorf(".bak content = %q, want %q (data must survive in .bak)", bak, "OLD\n")
	}
	assertNoTmpFiles(t, dir, ".env")
}

// TestUpdateEnvKeysRejectsNewline pins the .env writer guard: updateEnvKeys writes raw
// Key=Value lines, so a value carrying a CR/LF would inject a second .env
// line or shred CRLF endings; it must be rejected before any write.
func TestUpdateEnvKeysRejectsNewline(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(".env", []byte("SAFE_MODE=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"a\nb", "a\rb", "a\r\nb"} {
		if _, err := updateEnvKeys([]envUpdate{{Key: "AUTH_TOKENS", Value: bad}}); err == nil {
			t.Errorf("updateEnvKeys(%q) = nil error, want rejection", bad)
		}
	}
	got, err := os.ReadFile(".env")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "SAFE_MODE=true\n" {
		t.Errorf(".env mutated by rejected update: %q", got)
	}
}

// TestUpdateAuthTokensEnvRejectsComma pins the comma-joined list guard: AUTH_TOKENS is a
// comma-joined list, so an interior comma in one token would split it on
// the next reload; the whole update must be rejected.
func TestUpdateAuthTokensEnvRejectsComma(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := updateAuthTokensEnv([]string{"cb-ok", "cb,bad"}); err == nil {
		t.Fatal("updateAuthTokensEnv with comma-bearing token = nil error, want rejection")
	}
	if _, err := updateAuthTokensEnv([]string{"cb-ok", "cb\nbad"}); err == nil {
		t.Fatal("updateAuthTokensEnv with newline-bearing token = nil error, want rejection")
	}
	if _, err := updateAuthTokensEnv([]string{"cb-ok", "cb-two"}); err != nil {
		t.Fatalf("updateAuthTokensEnv clean list = %v, want nil", err)
	}
}

// TestTrustedProxyAddr pins the proxy allowlist: loopback, RFC1918, and
// link-local peers may vouch for X-Forwarded-Proto; everything else cannot.
func TestTrustedProxyAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:1234", true},
		{"127.0.0.1", true},
		{"[::1]:1234", true},
		{"10.1.2.3:80", true},
		{"172.16.5.5:443", true},
		{"192.168.1.1:8080", true},
		{"169.254.1.1:80", true},
		{"[fd00::1]:80", true},
		{"203.0.113.9:1234", false},
		{"8.8.8.8", false},
		{"1.2.3.4:0", false},
		{"not-an-ip", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isTrustedProxyAddr(c.addr); got != c.want {
			t.Errorf("isTrustedProxyAddr(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// TestSecureCookieTrustsOwnProxyOnly pins the trusted-proxy rule end to end:
// X-Forwarded-Proto lifts Secure only when the peer is loopback/private,
// and a direct TLS connection always does.
func TestSecureCookieTrustsOwnProxyOnly(t *testing.T) {
	req := func(addr string, xfp string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
		r.RemoteAddr = addr
		if xfp != "" {
			r.Header.Set("X-Forwarded-Proto", xfp)
		}
		return r
	}
	if !secureCookie(req("127.0.0.1:1234", "https")) {
		t.Error("secureCookie(loopback + X-Forwarded-Proto https) = false, want true")
	}
	if !secureCookie(req("10.0.0.5:1234", "https")) {
		t.Error("secureCookie(private peer + X-Forwarded-Proto https) = false, want true")
	}
	if secureCookie(req("203.0.113.9:1234", "https")) {
		t.Error("secureCookie(public peer + X-Forwarded-Proto https) = true, want false (spoofable header)")
	}
	r := req("203.0.113.9:1234", "http")
	r.TLS = &tls.ConnectionState{}
	if !secureCookie(r) {
		t.Error("secureCookie(direct TLS) = false, want true")
	}
	if secureCookie(req("127.0.0.1:1234", "")) {
		t.Error("secureCookie(loopback without X-Forwarded-Proto) = true, want false")
	}
}

// TestNewAdminAuthKeyRandom verifies the boot-time key generation never
// leaves a zero key: the constructor panics on RNG failure, so a
// live adminAuth must always carry a non-zero key.
func TestNewAdminAuthKeyRandom(t *testing.T) {
	a := newAdminAuth()
	var zero [32]byte
	if a.key == zero {
		t.Fatal("newAdminAuth key is all zeroes")
	}
}
