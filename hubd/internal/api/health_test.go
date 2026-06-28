package api

import (
	"encoding/json"
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
	var body map[string]any
	if err := json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, _ := body["version"].(string); v == "" {
		t.Fatalf("expected non-empty version, got %+v", body)
	}
}
