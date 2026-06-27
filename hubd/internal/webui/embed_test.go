package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSPAFallbackServesIndexForDeepLink(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	// A nested client route that doesn't exist as a file must return index.html.
	resp, err := http.Get(srv.URL + "/servers/server-a/sessions/foo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "AgentMon") {
		t.Fatalf("deep link did not return index.html: %q", body)
	}
}

func TestSPAFallbackDoesNotSwallowAPIPaths(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/v1/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown /api path, got %d", resp.StatusCode)
	}
}

func TestSPAFallbackDoesNotSwallowBareAPIPath(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for bare /api, got %d", resp.StatusCode)
	}
}
