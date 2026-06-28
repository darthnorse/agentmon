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
