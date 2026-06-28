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
