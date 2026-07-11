package report

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmon/agent/internal/config"
)

func testCfg() config.Config {
	return config.Config{Targets: []config.Target{{Label: "default", SocketName: "agentmon"}}}
}

func okResolver(session string) SessionResolver {
	return func(_ context.Context, socket, pane string) (string, error) {
		if socket != "agentmon" || pane != "%5" {
			return "", errors.New("unexpected resolver args")
		}
		return session, nil
	}
}

func intakePost(t *testing.T, st *Store, resolve SessionResolver, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 7, 10, 14, 0, 0, 0, time.UTC) }
	h := IntakeHandler(testCfg(), st, resolve, now)
	r := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	r.Header.Set("X-AgentMon-Pane", "%5")
	r.Header.Set("X-AgentMon-Tmux", "/tmp/tmux-0/agentmon,123,0")
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestIntakeBuffersServerStampedReport(t *testing.T) {
	st := NewStore("i", 10)
	w := intakePost(t, st, okResolver("epic-proj-16"), "/orchestrator/report",
		`{"repo":"o/r","epic":16,"stage":"planning","note":"n"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	b := st.Drain("default", "", 0)
	if len(b.Reports) != 1 {
		t.Fatalf("buffered = %+v", b)
	}
	r := b.Reports[0]
	if r.Session != "epic-proj-16" || r.Ts != "2026-07-10T14:00:00Z" || r.Epic != 16 || r.Note != "n" {
		t.Fatalf("report = %+v", r)
	}
}

func TestIntakeDryRunValidatesWithoutBuffering(t *testing.T) {
	st := NewStore("i", 10)
	w := intakePost(t, st, okResolver("s1"), "/orchestrator/report?dry_run=1",
		`{"repo":"o/r","epic":1,"stage":"planning"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"session":"s1"`) {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	if b := st.Drain("default", "", 0); len(b.Reports) != 0 {
		t.Fatalf("dry_run must not buffer: %+v", b)
	}
}

func TestIntakeRejections(t *testing.T) {
	st := NewStore("i", 10)
	cases := []struct {
		name, body string
		resolve    SessionResolver
	}{
		{"non-reportable stage", `{"repo":"o/r","epic":1,"stage":"merged"}`, okResolver("s")},
		{"empty repo", `{"epic":1,"stage":"planning"}`, okResolver("s")},
		{"pr_open without pr", `{"repo":"o/r","epic":1,"stage":"pr_open"}`, okResolver("s")},
		{"zero epic", `{"repo":"o/r","epic":0,"stage":"planning"}`, okResolver("s")},
		{"bad json", `{`, okResolver("s")},
		{"resolver failure", `{"repo":"o/r","epic":1,"stage":"planning"}`,
			func(_ context.Context, _, _ string) (string, error) { return "", errors.New("no pane") }},
	}
	for _, c := range cases {
		if w := intakePost(t, st, c.resolve, "/orchestrator/report", c.body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: code %d body %s", c.name, w.Code, w.Body)
		}
	}
}

func TestIntakeRejectsUnknownSocketOrBadPane(t *testing.T) {
	st := NewStore("i", 10)
	h := IntakeHandler(testCfg(), st, okResolver("s"), nil)
	r := httptest.NewRequest(http.MethodPost, "/orchestrator/report", strings.NewReader(`{"epic":1,"stage":"planning"}`))
	r.Header.Set("X-AgentMon-Pane", "%5")
	r.Header.Set("X-AgentMon-Tmux", "/tmp/tmux-0/othersock,1,0")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown socket: code %d", w.Code)
	}
	r2 := httptest.NewRequest(http.MethodPost, "/orchestrator/report", strings.NewReader(`{"epic":1,"stage":"planning"}`))
	r2.Header.Set("X-AgentMon-Pane", "not-a-pane")
	r2.Header.Set("X-AgentMon-Tmux", "/tmp/tmux-0/agentmon,1,0")
	w2 := httptest.NewRecorder()
	h(w2, r2)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("bad pane: code %d", w2.Code)
	}
}
