package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandler(t *testing.T) {
	h := HealthHandler("server-a", "test", true)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var body struct {
		OK            bool   `json:"ok"`
		ServerID      string `json:"serverId"`
		Version       string `json:"version"`
		TmuxAvailable bool   `json:"tmuxAvailable"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.ServerID != "server-a" || body.Version != "test" || !body.TmuxAvailable {
		t.Fatalf("bad body: %+v", body)
	}
}
