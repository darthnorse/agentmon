package api

import (
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
}
