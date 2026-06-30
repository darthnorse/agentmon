package registry

import (
	"context"
	"database/sql"
	"errors"
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

func (f *fakeStore) SetServerStatus(_ context.Context, id, status string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	s, ok := f.servers[id]
	if !ok {
		return false, nil
	}
	s.Status = status
	f.servers[id] = s
	return true, nil
}

func (f *fakeStore) DeleteServer(_ context.Context, id string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if _, ok := f.servers[id]; !ok {
		return false, nil
	}
	delete(f.servers, id)
	return true, nil
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

func TestGetPropagatesDBError(t *testing.T) {
	r := New(&fakeStore{err: errors.New("db is down")})
	if _, _, err := r.Get(context.Background(), "a"); err == nil {
		t.Fatal("a genuine DB error must propagate, not be masked as not-found")
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

func TestListPendingReturnsOnlyPendingNoSecrets(t *testing.T) {
	r := New(&fakeStore{servers: map[string]db.Server{
		"a": {ID: "a", Status: "active"},
		"p": {ID: "p", Hostname: "web-01", URL: "http://10.0.0.5:8377", Status: "pending", OS: "linux", Arch: "amd64", Bearer: "secret", SigningKey: "secret"},
	}})
	list, err := r.ListPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "p" || list[0].Hostname != "web-01" || list[0].URL != "http://10.0.0.5:8377" || list[0].Arch != "amd64" {
		t.Fatalf("pending list: %+v", list)
	}
	// PendingServer is a secrets-free projection — there is no field to leak Bearer.
}

func TestApproveOnlyPending(t *testing.T) {
	fs := &fakeStore{servers: map[string]db.Server{
		"p": {ID: "p", Hostname: "web-01", Status: "pending"},
		"a": {ID: "a", Status: "active"},
	}}
	r := New(fs)
	s, ok, err := r.Approve(context.Background(), "p")
	if err != nil || !ok || s.Hostname != "web-01" {
		t.Fatalf("approve pending: %+v ok=%v err=%v", s, ok, err)
	}
	if fs.servers["p"].Status != "active" {
		t.Fatalf("status must be active after approve, got %q", fs.servers["p"].Status)
	}
	if _, ok, _ := r.Approve(context.Background(), "a"); ok {
		t.Fatal("an already-active server must NOT be approvable (no resurrect)")
	}
	if _, ok, _ := r.Approve(context.Background(), "missing"); ok {
		t.Fatal("a missing id must report ok=false")
	}
}

func TestRejectOnlyPending(t *testing.T) {
	fs := &fakeStore{servers: map[string]db.Server{
		"p": {ID: "p", Hostname: "web-01", Status: "pending"},
		"a": {ID: "a", Status: "active"},
	}}
	r := New(fs)
	s, ok, err := r.Reject(context.Background(), "p")
	if err != nil || !ok || s.Hostname != "web-01" {
		t.Fatalf("reject pending: %+v ok=%v err=%v", s, ok, err)
	}
	if _, present := fs.servers["p"]; present {
		t.Fatal("a rejected pending server must be deleted")
	}
	if _, ok, _ := r.Reject(context.Background(), "a"); ok {
		t.Fatal("an ACTIVE server must NOT be deletable via reject (safety)")
	}
	if _, present := fs.servers["a"]; !present {
		t.Fatal("the active server must remain after a refused reject")
	}
}

func TestApproveRejectPropagateDBError(t *testing.T) {
	r := New(&fakeStore{err: errors.New("db down")})
	if _, _, err := r.Approve(context.Background(), "p"); err == nil {
		t.Fatal("approve must propagate a genuine DB error")
	}
	if _, _, err := r.Reject(context.Background(), "p"); err == nil {
		t.Fatal("reject must propagate a genuine DB error")
	}
}
