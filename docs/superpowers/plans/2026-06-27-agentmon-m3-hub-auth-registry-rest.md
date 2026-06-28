# AgentMon M3 — Hub auth + registry + REST Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the `agentmon-hubd` auth bundle (argon2id login, session cookie, CSRF, rate-limit, `authorize()` chokepoint), the config server registry, and the `/api/v1` REST surface (`/servers`, `/servers/{id}`, `/servers/{id}/sessions`, `/servers/{id}/sessions/{name}`, `/audit`) that dials agents over their per-server bearer — all unit + httptest-tested now, with the live two-real-servers acceptance deferred to Patrik.

**Architecture:** A single `agentmon-hubd` process resolves every request to a principal at the edge (session cookie → `RequireAuth` middleware → `authz.Authorize()` per handler), reads its server list from `config.yaml` at boot, and proxies session-tree reads to LAN agents using each agent's bearer token. TLS/origin is terminated by Caddy; the hub derives cookie `Secure` from `X-Forwarded-Proto` and origin-checks against `external_origin`. Sessions are held in an in-memory store (single-process, single-user; re-login after restart is acceptable for Phase 1).

**Tech Stack:** Go 1.25 (latest), `modernc.org/sqlite` (`CGO_ENABLED=0`), `golang.org/x/crypto/argon2`, `github.com/google/uuid`, `net/http` (stdlib routing), `httptest` for integration.

## Global Constraints

- **Go module layout:** three modules in a `go.work` (`agent`, `hubd`, `shared`) all on **Go 1.25** (bumped from 1.23 in Task 2 so `golang.org/x/crypto` v0.53.0 resolves; Dockerfile builder `golang:1.25-alpine` and CI `go-version: "1.25"` track it); hub code lives under `hubd/internal/...`; cross-module shared types live in `agentmon/shared`.
- **`CGO_ENABLED=0`** must keep building (pure-Go deps only; argon2/x-crypto are pure Go).
- **No secrets to the browser** (§10, #8): server token / signing key / password hash never appear in any `/api/v1` JSON response or the SPA bundle.
- **No raw keystroke logging; no secrets in audit** (§10): audit rows carry action/result/principal/resource and a JSON `meta` (session name allowed); never passwords, tokens, or terminal bytes.
- **Append-only `audit_log`** — only `INSERT`, never `UPDATE`/`DELETE`.
- **Secrets resolve via `*_ref`** (`env:`/`file:`) only — bare literals are rejected (Task 1).
- **TDD every task:** failing test first, watch it fail, minimal impl, watch it pass, commit. Run `go test ./...` from repo root (the `go.work` covers all three modules).
- **Error messages must never echo a secret value** (a bad `token_ref` literal could itself be the secret).
- Follow existing conventions: `writeJSONError(w, code, msg)` JSON error envelope `{"error": "..."}`; constant-time compares for token/secret equality (`crypto/subtle`); injected seams for testability (as in `agent/internal/api`).

---

## File structure (new + modified)

```
shared/
  secret.go                 NEW  ResolveSecretRef (shared by both config loaders)
  secret_test.go            NEW
hubd/internal/config/
  config.go                 MOD  use shared.ResolveSecretRef
  config_test.go            MOD  assert bare-literal rejected
agent/internal/config/
  config.go                 MOD  use shared.ResolveSecretRef (symmetric)
  config_test.go            MOD
hubd/internal/authn/
  password.go               NEW  argon2id Hash/Verify (PHC string)
  session.go                NEW  in-memory Store + Session (principal, csrf, expiry)
  cookie.go                 NEW  Set/Clear session cookie + secureFromRequest
  csrf.go                   NEW  CheckCSRF (constant-time header vs session)
  ratelimit.go              NEW  Limiter (max attempts / window, injectable clock)
  middleware.go             NEW  RequireAuth + principal-in-context
  origin.go                 NEW  CheckOrigin against external_origin
  login.go                  NEW  POST /auth/login handler
  session_handlers.go       NEW  POST /auth/logout + GET /me
  *_test.go                 NEW  per file
hubd/internal/authz/
  authz.go                  NEW  Principal, Action, Decision, Authorize chokepoint
  authz_test.go             NEW
hubd/internal/audit/
  audit.go                  NEW  Recorder over db.AuditRepo (typed events, uuid ids)
  audit_test.go             NEW
hubd/internal/registry/
  registry.go               NEW  Registry from config.Servers; List/Get; public DTOs
  client.go                 NEW  agent REST client (Sessions, Health)
  registry_test.go          NEW
  client_test.go            NEW
hubd/internal/api/
  servers.go                NEW  GET /servers, /servers/{id}
  sessions.go               NEW  GET /servers/{id}/sessions[/{name}]
  audit.go                  NEW  GET /audit
  router.go                 NEW  assemble /api/v1 mux + middleware + SPA fallback
  health_test.go            MOD  assert version field
  integration_test.go       NEW  httptest fake-agent end-to-end
hubd/internal/db/
  users.go                  MOD  SetPassword upsert
  repo_test.go              MOD  hygiene: d, err := Open(...)
hubd/cmd/agentmon-hubd/
  main.go                   MOD  wire everything + `user set-password` subcommand + server timeouts
hubd/go.mod                 MOD  + golang.org/x/crypto, github.com/google/uuid (direct)
```

---

## Task 1: Pre-task — shared `ResolveSecretRef` + harden both config loaders

Hardens secret resolution **before** auth lands: secret fields must use an `env:`/`file:` scheme; bare literals and unknown schemes are rejected; both loaders share one implementation so they are symmetric by construction.

**Files:**
- Create: `shared/secret.go`, `shared/secret_test.go`
- Modify: `hubd/internal/config/config.go` (drop local `resolveRef`, call `shared.ResolveSecretRef`)
- Modify: `agent/internal/config/config.go` (drop local `resolveRef`, call `shared.ResolveSecretRef`)
- Modify: `hubd/internal/config/config_test.go`, `agent/internal/config/config_test.go`

**Interfaces:**
- Produces: `func shared.ResolveSecretRef(v string) (string, error)` — `""`→error; `env:NAME`→`os.LookupEnv` (error if unset); `file:PATH`→read+`TrimSpace` (error wrapped); anything else→error **without echoing `v`**.

- [ ] **Step 1: Write the failing test** `shared/secret_test.go`

```go
package shared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSecretRef(t *testing.T) {
	t.Setenv("AGENTMON_T_SECRET", "s3cr3t")
	f := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(f, []byte("  filetok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveSecretRef("env:AGENTMON_T_SECRET"); err != nil || got != "s3cr3t" {
		t.Fatalf("env: got %q err %v", got, err)
	}
	if got, err := ResolveSecretRef("file:" + f); err != nil || got != "filetok" {
		t.Fatalf("file: got %q err %v", got, err)
	}
	if _, err := ResolveSecretRef(""); err == nil {
		t.Fatal("empty ref must error")
	}
	if _, err := ResolveSecretRef("env:DEFINITELY_UNSET_AGENTMON"); err == nil {
		t.Fatal("unset env must error")
	}
}

func TestResolveSecretRefRejectsBareLiteralWithoutEchoingIt(t *testing.T) {
	_, err := ResolveSecretRef("sk-this-is-a-literal-secret")
	if err == nil {
		t.Fatal("bare literal must be rejected")
	}
	if strings.Contains(err.Error(), "sk-this-is-a-literal-secret") {
		t.Fatalf("error must NOT echo the secret value: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./shared/...` → FAIL (`ResolveSecretRef` undefined).

- [ ] **Step 3: Write `shared/secret.go`**

```go
package shared

import (
	"fmt"
	"os"
	"strings"
)

// ResolveSecretRef expands a secret reference. It REQUIRES an explicit scheme so
// a typo or a pasted plaintext secret can never be silently accepted:
//   "env:NAME"  → value of environment variable NAME (error if unset)
//   "file:PATH" → trimmed contents of PATH (error wrapped)
// An empty ref or any other form is an error. The error never echoes v, since a
// bare-literal ref could itself be the secret.
func ResolveSecretRef(v string) (string, error) {
	switch {
	case v == "":
		return "", fmt.Errorf("empty secret ref")
	case strings.HasPrefix(v, "env:"):
		name := strings.TrimPrefix(v, "env:")
		s, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("env ref %q not set", name)
		}
		return s, nil
	case strings.HasPrefix(v, "file:"):
		b, err := os.ReadFile(strings.TrimPrefix(v, "file:"))
		if err != nil {
			return "", fmt.Errorf("file ref: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return "", fmt.Errorf("secret ref must use an env: or file: scheme")
	}
}
```

- [ ] **Step 4: Wire both loaders to it.** In `hubd/internal/config/config.go`: delete the local `resolveRef` func and replace its two call sites with `shared.ResolveSecretRef`; add `"agentmon/shared"` to imports; drop now-unused `"os"`/`"strings"` if unreferenced (keep `os` — `Load` uses `os.ReadFile`). In `agent/internal/config/config.go`: delete local `resolveRef`, call `shared.ResolveSecretRef` in the `HubToken`/`DirectiveKey` loop; add `"agentmon/shared"` import; drop unused `os`/`strings` (the agent loader no longer uses them directly).

- [ ] **Step 5: Update loader tests.** In `hubd/internal/config/config_test.go`, change `TestLoadUnsetEnvRefErrors` so the surviving assertion is that a **bare-literal** `signing_key_ref` is now rejected even when env is set — replace its body to set the env and use `signing_key_ref: "literal-key"` and assert `Load` errors. Add to `agent/internal/config/config_test.go` a `TestLoadRejectsBareLiteralSecret` feeding `hub_token = "literal"` and asserting an error.

```go
// agent/internal/config/config_test.go — add:
func TestLoadRejectsBareLiteralSecret(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(p, []byte(`
listen = "10.0.0.5:8377"
server_id = "server-a"
hub_token = "plain-literal-token"
directive_key = "env:SOMEKEY"
[[targets]]
  os_user = "dev"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("bare-literal hub_token must be rejected")
	}
}
```

- [ ] **Step 6: Run all tests** — `go test ./...` → PASS (existing hub/agent config tests still green; new ones pass).

- [ ] **Step 7: Commit**

```bash
git add shared/secret.go shared/secret_test.go hubd/internal/config agent/internal/config
git commit -m "feat(m3): shared ResolveSecretRef; reject bare-literal secret refs in both loaders"
```

---

## Task 2: argon2id password hashing + verify

**Files:**
- Create: `hubd/internal/authn/password.go`, `hubd/internal/authn/password_test.go`
- Modify: `hubd/go.mod` (`go get golang.org/x/crypto/argon2` makes it a direct require)

**Interfaces:**
- Produces: `func HashPassword(plain string) (string, error)` → PHC string `$argon2id$v=19$m=65536,t=3,p=2$<b64salt>$<b64hash>`.
- Produces: `func VerifyPassword(encoded, plain string) (bool, error)` — `false,nil` on mismatch; `error` only on a malformed `encoded`. Constant-time hash compare.

- [ ] **Step 1: Write the failing test**

```go
package authn

import "testing"

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(h, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("verify good: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(h, "wrong password")
	if err != nil || ok {
		t.Fatalf("verify bad must be (false,nil): ok=%v err=%v", ok, err)
	}
}

func TestHashIsSaltedAndPHCShaped(t *testing.T) {
	a, _ := HashPassword("pw")
	b, _ := HashPassword("pw")
	if a == b {
		t.Fatal("hashes must differ (random salt)")
	}
	if a[:10] != "$argon2id$" {
		t.Fatalf("not PHC argon2id: %q", a)
	}
}

func TestVerifyMalformedEncodedErrors(t *testing.T) {
	if _, err := VerifyPassword("not-a-phc-string", "pw"); err == nil {
		t.Fatal("malformed encoded must error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./hubd/internal/authn/...` → FAIL (undefined).

- [ ] **Step 3: Add the dependency** — `cd /root/agentmon && go get golang.org/x/crypto/argon2@latest` (uses the cached v0.4x). Then `go mod tidy` after Step 4 builds.

- [ ] **Step 4: Write `hubd/internal/authn/password.go`**

```go
package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory  = 64 * 1024 // KiB
	argonTime    = 3
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns a PHC-formatted argon2id hash with a random salt.
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether plain matches the PHC-encoded argon2id hash.
// It returns (false, nil) for a wrong password and an error only when encoded is
// malformed. The final comparison is constant-time.
func VerifyPassword(encoded, plain string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("bad argon2id encoding")
	}
	var version, mem, time, threads int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("bad version: %w", err)
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); err != nil {
		return false, fmt.Errorf("bad params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("bad salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("bad hash: %w", err)
	}
	got := argon2.IDKey([]byte(plain), salt, uint32(time), uint32(mem), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
```

- [ ] **Step 5: Tidy + run** — `go mod tidy` then `go test ./hubd/internal/authn/...` → PASS. Confirm `golang.org/x/crypto` is now a direct require in `hubd/go.mod`.

- [ ] **Step 6: Commit**

```bash
git add hubd/internal/authn/password.go hubd/internal/authn/password_test.go hubd/go.mod hubd/go.sum
git commit -m "feat(m3): argon2id password hashing + verify (PHC string)"
```

---

## Task 3: Session store + cookie / Secure derivation

**Files:**
- Create: `hubd/internal/authn/session.go`, `hubd/internal/authn/cookie.go`, `hubd/internal/authn/session_test.go`, `hubd/internal/authn/cookie_test.go`

**Interfaces:**
- Produces:
  - `type Session struct { Token, PrincipalID, Username, DisplayName, CSRFToken string; Expiry time.Time }`
  - `func NewStore(ttl time.Duration) *Store`
  - `func (s *Store) New(p authz.Principal) (Session, error)` — random 32-byte token + csrf token (base64url), expiry `now+ttl`.
  - `func (s *Store) Get(token string) (Session, bool)` — false if missing or expired (expired entries are deleted lazily).
  - `func (s *Store) Delete(token string)`
  - `Store` has an unexported `now func() time.Time` (defaults to `time.Now`), set in tests for expiry.
- Produces (cookie.go):
  - `func SetSessionCookie(w http.ResponseWriter, name, token string, secure bool, ttl time.Duration)` — `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`.
  - `func ClearSessionCookie(w http.ResponseWriter, name string, secure bool)` — `MaxAge=-1`.
  - `func SecureFromRequest(r *http.Request, trustForwardedProto bool) bool` — true when `trustForwardedProto && X-Forwarded-Proto == "https"`, else `r.TLS != nil`.

Note: `Store.New` takes an `authz.Principal` → this task **depends on Task 5's `authz.Principal`**. To avoid a forward dep, define `Principal` in Task 5 first, OR have `Store.New` take `(principalID, username, displayName string)`. **Decision: `Store.New(principalID, username, displayName string)` (no authz import)** — keeps authn/authz decoupled. Update the interface above accordingly.

- [ ] **Step 1: Write the failing tests** `session_test.go`

```go
package authn

import (
	"testing"
	"time"
)

func TestSessionNewGetDelete(t *testing.T) {
	s := NewStore(time.Hour)
	sess, err := s.New("u1", "patrik", "Patrik")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Token == "" || sess.CSRFToken == "" || sess.Token == sess.CSRFToken {
		t.Fatalf("tokens bad: %+v", sess)
	}
	got, ok := s.Get(sess.Token)
	if !ok || got.PrincipalID != "u1" || got.Username != "patrik" {
		t.Fatalf("get: %+v ok=%v", got, ok)
	}
	s.Delete(sess.Token)
	if _, ok := s.Get(sess.Token); ok {
		t.Fatal("deleted session still present")
	}
}

func TestSessionExpiry(t *testing.T) {
	s := NewStore(time.Minute)
	base := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return base }
	sess, _ := s.New("u1", "p", "P")
	s.now = func() time.Time { return base.Add(2 * time.Minute) }
	if _, ok := s.Get(sess.Token); ok {
		t.Fatal("expired session must not be returned")
	}
}
```

`cookie_test.go`:

```go
package authn

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSetSessionCookieAttributes(t *testing.T) {
	w := httptest.NewRecorder()
	SetSessionCookie(w, "agentmon_session", "tok", true, time.Hour)
	c := w.Result().Cookies()[0]
	if c.Name != "agentmon_session" || c.Value != "tok" || !c.HttpOnly || !c.Secure ||
		c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("cookie attrs: %+v", c)
	}
}

func TestSecureFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if !SecureFromRequest(r, true) {
		t.Fatal("trusted https forward must be secure")
	}
	if SecureFromRequest(r, false) {
		t.Fatal("untrusted forward must not be secure")
	}
}
```

- [ ] **Step 2: Run to verify fail** — `go test ./hubd/internal/authn/...` → FAIL.

- [ ] **Step 3: Write `session.go`**

```go
package authn

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

type Session struct {
	Token       string
	PrincipalID string
	Username    string
	DisplayName string
	CSRFToken   string
	Expiry      time.Time
}

type Store struct {
	mu  sync.Mutex
	m   map[string]Session
	ttl time.Duration
	now func() time.Time
}

func NewStore(ttl time.Duration) *Store {
	return &Store{m: make(map[string]Session), ttl: ttl, now: time.Now}
}

func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Store) New(principalID, username, displayName string) (Session, error) {
	tok, err := randToken()
	if err != nil {
		return Session{}, err
	}
	csrf, err := randToken()
	if err != nil {
		return Session{}, err
	}
	sess := Session{
		Token: tok, PrincipalID: principalID, Username: username,
		DisplayName: displayName, CSRFToken: csrf, Expiry: s.now().Add(s.ttl),
	}
	s.mu.Lock()
	s.m[tok] = sess
	s.mu.Unlock()
	return sess, nil
}

func (s *Store) Get(token string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[token]
	if !ok {
		return Session{}, false
	}
	if !s.now().Before(sess.Expiry) {
		delete(s.m, token)
		return Session{}, false
	}
	return sess, true
}

func (s *Store) Delete(token string) {
	s.mu.Lock()
	delete(s.m, token)
	s.mu.Unlock()
}
```

- [ ] **Step 4: Write `cookie.go`**

```go
package authn

import (
	"net/http"
	"time"
)

func SetSessionCookie(w http.ResponseWriter, name, token string, secure bool, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: token, Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(ttl.Seconds()),
	})
}

func ClearSessionCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
}

// SecureFromRequest reports whether the cookie should carry the Secure flag.
// Behind Caddy the LAN hop is plain HTTP, so we trust X-Forwarded-Proto only
// when configured to (trust_forwarded_proto); otherwise fall back to r.TLS.
func SecureFromRequest(r *http.Request, trustForwardedProto bool) bool {
	if trustForwardedProto && r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return r.TLS != nil
}
```

- [ ] **Step 5: Run** — `go test ./hubd/internal/authn/...` → PASS.

- [ ] **Step 6: Commit**

```bash
git add hubd/internal/authn/session.go hubd/internal/authn/cookie.go hubd/internal/authn/session_test.go hubd/internal/authn/cookie_test.go
git commit -m "feat(m3): in-memory session store + secure session cookie helpers"
```

---

## Task 4: CSRF check + login rate limiter

**Files:**
- Create: `hubd/internal/authn/csrf.go`, `hubd/internal/authn/ratelimit.go`, `hubd/internal/authn/csrf_test.go`, `hubd/internal/authn/ratelimit_test.go`

**Interfaces:**
- Produces: `func CheckCSRF(r *http.Request, sess Session) bool` — constant-time compare of the `X-CSRF-Token` header to `sess.CSRFToken`.
- Produces:
  - `func NewLimiter(maxAttempts int, window time.Duration) *Limiter` (an injectable `now func() time.Time`)
  - `func (l *Limiter) Allowed(key string) bool` — true if fewer than `maxAttempts` failures recorded within the trailing `window`.
  - `func (l *Limiter) Fail(key string)` — record a failed attempt at `now()`.
  - `func (l *Limiter) Reset(key string)` — clear on success.

- [ ] **Step 1: Write failing tests** `csrf_test.go`

```go
package authn

import (
	"net/http/httptest"
	"testing"
)

func TestCheckCSRF(t *testing.T) {
	sess := Session{CSRFToken: "tok123"}
	r := httptest.NewRequest("POST", "/x", nil)
	if CheckCSRF(r, sess) {
		t.Fatal("missing header must fail")
	}
	r.Header.Set("X-CSRF-Token", "tok123")
	if !CheckCSRF(r, sess) {
		t.Fatal("matching header must pass")
	}
	r.Header.Set("X-CSRF-Token", "wrong")
	if CheckCSRF(r, sess) {
		t.Fatal("mismatched header must fail")
	}
}
```

`ratelimit_test.go`:

```go
package authn

import (
	"testing"
	"time"
)

func TestLimiterBlocksAfterMaxThenRecoversAfterWindow(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	base := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return base }
	for i := 0; i < 3; i++ {
		if !l.Allowed("patrik") {
			t.Fatalf("attempt %d should be allowed", i)
		}
		l.Fail("patrik")
	}
	if l.Allowed("patrik") {
		t.Fatal("4th attempt must be blocked")
	}
	l.now = func() time.Time { return base.Add(2 * time.Minute) }
	if !l.Allowed("patrik") {
		t.Fatal("must recover after window")
	}
}

func TestLimiterResetOnSuccess(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	l.Fail("p")
	l.Fail("p")
	if l.Allowed("p") {
		t.Fatal("should be blocked")
	}
	l.Reset("p")
	if !l.Allowed("p") {
		t.Fatal("reset must clear failures")
	}
}
```

- [ ] **Step 2: Run to verify fail** — FAIL (undefined).

- [ ] **Step 3: Write `csrf.go`**

```go
package authn

import (
	"crypto/subtle"
	"net/http"
)

// CheckCSRF reports whether the request carries the session's CSRF token in the
// X-CSRF-Token header. The session cookie is SameSite=Lax; this synchronizer
// token is defense-in-depth for cookie-authed mutations.
func CheckCSRF(r *http.Request, sess Session) bool {
	got := r.Header.Get("X-CSRF-Token")
	if got == "" || sess.CSRFToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) == 1
}
```

- [ ] **Step 4: Write `ratelimit.go`**

```go
package authn

import (
	"sync"
	"time"
)

// Limiter is a per-key sliding-window failed-attempt counter for login throttling.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
	now    func() time.Time
}

func NewLimiter(maxAttempts int, window time.Duration) *Limiter {
	return &Limiter{max: maxAttempts, window: window, fails: make(map[string][]time.Time), now: time.Now}
}

func (l *Limiter) prune(key string, t time.Time) []time.Time {
	cutoff := t.Add(-l.window)
	kept := l.fails[key][:0]
	for _, ts := range l.fails[key] {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	l.fails[key] = kept
	return kept
}

func (l *Limiter) Allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, l.now())) < l.max
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()
	l.prune(key, t)
	l.fails[key] = append(l.fails[key], t)
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}
```

- [ ] **Step 5: Run** — `go test ./hubd/internal/authn/...` → PASS.

- [ ] **Step 6: Commit**

```bash
git add hubd/internal/authn/csrf.go hubd/internal/authn/ratelimit.go hubd/internal/authn/csrf_test.go hubd/internal/authn/ratelimit_test.go
git commit -m "feat(m3): CSRF synchronizer-token check + login rate limiter"
```

---

## Task 5: `authorize()` chokepoint

**Files:**
- Create: `hubd/internal/authz/authz.go`, `hubd/internal/authz/authz_test.go`

**Interfaces:**
- Produces:
  - `type Principal struct { ID, Username, DisplayName string }`
  - `type Action string` with consts `ServerView, SessionView, TerminalRead, TerminalWrite, AuditRead Action`
  - `type Decision struct { Allow bool; Reason string }`
  - `func Authorize(ctx context.Context, p Principal, action Action, resource string) (Decision, error)` — v1 body: a non-empty `p.ID` ⇒ `{Allow:true}`; empty ⇒ `{Allow:false, Reason:"no principal"}`.

- [ ] **Step 1: Write the failing test**

```go
package authz

import (
	"context"
	"testing"
)

func TestAuthorizeAllowsAuthenticatedPrincipalForEveryPhase1Action(t *testing.T) {
	ctx := context.Background()
	p := Principal{ID: "u1", Username: "patrik"}
	for _, a := range []Action{ServerView, SessionView, TerminalRead, TerminalWrite, AuditRead} {
		d, err := Authorize(ctx, p, a, "server:server-a")
		if err != nil || !d.Allow {
			t.Fatalf("action %q: allow=%v err=%v", a, d.Allow, err)
		}
	}
}

func TestAuthorizeDeniesEmptyPrincipal(t *testing.T) {
	d, err := Authorize(context.Background(), Principal{}, ServerView, "server:server-a")
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow {
		t.Fatal("empty principal must be denied")
	}
}
```

- [ ] **Step 2: Run to verify fail** — FAIL.

- [ ] **Step 3: Write `authz.go`**

```go
// Package authz is the hub's single authorization chokepoint. Every protected
// REST handler and (in M4) the WS upgrade calls Authorize. The v1 body is a
// trivial single-user allow, but the seam is real: the principal is stamped at
// the edge and every decision flows through here so denies can be audited.
package authz

import "context"

type Principal struct {
	ID          string
	Username    string
	DisplayName string
}

type Action string

const (
	ServerView    Action = "server.view"
	SessionView   Action = "session.view"
	TerminalRead  Action = "terminal.read"
	TerminalWrite Action = "terminal.write"
	AuditRead     Action = "audit.read"
)

type Decision struct {
	Allow  bool
	Reason string
}

// Authorize is the Phase 1 policy: any authenticated single principal is allowed
// every action. The signature carries action+resource so later phases add real
// policy without touching call sites.
func Authorize(ctx context.Context, p Principal, action Action, resource string) (Decision, error) {
	if p.ID == "" {
		return Decision{Allow: false, Reason: "no principal"}, nil
	}
	return Decision{Allow: true}, nil
}
```

- [ ] **Step 4: Run** — `go test ./hubd/internal/authz/...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add hubd/internal/authz
git commit -m "feat(m3): authorize() chokepoint (single-principal v1 body)"
```

---

## Task 6: audit recorder + `db.SetPassword` upsert + db test hygiene

**Files:**
- Create: `hubd/internal/audit/audit.go`, `hubd/internal/audit/audit_test.go`
- Modify: `hubd/internal/db/users.go` (add `SetPassword`)
- Modify: `hubd/internal/db/repo_test.go` (hygiene: `d, err := Open(...)`; add a `SetPassword` round-trip test)

**Interfaces:**
- Produces (`audit`):
  - `type Sink interface { Append(ctx context.Context, e db.AuditEntry) error }` (satisfied by `*db.DB`)
  - `func NewRecorder(s Sink) *Recorder`
  - `func (r *Recorder) LoginSuccess(ctx, principalID, ip, ua string)`
  - `func (r *Recorder) LoginFailure(ctx, username, ip, ua string)` — result `deny`, action `login.failure`, resource `user:<username>`, **no password**.
  - `func (r *Recorder) Deny(ctx, principalID string, action authz.Action, resource, ip, ua, meta string)`
  - All swallow+log the underlying error (audit must never break the request path) and stamp a `uuid` id.
- Produces (`db`): `func (d *DB) SetPassword(ctx context.Context, id, username, displayName, passwordHash string) error` — `INSERT ... ON CONFLICT(username) DO UPDATE SET password_hash=excluded.password_hash, display_name=excluded.display_name, updated_at=datetime('now')`.

Note: `audit` importing `authz` for the `Action` type is fine (one-way authz→nothing, audit→authz). Keep `Deny`'s `action` as `authz.Action`.

- [ ] **Step 1: Write the failing tests** `audit_test.go`

```go
package audit

import (
	"context"
	"testing"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
)

type fakeSink struct{ rows []db.AuditEntry }

func (f *fakeSink) Append(_ context.Context, e db.AuditEntry) error {
	f.rows = append(f.rows, e)
	return nil
}

func TestRecorderWritesTypedEvents(t *testing.T) {
	s := &fakeSink{}
	r := NewRecorder(s)
	ctx := context.Background()
	r.LoginSuccess(ctx, "u1", "1.2.3.4", "agent/1")
	r.LoginFailure(ctx, "patrik", "1.2.3.4", "agent/1")
	r.Deny(ctx, "u1", authz.SessionView, "server:server-a", "1.2.3.4", "agent/1", `{"session":"proj"}`)

	if len(s.rows) != 3 {
		t.Fatalf("rows: %d", len(s.rows))
	}
	if s.rows[0].Action != "login.success" || s.rows[0].Result != "allow" || s.rows[0].ID == "" {
		t.Fatalf("login success row: %+v", s.rows[0])
	}
	if s.rows[1].Action != "login.failure" || s.rows[1].Result != "deny" || s.rows[1].Resource != "user:patrik" {
		t.Fatalf("login failure row: %+v", s.rows[1])
	}
	if s.rows[2].Action != "session.view" || s.rows[2].Result != "deny" || s.rows[2].Meta != `{"session":"proj"}` {
		t.Fatalf("deny row: %+v", s.rows[2])
	}
}
```

`db/repo_test.go` — add:

```go
func TestSetPasswordUpserts(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := d.SetPassword(ctx, "u1", "patrik", "Patrik", "$argon2id$v1"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPassword(ctx, "u1", "patrik", "Patrik R", "$argon2id$v2"); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetUserByUsername(ctx, "patrik")
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash != "$argon2id$v2" || got.DisplayName != "Patrik R" {
		t.Fatalf("upsert did not update: %+v", got)
	}
}
```

Also fix the existing `TestUserRepoRoundTrip` `d, _ := Open(...)` → `d, err := Open(...); if err != nil { t.Fatal(err) }`.

- [ ] **Step 2: Run to verify fail** — FAIL (undefined `SetPassword`, `NewRecorder`).

- [ ] **Step 3: Add `db.SetPassword` in `hubd/internal/db/users.go`**

```go
func (d *DB) SetPassword(ctx context.Context, id, username, displayName, passwordHash string) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO users(id, username, display_name, password_hash, status, created_at, updated_at)
		 VALUES(?,?,?,?, 'active', datetime('now'), datetime('now'))
		 ON CONFLICT(username) DO UPDATE SET
		   display_name=excluded.display_name,
		   password_hash=excluded.password_hash,
		   updated_at=datetime('now')`,
		id, username, displayName, passwordHash)
	return err
}
```

- [ ] **Step 4: Write `audit/audit.go`**

```go
// Package audit records security-relevant events to the append-only audit_log.
// Writes never include secrets or raw keystrokes; the session name (if any) goes
// in the JSON meta. Append failures are logged, never propagated to the caller —
// a broken audit write must not break the request it describes.
package audit

import (
	"context"
	"log"

	"github.com/google/uuid"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
)

type Sink interface {
	Append(ctx context.Context, e db.AuditEntry) error
}

type Recorder struct{ sink Sink }

func NewRecorder(s Sink) *Recorder { return &Recorder{sink: s} }

func (r *Recorder) write(ctx context.Context, e db.AuditEntry) {
	e.ID = uuid.NewString()
	if err := r.sink.Append(ctx, e); err != nil {
		log.Printf("audit: append failed (action=%s result=%s): %v", e.Action, e.Result, err)
	}
}

func (r *Recorder) LoginSuccess(ctx context.Context, principalID, ip, ua string) {
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "login.success",
		Resource: "user:" + principalID, Result: "allow", IP: ip, UserAgent: ua})
}

func (r *Recorder) LoginFailure(ctx context.Context, username, ip, ua string) {
	r.write(ctx, db.AuditEntry{Action: "login.failure",
		Resource: "user:" + username, Result: "deny", IP: ip, UserAgent: ua})
}

func (r *Recorder) Deny(ctx context.Context, principalID string, action authz.Action, resource, ip, ua, meta string) {
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: string(action),
		Resource: resource, Result: "deny", IP: ip, UserAgent: ua, Meta: meta})
}
```

- [ ] **Step 5: Tidy + run** — `go mod tidy` (promotes `github.com/google/uuid` to direct), `go test ./hubd/...` → PASS.

- [ ] **Step 6: Commit**

```bash
git add hubd/internal/audit hubd/internal/db/users.go hubd/internal/db/repo_test.go hubd/go.mod hubd/go.sum
git commit -m "feat(m3): audit recorder + db.SetPassword upsert; db test hygiene"
```

---

## Task 7: `RequireAuth` middleware + principal context + origin check

**Files:**
- Create: `hubd/internal/authn/middleware.go`, `hubd/internal/authn/origin.go`, `hubd/internal/authn/middleware_test.go`, `hubd/internal/authn/origin_test.go`

**Interfaces:**
- Produces:
  - `func PrincipalFrom(ctx context.Context) (authz.Principal, bool)` and an unexported context key.
  - `type Authenticator struct { Store *Store; CookieName string; Audit *audit.Recorder }`
  - `func (a *Authenticator) RequireAuth(next http.Handler) http.Handler` — reads the session cookie, looks it up; on miss → `401 {"error":"unauthorized"}`; on hit → stamps `authz.Principal` into the request context and, for non-safe methods (`POST/PUT/PATCH/DELETE`), enforces `CheckCSRF` (403 on failure). Calls `next`.
  - `func CheckOrigin(r *http.Request, externalOrigin string) bool` — true if `Origin` header equals `externalOrigin`; if `Origin` is absent, true (same-origin / non-browser). (Host fallback kept simple for M3.)

This task **depends on** Task 3 (`Store`), Task 4 (`CheckCSRF`), Task 5 (`authz.Principal`), Task 6 (`audit.Recorder`).

- [ ] **Step 1: Write failing tests** `middleware_test.go`

```go
package authn

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/authz"
)

func newAuth(t *testing.T) (*Authenticator, *Store) {
	t.Helper()
	st := NewStore(time.Hour)
	return &Authenticator{Store: st, CookieName: "agentmon_session"}, st
}

func TestRequireAuthRejectsNoCookie(t *testing.T) {
	a, _ := newAuth(t)
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not reach handler")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/servers", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestRequireAuthStampsPrincipal(t *testing.T) {
	a, st := newAuth(t)
	sess, _ := st.New("u1", "patrik", "Patrik")
	var seen authz.Principal
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = PrincipalFrom(r.Context())
	}))
	r := httptest.NewRequest("GET", "/api/v1/servers", nil)
	r.AddCookie(&http.Cookie{Name: "agentmon_session", Value: sess.Token})
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen.ID != "u1" || seen.Username != "patrik" {
		t.Fatalf("principal: %+v", seen)
	}
}

func TestRequireAuthEnforcesCSRFOnMutations(t *testing.T) {
	a, st := newAuth(t)
	sess, _ := st.New("u1", "patrik", "Patrik")
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: "agentmon_session", Value: sess.Token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r) // no X-CSRF-Token
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF must be 403, got %d", w.Code)
	}
	r.Header.Set("X-CSRF-Token", sess.CSRFToken)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("valid CSRF must pass, got %d", w.Code)
	}
}
```

`origin_test.go`:

```go
package authn

import (
	"net/http/httptest"
	"testing"
)

func TestCheckOrigin(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	if !CheckOrigin(r, "https://agentmon.lan") {
		t.Fatal("absent Origin should pass")
	}
	r.Header.Set("Origin", "https://agentmon.lan")
	if !CheckOrigin(r, "https://agentmon.lan") {
		t.Fatal("matching Origin should pass")
	}
	r.Header.Set("Origin", "https://evil.example")
	if CheckOrigin(r, "https://agentmon.lan") {
		t.Fatal("mismatched Origin must fail")
	}
}
```

- [ ] **Step 2: Run to verify fail** — FAIL.

- [ ] **Step 3: Write `middleware.go`**

```go
package authn

import (
	"context"
	"encoding/json"
	"net/http"

	"agentmon/hubd/internal/authz"
)

type ctxKey int

const principalKey ctxKey = 0

func PrincipalFrom(ctx context.Context) (authz.Principal, bool) {
	p, ok := ctx.Value(principalKey).(authz.Principal)
	return p, ok
}

type Authenticator struct {
	Store      *Store
	CookieName string
}

func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(a.CookieName)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		sess, ok := a.Store.Get(c.Value)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if !CheckCSRF(r, sess) {
				writeErr(w, http.StatusForbidden, "csrf")
				return
			}
		}
		p := authz.Principal{ID: sess.PrincipalID, Username: sess.Username, DisplayName: sess.DisplayName}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
```

(Drop the `Audit` field from `Authenticator` for now — denies on these paths are 401/403 before a principal exists, and the audited authorization denies happen in the REST handlers via `authz` + `audit.Recorder`. Keep the middleware lean.)

- [ ] **Step 4: Write `origin.go`**

```go
package authn

import "net/http"

// CheckOrigin guards credentialed cross-origin requests (login + cookie-authed
// mutations). A present Origin must equal external_origin; an absent Origin
// (same-origin navigation or a non-browser client) is allowed. SameSite=Lax on
// the session cookie is the companion control.
func CheckOrigin(r *http.Request, externalOrigin string) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	return o == externalOrigin
}
```

- [ ] **Step 5: Run** — `go test ./hubd/internal/authn/...` → PASS.

- [ ] **Step 6: Commit**

```bash
git add hubd/internal/authn/middleware.go hubd/internal/authn/origin.go hubd/internal/authn/middleware_test.go hubd/internal/authn/origin_test.go
git commit -m "feat(m3): RequireAuth middleware (principal context + CSRF) + origin check"
```

---

## Task 8: Login handler

**Files:**
- Create: `hubd/internal/authn/login.go`, `hubd/internal/authn/login_test.go`

**Interfaces:**
- Produces:
  - `type UserLookup interface { GetUserByUsername(ctx context.Context, username string) (db.User, error) }` (satisfied by `*db.DB`)
  - `type LoginDeps struct { Users UserLookup; Store *Store; Limiter *Limiter; Audit *audit.Recorder; CookieName string; CookieTTL time.Duration; ExternalOrigin string; TrustForwardedProto bool }`
  - `func (d LoginDeps) LoginHandler() http.HandlerFunc` — `POST /api/v1/auth/login`:
    1. `CheckOrigin` → 403 on mismatch.
    2. decode `{username,password}` → 400 on bad body.
    3. `Limiter.Allowed(username)` → 429 if not.
    4. `Users.GetUserByUsername`; on not-found run `VerifyPassword` against a dummy hash to keep timing flat, then fail.
    5. `VerifyPassword(user.PasswordHash, password)`; on false → `Limiter.Fail`, `Audit.LoginFailure`, 401.
    6. on success → `Limiter.Reset`, `Store.New`, `SetSessionCookie` (Secure via `SecureFromRequest`), `Audit.LoginSuccess`, 200 `{principalId,username,displayName,csrfToken}`.

- [ ] **Step 1: Write the failing test** `login_test.go`

```go
package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/db"
)

type stubUsers struct{ u db.User; err error }

func (s stubUsers) GetUserByUsername(_ context.Context, _ string) (db.User, error) {
	return s.u, s.err
}

func deps(t *testing.T, u db.User, err error) LoginDeps {
	return LoginDeps{
		Users: stubUsers{u: u, err: err}, Store: NewStore(time.Hour),
		Limiter: NewLimiter(5, time.Minute), Audit: audit.NewRecorder(&countSink{}),
		CookieName: "agentmon_session", CookieTTL: time.Hour, ExternalOrigin: "https://agentmon.lan",
	}
}

type countSink struct{ n int }

func (c *countSink) Append(_ context.Context, _ db.AuditEntry) error { c.n++; return nil }

func TestLoginSuccessSetsCookieAndReturnsCSRF(t *testing.T) {
	hash, _ := HashPassword("pw")
	d := deps(t, db.User{ID: "u1", Username: "patrik", DisplayName: "Patrik", PasswordHash: hash, Status: "active"}, nil)
	body := strings.NewReader(`{"username":"patrik","password":"pw"}`)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", body)
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) == 0 || w.Result().Cookies()[0].Name != "agentmon_session" {
		t.Fatal("no session cookie set")
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["principalId"] != "u1" || resp["csrfToken"] == "" {
		t.Fatalf("resp: %+v", resp)
	}
}

func TestLoginWrongPasswordIs401(t *testing.T) {
	hash, _ := HashPassword("pw")
	d := deps(t, db.User{ID: "u1", Username: "patrik", PasswordHash: hash}, nil)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"NOPE"}`))
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", w.Code)
	}
}

func TestLoginOriginMismatchIs403(t *testing.T) {
	d := deps(t, db.User{}, nil)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code %d", w.Code)
	}
}

func TestLoginRateLimited(t *testing.T) {
	hash, _ := HashPassword("pw")
	d := deps(t, db.User{ID: "u1", Username: "patrik", PasswordHash: hash}, nil)
	d.Limiter = NewLimiter(1, time.Minute)
	r1 := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"NOPE"}`))
	d.LoginHandler()(httptest.NewRecorder(), r1) // 1 failure → limiter now full
	r2 := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"NOPE"}`))
	w := httptest.NewRecorder()
	d.LoginHandler()(w, r2)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run to verify fail** — FAIL.

- [ ] **Step 3: Write `login.go`**

```go
package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/db"
)

type UserLookup interface {
	GetUserByUsername(ctx context.Context, username string) (db.User, error)
}

type LoginDeps struct {
	Users               UserLookup
	Store               *Store
	Limiter             *Limiter
	Audit               *audit.Recorder
	CookieName          string
	CookieTTL           time.Duration
	ExternalOrigin      string
	TrustForwardedProto bool
}

// dummyHash is a valid argon2id encoding used to keep verify timing flat when the
// username does not exist (avoids a user-enumeration timing oracle).
var dummyHash, _ = HashPassword("agentmon-dummy-password")

func (d LoginDeps) LoginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !CheckOrigin(r, d.ExternalOrigin) {
			writeErr(w, http.StatusForbidden, "bad origin")
			return
		}
		var body struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "bad request")
			return
		}
		ip := clientIP(r)
		if !d.Limiter.Allowed(body.Username) {
			writeErr(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		u, err := d.Users.GetUserByUsername(r.Context(), body.Username)
		hash := u.PasswordHash
		if err != nil {
			hash = dummyHash // constant-time-ish failure for unknown user
		}
		ok, _ := VerifyPassword(hash, body.Password)
		if err != nil || !ok || u.Status != "active" {
			d.Limiter.Fail(body.Username)
			d.Audit.LoginFailure(r.Context(), body.Username, ip, r.UserAgent())
			writeErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		d.Limiter.Reset(body.Username)
		sess, err := d.Store.New(u.ID, u.Username, u.DisplayName)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "session")
			return
		}
		SetSessionCookie(w, d.CookieName, sess.Token, SecureFromRequest(r, d.TrustForwardedProto), d.CookieTTL)
		d.Audit.LoginSuccess(r.Context(), u.ID, ip, r.UserAgent())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"principalId": u.ID, "username": u.Username,
			"displayName": u.DisplayName, "csrfToken": sess.CSRFToken,
		})
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}
```

Note on `u.Status`: the test `TestLoginWrongPasswordIs401` sets no `Status`, but that path fails on `!ok` first, so the `Status` check is not reached. The success test sets `Status:"active"`. Good.

- [ ] **Step 4: Run** — `go test ./hubd/internal/authn/...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add hubd/internal/authn/login.go hubd/internal/authn/login_test.go
git commit -m "feat(m3): POST /auth/login (argon2id, rate-limit, origin, session cookie, audit)"
```

---

## Task 9: Logout + Me handlers

**Files:**
- Create: `hubd/internal/authn/session_handlers.go`, `hubd/internal/authn/session_handlers_test.go`

**Interfaces:**
- Produces:
  - `func (a *Authenticator) LogoutHandler(trustForwardedProto bool) http.HandlerFunc` — deletes the session, clears the cookie, 204. (Mounted behind `RequireAuth`, so CSRF is already enforced there.)
  - `func MeHandler() http.HandlerFunc` — reads `PrincipalFrom(ctx)`; needs the session's CSRF token too. Since the principal context lacks the CSRF token, `MeHandler` is a method on `Authenticator` that re-reads the cookie→session to surface `csrfToken`. Signature: `func (a *Authenticator) MeHandler() http.HandlerFunc` → 200 `{principalId,username,displayName,csrfToken}`.

- [ ] **Step 1: Write failing tests** `session_handlers_test.go`

```go
package authn

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLogoutDeletesSessionAndClearsCookie(t *testing.T) {
	st := NewStore(time.Hour)
	a := &Authenticator{Store: st, CookieName: "agentmon_session"}
	sess, _ := st.New("u1", "patrik", "Patrik")
	r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: "agentmon_session", Value: sess.Token})
	w := httptest.NewRecorder()
	a.LogoutHandler(false)(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("code %d", w.Code)
	}
	if _, ok := st.Get(sess.Token); ok {
		t.Fatal("session not deleted")
	}
	c := w.Result().Cookies()[0]
	if c.MaxAge >= 0 {
		t.Fatalf("cookie not cleared: %+v", c)
	}
}

func TestMeReturnsPrincipalAndCSRF(t *testing.T) {
	st := NewStore(time.Hour)
	a := &Authenticator{Store: st, CookieName: "agentmon_session"}
	sess, _ := st.New("u1", "patrik", "Patrik")
	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: "agentmon_session", Value: sess.Token})
	w := httptest.NewRecorder()
	a.MeHandler()(w, r)
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["principalId"] != "u1" || resp["username"] != "patrik" || resp["csrfToken"] != sess.CSRFToken {
		t.Fatalf("resp %+v", resp)
	}
}
```

- [ ] **Step 2: Run to verify fail** — FAIL.

- [ ] **Step 3: Write `session_handlers.go`**

```go
package authn

import (
	"encoding/json"
	"net/http"
)

func (a *Authenticator) LogoutHandler(trustForwardedProto bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(a.CookieName); err == nil {
			a.Store.Delete(c.Value)
		}
		ClearSessionCookie(w, a.CookieName, SecureFromRequest(r, trustForwardedProto))
		w.WriteHeader(http.StatusNoContent)
	}
}

// MeHandler returns the current principal plus the session CSRF token (the SPA
// needs it to send X-CSRF-Token on mutations; the HttpOnly cookie is unreadable
// to JS). It re-reads the cookie to surface the CSRF token. Mount behind
// RequireAuth so an absent/expired session is already 401.
func (a *Authenticator) MeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(a.CookieName)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		sess, ok := a.Store.Get(c.Value)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"principalId": sess.PrincipalID, "username": sess.Username,
			"displayName": sess.DisplayName, "csrfToken": sess.CSRFToken,
		})
	}
}
```

- [ ] **Step 4: Run** — `go test ./hubd/internal/authn/...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add hubd/internal/authn/session_handlers.go hubd/internal/authn/session_handlers_test.go
git commit -m "feat(m3): POST /auth/logout + GET /me handlers"
```

---

## Task 10: Registry + agent REST client

**Files:**
- Create: `hubd/internal/registry/registry.go`, `hubd/internal/registry/client.go`, `hubd/internal/registry/registry_test.go`, `hubd/internal/registry/client_test.go`

**Interfaces:**
- Produces (`registry.go`):
  - `type ServerSummary struct { ID, Name string; Labels []string; Enabled bool }` (JSON: `id,name,labels,enabled`)
  - `type ServerDetail struct { ID, Name string; Labels []string; Enabled, Healthy bool }` (JSON: `id,name,labels,enabled,healthy`)
  - `func New(servers []config.Server) *Registry`
  - `func (r *Registry) List() []ServerSummary`
  - `func (r *Registry) Get(id string) (config.Server, bool)` — returns the **full** internal server (with token/URL) for hub-side dialing; never serialized to the browser.
- Produces (`client.go`):
  - `type Client struct { HTTP *http.Client }` + `func NewClient(timeout time.Duration) *Client`
  - `func (c *Client) Sessions(ctx context.Context, srv config.Server, target string) ([]shared.Session, error)` — `GET {srv.URL}/sessions[?target=]` with `Authorization: Bearer <srv.Token>`; non-200 → error; decode `shared.SessionList`; stamp each session's `Server = srv.ID`; return `[]shared.Session` (never nil — `[]` when empty).
  - `func (c *Client) Health(ctx context.Context, srv config.Server) bool` — `GET {srv.URL}/healthz` 200 → true; any error/non-200 → false.

Labels default: `config.Server.Labels` may be nil → emit `[]` not null (handler concern; the DTO can keep nil and the JSON encoder will emit `null` — to force `[]`, normalize in `List()`).

- [ ] **Step 1: Write failing tests** `client_test.go`

```go
package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/config"
	"agentmon/shared"
)

func fakeAgent(t *testing.T, wantToken string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"sessions":[{"name":"proj","server":"WRONG","target":"default","cwd":"/home/dev/proj","command":"claude","windows":[]}]}`))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	return httptest.NewServer(mux)
}

func TestClientSessionsStampsServerID(t *testing.T) {
	ts := fakeAgent(t, "tok-a")
	defer ts.Close()
	c := NewClient(2 * time.Second)
	srv := config.Server{ID: "server-a", URL: ts.URL, Token: "tok-a"}
	got, err := c.Sessions(context.Background(), srv, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "proj" || got[0].Server != "server-a" {
		t.Fatalf("sessions: %+v", got)
	}
	_ = shared.Session{}
}

func TestClientSessionsBadTokenErrors(t *testing.T) {
	ts := fakeAgent(t, "tok-a")
	defer ts.Close()
	c := NewClient(2 * time.Second)
	srv := config.Server{ID: "server-a", URL: ts.URL, Token: "WRONG"}
	if _, err := c.Sessions(context.Background(), srv, ""); err == nil {
		t.Fatal("bad token must error")
	}
}

func TestClientSessionsMalformedJSONErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{not json`))
	}))
	defer ts.Close()
	c := NewClient(2 * time.Second)
	if _, err := c.Sessions(context.Background(), config.Server{ID: "s", URL: ts.URL, Token: "t"}, ""); err == nil {
		t.Fatal("malformed json must error")
	}
}

func TestClientHealth(t *testing.T) {
	ts := fakeAgent(t, "tok-a")
	defer ts.Close()
	c := NewClient(2 * time.Second)
	if !c.Health(context.Background(), config.Server{URL: ts.URL}) {
		t.Fatal("healthy agent must report true")
	}
	ts.Close()
	if c.Health(context.Background(), config.Server{URL: ts.URL}) {
		t.Fatal("dead agent must report false")
	}
}
```

`registry_test.go`:

```go
package registry

import (
	"testing"

	"agentmon/hubd/internal/config"
)

func TestRegistryListAndGet(t *testing.T) {
	r := New([]config.Server{
		{ID: "server-a", Name: "A", URL: "http://10.0.0.5:8377", Token: "t", Labels: []string{"prod"}},
		{ID: "server-b", Name: "B", URL: "http://10.0.0.6:8377", Token: "t2"},
	})
	list := r.List()
	if len(list) != 2 || list[0].ID != "server-a" || !list[0].Enabled {
		t.Fatalf("list: %+v", list)
	}
	if list[1].Labels == nil {
		t.Fatal("nil labels must normalize to empty slice")
	}
	s, ok := r.Get("server-b")
	if !ok || s.Token != "t2" {
		t.Fatalf("get: %+v ok=%v", s, ok)
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("unknown id must not be found")
	}
}
```

- [ ] **Step 2: Run to verify fail** — FAIL.

- [ ] **Step 3: Write `registry.go`**

```go
// Package registry holds the config-driven server list and dials agents. The
// internal config.Server (URL + bearer token) is hub-side only; List/ServerSummary
// are the browser-safe projections (no secrets).
package registry

import "agentmon/hubd/internal/config"

type ServerSummary struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Labels  []string `json:"labels"`
	Enabled bool     `json:"enabled"`
}

type ServerDetail struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Labels  []string `json:"labels"`
	Enabled bool     `json:"enabled"`
	Healthy bool     `json:"healthy"`
}

type Registry struct {
	order   []string
	servers map[string]config.Server
}

func New(servers []config.Server) *Registry {
	r := &Registry{servers: make(map[string]config.Server, len(servers))}
	for _, s := range servers {
		r.order = append(r.order, s.ID)
		r.servers[s.ID] = s
	}
	return r
}

func labelsOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func (r *Registry) List() []ServerSummary {
	out := make([]ServerSummary, 0, len(r.order))
	for _, id := range r.order {
		s := r.servers[id]
		out = append(out, ServerSummary{ID: s.ID, Name: s.Name, Labels: labelsOrEmpty(s.Labels), Enabled: true})
	}
	return out
}

func (r *Registry) Get(id string) (config.Server, bool) {
	s, ok := r.servers[id]
	return s, ok
}
```

- [ ] **Step 4: Write `client.go`**

```go
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"agentmon/hubd/internal/config"
	"agentmon/shared"
)

type Client struct{ HTTP *http.Client }

func NewClient(timeout time.Duration) *Client {
	return &Client{HTTP: &http.Client{Timeout: timeout}}
}

func (c *Client) Sessions(ctx context.Context, srv config.Server, target string) ([]shared.Session, error) {
	u := srv.URL + "/sessions"
	if target != "" {
		u += "?target=" + url.QueryEscape(target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+srv.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dial agent %s: %w", srv.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent %s returned %d", srv.ID, resp.StatusCode)
	}
	var list shared.SessionList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode agent %s sessions: %w", srv.ID, err)
	}
	out := make([]shared.Session, 0, len(list.Sessions))
	for _, s := range list.Sessions {
		s.Server = srv.ID // stamp the registry id; never trust the agent's self-report
		out = append(out, s)
	}
	return out, nil
}

func (c *Client) Health(ctx context.Context, srv config.Server) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
```

- [ ] **Step 5: Run** — `go test ./hubd/internal/registry/...` → PASS.

- [ ] **Step 6: Commit**

```bash
git add hubd/internal/registry
git commit -m "feat(m3): config server registry + hub→agent REST client (bearer, server-id stamp)"
```

---

## Task 11: Servers + audit REST handlers

**Files:**
- Create: `hubd/internal/api/servers.go`, `hubd/internal/api/audit.go`, `hubd/internal/api/servers_test.go`, `hubd/internal/api/audit_test.go`
- Modify: `hubd/internal/api/health_test.go` (assert `version`)

**Interfaces:**
- Consumes: `authn.PrincipalFrom`, `authz.Authorize`, `audit.Recorder`, `registry.Registry`, `registry.Client`, `db.AuditRepo`, `shared`.
- Produces:
  - `type Deps struct { Reg *registry.Registry; Agent *registry.Client; Audit *audit.Recorder; AuditRepo AuditReader; HealthTimeout time.Duration }` where `type AuditReader interface { Recent(ctx, limit int) ([]db.AuditEntry, error) }`.
  - `func (d Deps) ServersHandler() http.HandlerFunc` — `GET /api/v1/servers`: authorize `ServerView` on `server:*`; deny→audit+403; else `reg.List()` → 200 JSON array.
  - `func (d Deps) ServerHandler() http.HandlerFunc` — `GET /api/v1/servers/{id}`: authorize; `reg.Get(id)` → 404 if unknown; probe `agent.Health`; 200 `ServerDetail`.
  - `func (d Deps) AuditHandler() http.HandlerFunc` — `GET /api/v1/audit`: authorize `AuditRead`; `AuditRepo.Recent(ctx,100)`; 200 JSON array of `{id,principalId,action,resource,result,ts?}` (a browser-safe projection — no `ip`/`user_agent`/`meta`? keep `meta` out to avoid leaking; include id/principalId/action/resource/result).

The `authorizeOr403` helper (shared by handlers) lives in `servers.go`:
```go
func (d Deps) authorizeOr403(w http.ResponseWriter, r *http.Request, action authz.Action, resource string) (authz.Principal, bool)
```
returns the principal and `true` when allowed; on deny writes 403, audits via `d.Audit.Deny`, returns `false`.

- [ ] **Step 1: Write failing tests** `servers_test.go`

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
)

type nopSink struct{}

func (nopSink) Append(_ context.Context, _ db.AuditEntry) error { return nil }

// withPrincipal injects a principal as RequireAuth would, for direct handler tests.
func withPrincipal(r *http.Request, p authz.Principal) *http.Request {
	return r.WithContext(authn.ContextWithPrincipal(r.Context(), p))
}

func testDeps(reg *registry.Registry) Deps {
	return Deps{Reg: reg, Agent: registry.NewClient(time.Second),
		Audit: audit.NewRecorder(nopSink{}), HealthTimeout: time.Second}
}

func TestServersHandlerListsForAuthedPrincipal(t *testing.T) {
	reg := registry.New([]config.Server{{ID: "server-a", Name: "A", Token: "t", URL: "http://x"}})
	d := testDeps(reg)
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers", nil), authz.Principal{ID: "u1"})
	w := httptest.NewRecorder()
	d.ServersHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	var got []registry.ServerSummary
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 || got[0].ID != "server-a" {
		t.Fatalf("got %+v", got)
	}
}

func TestServersHandlerDeniesEmptyPrincipal(t *testing.T) {
	d := testDeps(registry.New(nil))
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers", nil), authz.Principal{})
	w := httptest.NewRecorder()
	d.ServersHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code %d", w.Code)
	}
}

func TestServerHandlerUnknownIDIs404(t *testing.T) {
	d := testDeps(registry.New(nil))
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/nope", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	d.ServerHandler()(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code %d", w.Code)
	}
}
```

This requires a helper `authn.ContextWithPrincipal` to inject a principal in tests. **Add to Task 7's `middleware.go`**: `func ContextWithPrincipal(ctx context.Context, p authz.Principal) context.Context { return context.WithValue(ctx, principalKey, p) }` (exported test seam; also used by the real middleware — refactor `RequireAuth` to call it). Note this as a back-edit to Task 7 if executing in order; since Task 7 precedes 11, add `ContextWithPrincipal` when writing Task 7.

`audit_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
)

type stubAudit struct{ rows []db.AuditEntry }

func (s stubAudit) Recent(_ context.Context, _ int) ([]db.AuditEntry, error) { return s.rows, nil }

func TestAuditHandlerReturnsRows(t *testing.T) {
	d := testDeps(nil)
	d.AuditRepo = stubAudit{rows: []db.AuditEntry{{ID: "a1", Action: "login.success", Result: "allow", Resource: "user:u1", PrincipalID: "u1"}}}
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/audit", nil), authz.Principal{ID: "u1"})
	w := httptest.NewRecorder()
	d.AuditHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	var got []map[string]string
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 || got[0]["action"] != "login.success" {
		t.Fatalf("got %+v", got)
	}
}

func TestAuditHandlerDeniesEmptyPrincipal(t *testing.T) {
	d := testDeps(nil)
	d.AuditRepo = stubAudit{}
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/audit", nil), authz.Principal{})
	w := httptest.NewRecorder()
	d.AuditHandler()(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code %d", w.Code)
	}
}
```

- [ ] **Step 2: Run to verify fail** — FAIL.

- [ ] **Step 3: Write `servers.go`**

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
)

type AuditReader interface {
	Recent(ctx context.Context, limit int) ([]db.AuditEntry, error)
}

type Deps struct {
	Reg           *registry.Registry
	Agent         *registry.Client
	Audit         *audit.Recorder
	AuditRepo     AuditReader
	HealthTimeout time.Duration
}

func (d Deps) authorizeOr403(w http.ResponseWriter, r *http.Request, action authz.Action, resource string) (authz.Principal, bool) {
	p, _ := authn.PrincipalFrom(r.Context())
	dec, err := authz.Authorize(r.Context(), p, action, resource)
	if err != nil || !dec.Allow {
		d.Audit.Deny(r.Context(), p.ID, action, resource, clientIP(r), r.UserAgent(), "")
		writeJSONError(w, http.StatusForbidden, "forbidden")
		return p, false
	}
	return p, true
}

func (d Deps) ServersHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.authorizeOr403(w, r, authz.ServerView, "server:*"); !ok {
			return
		}
		writeJSON(w, http.StatusOK, d.Reg.List())
	}
}

func (d Deps) ServerHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.ServerView, "server:"+id); !ok {
			return
		}
		srv, ok := d.Reg.Get(id)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), d.HealthTimeout)
		defer cancel()
		writeJSON(w, http.StatusOK, registry.ServerDetail{
			ID: srv.ID, Name: srv.Name, Labels: labelsOrEmpty(srv.Labels),
			Enabled: true, Healthy: d.Agent.Health(ctx, srv),
		})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}

func labelsOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
```

- [ ] **Step 4: Write `audit.go`**

```go
package api

import (
	"net/http"

	"agentmon/hubd/internal/authz"
)

func (d Deps) AuditHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.authorizeOr403(w, r, authz.AuditRead, "audit:*"); !ok {
			return
		}
		rows, err := d.AuditRepo.Recent(r.Context(), 100)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "audit read failed")
			return
		}
		out := make([]map[string]string, 0, len(rows))
		for _, e := range rows {
			out = append(out, map[string]string{
				"id": e.ID, "principalId": e.PrincipalID, "action": e.Action,
				"resource": e.Resource, "result": e.Result,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}
```

- [ ] **Step 5: Update `health_test.go`** to also assert `body["version"]` is present/non-empty (closes the M0 hygiene item).

- [ ] **Step 6: Run** — `go test ./hubd/internal/api/...` → PASS.

- [ ] **Step 7: Commit**

```bash
git add hubd/internal/api/servers.go hubd/internal/api/audit.go hubd/internal/api/servers_test.go hubd/internal/api/audit_test.go hubd/internal/api/health_test.go
git commit -m "feat(m3): GET /servers, /servers/{id}, /audit (authorize + audit denies)"
```

---

## Task 12: Sessions REST handlers

**Files:**
- Create: `hubd/internal/api/sessions.go`, `hubd/internal/api/sessions_test.go`

**Interfaces:**
- Produces (methods on `Deps`):
  - `func (d Deps) ServerSessionsHandler() http.HandlerFunc` — `GET /api/v1/servers/{id}/sessions`: authorize `SessionView` on `server:<id>`; `reg.Get(id)` 404 if unknown; `agent.Sessions(ctx, srv, "")`; agent error → 502; 200 JSON array of `shared.Session`.
  - `func (d Deps) SessionDetailHandler() http.HandlerFunc` — `GET /api/v1/servers/{id}/sessions/{name}`: same auth+lookup; fetch list; find by `Name`; 404 if absent; 200 single `shared.Session`.

- [ ] **Step 1: Write failing tests** `sessions_test.go`

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/registry"
	"agentmon/shared"
)

func fakeAgentSrv(t *testing.T, token string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"sessions":[{"name":"proj","server":"x","target":"default","cwd":"/p","command":"claude","windows":[]}]}`))
	}))
}

func depsWith(srv config.Server) Deps {
	d := testDeps(registry.New([]config.Server{srv}))
	d.Agent = registry.NewClient(2 * time.Second)
	return d
}

func TestServerSessionsReturnsStampedList(t *testing.T) {
	ts := fakeAgentSrv(t, "tok-a")
	defer ts.Close()
	d := depsWith(config.Server{ID: "server-a", URL: ts.URL, Token: "tok-a"})
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "server-a")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	var got []shared.Session
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 || got[0].Name != "proj" || got[0].Server != "server-a" {
		t.Fatalf("got %+v", got)
	}
}

func TestServerSessionsAgentErrorIs502(t *testing.T) {
	ts := fakeAgentSrv(t, "tok-a")
	defer ts.Close()
	d := depsWith(config.Server{ID: "server-a", URL: ts.URL, Token: "WRONG"}) // agent will 401
	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "server-a")
	w := httptest.NewRecorder()
	d.ServerSessionsHandler()(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("agent error must be 502, got %d", w.Code)
	}
}

func TestSessionDetailFoundAndNotFound(t *testing.T) {
	ts := fakeAgentSrv(t, "tok-a")
	defer ts.Close()
	d := depsWith(config.Server{ID: "server-a", URL: ts.URL, Token: "tok-a"})

	r := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions/proj", nil), authz.Principal{ID: "u1"})
	r.SetPathValue("id", "server-a")
	r.SetPathValue("name", "proj")
	w := httptest.NewRecorder()
	d.SessionDetailHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("found code %d", w.Code)
	}

	r2 := withPrincipal(httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions/ghost", nil), authz.Principal{ID: "u1"})
	r2.SetPathValue("id", "server-a")
	r2.SetPathValue("name", "ghost")
	w2 := httptest.NewRecorder()
	d.SessionDetailHandler()(w2, r2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("missing session must be 404, got %d", w2.Code)
	}
}
```

- [ ] **Step 2: Run to verify fail** — FAIL.

- [ ] **Step 3: Write `sessions.go`**

```go
package api

import (
	"net/http"

	"agentmon/hubd/internal/authz"
	"agentmon/shared"
)

func (d Deps) ServerSessionsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := d.authorizeOr403(w, r, authz.SessionView, "server:"+id); !ok {
			return
		}
		srv, ok := d.Reg.Get(id)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		sessions, err := d.Agent.Sessions(r.Context(), srv, "")
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			return
		}
		writeJSON(w, http.StatusOK, sessions)
	}
}

func (d Deps) SessionDetailHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		name := r.PathValue("name")
		if _, ok := d.authorizeOr403(w, r, authz.SessionView, shared.SessionID(id, "default", name)); !ok {
			return
		}
		srv, ok := d.Reg.Get(id)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		sessions, err := d.Agent.Sessions(r.Context(), srv, "")
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			return
		}
		for _, s := range sessions {
			if s.Name == name {
				writeJSON(w, http.StatusOK, s)
				return
			}
		}
		writeJSONError(w, http.StatusNotFound, "unknown session")
	}
}
```

- [ ] **Step 4: Run** — `go test ./hubd/internal/api/...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add hubd/internal/api/sessions.go hubd/internal/api/sessions_test.go
git commit -m "feat(m3): GET /servers/{id}/sessions[/{name}] via hub→agent bearer (502 on agent error)"
```

---

## Task 13: Router + main wiring + `set-password` CLI + server timeouts

**Files:**
- Create: `hubd/internal/api/router.go`, `hubd/internal/api/router_test.go`
- Modify: `hubd/cmd/agentmon-hubd/main.go`

**Interfaces:**
- Produces:
  - `type RouterDeps struct { Version string; Auth *authn.Authenticator; Login authn.LoginDeps; TrustForwardedProto bool; API Deps; WebUI http.Handler }`
  - `func NewRouter(rd RouterDeps) http.Handler` — assembles:
    - `GET /healthz` → `HealthHandler(version)` (unauthenticated)
    - `POST /api/v1/auth/login` → `Login.LoginHandler()` (unauthenticated; origin+rate-limit inside)
    - `POST /api/v1/auth/logout` → `Auth.RequireAuth(Auth.LogoutHandler(trust))`
    - `GET /api/v1/me` → `Auth.RequireAuth(Auth.MeHandler())`
    - `GET /api/v1/servers` → `RequireAuth(API.ServersHandler())`
    - `GET /api/v1/servers/{id}` → `RequireAuth(API.ServerHandler())`
    - `GET /api/v1/servers/{id}/sessions` → `RequireAuth(API.ServerSessionsHandler())`
    - `GET /api/v1/servers/{id}/sessions/{name}` → `RequireAuth(API.SessionDetailHandler())`
    - `GET /api/v1/audit` → `RequireAuth(API.AuditHandler())`
    - `/` → `WebUI` (SPA fallback)

- [ ] **Step 1: Write the failing test** `router_test.go` — assert wiring + that protected routes 401 without a cookie and `/healthz` is open.

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/registry"
)

func TestRouterProtectsAPIAndOpensHealthz(t *testing.T) {
	auth := &authn.Authenticator{Store: authn.NewStore(time.Hour), CookieName: "agentmon_session"}
	rd := RouterDeps{
		Version: "test", Auth: auth,
		API:    testDeps(registry.New([]config.Server{{ID: "server-a", Name: "A", Token: "t", URL: "http://x"}})),
		WebUI:  http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
	}
	h := NewRouter(rd)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatalf("healthz %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/servers", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("servers without cookie must be 401, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run to verify fail** — FAIL.

- [ ] **Step 3: Write `router.go`**

```go
package api

import (
	"net/http"

	"agentmon/hubd/internal/authn"
)

type RouterDeps struct {
	Version             string
	Auth                *authn.Authenticator
	Login               authn.LoginDeps
	TrustForwardedProto bool
	API                 Deps
	WebUI               http.Handler
}

func NewRouter(rd RouterDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", HealthHandler(rd.Version))

	mux.Handle("POST /api/v1/auth/login", rd.Login.LoginHandler())
	mux.Handle("POST /api/v1/auth/logout", rd.Auth.RequireAuth(rd.Auth.LogoutHandler(rd.TrustForwardedProto)))
	mux.Handle("GET /api/v1/me", rd.Auth.RequireAuth(rd.Auth.MeHandler()))

	mux.Handle("GET /api/v1/servers", rd.Auth.RequireAuth(rd.API.ServersHandler()))
	mux.Handle("GET /api/v1/servers/{id}", rd.Auth.RequireAuth(rd.API.ServerHandler()))
	mux.Handle("GET /api/v1/servers/{id}/sessions", rd.Auth.RequireAuth(rd.API.ServerSessionsHandler()))
	mux.Handle("GET /api/v1/servers/{id}/sessions/{name}", rd.Auth.RequireAuth(rd.API.SessionDetailHandler()))
	mux.Handle("GET /api/v1/audit", rd.Auth.RequireAuth(rd.API.AuditHandler()))

	mux.Handle("/", rd.WebUI)
	return mux
}
```

- [ ] **Step 4: Rewrite `main.go`** — wire DB/config/registry/authn/authz/audit/client/router; add the `user set-password` subcommand; add `http.Server` timeouts (ReadHeaderTimeout/ReadTimeout/IdleTimeout; **no global WriteTimeout** — M4's WS stream needs none, documented).

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"agentmon/hubd/internal/api"
	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
	"agentmon/hubd/internal/webui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "user" {
		if err := runUserCmd(os.Args[2:]); err != nil {
			log.Fatalf("user: %v", err)
		}
		return
	}
	cfgPath := flag.String("config", "/data/config.yaml", "path to config.yaml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	database, err := openDB(cfg)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	reg := registry.New(cfg.Servers)
	store := authn.NewStore(cookieTTL(cfg))
	auth := &authn.Authenticator{Store: store, CookieName: cfg.SessionCookie.Name}
	rec := audit.NewRecorder(database)

	router := api.NewRouter(api.RouterDeps{
		Version:             version,
		Auth:                auth,
		TrustForwardedProto: cfg.TrustForwardedProto,
		Login: authn.LoginDeps{
			Users: database, Store: store,
			Limiter:             authn.NewLimiter(rateMax(cfg), rateWindow(cfg)),
			Audit:               rec,
			CookieName:          cfg.SessionCookie.Name,
			CookieTTL:           cookieTTL(cfg),
			ExternalOrigin:      cfg.ExternalOrigin,
			TrustForwardedProto: cfg.TrustForwardedProto,
		},
		API: api.Deps{
			Reg: reg, Agent: registry.NewClient(10 * time.Second),
			Audit: rec, AuditRepo: database, HealthTimeout: 3 * time.Second,
		},
		WebUI: webui.Handler(),
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: M4's long-lived terminal WS relay must not be killed by
		// a global write deadline (it uses per-message deadlines instead).
	}
	log.Printf("agentmon-hubd %s listening on %s (%d servers)", version, cfg.Listen, len(cfg.Servers))
	log.Fatal(srv.ListenAndServe())
}

func openDB(cfg config.Config) (*db.DB, error) {
	dir := cfg.DataDir
	if dir == "" {
		dir = "."
	}
	return db.Open(dir + "/agentmon.sqlite")
}

func cookieTTL(cfg config.Config) time.Duration {
	if cfg.SessionCookie.TTL > 0 {
		return cfg.SessionCookie.TTL
	}
	return 168 * time.Hour
}
func rateMax(cfg config.Config) int {
	if cfg.LoginRateLimit.MaxAttempts > 0 {
		return cfg.LoginRateLimit.MaxAttempts
	}
	return 5
}
func rateWindow(cfg config.Config) time.Duration {
	if cfg.LoginRateLimit.Window > 0 {
		return cfg.LoginRateLimit.Window
	}
	return 15 * time.Minute
}

// runUserCmd implements: agentmon-hubd user set-password --username <u> [--display <d>] [--config <path>]
// The password is read from the AGENTMON_PASSWORD env var, or from stdin if unset.
func runUserCmd(args []string) error {
	if len(args) < 1 || args[0] != "set-password" {
		return fmt.Errorf("usage: agentmon-hubd user set-password --username <u>")
	}
	fs := flag.NewFlagSet("set-password", flag.ExitOnError)
	username := fs.String("username", "", "username")
	display := fs.String("display", "", "display name (defaults to username)")
	cfgPath := fs.String("config", "/data/config.yaml", "path to config.yaml")
	fs.Parse(args[1:])
	if *username == "" {
		return fmt.Errorf("--username is required")
	}
	pw := os.Getenv("AGENTMON_PASSWORD")
	if pw == "" {
		b, _ := io.ReadAll(os.Stdin)
		pw = strings.TrimRight(string(b), "\r\n")
	}
	if pw == "" {
		return fmt.Errorf("empty password (set AGENTMON_PASSWORD or pipe via stdin)")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	database, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer database.Close()
	hash, err := authn.HashPassword(pw)
	if err != nil {
		return err
	}
	dn := *display
	if dn == "" {
		dn = *username
	}
	if err := database.SetPassword(context.Background(), uuid.NewString(), *username, dn, hash); err != nil {
		return err
	}
	log.Printf("password set for user %q", *username)
	return nil
}
```

Note: `config.Load` requires resolvable secret refs even for `user set-password`. That's acceptable (the same env the server uses). If it proves annoying for first-run, a follow-up can add a `--db` flag bypassing config; out of scope for M3.

- [ ] **Step 5: Run** — `go test ./hubd/...` → PASS; `go build ./hubd/...` → green; `CGO_ENABLED=0 go build ./hubd/cmd/agentmon-hubd` → green.

- [ ] **Step 6: Commit**

```bash
git add hubd/internal/api/router.go hubd/internal/api/router_test.go hubd/cmd/agentmon-hubd/main.go
git commit -m "feat(m3): /api/v1 router, main wiring, user set-password CLI, server timeouts"
```

---

## Task 14: Integration test — httptest fake agent end-to-end through the hub

Exercises the **whole M3 path** with the real router and an in-process SQLite DB: set a password → login (cookie + csrf) → `GET /servers` → `GET /servers/{id}/sessions` against a fake agent → `GET /me` → `POST /logout`. Plus the failure paths the kickoff's Verify section names: bad/no agent token → 502, unauth → 401, origin reject → 403, rate-limit → 429.

**Files:**
- Create: `hubd/internal/api/integration_test.go`

- [ ] **Step 1: Write the integration test**

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
)

func buildHub(t *testing.T, agentURL, agentToken string) (http.Handler, *db.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "hub.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := authn.HashPassword("hunter2")
	if err := d.SetPassword(context.Background(), "u1", "patrik", "Patrik", hash); err != nil {
		t.Fatal(err)
	}
	store := authn.NewStore(time.Hour)
	auth := &authn.Authenticator{Store: store, CookieName: "agentmon_session"}
	rec := audit.NewRecorder(d)
	reg := registry.New([]config.Server{{ID: "server-a", Name: "A", URL: agentURL, Token: agentToken}})
	router := NewRouter(RouterDeps{
		Version: "test", Auth: auth,
		Login: authn.LoginDeps{Users: d, Store: store, Limiter: authn.NewLimiter(5, time.Minute),
			Audit: rec, CookieName: "agentmon_session", CookieTTL: time.Hour,
			ExternalOrigin: "https://agentmon.lan"},
		API:   Deps{Reg: reg, Agent: registry.NewClient(2 * time.Second), Audit: rec, AuditRepo: d, HealthTimeout: time.Second},
		WebUI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
	})
	return router, d
}

func login(t *testing.T, h http.Handler) (*http.Cookie, string) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"hunter2"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("login %d: %s", w.Code, w.Body)
	}
	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	return w.Result().Cookies()[0], body["csrfToken"]
}

func TestEndToEndLoginListSessionsLogout(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer agent-tok" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"sessions":[{"name":"proj","server":"x","target":"default","cwd":"/p","command":"claude","windows":[]}]}`))
	}))
	defer agent.Close()
	h, d := buildHub(t, agent.URL, "agent-tok")
	defer d.Close()

	cookie, csrf := login(t, h)

	// GET /servers
	req := httptest.NewRequest("GET", "/api/v1/servers", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "server-a") {
		t.Fatalf("servers: %d %s", w.Code, w.Body)
	}

	// GET /servers/server-a/sessions → stamped + project-labelled
	req = httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"proj"`) || !strings.Contains(w.Body.String(), `"server":"server-a"`) {
		t.Fatalf("sessions: %d %s", w.Code, w.Body)
	}

	// POST /logout requires CSRF
	req = httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", w.Code)
	}

	// audit shows the login.success
	rows, _ := d.Recent(context.Background(), 50)
	var sawLogin bool
	for _, e := range rows {
		if e.Action == "login.success" && e.PrincipalID == "u1" {
			sawLogin = true
		}
	}
	if !sawLogin {
		t.Fatal("login.success not audited")
	}
}

func TestEndToEndUnauthAndBadAgentToken(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer agent.Close()
	h, d := buildHub(t, agent.URL, "agent-tok") // registry token mismatches → agent 401
	defer d.Close()

	// unauth → 401
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/servers", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth servers %d", w.Code)
	}

	cookie, _ := login(t, h)
	req := httptest.NewRequest("GET", "/api/v1/servers/server-a/sessions", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("agent 401 → hub should 502, got %d", w.Code)
	}
}

func TestEndToEndOriginRejectAndRateLimit(t *testing.T) {
	h, d := buildHub(t, "http://unused", "x")
	defer d.Close()

	// origin reject
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"patrik","password":"hunter2"}`))
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("bad origin → 403, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run** — `go test ./hubd/internal/api/...` → PASS.

- [ ] **Step 3: Run the full suite** — `go test ./...` from repo root → all green.

- [ ] **Step 4: Build check** — `CGO_ENABLED=0 go build ./...` → green; `make build-hub` still restores the embed placeholder and builds.

- [ ] **Step 5: Optional local-agent smoke (dev box only, not CI).** If a local `tmux -L agentmon-smoke` socket + a local `agentmon-agent` is running, point a registry entry at it and `curl` the hub's `/api/v1/servers/{id}/sessions` after logging in. Document result in the carry-over; do not gate CI on it.

- [ ] **Step 6: Commit**

```bash
git add hubd/internal/api/integration_test.go
git commit -m "test(m3): httptest fake-agent end-to-end (login→servers→sessions→logout; 401/502/403 paths)"
```

---

## Self-review (completed against spec §4.2, §5, §6.1, §7.1, §7.3, §8 M3, §10, §13.3)

- **§4.2 authn** (login/logout/me, argon2id, cookie Secure via X-Forwarded-Proto, CSRF, edge principal): Tasks 2,3,7,8,9 ✓
- **§4.2 authz chokepoint** at every REST handler: Task 5 + `authorizeOr403` used by Tasks 11,12 ✓ (auth endpoints gate via RequireAuth)
- **§4.2 registry** from config at boot: Task 10, wired Task 13 ✓
- **§6.1 REST** `/servers`, `/servers/{id}`, `/servers/{id}/sessions`, `/servers/{id}/sessions/{name}`, `/me`, `/auth/login`, `/auth/logout`, `/audit`: Tasks 8,9,11,12,13 ✓
- **§6.1 hub→agent bearer**, server-id stamp, 502 on agent error: Tasks 10,12 ✓
- **§7.1 config** (external_origin, trust_forwarded_proto, cookie ttl, rate-limit, servers `*_ref`): consumed Tasks 1,13 ✓
- **§7.3 SQLite** users + audit_log actively used; registry config-driven: Tasks 6,13 ✓ (no new migration → `migrate()` txn item correctly stays deferred; documented in carry-over)
- **§10 security** (argon2id; no secrets to browser via `ServerSummary`/`ServerDetail`; CSRF; origin; rate-limit; audit login+denies; no keystrokes): Tasks 2,6,7,8,10,11 ✓
- **§13.3 auth bundle pulled forward**: full bundle present ✓
- **Pre-task** `resolveRef` hardening symmetric: Task 1 ✓
- **Carry-over hygiene folded:** repo_test `d, err :=` + health_test version assert (Tasks 6,11); `http.Server` timeouts (Task 13, WS-safe). The `migrate()` txn + `.dockerignore`/Dockerfile cache + `CapturePane` Runner seam remain deferred (no M3 trigger) — re-record in m3-carryover.

**Type consistency check:** `Deps`, `RouterDeps`, `LoginDeps`, `authz.Principal/Action`, `registry.Server*`, `authn.Store/Session/Limiter`, `audit.Recorder/Sink`, `db.SetPassword`, `ContextWithPrincipal`/`PrincipalFrom`, `shared.ResolveSecretRef`/`SessionID` — names are used identically across tasks. One back-edit noted: add `authn.ContextWithPrincipal` in Task 7 (used by Task 11/12 tests and refactored into `RequireAuth`).

**Deferred to M4 (not built here):** WS relay + directive minting; pong-liveness; the directive-minting wiring reminders in `m2-carryover.md`.
