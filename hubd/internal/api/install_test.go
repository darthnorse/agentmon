package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentmon/hubd/internal/agentbin"
)

func TestInstallScriptIsTemplated(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "shellscript") {
		t.Fatalf("content-type: %s", ct)
	}
	body := w.Body.String()
	amd, _ := agentbin.SHA256Hex("amd64")
	arm, _ := agentbin.SHA256Hex("arm64")
	for _, want := range []string{"https://hub.example.lan", amd, arm, "/api/v1/enroll", "agent-linux-"} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh missing %q", want)
		}
	}
	if strings.Contains(body, "{{") {
		t.Fatal("install.sh still contains an unrendered template directive")
	}
}

func TestBinaryHandlerServesBytesAndChecksum(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/dl/agent-linux-amd64", nil)
	r.SetPathValue("file", "agent-linux-amd64")
	w := httptest.NewRecorder()
	d.BinaryHandler()(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	want, _ := agentbin.Binary("amd64")
	if w.Body.Len() != len(want) {
		t.Fatalf("served %d bytes, want %d", w.Body.Len(), len(want))
	}
}

func TestBinaryHandlerRejectsUnknownFile(t *testing.T) {
	d := InstallDeps{HubURL: "x"}
	for _, f := range []string{"agent-linux-sparc", "../../etc/passwd", "install.sh"} {
		r := httptest.NewRequest("GET", "/dl/"+f, nil)
		r.SetPathValue("file", f)
		w := httptest.NewRecorder()
		d.BinaryHandler()(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("file %q: want 404, got %d", f, w.Code)
		}
	}
}
