package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func passThrough() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func doReq(h http.Handler, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestRequireBearerAllowsCorrectToken(t *testing.T) {
	rr := doReq(RequireBearer("s3cret", passThrough()), "Bearer s3cret")
	if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
		t.Fatalf("code=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestRequireBearerRejects(t *testing.T) {
	cases := map[string]string{
		"missing header":   "",
		"wrong scheme":     "Token s3cret",
		"wrong token":      "Bearer nope",
		"empty bearer":     "Bearer ",
		"no space":         "Bearers3cret",
		"prefix-only match": "Bearer s3cretXX",
	}
	for name, auth := range cases {
		t.Run(name, func(t *testing.T) {
			rr := doReq(RequireBearer("s3cret", passThrough()), auth)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("auth=%q → code %d, want 401", auth, rr.Code)
			}
			if rr.Body.String() == "ok" {
				t.Fatal("next handler should not have run")
			}
		})
	}
}
