# AgentMon Phase 1 · M0 — Contracts & Scaffold — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the AgentMon monorepo skeleton — three Go modules, a Vite/React SPA, shared contracts, SQLite migrations, embedded-SPA serving, and the multi-stage build/CI — so every later milestone has a green, testable foundation to build on.

**Architecture:** A Go multi-module workspace (`shared/`, `agent/`, `hubd/`) plus a `web/` Vite SPA. `shared/` holds the wire contracts both Go binaries import (via `replace` directives); the SPA mirrors the few DTOs by hand in TypeScript. `hubd` embeds the built SPA with `//go:embed` and serves it with SPA-fallback routing alongside `/api`. SQLite (pure-Go `modernc.org/sqlite`, `CGO_ENABLED=0`) is created from migrations at boot. The hub ships as one multi-stage container; the agent ships as a systemd binary.

**Tech Stack:** Go 1.23 (`net/http` + `chi`), `modernc.org/sqlite`, `gorilla/websocket` (later milestones); Vite + React + TypeScript, TanStack Router + Query, Zustand, `@xterm/xterm`; Docker (distroless), Caddy (TLS front), GitHub Actions CI.

## Global Constraints

*(Every task's requirements implicitly include this section — values copied verbatim from the spec.)*

- **Go 1.23**, all Go binaries build with **`CGO_ENABLED=0`** (static). SQLite driver is **`modernc.org/sqlite`** (pure Go) — never `mattn/go-sqlite3`.
- **Module paths:** `agentmon/shared`, `agentmon/agent`, `agentmon/hubd` (matches the spike's `agentmon/spike` convention). Cross-module use is via `replace … => ../…` so Docker builds work without relying on `go.work`.
- **Node 22** for the SPA build. SPA build output is `web/dist/`; it is copied into `hubd/internal/webui/dist/` for the embed.
- **Transport (locked):** binary WS frames for terminal data both directions; JSON text frames for control (`{"type":"resize","cols":…,"rows":…}`). Do not re-decide.
- **Security invariants (apply from day one):** agent tokens / signing keys are server-side only and never reach the SPA bundle; hub listener binds LAN/localhost; no public exposure; no raw keystroke logging.
- **Hub is one process** serving both `/api/v1` and the SPA from the same origin. No Node runtime in production.
- **Resource ID forms:** `server:<serverId>`, `target:<serverId>/<targetId>`, `session:<serverId>/<targetId>/<sessionName>`, `pane:<serverId>/<targetId>/<paneId>`, `user:<userId>`.
- This is M0 only. Do **not** implement discovery, terminal I/O, auth logic, the relay, or real UI here — those are M1–M5. M0 delivers a green skeleton with placeholder handlers.

---

## File structure (created in M0)

```text
agentmon/
├── go.work                          # dev workspace tying the 3 modules
├── Makefile                         # build-web, embed, build-hub/agent, test, lint, docker
├── README.md                        # repo overview + dev quickstart
├── shared/                          # module: agentmon/shared (wire contracts)
│   ├── go.mod
│   ├── ids.go                       # ResourceID builders/parsers
│   ├── ids_test.go
│   ├── session.go                   # Session/Window/Pane DTOs
│   ├── wsframe.go                   # JSON control-frame types (resize/error/reconnect)
│   ├── wsframe_test.go
│   ├── directive.go                 # Directive payload struct + canonical JSON
│   └── directive_test.go
├── agent/                           # module: agentmon/agent
│   ├── go.mod                       # replace agentmon/shared => ../shared
│   ├── cmd/agentmon-agent/main.go
│   └── internal/
│       ├── config/config.go         # agent.toml + env-ref resolution
│       ├── config/config_test.go
│       └── api/health.go            # GET /healthz
│       └── api/health_test.go
├── hubd/                            # module: agentmon/hubd
│   ├── go.mod                       # replace agentmon/shared => ../shared
│   ├── cmd/agentmon-hubd/main.go
│   └── internal/
│       ├── config/config.go         # config.yaml + env-ref resolution
│       ├── config/config_test.go
│       ├── api/health.go            # GET /healthz (+ /api/v1 router skeleton)
│       ├── api/health_test.go
│       ├── webui/embed.go           # //go:embed dist + SPA-fallback handler
│       ├── webui/embed_test.go
│       ├── webui/dist/index.html    # committed placeholder (real SPA overwrites at build)
│       └── db/
│           ├── db.go                # open + migrate (modernc.org/sqlite)
│           ├── db_test.go
│           ├── migrations/0001_init.sql
│           ├── migrations.go        # //go:embed migrations + runner
│           ├── repo.go              # repository interfaces
│           ├── users.go            # UserRepo impl
│           ├── audit.go            # AuditRepo impl
│           └── repo_test.go
├── web/                             # Vite + React + TS SPA
│   ├── package.json
│   ├── vite.config.ts               # /api + /ws dev proxy to hubd
│   ├── tsconfig.json
│   ├── index.html
│   └── src/
│       ├── main.tsx
│       ├── router.tsx               # TanStack Router (placeholder routes)
│       ├── routes/login.tsx         # placeholder
│       ├── routes/index.tsx         # placeholder home
│       └── lib/contracts.ts         # hand-mirrored TS DTOs
└── deploy/
    ├── Dockerfile                   # multi-stage hub image
    ├── docker-compose.yml
    ├── caddy.example.conf
    ├── agentmon-agent.service
    ├── install-agent.sh
    ├── hub.config.example.yaml
    └── agent.example.toml
└── .github/workflows/ci.yml
```

---

## Task 1: Repo skeleton & multi-module workspace

**Files:**
- Create: `go.work`, `shared/go.mod`, `agent/go.mod`, `hubd/go.mod`, `README.md`
- Create placeholder dirs via the files in later tasks.

**Interfaces:**
- Consumes: nothing.
- Produces: three buildable Go modules (`agentmon/shared`, `agentmon/agent`, `agentmon/hubd`) wired so `agent`/`hubd` can import `agentmon/shared`.

- [ ] **Step 1: Create the three `go.mod` files**

`shared/go.mod`:
```
module agentmon/shared

go 1.23
```

`agent/go.mod`:
```
module agentmon/agent

go 1.23

require agentmon/shared v0.0.0

replace agentmon/shared => ../shared
```

`hubd/go.mod`:
```
module agentmon/hubd

go 1.23

require agentmon/shared v0.0.0

replace agentmon/shared => ../shared
```

- [ ] **Step 2: Create `go.work`**

```
go 1.23

use (
	./shared
	./agent
	./hubd
)
```

- [ ] **Step 3: Create a minimal `README.md`**

```markdown
# AgentMon

Multi-server, mobile-first terminal dashboard for supervising AI coding agents.
See `agentmon-design.md` for the full design and `docs/superpowers/specs/` for phase specs.

## Layout
- `shared/` — wire contracts shared by hub and agent (Go module `agentmon/shared`)
- `agent/`  — per-server `agentmon-agent` (Go module `agentmon/agent`)
- `hubd/`   — central `agentmon-hubd` control plane (Go module `agentmon/hubd`)
- `web/`    — Vite + React SPA, embedded into `hubd`
- `deploy/` — Dockerfile, compose, Caddy, systemd unit
- `spike-0.5/` — throwaway validated input-fidelity spike (reference only)

## Dev quickstart
    make test          # Go unit tests across all modules
    make build         # build SPA + both Go binaries (CGO_ENABLED=0)
    cd web && npm run dev   # SPA dev server, proxies /api + /ws to a local hubd
```

- [ ] **Step 4: Add temporary placeholder `doc.go` files so empty modules build**

`shared/doc.go`:
```go
// Package shared holds the wire contracts shared by the AgentMon hub and agent.
package shared
```

Create `agent/internal/.gitkeep` and `hubd/internal/.gitkeep` (empty) so the dirs exist.

- [ ] **Step 5: Verify the workspace builds**

Run: `go work sync && go build ./... ` (from repo root)
Expected: no output, exit 0. (Empty modules build cleanly.)

- [ ] **Step 6: Commit**

```bash
git add go.work shared/go.mod shared/doc.go agent/go.mod hubd/go.mod README.md \
        agent/internal/.gitkeep hubd/internal/.gitkeep
git commit -m "chore(m0): multi-module Go workspace skeleton"
```

---

## Task 2: Shared contracts (`shared/`)

**Files:**
- Create: `shared/ids.go`, `shared/session.go`, `shared/wsframe.go`, `shared/directive.go`
- Test: `shared/ids_test.go`, `shared/wsframe_test.go`, `shared/directive_test.go`
- Delete: `shared/doc.go` (replaced by real files)

**Interfaces:**
- Consumes: nothing.
- Produces (imported by agent & hub in M1–M5):
  - `shared.ServerID(s string) string`, `shared.SessionID(server, target, name string) string`, `shared.PaneID(server, target, pane string) string`, `shared.UserID(id string) string`
  - `shared.ParsePaneID(rid string) (server, target, pane string, ok bool)`
  - `type Session struct{ Name, Server, Target, Cwd, Command string; Windows []Window }`, `type Window struct{ ID, Index, Name string; Panes []Pane }`, `type Pane struct{ ID, Command, Cwd string }`
  - `type ResizeFrame struct{ Type string; Cols, Rows int }`, `type ErrorFrame struct{ Type, Code, Message string }`, `type ReconnectFrame struct{ Type, Status string }`
  - `type Directive struct{ ServerID, Target, Resource, Mode, PrincipalID, Action, Exp, Nonce, RequestID string }` with `func (d Directive) CanonicalJSON() ([]byte, error)`

- [ ] **Step 1: Write the failing test for resource IDs**

`shared/ids_test.go`:
```go
package shared

import "testing"

func TestPaneIDRoundTrip(t *testing.T) {
	rid := PaneID("server-a", "default", "%3")
	if rid != "pane:server-a/default/%3" {
		t.Fatalf("got %q", rid)
	}
	srv, tgt, pane, ok := ParsePaneID(rid)
	if !ok || srv != "server-a" || tgt != "default" || pane != "%3" {
		t.Fatalf("parse: srv=%q tgt=%q pane=%q ok=%v", srv, tgt, pane, ok)
	}
}

func TestSessionAndServerID(t *testing.T) {
	if got := ServerID("server-a"); got != "server:server-a" {
		t.Fatalf("ServerID=%q", got)
	}
	if got := SessionID("server-a", "default", "api-refactor"); got != "session:server-a/default/api-refactor" {
		t.Fatalf("SessionID=%q", got)
	}
}

func TestParsePaneIDRejectsJunk(t *testing.T) {
	if _, _, _, ok := ParsePaneID("session:a/b/c"); ok {
		t.Fatal("expected non-pane resource to fail")
	}
	if _, _, _, ok := ParsePaneID("pane:a/b"); ok {
		t.Fatal("expected too-few-parts to fail")
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

Run: `go test ./shared/ -run TestPaneID -v`
Expected: FAIL — undefined: `PaneID`/`ParsePaneID`.

- [ ] **Step 3: Implement `shared/ids.go`**

```go
package shared

import "strings"

func ServerID(s string) string  { return "server:" + s }
func UserID(id string) string   { return "user:" + id }

func SessionID(server, target, name string) string {
	return "session:" + server + "/" + target + "/" + name
}

func PaneID(server, target, pane string) string {
	return "pane:" + server + "/" + target + "/" + pane
}

// ParsePaneID parses "pane:<server>/<target>/<paneId>". The paneId itself never
// contains '/', so we split the body into exactly 3 parts.
func ParsePaneID(rid string) (server, target, pane string, ok bool) {
	body, found := strings.CutPrefix(rid, "pane:")
	if !found {
		return "", "", "", false
	}
	parts := strings.SplitN(body, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
```

- [ ] **Step 4: Delete the placeholder and run the IDs test**

```bash
rm shared/doc.go
go test ./shared/ -run TestPaneID -v
go test ./shared/ -run TestSessionAndServerID -v
go test ./shared/ -run TestParsePaneIDRejectsJunk -v
```
Expected: PASS (all three).

- [ ] **Step 5: Add the DTOs in `shared/session.go`**

```go
package shared

// Session is the project-identifiable unit shown in every client surface.
// State is intentionally omitted in Phase 1 (hooks land in Phase 3).
type Session struct {
	Name    string   `json:"name"`
	Server  string   `json:"server"`
	Target  string   `json:"target"`
	Cwd     string   `json:"cwd"`
	Command string   `json:"command"`
	Windows []Window `json:"windows"`
}

type Window struct {
	ID    string `json:"id"`
	Index string `json:"index"`
	Name  string `json:"name"`
	Panes []Pane `json:"panes"`
}

type Pane struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Cwd     string `json:"cwd"`
}
```

- [ ] **Step 6: Add control frames in `shared/wsframe.go` + a round-trip test**

`shared/wsframe.go`:
```go
package shared

const (
	FrameResize    = "resize"
	FrameError     = "error"
	FrameReconnect = "reconnect"
)

type ResizeFrame struct {
	Type string `json:"type"` // "resize"
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type ErrorFrame struct {
	Type    string `json:"type"` // "error"
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ReconnectFrame struct {
	Type   string `json:"type"` // "reconnect"
	Status string `json:"status"`
}
```

`shared/wsframe_test.go`:
```go
package shared

import (
	"encoding/json"
	"testing"
)

func TestResizeFrameRoundTrip(t *testing.T) {
	in := ResizeFrame{Type: FrameResize, Cols: 88, Rows: 26}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out ResizeFrame
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip: %+v != %+v", out, in)
	}
	if string(b) != `{"type":"resize","cols":88,"rows":26}` {
		t.Fatalf("wire shape: %s", b)
	}
}
```

- [ ] **Step 7: Add the directive type in `shared/directive.go` + a canonical-JSON test**

`shared/directive.go`:
```go
package shared

import "encoding/json"

// Directive is the short-lived hub→agent access grant (HMAC-signed by the hub,
// verified by the agent). See Phase 1 spec §6.3. Mode is "ro" or "rw".
type Directive struct {
	ServerID    string `json:"serverId"`
	Target      string `json:"target"`
	Resource    string `json:"resource"` // e.g. pane:server-a/default/%3
	Mode        string `json:"mode"`
	PrincipalID string `json:"principalId"`
	Action      string `json:"action"`
	Exp         string `json:"exp"`   // RFC3339
	Nonce       string `json:"nonce"`
	RequestID   string `json:"requestId"`
}

// CanonicalJSON is the exact byte sequence that gets HMAC'd. Field order is fixed
// by the struct definition; both hub (sign) and agent (verify) must use this.
func (d Directive) CanonicalJSON() ([]byte, error) { return json.Marshal(d) }
```

`shared/directive_test.go`:
```go
package shared

import "testing"

func TestDirectiveCanonicalJSONStable(t *testing.T) {
	d := Directive{
		ServerID: "server-a", Target: "default",
		Resource: "pane:server-a/default/%3", Mode: "rw",
		PrincipalID: "user_1", Action: "terminal.write",
		Exp: "2026-06-27T10:32:00Z", Nonce: "n1", RequestID: "req_1",
	}
	a, err := d.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := d.CanonicalJSON()
	if string(a) != string(b) {
		t.Fatal("canonical JSON not stable across calls")
	}
	want := `{"serverId":"server-a","target":"default","resource":"pane:server-a/default/%3","mode":"rw","principalId":"user_1","action":"terminal.write","exp":"2026-06-27T10:32:00Z","nonce":"n1","requestId":"req_1"}`
	if string(a) != want {
		t.Fatalf("canonical shape changed:\n got %s\nwant %s", a, want)
	}
}
```

- [ ] **Step 8: Run the whole shared suite**

Run: `go test ./shared/...`
Expected: PASS (ok agentmon/shared).

- [ ] **Step 9: Commit**

```bash
git add shared/
git commit -m "feat(m0): shared wire contracts (ids, session, wsframe, directive)"
```

---

## Task 3: Agent skeleton — config + `/healthz`

**Files:**
- Create: `agent/internal/config/config.go`, `agent/internal/api/health.go`, `agent/cmd/agentmon-agent/main.go`
- Test: `agent/internal/config/config_test.go`, `agent/internal/api/health_test.go`
- Delete: `agent/internal/.gitkeep`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces:
  - `config.Load(path string) (config.Config, error)` where `type Config struct{ Listen, ServerID, HubToken, DirectiveKey string; ScrollbackLines int; Targets []Target }` and `type Target struct{ OSUser, SocketName, Label string }`. Secret fields resolve `env:NAME` / `file:/path` refs to their values.
  - `api.HealthHandler(serverID, version string) http.HandlerFunc` returning `{"ok":true,"version":…,"serverId":…,"tmuxAvailable":…}`.

- [ ] **Step 1: Write the failing config test**

`agent/internal/config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesEnvRefs(t *testing.T) {
	t.Setenv("AGENTMON_AGENT_TOKEN", "secret-token")
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	os.WriteFile(p, []byte(`
listen = "10.0.0.5:8377"
server_id = "server-a"
hub_token = "env:AGENTMON_AGENT_TOKEN"
directive_key = "literal-key"
scrollback_lines = 4000
[[targets]]
  os_user = "dev"
  socket_name = ""
  label = "default"
`), 0o600)

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HubToken != "secret-token" {
		t.Fatalf("env ref not resolved: %q", cfg.HubToken)
	}
	if cfg.DirectiveKey != "literal-key" {
		t.Fatalf("literal mangled: %q", cfg.DirectiveKey)
	}
	if cfg.ServerID != "server-a" || cfg.ScrollbackLines != 4000 {
		t.Fatalf("bad cfg: %+v", cfg)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].Label != "default" {
		t.Fatalf("targets: %+v", cfg.Targets)
	}
}

func TestLoadMissingEnvRefErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.toml")
	os.WriteFile(p, []byte(`
listen = "x"
server_id = "s"
hub_token = "env:DEFINITELY_NOT_SET_AGENTMON"
directive_key = "k"
`), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unset env ref")
	}
}
```

- [ ] **Step 2: Add the TOML dependency and run the test (expect fail)**

```bash
cd agent && go get github.com/BurntSushi/toml@v1.4.0 && cd ..
go test ./agent/internal/config/ -v
```
Expected: FAIL — undefined: `Load`.

- [ ] **Step 3: Implement `agent/internal/config/config.go`**

```go
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type Target struct {
	OSUser     string `toml:"os_user"`
	SocketName string `toml:"socket_name"`
	Label      string `toml:"label"`
}

type Config struct {
	Listen          string   `toml:"listen"`
	ServerID        string   `toml:"server_id"`
	HubToken        string   `toml:"hub_token"`
	DirectiveKey    string   `toml:"directive_key"`
	ScrollbackLines int      `toml:"scrollback_lines"`
	Targets         []Target `toml:"targets"`
}

func Load(path string) (Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if c.ScrollbackLines == 0 {
		c.ScrollbackLines = 5000
	}
	for _, p := range []*string{&c.HubToken, &c.DirectiveKey} {
		v, err := resolveRef(*p)
		if err != nil {
			return Config{}, err
		}
		*p = v
	}
	return c, nil
}

// resolveRef expands "env:NAME" and "file:/path" secret references; any other
// value is returned literally.
func resolveRef(v string) (string, error) {
	switch {
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
		return v, nil
	}
}
```

- [ ] **Step 4: Run the config tests, verify they pass**

Run: `go test ./agent/internal/config/ -v`
Expected: PASS (both tests).

- [ ] **Step 5: Write the failing healthz test**

`agent/internal/api/health_test.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandler(t *testing.T) {
	h := HealthHandler("server-a", "test")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var body struct {
		OK       bool   `json:"ok"`
		ServerID string `json:"serverId"`
		Version  string `json:"version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.ServerID != "server-a" || body.Version != "test" {
		t.Fatalf("bad body: %+v", body)
	}
}
```

- [ ] **Step 6: Run it, verify it fails**

Run: `go test ./agent/internal/api/ -v`
Expected: FAIL — undefined: `HealthHandler`.

- [ ] **Step 7: Implement `agent/internal/api/health.go`**

```go
package api

import (
	"encoding/json"
	"net/http"
	"os/exec"
)

func HealthHandler(serverID, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tmuxErr := exec.LookPath("tmux")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":            true,
			"version":       version,
			"serverId":      serverID,
			"tmuxAvailable": tmuxErr == nil,
		})
	}
}
```

- [ ] **Step 8: Run it, verify it passes**

Run: `go test ./agent/internal/api/ -v`
Expected: PASS.

- [ ] **Step 9: Wire `agent/cmd/agentmon-agent/main.go`**

```go
package main

import (
	"flag"
	"log"
	"net/http"

	"agentmon/agent/internal/api"
	"agentmon/agent/internal/config"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.HealthHandler(cfg.ServerID, version))

	log.Printf("agentmon-agent %s listening on %s (server %s)", version, cfg.Listen, cfg.ServerID)
	log.Fatal(http.ListenAndServe(cfg.Listen, mux))
}
```

- [ ] **Step 10: Build the agent binary**

```bash
rm -f agent/internal/.gitkeep
CGO_ENABLED=0 go build -o /tmp/agentmon-agent ./agent/cmd/agentmon-agent
echo $?
```
Expected: prints `0` (static build succeeds).

- [ ] **Step 11: Commit**

```bash
git add agent/
git commit -m "feat(m0): agent skeleton (config + /healthz)"
```

---

## Task 4: Hub skeleton — config + `/healthz` + embedded SPA + SPA fallback

**Files:**
- Create: `hubd/internal/config/config.go`, `hubd/internal/api/health.go`, `hubd/internal/webui/embed.go`, `hubd/internal/webui/dist/index.html`, `hubd/cmd/agentmon-hubd/main.go`
- Test: `hubd/internal/config/config_test.go`, `hubd/internal/api/health_test.go`, `hubd/internal/webui/embed_test.go`
- Delete: `hubd/internal/.gitkeep`

**Interfaces:**
- Consumes: nothing from other tasks (registry of `Server` is config-only).
- Produces:
  - `config.Load(path string) (config.Config, error)` with `type Config struct{ Listen, ExternalOrigin, DataDir string; TrustForwardedProto bool; SessionCookie CookieCfg; LoginRateLimit RateLimitCfg; Servers []Server }`, `type Server struct{ ID, Name, URL, Token, SigningKey string; Labels []string }` (Token/SigningKey resolved from `*_ref`).
  - `webui.Handler() http.Handler` — serves embedded assets with SPA fallback.
  - `api.HealthHandler(version string) http.HandlerFunc`.

- [ ] **Step 1: Write the failing hub config test**

`hubd/internal/config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesServerSecretRefs(t *testing.T) {
	t.Setenv("SRVA_TOKEN", "tok-a")
	t.Setenv("SRVA_KEY", "key-a")
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
listen: "127.0.0.1:8080"
external_origin: "https://agentmon.lan"
trust_forwarded_proto: true
data_dir: "/data"
servers:
  - id: server-a
    name: server-a
    url: "http://10.0.0.5:8377"
    token_ref: "env:SRVA_TOKEN"
    signing_key_ref: "env:SRVA_KEY"
`), 0o600)

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("servers: %+v", cfg.Servers)
	}
	s := cfg.Servers[0]
	if s.Token != "tok-a" || s.SigningKey != "key-a" {
		t.Fatalf("secret refs not resolved: %+v", s)
	}
	if !cfg.TrustForwardedProto || cfg.ExternalOrigin != "https://agentmon.lan" {
		t.Fatalf("bad cfg: %+v", cfg)
	}
}
```

- [ ] **Step 2: Add the YAML dep and run the test (expect fail)**

```bash
cd hubd && go get gopkg.in/yaml.v3@v3.0.1 && cd ..
go test ./hubd/internal/config/ -v
```
Expected: FAIL — undefined: `Load`.

- [ ] **Step 3: Implement `hubd/internal/config/config.go`**

```go
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type CookieCfg struct {
	Name string        `yaml:"name"`
	TTL  time.Duration `yaml:"ttl"`
}

type RateLimitCfg struct {
	MaxAttempts int           `yaml:"max_attempts"`
	Window      time.Duration `yaml:"window"`
}

type Server struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	URL           string   `yaml:"url"`
	TokenRef      string   `yaml:"token_ref"`
	SigningKeyRef string   `yaml:"signing_key_ref"`
	Labels        []string `yaml:"labels"`
	// resolved at load:
	Token      string `yaml:"-"`
	SigningKey string `yaml:"-"`
}

type Config struct {
	Listen              string       `yaml:"listen"`
	ExternalOrigin      string       `yaml:"external_origin"`
	TrustForwardedProto bool         `yaml:"trust_forwarded_proto"`
	DataDir             string       `yaml:"data_dir"`
	SessionCookie       CookieCfg    `yaml:"session_cookie"`
	LoginRateLimit      RateLimitCfg `yaml:"login_rate_limit"`
	Servers             []Server     `yaml:"servers"`
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.SessionCookie.Name == "" {
		c.SessionCookie.Name = "agentmon_session"
	}
	for i := range c.Servers {
		tok, err := resolveRef(c.Servers[i].TokenRef)
		if err != nil {
			return Config{}, fmt.Errorf("server %s token: %w", c.Servers[i].ID, err)
		}
		key, err := resolveRef(c.Servers[i].SigningKeyRef)
		if err != nil {
			return Config{}, fmt.Errorf("server %s signing_key: %w", c.Servers[i].ID, err)
		}
		c.Servers[i].Token = tok
		c.Servers[i].SigningKey = key
	}
	return c, nil
}

func resolveRef(v string) (string, error) {
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
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return v, nil
	}
}
```

- [ ] **Step 4: Run the hub config test, verify it passes**

Run: `go test ./hubd/internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Create the committed placeholder SPA so the embed compiles**

`hubd/internal/webui/dist/index.html`:
```html
<!doctype html>
<html><head><meta charset="utf-8"><title>AgentMon</title></head>
<body><div id="root">AgentMon hub — SPA not yet built (M0 placeholder).</div></body>
</html>
```

- [ ] **Step 6: Write the failing SPA-fallback test**

`hubd/internal/webui/embed_test.go`:
```go
package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSPAFallbackServesIndexForDeepLink(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	// A nested client route that doesn't exist as a file must return index.html.
	resp, err := http.Get(srv.URL + "/servers/server-a/sessions/foo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	buf := new(strings.Builder)
	io := make([]byte, 4096)
	n, _ := resp.Body.Read(io)
	buf.Write(io[:n])
	if !strings.Contains(buf.String(), "AgentMon") {
		t.Fatalf("deep link did not return index.html: %q", buf.String())
	}
}

func TestSPAFallbackDoesNotSwallowAPIPaths(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/v1/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown /api path, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 7: Run it, verify it fails**

Run: `go test ./hubd/internal/webui/ -v`
Expected: FAIL — undefined: `Handler`.

- [ ] **Step 8: Implement `hubd/internal/webui/embed.go`**

```go
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var assets embed.FS

// Handler serves the embedded SPA with SPA-fallback routing: any path that is
// not an existing static asset and is not under /api returns index.html, so
// client-side deep links survive a hard refresh. /api paths are passed through
// to a 404 here (the real /api router is mounted ahead of this in main).
func Handler() http.Handler {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err != nil {
			// Not a real asset → serve the SPA entrypoint.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 9: Run it, verify it passes**

Run: `go test ./hubd/internal/webui/ -v`
Expected: PASS (both tests).

- [ ] **Step 10: Add hub healthz (mirror the agent test pattern)**

`hubd/internal/api/health.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
)

func HealthHandler(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version})
	}
}
```

`hubd/internal/api/health_test.go`:
```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHubHealthHandler(t *testing.T) {
	rr := httptest.NewRecorder()
	HealthHandler("test")(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
```

Run: `go test ./hubd/internal/api/ -v`
Expected: PASS.

- [ ] **Step 11: Wire `hubd/cmd/agentmon-hubd/main.go`**

```go
package main

import (
	"flag"
	"log"
	"net/http"

	"agentmon/hubd/internal/api"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/webui"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/data/config.yaml", "path to config.yaml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.HealthHandler(version))
	// /api/v1 routes are added in M3/M4. The SPA handler is the catch-all.
	mux.Handle("/", webui.Handler())

	log.Printf("agentmon-hubd %s listening on %s (%d servers)", version, cfg.Listen, len(cfg.Servers))
	log.Fatal(http.ListenAndServe(cfg.Listen, mux))
}
```

- [ ] **Step 12: Build the hub statically (embed compiles)**

```bash
rm -f hubd/internal/.gitkeep
CGO_ENABLED=0 go build -o /tmp/agentmon-hubd ./hubd/cmd/agentmon-hubd
echo $?
```
Expected: `0`.

- [ ] **Step 13: Commit (force-add the placeholder dist past .gitignore)**

```bash
git add hubd/
git add -f hubd/internal/webui/dist/index.html
git commit -m "feat(m0): hub skeleton (config + /healthz + embedded SPA fallback)"
```

---

## Task 5: SQLite migrations + repositories

**Files:**
- Create: `hubd/internal/db/db.go`, `hubd/internal/db/migrations.go`, `hubd/internal/db/migrations/0001_init.sql`, `hubd/internal/db/repo.go`, `hubd/internal/db/users.go`, `hubd/internal/db/audit.go`
- Test: `hubd/internal/db/db_test.go`, `hubd/internal/db/repo_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `db.Open(path string) (*db.DB, error)` — opens SQLite (modernc), runs migrations, returns a handle.
  - `type User struct{ ID, Username, DisplayName, PasswordHash, Status string }`
  - `db.UserRepo` with `CreateUser(ctx, User) error`, `GetUserByUsername(ctx, string) (User, error)`
  - `type AuditEntry struct{ ID, PrincipalID, Action, Resource, Result, RequestID, IP, UserAgent, Meta string }`
  - `db.AuditRepo` with `Append(ctx, AuditEntry) error`, `Recent(ctx, limit int) ([]AuditEntry, error)`
  - `*db.DB` satisfies both `UserRepo` and `AuditRepo`.

- [ ] **Step 1: Write the migration SQL (full Phase 1+ schema, parent §7.2)**

`hubd/internal/db/migrations/0001_init.sql`:
```sql
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT UNIQUE NOT NULL,
  display_name TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS servers (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  url TEXT NOT NULL,
  token_ref TEXT NOT NULL,
  labels TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tmux_targets (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id),
  os_user TEXT NOT NULL,
  socket_name TEXT,
  label TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  UNIQUE(server_id, os_user, socket_name)
);

CREATE TABLE IF NOT EXISTS session_state_events (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id),
  target_id TEXT,
  tmux_session_name TEXT NOT NULL,
  tmux_pane_id TEXT,
  source TEXT NOT NULL,
  raw_event TEXT NOT NULL,
  derived_state TEXT NOT NULL,
  payload TEXT,
  event_ts TEXT NOT NULL,
  received_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS principal_seen (
  principal_id TEXT NOT NULL,
  server_id TEXT NOT NULL,
  target_id TEXT NOT NULL DEFAULT '',
  tmux_session_name TEXT NOT NULL,
  last_seen_event_id TEXT,
  last_focused_at TEXT NOT NULL,
  PRIMARY KEY(principal_id, server_id, target_id, tmux_session_name)
);

CREATE TABLE IF NOT EXISTS audit_log (
  id TEXT PRIMARY KEY,
  principal_id TEXT,
  action TEXT NOT NULL,
  resource TEXT NOT NULL,
  result TEXT NOT NULL,
  request_id TEXT,
  ip TEXT,
  user_agent TEXT,
  meta TEXT,
  ts TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts);
CREATE INDEX IF NOT EXISTS idx_state_events_session
  ON session_state_events(server_id, tmux_session_name, event_ts);
```

- [ ] **Step 2: Write the failing migration test**

`hubd/internal/db/db_test.go`:
```go
package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenRunsMigrations(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.sqlite")
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// All Phase 1+ tables must exist after Open.
	want := []string{"users", "servers", "tmux_targets",
		"session_state_events", "principal_seen", "audit_log"}
	for _, tbl := range want {
		var name string
		err := d.sql.QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", tbl, err)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.sqlite")
	d1, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	d1.Close()
	d2, err := Open(p) // re-open must not fail on already-applied migrations
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	d2.Close()
}
```

- [ ] **Step 3: Add the sqlite dep and run the test (expect fail)**

```bash
cd hubd && go get modernc.org/sqlite@v1.34.1 && cd ..
go test ./hubd/internal/db/ -run TestOpen -v
```
Expected: FAIL — undefined: `Open`.

- [ ] **Step 4: Implement `hubd/internal/db/migrations.go` and `db.go`**

`hubd/internal/db/migrations.go`:
```go
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrate applies every embedded *.sql file in lexical order, tracking applied
// files in a schema_migrations table so re-runs are idempotent.
func migrate(ctx context.Context, sqldb *sql.DB) error {
	if _, err := sqldb.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var seen string
		err := sqldb.QueryRowContext(ctx, `SELECT name FROM schema_migrations WHERE name=?`, name).Scan(&seen)
		if err == nil {
			continue // already applied
		}
		if err != sql.ErrNoRows {
			return err
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := sqldb.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := sqldb.ExecContext(ctx,
			`INSERT INTO schema_migrations(name, applied_at) VALUES(?, datetime('now'))`, name); err != nil {
			return err
		}
	}
	return nil
}
```

`hubd/internal/db/db.go`:
```go
package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the SQLite database at path, enables WAL, and
// runs migrations. The driver is pure-Go modernc.org/sqlite (CGO_ENABLED=0).
func Open(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqldb.SetMaxOpenConns(1) // SQLite single-writer; keeps WAL well-behaved on the volume
	if err := migrate(context.Background(), sqldb); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{sql: sqldb}, nil
}

func (d *DB) Close() error { return d.sql.Close() }
```

- [ ] **Step 5: Run the migration tests, verify they pass**

Run: `go test ./hubd/internal/db/ -run TestOpen -v`
Expected: PASS (both).

- [ ] **Step 6: Define repo interfaces and the failing repo test**

`hubd/internal/db/repo.go`:
```go
package db

import "context"

type User struct {
	ID           string
	Username     string
	DisplayName  string
	PasswordHash string
	Status       string
}

type AuditEntry struct {
	ID          string
	PrincipalID string
	Action      string
	Resource    string
	Result      string
	RequestID   string
	IP          string
	UserAgent   string
	Meta        string
}

type UserRepo interface {
	CreateUser(ctx context.Context, u User) error
	GetUserByUsername(ctx context.Context, username string) (User, error)
}

type AuditRepo interface {
	Append(ctx context.Context, e AuditEntry) error
	Recent(ctx context.Context, limit int) ([]AuditEntry, error)
}
```

`hubd/internal/db/repo_test.go`:
```go
package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestUserRepoRoundTrip(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	defer d.Close()
	ctx := context.Background()

	in := User{ID: "u1", Username: "patrik", DisplayName: "Patrik",
		PasswordHash: "$argon2id$...", Status: "active"}
	if err := d.CreateUser(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetUserByUsername(ctx, "patrik")
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Fatalf("got %+v want %+v", got, in)
	}
}

func TestAuditAppendAndRecent(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	defer d.Close()
	ctx := context.Background()

	if err := d.Append(ctx, AuditEntry{ID: "a1", PrincipalID: "u1",
		Action: "terminal.open", Resource: "pane:server-a/default/%3", Result: "allow"}); err != nil {
		t.Fatal(err)
	}
	rows, err := d.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Action != "terminal.open" {
		t.Fatalf("recent: %+v", rows)
	}
}
```

- [ ] **Step 7: Run it, verify it fails**

Run: `go test ./hubd/internal/db/ -run 'TestUserRepo|TestAudit' -v`
Expected: FAIL — `d.CreateUser` undefined.

- [ ] **Step 8: Implement `users.go` and `audit.go`**

`hubd/internal/db/users.go`:
```go
package db

import "context"

func (d *DB) CreateUser(ctx context.Context, u User) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO users(id, username, display_name, password_hash, status, created_at, updated_at)
		 VALUES(?,?,?,?,?, datetime('now'), datetime('now'))`,
		u.ID, u.Username, u.DisplayName, u.PasswordHash, u.Status)
	return err
}

func (d *DB) GetUserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	err := d.sql.QueryRowContext(ctx,
		`SELECT id, username, display_name, password_hash, status FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Status)
	return u, err
}
```

`hubd/internal/db/audit.go`:
```go
package db

import "context"

func (d *DB) Append(ctx context.Context, e AuditEntry) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO audit_log(id, principal_id, action, resource, result, request_id, ip, user_agent, meta, ts)
		 VALUES(?,?,?,?,?,?,?,?,?, datetime('now'))`,
		e.ID, e.PrincipalID, e.Action, e.Resource, e.Result, e.RequestID, e.IP, e.UserAgent, e.Meta)
	return err
}

func (d *DB) Recent(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, principal_id, action, resource, result, request_id, ip, user_agent, meta
		 FROM audit_log ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.PrincipalID, &e.Action, &e.Resource, &e.Result,
			&e.RequestID, &e.IP, &e.UserAgent, &e.Meta); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 9: Run the repo tests, verify they pass**

Run: `go test ./hubd/internal/db/ -v`
Expected: PASS (all four tests).

- [ ] **Step 10: Verify the whole hub module still builds static**

Run: `CGO_ENABLED=0 go build ./hubd/... && echo OK`
Expected: `OK`.

- [ ] **Step 11: Commit**

```bash
git add hubd/internal/db/
git commit -m "feat(m0): sqlite migrations + user/audit repos (modernc, CGO_ENABLED=0)"
```

---

## Task 6: Web SPA scaffold + dev proxy + contracts mirror

**Files:**
- Create: `web/package.json`, `web/tsconfig.json`, `web/vite.config.ts`, `web/index.html`, `web/src/main.tsx`, `web/src/router.tsx`, `web/src/routes/index.tsx`, `web/src/routes/login.tsx`, `web/src/lib/contracts.ts`

**Interfaces:**
- Consumes: the Go `shared` DTOs (mirrored by hand in `contracts.ts` — keep names identical).
- Produces: a SPA that `npm run build`s to `web/dist/`, and a dev server that proxies `/api` + `/ws` to `hubd` on `127.0.0.1:8080`.

- [ ] **Step 1: Create `web/package.json`**

```json
{
  "name": "agentmon-web",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview",
    "typecheck": "tsc -b --noEmit"
  },
  "dependencies": {
    "@tanstack/react-query": "^5.59.0",
    "@tanstack/react-router": "^1.58.0",
    "@xterm/addon-fit": "^0.10.0",
    "@xterm/xterm": "^5.5.0",
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "zustand": "^5.0.0"
  },
  "devDependencies": {
    "@types/react": "^18.3.0",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.0",
    "typescript": "^5.6.0",
    "vite": "^5.4.0"
  }
}
```

- [ ] **Step 2: Create `web/tsconfig.json`**

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "useDefineForClassFields": true,
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noEmit": true,
    "skipLibCheck": true,
    "esModuleInterop": true
  },
  "include": ["src"]
}
```

- [ ] **Step 3: Create `web/vite.config.ts` with the dev proxy**

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev: proxy API + WS to a locally running hubd. Prod: this dev server is unused;
// `vite build` emits dist/ which hubd embeds and serves same-origin.
export default defineConfig({
  plugins: [react()],
  build: { outDir: "dist" },
  server: {
    proxy: {
      "/api": { target: "http://127.0.0.1:8080", changeOrigin: true },
      "/ws": { target: "ws://127.0.0.1:8080", ws: true },
    },
  },
});
```

- [ ] **Step 4: Create `web/index.html` and the React entry**

`web/index.html`:
```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover" />
    <title>AgentMon</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

`web/src/main.tsx`:
```tsx
import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { router } from "./router";

const queryClient = new QueryClient();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>,
);
```

- [ ] **Step 5: Create the placeholder router + routes**

`web/src/router.tsx`:
```tsx
import { createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";
import { HomeRoute } from "./routes/index";
import { LoginRoute } from "./routes/login";

const rootRoute = createRootRoute({ component: () => <Outlet /> });
const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: HomeRoute });
const loginRoute = createRoute({ getParentRoute: () => rootRoute, path: "/login", component: LoginRoute });

const routeTree = rootRoute.addChildren([indexRoute, loginRoute]);
export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register { router: typeof router; }
}
```

`web/src/routes/index.tsx`:
```tsx
export function HomeRoute() {
  return <main><h1>AgentMon</h1><p>Session list lands in M5.</p></main>;
}
```

`web/src/routes/login.tsx`:
```tsx
export function LoginRoute() {
  return <main><h1>AgentMon — Login</h1><p>Login form lands in M5 (auth in M3).</p></main>;
}
```

- [ ] **Step 6: Create the TS contracts mirror**

`web/src/lib/contracts.ts`:
```ts
// Hand-mirrored from Go module `agentmon/shared`. Keep names/shapes in sync.
export interface Pane { id: string; command: string; cwd: string; }
export interface Window { id: string; index: string; name: string; panes: Pane[]; }
export interface Session {
  name: string; server: string; target: string;
  cwd: string; command: string; windows: Window[];
}
// WS control frames (client → hub). Terminal data uses binary frames, not these.
export interface ResizeFrame { type: "resize"; cols: number; rows: number; }
export interface ErrorFrame { type: "error"; code: string; message: string; }
export interface ReconnectFrame { type: "reconnect"; status: string; }
```

- [ ] **Step 7: Install and build the SPA**

```bash
cd web && npm install && npm run build && cd ..
ls web/dist/index.html
```
Expected: `npm run build` completes; `web/dist/index.html` exists.

- [ ] **Step 8: Commit (dist/ stays gitignored; node_modules ignored)**

```bash
git add web/package.json web/package-lock.json web/tsconfig.json web/vite.config.ts \
        web/index.html web/src/
git commit -m "feat(m0): vite/react SPA scaffold + dev proxy + TS contracts mirror"
```

---

## Task 7: Build glue — Makefile, multi-stage Dockerfile, deploy assets, CI

**Files:**
- Create: `Makefile`, `deploy/Dockerfile`, `deploy/docker-compose.yml`, `deploy/caddy.example.conf`, `deploy/agentmon-agent.service`, `deploy/install-agent.sh`, `deploy/hub.config.example.yaml`, `deploy/agent.example.toml`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: the build commands proven in Tasks 3–6.
- Produces: `make build` (SPA → embed → both static binaries), `make test`, and a green CI pipeline + a buildable hub image.

- [ ] **Step 1: Create the `Makefile`**

```makefile
.PHONY: test build build-web build-hub build-agent embed docker clean

test:
	go test ./...

build-web:
	cd web && npm ci && npm run build

# Copy the built SPA where //go:embed expects it (overwrites the placeholder).
embed: build-web
	rm -rf hubd/internal/webui/dist
	cp -r web/dist hubd/internal/webui/dist

build-hub: embed
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
		-o bin/agentmon-hubd ./hubd/cmd/agentmon-hubd

build-agent:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
		-o bin/agentmon-agent ./agent/cmd/agentmon-agent

build: build-hub build-agent

docker:
	docker build -f deploy/Dockerfile -t agentmon-hubd:dev .

clean:
	rm -rf bin web/dist hubd/internal/webui/dist
	git checkout -- hubd/internal/webui/dist/index.html
```

- [ ] **Step 2: Create the multi-stage `deploy/Dockerfile` (parent §16.1)**

```dockerfile
# ---- Stage 1: build the SPA ----
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- Stage 2: build hubd with the SPA embedded ----
FROM golang:1.23-alpine AS hubd
WORKDIR /src
COPY . /src
COPY --from=web /web/dist /src/hubd/internal/webui/dist
WORKDIR /src/hubd
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/agentmon-hubd ./cmd/agentmon-hubd

# ---- Stage 3: minimal runtime ----
FROM gcr.io/distroless/static-debian12
COPY --from=hubd /out/agentmon-hubd /agentmon-hubd
VOLUME ["/data"]
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/agentmon-hubd"]
CMD ["--config", "/data/config.yaml"]
```

- [ ] **Step 3: Create `deploy/docker-compose.yml`**

```yaml
services:
  agentmon-hub:
    build:
      context: ..
      dockerfile: deploy/Dockerfile
    image: agentmon-hubd:dev
    restart: unless-stopped
    ports:
      - "127.0.0.1:8080:8080"   # behind Caddy on the host; bind LAN in real deploys
    volumes:
      - agentmon-data:/data
volumes:
  agentmon-data:
```

- [ ] **Step 4: Create `deploy/caddy.example.conf`**

```caddyfile
# Caddy terminates TLS with a real cert and reverse-proxies to hubd (plain HTTP).
# It forwards X-Forwarded-* (incl. Proto) and upgrades WebSockets by default.
agentmon.example.lan {
	reverse_proxy 127.0.0.1:8080
}
```

- [ ] **Step 5: Create `deploy/agentmon-agent.service` and `deploy/install-agent.sh`**

`deploy/agentmon-agent.service`:
```ini
[Unit]
Description=AgentMon agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/agentmon-agent --config /etc/agentmon/agent.toml
User=dev
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

`deploy/install-agent.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail
# Minimal M0 installer: drop the binary + unit, leave config to the operator.
BIN_SRC="${1:-bin/agentmon-agent}"
install -m 0755 "$BIN_SRC" /usr/local/bin/agentmon-agent
install -d /etc/agentmon
install -m 0644 deploy/agentmon-agent.service /etc/systemd/system/agentmon-agent.service
echo "Installed. Create /etc/agentmon/agent.toml (see deploy/agent.example.toml), then:"
echo "  systemctl daemon-reload && systemctl enable --now agentmon-agent"
```

- [ ] **Step 6: Create the example configs**

`deploy/hub.config.example.yaml`:
```yaml
listen: "127.0.0.1:8080"
external_origin: "https://agentmon.example.lan"
trust_forwarded_proto: true
data_dir: "/data"
session_cookie: { name: "agentmon_session", ttl: "168h" }
login_rate_limit: { max_attempts: 5, window: "15m" }
servers:
  - id: "server-a"
    name: "server-a"
    url: "http://10.0.0.5:8377"
    token_ref: "env:AGENTMON_SRVA_TOKEN"
    signing_key_ref: "env:AGENTMON_SRVA_SIGNKEY"
  - id: "server-b"
    name: "server-b"
    url: "http://10.0.0.6:8377"
    token_ref: "env:AGENTMON_SRVB_TOKEN"
    signing_key_ref: "env:AGENTMON_SRVB_SIGNKEY"
```

`deploy/agent.example.toml`:
```toml
listen      = "10.0.0.5:8377"
server_id   = "server-a"
hub_token   = "env:AGENTMON_AGENT_TOKEN"
directive_key = "env:AGENTMON_AGENT_SIGNKEY"
scrollback_lines = 5000
[[targets]]
  os_user = "dev"
  socket_name = ""
  label = "default"
```

- [ ] **Step 7: Create `.github/workflows/ci.yml`**

```yaml
name: ci
on:
  push: { branches: [main] }
  pull_request:
jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.23" }
      - name: Unit tests (all modules)
        run: go test ./...
      - name: Static build check (CGO_ENABLED=0)
        run: |
          CGO_ENABLED=0 go build ./agent/...
          CGO_ENABLED=0 go vet ./shared/... ./agent/...
  web:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: "22" }
      - run: cd web && npm ci && npm run build
  docker:
    runs-on: ubuntu-latest
    needs: [go, web]
    steps:
      - uses: actions/checkout@v4
      - name: Build hub image (embeds SPA, static hubd)
        run: docker build -f deploy/Dockerfile -t agentmon-hubd:ci .
```

> Note: CI does **not** run tmux/Claude-dependent tests (no tmux on the runner). Those are M1+ integration tests run on the dev box. The `go test ./...` here is the pure unit suite.

- [ ] **Step 8: Make the installer executable and run the full local build**

```bash
chmod +x deploy/install-agent.sh
make test
make build
ls -la bin/agentmon-hubd bin/agentmon-agent
```
Expected: `make test` passes; `make build` produces both binaries; the embedded `hubd` contains the real SPA (not the placeholder).

- [ ] **Step 9: Verify the Docker image builds**

```bash
make docker
```
Expected: image `agentmon-hubd:dev` builds through all three stages.

- [ ] **Step 10: Restore the placeholder dist so the tree is clean for commit**

```bash
git checkout -- hubd/internal/webui/dist/index.html 2>/dev/null || true
git status --short
```
Expected: only the new `deploy/`, `Makefile`, `.github/` files are untracked/modified (no `bin/`, no rebuilt `dist/`).

- [ ] **Step 11: Commit**

```bash
git add Makefile deploy/ .github/
git commit -m "feat(m0): build glue — Makefile, multi-stage Dockerfile, deploy assets, CI"
```

---

## M0 Definition of Done

- [ ] `make test` is green (shared + agent + hub unit suites).
- [ ] `make build` produces static `bin/agentmon-hubd` (real SPA embedded) and `bin/agentmon-agent`.
- [ ] `make docker` builds the multi-stage hub image.
- [ ] CI workflow runs `go test ./...`, the SPA build, the `CGO_ENABLED=0` checks, and the Docker build.
- [ ] `agentmon-hubd` serves the SPA with working **deep-link refresh** (nested route → `index.html`) and `GET /healthz`; unknown `/api/*` → 404.
- [ ] `agentmon-agent` answers `GET /healthz` with `tmuxAvailable`.
- [ ] SQLite migrations create the full schema on a fresh DB and re-open idempotently.
- [ ] No secrets, binaries, or build output committed (`.gitignore` holds).

*(Next: M1 plan — agent tmux discovery + REST — written once M0 is green.)*

---

## Self-review notes (author)

- **Spec coverage (M0 portion of §8):** monorepo ✓(T1); shared contracts ✓(T2); config formats ✓(T3/T4); full SQLite migrations + repo interfaces ✓(T5); config registry loader ✓(T4 `config.Servers`); Vite→hubd dev proxy ✓(T6); CI incl. multi-stage image + `//go:embed` + `CGO_ENABLED=0` ✓(T7); SPA-fallback + deep-link verify ✓(T4). Discovery/IO/auth/relay/UI correctly **excluded** (M1–M5).
- **Placeholder scan:** no TBD/TODO; every code step shows complete code; commands have expected output.
- **Type consistency:** `config.Load`/`Config`/`Server`/`Target` names match across T3/T4 and the example configs in T7; `shared` DTO names (`Session`/`Window`/`Pane`/`ResizeFrame`) match the TS mirror in T6; `db.Open`/`UserRepo`/`AuditRepo`/`User`/`AuditEntry` names match across T5 steps; module paths `agentmon/{shared,agent,hubd}` consistent in go.mod files and imports.
