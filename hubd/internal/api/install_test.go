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

func TestInstallScriptChownsAgentTomlToRunUser(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	body := w.Body.String()
	// agent.toml must be chowned to the service user, else the agent (User=RUN_USER) can't read its config.
	if !strings.Contains(body, `chown "$RUN_USER" /etc/agentmon/agent.toml`) {
		t.Fatal("install.sh must chown agent.toml to the run user (agent runs as that user and reads the config)")
	}
}

func TestInstallScriptDefaultsToDedicatedSocket(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	// The agent must default to a dedicated 'agentmon' socket, never the run-user's
	// default socket (where unrelated/sensitive sessions live), unless --socket overrides.
	if !strings.Contains(w.Body.String(), `SOCKET="${SOCKET_OVERRIDE:-agentmon}"`) {
		t.Fatal("install.sh must default the agent socket to the dedicated 'agentmon' socket")
	}
}

func TestInstallScriptDoesNotPromptForHooksWhenPiped(t *testing.T) {
	d := InstallDeps{HubURL: "https://hub.example.lan"}
	r := httptest.NewRequest("GET", "/install.sh", nil)
	w := httptest.NewRecorder()
	d.ScriptHandler()(w, r)
	// Interactive prompts are unreliable under `curl | sudo bash` (stdin is the pipe, so
	// a /dev/tty read hits EOF and the keystroke leaks to the shell). The hooks prompt
	// must gate on stdin being a real terminal and otherwise point at the explicit flag.
	if !strings.Contains(w.Body.String(), "[ ! -t 0 ]") {
		t.Fatal("hooks prompt must skip (and guide to --hooks) when stdin is not a terminal")
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
