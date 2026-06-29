package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmon/agent/internal/state"
	"agentmon/shared"
)

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
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", w.Code)
	}
}
