# Agent Onboarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the manual agent install (hand-write config, generate secrets, edit the hub's `config.yaml`) with a one-command self-registering flow: `curl https://hub/install.sh | sudo bash` enrolls a box as `pending`; `agentmon-hubd server approve <host>` admits it.

**Architecture:** The hub gains a DB-backed dynamic server registry (replacing M3's static `config.yaml` list), three **open** LAN-only rate-limited endpoints (`POST /api/v1/enroll`, `GET /install.sh`, `GET /dl/agent-linux-{amd64,arm64}`), an admin CLI (`agentmon-hubd server list|approve|revoke|rm`), and embedded agent binaries served to the installer. The agent binary is unchanged — this is hub-side + a bash installer.

**Tech Stack:** Go 1.26.4 (pure-Go, `CGO_ENABLED=0`), `modernc.org/sqlite` (WAL), `net/http` ServeMux (Go 1.22 routing), `text/template` for the installer, `//go:embed` for binaries, bash + systemd for the installer.

## Global Constraints

- **Go toolchain:** `GOTOOLCHAIN=auto`, toolchain `go1.26.4` (auto-downloads). Verify any "latest version" claim from a live source (`go.dev/VERSION`, module proxy), never from memory.
- **Build/test paths are per-module, NEVER `./...` at the workspace root:** `go test ./shared/... ./agent/... ./hubd/...`; `CGO_ENABLED=0 go build ./agent/... ./hubd/...`; `CGO_ENABLED=0 go vet ./shared/... ./agent/... ./hubd/...`.
- **`CGO_ENABLED=0` everywhere** — the SQLite driver is pure-Go `modernc.org/sqlite`. No cgo deps may be introduced.
- **Secrets never logged, never in audit rows, never in browser-facing DTOs.** The enroll HTTPS response is the ONLY place a generated secret crosses the wire. `ServerSummary`/`ServerDetail` expose only `id/name/labels/enabled/healthy`.
- **Agent listen port is `8377`** (fixed); the hub derives a dialled server URL as `http://<agent-peer-ip>:8377`.
- **TDD throughout:** failing test first, watch it fail, minimal impl, watch it pass, commit. Frequent commits. DRY, YAGNI.
- **SDD scratch lives in `.superpowers/` (gitignored)**; ledger at `.superpowers/sdd/progress.md`. Work on branch `agent-onboarding`; never commit to `main` until the finish step.
- **Migrations are forward-only and now transactional** (Task 1). The data volume is fresh (nothing deployed), so `0002` may drop+recreate `servers`.
- **Per-task model tiers:** Tasks 1, 5, 6 touch the data-integrity / security boundary (txn migration, the open enroll endpoint, the bash installer + generated secrets) — give those an **opus** quality/security review. Others: sonnet implementer + sonnet reviews are fine.

---

## File structure

**Created:**
- `hubd/internal/db/migrations/0002_enrollment.sql` — drop+recreate `servers` for enrollment.
- `hubd/internal/db/servers.go` — `db.Server` struct + servers repo methods.
- `hubd/internal/db/servers_test.go` — repo CRUD/filter tests.
- `hubd/internal/api/enroll.go` — `POST /api/v1/enroll` handler + `EnrollDeps`.
- `hubd/internal/api/enroll_test.go` — enroll handler tests.
- `hubd/internal/api/install.go` — `GET /install.sh` + `GET /dl/{file}` handlers + `InstallDeps`.
- `hubd/internal/api/install_test.go` — install/download handler tests.
- `hubd/internal/api/install.sh.tmpl` — the templated bash installer (embedded).
- `hubd/internal/api/ratelimit_mw.go` — onboarding IP rate-limit middleware.
- `hubd/internal/agentbin/embed.go` — embeds + checksums the agent binaries.
- `hubd/internal/agentbin/embed_test.go` — embed/checksum tests.
- `hubd/internal/agentbin/bin/agent-linux-amd64` — **placeholder** (real binary baked at image build).
- `hubd/internal/agentbin/bin/agent-linux-arm64` — **placeholder**.
- `hubd/cmd/agentmon-hubd/server_cmd.go` — `server list|approve|revoke|rm` subcommand.
- `hubd/cmd/agentmon-hubd/server_cmd_test.go` — CLI tests.

**Modified:**
- `hubd/internal/db/migrations.go` — wrap each migration file in a transaction.
- `hubd/internal/db/migrations_test.go` (new file, package `db`) — txn rollback test.
- `hubd/internal/registry/registry.go` — `New(store)`, live DB reads, `active`-only filter.
- `hubd/internal/registry/client.go` — dial with `db.Server`.
- `hubd/internal/registry/registry_test.go`, `client_test.go` — fake store / `db.Server`.
- `hubd/internal/api/servers.go`, `sessions.go` — thread `ctx`, handle registry errors, stamp last-seen.
- `hubd/internal/api/servers_test.go`, `integration_test.go` — fake store / real DB rows.
- `hubd/internal/api/router.go` — mount the 3 open endpoints + onboarding limiter.
- `hubd/internal/audit/audit.go` — `server.*` audit methods.
- `hubd/internal/config/config.go`, `config_test.go` — drop `Servers`/`Server`.
- `hubd/cmd/agentmon-hubd/main.go` — `registry.New(db)`, `server` dispatch, wire enroll/install deps.
- `deploy/Dockerfile` — cross-compile both agent arches into the embed dir before building hubd.
- `deploy/hub.config.example.yaml`, `deploy/agent.example.toml`, `deploy/docker-compose.yml` — drop servers block; note auto-managed agent config.
- `.gitignore` — track only the agentbin placeholders.
- `.github/workflows/ci.yml` — embed-placeholder guard + `shellcheck` the installer template.

---

## Task 1: Transactional migrations + the `0002` enrollment migration

**Files:**
- Modify: `hubd/internal/db/migrations.go`
- Create: `hubd/internal/db/migrations/0002_enrollment.sql`
- Create: `hubd/internal/db/migrations_test.go` (package `db`)

**Interfaces:**
- Consumes: existing `migrate(ctx, *sql.DB)` and `migrationFS`.
- Produces: `applyMigration(ctx context.Context, sqldb *sql.DB, name string, body []byte) error` — runs one migration's SQL **and** its `schema_migrations` insert inside a single transaction, rolling back on any error. The `servers` table after `0002` has columns: `id,name,hostname,url,status,bearer,signing_key,labels,os,arch,agent_version,last_seen_at,created_at,updated_at`.

- [ ] **Step 1: Write the failing rollback test**

Create `hubd/internal/db/migrations_test.go`:

```go
package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// applyMigration must be atomic: if the SQL fails partway, NOTHING it created
// survives and schema_migrations is not stamped.
func TestApplyMigrationRollsBackOnError(t *testing.T) {
	sqldb, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "m.sqlite")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if _, err := sqldb.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	// First statement is valid, second is a syntax error → the whole file must roll back.
	body := []byte(`CREATE TABLE good (id TEXT); CREATE TABLE bad (id TEXT) NOPE;`)
	if err := applyMigration(context.Background(), sqldb, "9999_broken.sql", body); err == nil {
		t.Fatal("broken migration must error")
	}
	var n string
	if err := sqldb.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='good'`).Scan(&n); err != sql.ErrNoRows {
		t.Fatalf("table 'good' must not survive a rolled-back migration (err=%v)", err)
	}
	if err := sqldb.QueryRowContext(context.Background(),
		`SELECT name FROM schema_migrations WHERE name='9999_broken.sql'`).Scan(&n); err != sql.ErrNoRows {
		t.Fatalf("rolled-back migration must not be recorded (err=%v)", err)
	}
}

func TestServersTableHasEnrollmentColumns(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cols := map[string]bool{}
	rows, err := d.sql.QueryContext(context.Background(), `PRAGMA table_info(servers)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	for _, want := range []string{"id", "name", "hostname", "url", "status", "bearer", "signing_key", "labels", "os", "arch", "agent_version", "last_seen_at", "created_at", "updated_at"} {
		if !cols[want] {
			t.Fatalf("servers table missing column %q (have %v)", want, cols)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./hubd/internal/db/ -run 'ApplyMigration|ServersTableHas' -v`
Expected: FAIL — `applyMigration` undefined; `servers` lacks the new columns.

- [ ] **Step 3: Add the `0002` migration**

Create `hubd/internal/db/migrations/0002_enrollment.sql`:

```sql
-- 0002: reshape `servers` from the M0 static-config shape to the enrollment shape.
-- The data volume is fresh (nothing deployed), so we drop and recreate. This is the
-- first non-idempotent migration; migrate() now wraps each file in a transaction.
DROP TABLE IF EXISTS servers;
CREATE TABLE servers (
  id            TEXT PRIMARY KEY,                 -- default = hostname
  name          TEXT NOT NULL,                    -- display; default = hostname
  hostname      TEXT NOT NULL,
  url           TEXT NOT NULL,                    -- http://<lan-ip>:8377
  status        TEXT NOT NULL DEFAULT 'pending',  -- pending | active | revoked
  bearer        TEXT NOT NULL,                    -- hub→agent bearer (server-side secret)
  signing_key   TEXT NOT NULL,                    -- HMAC directive key (used by M4)
  labels        TEXT,
  os            TEXT,
  arch          TEXT,
  agent_version TEXT,
  last_seen_at  TEXT,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
```

- [ ] **Step 4: Make `migrate()` transactional**

Replace the body of `hubd/internal/db/migrations.go` (keep the `//go:embed` line and imports; add `database/sql` if not present):

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
// files in schema_migrations. Each file's SQL and its schema_migrations insert
// run together in one transaction (applyMigration), so a failing migration leaves
// no partial schema and is not recorded — safe for non-idempotent migrations.
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
		if err := applyMigration(ctx, sqldb, name, body); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration file's SQL and records it in schema_migrations
// atomically. Any error rolls the whole thing back, so a non-idempotent migration
// never leaves a half-applied schema.
func applyMigration(ctx context.Context, sqldb *sql.DB, name string, body []byte) error {
	tx, err := sqldb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(name, applied_at) VALUES(?, datetime('now'))`, name); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record %s: %w", name, err)
	}
	return tx.Commit()
}
```

- [ ] **Step 5: Run the migration tests + the existing db suite to verify they pass**

Run: `go test ./hubd/internal/db/ -v`
Expected: PASS — including `TestOpenRunsMigrations`, `TestOpenIsIdempotent`, the new rollback + columns tests. (The pre-existing `servers` table check still passes; `0002` recreates it.)

- [ ] **Step 6: Commit**

```bash
git add hubd/internal/db/migrations.go hubd/internal/db/migrations/0002_enrollment.sql hubd/internal/db/migrations_test.go
git commit -m "feat(db): transactional migrate() + 0002 enrollment servers reshape"
```

---

## Task 2: Servers repo (`db.Server` + CRUD)

**Files:**
- Create: `hubd/internal/db/servers.go`
- Create: `hubd/internal/db/servers_test.go`

**Interfaces:**
- Consumes: `db.Server` columns from Task 1's `0002` migration; the `*DB.sql` handle.
- Produces:
  - `type Server struct { ID, Name, Hostname, URL, Status, Bearer, SigningKey string; Labels []string; OS, Arch, AgentVersion, LastSeenAt string }`
  - `func (d *DB) EnrollServer(ctx context.Context, s Server) error` — inserts a row; `created_at`/`updated_at` = `datetime('now')`; `status` taken from `s.Status`.
  - `func (d *DB) GetServer(ctx context.Context, id string) (Server, error)` — `sql.ErrNoRows` when absent.
  - `func (d *DB) FindServer(ctx context.Context, idOrHostname string) (Server, error)` — matches `id` OR `hostname`; `sql.ErrNoRows` when absent.
  - `func (d *DB) ListServers(ctx context.Context, status string) ([]Server, error)` — `status==""` returns all; ordered by `id`.
  - `func (d *DB) SetServerStatus(ctx context.Context, id, status string) (bool, error)` — `false` when no row matched.
  - `func (d *DB) DeleteServer(ctx context.Context, id string) (bool, error)` — `false` when no row matched.
  - `func (d *DB) TouchServerLastSeen(ctx context.Context, id string) error` — sets `last_seen_at = datetime('now')`.

- [ ] **Step 1: Write the failing repo tests**

Create `hubd/internal/db/servers_test.go`:

```go
package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestEnrollAndGetServer(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	in := Server{ID: "web-01", Name: "web-01", Hostname: "web-01",
		URL: "http://10.0.0.9:8377", Status: "pending", Bearer: "b", SigningKey: "k",
		OS: "linux", Arch: "amd64", AgentVersion: "dev"}
	if err := d.EnrollServer(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetServer(ctx, "web-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "web-01" || got.Status != "pending" || got.Bearer != "b" || got.URL != "http://10.0.0.9:8377" || got.Arch != "amd64" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if _, err := d.GetServer(ctx, "nope"); err != sql.ErrNoRows {
		t.Fatalf("missing server: want ErrNoRows, got %v", err)
	}
}

func TestFindServerByHostname(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	if err := d.EnrollServer(ctx, Server{ID: "abc123", Name: "n", Hostname: "db-02", URL: "u", Status: "pending", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.FindServer(ctx, "db-02") // by hostname, not id
	if err != nil || got.ID != "abc123" {
		t.Fatalf("find by hostname: %+v err=%v", got, err)
	}
	got, err = d.FindServer(ctx, "abc123") // by id
	if err != nil || got.Hostname != "db-02" {
		t.Fatalf("find by id: %+v err=%v", got, err)
	}
}

func TestListServersStatusFilter(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	for _, s := range []Server{
		{ID: "a", Name: "a", Hostname: "a", URL: "u", Status: "pending", Bearer: "b", SigningKey: "k"},
		{ID: "b", Name: "b", Hostname: "b", URL: "u", Status: "active", Bearer: "b", SigningKey: "k"},
		{ID: "c", Name: "c", Hostname: "c", URL: "u", Status: "active", Bearer: "b", SigningKey: "k"},
	} {
		if err := d.EnrollServer(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	active, err := d.ListServers(ctx, "active")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[0].ID != "b" || active[1].ID != "c" {
		t.Fatalf("active filter: %+v", active)
	}
	all, err := d.ListServers(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("unfiltered: %+v", all)
	}
}

func TestSetStatusDeleteAndTouch(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	if err := d.EnrollServer(ctx, Server{ID: "a", Name: "a", Hostname: "a", URL: "u", Status: "pending", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	ok, err := d.SetServerStatus(ctx, "a", "active")
	if err != nil || !ok {
		t.Fatalf("set status: ok=%v err=%v", ok, err)
	}
	if got, _ := d.GetServer(ctx, "a"); got.Status != "active" {
		t.Fatalf("status not updated: %+v", got)
	}
	if ok, _ := d.SetServerStatus(ctx, "ghost", "active"); ok {
		t.Fatal("setting status on a missing id must report not-found")
	}
	if err := d.TouchServerLastSeen(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetServer(ctx, "a"); got.LastSeenAt == "" {
		t.Fatal("last_seen_at must be set after touch")
	}
	ok, err = d.DeleteServer(ctx, "a")
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	if _, err := d.GetServer(ctx, "a"); err != sql.ErrNoRows {
		t.Fatalf("deleted server still present: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./hubd/internal/db/ -run 'Server' -v`
Expected: FAIL — `EnrollServer`/`GetServer`/etc. undefined.

- [ ] **Step 3: Implement the repo**

Create `hubd/internal/db/servers.go`:

```go
package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

type Server struct {
	ID           string
	Name         string
	Hostname     string
	URL          string
	Status       string
	Bearer       string
	SigningKey   string
	Labels       []string
	OS           string
	Arch         string
	AgentVersion string
	LastSeenAt   string
}

// marshalLabels stores nil/empty as SQL NULL; otherwise a JSON array.
func marshalLabels(l []string) any {
	if len(l) == 0 {
		return nil
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func unmarshalLabels(s sql.NullString) []string {
	if !s.Valid || s.String == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s.String), &out)
	return out
}

func (d *DB) EnrollServer(ctx context.Context, s Server) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO servers(id, name, hostname, url, status, bearer, signing_key, labels, os, arch, agent_version, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?, datetime('now'), datetime('now'))`,
		s.ID, s.Name, s.Hostname, s.URL, s.Status, s.Bearer, s.SigningKey,
		marshalLabels(s.Labels), s.OS, s.Arch, s.AgentVersion)
	return err
}

func scanServer(row interface{ Scan(...any) error }) (Server, error) {
	var s Server
	var labels sql.NullString
	var os, arch, ver, lastSeen sql.NullString
	if err := row.Scan(&s.ID, &s.Name, &s.Hostname, &s.URL, &s.Status, &s.Bearer,
		&s.SigningKey, &labels, &os, &arch, &ver, &lastSeen); err != nil {
		return Server{}, err
	}
	s.Labels = unmarshalLabels(labels)
	s.OS, s.Arch, s.AgentVersion, s.LastSeenAt = os.String, arch.String, ver.String, lastSeen.String
	return s, nil
}

const serverCols = `id, name, hostname, url, status, bearer, signing_key, labels, os, arch, agent_version, last_seen_at`

func (d *DB) GetServer(ctx context.Context, id string) (Server, error) {
	return scanServer(d.sql.QueryRowContext(ctx,
		`SELECT `+serverCols+` FROM servers WHERE id=?`, id))
}

func (d *DB) FindServer(ctx context.Context, idOrHostname string) (Server, error) {
	return scanServer(d.sql.QueryRowContext(ctx,
		`SELECT `+serverCols+` FROM servers WHERE id=? OR hostname=? ORDER BY id LIMIT 1`,
		idOrHostname, idOrHostname))
}

func (d *DB) ListServers(ctx context.Context, status string) ([]Server, error) {
	q := `SELECT ` + serverCols + ` FROM servers`
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY id`
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) SetServerStatus(ctx context.Context, id, status string) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE servers SET status=?, updated_at=datetime('now') WHERE id=?`, status, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (d *DB) DeleteServer(ctx context.Context, id string) (bool, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM servers WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (d *DB) TouchServerLastSeen(ctx context.Context, id string) error {
	_, err := d.sql.ExecContext(ctx,
		`UPDATE servers SET last_seen_at=datetime('now') WHERE id=?`, id)
	return err
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./hubd/internal/db/ -v`
Expected: PASS — all servers repo tests + the existing db suite.

- [ ] **Step 5: Commit**

```bash
git add hubd/internal/db/servers.go hubd/internal/db/servers_test.go
git commit -m "feat(db): servers repo — enroll/get/find/list/status/delete/touch"
```

---

## Task 3: Dynamic registry reads the DB

**Files:**
- Modify: `hubd/internal/registry/registry.go`, `hubd/internal/registry/client.go`
- Modify: `hubd/internal/registry/registry_test.go`, `hubd/internal/registry/client_test.go`
- Modify: `hubd/internal/api/servers.go`, `hubd/internal/api/sessions.go`
- Modify: `hubd/internal/api/servers_test.go`, `hubd/internal/api/integration_test.go`
- Modify: `hubd/cmd/agentmon-hubd/main.go` (the `registry.New` call + log line only)

**Interfaces:**
- Consumes: `db.Server`, `db.DB.ListServers/GetServer/TouchServerLastSeen` (Task 2).
- Produces:
  - `type Store interface { ListServers(ctx, status string) ([]db.Server, error); GetServer(ctx, id string) (db.Server, error); TouchServerLastSeen(ctx, id string) error }` (`*db.DB` satisfies it).
  - `func New(store Store) *Registry`
  - `func (r *Registry) List(ctx) ([]ServerSummary, error)` — **active only**.
  - `func (r *Registry) Get(ctx, id string) (db.Server, bool, error)` — found-and-**active** → `(srv, true, nil)`; non-active or missing → `(_, false, nil)`; DB error → `(_, false, err)`.
  - `func (r *Registry) TouchLastSeen(ctx, id string) error` — best-effort; delegates to the store.
  - `func (c *Client) Sessions(ctx, srv db.Server, target string) ([]shared.Session, error)`, `func (c *Client) Health(ctx, srv db.Server) bool` — dial with `srv.URL` + `srv.Bearer`, stamp `srv.ID`.

- [ ] **Step 1: Rewrite the registry tests (failing)**

Replace `hubd/internal/registry/registry_test.go`:

```go
package registry

import (
	"context"
	"database/sql"
	"testing"

	"agentmon/hubd/internal/db"
)

// fakeStore is an in-memory registry.Store for tests.
type fakeStore struct {
	servers map[string]db.Server
	err     error
	touched []string
}

func (f *fakeStore) ListServers(_ context.Context, status string) ([]db.Server, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []db.Server
	for _, s := range f.servers {
		if status == "" || s.Status == status {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeStore) GetServer(_ context.Context, id string) (db.Server, error) {
	if f.err != nil {
		return db.Server{}, f.err
	}
	s, ok := f.servers[id]
	if !ok {
		return db.Server{}, sql.ErrNoRows // mirrors *db.DB.GetServer's missing-row error
	}
	return s, nil
}

func (f *fakeStore) TouchServerLastSeen(_ context.Context, id string) error {
	f.touched = append(f.touched, id)
	return nil
}

func TestListReturnsOnlyActive(t *testing.T) {
	r := New(&fakeStore{servers: map[string]db.Server{
		"a": {ID: "a", Name: "A", Status: "active", Labels: []string{"prod"}},
		"b": {ID: "b", Name: "B", Status: "pending"},
		"c": {ID: "c", Name: "C", Status: "revoked"},
	}})
	list, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "a" || !list[0].Enabled {
		t.Fatalf("list must contain only active: %+v", list)
	}
	if list[0].Labels == nil {
		t.Fatal("nil labels must normalize to empty slice")
	}
}

func TestGetActiveOnly(t *testing.T) {
	r := New(&fakeStore{servers: map[string]db.Server{
		"a": {ID: "a", Status: "active", Bearer: "tok"},
		"p": {ID: "p", Status: "pending"},
	}})
	srv, ok, err := r.Get(context.Background(), "a")
	if err != nil || !ok || srv.Bearer != "tok" {
		t.Fatalf("active get: %+v ok=%v err=%v", srv, ok, err)
	}
	if _, ok, _ := r.Get(context.Background(), "p"); ok {
		t.Fatal("pending server must not be found by the registry")
	}
	if _, ok, _ := r.Get(context.Background(), "missing"); ok {
		t.Fatal("missing server must not be found")
	}
}
```

Replace the `config.Server`/`Token` usages in `hubd/internal/registry/client_test.go` with `db.Server`/`Bearer` (import `agentmon/hubd/internal/db`, drop the `config` import):

```go
	srv := db.Server{ID: "server-a", URL: ts.URL, Bearer: "tok-a"}
```
(and likewise `db.Server{ID: "server-a", URL: ts.URL, Bearer: "WRONG"}`, `db.Server{ID: "s", URL: ts.URL, Bearer: "t"}`, `db.Server{URL: ts.URL}`).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./hubd/internal/registry/ -v`
Expected: FAIL — `New(*fakeStore)` signature mismatch, `db.Server` not accepted by client.

- [ ] **Step 3: Rewrite `registry.go`**

Replace `hubd/internal/registry/registry.go`:

```go
// Package registry holds the DB-backed server list and dials agents. db.Server
// (URL + bearer) is hub-side only; List/ServerSummary are the browser-safe
// projections (no secrets). The registry reads the DB live on every lookup, so a
// CLI approve/revoke/rm (a separate process on the shared WAL DB) takes effect on
// the running hub without a restart.
package registry

import (
	"context"
	"database/sql"
	"errors"

	"agentmon/hubd/internal/db"
)

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

// Store is the subset of *db.DB the registry needs. Defined here so the registry
// is unit-testable with a fake.
type Store interface {
	ListServers(ctx context.Context, status string) ([]db.Server, error)
	GetServer(ctx context.Context, id string) (db.Server, error)
	TouchServerLastSeen(ctx context.Context, id string) error
}

type Registry struct{ store Store }

func New(store Store) *Registry { return &Registry{store: store} }

// LabelsOrEmpty returns l unchanged if non-nil, or an empty slice to avoid
// marshalling a JSON null for servers with no labels.
func LabelsOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// List returns browser-safe summaries for ACTIVE servers only.
func (r *Registry) List(ctx context.Context) ([]ServerSummary, error) {
	servers, err := r.store.ListServers(ctx, "active")
	if err != nil {
		return nil, err
	}
	out := make([]ServerSummary, 0, len(servers))
	for _, s := range servers {
		out = append(out, ServerSummary{ID: s.ID, Name: s.Name, Labels: LabelsOrEmpty(s.Labels), Enabled: true})
	}
	return out, nil
}

// Get returns an ACTIVE server by id. (srv,true,nil) when found and active;
// (_,false,nil) when missing or not active; (_,false,err) on a genuine DB error.
func (r *Registry) Get(ctx context.Context, id string) (db.Server, bool, error) {
	s, err := r.store.GetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Server{}, false, nil // no such row → not found, not an error
	}
	if err != nil {
		return db.Server{}, false, err // genuine DB failure → surface as 500
	}
	if s.Status != "active" {
		return db.Server{}, false, nil // pending/revoked → invisible to the API
	}
	return s, true, nil
}

// TouchLastSeen records a successful hub→agent dial. Best-effort.
func (r *Registry) TouchLastSeen(ctx context.Context, id string) error {
	return r.store.TouchServerLastSeen(ctx, id)
}
```

> Note: `Get` maps `sql.ErrNoRows` and non-active status to not-found `(_,false,nil)` (→ 404), but surfaces a genuine DB error `(_,false,err)` (→ 500) rather than masking infrastructure failure as a 404. `List` likewise propagates store errors. This matches the spec's "Get of a non-active id → not found (404)" without swallowing real failures.

- [ ] **Step 4: Update `client.go` to dial with `db.Server`**

In `hubd/internal/registry/client.go`: change the import `agentmon/hubd/internal/config` → `agentmon/hubd/internal/db`, and the two method signatures + field reads:

```go
func (c *Client) Sessions(ctx context.Context, srv db.Server, target string) ([]shared.Session, error) {
	u := srv.URL + "/sessions"
	if target != "" {
		u += "?target=" + url.QueryEscape(target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+srv.Bearer)
	// ... unchanged: Do, status check, decode, stamp srv.ID ...
}

func (c *Client) Health(ctx context.Context, srv db.Server) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/healthz", nil)
	// ... unchanged ...
}
```

- [ ] **Step 5: Update the API handlers to thread `ctx` + handle errors**

In `hubd/internal/api/servers.go`, `ServersHandler`:

```go
func (d Deps) ServersHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.authorizeOr403(w, r, authz.ServerView, "server:*"); !ok {
			return
		}
		list, err := d.Reg.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}
```

`ServerHandler` — replace the `srv, ok := d.Reg.Get(id)` block:

```go
		srv, ok, err := d.Reg.Get(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown server")
			return
		}
```

In `hubd/internal/api/sessions.go`, both handlers: replace `srv, ok := d.Reg.Get(id)` with the same `srv, ok, err := d.Reg.Get(r.Context(), id)` 3-value block (500 on err, 404 on !ok). In `ServerSessionsHandler`, after a successful `d.Agent.Sessions(...)` and before `writeJSON`, stamp last-seen best-effort:

```go
		_ = d.Reg.TouchLastSeen(r.Context(), id)
		writeJSON(w, http.StatusOK, sessions)
```

- [ ] **Step 6: Update the API tests to use a fake store / real DB rows**

`hubd/internal/api/servers_test.go` — add a local fake store and use it (drop the `config` import; add `db` + `context`):

```go
type fakeStore struct{ servers map[string]db.Server }

func (f fakeStore) ListServers(_ context.Context, status string) ([]db.Server, error) {
	var out []db.Server
	for _, s := range f.servers {
		if status == "" || s.Status == status {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f fakeStore) GetServer(_ context.Context, id string) (db.Server, error) {
	if s, ok := f.servers[id]; ok {
		return s, nil
	}
	return db.Server{}, sql.ErrNoRows
}
func (f fakeStore) TouchServerLastSeen(_ context.Context, _ string) error { return nil }
```

Update the three tests:
- `TestServersHandlerListsForAuthedPrincipal`: `reg := registry.New(fakeStore{servers: map[string]db.Server{"server-a": {ID: "server-a", Name: "A", Status: "active", URL: "http://x"}}})`.
- `TestServersHandlerDeniesEmptyPrincipal`: `testDeps(registry.New(fakeStore{}))`.
- `TestServerHandlerUnknownIDIs404`: `testDeps(registry.New(fakeStore{}))`.

(Imports: add `"context"`, `"database/sql"`, `"agentmon/hubd/internal/db"`; remove `"agentmon/hubd/internal/config"`.)

`hubd/internal/api/integration_test.go` — in `buildHub`, replace the registry construction. Use the **real** `*db.DB` (already opened as `d`) as the store, and enroll+activate `server-a` so the registry sees it:

```go
	if err := d.EnrollServer(context.Background(), db.Server{
		ID: "server-a", Name: "A", Hostname: "server-a", URL: agentURL,
		Status: "active", Bearer: agentToken, SigningKey: "k",
	}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New(d)
```
(Drop the `config` import from `integration_test.go`.)

- [ ] **Step 7: Update `main.go`'s registry construction (keep the module building)**

In `hubd/cmd/agentmon-hubd/main.go`: change `reg := registry.New(cfg.Servers)` → `reg := registry.New(database)`, and drop the server count from the listen log line (the registry no longer knows a static count at boot):

```go
	log.Printf("agentmon-hubd %s listening on %s", version, cfg.Listen)
```

> `cfg.Servers` is now unused by the registry but still parsed by config — that's removed in Task 8. Leaving it here keeps `go build ./hubd/...` green.

- [ ] **Step 8: Run the registry + api tests and the static build**

Run:
```
go test ./hubd/internal/registry/ ./hubd/internal/api/ -v
CGO_ENABLED=0 go build ./hubd/...
```
Expected: PASS; build succeeds.

- [ ] **Step 9: Commit**

```bash
git add hubd/internal/registry/ hubd/internal/api/servers.go hubd/internal/api/sessions.go hubd/internal/api/servers_test.go hubd/internal/api/integration_test.go hubd/cmd/agentmon-hubd/main.go
git commit -m "refactor(registry): read the DB live (active-only), dial with db.Server"
```

---

## Task 4: Audit recorder `server.*` events

**Files:**
- Modify: `hubd/internal/audit/audit.go`
- Modify: `hubd/internal/audit/audit_test.go`

**Interfaces:**
- Consumes: existing `Recorder.write` + `db.AuditEntry`.
- Produces:
  - `func (r *Recorder) ServerEnroll(ctx context.Context, id, hostname, ip string)` — `action=server.enroll`, `resource=server:<id>`, `result=allow`, `ip`, `meta=hostname`. No secrets.
  - `func (r *Recorder) ServerApprove(ctx context.Context, id, hostname string)` — `action=server.approve`.
  - `func (r *Recorder) ServerRevoke(ctx context.Context, id, hostname string)` — `action=server.revoke`.
  - `func (r *Recorder) ServerRemove(ctx context.Context, id, hostname string)` — `action=server.remove`.

- [ ] **Step 1: Write the failing test**

Add to `hubd/internal/audit/audit_test.go` (a capturing sink — reuse the existing one in that file if present, else add):

```go
func TestServerLifecycleAudits(t *testing.T) {
	cap := &captureSink{}
	r := NewRecorder(cap)
	ctx := context.Background()
	r.ServerEnroll(ctx, "web-01", "web-01.lan", "10.0.0.9")
	r.ServerApprove(ctx, "web-01", "web-01.lan")
	r.ServerRevoke(ctx, "web-01", "web-01.lan")
	r.ServerRemove(ctx, "web-01", "web-01.lan")
	if len(cap.entries) != 4 {
		t.Fatalf("want 4 audit rows, got %d", len(cap.entries))
	}
	enroll := cap.entries[0]
	if enroll.Action != "server.enroll" || enroll.Resource != "server:web-01" ||
		enroll.Result != "allow" || enroll.IP != "10.0.0.9" || enroll.Meta != "web-01.lan" {
		t.Fatalf("enroll row: %+v", enroll)
	}
	if cap.entries[1].Action != "server.approve" || cap.entries[2].Action != "server.revoke" || cap.entries[3].Action != "server.remove" {
		t.Fatalf("lifecycle actions: %+v", cap.entries)
	}
	// No secret material may appear anywhere in the rows.
	for _, e := range cap.entries {
		if e.Meta == "" {
			t.Fatalf("hostname meta missing: %+v", e)
		}
	}
}
```

If `captureSink` does not already exist in the test file, add:

```go
type captureSink struct{ entries []db.AuditEntry }

func (c *captureSink) Append(_ context.Context, e db.AuditEntry) error {
	c.entries = append(c.entries, e)
	return nil
}
```
(Ensure imports include `context`, `testing`, `agentmon/hubd/internal/db`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./hubd/internal/audit/ -run ServerLifecycle -v`
Expected: FAIL — `ServerEnroll` undefined.

- [ ] **Step 3: Implement the methods**

Add to `hubd/internal/audit/audit.go`:

```go
func (r *Recorder) ServerEnroll(ctx context.Context, id, hostname, ip string) {
	r.write(ctx, db.AuditEntry{Action: "server.enroll",
		Resource: "server:" + id, Result: "allow", IP: ip, Meta: hostname})
}

func (r *Recorder) ServerApprove(ctx context.Context, id, hostname string) {
	r.write(ctx, db.AuditEntry{Action: "server.approve",
		Resource: "server:" + id, Result: "allow", Meta: hostname})
}

func (r *Recorder) ServerRevoke(ctx context.Context, id, hostname string) {
	r.write(ctx, db.AuditEntry{Action: "server.revoke",
		Resource: "server:" + id, Result: "allow", Meta: hostname})
}

func (r *Recorder) ServerRemove(ctx context.Context, id, hostname string) {
	r.write(ctx, db.AuditEntry{Action: "server.remove",
		Resource: "server:" + id, Result: "allow", Meta: hostname})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./hubd/internal/audit/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add hubd/internal/audit/audit.go hubd/internal/audit/audit_test.go
git commit -m "feat(audit): server.enroll/approve/revoke/remove events"
```

---

## Task 5: Enroll endpoint (`POST /api/v1/enroll`) + onboarding rate-limit

**Files:**
- Create: `hubd/internal/api/enroll.go`, `hubd/internal/api/enroll_test.go`
- Create: `hubd/internal/api/ratelimit_mw.go`
- Modify: `hubd/internal/api/router.go`
- Modify: `hubd/cmd/agentmon-hubd/main.go`

**Interfaces:**
- Consumes: `db.Server`, `db.DB.GetServer/EnrollServer` (Tasks 2), `audit.Recorder.ServerEnroll` (Task 4), `authn.Limiter`, `authn.ClientIP`.
- Produces:
  - `type EnrollStore interface { GetServer(ctx, id string) (db.Server, error); EnrollServer(ctx, db.Server) error }` (`*db.DB` satisfies).
  - `type EnrollDeps struct { Servers EnrollStore; Audit *audit.Recorder; TrustForwardedProto bool }`
  - `func (e EnrollDeps) Handler() http.HandlerFunc`
  - request `enrollReq{Hostname, OS, Arch, AgentVersion string; Target struct{OSUser, Socket, Label string}}`; response `enrollResp{ServerID, Bearer, SigningKey string}`.
  - `func onboardRateLimit(l *authn.Limiter, trustForwardedProto bool, next http.Handler) http.Handler` (in `ratelimit_mw.go`) — 429 (`{"error":"too many attempts"}`) when the per-IP window is exhausted, else records the request and proceeds.

- [ ] **Step 1: Write the failing enroll tests**

Create `hubd/internal/api/enroll_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/db"
)

// memEnrollStore is an in-memory EnrollStore.
type memEnrollStore struct{ servers map[string]db.Server }

func (m *memEnrollStore) GetServer(_ context.Context, id string) (db.Server, error) {
	if s, ok := m.servers[id]; ok {
		return s, nil
	}
	return db.Server{}, errNoRow
}
func (m *memEnrollStore) EnrollServer(_ context.Context, s db.Server) error {
	m.servers[s.ID] = s
	return nil
}

func enrollDeps() (EnrollDeps, *memEnrollStore) {
	st := &memEnrollStore{servers: map[string]db.Server{}}
	return EnrollDeps{Servers: st, Audit: audit.NewRecorder(nopSink{})}, st
}

func TestEnrollCreatesPendingAndReturnsCreds(t *testing.T) {
	d, st := enrollDeps()
	body := `{"hostname":"web-01","os":"linux","arch":"amd64","agentVersion":"dev","target":{"osUser":"dev","label":"default"}}`
	r := httptest.NewRequest("POST", "/api/v1/enroll", strings.NewReader(body))
	r.RemoteAddr = "10.0.0.9:54000"
	w := httptest.NewRecorder()
	d.Handler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	var resp enrollResp
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.ServerID != "web-01" || resp.Bearer == "" || resp.SigningKey == "" {
		t.Fatalf("resp: %+v", resp)
	}
	if resp.Bearer == resp.SigningKey {
		t.Fatal("bearer and signing key must be independently generated")
	}
	got := st.servers["web-01"]
	if got.Status != "pending" || got.Bearer != resp.Bearer || got.URL != "http://10.0.0.9:8377" || got.Arch != "amd64" {
		t.Fatalf("stored row: %+v", got)
	}
}

func TestEnrollDuplicateIs409(t *testing.T) {
	d, st := enrollDeps()
	st.servers["web-01"] = db.Server{ID: "web-01", Status: "pending"}
	r := httptest.NewRequest("POST", "/api/v1/enroll", strings.NewReader(`{"hostname":"web-01","arch":"amd64"}`))
	r.RemoteAddr = "10.0.0.9:1"
	w := httptest.NewRecorder()
	d.Handler()(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("dup enroll: want 409, got %d", w.Code)
	}
}

func TestEnrollBadBodyIs400(t *testing.T) {
	d, _ := enrollDeps()
	for _, body := range []string{`{not json`, `{"hostname":"","arch":"amd64"}`, `{"hostname":"web-01","arch":"sparc"}`} {
		r := httptest.NewRequest("POST", "/api/v1/enroll", strings.NewReader(body))
		r.RemoteAddr = "10.0.0.9:1"
		w := httptest.NewRecorder()
		d.Handler()(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %q: want 400, got %d", body, w.Code)
		}
	}
}

func TestOnboardRateLimitReturns429(t *testing.T) {
	l := authn.NewLimiter(2, time.Minute)
	called := 0
	h := onboardRateLimit(l, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(200)
	}))
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/install.sh", nil)
		r.RemoteAddr = "10.0.0.9:1"
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("attempt %d: want 200, got %d", i, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/install.sh", nil)
	r.RemoteAddr = "10.0.0.9:1"
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd attempt: want 429, got %d", w.Code)
	}
	if called != 2 {
		t.Fatalf("rate-limited request must not reach the handler: called=%d", called)
	}
}
```

Add a shared `errNoRow` sentinel to the test package (top of `enroll_test.go`, since multiple in-package fakes use it):

```go
import "errors"

var errNoRow = errors.New("no rows")
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./hubd/internal/api/ -run 'Enroll|OnboardRate' -v`
Expected: FAIL — `EnrollDeps`, `enrollResp`, `onboardRateLimit` undefined.

- [ ] **Step 3: Implement the rate-limit middleware**

Create `hubd/internal/api/ratelimit_mw.go`:

```go
package api

import (
	"net/http"

	"agentmon/hubd/internal/authn"
)

// onboardRateLimit caps the rate of the open onboarding endpoints per client IP.
// Unlike the login limiter (which counts failures), this records EVERY request,
// so the sliding window bounds total onboarding traffic from one IP.
func onboardRateLimit(l *authn.Limiter, trustForwardedProto bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := authn.ClientIP(r, trustForwardedProto)
		if !l.Allowed(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		l.Fail(ip) // record this request in the window
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Implement the enroll handler**

Create `hubd/internal/api/enroll.go`:

```go
package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"regexp"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/db"
)

const agentPort = "8377"

var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$`)

// EnrollStore is the DB surface the enroll handler needs.
type EnrollStore interface {
	GetServer(ctx context.Context, id string) (db.Server, error)
	EnrollServer(ctx context.Context, s db.Server) error
}

type EnrollDeps struct {
	Servers             EnrollStore
	Audit               *audit.Recorder
	TrustForwardedProto bool
}

type enrollReq struct {
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agentVersion"`
	Target       struct {
		OSUser string `json:"osUser"`
		Socket string `json:"socket"`
		Label  string `json:"label"`
	} `json:"target"`
}

type enrollResp struct {
	ServerID   string `json:"serverId"`
	Bearer     string `json:"bearer"`
	SigningKey string `json:"signingKey"`
}

// Handler is open (no RequireAuth); it is mounted behind the onboarding
// rate-limiter. It records a pending server and returns generated credentials.
func (e EnrollDeps) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req enrollReq
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if !hostnameRe.MatchString(req.Hostname) {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.Arch != "amd64" && req.Arch != "arm64" {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		id := req.Hostname // default id = hostname

		// Duplicate id → 409 (operator must revoke + rm, or pass --hostname).
		if _, err := e.Servers.GetServer(r.Context(), id); err == nil {
			writeJSONError(w, http.StatusConflict, "already enrolled; revoke + rm first, or pass --hostname")
			return
		}

		bearer, err := genSecret()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		signingKey, err := genSecret()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}

		// The dialled URL is the agent's direct peer IP (the box that enrolled) +
		// the fixed agent port. net.JoinHostPort brackets IPv6 literals.
		peer := r.RemoteAddr
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			peer = host
		}
		url := "http://" + net.JoinHostPort(peer, agentPort)

		srv := db.Server{
			ID: id, Name: req.Hostname, Hostname: req.Hostname, URL: url,
			Status: "pending", Bearer: bearer, SigningKey: signingKey,
			OS: req.OS, Arch: req.Arch, AgentVersion: req.AgentVersion,
		}
		if err := e.Servers.EnrollServer(r.Context(), srv); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		e.Audit.ServerEnroll(r.Context(), id, req.Hostname, authn.ClientIP(r, e.TrustForwardedProto))
		writeJSON(w, http.StatusOK, enrollResp{ServerID: id, Bearer: bearer, SigningKey: signingKey})
	}
}

// genSecret returns 32 bytes of CSPRNG as base64url (no padding).
func genSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./hubd/internal/api/ -run 'Enroll|OnboardRate' -v`
Expected: PASS.

- [ ] **Step 6: Wire the route + main**

In `hubd/internal/api/router.go`: add fields to `RouterDeps` and mount the open route. Add to the struct:

```go
	Enroll  EnrollDeps
	Install InstallDeps // (defined in Task 6; add this field now and pass a zero value until then)
	Onboard *authn.Limiter
```
> If implementing Task 5 before Task 6, temporarily add only `Enroll` and `Onboard`; add `Install` and its routes in Task 6. Keep `RouterDeps` compiling at each step.

Mount in `NewRouter`, before `mux.Handle("/", rd.WebUI)`:

```go
	mux.Handle("POST /api/v1/enroll", onboardRateLimit(rd.Onboard, rd.TrustForwardedProto, rd.Enroll.Handler()))
```

In `hubd/cmd/agentmon-hubd/main.go`: construct the onboarding limiter and pass the deps. After `rec := audit.NewRecorder(database)`:

```go
	onboard := authn.NewLimiter(enrollMax(cfg), enrollWindow(cfg))
```
Add to the `api.NewRouter(api.RouterDeps{...})` literal:

```go
		Enroll:  api.EnrollDeps{Servers: database, Audit: rec, TrustForwardedProto: cfg.TrustForwardedProto},
		Onboard: onboard,
```
Add helper functions to `main.go`:

```go
func enrollMax(cfg config.Config) int {
	if cfg.EnrollRateLimit.MaxAttempts > 0 {
		return cfg.EnrollRateLimit.MaxAttempts
	}
	return 30
}

func enrollWindow(cfg config.Config) time.Duration {
	if cfg.EnrollRateLimit.Window > 0 {
		return cfg.EnrollRateLimit.Window
	}
	return time.Minute
}
```

Add the config field in `hubd/internal/config/config.go` `Config` struct:

```go
	EnrollRateLimit RateLimitCfg `yaml:"enroll_rate_limit"`
```

- [ ] **Step 7: Run the api suite + static build**

Run:
```
go test ./hubd/internal/api/ ./hubd/internal/config/ -v
CGO_ENABLED=0 go build ./hubd/...
```
Expected: PASS; build succeeds.

- [ ] **Step 8: Commit**

```bash
git add hubd/internal/api/enroll.go hubd/internal/api/enroll_test.go hubd/internal/api/ratelimit_mw.go hubd/internal/api/router.go hubd/internal/config/config.go hubd/cmd/agentmon-hubd/main.go
git commit -m "feat(api): open rate-limited POST /api/v1/enroll → pending server + creds"
```

---

## Task 6: Installer hosting (`GET /install.sh`, `GET /dl/...`) + embedded agent binaries

**Files:**
- Create: `hubd/internal/agentbin/embed.go`, `hubd/internal/agentbin/embed_test.go`
- Create: `hubd/internal/agentbin/bin/agent-linux-amd64`, `.../agent-linux-arm64` (placeholders)
- Create: `hubd/internal/api/install.go`, `hubd/internal/api/install_test.go`, `hubd/internal/api/install.sh.tmpl`
- Modify: `hubd/internal/api/router.go`, `hubd/cmd/agentmon-hubd/main.go`, `.gitignore`

**Interfaces:**
- Consumes: nothing from earlier tasks except the router/main wiring slots from Task 5.
- Produces:
  - `agentbin.Binary(arch string) ([]byte, bool)` — embedded bytes for `amd64`/`arm64`.
  - `agentbin.SHA256Hex(arch string) (string, bool)` — hex sha256 of the embedded bytes (computed once).
  - `type InstallDeps struct { HubURL string }`
  - `func (d InstallDeps) ScriptHandler() http.HandlerFunc` — `GET /install.sh`, `text/x-shellscript`, templated with `HubURL` + both checksums.
  - `func (d InstallDeps) BinaryHandler() http.HandlerFunc` — `GET /dl/{file}`, serves the embedded binary for `agent-linux-amd64`/`agent-linux-arm64`, 404 otherwise.

- [ ] **Step 1: Create placeholder binaries + the embed package (build-gated by `//go:embed`)**

Create `hubd/internal/agentbin/bin/agent-linux-amd64` with content:
```
AGENTMON_AGENT_PLACEHOLDER amd64 — replaced by the real cross-compiled binary at image build.
```
Create `hubd/internal/agentbin/bin/agent-linux-arm64` with content:
```
AGENTMON_AGENT_PLACEHOLDER arm64 — replaced by the real cross-compiled binary at image build.
```

Create `hubd/internal/agentbin/embed.go`:

```go
// Package agentbin embeds the cross-compiled agent binaries the hub serves to the
// installer. In CI/unit builds these are tiny placeholders; the Docker image build
// overwrites them with the real static binaries before compiling hubd, so the
// served bytes and their advertised sha256 always match what the image ships.
package agentbin

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
)

//go:embed bin/agent-linux-amd64 bin/agent-linux-arm64
var files embed.FS

var paths = map[string]string{
	"amd64": "bin/agent-linux-amd64",
	"arm64": "bin/agent-linux-arm64",
}

var sums = func() map[string]string {
	m := map[string]string{}
	for arch, p := range paths {
		b, err := files.ReadFile(p)
		if err != nil {
			panic(err)
		}
		h := sha256.Sum256(b)
		m[arch] = hex.EncodeToString(h[:])
	}
	return m
}()

func Binary(arch string) ([]byte, bool) {
	p, ok := paths[arch]
	if !ok {
		return nil, false
	}
	b, err := files.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return b, true
}

func SHA256Hex(arch string) (string, bool) {
	s, ok := sums[arch]
	return s, ok
}
```

Create `hubd/internal/agentbin/embed_test.go`:

```go
package agentbin

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestBinaryAndChecksumMatch(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		b, ok := Binary(arch)
		if !ok || len(b) == 0 {
			t.Fatalf("%s: no embedded bytes", arch)
		}
		want := sha256.Sum256(b)
		got, ok := SHA256Hex(arch)
		if !ok || got != hex.EncodeToString(want[:]) {
			t.Fatalf("%s: checksum mismatch got=%s", arch, got)
		}
	}
	if _, ok := Binary("sparc"); ok {
		t.Fatal("unknown arch must not resolve")
	}
}
```

- [ ] **Step 2: Run to verify the embed package passes**

Run: `go test ./hubd/internal/agentbin/ -v`
Expected: PASS.

- [ ] **Step 3: Write the installer template**

Create `hubd/internal/api/install.sh.tmpl` (every template directive sits inside a double-quoted bash assignment, so `shellcheck` parses the raw template):

```bash
#!/usr/bin/env bash
# shellcheck shell=bash
# AgentMon agent installer — served by the hub, templated with its own URL + checksums.
# Usage: curl <hub>/install.sh | sudo bash [-s -- --hostname=H --user=U --socket=S --dry-run]
set -euo pipefail

HUB_URL="{{.HubURL}}"
SHA256_AMD64="{{.SHA256AMD64}}"
SHA256_ARM64="{{.SHA256ARM64}}"
AGENT_PORT="8377"

HOSTNAME_OVERRIDE=""
USER_OVERRIDE=""
SOCKET_OVERRIDE=""
DRY_RUN="0"
for arg in "$@"; do
  case "$arg" in
    --hostname=*) HOSTNAME_OVERRIDE="${arg#*=}" ;;
    --user=*)     USER_OVERRIDE="${arg#*=}" ;;
    --socket=*)   SOCKET_OVERRIDE="${arg#*=}" ;;
    --dry-run)    DRY_RUN="1" ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

die() { echo "error: $*" >&2; exit 1; }

command -v systemctl >/dev/null 2>&1 || die "systemd (systemctl) is required; this installer does not support non-systemd hosts"
command -v curl >/dev/null 2>&1 || die "curl is required"

ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
  x86_64|amd64) ARCH="amd64"; SHA256_EXPECT="$SHA256_AMD64" ;;
  aarch64|arm64) ARCH="arm64"; SHA256_EXPECT="$SHA256_ARM64" ;;
  *) die "unsupported architecture: $ARCH_RAW (only amd64/arm64)" ;;
esac

HOST="${HOSTNAME_OVERRIDE:-$(hostname -s)}"
RUN_USER="${USER_OVERRIDE:-${SUDO_USER:-$(id -un)}}"
SOCKET="${SOCKET_OVERRIDE:-}"

echo "AgentMon installer:"
echo "  hub:      $HUB_URL"
echo "  hostname: $HOST"
echo "  run-as:   $RUN_USER"
echo "  arch:     $ARCH"

if [ "$DRY_RUN" = "1" ]; then
  echo "[dry-run] would download $HUB_URL/dl/agent-linux-$ARCH (sha256 $SHA256_EXPECT)"
  echo "[dry-run] would enroll at $HUB_URL/api/v1/enroll as '$HOST'"
  echo "[dry-run] would write /etc/agentmon/agent.toml + secrets, install + start the systemd unit"
  echo "[dry-run] no changes made."
  exit 0
fi

[ "$(id -u)" = "0" ] || die "must run as root (use sudo)"

TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
echo "Downloading agent ($ARCH)..."
curl -fsSL "$HUB_URL/dl/agent-linux-$ARCH" -o "$TMP"
GOT_SHA="$(sha256sum "$TMP" | cut -d' ' -f1)"
[ "$GOT_SHA" = "$SHA256_EXPECT" ] || die "checksum mismatch (got $GOT_SHA, want $SHA256_EXPECT)"
install -m 0755 "$TMP" /usr/local/bin/agentmon-agent

echo "Enrolling..."
REQ="$(printf '{"hostname":"%s","os":"linux","arch":"%s","agentVersion":"installed","target":{"osUser":"%s","socket":"%s","label":"default"}}' "$HOST" "$ARCH" "$RUN_USER" "$SOCKET")"
RESP="$(curl -fsS -X POST -H 'Content-Type: application/json' -d "$REQ" "$HUB_URL/api/v1/enroll")" || die "enroll request failed"

extract() { printf '%s' "$RESP" | grep -o "\"$1\":\"[^\"]*\"" | head -n1 | cut -d'"' -f4; }
SERVER_ID="$(extract serverId)"
BEARER="$(extract bearer)"
SIGNING_KEY="$(extract signingKey)"
[ -n "$SERVER_ID" ] && [ -n "$BEARER" ] && [ -n "$SIGNING_KEY" ] || die "enroll response missing fields: $RESP"

install -d -m 0755 /etc/agentmon
umask 077
printf '%s' "$BEARER" > /etc/agentmon/hub_token
printf '%s' "$SIGNING_KEY" > /etc/agentmon/signing_key
cat > /etc/agentmon/agent.toml <<EOF
listen = "0.0.0.0:$AGENT_PORT"
server_id = "$SERVER_ID"
hub_token = "file:/etc/agentmon/hub_token"
directive_key = "file:/etc/agentmon/signing_key"
scrollback_lines = 5000
[[targets]]
  os_user = "$RUN_USER"
  socket_name = "$SOCKET"
  label = "default"
EOF
chown "$RUN_USER" /etc/agentmon/hub_token /etc/agentmon/signing_key
chmod 600 /etc/agentmon/hub_token /etc/agentmon/signing_key

cat > /etc/systemd/system/agentmon-agent.service <<EOF
[Unit]
Description=AgentMon agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$RUN_USER
ExecStart=/usr/local/bin/agentmon-agent --config /etc/agentmon/agent.toml
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now agentmon-agent

echo "Smoke-testing the agent..."
ok="0"
for _ in 1 2 3 4 5; do
  if curl -fsS "http://127.0.0.1:$AGENT_PORT/healthz" >/dev/null 2>&1; then ok="1"; break; fi
  sleep 1
done
[ "$ok" = "1" ] || die "agent did not become healthy; check: journalctl -u agentmon-agent"

echo "✓ $HOST enrolled — pending approval. Run 'agentmon-hubd server approve $HOST' on the hub."
```

- [ ] **Step 4: Write the failing install handler tests**

Create `hubd/internal/api/install_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentmon/hubd/internal/agentbin"
)

func TestInstallScriptIsTemplated(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "shellscript") {
		t.Fatalf("content-type: %s", ct)
	}
	body := w.Body.String()
	amd, _ := agentbin.SHA256Hex("amd64")
	arm, _ := agentbin.SHA256Hex("arm64")
	for _, want := range []string{"https://hub.example.lan", amd, arm, "/api/v1/enroll", "agent-linux-"} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh missing %q", want)
		}
	}
	if strings.Contains(body, "{{") {
		t.Fatal("install.sh still contains an unrendered template directive")
	}
}

func TestBinaryHandlerServesBytesAndChecksum(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/dl/agent-linux-amd64", nil)
	r.SetPathValue("file", "agent-linux-amd64")
	w := httptest.NewRecorder()
	d.BinaryHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	want, _ := agentbin.Binary("amd64")
	if w.Body.Len() != len(want) {
		t.Fatalf("served %d bytes, want %d", w.Body.Len(), len(want))
	}
}

func TestBinaryHandlerRejectsUnknownFile(t *testing.T) {
	d := InstallDeps{HubURL: "x"}
	for _, f := range []string{"agent-linux-sparc", "../../etc/passwd", "install.sh"} {
		r := httptest.NewRequest("GET", "/dl/"+f, nil)
		r.SetPathValue("file", f)
		w := httptest.NewRecorder()
		d.BinaryHandler()(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("file %q: want 404, got %d", f, w.Code)
		}
	}
}
```

- [ ] **Step 5: Run to verify it fails**

Run: `go test ./hubd/internal/api/ -run 'Install|BinaryHandler' -v`
Expected: FAIL — `InstallDeps` undefined.

- [ ] **Step 6: Implement the install handlers**

Create `hubd/internal/api/install.go`:

```go
package api

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"
	"text/template"

	"agentmon/hubd/internal/agentbin"
)

//go:embed install.sh.tmpl
var installScriptTmpl string

var installTmpl = template.Must(template.New("install").Parse(installScriptTmpl))

type InstallDeps struct {
	HubURL string
}

type installData struct {
	HubURL      string
	SHA256AMD64 string
	SHA256ARM64 string
}

func (d InstallDeps) ScriptHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		amd, _ := agentbin.SHA256Hex("amd64")
		arm, _ := agentbin.SHA256Hex("arm64")
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		if err := installTmpl.Execute(w, installData{HubURL: strings.TrimRight(d.HubURL, "/"), SHA256AMD64: amd, SHA256ARM64: arm}); err != nil {
			// Header already sent on partial write; nothing more to do but log upstream.
			return
		}
	}
}

func (d InstallDeps) BinaryHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		arch := strings.TrimPrefix(file, "agent-linux-")
		if arch == file { // prefix not present
			http.NotFound(w, r)
			return
		}
		b, ok := agentbin.Binary(arch)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Header().Set("Content-Disposition", `attachment; filename="agentmon-agent"`)
		_, _ = w.Write(b)
	}
}
```

- [ ] **Step 7: Run to verify it passes**

Run: `go test ./hubd/internal/api/ -run 'Install|BinaryHandler' -v`
Expected: PASS.

- [ ] **Step 8: Wire routes + main**

In `hubd/internal/api/router.go` `NewRouter`, add (before the WebUI catch-all):

```go
	mux.Handle("GET /install.sh", onboardRateLimit(rd.Onboard, rd.TrustForwardedProto, rd.Install.ScriptHandler()))
	mux.Handle("GET /dl/{file}", onboardRateLimit(rd.Onboard, rd.TrustForwardedProto, rd.Install.BinaryHandler()))
```
(The `Install InstallDeps` field was added to `RouterDeps` in Task 5; if you deferred it, add it now.)

In `hubd/cmd/agentmon-hubd/main.go`, add to the `api.RouterDeps{...}` literal:

```go
		Install: api.InstallDeps{HubURL: cfg.ExternalOrigin},
```

- [ ] **Step 9: Track only the placeholders in `.gitignore`**

Append to `.gitignore` (mirrors the SPA placeholder rule):

```
# Embedded agent binaries: only the placeholders are tracked; the real
# cross-compiled binaries are baked into the image at build time.
/hubd/internal/agentbin/bin/*
!/hubd/internal/agentbin/bin/agent-linux-amd64
!/hubd/internal/agentbin/bin/agent-linux-arm64
```

- [ ] **Step 10: Run the api suite + static build, and force-add placeholders**

Run:
```
go test ./hubd/internal/api/ ./hubd/internal/agentbin/ -v
CGO_ENABLED=0 go build ./hubd/...
git add -f hubd/internal/agentbin/bin/agent-linux-amd64 hubd/internal/agentbin/bin/agent-linux-arm64
```
Expected: PASS; build succeeds; both placeholders staged (the ignore rule's `!` exceptions should let a plain `git add` work too, but `-f` is explicit).

- [ ] **Step 11: Commit**

```bash
git add hubd/internal/agentbin/embed.go hubd/internal/agentbin/embed_test.go \
        hubd/internal/api/install.go hubd/internal/api/install_test.go hubd/internal/api/install.sh.tmpl \
        hubd/internal/api/router.go hubd/cmd/agentmon-hubd/main.go .gitignore
git commit -m "feat(api): serve templated /install.sh + embedded agent binaries via /dl"
```

---

## Task 7: Admin CLI — `agentmon-hubd server list|approve|revoke|rm`

**Files:**
- Create: `hubd/cmd/agentmon-hubd/server_cmd.go`, `hubd/cmd/agentmon-hubd/server_cmd_test.go`
- Modify: `hubd/cmd/agentmon-hubd/main.go` (dispatch `server`)

**Interfaces:**
- Consumes: `db.DB.FindServer/ListServers/SetServerStatus/DeleteServer` (Task 2), `audit.Recorder.ServerApprove/Revoke/Remove` (Task 4), `config.Load`/`openDB` (existing in `main.go`).
- Produces: `func runServerCmd(args []string) error` — dispatches `list|approve|revoke|rm`; and a testable core `func serverAction(ctx, d serverCmdStore, rec serverAuditor, action, idOrHostname string) (string, error)`.

- [ ] **Step 1: Write the failing CLI-core test**

Create `hubd/cmd/agentmon-hubd/server_cmd_test.go`:

```go
package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/db"
)

func TestServerActionApproveRevokeRemove(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := d.EnrollServer(ctx, db.Server{ID: "web-01", Name: "web-01", Hostname: "web-01.lan",
		URL: "u", Status: "pending", Bearer: "b", SigningKey: "k"}); err != nil {
		t.Fatal(err)
	}
	rec := audit.NewRecorder(d)

	if _, err := serverAction(ctx, d, rec, "approve", "web-01.lan"); err != nil { // resolve by hostname
		t.Fatal(err)
	}
	if got, _ := d.GetServer(ctx, "web-01"); got.Status != "active" {
		t.Fatalf("approve did not activate: %+v", got)
	}
	if _, err := serverAction(ctx, d, rec, "revoke", "web-01"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetServer(ctx, "web-01"); got.Status != "revoked" {
		t.Fatalf("revoke did not set revoked: %+v", got)
	}
	if _, err := serverAction(ctx, d, rec, "rm", "web-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetServer(ctx, "web-01"); err == nil {
		t.Fatal("rm did not delete the row")
	}
	// audited
	rows, _ := d.Recent(ctx, 50)
	var approve, revoke, remove bool
	for _, e := range rows {
		switch e.Action {
		case "server.approve":
			approve = true
		case "server.revoke":
			revoke = true
		case "server.remove":
			remove = true
		}
	}
	if !approve || !revoke || !remove {
		t.Fatalf("lifecycle not audited: approve=%v revoke=%v remove=%v", approve, revoke, remove)
	}
}

func TestServerActionUnknownTarget(t *testing.T) {
	d, _ := db.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	defer d.Close()
	rec := audit.NewRecorder(d)
	_, err := serverAction(context.Background(), d, rec, "approve", "ghost")
	if err == nil || !strings.Contains(err.Error(), "no server") {
		t.Fatalf("want no-server error, got %v", err)
	}
}

func TestServerListRenders(t *testing.T) {
	d, _ := db.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	defer d.Close()
	ctx := context.Background()
	d.EnrollServer(ctx, db.Server{ID: "web-01", Name: "web-01", Hostname: "web-01.lan", URL: "u", Status: "pending", Bearer: "b", SigningKey: "k", OS: "linux", Arch: "amd64", AgentVersion: "dev"})
	out, err := serverList(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"web-01", "pending", "amd64"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./hubd/cmd/agentmon-hubd/ -run Server -v`
Expected: FAIL — `serverAction`/`serverList` undefined.

- [ ] **Step 3: Implement the CLI core + dispatch**

Create `hubd/cmd/agentmon-hubd/server_cmd.go`:

```go
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
)

// serverCmdStore is the DB surface the CLI needs (satisfied by *db.DB).
type serverCmdStore interface {
	FindServer(ctx context.Context, idOrHostname string) (db.Server, error)
	ListServers(ctx context.Context, status string) ([]db.Server, error)
	SetServerStatus(ctx context.Context, id, status string) (bool, error)
	DeleteServer(ctx context.Context, id string) (bool, error)
}

// serverAuditor is the audit surface (satisfied by *audit.Recorder).
type serverAuditor interface {
	ServerApprove(ctx context.Context, id, hostname string)
	ServerRevoke(ctx context.Context, id, hostname string)
	ServerRemove(ctx context.Context, id, hostname string)
}

// serverAction approves/revokes/removes a server, resolving id-or-hostname, and
// audits. Returns a human message.
func serverAction(ctx context.Context, d serverCmdStore, rec serverAuditor, action, idOrHostname string) (string, error) {
	srv, err := d.FindServer(ctx, idOrHostname)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no server matching %q", idOrHostname)
	}
	if err != nil {
		return "", err
	}
	switch action {
	case "approve":
		if _, err := d.SetServerStatus(ctx, srv.ID, "active"); err != nil {
			return "", err
		}
		rec.ServerApprove(ctx, srv.ID, srv.Hostname)
		return fmt.Sprintf("approved %s — now active and dialable", srv.ID), nil
	case "revoke":
		if _, err := d.SetServerStatus(ctx, srv.ID, "revoked"); err != nil {
			return "", err
		}
		rec.ServerRevoke(ctx, srv.ID, srv.Hostname)
		return fmt.Sprintf("revoked %s — hub will stop dialing it", srv.ID), nil
	case "rm":
		if _, err := d.DeleteServer(ctx, srv.ID); err != nil {
			return "", err
		}
		rec.ServerRemove(ctx, srv.ID, srv.Hostname)
		return fmt.Sprintf("removed %s", srv.ID), nil
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
}

// serverList renders the full server table.
func serverList(ctx context.Context, d serverCmdStore) (string, error) {
	servers, err := d.ListServers(ctx, "")
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tHOSTNAME\tSTATUS\tOS/ARCH\tVERSION\tLAST-SEEN")
	for _, s := range servers {
		last := s.LastSeenAt
		if last == "" {
			last = "never"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s/%s\t%s\t%s\n",
			s.ID, s.Name, s.Hostname, s.Status, s.OS, s.Arch, s.AgentVersion, last)
	}
	tw.Flush()
	return sb.String(), nil
}

// runServerCmd implements: agentmon-hubd server list|approve|revoke|rm [<id|hostname>] [--config <path>]
func runServerCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: agentmon-hubd server <list|approve|revoke|rm> [<id|hostname>]")
	}
	sub := args[0]
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	cfgPath := fs.String("config", "/data/config.yaml", "path to config.yaml")
	fs.Parse(args[1:]) //nolint:errcheck // ExitOnError never returns

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	database, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer database.Close()
	ctx := context.Background()
	rec := audit.NewRecorder(database)

	if sub == "list" {
		out, err := serverList(ctx, database)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: agentmon-hubd server %s <id|hostname>", sub)
	}
	switch sub {
	case "approve", "revoke", "rm":
		msg, err := serverAction(ctx, database, rec, sub, rest[0])
		if err != nil {
			return err
		}
		fmt.Println(msg)
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q (want list|approve|revoke|rm)", sub)
	}
}
```

> Note the flag-then-positional ordering: `--config` is parsed off the front, and the `<id|hostname>` is read from `fs.Args()`. Operators invoke as `agentmon-hubd server approve web-01` or `agentmon-hubd server approve --config /data/config.yaml web-01`.

In `hubd/cmd/agentmon-hubd/main.go`, add a dispatch beside the existing `user` block at the top of `main()`:

```go
	if len(os.Args) > 1 && os.Args[1] == "server" {
		if err := runServerCmd(os.Args[2:]); err != nil {
			log.Fatalf("server: %v", err)
		}
		return
	}
```

- [ ] **Step 4: Run to verify it passes + static build**

Run:
```
go test ./hubd/cmd/agentmon-hubd/ -v
CGO_ENABLED=0 go build ./hubd/...
```
Expected: PASS; build succeeds.

- [ ] **Step 5: Commit**

```bash
git add hubd/cmd/agentmon-hubd/server_cmd.go hubd/cmd/agentmon-hubd/server_cmd_test.go hubd/cmd/agentmon-hubd/main.go
git commit -m "feat(cli): agentmon-hubd server list|approve|revoke|rm"
```

---

## Task 8: Config cleanup + deploy (Dockerfile, CI guard, examples)

**Files:**
- Modify: `hubd/internal/config/config.go`, `hubd/internal/config/config_test.go`
- Modify: `deploy/Dockerfile`, `deploy/hub.config.example.yaml`, `deploy/agent.example.toml`, `deploy/docker-compose.yml`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: nothing new.
- Produces: a `config.Config` with no `Servers`/`Server` (the registry is fully DB-driven); a hub image that bakes both real agent binaries into the embed dir before building hubd; CI guards for the placeholders + `shellcheck`.

- [ ] **Step 1: Rewrite the config tests (failing)**

Replace `hubd/internal/config/config_test.go` entirely (the server-secret-resolution tests no longer apply):

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadServerlessConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
listen: "127.0.0.1:8080"
external_origin: "https://agentmon.lan"
trust_forwarded_proto: true
data_dir: "/data"
session_cookie: { name: "agentmon_session", ttl: "168h" }
login_rate_limit: { max_attempts: 5, window: "15m" }
enroll_rate_limit: { max_attempts: 30, window: "1m" }
`), 0o600)

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExternalOrigin != "https://agentmon.lan" || !cfg.TrustForwardedProto {
		t.Fatalf("bad cfg: %+v", cfg)
	}
	if cfg.SessionCookie.Name != "agentmon_session" || cfg.SessionCookie.TTL != 168*time.Hour {
		t.Fatalf("cookie: %+v", cfg.SessionCookie)
	}
	if cfg.EnrollRateLimit.MaxAttempts != 30 || cfg.EnrollRateLimit.Window != time.Minute {
		t.Fatalf("enroll rate limit: %+v", cfg.EnrollRateLimit)
	}
}

func TestLoadDefaultsCookieName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`listen: "127.0.0.1:8080"`+"\n"), 0o600)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionCookie.Name != "agentmon_session" {
		t.Fatalf("cookie name default not applied: %q", cfg.SessionCookie.Name)
	}
}
```

- [ ] **Step 2: Run to verify it fails to compile (old refs gone)**

Run: `go test ./hubd/internal/config/ -v`
Expected: FAIL — the old tests referenced removed fields, and the new tests can't yet rely on the cleaned loader (still has the `Servers` resolution loop). Proceed to clean the loader.

- [ ] **Step 3: Remove `Servers`/`Server` from the config loader**

In `hubd/internal/config/config.go`:
- Delete the entire `Server` struct.
- Delete the `Servers []Server` field from `Config`.
- Delete the per-server resolution loop in `Load` (the `for i := range c.Servers { ... }` block).
- Remove the now-unused `agentmon/shared` import.

The cleaned `config.go`:

```go
package config

import (
	"fmt"
	"os"
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

type Config struct {
	Listen              string       `yaml:"listen"`
	ExternalOrigin      string       `yaml:"external_origin"`
	TrustForwardedProto bool         `yaml:"trust_forwarded_proto"`
	DataDir             string       `yaml:"data_dir"`
	SessionCookie       CookieCfg    `yaml:"session_cookie"`
	LoginRateLimit      RateLimitCfg `yaml:"login_rate_limit"`
	EnrollRateLimit     RateLimitCfg `yaml:"enroll_rate_limit"`
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
	return c, nil
}
```

- [ ] **Step 4: Run the config tests + the whole hub module build/vet**

Run:
```
go test ./hubd/internal/config/ -v
CGO_ENABLED=0 go build ./hubd/...
CGO_ENABLED=0 go vet ./hubd/...
```
Expected: PASS; build + vet clean. (Confirms nothing else still imports `config.Server`.)

- [ ] **Step 5: Update the deploy examples**

Replace `deploy/hub.config.example.yaml`:

```yaml
listen: "127.0.0.1:8080"
external_origin: "https://agentmon.example.lan"
trust_forwarded_proto: true
data_dir: "/data"
session_cookie: { name: "agentmon_session", ttl: "168h" }
login_rate_limit: { max_attempts: 5, window: "15m" }
enroll_rate_limit: { max_attempts: 30, window: "1m" }
# Servers are no longer listed here — they self-register via `curl <hub>/install.sh | sudo bash`
# and are admitted with `agentmon-hubd server approve <hostname>`.
```

Replace `deploy/agent.example.toml` with a note that it is auto-generated by the installer:

```toml
# NOTE: agent.toml is normally written by the hub-served installer
# (curl <hub>/install.sh | sudo bash); it generates server_id + secret files for you.
# This example documents the shape for manual installs only.
listen = "0.0.0.0:8377"
server_id = "web-01"
hub_token = "file:/etc/agentmon/hub_token"
directive_key = "file:/etc/agentmon/signing_key"
scrollback_lines = 5000
[[targets]]
  os_user = "dev"
  socket_name = ""
  label = "default"
```

Add a comment to `deploy/docker-compose.yml` after the `volumes:` mapping line documenting the approval step:

```yaml
    # After `docker compose up`, enroll each box with:
    #   curl <external_origin>/install.sh | sudo bash
    # then admit it from the hub host:
    #   docker compose exec agentmon-hub /agentmon-hubd server approve <hostname>
```

- [ ] **Step 6: Bake both agent arches into the hub image**

Edit `deploy/Dockerfile` — add an agent cross-compile stage and copy both binaries into the embed dir before the hubd build. Insert after the `web` stage and modify the `hubd` stage:

```dockerfile
# ---- Stage 1b: cross-compile both agent binaries (static, pure-Go) ----
FROM golang:1.26-alpine AS agentbin
WORKDIR /src
COPY . /src
WORKDIR /src/agent
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
    -o /agents/agent-linux-amd64 ./cmd/agentmon-agent
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" \
    -o /agents/agent-linux-arm64 ./cmd/agentmon-agent

# ---- Stage 2: build hubd with the SPA + agent binaries embedded ----
FROM golang:1.26-alpine AS hubd
WORKDIR /src
COPY . /src
COPY --from=web /web/dist /src/hubd/internal/webui/dist
COPY --from=agentbin /agents/agent-linux-amd64 /src/hubd/internal/agentbin/bin/agent-linux-amd64
COPY --from=agentbin /agents/agent-linux-arm64 /src/hubd/internal/agentbin/bin/agent-linux-arm64
WORKDIR /src/hubd
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/agentmon-hubd ./cmd/agentmon-hubd
```
(Leave Stage 3 unchanged.)

- [ ] **Step 7: Add CI guards (placeholder + shellcheck)**

Edit `.github/workflows/ci.yml` — in the `go` job, after the existing "Embed placeholder must not be a built SPA" step, add:

```yaml
      - name: Embedded agent binaries must be placeholders (not real binaries)
        run: |
          for f in hubd/internal/agentbin/bin/agent-linux-amd64 hubd/internal/agentbin/bin/agent-linux-arm64; do
            if ! grep -q 'AGENTMON_AGENT_PLACEHOLDER' "$f"; then
              echo "ERROR: $f is not the tracked placeholder — a real binary was committed"; exit 1
            fi
          done
      - name: Shellcheck the installer template
        run: |
          sudo apt-get update && sudo apt-get install -y shellcheck
          shellcheck hubd/internal/api/install.sh.tmpl
```

- [ ] **Step 8: Verify shellcheck locally (or note for CI)**

Run (if `shellcheck` is available locally; otherwise this is the CI gate):
```
shellcheck hubd/internal/api/install.sh.tmpl
```
Expected: clean (no warnings). If `shellcheck` flags the `extract()`/`grep -o` line, address per its suggestion while keeping the template directives inside quoted assignments.

- [ ] **Step 9: Full per-module suite + vet**

Run:
```
go test ./shared/... ./agent/... ./hubd/...
CGO_ENABLED=0 go build ./agent/... ./hubd/...
CGO_ENABLED=0 go vet ./shared/... ./agent/... ./hubd/...
```
Expected: all green.

- [ ] **Step 10: Commit**

```bash
git add hubd/internal/config/config.go hubd/internal/config/config_test.go \
        deploy/Dockerfile deploy/hub.config.example.yaml deploy/agent.example.toml deploy/docker-compose.yml \
        .github/workflows/ci.yml
git commit -m "chore: drop static servers config; bake agent binaries into image; CI guards"
```

---

## Whole-milestone verification (after Task 8)

Run all of these and confirm green before the review gate:

- [ ] `go test ./shared/... ./agent/... ./hubd/...` — full unit + httptest suite.
- [ ] `CGO_ENABLED=0 go build ./agent/... ./hubd/...` — static build.
- [ ] `CGO_ENABLED=0 go vet ./shared/... ./agent/... ./hubd/...` — vet clean.
- [ ] `go test -race ./hubd/internal/api/ ./hubd/internal/authn/ ./hubd/internal/db/` — race check on the concurrent surfaces (rate-limiter, DB, handlers).
- [ ] `shellcheck hubd/internal/api/install.sh.tmpl` — installer clean.
- [ ] `docker build -f deploy/Dockerfile -t agentmon-hubd:onboarding .` — image builds with both agent binaries embedded (real binaries pass the checksum self-consistency).
- [ ] **Manual smoke** (optional, dev box): run `/install.sh` rendering via a temporary hub process, and `--dry-run` the script to confirm it prints a plan and makes no changes.

## Spec coverage self-check (done while writing — recorded for the executor)

- Open enroll (§4.2, §5.1): Task 5 — pending row, generated creds, audit, 409 dup, 400 bad body, 429 rate-limit.
- DB-backed registry, active-only, live reads (§2.2, §4.1): Task 3.
- `0002` reshape + transactional migrate (§5.2): Task 1.
- Installer + binary hosting (§4.3): Task 6.
- Admin CLI (§4.4): Task 7.
- Router/auth wiring, open endpoints behind limiter (§4.5): Tasks 5 + 6.
- Config drops servers (§5.3): Task 8.
- Browser-safe projections unchanged (§5.4): preserved in Task 3 (`ServerSummary`/`ServerDetail`).
- Security model — secrets never logged/audited/projected; sha256 binary integrity (§6): Tasks 5 (genSecret, no-secret audit), 6 (checksum self-consistency).
- Dockerfile embeds both arches; CI guards (§4.3, testing §7): Task 8.
- Open question resolutions baked in: systemd detect-and-error (§9.1 → installer step 3), `last_seen_at` stamped on successful dial (§9.2 → Task 3 step 5), binary size accepted (§9.3 → Task 8).
- Live two-server acceptance (§7, §8) — **Patrik's to run**; not a code task.
```
