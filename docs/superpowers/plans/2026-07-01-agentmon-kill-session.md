# Kill Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a destructive "kill tmux session" action reachable from a ⋯ overflow menu on the desktop sidebar, with a confirmation modal.

**Architecture:** Mirror the existing create/rename plumbing end-to-end — agent `POST /sessions/kill` (socket-scoped, no-shell `tmux kill-session`), hub `POST /api/v1/servers/{id}/sessions/kill` (`authz.SessionKill` + `session.kill` audit + CSRF), and a web `⋯` menu (Rename + Kill session…) + `KillSessionModal` on the desktop sidebar. Desktop only.

**Tech Stack:** Go 1.26 (`agent`, `hubd`, `shared` modules; CGO_ENABLED=0), gorilla/websocket unaffected. Web: Vite+React+TS, zustand, vitest + @testing-library/react. Tests run from each module dir (`cd hubd && go test …`) and `cd web && npx vitest run …`.

## Global Constraints

- Threat model: **single LAN operator, no adversary.** Guardrails prevent an accidental self-kill, not an attacker.
- **Socket scoping is non-negotiable:** the agent resolves the tmux socket from its own `cfg.ResolveTarget(target)`, NEVER from client input. Kill must be structurally unable to touch the default socket / session 0.
- No-shell exec only (arg-array `Runner`); name passed as a positional `-t` arg.
- Kill is **irreversible** → modal-confirmed; **no undo**.
- Kill the **whole tmux session** (all windows/panes), not a pane.
- **Desktop sidebar only.** The mobile inbox (`SessionList`) and grid tile header are unchanged.
- Every mutation is a **POST** with **CSRF** (via `RequireAuth`), audited on success, mirroring create/rename.
- HTTP success = **200** (symmetry with create/rename), body `{"name": "<session>"}`.
- Bound the agent handler with the Phase-5 `withTmuxTimeout(r)` (10s), like the other tmux handlers.
- Commit after each task. Do NOT push or deploy without owner confirmation.

---

## File Structure

- **Modify** `shared/session.go` — add `KillSessionRequest{ Name string }`.
- **Modify** `agent/internal/tmux/create.go` — add `KillSession` (reuses `ErrNoSession`/`isNoSession`).
- **Modify** `agent/internal/tmux/create_test.go` — `KillSession` argv + not-found test.
- **Modify** `agent/internal/api/sessions.go` — add `SessionKiller` seam + `KillSessionHandler`.
- **Modify** `agent/internal/api/sessions_test.go` — handler tests.
- **Modify** `agent/cmd/agentmon-agent/main.go` — `killSession` closure + `POST /sessions/kill` route.
- **Modify** `hubd/internal/authz/authz.go` — add `SessionKill` action.
- **Modify** `hubd/internal/audit/audit.go` — add `SessionKill` recorder method.
- **Modify** `hubd/internal/registry/client.go` — add `Client.KillSession`.
- **Modify** `hubd/internal/api/sessions.go` — add `ServerKillSessionHandler`.
- **Modify** `hubd/internal/api/sessions_test.go` — handler test.
- **Modify** `hubd/internal/api/router.go` — register the route.
- **Modify** `web/src/lib/api-client.ts` — add `killSession`.
- **Create** `web/src/components/KillSessionModal.tsx` + `.test.tsx`.
- **Create** `web/src/components/SessionActionsMenu.tsx` + `.test.tsx`.
- **Modify** `web/src/components/SessionNameEditor.tsx` — optional `autoEdit`/`onDone` (backwards-compatible).
- **Modify** `web/src/components/Sidebar.tsx` — use `SessionActionsMenu` in rows.

---

## Task 1: shared type + `tmux.KillSession`

**Files:**
- Modify: `shared/session.go`
- Modify: `agent/internal/tmux/create.go`
- Test: `agent/internal/tmux/create_test.go`

**Interfaces:**
- Consumes: existing `Runner`, `with`, `socketArgs`, `ErrNoSession`, `isNoSession` (all in the `tmux` package).
- Produces: `shared.KillSessionRequest{ Name string }`; `func KillSession(ctx context.Context, run Runner, socket, name string) error`.

- [ ] **Step 1: Add the shared request type**

In `shared/session.go`, after the `RenameSessionRequest` type, add:

```go
// KillSessionRequest is the body of POST /sessions/kill (agent) and
// POST /api/v1/servers/{id}/sessions/kill (hub). Name is an existing tmux
// session name on the target socket.
type KillSessionRequest struct {
	Name string `json:"name"`
}
```

- [ ] **Step 2: Write the failing tmux test**

In `agent/internal/tmux/create_test.go`, add (match the existing rename test's fake-Runner style in this file):

```go
func TestKillSessionArgvAndSuccess(t *testing.T) {
	var gotArgs []string
	run := func(_ context.Context, args ...string) ([]byte, error) { gotArgs = args; return nil, nil }
	if err := KillSession(context.Background(), run, "agentmon", "proj"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	want := []string{"-L", "agentmon", "kill-session", "-t", "proj"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v, want %v", gotArgs, want)
	}
}

func TestKillSessionNotFound(t *testing.T) {
	run := func(_ context.Context, _ ...string) ([]byte, error) {
		return nil, errors.New("can't find session: nope")
	}
	if err := KillSession(context.Background(), run, "", "nope"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("want ErrNoSession, got %v", err)
	}
}
```

Ensure `create_test.go` imports `context`, `errors`, `strings` (add any missing).

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /root/agentmon/agent && go test ./internal/tmux/ -run TestKillSession -v`
Expected: FAIL — `undefined: KillSession`.

- [ ] **Step 4: Implement `KillSession`**

In `agent/internal/tmux/create.go`, after `RenameSession`, add:

```go
// KillSession terminates the tmux session `name` on the socket via the arg-array
// Runner (no shell — the name is a positional -t arg). The socket is the agent's
// own configured socket, never client input, so this cannot target another socket.
// An unknown session → ErrNoSession (404). Kills the whole session (all windows).
func KillSession(ctx context.Context, run Runner, socket, name string) error {
	out, err := run(ctx, with(socketArgs(socket), "kill-session", "-t", name)...)
	if err != nil {
		errb := []byte(err.Error())
		if isNoSession(out) || isNoSession(errb) {
			return ErrNoSession
		}
		return fmt.Errorf("tmux kill-session: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/agent && go test ./internal/tmux/ -run TestKillSession -v && cd ../shared && go build ./...`
Expected: PASS; shared builds.

- [ ] **Step 6: Commit**

```bash
cd /root/agentmon
git add shared/session.go agent/internal/tmux/create.go agent/internal/tmux/create_test.go
git commit -m "feat(agent/tmux): add KillSession + shared.KillSessionRequest"
```

---

## Task 2: agent `KillSessionHandler` + route

**Files:**
- Modify: `agent/internal/api/sessions.go`
- Modify: `agent/cmd/agentmon-agent/main.go`
- Test: `agent/internal/api/sessions_test.go`

**Interfaces:**
- Consumes: `tmux.KillSession` (Task 1); `shared.KillSessionRequest`; existing `withTmuxTimeout`, `maxCreateBody`, `writeJSONError`, `config.Config.ResolveTarget`, `tmux.ErrNoSession`.
- Produces: `type SessionKiller func(ctx context.Context, socket, name string) error`; `func KillSessionHandler(cfg config.Config, kill SessionKiller) http.HandlerFunc`.

- [ ] **Step 1: Write the failing handler tests**

In `agent/internal/api/sessions_test.go`, add:

```go
func TestKillSessionHandlerSuccess(t *testing.T) {
	var gotSocket, gotName string
	kill := func(_ context.Context, socket, name string) error { gotSocket = socket; gotName = name; return nil }
	h := KillSessionHandler(testCfg(), kill)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/sessions/kill?target=default", strings.NewReader(`{"name":"proj"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("code %d body %s", rr.Code, rr.Body.String())
	}
	// testCfg's default target has socket_name "" — the agent's own configured socket,
	// never taken from client input.
	if gotSocket != "" || gotName != "proj" {
		t.Fatalf("kill got socket=%q name=%q", gotSocket, gotName)
	}
}

func TestKillSessionHandlerEmptyNameIs400(t *testing.T) {
	kill := func(context.Context, string, string) error { t.Fatal("must not exec"); return nil }
	h := KillSessionHandler(testCfg(), kill)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/sessions/kill?target=default", strings.NewReader(`{"name":""}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestKillSessionHandlerNotFoundIs404(t *testing.T) {
	kill := func(context.Context, string, string) error { return tmux.ErrNoSession }
	h := KillSessionHandler(testCfg(), kill)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/sessions/kill?target=default", strings.NewReader(`{"name":"gone"}`)))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestKillSessionHandlerUnknownTargetIs404(t *testing.T) {
	kill := func(context.Context, string, string) error { t.Fatal("must not exec"); return nil }
	h := KillSessionHandler(testCfg(), kill)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/sessions/kill?target=ghost", strings.NewReader(`{"name":"proj"}`)))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown target, got %d", rr.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/agent && go test ./internal/api/ -run TestKillSessionHandler -v`
Expected: FAIL — `undefined: KillSessionHandler`.

- [ ] **Step 3: Implement the handler + seam**

In `agent/internal/api/sessions.go`, after `RenameSessionHandler` (and its `SessionRenamer` type), add:

```go
// SessionKiller terminates an existing tmux session on the given socket. DI seam
// for KillSessionHandler (mirrors SessionRenamer): production binds tmux.KillSession
// + tmux.ExecRunner; tests inject a fake.
type SessionKiller func(ctx context.Context, socket, name string) error

// KillSessionHandler serves POST /sessions/kill?target=<label>. The body's `name`
// must be a non-empty existing tmux session name; the target resolves via config
// (the agent's own socket — never client-controlled). Maps tmux.ErrNoSession→404.
func KillSessionHandler(cfg config.Config, kill SessionKiller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxCreateBody)
		var req shared.KillSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "name is required")
			return
		}
		t, ok := cfg.ResolveTarget(r.URL.Query().Get("target"))
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown target")
			return
		}
		ctx, cancel := withTmuxTimeout(r)
		defer cancel()
		if err := kill(ctx, t.SocketName, req.Name); err != nil {
			if errors.Is(err, tmux.ErrNoSession) {
				writeJSONError(w, http.StatusNotFound, "no such session")
				return
			}
			log.Printf("sessions: kill failed (target=%q name=%q): %v", t.Label, req.Name, err)
			writeJSONError(w, http.StatusInternalServerError, "kill failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.CreateSessionResponse{Name: req.Name})
	}
}
```

- [ ] **Step 4: Wire the route in `main.go`**

In `agent/cmd/agentmon-agent/main.go`, after the `renameSession` closure (line ~75) add:

```go
	killSession := func(ctx context.Context, socket, name string) error {
		return tmux.KillSession(ctx, tmux.ExecRunner, socket, name)
	}
```

and after the `POST /sessions/rename` route registration add:

```go
	mux.Handle("POST /sessions/kill", api.RequireBearer(cfg.HubToken, api.KillSessionHandler(cfg, killSession)))
```

- [ ] **Step 5: Run tests + build**

Run: `cd /root/agentmon/agent && go test ./internal/api/ -run TestKillSessionHandler -v && go build ./...`
Expected: PASS (4 tests); clean build.

- [ ] **Step 6: Commit**

```bash
cd /root/agentmon
git add agent/internal/api/sessions.go agent/internal/api/sessions_test.go agent/cmd/agentmon-agent/main.go
git commit -m "feat(agent): POST /sessions/kill — socket-scoped tmux kill-session"
```

---

## Task 3: hub kill plumbing (authz + audit + client + handler + route)

**Files:**
- Modify: `hubd/internal/authz/authz.go`
- Modify: `hubd/internal/audit/audit.go`
- Modify: `hubd/internal/registry/client.go`
- Modify: `hubd/internal/api/sessions.go`
- Modify: `hubd/internal/api/router.go`
- Test: `hubd/internal/api/sessions_test.go`

**Interfaces:**
- Consumes: `shared.KillSessionRequest`; existing `Deps`, `authorizeOr403`, `maxCreateSessionBody`, `registry.ErrNoSession`, `registry.ErrInvalidSession`, `shared.SessionID`, `authn.ClientIP`.
- Produces: `authz.SessionKill Action = "session.kill"`; `(*audit.Recorder).SessionKill(ctx, principalID, resource, sessionName, ip, ua)`; `(*registry.Client).KillSession(ctx, srv, target, name)`; `(Deps).ServerKillSessionHandler()`.

- [ ] **Step 1: Write the failing hub handler tests**

In `hubd/internal/api/sessions_test.go`, add (mirror the existing rename handler tests' Deps/fake-agent setup already in this file):

```go
func TestServerKillSessionForwardsAndAudits(t *testing.T) {
	var gotName string
	agent := killFakeAgent(t, func(name string) (int, string) { gotName = name; return 200, `{"name":"proj"}` })
	defer agent.Close()
	sink := &recSink{}
	d := killDeps(agent.URL, sink) // helper below; wires a registry pointing at agent + audit sink
	rr := doKill(t, d, "aigallery", `{"name":"proj"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("code %d body %s", rr.Code, rr.Body.String())
	}
	if gotName != "proj" {
		t.Fatalf("agent saw name %q", gotName)
	}
	if !contains(sink.actions(), "session.kill") {
		t.Fatalf("session.kill not audited; saw %v", sink.actions())
	}
}

func TestServerKillSessionDenyEmptyPrincipalAudited(t *testing.T) {
	sink := &recSink{}
	d := killDeps("http://unused", sink)
	rr := doKillAs(t, d, authz.Principal{}, "aigallery", `{"name":"proj"}`) // empty principal → deny
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rr.Code)
	}
}

func TestServerKillSessionNotFoundMaps404(t *testing.T) {
	agent := killFakeAgent(t, func(string) (int, string) { return 404, `{"error":"no such session"}` })
	defer agent.Close()
	d := killDeps(agent.URL, &recSink{})
	rr := doKill(t, d, "aigallery", `{"name":"gone"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestServerKillSessionEmptyNameIs400(t *testing.T) {
	d := killDeps("http://unused", &recSink{})
	rr := doKill(t, d, "aigallery", `{"name":""}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}
```

Add these test helpers at the bottom of `sessions_test.go` (mirror the rename tests' existing registry/audit wiring — `testDeps`, `fakeStore`, `recSink`, `contains` already exist in this package from the relay/rename tests):

```go
// killFakeAgent serves POST /sessions/kill, returning (status, body) from decide(name).
func killFakeAgent(t *testing.T, decide func(name string) (int, string)) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions/kill", func(w http.ResponseWriter, r *http.Request) {
		var req shared.KillSessionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		code, body := decide(req.Name)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	})
	return httptest.NewServer(mux)
}

func killDeps(agentURL string, sink audit.Sink) Deps {
	srv := db.Server{ID: "aigallery", Name: "AG", URL: agentURL, Bearer: "b", Status: "active"}
	d := testDeps(registry.New(fakeStore{servers: map[string]db.Server{srv.ID: srv}}))
	d.Audit = audit.NewRecorder(sink)
	d.Agent = registry.NewClient(2 * time.Second)
	return d
}

func doKill(t *testing.T, d Deps, id, body string) *httptest.ResponseRecorder {
	return doKillAs(t, d, authz.Principal{ID: "u1"}, id, body)
}

func doKillAs(t *testing.T, d Deps, p authz.Principal, id, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/servers/{id}/sessions/kill", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.ServerKillSessionHandler()(w, r.WithContext(authn.ContextWithPrincipal(r.Context(), p)))
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/"+id+"/sessions/kill", strings.NewReader(body))
	mux.ServeHTTP(rr, req)
	return rr
}
```

(If `testDeps`/`fakeStore`/`recSink`/`contains` are defined in `ws_test.go` in the same package, they are already visible here — do not redefine. Add any missing imports: `time`, `encoding/json`, `net/http/httptest`, `strings`, `agentmon/hubd/internal/audit`, `.../authz`, `.../db`, `.../registry`, `agentmon/shared`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ -run TestServerKillSession -v`
Expected: FAIL — `undefined: ServerKillSessionHandler` (and `authz.SessionKill`, `Recorder.SessionKill`, `Client.KillSession`).

- [ ] **Step 3a: Add the authz action**

In `hubd/internal/authz/authz.go`, in the `const (...)` action block, after `SessionRename`, add:

```go
	SessionKill Action = "session.kill"
```

- [ ] **Step 3b: Add the audit recorder method**

In `hubd/internal/audit/audit.go`, after `SessionRename`, add:

```go
func (r *Recorder) SessionKill(ctx context.Context, principalID, resource, sessionName, ip, ua string) {
	meta, err := json.Marshal(map[string]string{"session": sessionName})
	if err != nil {
		meta = []byte("{}")
	}
	r.write(ctx, db.AuditEntry{PrincipalID: principalID, Action: "session.kill",
		Resource: resource, Result: "allow", IP: ip, UserAgent: ua, Meta: string(meta)})
}
```

- [ ] **Step 3c: Add the registry client method**

In `hubd/internal/registry/client.go`, after `RenameSession`, add:

```go
// KillSession terminates session `name` on the agent's target. Maps the agent's
// 400→ErrInvalidSession, 404→ErrNoSession.
func (c *Client) KillSession(ctx context.Context, srv db.Server, target, name string) error {
	u := srv.URL + "/sessions/kill"
	if target != "" {
		u += "?target=" + url.QueryEscape(target)
	}
	body, err := json.Marshal(shared.KillSessionRequest{Name: name})
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+srv.Bearer)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("dial agent %s: %w", srv.ID, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusBadRequest:
		return ErrInvalidSession
	case http.StatusNotFound:
		return ErrNoSession
	default:
		return fmt.Errorf("agent %s kill-session returned %d", srv.ID, resp.StatusCode)
	}
}
```

- [ ] **Step 3d: Add the hub handler**

In `hubd/internal/api/sessions.go`, after `ServerRenameSessionHandler`, add:

```go
// ServerKillSessionHandler handles POST /api/v1/servers/{id}/sessions/kill:
// authorizes session.kill, terminates the tmux session via the agent, audits, and
// returns 200 {"name": ...}. CSRF is enforced by RequireAuth. Maps the agent's
// 404 (no such session) / 400.
func (d Deps) ServerKillSessionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, ok := d.authorizeOr403(w, r, authz.SessionKill, "server:"+id)
		if !ok {
			return
		}
		var req shared.KillSessionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCreateSessionBody)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "name is required")
			return
		}
		srv, found, err := d.Reg.Get(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
		target := r.URL.Query().Get("target")
		auditTarget := target
		if auditTarget == "" {
			auditTarget = "default"
		}
		if err := d.Agent.KillSession(r.Context(), srv, target, req.Name); err != nil {
			switch {
			case errors.Is(err, registry.ErrNoSession):
				writeJSONError(w, http.StatusNotFound, "no such session")
			case errors.Is(err, registry.ErrInvalidSession):
				writeJSONError(w, http.StatusBadRequest, "invalid session request")
			default:
				log.Printf("kill-session: agent %s: %v", id, err)
				writeJSONError(w, http.StatusBadGateway, "agent unavailable")
			}
			return
		}
		d.Audit.SessionKill(r.Context(), p.ID, shared.SessionID(id, auditTarget, req.Name), req.Name,
			authn.ClientIP(r, d.TrustForwardedProto), r.UserAgent())
		writeJSON(w, http.StatusOK, map[string]string{"name": req.Name})
	}
}
```

- [ ] **Step 3e: Register the route**

In `hubd/internal/api/router.go`, after the `.../sessions/rename` route (line ~40), add:

```go
	mux.Handle("POST /api/v1/servers/{id}/sessions/kill", rd.Auth.RequireAuth(rd.API.ServerKillSessionHandler()))
```

- [ ] **Step 4: Run tests + build**

Run: `cd /root/agentmon/hubd && go test ./internal/api/ ./internal/authz/ ./internal/audit/ ./internal/registry/ -run 'TestServerKillSession|Kill' -v && go build ./...`
Expected: PASS (4 handler tests); clean build.

- [ ] **Step 5: Full hub suite (no regressions)**

Run: `cd /root/agentmon/hubd && go test ./... -race`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /root/agentmon
git add hubd/internal/authz/authz.go hubd/internal/audit/audit.go hubd/internal/registry/client.go hubd/internal/api/sessions.go hubd/internal/api/router.go hubd/internal/api/sessions_test.go
git commit -m "feat(hubd): POST /sessions/kill — authz.SessionKill + session.kill audit"
```

---

## Task 4: web `api-client.killSession` + `KillSessionModal`

**Files:**
- Modify: `web/src/lib/api-client.ts`
- Create: `web/src/components/KillSessionModal.tsx`
- Test: `web/src/components/KillSessionModal.test.tsx`

**Interfaces:**
- Consumes: existing `request` helper, `SessionState` type from `@/lib/contracts`, `Button` from `@/components/ui/button`.
- Produces: `killSession(serverId, name, target?) => Promise<void>`; `<KillSessionModal server name state onConfirm onClose />`.

- [ ] **Step 1: Add the api-client function**

In `web/src/lib/api-client.ts`, after `renameSession`, add:

```ts
// Kill (terminate) a tmux session. Irreversible; the caller confirms first.
// Auto-CSRF (mutating). 404 (already gone) is treated as success by the caller.
export const killSession = (serverId: string, name: string, target?: string) =>
  request<void>(
    "POST",
    `/servers/${encodeURIComponent(serverId)}/sessions/kill` +
      (target ? `?target=${encodeURIComponent(target)}` : ""),
    { name },
  );
```

- [ ] **Step 2: Write the failing modal test**

Create `web/src/components/KillSessionModal.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { KillSessionModal } from "./KillSessionModal";

describe("KillSessionModal", () => {
  it("names the session + host and confirms on Kill", async () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    render(<KillSessionModal server="aigallery" name="proj" state="idle" onConfirm={onConfirm} onClose={onClose} />);
    expect(screen.getByText(/proj/)).toBeInTheDocument();
    expect(screen.getByText(/aigallery/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /kill session/i }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("cancels without confirming", async () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    render(<KillSessionModal server="aigallery" name="proj" state="idle" onConfirm={onConfirm} onClose={onClose} />);
    await userEvent.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("warns when the agent is mid-task (working/blocked)", () => {
    const { rerender } = render(
      <KillSessionModal server="s" name="p" state="working" onConfirm={() => {}} onClose={() => {}} />,
    );
    expect(screen.getByText(/mid-task/i)).toBeInTheDocument();
    rerender(<KillSessionModal server="s" name="p" state="blocked" onConfirm={() => {}} onClose={() => {}} />);
    expect(screen.getByText(/mid-task/i)).toBeInTheDocument();
  });

  it("shows no mid-task warning for idle/done", () => {
    render(<KillSessionModal server="s" name="p" state="idle" onConfirm={() => {}} onClose={() => {}} />);
    expect(screen.queryByText(/mid-task/i)).toBeNull();
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/components/KillSessionModal.test.tsx`
Expected: FAIL — cannot resolve `./KillSessionModal`.

- [ ] **Step 4: Implement the modal**

Create `web/src/components/KillSessionModal.tsx`:

```tsx
import * as React from "react";
import type { SessionState } from "@/lib/contracts";
import { Button } from "@/components/ui/button";

interface Props {
  server: string;
  name: string;
  state: SessionState;
  onConfirm(): void;
  onClose(): void;
}

// Confirmation for the irreversible kill. Escape / backdrop / Cancel closes; the
// single Kill button confirms. When the session is mid-task (working/blocked) it
// adds a warning line — a nudge, never a block.
export function KillSessionModal({ server, name, state, onConfirm, onClose }: Props) {
  const midTask = state === "working" || state === "blocked";
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="dialog"
      aria-modal="true"
      aria-label="Kill session"
      onClick={onClose}
    >
      <div className="w-full max-w-sm rounded-lg border border-border bg-background p-4 shadow-lg" onClick={(e) => e.stopPropagation()}>
        <h2 className="text-base font-semibold">Kill session</h2>
        <p className="mt-2 text-sm text-muted-foreground">
          Terminate <span className="font-medium text-foreground">{name}</span> on{" "}
          <span className="font-medium text-foreground">{server}</span>? This ends the tmux session
          and everything running in it. This can’t be undone.
        </p>
        {midTask && (
          <p role="alert" className="mt-2 text-sm text-destructive">
            This agent is mid-task — killing it stops the agent.
          </p>
        )}
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button variant="destructive" onClick={onConfirm}>Kill session</Button>
        </div>
      </div>
    </div>
  );
}
```

(If `Button` has no `destructive` variant, use `className="bg-destructive text-destructive-foreground hover:bg-destructive/90"` on a default `Button` instead — check `web/src/components/ui/button.tsx` for the available variants and adapt.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/KillSessionModal.test.tsx`
Expected: PASS (4 tests).

- [ ] **Step 6: Commit**

```bash
cd /root/agentmon
git add web/src/lib/api-client.ts web/src/components/KillSessionModal.tsx web/src/components/KillSessionModal.test.tsx
git commit -m "feat(web): killSession api + KillSessionModal (state-aware confirm)"
```

---

## Task 5: web `SessionActionsMenu` (⋯) + Sidebar wiring

**Files:**
- Modify: `web/src/components/SessionNameEditor.tsx` (add optional `autoEdit`/`onDone`)
- Create: `web/src/components/SessionActionsMenu.tsx`
- Test: `web/src/components/SessionActionsMenu.test.tsx`
- Modify: `web/src/components/Sidebar.tsx`

**Interfaces:**
- Consumes: `SessionNameEditor` (Task target, extended), `KillSessionModal` (Task 4), `killSession` (Task 4), `usePanes` + `paneKey` (`@/store/panes`), `queryClient`, `SessionState`, `ApiError`.
- Produces: `<SessionActionsMenu serverId target name paneId state serverName />`.

- [ ] **Step 1: Extend `SessionNameEditor` for external triggering (backwards-compatible)**

In `web/src/components/SessionNameEditor.tsx`:

1. Add to `Props`: `autoEdit?: boolean;` and `onDone?: () => void;`.
2. Change the initial editing state to honor `autoEdit`:
   `const [editing, setEditing] = React.useState(!!autoEdit);`
3. In `cancel`, call `onDone` after closing:
   `const cancel = () => { setEditing(false); setError(null); onDone?.(); };`
4. In `save`, after `setEditing(false); onRenamed?.(value);` add `onDone?.();`.
5. In the non-editing branch, when `autoEdit` is set the parent controls visibility — render nothing so there is no stray name+pencil flash:
   at the top of the `if (!editing)` block: `if (autoEdit) return null;`

These are additive: existing callers (grid tile, mobile inbox) pass neither prop and are unaffected.

- [ ] **Step 2: Write the failing menu test**

Create `web/src/components/SessionActionsMenu.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SessionActionsMenu } from "./SessionActionsMenu";

vi.mock("@/lib/api-client", () => ({ killSession: vi.fn().mockResolvedValue(undefined), ApiError: class extends Error { status = 0; } }));
vi.mock("@/lib/query-client", () => ({ queryClient: { invalidateQueries: vi.fn() } }));

import { killSession } from "@/lib/api-client";

function row() {
  return { serverId: "aigallery", serverName: "AG", target: "default", name: "proj", paneId: "%0", state: "idle" as const };
}

describe("SessionActionsMenu", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows the name and a menu button, not an open menu", () => {
    render(<SessionActionsMenu {...row()} />);
    expect(screen.getByText("proj")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /session actions/i })).toBeInTheDocument();
    expect(screen.queryByText(/kill session/i)).toBeNull();
  });

  it("opens the menu, then the kill modal, and kills on confirm", async () => {
    render(<SessionActionsMenu {...row()} />);
    await userEvent.click(screen.getByRole("button", { name: /session actions/i }));
    await userEvent.click(screen.getByRole("menuitem", { name: /kill session/i }));
    // modal is up
    await userEvent.click(screen.getByRole("button", { name: /^kill session$/i }));
    expect(killSession).toHaveBeenCalledWith("aigallery", "proj", "default");
  });

  it("enters rename mode from the menu", async () => {
    render(<SessionActionsMenu {...row()} />);
    await userEvent.click(screen.getByRole("button", { name: /session actions/i }));
    await userEvent.click(screen.getByRole("menuitem", { name: /rename/i }));
    expect(screen.getByRole("textbox", { name: /new session name/i })).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /root/agentmon/web && npx vitest run src/components/SessionActionsMenu.test.tsx`
Expected: FAIL — cannot resolve `./SessionActionsMenu`.

- [ ] **Step 4: Implement `SessionActionsMenu`**

Create `web/src/components/SessionActionsMenu.tsx`:

```tsx
import * as React from "react";
import type { SessionState } from "@/lib/contracts";
import { SessionNameEditor } from "@/components/SessionNameEditor";
import { KillSessionModal } from "@/components/KillSessionModal";
import { killSession, ApiError } from "@/lib/api-client";
import { usePanes, paneKey } from "@/store/panes";
import { queryClient } from "@/lib/query-client";

interface Props {
  serverId: string;
  serverName: string;
  target: string;
  name: string;
  paneId: string;
  state: SessionState;
}

// Per-session ⋯ overflow menu on the desktop sidebar: Rename… (reuses the inline
// editor) and Kill session… (confirmation modal). Its controls stopPropagation so
// the menu lives inside the click-to-open row.
export function SessionActionsMenu({ serverId, serverName, target, name, paneId, state }: Props) {
  const [open, setOpen] = React.useState(false);
  const [mode, setMode] = React.useState<"idle" | "rename">("idle");
  const [killOpen, setKillOpen] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const ref = React.useRef<HTMLDivElement>(null);
  const stop = (e: React.SyntheticEvent) => e.stopPropagation();

  React.useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false); };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onDoc); document.removeEventListener("keydown", onKey); };
  }, [open]);

  if (mode === "rename") {
    return (
      <SessionNameEditor serverId={serverId} target={target} name={name} paneId={paneId} autoEdit onDone={() => setMode("idle")} />
    );
  }

  async function doKill() {
    if (busy) return;
    setBusy(true);
    try {
      await killSession(serverId, name, target);
    } catch (err) {
      // 404 = already gone → treat as success; other errors: keep the row, log.
      if (!(err instanceof ApiError && err.status === 404)) {
        setBusy(false);
        setKillOpen(false);
        return; // (a toast could go here; leaving the row is the safe fallback)
      }
    }
    // Drop the session from the list + close any open tile for it.
    usePanes.getState().closePane(paneKey(serverId, target, name, paneId));
    queryClient.invalidateQueries({ queryKey: ["sessions", serverId] });
    setBusy(false);
    setKillOpen(false);
  }

  return (
    <span className="inline-flex min-w-0 items-center gap-1" onClick={stop}>
      <span className="truncate">{name}</span>
      <div className="relative flex-none" ref={ref}>
        <button
          type="button"
          aria-label="Session actions"
          aria-haspopup="menu"
          onClick={(e) => { stop(e); setOpen((v) => !v); }}
          className="rounded p-0.5 text-muted-foreground opacity-60 hover:opacity-100"
        >
          ⋯
        </button>
        {open && (
          <div role="menu" className="absolute left-0 top-full z-20 mt-1 min-w-32 rounded-md border border-border bg-popover py-1 shadow-md">
            <button
              type="button"
              role="menuitem"
              onClick={(e) => { stop(e); setOpen(false); setMode("rename"); }}
              className="block w-full px-3 py-1.5 text-left text-sm hover:bg-accent"
            >
              Rename…
            </button>
            <button
              type="button"
              role="menuitem"
              onClick={(e) => { stop(e); setOpen(false); setKillOpen(true); }}
              className="block w-full px-3 py-1.5 text-left text-sm text-destructive hover:bg-accent"
            >
              Kill session…
            </button>
          </div>
        )}
      </div>
      {killOpen && (
        <KillSessionModal server={serverName} name={name} state={state} onConfirm={() => void doKill()} onClose={() => setKillOpen(false)} />
      )}
    </span>
  );
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/agentmon/web && npx vitest run src/components/SessionActionsMenu.test.tsx src/components/KillSessionModal.test.tsx`
Expected: PASS.

- [ ] **Step 6: Wire it into the Sidebar**

In `web/src/components/Sidebar.tsx`, replace the `<SessionNameEditor .../>` in the row (lines ~71-76) with:

```tsx
                  <SessionActionsMenu
                    serverId={row.server.id}
                    serverName={serverName}
                    target={row.session.target}
                    name={row.session.name}
                    paneId={row.pane.id}
                    state={stateOf(row)}
                  />
```

and update the import at the top: replace `import { SessionNameEditor } from "@/components/SessionNameEditor";` with `import { SessionActionsMenu } from "@/components/SessionActionsMenu";`.

- [ ] **Step 7: Full web suite + build**

Run: `cd /root/agentmon/web && npx vitest run && npm run build`
Expected: all tests pass; `tsc --noEmit && vite build` clean.

- [ ] **Step 8: Commit**

```bash
cd /root/agentmon
git add web/src/components/SessionNameEditor.tsx web/src/components/SessionActionsMenu.tsx web/src/components/SessionActionsMenu.test.tsx web/src/components/Sidebar.tsx
git commit -m "feat(web): ⋯ session actions menu (Rename + Kill) on the desktop sidebar"
```

---

## Task 6: Verification, review, and acceptance (no new code)

- [ ] **Step 1: Full workspace test + vet + CGO=0 build**

Run:
```bash
cd /root/agentmon
( cd hubd && go test ./... -race ) && ( cd agent && go test ./... -race ) && ( cd shared && go test ./... )
( cd hubd && go vet ./... ) && ( cd agent && go vet ./... )
( cd hubd && CGO_ENABLED=0 go build ./... ) && ( cd agent && CGO_ENABLED=0 go build ./... )
( cd web && npx vitest run && npm run build )
```
Expected: all PASS; vet clean; both binaries + web build.

- [ ] **Step 2: `/multi-review --codex` on the branch diff**

Invoke `multi-review` with `--codex` on the `feat-kill-session` branch diff. Apply real findings (regression test first), defer the rest with rationale in the carryover.

- [ ] **Step 3: SAFE live acceptance (throwaway socket only)**

```bash
tmux -L p6scratch new -d -s killme   # throwaway socket — NOT default, NOT agentmon
```
Build the agent to scratch, point a scratch hub at it (loopback, fresh DB), open the app, use the ⋯ → Kill session… on `killme`, confirm the modal, and verify `tmux -L p6scratch ls` no longer lists it. Then `tmux -L p6scratch kill-server` to clean up. Confirm the default socket (session 0) + the live `agentmon` socket are untouched. (If a headless browser isn't available, drive the hub route with curl against the scratch hub instead, and note that the React UI is owner-verified on-device.)

- [ ] **Step 4: Carryover + memory**

Write `docs/superpowers/kill-session-carryover.md` (what shipped, review outcome, acceptance, deploy note: BOTH an agent rebuild+restart AND a hub `docker compose up -d --build`). Update the memory index + status.

- [ ] **Step 5: Finish the branch**

Invoke `superpowers:finishing-a-development-branch`. **Deploy only after owner confirmation** — this touches BOTH halves (agent kill route + hub route/UI), so it needs an agent rebuild+restart AND a hub redeploy on the dedicated box; no DB migration.

---

## Self-Review (against the spec)

- **Spec coverage:** §2 agent → Tasks 1–2; §2 hub (authz/audit/client/handler/route) → Task 3; §3 web (api-client, modal, ⋯ menu, Sidebar) → Tasks 4–5; §4 scope/safety → socket-scoping in Task 1 + tests in Tasks 2–3; §6 testing/acceptance → Tasks 1–6; deploy note → Task 6. No gaps.
- **Placeholder scan:** every code step has complete code + exact commands; the two adapt-if-needed notes (Button `destructive` variant; reuse of existing `testDeps`/`fakeStore` helpers) name the exact file to check, not a vague TODO.
- **Type consistency:** `KillSession(ctx, run, socket, name)` / `SessionKiller(ctx, socket, name)` / `Client.KillSession(ctx, srv, target, name)` / `killSession(serverId, name, target?)` / `ServerKillSessionHandler` / `authz.SessionKill` / `Recorder.SessionKill` / `shared.KillSessionRequest{Name}` are used identically across tasks. The `⋯` menu passes `serverName` to the modal's `server` prop (host label) and `stateOf(row)` to `state`.
- **Scope:** one focused plan, 5 code tasks + 1 verification.
