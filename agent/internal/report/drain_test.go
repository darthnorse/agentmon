package report

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentmon/shared"
)

func drainGet(t *testing.T, st *Store, url string) *httptest.ResponseRecorder {
	t.Helper()
	h := DrainHandler(testCfg(), st)
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func TestDrainHandlerAckThenReturnRemainder(t *testing.T) {
	st := NewStore("inst", 10)
	st.Add("default", rep(1))
	st.Add("default", rep(2))

	w := drainGet(t, st, "/orchestrator/reports?target=default")
	var b shared.OrchestratorReportBatch
	if err := json.NewDecoder(w.Body).Decode(&b); err != nil || w.Code != 200 {
		t.Fatalf("code %d err %v", w.Code, err)
	}
	if b.Instance != "inst" || b.Cursor != 2 || len(b.Reports) != 2 {
		t.Fatalf("batch = %+v", b)
	}

	st.Add("default", rep(3))
	w2 := drainGet(t, st, "/orchestrator/reports?target=default&instance=inst&ack=2")
	var b2 shared.OrchestratorReportBatch
	_ = json.NewDecoder(w2.Body).Decode(&b2)
	if len(b2.Reports) != 1 || b2.Reports[0].Epic != 3 || b2.Cursor != 3 {
		t.Fatalf("post-ack batch = %+v", b2)
	}
}

func TestDrainHandlerEmptyIsJSONArrayNotNull(t *testing.T) {
	st := NewStore("inst", 10)
	w := drainGet(t, st, "/orchestrator/reports")
	if !strings.Contains(w.Body.String(), `"reports":[]`) {
		t.Fatalf("empty drain must encode []: %s", w.Body)
	}
}

func TestDrainHandlerErrors(t *testing.T) {
	st := NewStore("inst", 10)
	if w := drainGet(t, st, "/orchestrator/reports?target=nope"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown target: code %d", w.Code)
	}
	if w := drainGet(t, st, "/orchestrator/reports?ack=banana"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad ack: code %d", w.Code)
	}
}
