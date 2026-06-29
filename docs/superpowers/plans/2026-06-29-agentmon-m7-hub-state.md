# M7 Hub State Aggregation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the agent-side Claude state M6 exposes into the hub supervision data plane: proactively ingest state transitions, store them as events, derive current state, project per-principal "seen", roll up to session/server dots, and serve it via the public payload + SSE + the terminal-WS state frame.

**Architecture:** The agent gains an additive `GET /state` exposing per-pane state + `transitionSeq`/`doneSeq` counters + the `$TMUX` epoch (transport decision **B**). A hub background poller polls each active server's `/state` (~3s), writes `session_state_events` on every transition (incl. `done→done` via `doneSeq`), and maintains an in-memory current-state projection. A pub/sub broadcaster feeds `GET /api/v1/events` (SSE) and the terminal-WS `{t:"state"}` frame. `POST /api/v1/seen` records `principal_seen`; a `SeenProject` helper masks `done→idle` per principal. All seen comparisons use a single (hub) clock.

**Tech Stack:** Go (stdlib `net/http`, `database/sql`), `modernc.org/sqlite` (CGO-free), `github.com/gorilla/websocket`, `github.com/google/uuid`. React/TanStack on the web side is **M8** — out of scope here.

**Spec:** `docs/superpowers/specs/2026-06-29-agentmon-m7-hub-state-design.md` (read it; this plan implements it).

## Global Constraints

- **Go-only milestone.** No web/`contracts.ts` changes (M8 territory). Touch only `shared/`, `agent/`, `hubd/`.
- **Pure-Go SQLite.** `CGO_ENABLED=0 go build ./...` must pass; `go test ./... -race` green; `go vet ./...` clean.
- **Additive & backward-compatible.** `GET /state` is additive; an un-upgraded agent returns 404 and the hub must degrade to snapshot-diffing `/sessions`. New JSON fields are additive (`omitempty` where a zero value would mislead M8).
- **Single clock for seen.** `received_at` (events) and `last_focused_at` (`principal_seen`) are BOTH stamped by the hub via one helper `hubTS(t time.Time)` = `t.UTC().Format("2006-01-02 15:04:05.000")`. Never compare against the agent-supplied `event_ts`. String comparison of this fixed format is chronological.
- **State vocabulary** is `shared.State` (`blocked>done>working>idle>unknown`) and `shared.RollUp` — reuse, never redefine.
- **FK is enforced** (`foreign_keys(1)`): every `session_state_events.server_id` must be a real active server id (the registry always provides one).
- **Auth invariants:** hub→agent calls carry `Authorization: Bearer <srv.Bearer>` (agent side: `api.RequireBearer(cfg.HubToken, …)`). Public hub endpoints go through `rd.Auth.RequireAuth` (CSRF enforced on POST). `/events` and `/seen` both call `authorizeOr403`.
- **Safety (live work only):** never touch `~/.claude/settings.json`, session 0 / the default tmux socket, the `agentmon` demo panes, or prod `deploy/data`. Tests use `t.TempDir()` + httptest + fakes only.

---

## File Structure

**New files**
- `shared/agentstate.go` — `PaneState`, `AgentState` wire types (agent `/state` response; consumed by agent api + registry client + poller).
- `agent/internal/api/state.go` (+ `state_test.go`) — `StateHandler` for `GET /state`.
- `hubd/internal/db/state.go` (+ `state_test.go`) — `StateEvent` storage (`AppendStateEvent`, `LatestSessionEvent`).
- `hubd/internal/db/seen.go` (+ `seen_test.go`) — `PrincipalSeen` storage (`UpsertSeen`, `GetSeen`, `ListSeenForPrincipal`).
- `hubd/internal/state/projection.go` (+ `projection_test.go`) — in-memory current-state projection.
- `hubd/internal/state/poller.go` (+ `poller_test.go`) — the polling/ingest loop.
- `hubd/internal/state/seen.go` (+ `seen_test.go`) — pure `SeenProject` helper + `hubTS`.
- `hubd/internal/state/broadcaster.go` (+ `broadcaster_test.go`) — pub/sub for SSE + WS.
- `hubd/internal/api/seen.go` (+ `seen_test.go`) — `POST /api/v1/seen`.
- `hubd/internal/api/events.go` (+ `events_test.go`) — `GET /api/v1/events` SSE.

**Modified files**
- `agent/internal/state/state.go` (+ test) — counters, epoch, `Snapshot`.
- `agent/internal/hooks/hooks.go` (+ test) — capture `$TMUX` epoch into `state.Event`.
- `agent/cmd/agentmon-agent/main.go` — mount `GET /state`.
- `hubd/internal/registry/client.go` (+ test) — `State(...)` + `ErrStateUnsupported`.
- `hubd/internal/registry/registry.go` — add `State shared.State` to `ServerSummary`/`ServerDetail`.
- `hubd/internal/api/servers.go` — Deps gain projection/broadcaster/seen store; server rollup; `authorizeOr403` unchanged.
- `hubd/internal/api/sessions.go` — overlay seen-projected state on the session payload.
- `hubd/internal/api/ws.go` — relay refactor for the `{t:"state"}` frame.
- `hubd/internal/api/router.go` — register `/events` + `/seen`.
- `hubd/internal/config/config.go` (+ test) — `StatePollInterval`, `SSEHeartbeat`.
- `hubd/cmd/agentmon-hubd/main.go` — construct store/projection/broadcaster/poller; lifecycle.

---

# PHASE A — Ingest & aggregate (global state, no per-principal view yet)

## Task A1: Shared wire types + state-machine counters/epoch/Snapshot

**Files:**
- Create: `shared/agentstate.go`
- Modify: `agent/internal/state/state.go`
- Test: `agent/internal/state/state_test.go` (extend)

**Interfaces:**
- Produces: `shared.PaneState{Target,Pane string; State shared.State; TransitionSeq,DoneSeq uint64; Epoch,ClaudeSessionID string; LastChangeAt time.Time}`; `shared.AgentState{Panes []shared.PaneState}`.
- Produces: `state.Event.Epoch string` (new field); `(*state.Machine).Snapshot(target string) []shared.PaneState`.
- `Apply` keeps its `(shared.State, bool)` signature; counters update internally.

- [ ] **Step 1: Write `shared/agentstate.go`**
```go
package shared

import "time"

// PaneState is one pane's current derived state plus the transition counters the
// hub poller uses to ingest every transition exactly once (transport decision B).
type PaneState struct {
	Target          string    `json:"target"`
	Pane            string    `json:"pane"`
	State           State     `json:"state"`
	TransitionSeq   uint64    `json:"transitionSeq"` // bumped on every state change
	DoneSeq         uint64    `json:"doneSeq"`       // bumped on every entry into done (incl. done→done)
	Epoch           string    `json:"epoch"`         // $TMUX server pid; "" if unknown
	ClaudeSessionID string    `json:"claudeSessionId"`
	LastChangeAt    time.Time `json:"lastChangeAt"`
}

// AgentState is the agent's GET /state response: the per-pane snapshot the hub polls.
type AgentState struct {
	Panes []PaneState `json:"panes"`
}
```

- [ ] **Step 2: Write failing machine tests** (extend `state_test.go`)
```go
func TestApplyCountersTransitionAndDone(t *testing.T) {
	m := New(func() time.Time { return time.Unix(0, 0) })
	// SessionStart: unknown→idle = first change
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "SessionStart"})
	// UserPromptSubmit: idle→working = change
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "UserPromptSubmit"})
	// Stop: working→done = change AND a finished turn
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "Stop"})
	// Stop again: done→done = NOT a change, but a NEW finished turn
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "Stop"})

	snap := m.Snapshot("dev")
	if len(snap) != 1 {
		t.Fatalf("want 1 pane, got %d", len(snap))
	}
	got := snap[0]
	if got.State != shared.StateDone {
		t.Errorf("state = %q, want done", got.State)
	}
	if got.TransitionSeq != 3 { // unknown→idle→working→done (the 2nd Stop is not a change)
		t.Errorf("TransitionSeq = %d, want 3", got.TransitionSeq)
	}
	if got.DoneSeq != 2 { // both Stops are finished turns
		t.Errorf("DoneSeq = %d, want 2", got.DoneSeq)
	}
}

func TestApplyPreserveDoesNotBumpCounters(t *testing.T) {
	m := New(func() time.Time { return time.Unix(0, 0) })
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "Stop"}) // →done: Transition=1, Done=1
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "SubagentStop"}) // preserve
	got := m.Snapshot("dev")[0]
	if got.TransitionSeq != 1 || got.DoneSeq != 1 {
		t.Errorf("counters = (%d,%d), want (1,1)", got.TransitionSeq, got.DoneSeq)
	}
}

func TestApplyCapturesEpoch(t *testing.T) {
	m := New(func() time.Time { return time.Unix(0, 0) })
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "SessionStart", Epoch: "8421"})
	if got := m.Snapshot("dev")[0].Epoch; got != "8421" {
		t.Errorf("Epoch = %q, want 8421", got)
	}
}

func TestSnapshotFiltersByTargetAndSorts(t *testing.T) {
	m := New(func() time.Time { return time.Unix(0, 0) })
	m.Apply(Event{Target: "dev", Pane: "%1", Name: "Stop"})
	m.Apply(Event{Target: "dev", Pane: "%0", Name: "Stop"})
	m.Apply(Event{Target: "prod", Pane: "%0", Name: "Stop"})
	dev := m.Snapshot("dev")
	if len(dev) != 2 || dev[0].Pane != "%0" || dev[1].Pane != "%1" {
		t.Fatalf("dev snapshot wrong: %+v", dev)
	}
	if len(m.Snapshot("")) != 3 {
		t.Errorf("empty target should return all panes")
	}
}
```

- [ ] **Step 3: Run tests, verify they fail** — `go test ./agent/internal/state/ -run 'Counters|Preserve|Epoch|Snapshot' -v` → FAIL (undefined `Snapshot`, `Epoch`, fields).

- [ ] **Step 4: Implement.** In `state.go`:
  - Add `Epoch string` to `Event`.
  - Extend `paneState` with `Epoch string; TransitionSeq, DoneSeq uint64; ChangedAt time.Time`.
  - Rewrite `Apply` to carry/bump counters:
```go
func (m *Machine) Apply(ev Event) (shared.State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key{ev.Target, ev.Pane}
	ps := m.panes[k]            // zero value for a new pane (counters 0)
	prior := ps.State
	if prior == "" {
		prior = shared.StateUnknown
	}
	at := ev.At
	if at.IsZero() {
		at = m.now()
	}
	if ev.Name == "SessionEnd" {
		delete(m.panes, k) // counters die with the entry
		return shared.StateUnknown, prior != shared.StateUnknown
	}
	next := prior
	d, ok := derive(ev.Name, ev.NotificationKind)
	if ok {
		next = d
	}
	changed := next != prior
	if changed {
		ps.TransitionSeq++
		ps.ChangedAt = at
	}
	if ok && d == shared.StateDone { // every finished turn, incl. done→done
		ps.DoneSeq++
	}
	ps.State = next
	ps.LastEvent = ev.Name
	ps.ClaudeSessionID = ev.ClaudeSessionID
	ps.Epoch = ev.Epoch
	ps.UpdatedAt = at
	m.panes[k] = ps
	return next, changed
}
```
  - Add `Snapshot` (sorted, target-filtered) — import `sort`:
```go
func (m *Machine) Snapshot(target string) []shared.PaneState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]shared.PaneState, 0, len(m.panes))
	for k, ps := range m.panes {
		if target != "" && k.target != target {
			continue
		}
		out = append(out, shared.PaneState{
			Target: k.target, Pane: k.pane, State: ps.State,
			TransitionSeq: ps.TransitionSeq, DoneSeq: ps.DoneSeq,
			Epoch: ps.Epoch, ClaudeSessionID: ps.ClaudeSessionID,
			LastChangeAt: ps.ChangedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Pane < out[j].Pane
	})
	return out
}
```

- [ ] **Step 5: Run tests, verify pass** — `go test ./agent/internal/state/ -race -v` → PASS (all M6 tests still green).

- [ ] **Step 6: Commit** — `git commit -am "feat(agent/m7): state-machine transition/done counters + epoch + Snapshot; shared wire types"`

---

## Task A2: Hook epoch capture + agent `GET /state` endpoint + wiring

**Files:**
- Modify: `agent/internal/hooks/hooks.go`
- Test: `agent/internal/hooks/hooks_test.go` (extend)
- Create: `agent/internal/api/state.go`, `agent/internal/api/state_test.go`
- Modify: `agent/cmd/agentmon-agent/main.go`

**Interfaces:**
- Consumes: `shared.AgentState`, `(*state.Machine).Snapshot` (A1); `config.Config.ResolveTarget`; `api.RequireBearer`.
- Produces: `api.StateHandler(cfg config.Config, m *state.Machine) http.HandlerFunc` serving `GET /state`.

- [ ] **Step 1: Failing hook epoch test** (extend `hooks_test.go`) — assert the machine records the epoch from `X-AgentMon-Tmux`:
```go
func TestHookCapturesEpoch(t *testing.T) {
	m := state.New(func() time.Time { return time.Unix(0, 0) })
	h := RequireLoopback(RequireHookAuth("hooktok", HookHandler(testCfg(), m, nil)))
	req := httptest.NewRequest("POST", "http://127.0.0.1/hook", strings.NewReader(`{"hook_event_name":"SessionStart"}`))
	req.Header.Set("Authorization", "Bearer hooktok")
	req.Header.Set("X-AgentMon-Pane", "%0")
	req.Header.Set("X-AgentMon-Tmux", "/tmp/tmux-0/default,8421,0")
	req.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("status %d", w.Code)
	}
	if got := m.Snapshot("")[0].Epoch; got != "8421" { // testCfg() default target label
		t.Errorf("epoch = %q, want 8421", got)
	}
}
```
(Confirm `testCfg()`’s default-socket target label so `Snapshot("")` returns the pane; the test reads index 0.)

- [ ] **Step 2: Run → FAIL** (`epoch = "" ...`). `go test ./agent/internal/hooks/ -run Epoch -v`.

- [ ] **Step 3: Implement epoch capture** in `hooks.go`:
  - Add helper next to `socketFromTmux`:
```go
// epochFromTmux extracts the tmux server pid (field 2 of $TMUX
// "<path>,<pid>,<idx>"). "" when absent/malformed — epoch is best-effort.
func epochFromTmux(tmuxEnv string) string {
	parts := strings.SplitN(tmuxEnv, ",", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
```
  - In `HookHandler`, set `Epoch: epochFromTmux(r.Header.Get("X-AgentMon-Tmux"))` in the `state.Event` literal.

- [ ] **Step 4: Run hook test → PASS.**

- [ ] **Step 5: Failing `/state` handler tests** (`agent/internal/api/state_test.go`):
```go
func TestStateHandlerReturnsSnapshot(t *testing.T) {
	m := state.New(func() time.Time { return time.Unix(0, 0) })
	m.Apply(state.Event{Target: "default", Pane: "%0", Name: "Stop", Epoch: "8421"})
	h := RequireBearer("tok", StateHandler(testCfg(), m)) // testCfg has a default target labelled "default"
	req := httptest.NewRequest("GET", "/state", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var got shared.AgentState
	json.NewDecoder(w.Body).Decode(&got)
	if len(got.Panes) != 1 || got.Panes[0].State != shared.StateDone || got.Panes[0].DoneSeq != 1 {
		t.Fatalf("panes = %+v", got.Panes)
	}
}

func TestStateHandlerEmptyMachine(t *testing.T) {
	h := StateHandler(testCfg(), state.New(nil))
	req := httptest.NewRequest("GET", "/state", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var got shared.AgentState
	json.NewDecoder(w.Body).Decode(&got)
	if got.Panes == nil {
		t.Error("Panes must serialize as [] not null")
	}
}

func TestStateHandlerUnknownTarget(t *testing.T) {
	h := StateHandler(testCfg(), state.New(nil))
	req := httptest.NewRequest("GET", "/state?target=nope", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("status %d, want 404", w.Code)
	}
}
```
(Reuse the existing `testCfg()` helper in the agent api test package — check `sessions_test.go` for its name; if it differs, match it.)

- [ ] **Step 6: Run → FAIL** (undefined `StateHandler`).

- [ ] **Step 7: Implement `agent/internal/api/state.go`:**
```go
package api

import (
	"encoding/json"
	"net/http"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/state"
	"agentmon/shared"
)

// StateHandler serves GET /state?target=<label> — the per-pane transition snapshot
// the hub poller ingests (transport decision B). Internal agent↔hub surface; sits
// behind the same RequireBearer as /sessions. A nil machine yields {panes:[]}.
func StateHandler(cfg config.Config, m *state.Machine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := ""
		if q := r.URL.Query().Get("target"); q != "" {
			t, ok := cfg.ResolveTarget(q)
			if !ok {
				writeJSONError(w, http.StatusNotFound, "unknown target")
				return
			}
			target = t.Label
		}
		panes := []shared.PaneState{}
		if m != nil {
			if s := m.Snapshot(target); s != nil {
				panes = s
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(shared.AgentState{Panes: panes})
	}
}
```

- [ ] **Step 8: Run → PASS** (`go test ./agent/internal/api/ -race`).

- [ ] **Step 9: Wire in `main.go`** — after the `/sessions` line:
```go
	mux.Handle("GET /state", api.RequireBearer(cfg.HubToken, api.StateHandler(cfg, machine)))
```

- [ ] **Step 10: Build + commit** — `CGO_ENABLED=0 go build ./agent/...` then `git commit -am "feat(agent/m7): GET /state endpoint + hook epoch capture"`.

---

## Task A3: Registry client `State` + `ErrStateUnsupported`

**Files:**
- Modify: `hubd/internal/registry/client.go`
- Test: `hubd/internal/registry/client_test.go` (extend)

**Interfaces:**
- Produces: `registry.ErrStateUnsupported error`; `(*Client).State(ctx context.Context, srv db.Server, target string) (shared.AgentState, error)` — returns `ErrStateUnsupported` on HTTP 404 (agent without `/state`).

- [ ] **Step 1: Failing tests** (extend `client_test.go`, following the existing `Sessions` test’s httptest pattern):
```go
func TestClientStateDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/state" || r.Header.Get("Authorization") != "Bearer b" {
			w.WriteHeader(401); return
		}
		json.NewEncoder(w).Encode(shared.AgentState{Panes: []shared.PaneState{{Pane: "%0", State: shared.StateBlocked, DoneSeq: 2}}})
	}))
	defer srv.Close()
	got, err := NewClient(time.Second).State(context.Background(), db.Server{ID: "s", URL: srv.URL, Bearer: "b"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Panes) != 1 || got.Panes[0].State != shared.StateBlocked || got.Panes[0].DoneSeq != 2 {
		t.Fatalf("got %+v", got.Panes)
	}
}

func TestClientStateUnsupportedOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer srv.Close()
	_, err := NewClient(time.Second).State(context.Background(), db.Server{ID: "s", URL: srv.URL, Bearer: "b"}, "")
	if !errors.Is(err, ErrStateUnsupported) {
		t.Fatalf("err = %v, want ErrStateUnsupported", err)
	}
}
```

- [ ] **Step 2: Run → FAIL.**

- [ ] **Step 3: Implement** in `client.go` (mirror `Sessions`):
```go
var ErrStateUnsupported = errors.New("agent does not support /state")

func (c *Client) State(ctx context.Context, srv db.Server, target string) (shared.AgentState, error) {
	u := srv.URL + "/state"
	if target != "" {
		u += "?target=" + url.QueryEscape(target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return shared.AgentState{}, err
	}
	req.Header.Set("Authorization", "Bearer "+srv.Bearer)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return shared.AgentState{}, fmt.Errorf("dial agent %s: %w", srv.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return shared.AgentState{}, ErrStateUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return shared.AgentState{}, fmt.Errorf("agent %s state returned %d", srv.ID, resp.StatusCode)
	}
	var st shared.AgentState
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return shared.AgentState{}, fmt.Errorf("decode agent %s state: %w", srv.ID, err)
	}
	return st, nil
}
```
(Add `"errors"` to imports.)

- [ ] **Step 4: Run → PASS** (`go test ./hubd/internal/registry/ -race`).

- [ ] **Step 5: Commit** — `git commit -am "feat(hub/m7): registry Client.State + ErrStateUnsupported"`.

---

## Task A4: DB state-event storage

**Files:**
- Create: `hubd/internal/db/state.go`, `hubd/internal/db/state_test.go`

**Interfaces:**
- Produces: `db.StateEvent{ID,ServerID,TargetID,Session,Pane,Source,RawEvent,DerivedState,Payload,EventTs,ReceivedAt string}`.
- Produces: `(*DB).AppendStateEvent(ctx, e StateEvent) error`; `(*DB).LatestSessionEvent(ctx, serverID, target, session string) (StateEvent, bool, error)`.
- Note: callers stamp `EventTs` and `ReceivedAt` (both pre-formatted strings; `ReceivedAt` uses `hubTS` from Task B1/used by poller A6). The DB layer inserts them verbatim (single-clock invariant).

- [ ] **Step 1: Failing test** (`state_test.go`) — use the existing test DB helper (check `db_test.go` for how it opens a temp DB; reuse it). Must seed a server first (FK enforced):
```go
func TestAppendAndLatestStateEvent(t *testing.T) {
	d := newTestDB(t) // existing helper; opens temp sqlite + migrations
	ctx := context.Background()
	mustEnrollServer(t, d, "srvA") // existing helper or inline EnrollServer with status active
	e1 := db.StateEvent{ID: "e1", ServerID: "srvA", TargetID: "", Session: "api", Pane: "%0",
		Source: "hook", RawEvent: "{}", DerivedState: "working", EventTs: "2026-06-29 10:00:00.000", ReceivedAt: "2026-06-29 10:00:01.000"}
	e2 := e1
	e2.ID, e2.DerivedState, e2.ReceivedAt = "e2", "done", "2026-06-29 10:00:05.000"
	if err := d.AppendStateEvent(ctx, e1); err != nil { t.Fatal(err) }
	if err := d.AppendStateEvent(ctx, e2); err != nil { t.Fatal(err) }
	got, ok, err := d.LatestSessionEvent(ctx, "srvA", "", "api")
	if err != nil || !ok { t.Fatalf("ok=%v err=%v", ok, err) }
	if got.ID != "e2" || got.DerivedState != "done" {
		t.Fatalf("latest = %+v", got)
	}
}

func TestAppendStateEventRejectsBogusServer(t *testing.T) {
	d := newTestDB(t)
	err := d.AppendStateEvent(context.Background(), db.StateEvent{ID: "x", ServerID: "ghost", Session: "s",
		Source: "hook", RawEvent: "{}", DerivedState: "done", EventTs: "t", ReceivedAt: "t"})
	if err == nil {
		t.Fatal("expected FK violation for unknown server_id")
	}
}

func TestLatestSessionEventNoRows(t *testing.T) {
	d := newTestDB(t)
	_, ok, err := d.LatestSessionEvent(context.Background(), "srvA", "", "nope")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
```
(If `newTestDB`/`mustEnrollServer` helpers don’t exist with these names, reuse whatever `db_test.go`/`servers_test.go` already provide for a temp DB + an active server, and adjust.)

- [ ] **Step 2: Run → FAIL.**

- [ ] **Step 3: Implement `state.go`:**
```go
package db

import (
	"context"
	"database/sql"
)

type StateEvent struct {
	ID, ServerID, TargetID, Session, Pane string
	Source, RawEvent, DerivedState, Payload string
	EventTs, ReceivedAt string
}

func (d *DB) AppendStateEvent(ctx context.Context, e StateEvent) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO session_state_events(id, server_id, target_id, tmux_session_name, tmux_pane_id,
		    source, raw_event, derived_state, payload, event_ts, received_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.ServerID, nullIfEmpty(e.TargetID), e.Session, nullIfEmpty(e.Pane),
		e.Source, e.RawEvent, e.DerivedState, nullIfEmpty(e.Payload), e.EventTs, e.ReceivedAt)
	return err
}

func (d *DB) LatestSessionEvent(ctx context.Context, serverID, target, session string) (StateEvent, bool, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT id, server_id, COALESCE(target_id,''), tmux_session_name, COALESCE(tmux_pane_id,''),
		        source, raw_event, derived_state, COALESCE(payload,''), event_ts, received_at
		 FROM session_state_events
		 WHERE server_id=? AND COALESCE(target_id,'')=? AND tmux_session_name=?
		 ORDER BY received_at DESC, id DESC LIMIT 1`, serverID, target, session)
	var e StateEvent
	err := row.Scan(&e.ID, &e.ServerID, &e.TargetID, &e.Session, &e.Pane, &e.Source,
		&e.RawEvent, &e.DerivedState, &e.Payload, &e.EventTs, &e.ReceivedAt)
	if err == sql.ErrNoRows {
		return StateEvent{}, false, nil
	}
	if err != nil {
		return StateEvent{}, false, err
	}
	return e, true, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```
(If a `nullIfEmpty` equivalent already exists in the `db` package, reuse it instead of redeclaring.)

- [ ] **Step 4: Run → PASS** (`go test ./hubd/internal/db/ -race`).

- [ ] **Step 5: Commit** — `git commit -am "feat(hub/m7): session_state_events storage (AppendStateEvent, LatestSessionEvent)"`.

---

## Task A5: Hub current-state projection

**Files:**
- Create: `hubd/internal/state/projection.go`, `hubd/internal/state/projection_test.go`

**Interfaces:**
- Produces: `state.SessionView{ServerID,Target,Session string; Global shared.State; LatestReceivedAt string}`.
- Produces: `state.NewProjection() *Projection` with `Set(SessionView)`, `Session(server,target,session string)(SessionView,bool)`, `Server(server string)[]SessionView`, `All()[]SessionView`, `ReplaceServer(server string, views []SessionView)` (atomically swap a server’s sessions → prunes vanished ones). Thread-safe.

- [ ] **Step 1: Failing tests:**
```go
func TestProjectionSetGetServer(t *testing.T) {
	p := NewProjection()
	p.Set(SessionView{ServerID: "s", Session: "a", Global: shared.StateBlocked, LatestReceivedAt: "t1"})
	p.Set(SessionView{ServerID: "s", Session: "b", Global: shared.StateDone, LatestReceivedAt: "t2"})
	if v, ok := p.Session("s", "", "a"); !ok || v.Global != shared.StateBlocked {
		t.Fatalf("session a: %+v ok=%v", v, ok)
	}
	if len(p.Server("s")) != 2 {
		t.Fatalf("server should have 2 sessions")
	}
}

func TestProjectionReplaceServerPrunes(t *testing.T) {
	p := NewProjection()
	p.Set(SessionView{ServerID: "s", Session: "a", Global: shared.StateDone})
	p.Set(SessionView{ServerID: "s", Session: "b", Global: shared.StateDone})
	p.ReplaceServer("s", []SessionView{{ServerID: "s", Session: "a", Global: shared.StateWorking}})
	if _, ok := p.Session("s", "", "b"); ok {
		t.Error("session b should have been pruned")
	}
	if v, _ := p.Session("s", "", "a"); v.Global != shared.StateWorking {
		t.Error("session a should be updated to working")
	}
}
```

- [ ] **Step 2: Run → FAIL.**

- [ ] **Step 3: Implement `projection.go`:**
```go
package state

import (
	"sync"

	"agentmon/shared"
)

type SessionView struct {
	ServerID, Target, Session string
	Global                    shared.State
	LatestReceivedAt          string // hub-clock received_at of the session's latest event
}

type sessKey struct{ server, target, session string }

// Projection is the in-memory current-state derived from ingested events. It is a
// cache (the durable record is session_state_events); empty after a hub restart
// until the next poll repopulates it.
type Projection struct {
	mu       sync.RWMutex
	sessions map[sessKey]SessionView
}

func NewProjection() *Projection { return &Projection{sessions: map[sessKey]SessionView{}} }

func (p *Projection) Set(v SessionView) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessions[sessKey{v.ServerID, v.Target, v.Session}] = v
}

func (p *Projection) Session(server, target, session string) (SessionView, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.sessions[sessKey{server, target, session}]
	return v, ok
}

func (p *Projection) Server(server string) []SessionView {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []SessionView
	for k, v := range p.sessions {
		if k.server == server {
			out = append(out, v)
		}
	}
	return out
}

func (p *Projection) All() []SessionView {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]SessionView, 0, len(p.sessions))
	for _, v := range p.sessions {
		out = append(out, v)
	}
	return out
}

// ReplaceServer atomically swaps all of a server's sessions, pruning any not present
// in views (vanished sessions disappear from the projection).
func (p *Projection) ReplaceServer(server string, views []SessionView) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.sessions {
		if k.server == server {
			delete(p.sessions, k)
		}
	}
	for _, v := range views {
		p.sessions[sessKey{v.ServerID, v.Target, v.Session}] = v
	}
}
```

- [ ] **Step 4: Run → PASS** (`go test ./hubd/internal/state/ -race`).

- [ ] **Step 5: Commit** — `git commit -am "feat(hub/m7): in-memory current-state projection"`.

---

## Task A6: Hub poller (ingest → storage + projection)

**Files:**
- Create: `hubd/internal/state/poller.go`, `hubd/internal/state/poller_test.go`

**Interfaces:**
- Consumes: `registry` (List/Get/State/Sessions), `db` (AppendStateEvent), `Projection` (A5), `shared.RollUp`.
- Produces:
```go
type AgentAPI interface {
	State(ctx context.Context, srv db.Server, target string) (shared.AgentState, error)
	Sessions(ctx context.Context, srv db.Server, target string) ([]shared.Session, error)
}
type ServerLister interface{ List(ctx context.Context) ([]registry.ServerSummary, error); Get(ctx context.Context, id string) (db.Server, bool, error) }
type EventStore interface{ AppendStateEvent(ctx context.Context, e db.StateEvent) error }
type Poller struct{ /* unexported deps */ }
func NewPoller(lister ServerLister, agent AgentAPI, store EventStore, proj *Projection, interval time.Duration, now func() time.Time) *Poller
func (p *Poller) Run(ctx context.Context)        // ticks every interval until ctx.Done
func (p *Poller) Tick(ctx context.Context)       // one poll cycle (used by tests; no sleeping)
```
- Produces: `hubTS(t time.Time) string` lives in Task B1’s `state/seen.go`; for A6 add it now in `state/util.go` if B1 not yet done — but to keep ordering clean, **define `hubTS` in A6** (`hubd/internal/state/util.go`) and B1 reuses it.

- [ ] **Step 1: Define `hubTS`** in `hubd/internal/state/util.go`:
```go
package state

import "time"

// hubTS formats a hub-clock timestamp for storage/comparison. Fixed width so
// lexical string comparison is chronological. Single clock for the seen invariant.
func hubTS(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000") }
```

- [ ] **Step 2: Failing poller tests** with fakes:
```go
type fakeLister struct{ servers []registry.ServerSummary; get map[string]db.Server }
func (f *fakeLister) List(context.Context) ([]registry.ServerSummary, error) { return f.servers, nil }
func (f *fakeLister) Get(_ context.Context, id string) (db.Server, bool, error) { s, ok := f.get[id]; return s, ok, nil }

type fakeAgent struct {
	state    map[string]shared.AgentState // by serverID
	sessions map[string][]shared.Session
	stateErr map[string]error
}
func (f *fakeAgent) State(_ context.Context, srv db.Server, _ string) (shared.AgentState, error) {
	if e := f.stateErr[srv.ID]; e != nil { return shared.AgentState{}, e }
	return f.state[srv.ID], nil
}
func (f *fakeAgent) Sessions(_ context.Context, srv db.Server, _ string) ([]shared.Session, error) {
	return f.sessions[srv.ID], nil
}

type fakeStore struct{ events []db.StateEvent }
func (f *fakeStore) AppendStateEvent(_ context.Context, e db.StateEvent) error { f.events = append(f.events, e); return nil }

func newPollerFixture() (*Poller, *fakeStore, *Projection) {
	lister := &fakeLister{
		servers: []registry.ServerSummary{{ID: "s"}},
		get:     map[string]db.Server{"s": {ID: "s", URL: "http://x", Bearer: "b"}},
	}
	agent := &fakeAgent{
		state:    map[string]shared.AgentState{"s": {Panes: []shared.PaneState{{Target: "", Pane: "%0", State: shared.StateDone, TransitionSeq: 1, DoneSeq: 1, Epoch: "1"}}}},
		sessions: map[string][]shared.Session{"s": {{Name: "api", Windows: []shared.Window{{Panes: []shared.Pane{{ID: "%0"}}}}}}},
	}
	store := &fakeStore{}
	proj := NewProjection()
	clk := time.Unix(100, 0)
	p := NewPoller(lister, agent, store, proj, time.Second, func() time.Time { return clk })
	return p, store, proj
}

func TestPollerIngestsFirstSeenEventAndProjects(t *testing.T) {
	p, store, proj := newPollerFixture()
	p.Tick(context.Background())
	if len(store.events) != 1 || store.events[0].DerivedState != "done" || store.events[0].Session != "api" {
		t.Fatalf("events = %+v", store.events)
	}
	if v, ok := proj.Session("s", "", "api"); !ok || v.Global != shared.StateDone {
		t.Fatalf("projection = %+v ok=%v", v, ok)
	}
}

func TestPollerDoneToDoneViaDoneSeq(t *testing.T) {
	p, store, _ := newPollerFixture()
	p.Tick(context.Background()) // first event (done, doneSeq=1)
	// Same state (done) but a NEW finished turn → doneSeq bumps; expect a 2nd event.
	pollerSetDoneSeq(p, "s", "%0", 2) // helper updates the fake agent's returned snapshot
	p.Tick(context.Background())
	if len(store.events) != 2 {
		t.Fatalf("want 2 events (done→done re-alert), got %d", len(store.events))
	}
}

func TestPollerNoChangeNoEvent(t *testing.T) {
	p, store, _ := newPollerFixture()
	p.Tick(context.Background())
	p.Tick(context.Background()) // identical snapshot → no new event
	if len(store.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(store.events))
	}
}

func TestPollerEpochChangeReingests(t *testing.T) {
	p, store, _ := newPollerFixture()
	p.Tick(context.Background())
	pollerSetEpoch(p, "s", "%0", "999")
	p.Tick(context.Background())
	if len(store.events) != 2 {
		t.Fatalf("epoch change should re-ingest, got %d events", len(store.events))
	}
}

func TestPollerDegradesOn404(t *testing.T) {
	p, store, proj := newPollerFixture()
	pollerForceStateErr(p, "s", registry.ErrStateUnsupported)
	p.Tick(context.Background())
	// Falls back to Sessions(): the fake returns session "api" with state "" → unknown;
	// to make it meaningful set the session's State in the fake to done.
	if v, ok := proj.Session("s", "", "api"); !ok {
		t.Fatalf("degraded path must still project; ok=%v %+v", ok, v)
	}
	_ = store
}
```
(The `pollerSet*`/`pollerForce*` helpers mutate the `fakeAgent` the fixture closed over — implement them in the test file by keeping a reference to the `fakeAgent`. Adjust fixture to return it.)

- [ ] **Step 3: Run → FAIL.**

- [ ] **Step 4: Implement `poller.go`.** Core logic:
  - `Poller` holds deps + `lastSeen map[paneKey]shared.PaneState` (key = server+target+pane) + per-server backoff state.
  - `Run`: `time.NewTicker(interval)`; on each tick call `Tick`; return on `ctx.Done()`.
  - `Tick`:
    1. `lister.List(ctx)` → for each active server (bounded concurrency e.g. `errgroup` or a semaphore of 4); per server respect backoff (skip until `nextAttempt`).
    2. `srv,_,_ := lister.Get(ctx, id)`.
    3. `st, err := agent.State(ctx, srv, "")`. On `ErrStateUnsupported` → `pollDegraded(...)` (use `Sessions`, synthesize one `PaneState` per session’s rolled-up `Session.State`, `Source:"snapshot"`); on other error → bump backoff, continue.
    4. On success: reset backoff. `sessions, _ := agent.Sessions(ctx, srv, "")` → build `pane→session` map; drop panes not in the live tree.
    5. For each polled `PaneState`: compute `paneKey`; compare to `lastSeen`. Write an event iff `state changed || doneSeq increased || epoch changed || not seen before || counters reset (seq < lastSeen seq → agent restart, treat as new)`. Event fields: `ID=uuid`, `ServerID=id`, `TargetID=pane.Target`, `Session=mapped session`, `Pane=pane.Pane`, `Source="hook"`, `RawEvent=compactJSON(pane)`, `DerivedState=string(pane.State)`, `EventTs=hubTS(pane.LastChangeAt)` (agent time, informational), `ReceivedAt=hubTS(p.now())`. Update `lastSeen`.
    6. Build `[]SessionView` for the server: group panes by session, `Global=shared.RollUp(paneStates...)`, `LatestReceivedAt=` the max `ReceivedAt` written for that session this tick (or carry prior from projection if unchanged). `proj.ReplaceServer(id, views)`.
  - Prune `lastSeen` entries for panes no longer present (so a vanished pane’s state doesn’t linger).
  - Backoff: `base=interval`, double on each failure, cap 30s; store `nextAttempt time.Time` per server; reset on success.

- [ ] **Step 5: Run → PASS** (`go test ./hubd/internal/state/ -race`).

- [ ] **Step 6: Commit** — `git commit -am "feat(hub/m7): state poller — ingest transitions, projection, backoff, /state-404 degrade"`.

---

## Task A7: Public payload — server rollup + session overlay (global state)

**Files:**
- Modify: `hubd/internal/registry/registry.go` (add `State` field), `hubd/internal/api/servers.go`, `hubd/internal/api/sessions.go`
- Test: `hubd/internal/api/servers_test.go`, `hubd/internal/api/sessions_test.go` (extend)

**Interfaces:**
- Consumes: `*state.Projection` (A5), `shared.RollUp`.
- Modify `api.Deps`: add `Proj *state.Projection`.
- Produces: `ServerSummary.State shared.State` / `ServerDetail.State shared.State` (`json:"state,omitempty"`), filled from the projection’s server rollup. Session payload `state` overlaid from the projection (global; seen projection added in B3). Pre-poll fallback: if the projection has no entry for a session, keep the agent’s inline `Session.State`.

- [ ] **Step 1: Failing tests** (handler-level, injecting a primed `Projection` into `Deps`):
```go
func TestServerSessionsOverlaysProjectionState(t *testing.T) {
	proj := state.NewProjection()
	proj.Set(state.SessionView{ServerID: "s", Session: "api", Global: shared.StateBlocked})
	d := testDeps(t, withProjection(proj), withAgentSessions("s", []shared.Session{{Name: "api", State: shared.StateUnknown}}))
	// ... call ServerSessionsHandler for server "s", decode []shared.Session
	// assert the "api" session's State == blocked (overlay wins over agent's unknown)
}

func TestServerSessionsPrePollFallback(t *testing.T) {
	d := testDeps(t, withProjection(state.NewProjection()), // empty
		withAgentSessions("s", []shared.Session{{Name: "api", State: shared.StateWorking}}))
	// assert "api" State == working (falls back to agent inline state when projection empty)
}

func TestServerDetailHasRollupState(t *testing.T) {
	proj := state.NewProjection()
	proj.Set(state.SessionView{ServerID: "s", Session: "a", Global: shared.StateDone})
	proj.Set(state.SessionView{ServerID: "s", Session: "b", Global: shared.StateBlocked})
	// GET /servers/s → ServerDetail.State == blocked (RollUp of done+blocked)
}
```
(Build on the existing `servers_test.go`/`sessions_test.go` harness — match how they construct `Deps` and fake the registry/agent. Add a `Proj` to that harness.)

- [ ] **Step 2: Run → FAIL.**

- [ ] **Step 3: Implement.**
  - `registry.go`: add `State shared.State \`json:"state,omitempty"\`` to `ServerSummary` and `ServerDetail`. (Import `shared`.)
  - In `servers.go` add a helper:
```go
// serverRollup returns the §9.2 rollup of a server's session states from the
// projection (StateUnknown when nothing is known yet).
func (d Deps) serverRollup(serverID string) shared.State {
	views := d.Proj.Server(serverID)
	states := make([]shared.State, 0, len(views))
	for _, v := range views {
		states = append(states, v.Global)
	}
	return shared.RollUp(states...)
}
```
  Fill `State: d.serverRollup(s.ID)` when building each summary in `ServersHandler`, and `State: d.serverRollup(srv.ID)` in `ServerHandler`’s `ServerDetail`.
  - In `sessions.go` add an overlay helper and apply it in both `ServerSessionsHandler` and `SessionDetailHandler` after fetching `sessions`:
```go
// overlayState replaces each session's State with the hub projection's global
// state when known; otherwise keeps the agent's inline state (pre-poll fallback).
// (B3 extends this with the per-principal seen projection.)
func (d Deps) overlayState(serverID, target string, sessions []shared.Session) {
	for i := range sessions {
		if v, ok := d.Proj.Session(serverID, target, sessions[i].Name); ok {
			sessions[i].State = v.Global
		}
	}
}
```
  Call `d.overlayState(id, "", sessions)` in `ServerSessionsHandler` (target "" — the default) and `d.overlayState(id, target, sessions)` in `SessionDetailHandler`, before `writeJSON`.

- [ ] **Step 4: Run → PASS** (`go test ./hubd/internal/api/ -race`).

- [ ] **Step 5: Commit** — `git commit -am "feat(hub/m7): overlay projection state on session payload + server rollup dots"`.

---

## Task A8: Hub `main` lifecycle wiring + `StatePollInterval` config

**Files:**
- Modify: `hubd/internal/config/config.go` (+ `config_test.go`), `hubd/cmd/agentmon-hubd/main.go`

**Interfaces:**
- Consumes: everything from A4–A7.
- Produces: `config.Config.StatePollInterval time.Duration` (default 3s); a running poller goroutine + graceful shutdown.

- [ ] **Step 1: Failing config test** — assert the field parses and defaults. Follow the existing optional-duration field pattern in `config_test.go` (e.g. how `SessionCookie.TTL` is tested). Add a `statePollDefault(cfg)` accessor in `main.go` mirroring `cookieTTL`.

- [ ] **Step 2: Implement config field** — add `StatePollInterval time.Duration \`yaml:"state_poll_interval"\`` (match the existing config struct’s tag style — verify whether it’s yaml/toml; the hub uses yaml per `config.Load("config.yaml")`). Add `statePoll(cfg)` returning the field or `3 * time.Second`.

- [ ] **Step 3: Wire `main.go`** (no new behavior test here beyond build + existing suite; this is integration glue):
```go
	proj := state.NewProjection()
	agentClient := registry.NewClient(10 * time.Second)
	poller := state.NewPoller(reg, agentClient, database, proj, statePoll(cfg), time.Now)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go poller.Run(ctx)
```
  - Pass `Proj: proj` (and reuse `agentClient` as `Agent`) into `api.Deps`.
  - Replace `log.Fatal(srv.ListenAndServe())` with a graceful pattern:
```go
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()
	<-ctx.Done()
	stop()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
```
  (Add imports `os/signal`, `syscall`. `reg` must satisfy the poller’s `ServerLister` — it has `List(ctx)([]ServerSummary,error)` and `Get`; confirm signatures match, adapt the interface in A6 to the real `registry.Registry` methods.)

- [ ] **Step 4: Build + full suite** — `CGO_ENABLED=0 go build ./... && go test ./... -race && go vet ./...` → all green.

- [ ] **Step 5: Commit** — `git commit -am "feat(hub/m7): wire poller + projection into hubd main with graceful shutdown; state_poll_interval config"`.

> **Phase A checkpoint:** the hub now proactively ingests state, stores events, and serves rollup dots + projection-overlaid session state (global, no seen yet). Run the full suite before starting Phase B.

---

# PHASE B — Per-principal view & delivery

## Task B1: DB seen storage + `SeenProject` helper

**Files:**
- Create: `hubd/internal/db/seen.go`, `hubd/internal/db/seen_test.go`
- Create: `hubd/internal/state/seen.go`, `hubd/internal/state/seen_test.go`

**Interfaces:**
- Produces (db): `db.PrincipalSeen{PrincipalID,ServerID,TargetID,Session,LastSeenEventID,LastFocusedAt string}`; `(*DB).UpsertSeen(ctx, s PrincipalSeen) error`; `(*DB).GetSeen(ctx, principalID,serverID,target,session string)(PrincipalSeen,bool,error)`; `(*DB).ListSeenForPrincipal(ctx, principalID string)([]PrincipalSeen,error)`.
- Produces (state): `state.SeenProject(global shared.State, latestReceivedAt string, seen db.PrincipalSeen, ok bool) shared.State`.

- [ ] **Step 1: Failing `SeenProject` tests:**
```go
func TestSeenProject(t *testing.T) {
	doneAt := "2026-06-29 10:00:05.000"
	cases := []struct {
		name    string
		global  shared.State
		latest  string
		seen    db.PrincipalSeen
		ok      bool
		want    shared.State
	}{
		{"done unseen (no record)", shared.StateDone, doneAt, db.PrincipalSeen{}, false, shared.StateDone},
		{"done focused before finish", shared.StateDone, doneAt, db.PrincipalSeen{LastFocusedAt: "2026-06-29 10:00:01.000"}, true, shared.StateDone},
		{"done focused after finish", shared.StateDone, doneAt, db.PrincipalSeen{LastFocusedAt: "2026-06-29 10:00:09.000"}, true, shared.StateIdle},
		{"done focused exactly at finish", shared.StateDone, doneAt, db.PrincipalSeen{LastFocusedAt: doneAt}, true, shared.StateIdle},
		{"blocked never masked", shared.StateBlocked, doneAt, db.PrincipalSeen{LastFocusedAt: "2026-06-29 23:00:00.000"}, true, shared.StateBlocked},
		{"working passes through", shared.StateWorking, doneAt, db.PrincipalSeen{LastFocusedAt: "2026-06-29 23:00:00.000"}, true, shared.StateWorking},
	}
	for _, c := range cases {
		if got := SeenProject(c.global, c.latest, c.seen, c.ok); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Implement `state/seen.go`:**
```go
package state

import (
	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// SeenProject masks done→idle for a principal who has focused the session at or
// after its latest finish. Only done is maskable; blocked/working/idle/unknown
// pass through. Comparison is hub-clock string compare (single-clock invariant).
func SeenProject(global shared.State, latestReceivedAt string, seen db.PrincipalSeen, ok bool) shared.State {
	if global != shared.StateDone || !ok {
		return global
	}
	if seen.LastFocusedAt >= latestReceivedAt {
		return shared.StateIdle
	}
	return global
}
```

- [ ] **Step 3: Run state/seen tests → PASS.**

- [ ] **Step 4: Failing db seen tests** (`seen_test.go`): upsert then get returns the row; a second upsert (same PK) updates `last_focused_at`/`last_seen_event_id`; `ListSeenForPrincipal` returns all of a principal’s rows. (Use the temp-DB helper; `principal_seen` has no FK so no server seeding needed.)

- [ ] **Step 5: Implement `db/seen.go`:**
```go
package db

import (
	"context"
	"database/sql"
)

type PrincipalSeen struct {
	PrincipalID, ServerID, TargetID, Session string
	LastSeenEventID, LastFocusedAt           string
}

func (d *DB) UpsertSeen(ctx context.Context, s PrincipalSeen) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO principal_seen(principal_id, server_id, target_id, tmux_session_name, last_seen_event_id, last_focused_at)
		 VALUES(?,?,?,?,?,?)
		 ON CONFLICT(principal_id, server_id, target_id, tmux_session_name)
		 DO UPDATE SET last_seen_event_id=excluded.last_seen_event_id, last_focused_at=excluded.last_focused_at`,
		s.PrincipalID, s.ServerID, s.TargetID, s.Session, nullIfEmpty(s.LastSeenEventID), s.LastFocusedAt)
	return err
}

func (d *DB) GetSeen(ctx context.Context, principalID, serverID, target, session string) (PrincipalSeen, bool, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT principal_id, server_id, target_id, tmux_session_name, COALESCE(last_seen_event_id,''), last_focused_at
		 FROM principal_seen WHERE principal_id=? AND server_id=? AND target_id=? AND tmux_session_name=?`,
		principalID, serverID, target, session)
	var s PrincipalSeen
	err := row.Scan(&s.PrincipalID, &s.ServerID, &s.TargetID, &s.Session, &s.LastSeenEventID, &s.LastFocusedAt)
	if err == sql.ErrNoRows {
		return PrincipalSeen{}, false, nil
	}
	if err != nil {
		return PrincipalSeen{}, false, err
	}
	return s, true, nil
}

func (d *DB) ListSeenForPrincipal(ctx context.Context, principalID string) ([]PrincipalSeen, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT principal_id, server_id, target_id, tmux_session_name, COALESCE(last_seen_event_id,''), last_focused_at
		 FROM principal_seen WHERE principal_id=?`, principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PrincipalSeen
	for rows.Next() {
		var s PrincipalSeen
		if err := rows.Scan(&s.PrincipalID, &s.ServerID, &s.TargetID, &s.Session, &s.LastSeenEventID, &s.LastFocusedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
```
(`target_id` is `NOT NULL DEFAULT ''` in the schema — pass `""` literally, not NULL, so the PK/`ON CONFLICT` match works.)

- [ ] **Step 6: Run → PASS.** Note the cross-package import: `hubd/internal/state` now imports `hubd/internal/db` — confirm no import cycle (db must NOT import state; it doesn’t).

- [ ] **Step 7: Commit** — `git commit -am "feat(hub/m7): principal_seen storage + SeenProject helper"`.

---

## Task B2: `POST /api/v1/seen`

**Files:**
- Create: `hubd/internal/api/seen.go`, `hubd/internal/api/seen_test.go`
- Modify: `hubd/internal/api/servers.go` (Deps gain `SeenStore`), `hubd/internal/api/router.go`

**Interfaces:**
- Consumes: `db.LatestSessionEvent`, `db.UpsertSeen`, `authn.PrincipalFrom`, `authorizeOr403`, `state.hubTS` (export it or add `state.HubTS`).
- Produces: `(Deps) SeenHandler() http.HandlerFunc`. Deps add `SeenStore` interface:
```go
type SeenStore interface {
	UpsertSeen(ctx context.Context, s db.PrincipalSeen) error
	GetSeen(ctx context.Context, principalID, serverID, target, session string) (db.PrincipalSeen, bool, error)
	ListSeenForPrincipal(ctx context.Context, principalID string) ([]db.PrincipalSeen, error)
	LatestSessionEvent(ctx context.Context, serverID, target, session string) (db.StateEvent, bool, error)
}
```
- **Export `HubTS`**: rename `state.hubTS` → `state.HubTS` (update A6 callers) so the api package can stamp `last_focused_at` with the same format.

- [ ] **Step 1: Failing tests:**
```go
func TestSeenUpsertsAndAnchors(t *testing.T) {
	// seed a state event for (s, "", "api") so LatestSessionEvent returns it,
	// POST /seen {serverId:s,target:"",sessionName:api} with a valid principal+CSRF,
	// assert 204 and GetSeen returns a row whose LastSeenEventID == the seeded event id.
}
func TestSeenRequiresCSRF(t *testing.T) { /* POST without CSRF → 403 */ }
func TestSeenBadBody(t *testing.T) { /* empty sessionName → 400 */ }
```
(Use the existing api test harness for an authenticated principal + CSRF; see how `*_test.go` builds authed POSTs. Inject a real temp `*db.DB` as `SeenStore` or a fake implementing the interface.)

- [ ] **Step 2: Run → FAIL.**

- [ ] **Step 3: Implement `seen.go`:**
```go
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

type seenRequest struct {
	ServerID    string `json:"serverId"`
	Target      string `json:"target"`
	SessionName string `json:"sessionName"`
}

func (d Deps) SeenHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req seenRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.ServerID == "" || req.SessionName == "" {
			writeJSONError(w, http.StatusBadRequest, "serverId and sessionName required")
			return
		}
		p, ok := d.authorizeOr403(w, r, authz.SessionView, shared.SessionID(req.ServerID, req.Target, req.SessionName))
		if !ok {
			return
		}
		latestID := ""
		if ev, found, err := d.Seen.LatestSessionEvent(r.Context(), req.ServerID, req.Target, req.SessionName); err == nil && found {
			latestID = ev.ID
		}
		err := d.Seen.UpsertSeen(r.Context(), db.PrincipalSeen{
			PrincipalID: p.ID, ServerID: req.ServerID, TargetID: req.Target, Session: req.SessionName,
			LastSeenEventID: latestID, LastFocusedAt: state.HubTS(time.Now()),
		})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 4: Register route** in `router.go`:
```go
	mux.Handle("POST /api/v1/seen", rd.Auth.RequireAuth(rd.API.SeenHandler()))
```
  Add `Seen SeenStore` to `Deps`; wire `Seen: database` in `main.go` (the `*db.DB` satisfies it).

- [ ] **Step 5: Run → PASS.**

- [ ] **Step 6: Commit** — `git commit -am "feat(hub/m7): POST /api/v1/seen records principal_seen anchored to latest event"`.

---

## Task B3: Apply the seen projection in the payload overlay

**Files:**
- Modify: `hubd/internal/api/sessions.go`, `hubd/internal/api/servers.go`
- Test: extend `sessions_test.go`, `servers_test.go`

**Interfaces:**
- Consumes: `state.SeenProject`, `Deps.Seen.GetSeen`, the requesting principal.
- Behavior: the session/server payload state becomes per-principal seen-projected.

- [ ] **Step 1: Failing tests** — a session whose projection `Global==done` with `LatestReceivedAt=t1` and a `principal_seen` row with `LastFocusedAt >= t1` must render `idle`; with no seen row must render `done`; the server rollup must reflect the projected states (a server whose only `done` was seen reads `idle`, not `done`).

- [ ] **Step 2: Run → FAIL.**

- [ ] **Step 3: Implement.** Replace `overlayState` (A7) with a principal-aware version and route the principal through:
```go
func (d Deps) overlayState(ctx context.Context, principalID, serverID, target string, sessions []shared.Session) {
	for i := range sessions {
		v, ok := d.Proj.Session(serverID, target, sessions[i].Name)
		if !ok {
			continue // pre-poll fallback: keep agent inline state
		}
		seen, has, _ := d.Seen.GetSeen(ctx, principalID, serverID, target, sessions[i].Name)
		sessions[i].State = state.SeenProject(v.Global, v.LatestReceivedAt, seen, has)
	}
}
```
  Update both call sites to pass `r.Context()` and the principal id (from `authorizeOr403`’s returned principal). Update `serverRollup` similarly to project each session for the principal before rolling up:
```go
func (d Deps) serverRollup(ctx context.Context, principalID, serverID string) shared.State {
	views := d.Proj.Server(serverID)
	states := make([]shared.State, 0, len(views))
	for _, v := range views {
		seen, has, _ := d.Seen.GetSeen(ctx, principalID, serverID, v.Target, v.Session)
		states = append(states, state.SeenProject(v.Global, v.LatestReceivedAt, seen, has))
	}
	return shared.RollUp(states...)
}
```
  (For `ServersHandler`, which lists many servers, this is N×M `GetSeen` calls; acceptable at single-user scale. If it shows up, batch via `ListSeenForPrincipal` into a map — note as a possible later optimization, not required now.)

- [ ] **Step 4: Run → PASS** + full `go test ./... -race`.

- [ ] **Step 5: Commit** — `git commit -am "feat(hub/m7): per-principal seen projection in session + server payloads"`.

---

## Task B4: Broadcaster

**Files:**
- Create: `hubd/internal/state/broadcaster.go`, `hubd/internal/state/broadcaster_test.go`

**Interfaces:**
- Produces: `state.Change{ServerID,Target,Session string; Global shared.State; LatestReceivedAt string}`; `state.NewBroadcaster() *Broadcaster` with `Subscribe()(id uint64, ch <-chan Change, cancel func())` and `Publish(c Change)` (non-blocking; drop-oldest per slow subscriber).

- [ ] **Step 1: Failing tests:**
```go
func TestBroadcasterDelivers(t *testing.T) {
	b := NewBroadcaster()
	_, ch, cancel := b.Subscribe()
	defer cancel()
	b.Publish(Change{Session: "a", Global: shared.StateBlocked})
	select {
	case c := <-ch:
		if c.Session != "a" || c.Global != shared.StateBlocked { t.Fatalf("got %+v", c) }
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}
}

func TestBroadcasterCancelStopsDelivery(t *testing.T) {
	b := NewBroadcaster()
	_, ch, cancel := b.Subscribe()
	cancel()
	b.Publish(Change{Session: "a"})
	if _, open := <-ch; open { t.Fatal("channel should be closed after cancel") }
}

func TestBroadcasterDropsOldestWhenFull(t *testing.T) {
	b := NewBroadcaster()
	_, ch, cancel := b.Subscribe()
	defer cancel()
	for i := 0; i < 200; i++ { b.Publish(Change{Session: "x"}) } // never blocks despite no reader
	if len(ch) == 0 { t.Fatal("expected buffered changes") }
}
```

- [ ] **Step 2: Run → FAIL.**

- [ ] **Step 3: Implement** with a `sync.Mutex`, `subs map[uint64]chan Change`, buffered channels (cap 64), monotonic id counter. `Publish` does a non-blocking send (`select { case ch <- c: default: <drop-oldest then send> }`); `cancel` deletes + closes. Document drop-oldest semantics (a fresh SSE snapshot reconciles drops).

- [ ] **Step 4: Run → PASS** (`-race`, include a parallel publish/subscribe test).

- [ ] **Step 5: Commit** — `git commit -am "feat(hub/m7): in-process state Broadcaster (SSE + WS fan-out)"`.

---

## Task B5: Poller publishes session changes to the broadcaster

**Files:**
- Modify: `hubd/internal/state/poller.go`
- Test: extend `poller_test.go`

**Interfaces:**
- `NewPoller` gains a `*Broadcaster` param (nil-safe — tests may pass nil). On any session whose rolled-up `Global` changed this tick (or a new finished turn landed), call `bcast.Publish(Change{...})`.

- [ ] **Step 1: Failing test** — subscribe to a broadcaster, run a tick that produces a `done` session, assert a `Change` is delivered with `Global==done`; a second identical tick (no change) publishes nothing.

- [ ] **Step 2: Implement** — track prior `Global` per session (from the projection before `ReplaceServer`); publish when changed or a new event was written for the session this tick. Guard `if p.bcast != nil`.

- [ ] **Step 3: Run → PASS.**

- [ ] **Step 4: Commit** — `git commit -am "feat(hub/m7): poller publishes session state changes to the broadcaster"`.

---

## Task B6: `GET /api/v1/events` SSE + `SSEHeartbeat` config

**Files:**
- Create: `hubd/internal/api/events.go`, `hubd/internal/api/events_test.go`
- Modify: `hubd/internal/api/router.go`, `hubd/internal/api/servers.go` (Deps gain `Bcast *state.Broadcaster`), `hubd/internal/config/config.go`, `hubd/cmd/agentmon-hubd/main.go`

**Interfaces:**
- Consumes: `Deps.Proj`, `Deps.Bcast`, `Deps.Seen.ListSeenForPrincipal`, `state.SeenProject`, `authn.PrincipalFrom`.
- Produces: `(Deps) EventsHandler() http.HandlerFunc`. Config `SSEHeartbeat time.Duration` (default 25s).

- [ ] **Step 1: Failing test** — a primed projection + broadcaster; open the handler with `httptest` + a context that cancels after reading; assert: (1) an initial `event: snapshot` containing the visible sessions seen-projected for the principal; (2) after a `Publish`, an `event: state` delta line; (3) clean return on context cancel. Use `httptest.NewRecorder` with a flushable writer, or run the handler in a goroutine against an `httptest.Server` and read the stream with `bufio.Scanner`.

- [ ] **Step 2: Implement `events.go`:**
```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

type stateEvent struct {
	Server  string       `json:"server"`
	Target  string       `json:"target"`
	Session string       `json:"session"`
	State   shared.State `json:"state"`
}

func (d Deps) EventsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := authn.PrincipalFrom(r.Context())
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, "stream unsupported")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// seen map for this principal (projection applied below)
		seen := map[seenKey]db.PrincipalSeen{}
		if rows, err := d.Seen.ListSeenForPrincipal(r.Context(), p.ID); err == nil {
			for _, s := range rows {
				seen[seenKey{s.ServerID, s.TargetID, s.Session}] = s
			}
		}
		project := func(v state.SessionView) shared.State {
			s, has := seen[seenKey{v.ServerID, v.Target, v.Session}]
			return state.SeenProject(v.Global, v.LatestReceivedAt, s, has)
		}

		// initial snapshot
		snap := make([]stateEvent, 0)
		for _, v := range d.Proj.All() {
			snap = append(snap, stateEvent{v.ServerID, v.Target, v.Session, project(v)})
		}
		writeSSE(w, "snapshot", snap)
		flusher.Flush()

		_, ch, cancel := d.Bcast.Subscribe()
		defer cancel()
		hb := time.NewTicker(d.SSEHeartbeat)
		defer hb.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case c := <-ch:
				s, has := seen[seenKey{c.ServerID, c.Target, c.Session}]
				writeSSE(w, "state", stateEvent{c.ServerID, c.Target, c.Session,
					state.SeenProject(c.Global, c.LatestReceivedAt, s, has)})
				flusher.Flush()
			case <-hb.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}
	}
}

type seenKey struct{ server, target, session string }

func writeSSE(w http.ResponseWriter, event string, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}
```
  (Note: SSE deltas use the *connect-time* seen map; a `POST /seen` during a live stream isn’t reflected until reconnect. Acceptable for M7 — document it. If undesired, re-query `GetSeen` per delta.)

- [ ] **Step 3: Config + wiring** — add `SSEHeartbeat time.Duration` (default 25s) to config + an `sseHeartbeat(cfg)` accessor; set `Deps.SSEHeartbeat` and `Deps.Bcast` in `main.go`; pass the same `*Broadcaster` to `NewPoller` (B5). Register:
```go
	mux.Handle("GET /api/v1/events", rd.Auth.RequireAuth(rd.API.EventsHandler()))
```

- [ ] **Step 4: Run → PASS** + full suite `-race` (watch for goroutine leaks: the test must cancel the request context).

- [ ] **Step 5: Commit** — `git commit -am "feat(hub/m7): GET /api/v1/events SSE (snapshot + seen-projected deltas + heartbeat)"`.

---

## Task B7: Terminal-WS `{t:"state"}` frame (relay refactor)

**Files:**
- Modify: `hubd/internal/api/ws.go`
- Test: extend `ws_test.go`

**Interfaces:**
- Consumes: `Deps.Bcast`, `Deps.Proj`, `Deps.Seen`, the relay’s resolved session name + principal.
- Behavior: while a terminal WS is open, state changes for *that pane’s session* are pushed to the browser as `{"t":"state","state":"<seen-projected>","session":"<name>"}` JSON text frames, interleaved with binary terminal frames. M4 liveness invariants preserved.

- [ ] **Step 1: Resolve the session name** for the relayed pane at open: after a successful agent dial, find which session owns `paneID`. Cheapest correct source: scan `d.Proj.Server(id)` is keyed by session not pane — so instead pull the live tree once (`d.Agent.Sessions(r.Context(), srv, target)`) and find the session whose windows contain `paneID`. Store `sessionName`. If not found, skip the state-frame feature for this connection (still relay terminal bytes).

- [ ] **Step 2: Failing test** — drive `relayPanes` (or a thin testable wrapper) with a fake agent conn + a broadcaster; publish a `Change` for the relay’s session; assert the browser side receives a text frame `{"t":"state",...}` with the seen-projected state, AND that normal binary frames still pass through, AND that ping/pong still works. (You may need to extract the browser-writer mux into a helper that takes the agent-read channel + the broadcaster channel for unit testing; an `httptest`-based end-to-end like the existing `ws_test.go` is also acceptable.)

- [ ] **Step 3: Refactor `relayPanes`** to a single browser-writer:
  - Keep `browser→agent` copy goroutine unchanged (browser is the sole reader of the agent there… actually that goroutine reads browser, writes agent — unchanged).
  - Replace the `agent→browser` copy goroutine with: an agent-reader goroutine that pushes `(messageType, data)` onto a channel; a **single browser-writer** goroutine that `select`s over (a) that channel → write verbatim, (b) a `<-stateCh` of `Change`s → marshal `{t:"state",...}` and write as `websocket.TextMessage`, (c) teardown. Only this goroutine ever calls `browser.WriteMessage` (pings still go via `WriteControl`, which is concurrency-safe).
  - Subscribe to `Bcast` in `PaneRelayHandler`, filter to `(id,target,sessionName)`, apply `SeenProject` with the relay principal’s seen (look up `GetSeen` per change, or snapshot at open like SSE).
  - Preserve: read limits, `armLiveness`, `pingLoop`, both-sides teardown (no leaked goroutine/subprocess). The new agent-reader must also signal `done` on read error so teardown still fires.

- [ ] **Step 4: Run → PASS**, including the existing relay tests (liveness, teardown, transparent passthrough) — they must stay green.

- [ ] **Step 5: Full suite** — `CGO_ENABLED=0 go build ./... && go test ./... -race && go vet ./...`.

- [ ] **Step 6: Commit** — `git commit -am "feat(hub/m7): terminal-WS {t:state} control frame via single browser-writer + broadcaster"`.

---

## Final acceptance gate (after B7)

- [ ] `go test ./... -race` green across `shared`, `agent`, `hubd`.
- [ ] `go vet ./...` clean; `CGO_ENABLED=0 go build ./...` OK.
- [ ] Opus whole-branch review.
- [ ] `/multi-review --codex`; fix everything but nitpicks (re-review each fix).
- [ ] SAFE live acceptance (spec §11): loopback hub on a **copy** of `deploy/data` + throwaway-socket Claude → observe blocked/done/working dots, the `done→done` re-alert, the seen `done→idle`, an SSE stream, and a `{t:"state"}` frame on an open terminal. Prod DB/container/session-0/demo-panes untouched.
- [ ] Write `docs/superpowers/m7-carryover.md` for M8; update milestone memory.

---

## Self-review (plan vs spec)

- **Spec coverage:** §1 transport B → A1–A3,A6; §2 agent surface → A1,A2; §3 poller/ingest/storage/projection → A4,A5,A6; §4 rollup/payload → A7 (+B3 seen); §5 seen → B1,B2,B3; §6 epoch/prune → A6 (epoch re-ingest + `ReplaceServer` prune); §7 SSE+WS+broadcaster → B4,B5,B6,B7; §8 config/wiring → A8,B6; §9 acceptance → final gate; §10 phasing → the A/B split. All covered.
- **Placeholders:** none — every code step has concrete code; test steps that lean on existing harness helpers name the exact behavior to assert and point at the harness to copy.
- **Type consistency:** `shared.PaneState`/`AgentState` (A1) consumed identically by A2/A3/A6; `state.SessionView`/`Projection` (A5) consumed by A6/A7/B3/B6; `db.StateEvent`/`PrincipalSeen` (A4/B1) consumed by B2/B6; `state.Change` (B4) consumed by B5/B6/B7; `state.HubTS` (A6, exported) consumed by B2. `SeenProject(global, latestReceivedAt, seen, ok)` signature identical in B1/B3/B6/B7. `Deps` accreted fields: `Proj` (A7), `Seen` (B2), `Bcast`+`SSEHeartbeat` (B6) — all additive.
- **Known follow-ups deliberately deferred (documented in steps):** SSE/WS use connect-time seen snapshots; `ServersHandler` per-server `GetSeen` is N×M (batchable later); multi-pane sessions’ seen anchor uses the latest *session* event (exact for the single-pane Claude case).
